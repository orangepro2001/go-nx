package remote

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"sync"

	"github.com/quic-go/quic-go"

	"go-nx/internal/proto"
	"go-nx/internal/tile"
	"go-nx/internal/trust"
)

const maxFrameBytes = 64 << 20

// Client is the controlling side of a session.
type Client struct {
	conn *quic.Conn
	ctrl *quic.Stream
	wmu  sync.Mutex
}

// Dial connects to a host, presenting id and pinning the host's fingerprint in
// hosts. password may be empty; the host only asks for it if this PC is not paired.
func Dial(ctx context.Context, addr string, id tls.Certificate, hosts *trust.Store, password string) (*Client, error) {
	cfg, pin := clientTLS(id, hostOf(addr), hosts)
	conn, err := quic.DialAddr(ctx, addr, cfg, quicConfig())
	if err != nil {
		return nil, err
	}
	// Persist a first-seen host fingerprint now that the handshake is done, so
	// disk I/O never sits on the connection's critical path. A failed write only
	// means the next connection re-pins, so it must not fail this one.
	if err := pin.commit(hosts); err != nil {
		log.Printf("pin host %s: %v", hostOf(addr), err)
	}
	ctrl, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(0, "")
		return nil, err
	}
	if err := proto.WriteHello(ctrl, password); err != nil {
		conn.CloseWithError(0, "")
		return nil, err
	}
	return &Client{conn: conn, ctrl: ctrl}, nil
}

func (c *Client) Close() { c.conn.CloseWithError(0, "") }

// SendInput sends one user action.
func (c *Client) SendInput(in proto.Input) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return proto.WriteInput(c.ctrl, in)
}

// Run receives screen updates and receipts until the connection ends.
// onFrame is called synchronously after each update is applied to the frame it
// is given, with the rectangles that update changed; the frame must not be
// retained or used after onFrame returns.
// onReceipt may be nil.
func (c *Client) Run(ctx context.Context, onFrame func(*tile.Frame, []tile.Rect), onReceipt func(opID uint64, status byte)) error {
	errc := make(chan error, 2)
	go func() {
		for {
			id, st, err := proto.ReadReceipt(c.ctrl)
			if err != nil {
				errc <- err
				return
			}
			if onReceipt != nil {
				onReceipt(id, st)
			}
		}
	}()
	go func() { errc <- c.recvFrames(ctx, onFrame) }()
	return <-errc
}

func (c *Client) recvFrames(ctx context.Context, onFrame func(*tile.Frame, []tile.Rect)) error {
	var frame *tile.Frame
	var lastSeq uint32
	for {
		st, err := c.conn.AcceptUniStream(ctx)
		if err != nil {
			return err
		}
		// Streams are read one at a time in the order the host opened them, so
		// updates are applied in order. A cancelled stream fails here and is skipped.
		data, err := io.ReadAll(io.LimitReader(st, maxFrameBytes))
		if err != nil {
			continue
		}
		hdr, err := proto.ParseFrameHeader(data)
		if err != nil || hdr.Seq <= lastSeq {
			continue
		}
		if frame == nil || frame.W != int(hdr.W) || frame.H != int(hdr.H) {
			frame = tile.NewFrame(int(hdr.W), int(hdr.H))
		}
		dirty, err := tile.Decode(frame, data[proto.FrameHeaderSize:])
		if err != nil {
			return err
		}
		lastSeq = hdr.Seq
		onFrame(frame, dirty)
		c.wmu.Lock()
		err = proto.WriteAck(c.ctrl, hdr.Seq)
		c.wmu.Unlock()
		if err != nil {
			return err
		}
	}
}
