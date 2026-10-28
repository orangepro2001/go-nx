package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// maxFrameBytes bounds one update's stream: the largest frame the protocol allows
// at four bytes a pixel, plus room for tile headers and deflate framing.
const maxFrameBytes = proto.MaxFramePixels*4 + 4<<20

// stallTimeout is how long an update's stream may go without delivering a byte
// before the viewer gives up on it and moves to the next one.
const stallTimeout = 15 * time.Second

var errFrameTooLarge = errors.New("remote: update exceeds the size limit")

// ErrUnsupported is returned for something the host has not said it can do.
var ErrUnsupported = errors.New("remote: the host does not support this")

// HostInfo is what a host says about itself. A v1 host sends it right after it has
// admitted the viewer; a v0 host says nothing, so Version is 0 and Caps is empty.
type HostInfo struct {
	Version int
	Caps    proto.Caps
	W, H    int    // the shared screen when the welcome arrived
	Name    string // the host's display name, if it has one
}

// Client is the controlling side of a session.
type Client struct {
	conn *quic.Conn
	ctrl *quic.Stream
	wmu  sync.Mutex
	cd   codec

	maxFrame int64         // largest update stream accepted, in bytes
	stall    time.Duration // longest an update stream may go silent

	welcome  chan struct{} // closed once host is set
	host     HostInfo
	hostOnce sync.Once
	assumed  atomic.Uint64 // abilities the caller vouches for
}

// DialOptions are the optional parts of a connection.
type DialOptions struct {
	Password string // may be empty; the host only asks for it if this PC is not paired
	Name     string // this PC's display name for a v1 host's lists; default: the computer name
	ForceV0  bool   // speak only the original protocol (tests and diagnostics)
	// WantCursor asks a v1 host to draw the system pointer into the picture, for a
	// viewer that has no pointer of its own on screen (a phone). A viewer that does
	// show its own pointer should leave it off, or it shows two.
	WantCursor bool
}

// Dial connects to a host, presenting id and pinning the host's fingerprint in
// hosts. password may be empty; the host only asks for it if this PC is not paired.
func Dial(ctx context.Context, addr string, id tls.Certificate, hosts *trust.Store, password string) (*Client, error) {
	return DialWith(ctx, addr, id, hosts, DialOptions{Password: password})
}

// DialWith is Dial with options. The control-protocol version is whatever the TLS
// handshake agreed: v1 if the host offers it, otherwise v0.
func DialWith(ctx context.Context, addr string, id tls.Certificate, hosts *trust.Store, o DialOptions) (*Client, error) {
	maxV := proto.Version1
	if o.ForceV0 {
		maxV = 0
	}
	cfg, pin := clientTLSVersions(id, hostOf(addr), hosts, maxV)
	conn, err := quic.DialAddr(ctx, addr, cfg, quicConfig())
	if err != nil {
		return nil, err
	}
	cd, err := codecFor(conn.ConnectionState().TLS.NegotiatedProtocol)
	if err != nil {
		conn.CloseWithError(0, "")
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
	name := o.Name
	if name == "" {
		name = ComputerName()
	}
	hello := proto.HelloV1{Version: proto.Version1, Password: o.Password, Name: name}
	if o.WantCursor {
		hello.Caps |= proto.CapCursor
	}
	if err := cd.writeHello(ctrl, hello); err != nil {
		conn.CloseWithError(0, "")
		return nil, err
	}
	c := &Client{conn: conn, ctrl: ctrl, cd: cd, maxFrame: maxFrameBytes, stall: stallTimeout, welcome: make(chan struct{})}
	if cd.version() == 0 {
		c.setHost(HostInfo{}) // a v0 host never introduces itself
	}
	return c, nil
}

// ComputerName is this PC's name, cut to what the protocol carries.
func ComputerName() string {
	n, err := os.Hostname()
	if err != nil {
		return ""
	}
	for len(n) > proto.MaxNameLen {
		_, size := utf8.DecodeLastRuneInString(n)
		n = n[:len(n)-size]
	}
	return n
}

func (c *Client) setHost(h HostInfo) { c.hostOnce.Do(func() { c.host = h; close(c.welcome) }) }

// Version is the control-protocol version in use: 1 or 0.
func (c *Client) Version() int { return c.cd.version() }

// Host returns what the host said about itself. ok is false until a v1 host's
// welcome has been read, which happens inside Run; for a v0 host it is true at once.
func (c *Client) Host() (HostInfo, bool) {
	select {
	case <-c.welcome:
		return c.host, true
	default:
		return HostInfo{}, false
	}
}

// WaitHost waits for Host to be known. Run must be running for a v1 host.
func (c *Client) WaitHost(ctx context.Context) (HostInfo, error) {
	select {
	case <-c.welcome:
		return c.host, nil
	case <-ctx.Done():
		return HostInfo{}, ctx.Err()
	}
}

// Supports reports whether the host can do what caps names: it said so in its
// welcome, or the caller vouched for it with AssumeCaps.
func (c *Client) Supports(caps proto.Caps) bool {
	have := c.assumed.Load()
	if h, ok := c.Host(); ok {
		have |= uint64(h.Caps)
	}
	return proto.Caps(have).Has(caps)
}

// AssumeCaps records abilities the caller knows the host has although it did not
// say so, as a v0 host never does. Use it only on that knowledge: text sent to a
// v0 host that cannot type ends the session.
func (c *Client) AssumeCaps(caps proto.Caps) {
	for {
		old := c.assumed.Load()
		if c.assumed.CompareAndSwap(old, old|uint64(caps)) {
			return
		}
	}
}

func (c *Client) Close() { c.conn.CloseWithError(0, "") }

// SendInput sends one user action.
func (c *Client) SendInput(in proto.Input) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.cd.writeInput(c.ctrl, in)
}

// SendText asks the host to type text, as literal Unicode and independent of any
// input method active on the host. opID must be non-zero so the result comes
// back as a receipt. Text longer than one message carries (proto.MaxTextLenV1
// bytes on v1, proto.MaxTextLen on v0) is refused: split it at a character
// boundary first.
//
// It returns ErrUnsupported unless the host said it can type, or the caller
// vouched for it with AssumeCaps: a host that predates text ends the session when
// the message arrives. For a v1 host the welcome must have been read, so call
// WaitHost first.
func (c *Client) SendText(opID uint64, text string) error {
	if opID == 0 {
		return errors.New("remote: text needs a non-zero op id to be acknowledged")
	}
	if !c.Supports(proto.CapText) {
		return ErrUnsupported
	}
	if len(text) > c.cd.maxText() {
		return errors.New("remote: text is longer than one message carries")
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.cd.writeText(c.ctrl, opID, text)
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
			m, err := c.cd.readHostMsg(c.ctrl)
			if err != nil {
				errc <- err
				return
			}
			switch m.Type {
			case proto.V1Welcome:
				w := m.Welcome
				c.setHost(HostInfo{Version: int(w.Version), Caps: w.Caps, W: int(w.W), H: int(w.H), Name: w.Name})
			case proto.V1Receipt:
				if onReceipt != nil {
					onReceipt(m.OpID, m.Status)
				}
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
		data, err := io.ReadAll(io.LimitReader(stallReader{st, c.stall}, c.maxFrame+1))
		if err != nil {
			st.CancelRead(0) // tell the host to stop sending it
			continue
		}
		if int64(len(data)) > c.maxFrame {
			st.CancelRead(0)
			return errFrameTooLarge
		}
		hdr, err := proto.ParseFrameHeader(data)
		if errors.Is(err, proto.ErrFrameSize) {
			return err // a host announcing a buffer we will not allocate is not a host to keep
		}
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
		err = c.cd.writeAck(c.ctrl, hdr.Seq)
		c.wmu.Unlock()
		if err != nil {
			return err
		}
	}
}

// stallReader sets a fresh read deadline before every read, so a stream that
// stops delivering is abandoned after d while a slow but steady one is not.
type stallReader struct {
	st *quic.ReceiveStream
	d  time.Duration
}

func (r stallReader) Read(p []byte) (int, error) {
	r.st.SetReadDeadline(time.Now().Add(r.d))
	return r.st.Read(p)
}
