package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"math/rand"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

func identity(t *testing.T) tls.Certificate {
	t.Helper()
	id, err := trust.Identity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func hostsStore(t *testing.T) *trust.Store {
	t.Helper()
	s, err := trust.OpenStore(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fp(id tls.Certificate) string { return trust.Fingerprint(id.Certificate[0]) }

type fakeSource struct {
	mu    sync.Mutex
	f     *tile.Frame
	grabs atomic.Int64 // how many times the host has looked at the screen
}

func newFakeSource(w, h int) *fakeSource { return &fakeSource{f: tile.NewFrame(w, h)} }

func (s *fakeSource) Size() (int, int) { return s.f.W, s.f.H }

func (s *fakeSource) Grab(dst *tile.Frame) error {
	s.grabs.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	copy(dst.Pix, s.f.Pix)
	return nil
}

// paint sets one pixel's blue channel, changing exactly one tile.
func (s *fakeSource) paint(x, y int, v byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f.Pix[(y*s.f.W+x)*4] = v
}

type fakeInjector struct {
	mu    sync.Mutex
	seen  []proto.Input
	texts []string
}

// InjectText makes fakeInjector a TextInjector.
func (i *fakeInjector) InjectText(s string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.texts = append(i.texts, s)
	return nil
}

func (i *fakeInjector) typed() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]string(nil), i.texts...)
}

func (i *fakeInjector) Inject(in proto.Input) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.seen = append(i.seen, in)
	return nil
}

func (i *fakeInjector) count(kind byte) (n int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, in := range i.seen {
		if in.Kind == kind {
			n++
		}
	}
	return
}

type rig struct {
	src     *fakeSource
	inj     *fakeInjector
	client  *Client
	mu      sync.Mutex
	view    []byte
	recv    map[uint64]byte
	served  chan error
	dialErr error
}

// start runs a host and connects a viewer. The host trusts the viewer's
// identity unless trustViewer is false.
func start(t *testing.T, opt Options, trustViewer bool, onFrameDelay time.Duration) *rig {
	t.Helper()
	viewerID, hostID := identity(t), identity(t)
	return startWith(t, opt, hostID, viewerID, hostsStore(t), trustViewer, onFrameDelay)
}

func startWith(t *testing.T, opt Options, hostID, viewerID tls.Certificate, hosts *trust.Store, trustViewer bool, onFrameDelay time.Duration) *rig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	ln, err := Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	r := &rig{src: newFakeSource(150, 90), inj: &fakeInjector{}, recv: map[uint64]byte{}, served: make(chan error, 1)}
	if opt.Check == nil {
		opt.Check = func(f, _, _ string) (trust.Verdict, error) {
			if trustViewer && f == fp(viewerID) {
				return trust.Allowed, nil
			}
			return trust.Untrusted, nil
		}
	}
	if opt.FPS == 0 {
		opt.FPS = 50
	}
	dd := proto.NewDedup()
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			r.served <- err
			return
		}
		r.served <- Serve(ctx, conn, r.src, r.inj, dd, opt)
	}()

	r.client, err = Dial(ctx, ln.Addr().String(), viewerID, hosts, "pw")
	if err != nil {
		r.dialErr = err
		return r
	}
	t.Cleanup(r.client.Close)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		r.client.Run(ctx, func(f *tile.Frame, _ []tile.Rect) {
			time.Sleep(onFrameDelay)
			r.mu.Lock()
			r.view = append(r.view[:0], f.Pix...)
			r.mu.Unlock()
		}, func(id uint64, st byte) {
			r.mu.Lock()
			r.recv[id] = st
			r.mu.Unlock()
		})
	}()
	// A v1 host introduces itself before anything else, and a host that refuses the
	// viewer closes the connection instead; either way the test can start.
	select {
	case <-r.client.welcome:
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Error("the host neither introduced itself nor closed the connection")
	}
	return r
}

func (r *rig) waitView(t *testing.T, want []byte) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.src.mu.Lock()
		want = append(want[:0], r.src.f.Pix...)
		r.src.mu.Unlock()
		r.mu.Lock()
		ok := bytes.Equal(r.view, want)
		r.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("client view never converged to host screen")
}

func TestFramesConverge(t *testing.T) {
	r := start(t, Options{}, true, 0)
	r.waitView(t, nil)
	r.src.paint(140, 80, 200)
	r.waitView(t, nil)
	r.src.paint(3, 3, 99)
	r.waitView(t, nil)
}

func TestFramesConvergeWhenAcksAreLate(t *testing.T) {
	// The client takes 60ms per frame but the host cancels after 10ms, so frames
	// are cancelled and their tiles resent; the view must still converge.
	r := start(t, Options{AckTimeout: 10 * time.Millisecond}, true, 60*time.Millisecond)
	for i := 0; i < 5; i++ {
		r.src.paint(i*30, i*15, byte(10+i))
		time.Sleep(15 * time.Millisecond)
	}
	r.waitView(t, nil)
}

func TestInputReceiptAndDedup(t *testing.T) {
	r := start(t, Options{}, true, 0)
	click := proto.Input{OpID: 7, Kind: proto.KindMouseDown, X: 10, Y: 20}
	for i := 0; i < 3; i++ { // the same opID sent repeatedly
		if err := r.client.SendInput(click); err != nil {
			t.Fatal(err)
		}
	}
	r.client.SendInput(proto.Input{Kind: proto.KindMouseMove, X: 1, Y: 1}) // no receipt
	r.client.SendInput(proto.Input{OpID: 8, Kind: proto.KindKeyDown, Arg: 65})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.recv)
		r.mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recv[7] != proto.StatusExecuted || r.recv[8] != proto.StatusExecuted || len(r.recv) != 2 {
		t.Fatalf("receipts = %v", r.recv)
	}
	if got := r.inj.count(proto.KindMouseDown); got != 1 {
		t.Fatalf("click injected %d times, want 1", got)
	}
	if got := r.inj.count(proto.KindMouseMove); got != 1 {
		t.Fatalf("move injected %d times, want 1", got)
	}
}

func TestInputControllerReleasesOnTakeoverAndIgnoresStaleSession(t *testing.T) {
	inj := &fakeInjector{}
	controller := NewInputController(inj)
	first, second := controller.newSession(), controller.newSession()

	if err := first.Activate(nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Inject(proto.Input{Kind: proto.KindKeyDown, Arg: 0xA2}); err != nil {
		t.Fatal(err)
	}
	if err := first.Inject(proto.Input{Kind: proto.KindMouseDown, Arg: 0, X: 12, Y: 34}); err != nil {
		t.Fatal(err)
	}
	if err := second.Activate(nil); err != nil {
		t.Fatal(err)
	}
	if err := second.Inject(proto.Input{Kind: proto.KindKeyDown, Arg: 0xA3}); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil { // a late old-session exit must not release RCtrl
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}

	inj.mu.Lock()
	defer inj.mu.Unlock()
	want := []proto.Input{
		{Kind: proto.KindKeyDown, Arg: 0xA2},
		{Kind: proto.KindMouseDown, Arg: 0, X: 12, Y: 34},
		{Kind: proto.KindMouseUp, Arg: 0, X: proto.NoMove, Y: proto.NoMove}, // buttons first, where the pointer is
		{Kind: proto.KindKeyUp, Arg: 0xA2},
		{Kind: proto.KindKeyDown, Arg: 0xA3},
		{Kind: proto.KindKeyUp, Arg: 0xA3},
	}
	if len(inj.seen) != len(want) {
		t.Fatalf("injected %d events, want %d: %+v", len(inj.seen), len(want), inj.seen)
	}
	for i := range want {
		if inj.seen[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, inj.seen[i], want[i])
		}
	}
}

func TestServeReleasesHeldKeysOnDisconnect(t *testing.T) {
	viewerID, hostID := identity(t), identity(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	inj := &fakeInjector{}
	controller := NewInputController(inj)
	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			served <- err
			return
		}
		served <- Serve(ctx, conn, newFakeSource(64, 64), inj, proto.NewDedup(), Options{
			FPS:             50,
			InputController: controller,
			Check:           func(_, _, _ string) (trust.Verdict, error) { return trust.Allowed, nil },
		})
	}()

	cl, err := Dial(ctx, ln.Addr().String(), viewerID, hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: 0xA2}); err != nil { // VK_LCONTROL
		t.Fatal(err)
	}
	waitForInputCount(t, inj, proto.KindKeyDown, 1)
	cl.Close() // Simulate a dropped connection before Ctrl key-up reaches the host.

	select {
	case <-served:
	case <-ctx.Done():
		t.Fatal("Serve did not end after client disconnect")
	}
	waitForInputCount(t, inj, proto.KindKeyUp, 1)
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if n := len(inj.seen); n != 2 || inj.seen[0].Kind != proto.KindKeyDown || inj.seen[1].Kind != proto.KindKeyUp || inj.seen[1].Arg != 0xA2 {
		t.Fatalf("injected inputs = %+v, want Ctrl down then Ctrl up", inj.seen)
	}
}

func waitForInputCount(t *testing.T, inj *fakeInjector, kind byte, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if inj.count(kind) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("input kind %d count reached %d, never %d", kind, inj.count(kind), want)
}

func TestUntrustedViewerRejected(t *testing.T) {
	r := start(t, Options{}, false, 0)
	select {
	case err := <-r.served:
		if err != ErrDenied {
			t.Fatalf("Serve = %v, want ErrDenied", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not reject the untrusted viewer")
	}
	if r.client != nil {
		r.client.SendInput(proto.Input{OpID: 1, Kind: proto.KindMouseDown})
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(r.inj.seen); n != 0 {
		t.Fatalf("%d inputs injected for an untrusted viewer", n)
	}
}

func TestHostPinnedAfterFirstConnection(t *testing.T) {
	viewerID, hostA, hostB := identity(t), identity(t), identity(t)
	hosts := hostsStore(t)
	r := startWith(t, Options{}, hostA, viewerID, hosts, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.waitView(t, nil)
	if got, _ := hosts.Get("127.0.0.1"); got != fp(hostA) {
		t.Fatalf("pinned %q, want host A's fingerprint", got)
	}
	// A different machine answering at the same address must be refused.
	r2 := startWith(t, Options{}, hostB, viewerID, hosts, true, 0)
	if r2.dialErr == nil || !strings.Contains(r2.dialErr.Error(), ErrHostChanged.Error()) {
		t.Fatalf("dial to changed host = %v, want %q", r2.dialErr, ErrHostChanged)
	}
}

func TestWrongPasswordReportedToViewer(t *testing.T) {
	seen := make(chan string, 1)
	opt := Options{Check: func(_, _, pw string) (trust.Verdict, error) {
		seen <- pw
		return trust.BadPassword, nil
	}}
	viewerID, hostID := identity(t), identity(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if conn, err := ln.Accept(ctx); err == nil {
			Serve(ctx, conn, newFakeSource(64, 64), &fakeInjector{}, proto.NewDedup(), opt)
		}
	}()
	cl, err := Dial(ctx, ln.Addr().String(), viewerID, hostsStore(t), "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	err = cl.Run(ctx, func(*tile.Frame, []tile.Rect) {}, nil)
	var ae *quic.ApplicationError
	if !errors.As(err, &ae) || ae.ErrorCode != CloseBadPassword {
		t.Fatalf("Run = %v, want application error %d", err, CloseBadPassword)
	}
	if got := <-seen; got != "hunter2" {
		t.Fatalf("host saw password %q", got)
	}
}

// The first update after connecting is a whole screen. A fixed ack/write timeout
// would cancel and resend it forever whenever sending it takes longer than the
// timeout (cold congestion window, slow Wi-Fi), so the viewer would never get a picture.
func TestLargeFirstFrameIsNotCancelledForever(t *testing.T) {
	big := newFakeSource(1280, 720)
	rand.New(rand.NewSource(7)).Read(big.f.Pix) // incompressible: ~3.6 MB on the wire

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	viewerID, hostID := identity(t), identity(t)
	ln, err := Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if conn, err := ln.Accept(ctx); err == nil {
			Serve(ctx, conn, big, &fakeInjector{}, proto.NewDedup(), Options{
				FPS: 50, AckTimeout: 2 * time.Millisecond,
				Check: func(_, _, _ string) (trust.Verdict, error) { return trust.Allowed, nil },
			})
		}
	}()
	cl, err := Dial(ctx, ln.Addr().String(), viewerID, hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	got := make(chan struct{}, 1)
	go cl.Run(ctx, func(f *tile.Frame, _ []tile.Rect) {
		if bytes.Equal(f.Pix, big.f.Pix) {
			select {
			case got <- struct{}{}:
			default:
			}
		}
	}, nil)
	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("the full first frame never arrived: it is cancelled and resent before it can finish")
	}
}

// After the user decides to trust a host's new identity, ForgetHost lets the
// next connection pin it; nothing else changes the pin.
func TestForgetHostLetsAChangedHostBePinnedAgain(t *testing.T) {
	viewerID, hostA, hostB := identity(t), identity(t), identity(t)
	hosts := hostsStore(t)
	if r := startWith(t, Options{}, hostA, viewerID, hosts, true, 0); r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	if r := startWith(t, Options{}, hostB, viewerID, hosts, true, 0); r.dialErr == nil {
		t.Fatal("changed host was accepted before ForgetHost")
	}
	if err := ForgetHost(hosts, "127.0.0.1:7000"); err != nil { // the port is not part of the pin
		t.Fatal(err)
	}
	if _, ok := hosts.Get("127.0.0.1"); ok {
		t.Fatal("pin still present after ForgetHost")
	}
	if r := startWith(t, Options{}, hostB, viewerID, hosts, true, 0); r.dialErr != nil {
		t.Fatalf("dial after ForgetHost: %v", r.dialErr)
	}
	if got, _ := hosts.Get("127.0.0.1"); got != fp(hostB) {
		t.Fatalf("pinned %q, want host B's fingerprint", got)
	}
}

// The whole pairing story over real connections: a stranger is turned away, the
// right code pairs it once, and from then on it connects with no password.
func TestPairingCodeEndToEnd(t *testing.T) {
	store, err := trust.OpenStore(filepath.Join(t.TempDir(), "trusted.json"))
	if err != nil {
		t.Fatal(err)
	}
	policy := trust.NewPolicy(store)
	policy.SetRequireAuth(true)
	policy.OpenPairing("123456", time.Minute)
	opt := Options{Check: policy.Check}

	viewerID, hostID := identity(t), identity(t)
	hosts := hostsStore(t)

	dial := func(password string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ln, err := Listen("127.0.0.1:0", hostID)
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
			served <- Serve(ctx, conn, newFakeSource(64, 64), &fakeInjector{}, proto.NewDedup(), opt)
		}()
		c, err := Dial(ctx, ln.Addr().String(), viewerID, hosts, password)
		if err != nil {
			return err
		}
		defer c.Close()
		select {
		case err := <-served:
			if errors.Is(err, ErrDenied) {
				return err
			}
		case <-time.After(300 * time.Millisecond): // still serving: the host let it in
		}
		return nil
	}

	if err := dial(""); !errors.Is(err, ErrDenied) {
		t.Fatalf("no code: %v, want the host to refuse", err)
	}
	if err := dial("000000"); !errors.Is(err, ErrDenied) {
		t.Fatalf("wrong code: %v, want the host to refuse", err)
	}
	if store.Len() != 0 {
		t.Fatal("a device was paired without the code")
	}
	if err := dial("123456"); err != nil {
		t.Fatalf("right code: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("paired devices = %d, want 1", store.Len())
	}
	if err := dial(""); err != nil {
		t.Fatalf("paired device with no password: %v", err)
	}
}
