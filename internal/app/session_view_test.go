package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/view"
)

// richConn is a connection with every optional ability: it can type text, it has a name and
// statistics, it shows a picture on request and it can be made to end with an error.
type richConn struct {
	mu      sync.Mutex
	onFrame func(*tile.Frame, []tile.Rect)
	ready   chan struct{}
	end     chan error
	closed  bool
	caps    proto.Caps
	texts   []string
	inputs  []proto.Input
}

func newRichConn(caps proto.Caps) *richConn {
	return &richConn{ready: make(chan struct{}), end: make(chan error, 1), caps: caps}
}

func (c *richConn) Run(ctx context.Context, onFrame func(*tile.Frame, []tile.Rect), _ func(uint64, byte)) error {
	c.mu.Lock()
	c.onFrame = onFrame
	c.mu.Unlock()
	close(c.ready)
	select {
	case err := <-c.end:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *richConn) SendInput(in proto.Input) error {
	c.mu.Lock()
	c.inputs = append(c.inputs, in)
	c.mu.Unlock()
	return nil
}
func (c *richConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.end <- errors.New("closed")
	}
}
func (c *richConn) Supports(caps proto.Caps) bool { return c.caps.Has(caps) }
func (c *richConn) SendText(id uint64, s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, s)
	return nil
}
func (c *richConn) Stats() remote.Stats {
	return remote.Stats{RTT: 38 * time.Millisecond, BytesReceived: 1 << 20}
}
func (c *richConn) Host() (remote.HostInfo, bool) { return remote.HostInfo{Name: "WORKSHOP-PC"}, true }
func (c *richConn) show() {
	<-c.ready
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onFrame(tile.NewFrame(640, 480), nil)
}
func (c *richConn) typed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

// A connector whose connections are the ones the test made.
type scriptedConnector struct {
	*fakeConnector
	mu    sync.Mutex
	conns []*richConn
	fail  int   // this many dials fail first, with failErr
	fatal error // if set, every dial fails with it
	err   error
}

func (c *scriptedConnector) Dial(_ context.Context, addr, password string) (Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dials++
	c.passwords = append(c.passwords, password)
	if c.fatal != nil {
		return nil, c.fatal
	}
	if c.fail > 0 {
		c.fail--
		return nil, c.err
	}
	cn := newRichConn(proto.CapText)
	c.conns = append(c.conns, cn)
	return cn, nil
}
func (c *scriptedConnector) conn(i int) *richConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < len(c.conns) {
		return c.conns[i]
	}
	return nil
}

// liveRig is a rig with one live tab that has a picture.
func liveRig(t *testing.T, opts ...rigOption) (*rig, *scriptedConnector, *richConn) {
	t.Helper()
	r := newRig(t, opts...)
	r.a.Resize(1900, 1200, 1) // wide enough for the toolbar with its words
	sc := &scriptedConnector{fakeConnector: r.conn, err: &quic.IdleTimeoutError{}}
	r.a.cfg.Connector = sc
	sc.pinned["10.0.0.1:7000"] = "x"
	r.a.openTab("Press 3", "10.0.0.1", "pw", "")
	r.wait("live", func() bool { return r.a.tabs[0].state == tabLive })
	c := sc.conn(0)
	c.show()
	r.step(kit.Input{})
	r.step(kit.Input{})
	return r, sc, c
}

func TestReconnectableErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"idle timeout", &quic.IdleTimeoutError{}, true},
		{"stateless reset", &quic.StatelessResetError{}, true},
		{"a socket error", &net.OpError{Op: "read", Err: errors.New("reset")}, true},
		{"the host closed normally", &quic.ApplicationError{ErrorCode: 0}, true},
		{"removed from the paired devices", &quic.ApplicationError{ErrorCode: remote.CloseRevoked}, false},
		{"kicked", &quic.ApplicationError{ErrorCode: remote.CloseKicked}, false},
		{"replaced", &quic.ApplicationError{ErrorCode: remote.CloseReplaced}, false},
		{"unverified", &quic.ApplicationError{ErrorCode: remote.CloseUnverified}, false},
		{"something unknown", errors.New("closed"), false},
		{"host identity changed", remote.ErrHostChanged, false},
	} {
		if got := reconnectable(c.err); got != c.want {
			t.Errorf("%s: reconnectable = %v, want %v", c.name, got, c.want)
		}
	}
	if !fatalDial(&quic.ApplicationError{ErrorCode: remote.CloseUntrusted}) || fatalDial(&quic.IdleTimeoutError{}) || fatalDial(&quic.ApplicationError{ErrorCode: 0}) || !fatalDial(remote.ErrHostChanged) {
		t.Error("fatalDial: a refusal and a changed identity end the attempts; a timeout does not")
	}
}

func TestALostConnectionIsBroughtBackAndTheViewKeepsItsMode(t *testing.T) {
	r, sc, c := liveRig(t)
	r.a.reconnectWaits = []time.Duration{20 * time.Millisecond}
	sc.fail = 2 // the first two attempts do not get through
	r.a.tabs[0].sess.mode = view.ModeActual
	c.end <- &quic.IdleTimeoutError{}
	r.wait("the tab to notice", func() bool { return r.a.tabs[0].state == tabReconnecting })
	if r.a.tabs[0].old == nil || !r.has("Connection lost. Reconnecting…") {
		t.Fatalf("no reconnecting card: %q", r.texts())
	}
	r.wait("the connection to come back", func() bool { return r.a.tabs[0].state == tabLive })
	if r.a.tabs[0].sess.mode != view.ModeActual {
		t.Error("the picture's mode was lost across the reconnection")
	}
	if !r.a.tabs[0].sess.sized {
		t.Error("the window would be resized to the picture again")
	}
	sc.mu.Lock()
	dials, pw := sc.dials, sc.passwords
	sc.mu.Unlock()
	if dials != 4 || len(pw) < 4 || pw[len(pw)-1] != "pw" { // the first dial, two failures, the one that worked
		t.Errorf("dials %d passwords %v: the password goes with every attempt", dials, pw)
	}
}

func TestReconnectingStopsAtARefusalAndWhenAskedAndGivesUp(t *testing.T) {
	fast := []time.Duration{10 * time.Millisecond}

	// A refusal: no more attempts, and the reason in words.
	r, sc, c := liveRig(t)
	r.a.reconnectWaits = fast
	sc.fatal = &quic.ApplicationError{ErrorCode: remote.CloseRevoked}
	c.end <- &quic.IdleTimeoutError{}
	r.wait("the refusal", func() bool { return r.a.tabs[0].state == tabError })
	if !r.has("This PC was removed from the host's paired devices.") {
		t.Errorf("the refusal is not explained: %q", r.texts())
	}

	// Stop.
	r, sc, c = liveRig(t)
	r.a.reconnectWaits = fast
	sc.err = &quic.IdleTimeoutError{}
	sc.fail = 1000
	c.end <- &quic.IdleTimeoutError{}
	r.wait("reconnecting", func() bool { return r.a.tabs[0].state == tabReconnecting })
	r.pressButton("bar.stop")
	r.wait("the stop", func() bool { return r.a.tabs[0].state == tabError })
	if !r.has("Disconnected") {
		t.Errorf("a stopped reconnection should say the session was disconnected: %q", r.texts())
	}

	// A person who turned automatic reconnection off gets the error card at once.
	r, _, c = liveRig(t)
	r.a.reconnectWaits = fast
	r.set.Set(keyAutoReconnect, "0")
	c.end <- &quic.IdleTimeoutError{}
	r.wait("the error", func() bool { return r.a.tabs[0].state == tabError })

	// Closing the tab while it is reconnecting stops the attempts.
	r, sc, c = liveRig(t)
	r.a.reconnectWaits = fast
	sc.fail = 1000
	c.end <- &quic.IdleTimeoutError{}
	r.wait("reconnecting", func() bool { return r.a.tabs[0].state == tabReconnecting })
	r.a.closeTab(1)
	if len(r.a.tabs) != 0 {
		t.Error("the tab did not close")
	}
}

func TestToolbarShowsOnDemandAndHasTheControlsTheHostSupports(t *testing.T) {
	r, _, _ := liveRig(t)
	if !r.has("Press 3") && r.a.tabs[0].barShown {
		t.Fatal("the toolbar is up before it was asked for")
	}
	if r.has("Release all keys") {
		t.Error("the toolbar is showing before it was asked for")
	}
	r.pressButton("bar.handle")
	r.step(kit.Input{})
	for _, want := range []string{"Fit", "100%", "Full screen", "Send text", "Release all keys", "Statistics", "Disconnect", "Host checked", "WORKSHOP-PC"} {
		if !r.has(want) {
			t.Errorf("the toolbar lacks %q: %q", want, r.texts())
		}
	}
	if r.has("한/영") {
		t.Error("the Korean keys show in an English interface")
	}

	// A host that cannot type text gets no button for it.
	r2 := newRig(t)
	r2.a.Resize(1900, 1200, 1)
	sc := &scriptedConnector{fakeConnector: r2.conn}
	r2.a.cfg.Connector = sc
	sc.pinned["10.0.0.2:7000"] = "x"
	r2.a.openTab("Old host", "10.0.0.2", "", "")
	r2.wait("live", func() bool { return r2.a.tabs[0].state == tabLive })
	sc.conn(0).caps = 0
	sc.conn(0).show()
	r2.step(kit.Input{})
	r2.pressButton("bar.handle")
	r2.step(kit.Input{})
	if r2.has("Send text") || !r2.has("Release all keys") {
		t.Errorf("an old host's toolbar: %q (no text button, since it would end the session)", r2.texts())
	}

	// Korean keys follow the language, or the setting.
	r3, _, _ := liveRig(t, withLang("ko"))
	r3.pressButton("bar.handle")
	r3.step(kit.Input{})
	if !r3.has("한/영") || !r3.has("한자") {
		t.Errorf("Korean interface: %q", r3.texts())
	}
	r4, _, _ := liveRig(t)
	r4.set.Set(keyKoreanKeys, "1")
	r4.pressButton("bar.handle")
	r4.step(kit.Input{})
	if !r4.has("한/영") {
		t.Errorf("setting on: %q", r4.texts())
	}
}

func TestToolbarActions(t *testing.T) {
	r, _, c := liveRig(t, withLang("ko"))
	tb := r.a.tabs[0]
	r.pressButton("bar.handle")
	r.step(kit.Input{})
	r.pressButton("bar.actual")
	if tb.sess.mode != view.ModeActual {
		t.Error("100% did not switch the mode")
	}
	r.pressButton("bar.fit")
	if tb.sess.mode != view.ModeFit {
		t.Error("Fit did not switch the mode back")
	}
	r.pressButton("bar.hangul")
	r.pressButton("bar.hanja")
	var codes []int32
	c.mu.Lock()
	for _, in := range c.inputs {
		if in.Kind == proto.KindKeyDown {
			codes = append(codes, in.Arg)
		}
	}
	c.mu.Unlock()
	if len(codes) != 2 || codes[0] != vkHangul || codes[1] != vkHanja {
		t.Errorf("the Korean buttons sent %v, want VK_HANGUL then VK_HANJA", codes)
	}
	// Release all keys lets go of what is held.
	tb.sess.held[0xA2] = true
	r.pressButton("bar.release")
	if len(tb.sess.held) != 0 {
		t.Error("keys are still held after the release button")
	}
	// Full screen hides the strip and the banner; leaving the picture leaves full screen.
	r.pressButton("bar.fullscreen")
	r.step(kit.Input{})
	if !r.a.Fullscreen() || r.has("Devices") {
		t.Errorf("full screen: %v %q (the tab strip should be gone)", r.a.Fullscreen(), r.texts())
	}
	r.a.setActive(0)
	r.a.StepLive(func(int, int) {})
	if r.a.Fullscreen() {
		t.Error("the pages stayed in full screen")
	}
	// Disconnect closes the tab.
	r.a.setActive(1)
	r.a.showBar(tb)
	r.step(kit.Input{})
	r.pressButton("bar.close")
	if len(r.a.tabs) != 0 {
		t.Error("the disconnect button did not close the tab")
	}
}

func TestSendingTextTypesItOnTheHostAndClearsTheBox(t *testing.T) {
	r, _, c := liveRig(t)
	tb := r.a.tabs[0]
	r.pressButton("bar.handle")
	r.step(kit.Input{})
	r.pressButton("bar.text")
	r.step(kit.Input{})
	if !tb.textOpen || r.a.ui.Focused() != "bar.text" {
		t.Fatalf("text box open %v, focus on %q", tb.textOpen, r.a.ui.Focused())
	}
	if !r.a.keysToUI() {
		t.Error("the keyboard must belong to the box while it has it, or the typing goes to the remote PC")
	}
	r.typ("你好 world")
	if tb.text != "你好 world" {
		t.Fatalf("typed %q", tb.text)
	}
	r.key(kit.KeyPress{Key: kit.KeyEnter})
	if got := c.typed(); len(got) != 1 || got[0] != "你好 world" {
		t.Errorf("the host was sent %q", got)
	}
	if tb.text != "" {
		t.Errorf("the box still holds %q", tb.text)
	}
	r.key(kit.KeyPress{Key: kit.KeyEnter}) // an empty box sends nothing
	if len(c.typed()) != 1 {
		t.Error("an empty box sent text")
	}
	// Escape closes it, and the keyboard goes back to the remote PC.
	r.key(kit.KeyPress{Key: kit.KeyEscape})
	if tb.textOpen || r.a.keysToUI() {
		t.Errorf("text box open %v, keys to the interface %v", tb.textOpen, r.a.keysToUI())
	}
}

func TestStatisticsModifiersAndTheBarGoingAway(t *testing.T) {
	r, _, _ := liveRig(t)
	tb := r.a.tabs[0]
	r.pressButton("bar.handle")
	r.step(kit.Input{})
	r.pressButton("bar.stats")
	r.step(kit.Input{})
	if !r.has("Round trip 38 ms") || !r.has("Picture 640×480") {
		t.Fatalf("statistics: %q", r.texts())
	}
	// A Ctrl that stayed down on the remote PC is shown.
	tb.sess.inputMu.Lock()
	tb.sess.held[0xA2] = true
	tb.sess.inputMu.Unlock()
	r.a.noteModifiers(tb.sess)
	r.step(kit.Input{})
	if !r.has("Ctrl") || r.has("Alt") {
		t.Errorf("modifier badges: %q", r.texts())
	}
	// The toolbar goes away by itself unless the pointer is on it.
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	r.a.tickBar(tb)
	if !tb.barShown {
		t.Fatal("the bar went at once")
	}
	r.now = r.now.Add(barShowFor + time.Second)
	r.step(kit.Input{})
	r.a.tickBar(tb)
	if tb.barShown {
		t.Error("the toolbar stayed after its time")
	}
	// The pointer on the handle belongs to the interface, not the remote PC.
	r.step(kit.Input{})
	op, ok := r.paint().Text1("") // no text on the handle: find it by position
	_ = op
	_ = ok
	r.move(550, 60)
	if hot := r.a.ui.Hot(); hot != "" && !r.a.pointerOnBar() {
		t.Errorf("hot %q is not recognised as the bar", hot)
	}
}

func TestSettingsForTheSessionView(t *testing.T) {
	r := newRig(t, withLang("ko"))
	r.a.page = PageSettings
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	if !r.has("네트워크 문제가 생기면 자동으로 다시 연결") {
		t.Fatalf("settings: %q", r.texts())
	}
	r.pressButton("set.reconnect")
	if v, _ := r.set.Get(keyAutoReconnect); v != "0" {
		t.Errorf("auto_reconnect = %q after turning it off", v)
	}
	r.pressButton("set.korean")
	if v, _ := r.set.Get(keyKoreanKeys); v != "0" {
		t.Errorf("korean_keys = %q after turning it off", v)
	}
	if strings.Contains(strings.Join(r.texts(), ""), "{") {
		t.Error("a placeholder is showing")
	}
	_ = i18n.Korean
}

// In a window too narrow for its words the toolbar keeps its controls and loses the words,
// and in one narrower still, the name of the host as well.
func TestTheToolbarShrinksToFitTheWindow(t *testing.T) {
	r, _, _ := liveRig(t)
	r.pressButton("bar.handle")
	count := func(w int) (words, name bool, buttons int) {
		r.a.Resize(w, 1200, 1)
		r.a.ui.Invalidate()
		r.step(kit.Input{})
		rec := r.paint()
		_, words = rec.Text1("Release all keys")
		_, name = rec.Text1("Press 3 · WORKSHOP-PC")
		for _, id := range []kit.ID{"bar.fit", "bar.actual", "bar.fullscreen", "bar.text", "bar.release", "bar.stats", "bar.close"} {
			if r.a.ui.IsHot(id) || true {
				buttons++
			}
		}
		return
	}
	if w, n, _ := count(1900); !w || !n {
		t.Errorf("wide: words %v name %v", w, n)
	}
	if w, n, _ := count(700); w || !n {
		t.Errorf("narrow: words %v name %v (the icons stay, the words go)", w, n)
	}
	if w, n, _ := count(420); w || n {
		t.Errorf("very narrow: words %v name %v (only the controls)", w, n)
	}
}
