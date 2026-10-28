package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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
	wmu  sync.Mutex // serializes writes to the control stream
	cd   codec
	out  *outbox // input waiting to be written by writeLoop

	closing atomic.Bool // Close has begun ending the control stream; nothing more may be written to it

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
	// ExpectFingerprint, if set, connects only to a host whose certificate has this
	// SHA-256 fingerprint (hex, any case, colons and spaces allowed): the case of a
	// pairing code that carries it. The host store is then neither read nor written,
	// may be nil, and a host with another certificate is refused during the handshake
	// with ErrHostChanged, before the password is sent.
	ExpectFingerprint string
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
	var cfg *tls.Config
	var pin *pinRecorder // nil when the fingerprint was given and nothing is to be remembered
	if o.ExpectFingerprint != "" {
		want, ok := NormalizeFingerprint(o.ExpectFingerprint)
		if !ok {
			return nil, fmt.Errorf("remote: %q is not a certificate fingerprint (want 64 hex digits)", o.ExpectFingerprint)
		}
		cfg = pinnedClientTLS(id, want, maxV)
	} else {
		if hosts == nil {
			return nil, errors.New("remote: no host store and no expected fingerprint to check the host against")
		}
		cfg, pin = clientTLSVersions(id, hostOf(addr), hosts, maxV)
	}
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
	if pin != nil {
		if err := pin.commit(hosts); err != nil {
			log.Printf("pin host %s: %v", hostOf(addr), err)
		}
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
	c := &Client{conn: conn, ctrl: ctrl, cd: cd, out: newOutbox(), maxFrame: maxFrameBytes, stall: stallTimeout, welcome: make(chan struct{})}
	go c.writeLoop()
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

// Stats is what the connection can say about itself, for a display of its health.
type Stats struct {
	RTT           time.Duration // smoothed round-trip time
	BytesReceived uint64        // from the host since the connection began, including repeats
	BytesSent     uint64
}

// Stats reads the connection's statistics.
func (c *Client) Stats() Stats {
	s := c.conn.ConnectionStats()
	return Stats{RTT: s.SmoothedRTT, BytesReceived: s.BytesReceived, BytesSent: s.BytesSent}
}

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

// How long Close gives the input already accepted: closeFlush to be written to the
// connection, and then closeLinger for the host to read it to the end and hang up.
// A healthy link needs a few milliseconds; these only bound the wait on one that is
// not.
const (
	closeFlush  = 300 * time.Millisecond
	closeLinger = time.Second
)

// Flush waits until the input accepted so far has been written to the connection,
// for at most timeout. It returns ErrFlushTimeout if some was still waiting, or the
// error that made the connection unusable.
func (c *Client) Flush(timeout time.Duration) error { return c.out.flush(timeout) }

// Close ends the session. Input that was accepted a moment ago (a key released just
// before quitting) gets there first, if the link takes it within a moment: it is
// written, then the control stream is ended, which the host reads to its end,
// carrying everything out, before it closes the connection. Closing the connection
// at once would discard what had been written but not yet delivered. A link that
// does not take the input in time is closed regardless.
func (c *Client) Close() {
	if c.out.flush(closeFlush) == nil {
		c.wmu.Lock()
		c.closing.Store(true)
		c.ctrl.Close()
		c.wmu.Unlock()
		select {
		case <-c.conn.Context().Done():
		case <-time.After(closeLinger):
		}
	}
	c.out.fail(errOutboxClosed) // from now on SendInput says so, instead of queueing for a connection that is gone
	c.conn.CloseWithError(0, "")
}

// SendInput queues one user action to be sent and returns at once: it never
// waits for the network, so the caller (a window's update loop) is never frozen
// by a host that stopped reading. Consecutive pointer moves are merged into the
// latest. If the host is so far behind that the queue is full, everything but a
// move is refused with ErrBacklog. Once the connection has failed the error is
// returned here as well.
func (c *Client) SendInput(in proto.Input) error {
	return c.out.push(outMsg{
		move:  in.Kind == proto.KindMouseMove && in.OpID == 0,
		write: func(w io.Writer) error { return c.cd.writeInput(w, in) },
	})
}

// writeLoop writes the queue to the control stream, in order. A write that takes
// longer than the timeout means the link is dead; a message that was only half
// written would make everything after it unreadable, so the connection is closed.
func (c *Client) writeLoop() {
	for {
		select {
		case <-c.out.wake:
		case <-c.conn.Context().Done():
			c.out.fail(errOutboxClosed)
			return
		}
		for msgs := c.out.drain(); len(msgs) > 0; msgs = c.out.drain() {
			for _, m := range msgs {
				if err := c.writeCtrl(m.write); err != nil {
					return
				}
				c.out.written(1)
			}
		}
	}
}

// writeCtrl writes one control message with a deadline. On failure the connection
// is closed and the outbox refuses further input.
func (c *Client) writeCtrl(write func(io.Writer) error) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closing.Load() {
		// The stream was ended on purpose; an acknowledgement that comes after that
		// is not a failure and must not take the connection down with it, along with
		// the input that is still on its way.
		return errOutboxClosed
	}
	c.ctrl.SetWriteDeadline(time.Now().Add(c.out.writeTimeout))
	if err := write(c.ctrl); err != nil {
		c.out.fail(err)
		c.conn.CloseWithError(0, "control stream could not be written")
		return err
	}
	return nil
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
	return c.out.push(outMsg{write: func(w io.Writer) error { return c.cd.writeText(w, opID, text) }})
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
		err = c.writeCtrl(func(w io.Writer) error { return c.cd.writeAck(w, hdr.Seq) })
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
