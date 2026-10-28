package remote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// hostileHost is a raw QUIC listener that accepts one viewer's connection and
// hands it to script. It speaks no real protocol, so a test can send a viewer
// whatever bytes it likes.
func hostileHost(t *testing.T, script func(ctx context.Context, conn *quic.Conn)) (addr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		if _, err := conn.AcceptStream(ctx); err != nil { // the viewer's control stream
			return
		}
		script(ctx, conn)
		<-ctx.Done() // keep the connection open until the test ends
	}()
	return ln.Addr().String()
}

// runViewer dials addr and returns what Run returned plus the frames it showed.
func runViewer(t *testing.T, addr string, tweak func(*Client)) (chan error, chan *tile.Frame) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	c, err := Dial(ctx, addr, identity(t), hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if tweak != nil {
		tweak(c)
	}
	done := make(chan error, 1)
	frames := make(chan *tile.Frame, 8)
	go func() {
		done <- c.Run(ctx, func(f *tile.Frame, _ []tile.Rect) {
			cp := tile.NewFrame(f.W, f.H)
			copy(cp.Pix, f.Pix)
			frames <- cp
		}, nil)
	}()
	return done, frames
}

func sendStream(t *testing.T, conn *quic.Conn, b []byte) {
	t.Helper()
	st, err := conn.OpenUniStream()
	if err != nil {
		t.Error(err)
		return
	}
	st.Write(b)
	st.Close()
}

// validUpdate encodes a solid 64x64 frame as one update with the given seq.
func validUpdate(seq uint32, colour byte) []byte {
	f := tile.NewFrame(64, 64)
	for i := range f.Pix {
		f.Pix[i] = colour
	}
	var d tile.Differ
	data, err := tile.Encode(f, d.Diff(f))
	if err != nil {
		panic(err)
	}
	return append(proto.FrameHeader{Seq: seq, W: 64, H: 64}.Marshal(), data...)
}

func TestViewerRefusesAnnouncedFrameSizeBeyondLimit(t *testing.T) {
	addr := hostileHost(t, func(ctx context.Context, conn *quic.Conn) {
		// 65535 x 65535 would be a 16 GiB buffer.
		sendStream(t, conn, proto.FrameHeader{Seq: 1, W: 65535, H: 65535}.Marshal())
	})
	done, _ := runViewer(t, addr, nil)
	select {
	case err := <-done:
		if !errors.Is(err, proto.ErrFrameSize) {
			t.Fatalf("Run = %v, want proto.ErrFrameSize", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer kept running after a host announced an absurd frame size")
	}
}

func TestViewerRefusesUpdateLargerThanLimit(t *testing.T) {
	addr := hostileHost(t, func(ctx context.Context, conn *quic.Conn) {
		b := append(proto.FrameHeader{Seq: 1, W: 64, H: 64}.Marshal(), make([]byte, 64<<10)...)
		sendStream(t, conn, b)
	})
	done, _ := runViewer(t, addr, func(c *Client) { c.maxFrame = 4 << 10 })
	select {
	case err := <-done:
		if !errors.Is(err, errFrameTooLarge) {
			t.Fatalf("Run = %v, want errFrameTooLarge", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer accepted an update past its size limit")
	}
}

// A stream that stops delivering must be abandoned so that later, valid updates
// are still shown.
func TestViewerMovesPastAStalledUpdate(t *testing.T) {
	addr := hostileHost(t, func(ctx context.Context, conn *quic.Conn) {
		st, err := conn.OpenUniStream()
		if err != nil {
			return
		}
		st.Write(proto.FrameHeader{Seq: 1, W: 64, H: 64}.Marshal()) // never finished
		time.Sleep(100 * time.Millisecond)
		sendStream(t, conn, validUpdate(2, 77))
	})
	_, frames := runViewer(t, addr, func(c *Client) { c.stall = 300 * time.Millisecond })
	select {
	case f := <-frames:
		if f.Pix[0] != 77 {
			t.Fatalf("first pixel = %d, want 77", f.Pix[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("viewer never recovered from a stalled update stream")
	}
}

type hugeSource struct{ w, h int }

func (s hugeSource) Size() (int, int)       { return s.w, s.h }
func (s hugeSource) Grab(*tile.Frame) error { return errors.New("must not be reached") }

// A host whose screen is bigger than the protocol carries must say so rather
// than stream frames that every viewer refuses.
func TestServeRefusesScreenBeyondProtocolLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			served <- err
			return
		}
		served <- Serve(ctx, conn, hugeSource{20000, 20000}, &fakeInjector{}, proto.NewDedup(), Options{
			Check: func(_, _, _ string) (trust.Verdict, error) { return trust.Allowed, nil },
		})
	}()
	c, err := Dial(ctx, ln.Addr().String(), identity(t), hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case err := <-served:
		if err == nil || !strings.Contains(err.Error(), "outside what the protocol carries") {
			t.Fatalf("Serve = %v, want a protocol-limit error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not refuse an oversized screen")
	}
}
