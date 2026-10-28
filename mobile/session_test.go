package mobile

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/logsink"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// --- a callback that records what the application would be shown ---

type testCallback struct {
	mu        sync.Mutex
	states    []string
	errs      [][2]string
	receipts  map[int64]int
	frame     *tile.Frame // the picture assembled from the tiles, BGRA
	frames    int         // OnFrameBegin calls
	tiles     int
	stateCh   chan string
	errCh     chan string
	receiptCh chan int64
	frameCh   chan struct{}
	panicIn   string // a method that panics, to see that the session survives it
}

func newTestCallback() *testCallback {
	return &testCallback{
		receipts: map[int64]int{},
		stateCh:  make(chan string, 64), errCh: make(chan string, 64),
		receiptCh: make(chan int64, 64), frameCh: make(chan struct{}, 1024),
	}
}

func (c *testCallback) OnState(state string) {
	c.mu.Lock()
	c.states = append(c.states, state)
	c.mu.Unlock()
	c.stateCh <- state
}

func (c *testCallback) OnFrameBegin(w, h int) {
	c.mu.Lock()
	c.frames++
	if c.frame == nil || c.frame.W != w || c.frame.H != h {
		c.frame = tile.NewFrame(w, h)
	}
	c.mu.Unlock()
}

func (c *testCallback) OnTile(x, y, w, h int, pixels []byte) {
	if c.panicIn == "OnTile" {
		panic("the application's callback blew up")
	}
	c.mu.Lock()
	c.tiles++
	if len(pixels) != w*h*4 {
		c.mu.Unlock()
		panic("a tile whose pixels do not fill its rectangle")
	}
	for row := 0; row < h; row++ {
		copy(c.frame.Pix[((y+row)*c.frame.W+x)*4:], pixels[row*w*4:(row+1)*w*4]) // the slice is only valid now
	}
	c.mu.Unlock()
	select {
	case c.frameCh <- struct{}{}:
	default:
	}
}

func (c *testCallback) OnReceipt(opID int64, status int) {
	c.mu.Lock()
	c.receipts[opID] = status
	c.mu.Unlock()
	c.receiptCh <- opID
}

func (c *testCallback) OnError(code, message string) {
	c.mu.Lock()
	c.errs = append(c.errs, [2]string{code, message})
	c.mu.Unlock()
	c.errCh <- code
}

func (c *testCallback) waitState(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case got := <-c.stateCh:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("state %q never reported (states so far: %v)", want, c.statesSoFar())
		}
	}
}

func (c *testCallback) statesSoFar() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.states...)
}

func (c *testCallback) waitError(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case got := <-c.errCh:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("error %q never reported", want)
		}
	}
}

func (c *testCallback) waitReceipt(t *testing.T, id int64) int {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case got := <-c.receiptCh:
			if got == id {
				c.mu.Lock()
				defer c.mu.Unlock()
				return c.receipts[id]
			}
		case <-deadline:
			t.Fatalf("receipt %d never arrived", id)
		}
	}
}

// waitFrames waits until n pictures have been announced.
func (c *testCallback) waitFrames(t *testing.T, n int) {
	t.Helper()
	waitUntil(t, "pictures", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.frames >= n
	})
}

func (c *testCallback) picture() *tile.Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frame == nil {
		return nil
	}
	cp := tile.NewFrame(c.frame.W, c.frame.H)
	copy(cp.Pix, c.frame.Pix)
	return cp
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- a host to talk to ---

type testHost struct {
	addr string
	fp   string
	inj  *logsink.Sink
	src  remote.Source

	mu     sync.Mutex
	cursor []bool // SetCursorVisible calls
}

type cursorSource struct {
	remote.Source
	h *testHost
}

func (c cursorSource) SetCursorVisible(on bool) {
	c.h.mu.Lock()
	c.h.cursor = append(c.h.cursor, on)
	c.h.mu.Unlock()
}

type textlessInjector struct{ s *logsink.Sink }

func (t textlessInjector) Inject(in proto.Input) error { return t.s.Inject(in) }

// startHost runs a host that lets the viewer with fingerprint viewerFP in when it
// offers code (any code if the host's code is empty).
func startHost(t *testing.T, viewerFP, code string, typing bool) *testHost {
	t.Helper()
	hostID, err := trust.Identity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := remote.Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	src, err := synth.New(640, 480)
	if err != nil {
		t.Fatal(err)
	}
	h := &testHost{addr: ln.Addr().String(), fp: trust.Fingerprint(hostID.Certificate[0]), inj: logsink.New(nil)}
	h.inj.SetScreen(640, 480)
	h.src = cursorSource{src, h}
	var inj remote.Injector = h.inj
	if !typing {
		inj = textlessInjector{h.inj}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); ln.Close() })
	dd := proto.NewDedup()
	go func() {
		for {
			conn, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			go remote.Serve(ctx, conn, h.src, inj, dd, remote.Options{FPS: 30, HostName: "mobile-test-host",
				Check: func(f, _, pw string) (trust.Verdict, error) {
					if f == viewerFP && pw == code {
						return trust.Allowed, nil
					}
					return trust.BadPassword, nil
				}})
		}
	}()
	return h
}

func viewerIdentity(t *testing.T) (dir, fingerprint string, id tls.Certificate) {
	t.Helper()
	dir = t.TempDir()
	id, err := trust.Identity(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, trust.Fingerprint(id.Certificate[0]), id
}

// connect starts a session to a fresh host and waits until it streams.
func connect(t *testing.T, typing bool) (*Session, *testCallback, *testHost) {
	t.Helper()
	dir, vfp, _ := viewerIdentity(t)
	h := startHost(t, vfp, "123456", typing)
	cb := newTestCallback()
	s, err := NewSession(dir, h.addr, h.fp, "123456", cb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	cb.waitState(t, StateStreaming)
	return s, cb, h
}

func (h *testHost) waitInputs(t *testing.T, n int) []proto.Input {
	t.Helper()
	waitUntil(t, "the host to receive the input", func() bool { return len(h.inj.Inputs()) >= n })
	return h.inj.Inputs()
}

// --- tests ---

func TestSessionPairsStreamsAndTheTilesAssembleToTheRightPicture(t *testing.T) {
	s, cb, _ := connect(t, true)
	cb.waitFrames(t, 1)
	if s.Width() != 640 || s.Height() != 480 {
		t.Fatalf("size = %dx%d", s.Width(), s.Height())
	}
	// The pixel contract of the callback: BGRA, exactly as the host captured it.
	// (The application swaps red and blue itself, as Android's bitmaps want RGBA.)
	waitUntil(t, "the whole picture", func() bool {
		p := cb.picture()
		return p != nil && synth.VerifyMarkers(p) == nil
	})
	if st := cb.statesSoFar(); st[0] != StateConnecting || st[len(st)-1] != StateStreaming {
		t.Errorf("states = %v, want connecting ... streaming", st)
	}
	if s.HostName() != "mobile-test-host" || s.ProtocolVersion() != 1 || !s.CanText() || !s.CanWheelH() {
		t.Errorf("host %q protocol %d text %v wheelH %v", s.HostName(), s.ProtocolVersion(), s.CanText(), s.CanWheelH())
	}
}

// Every pointer action carries the position the finger is at, and never (0,0) just
// because no move came first: the bug where releasing the button threw the pointer
// to the top-left corner of the host.
func TestMouseActionsCarryTheLastPosition(t *testing.T) {
	s, _, h := connect(t, true)
	steps := []func() error{
		func() error { return s.Move(10, 20) },
		func() error { return s.MouseDown(0) },
		func() error { return s.Wheel(120) },
		func() error { return s.WheelH(-120) },
		func() error { return s.MouseUp(0) },
		func() error { return s.Move(300, 200) },
		func() error { return s.MouseDown(1) },
		func() error { return s.MouseUp(1) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	got := h.waitInputs(t, len(steps))
	want := []struct {
		kind byte
		x, y int32
	}{
		{proto.KindMouseMove, 10, 20}, {proto.KindMouseDown, 10, 20}, {proto.KindWheel, 10, 20}, {proto.KindWheelH, 10, 20},
		{proto.KindMouseUp, 10, 20}, {proto.KindMouseMove, 300, 200}, {proto.KindMouseDown, 300, 200}, {proto.KindMouseUp, 300, 200},
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].X != w.x || got[i].Y != w.y {
			t.Errorf("input %d = kind %d at %d,%d, want kind %d at %d,%d", i, got[i].Kind, got[i].X, got[i].Y, w.kind, w.x, w.y)
		}
	}
}

// Before the first move there is no position to act at; guessing the corner would
// click somewhere the person never pointed.
func TestPointerActionsBeforeAnyMoveAreRefused(t *testing.T) {
	s, _, h := connect(t, true)
	for name, f := range map[string]func() error{
		"MouseDown": func() error { return s.MouseDown(0) },
		"MouseUp":   func() error { return s.MouseUp(0) },
		"Wheel":     func() error { return s.Wheel(120) },
		"WheelH":    func() error { return s.WheelH(120) },
	} {
		if err := f(); !errors.Is(err, errNoPosition) {
			t.Errorf("%s before any Move = %v, want errNoPosition", name, err)
		}
	}
	s.Move(5, 5)
	h.waitInputs(t, 1)
	if n := len(h.inj.Inputs()); n != 1 {
		t.Errorf("%d inputs reached the host, want only the move", n)
	}
	// Keys have no position and need none.
	if err := s.KeyDown(0x41); err != nil {
		t.Errorf("KeyDown: %v", err)
	}
}

func TestPositionsAreClampedToThePicture(t *testing.T) {
	s, cb, h := connect(t, true)
	cb.waitFrames(t, 1)
	// One at a time: moves sent faster than they can be written are merged into the
	// last, which is right for a finger but would hide the ones before it here.
	var got []proto.Input
	want := [][2]int32{{0, 0}, {639, 479}, {639, 479}, {639, 0}}
	for i, p := range [][2]int{{-5, -9}, {99999, 99999}, {640, 480}, {1 << 40, -(1 << 40)}} {
		if err := s.Move(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
		got = h.waitInputs(t, i+1)
		if got[i].X != want[i][0] || got[i].Y != want[i][1] {
			t.Errorf("move %d reached the host as %d,%d, want %d,%d", i, got[i].X, got[i].Y, want[i][0], want[i][1])
		}
	}
	// And the next action acts where the clamped move left the pointer.
	s.MouseDown(0)
	got = h.waitInputs(t, 5)
	if got[4].X != 639 || got[4].Y != 0 {
		t.Errorf("a click after a clamped move is at %d,%d, want 639,0", got[4].X, got[4].Y)
	}
}

// A move the connection refused must not become the position later actions use.
func TestARefusedMoveDoesNotChangeThePosition(t *testing.T) {
	s, _, h := connect(t, true)
	s.Move(40, 50)
	s.client.Close() // the connection is gone: the next move is refused
	if err := s.Move(200, 200); err == nil {
		t.Fatal("a move on a closed connection succeeded")
	}
	s.inputMu.Lock()
	x, y := s.mouseX, s.mouseY
	s.inputMu.Unlock()
	if x != 40 || y != 50 {
		t.Errorf("the remembered position is %d,%d, want 40,50", x, y)
	}
	_ = h
}

func TestReleaseAllLetsGoButtonsFirstThenKeysLastPressedFirst(t *testing.T) {
	for run := 0; run < 5; run++ { // a map would pass by luck
		s, _, h := connect(t, true)
		s.Move(23, 34)
		s.MouseDown(0)
		s.MouseDown(2)
		s.KeyDown(0xA2) // Ctrl
		s.KeyDown(0xA0) // Shift
		s.KeyDown(0x41)
		s.KeyDown(0x41)    // auto-repeat: still one key
		h.waitInputs(t, 7) // the move, two buttons, four key-downs
		before := len(h.inj.Inputs())
		if err := s.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		got := h.waitInputs(t, before+5)[before:]
		want := []struct {
			kind byte
			arg  int32
		}{
			{proto.KindMouseUp, 2}, {proto.KindMouseUp, 0}, {proto.KindKeyUp, 0x41}, {proto.KindKeyUp, 0xA0}, {proto.KindKeyUp, 0xA2},
		}
		for i, w := range want {
			if got[i].Kind != w.kind || got[i].Arg != w.arg {
				t.Fatalf("run %d: release %d = kind %d arg %#x, want kind %d arg %#x", run, i, got[i].Kind, got[i].Arg, w.kind, w.arg)
			}
			if w.kind == proto.KindMouseUp && (got[i].X != 23 || got[i].Y != 34) {
				t.Fatalf("run %d: a release let go at %d,%d, want 23,34 (not the corner)", run, got[i].X, got[i].Y)
			}
		}
		if err := s.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		if n := len(h.inj.Inputs()); n != before+5 {
			t.Fatalf("a second ReleaseAll sent %d more inputs, want none", n-before-5)
		}
		s.Close()
	}
}

// Closing lets go of what is held, and it gets to the host before the connection
// goes: a key left pressed on a host is worse than a delay.
func TestCloseReleasesWhatIsHeldAndItArrives(t *testing.T) {
	s, cb, h := connect(t, true)
	s.Move(7, 8)
	s.MouseDown(0)
	s.KeyDown(0x10)
	h.waitInputs(t, 3)
	s.Close()
	cb.waitState(t, StateClosed)
	got := h.waitInputs(t, 5)
	if got[3].Kind != proto.KindMouseUp || got[4].Kind != proto.KindKeyUp {
		t.Fatalf("after Close the host saw %+v", got[3:])
	}
	// An intentional close is not an error.
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.errs) != 0 {
		t.Errorf("Close reported errors: %v", cb.errs)
	}
	s.Close() // and may be repeated
}

func TestKeysAndMouseButtonsAreValidated(t *testing.T) {
	s, _, _ := connect(t, true)
	s.Move(1, 1)
	for _, b := range []int{-1, 3, 99} {
		if s.MouseDown(b) == nil || s.MouseUp(b) == nil {
			t.Errorf("mouse button %d was accepted", b)
		}
	}
	for _, vk := range []int{0, -1, 0x200, 1 << 20} {
		if s.KeyDown(vk) == nil || s.KeyUp(vk) == nil {
			t.Errorf("virtual key %#x was accepted", vk)
		}
	}
	for _, vk := range []int{1, 0x41, 0x100 | 0x2D, 0x1ff} {
		if err := s.KeyDown(vk); err != nil {
			t.Errorf("virtual key %#x refused: %v", vk, err)
		}
		s.KeyUp(vk)
	}
}

func TestTextIsDeliveredWithAReceipt(t *testing.T) {
	s, cb, h := connect(t, true)
	if err := s.Text(""); err != nil {
		t.Errorf("empty text = %v, want nothing to do", err)
	}
	if err := s.Text("hello 世界"); err != nil {
		t.Fatal(err)
	}
	if st := cb.waitReceipt(t, 1); st != ReceiptExecuted {
		t.Errorf("receipt = %d, want executed", st)
	}
	if err := s.Text("again"); err != nil {
		t.Fatal(err)
	}
	cb.waitReceipt(t, 2) // the operation numbers count up
	waitUntil(t, "both texts", func() bool { return len(h.inj.Typed()) == 2 })
	if typed := h.inj.Typed(); typed[0] != "hello 世界" || typed[1] != "again" {
		t.Errorf("host typed %q", typed)
	}
	if err := s.Text(strings.Repeat("x", proto.MaxTextLen+1)); err == nil {
		t.Error("text over the limit was accepted")
	}
}

func TestTextToAHostThatCannotTypeIsRefusedHere(t *testing.T) {
	s, _, h := connect(t, false)
	if s.CanText() {
		t.Fatal("the host cannot type but says it can")
	}
	if err := s.Text("hi"); err == nil {
		t.Fatal("Text succeeded against a host that cannot type")
	}
	if len(h.inj.Typed()) != 0 {
		t.Error("text reached a host that cannot type")
	}
}

func TestWantCursorAsksTheHostToDrawThePointer(t *testing.T) {
	dir, vfp, _ := viewerIdentity(t)
	h := startHost(t, vfp, "c", true)
	cb := newTestCallback()
	s, err := NewSessionWithOptions(dir, h.addr, h.fp, "c", true, cb)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cb.waitState(t, StateStreaming)
	waitUntil(t, "the host to be asked", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.cursor) > 0 && h.cursor[len(h.cursor)-1]
	})

	// And by default it is not asked.
	dir2, vfp2, _ := viewerIdentity(t)
	h2 := startHost(t, vfp2, "c", true)
	cb2 := newTestCallback()
	s2, err := NewSession(dir2, h2.addr, h2.fp, "c", cb2)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	cb2.waitState(t, StateStreaming)
	time.Sleep(100 * time.Millisecond)
	h2.mu.Lock()
	defer h2.mu.Unlock()
	for _, on := range h2.cursor {
		if on {
			t.Error("a session that did not ask for the pointer was given it")
		}
	}
}

func TestSessionRejectsAWrongHostFingerprint(t *testing.T) {
	dir, vfp, _ := viewerIdentity(t)
	h := startHost(t, vfp, "", true)
	cb := newTestCallback()
	_, err := NewSession(dir, h.addr, strings.Repeat("0", 64), "", cb)
	if !errors.Is(err, remote.ErrHostChanged) && (err == nil || !strings.Contains(err.Error(), remote.ErrHostChanged.Error())) {
		t.Fatalf("err = %v, want the host identity to be refused", err)
	}
	cb.waitError(t, "host-identity")
	cb.waitState(t, StateClosed)
	if n := len(h.inj.Inputs()); n != 0 {
		t.Errorf("%d inputs reached a host that was not the expected one", n)
	}
}

func TestSessionReportsErrorsWithStableCodes(t *testing.T) {
	dir, vfp, _ := viewerIdentity(t)
	h := startHost(t, vfp, "123456", true)
	cb := newTestCallback()
	s, err := NewSession(dir, h.addr, h.fp, "000000", cb) // a wrong code
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cb.waitError(t, "bad-code")
	cb.waitState(t, StateClosed)

	// A device the host does not know, with no code at all.
	dir2, _, _ := viewerIdentity(t)
	cb2 := newTestCallback()
	s2, err := NewSession(dir2, h.addr, h.fp, "", cb2)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	cb2.waitError(t, "bad-code") // the test host answers every refusal with a bad password
}

func TestClassifyMapsEveryCloseCode(t *testing.T) {
	for code, want := range map[quicCode]string{
		remote.CloseUntrusted: "untrusted", remote.CloseBadPassword: "bad-code", remote.CloseReplaced: "replaced",
		remote.CloseLocked: "locked", remote.CloseRevoked: "revoked", remote.CloseBusy: "busy", remote.CloseUnverified: "unverified",
	} {
		got, msg := classify(appError(code))
		if got != want || msg == "" {
			t.Errorf("close code %d = %q (%q), want %q with a message", code, got, msg, want)
		}
	}
	if got, _ := classify(nil); got != "disconnected" {
		t.Errorf("a clean end = %q", got)
	}
	if got, _ := classify(context.Canceled); got != "disconnected" {
		t.Errorf("a cancelled session = %q", got)
	}
	if got, msg := classify(errors.New("boom")); got != "disconnected" || msg != "boom" {
		t.Errorf("another error = %q %q", got, msg)
	}
}

func TestSessionValidatesArguments(t *testing.T) {
	cb := newTestCallback()
	good := strings.Repeat("ab", 32)
	cases := map[string]func() error{
		"no callback":       func() error { _, err := NewSession(t.TempDir(), "127.0.0.1:1", good, "", nil); return err },
		"no storage dir":    func() error { _, err := NewSession("", "127.0.0.1:1", good, "", cb); return err },
		"blank storage dir": func() error { _, err := NewSession("   ", "127.0.0.1:1", good, "", cb); return err },
		"bad address":       func() error { _, err := NewSession(t.TempDir(), "not-an-address", good, "", cb); return err },
		"no port":           func() error { _, err := NewSession(t.TempDir(), "127.0.0.1", good, "", cb); return err },
		"bad fingerprint":   func() error { _, err := NewSession(t.TempDir(), "127.0.0.1:1", "bad", "", cb); return err },
		"code too long": func() error {
			_, err := NewSession(t.TempDir(), "127.0.0.1:1", good, strings.Repeat("9", 256), cb)
			return err
		},
	}
	for name, f := range cases {
		start := time.Now()
		if err := f(); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%s: took %v to refuse", name, time.Since(start))
		}
	}
	if st := cb.statesSoFar(); len(st) != 0 {
		t.Errorf("a refused argument produced states %v", st)
	}
}

// The application's callbacks are code the session does not control. One that
// panics must not take the session, or the process, down.
func TestAPanickingCallbackDoesNotKillTheSession(t *testing.T) {
	dir, vfp, _ := viewerIdentity(t)
	h := startHost(t, vfp, "c", true)
	cb := newTestCallback()
	cb.panicIn = "OnTile"
	s, err := NewSession(dir, h.addr, h.fp, "c", cb)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cb.waitState(t, StateStreaming)
	if err := s.Move(5, 5); err != nil {
		t.Fatal(err)
	}
	h.waitInputs(t, 1)
	cb.waitFrames(t, 2)
}

func TestWidthAndHeightAreZeroBeforeThePicture(t *testing.T) {
	s := &Session{}
	if s.Width() != 0 || s.Height() != 0 {
		t.Errorf("size = %dx%d before any picture", s.Width(), s.Height())
	}
	var nilSession *Session
	nilSession.Close() // and a nil session is harmless to close
	if err := nilSession.ReleaseAll(); err != nil {
		t.Error(err)
	}
}

// Input comes from the UI thread, a gesture thread and a timer; the session is
// used from all of them while it is closed.
func TestConcurrentUseIsSafe(t *testing.T) {
	s, _, _ := connect(t, true)
	var wg sync.WaitGroup
	var n atomic.Int64
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch (i + g) % 6 {
				case 0:
					s.Move(i%600, i%400)
				case 1:
					s.MouseDown(g % 3)
				case 2:
					s.MouseUp(g % 3)
				case 3:
					s.KeyDown(0x41 + g)
				case 4:
					s.KeyUp(0x41 + g)
				case 5:
					s.Wheel(120)
				}
				n.Add(1)
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		s.ReleaseAll()
		s.Close()
	}()
	wg.Wait()
	if n.Load() != 800 {
		t.Fatalf("%d calls ran", n.Load())
	}
}

type quicCode = quic.ApplicationErrorCode

func appError(code quicCode) error { return &quic.ApplicationError{ErrorCode: code, Remote: true} }
