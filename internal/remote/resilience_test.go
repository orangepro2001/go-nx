package remote

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
)

// shiftySource is a screen that can fail, change size and be told to draw the
// pointer: everything a real monitor does that a fixed fake does not.
type shiftySource struct {
	mu        sync.Mutex
	w, h      int
	shade     byte
	failGrabs int // Grab fails this many times, then works
	nextSize  *[2]int
	refreshes int
	cursorOn  []bool
	noRefresh bool // hide Refresh, to model a source that cannot recover
}

func (s *shiftySource) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

func (s *shiftySource) Grab(dst *tile.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failGrabs > 0 {
		s.failGrabs--
		return errors.New("shifty: the desktop is not available")
	}
	if dst.W != s.w || dst.H != s.h {
		return errors.New("shifty: frame size mismatch")
	}
	for i := range dst.Pix {
		dst.Pix[i] = s.shade
	}
	return nil
}

func (s *shiftySource) Refresh() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshes++
	if s.nextSize != nil {
		s.w, s.h = s.nextSize[0], s.nextSize[1]
		s.nextSize = nil
		return true, nil
	}
	return false, nil
}

func (s *shiftySource) SetCursorVisible(on bool) {
	s.mu.Lock()
	s.cursorOn = append(s.cursorOn, on)
	s.mu.Unlock()
}

func (s *shiftySource) set(f func(*shiftySource)) {
	s.mu.Lock()
	f(s)
	s.mu.Unlock()
}

// nonRefreshing hides Refresh and SetCursorVisible from the host.
type nonRefreshing struct{ s *shiftySource }

func (n nonRefreshing) Size() (int, int)         { return n.s.Size() }
func (n nonRefreshing) Grab(f *tile.Frame) error { return n.s.Grab(f) }

type viewerRig struct {
	client *Client
	served chan error
	frames chan frameInfo
	errs   chan error
	peer   chan PeerInfo
}

type frameInfo struct {
	w, h  int
	first byte
}

func hostShifty(t *testing.T, src Source, opt Options, o DialOptions) *viewerRig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	r := &viewerRig{served: make(chan error, 1), frames: make(chan frameInfo, 256), errs: make(chan error, 16), peer: make(chan PeerInfo, 1)}
	if opt.Check == nil {
		opt.Check = allowAll
	}
	if opt.FPS == 0 {
		opt.FPS = 50
	}
	opt.OnError = func(err error) { r.errs <- err }
	opt.OnPeer = func(p PeerInfo) { r.peer <- p }
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			r.served <- err
			return
		}
		r.served <- Serve(ctx, conn, src, &fakeInjector{}, proto.NewDedup(), opt)
	}()
	r.client, err = DialWith(ctx, ln.Addr().String(), identity(t), hostsStore(t), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.client.Close)
	go r.client.Run(ctx, func(f *tile.Frame, _ []tile.Rect) {
		select {
		case r.frames <- frameInfo{f.W, f.H, f.Pix[0]}:
		default:
		}
	}, nil)
	return r
}

func (r *viewerRig) waitFrame(t *testing.T, what string, ok func(frameInfo) bool) {
	t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case f := <-r.frames:
			if ok(f) {
				return
			}
		case err := <-r.served:
			t.Fatalf("waiting for %s: the host ended the session: %v", what, err)
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// A desktop that disappears for a moment (lock screen, UAC, a display change)
// must not end the session: the viewer keeps its last picture and the picture
// resumes when the desktop is back.
func TestGrabFailureDoesNotEndTheSession(t *testing.T) {
	src := &shiftySource{w: 128, h: 128, shade: 10}
	r := hostShifty(t, src, Options{RefreshEvery: time.Hour}, DialOptions{})
	r.waitFrame(t, "the first frame", func(f frameInfo) bool { return f.first == 10 })

	src.set(func(s *shiftySource) { s.failGrabs = 20; s.shade = 99 }) // ~0.4 s of failures at 50 fps
	r.waitFrame(t, "the picture to resume after the failures", func(f frameInfo) bool { return f.first == 99 })

	src.mu.Lock()
	refreshes := src.refreshes
	src.mu.Unlock()
	if refreshes == 0 {
		t.Error("the host never asked the source to recover")
	}
	select {
	case err := <-r.errs:
		if err == nil {
			t.Error("nil error reported")
		}
	case <-time.After(time.Second):
		t.Error("the outage was not reported")
	}
	select {
	case err := <-r.served:
		t.Fatalf("the session ended: %v", err)
	default:
	}
}

func TestPermanentGrabFailureEndsTheSessionEventually(t *testing.T) {
	src := &shiftySource{w: 64, h: 64, failGrabs: 1 << 30}
	r := hostShifty(t, src, Options{RefreshEvery: time.Hour, GrabRetryFor: 400 * time.Millisecond}, DialOptions{})
	select {
	case err := <-r.served:
		if err == nil {
			t.Fatal("Serve returned nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a screen that never comes back kept the session open")
	}
}

func TestGrabFailureWithoutARefresherEndsTheSessionAtOnce(t *testing.T) {
	src := &shiftySource{w: 64, h: 64, failGrabs: 1 << 30}
	r := hostShifty(t, nonRefreshing{src}, Options{}, DialOptions{})
	select {
	case err := <-r.served:
		if err == nil {
			t.Fatal("Serve returned nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a source that cannot recover kept the session open")
	}
}

// The host's resolution changes during a session: the viewer is sent frames of the
// new size and the injector is told so it can map coordinates onto the new screen.
func TestResolutionChangeReachesTheViewerAndTheInjector(t *testing.T) {
	src := &shiftySource{w: 128, h: 96, shade: 5}
	changed := make(chan [2]int, 4)
	r := hostShifty(t, src, Options{
		RefreshEvery:    30 * time.Millisecond,
		OnSourceChanged: func(w, h int) { changed <- [2]int{w, h} },
	}, DialOptions{})
	r.waitFrame(t, "the first frame", func(f frameInfo) bool { return f.w == 128 && f.h == 96 })

	src.set(func(s *shiftySource) { s.nextSize = &[2]int{256, 192}; s.shade = 77 })
	r.waitFrame(t, "a frame at the new size", func(f frameInfo) bool { return f.w == 256 && f.h == 192 && f.first == 77 })
	select {
	case got := <-changed:
		if got != [2]int{256, 192} {
			t.Errorf("OnSourceChanged(%v), want [256 192]", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("OnSourceChanged was never called")
	}
	select {
	case err := <-r.served:
		t.Fatalf("the session ended: %v", err)
	default:
	}
}

func TestAScreenBeyondTheProtocolAfterAChangeEndsTheSession(t *testing.T) {
	src := &shiftySource{w: 128, h: 96, shade: 5}
	r := hostShifty(t, src, Options{RefreshEvery: 30 * time.Millisecond}, DialOptions{})
	r.waitFrame(t, "the first frame", func(f frameInfo) bool { return f.w == 128 })
	src.set(func(s *shiftySource) { s.nextSize = &[2]int{proto.MaxFrameDim + 1, 100} })
	select {
	case err := <-r.served:
		if err == nil {
			t.Fatal("Serve returned nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host kept sending after the screen outgrew the protocol")
	}
}

func TestAdvertisedCapabilitiesFollowTheSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(*shiftySource) Source
		want proto.Caps
	}{
		{"a source that can refresh and draw the pointer", func(s *shiftySource) Source { return s }, proto.CapWheelH | proto.CapResize | proto.CapCursor},
		{"a plain source", func(s *shiftySource) Source { return nonRefreshing{s} }, proto.CapWheelH},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := hostShifty(t, tc.src(&shiftySource{w: 64, h: 64}), Options{}, DialOptions{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			info, err := r.client.WaitHost(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// fakeInjector can type, so CapText is there too.
			want := tc.want | proto.CapText
			if info.Caps != want {
				t.Errorf("caps = %b, want %b", info.Caps, want)
			}
		})
	}
}

// The pointer is drawn into the picture only for a viewer that asks: one with its
// own pointer on screen would show two.
func TestThePointerIsDrawnOnlyForViewersThatAsk(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    DialOptions
		want bool
	}{
		{"v1 viewer that asks", DialOptions{WantCursor: true}, true},
		{"v1 viewer that does not", DialOptions{}, false},
		{"v0 viewer", DialOptions{ForceV0: true, WantCursor: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &shiftySource{w: 64, h: 64}
			r := hostShifty(t, src, Options{}, tc.o)
			select {
			case <-r.peer:
			case <-time.After(5 * time.Second):
				t.Fatal("no peer")
			}
			time.Sleep(100 * time.Millisecond)
			src.mu.Lock()
			calls := append([]bool(nil), src.cursorOn...)
			src.mu.Unlock()
			if len(calls) == 0 || calls[len(calls)-1] != tc.want {
				t.Errorf("SetCursorVisible calls = %v, want the last to be %v", calls, tc.want)
			}
		})
	}
}
