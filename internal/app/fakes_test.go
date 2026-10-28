package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/logging"
	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/update"
)

// fakeHost is the sharing side as the pages see it.
type fakeHost struct {
	mu            sync.Mutex
	info          HostInfo
	password      string
	paired        []PairedDevice
	calls         []string
	endedOnStrict int
}

func newFakeHost() *fakeHost {
	return &fakeHost{info: HostInfo{Addrs: []string{"192.168.1.5"}, Port: 7000, ID: "9f3a1c77d0e24b58", RequireAuth: true}}
}

func (h *fakeHost) Info() HostInfo  { h.mu.Lock(); defer h.mu.Unlock(); return h.info }
func (h *fakeHost) record(s string) { h.calls = append(h.calls, s) }
func (h *fakeHost) StartPairing() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.record("pair")
	h.info.PairingLeft, h.info.PairingCode = 2*time.Minute, "123456"
}
func (h *fakeHost) ClosePairing() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.record("close-pairing")
	h.info.PairingLeft, h.info.PairingCode = 0, ""
}
func (h *fakeHost) SetRequireAuth(on bool) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.info.RequireAuth = on
	if on {
		h.record("strict")
		return h.endedOnStrict
	}
	h.record("open")
	return 0
}
func (h *fakeHost) Password() string { return h.password }
func (h *fakeHost) SetPassword(p string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.password = p
	h.record("password:" + p)
}
func (h *fakeHost) Paired() []PairedDevice {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]PairedDevice(nil), h.paired...)
}
func (h *fakeHost) Revoke(fp string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.record("revoke:" + fp)
	for i, d := range h.paired {
		if d.Fingerprint == fp {
			h.paired = append(h.paired[:i], h.paired[i+1:]...)
			break
		}
	}
	h.info.Paired = len(h.paired)
	return nil
}
func (h *fakeHost) RevokeAll() (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.paired)
	h.paired, h.info.Paired = nil, 0
	h.record("revoke-all")
	return n, nil
}
func (h *fakeHost) DisconnectController() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.record("kick")
	h.info.Controller = ""
}
func (h *fakeHost) called(s string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c == s {
			return true
		}
	}
	return false
}

// memSettings is the settings store in memory.
type memSettings struct{ m map[string]string }

func (s *memSettings) Get(k string) (string, bool) { v, ok := s.m[k]; return v, ok }
func (s *memSettings) Set(k, v string) error {
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = v
	return nil
}

type memClip struct{ s string }

func (c *memClip) Get() string  { return c.s }
func (c *memClip) Set(s string) { c.s = s }

// fakePlatform is the operating system.
type fakePlatform struct {
	clip     memClip
	dark     bool
	darkOK   bool
	langs    []string
	quit     bool
	hidden   bool
	themeSet []bool
}

func (p *fakePlatform) Quit()                        { p.quit = true }
func (p *fakePlatform) HideWindow()                  { p.hidden = true }
func (p *fakePlatform) WindowHidden() bool           { return p.hidden }
func (p *fakePlatform) Clipboard() kit.Clipboard     { return &p.clip }
func (p *fakePlatform) SystemDark() (bool, bool)     { return p.dark, p.darkOK }
func (p *fakePlatform) PreferredLanguages() []string { return p.langs }
func (p *fakePlatform) ApplyTheme(dark bool)         { p.themeSet = append(p.themeSet, dark) }
func (p *fakePlatform) MessageBox(string, string)    {}

// fakeUpdater is the updater.
type fakeUpdater struct {
	mu      sync.Mutex
	st      update.Status
	pending bool
	clicks  int
}

func (u *fakeUpdater) Status() update.Status { u.mu.Lock(); defer u.mu.Unlock(); return u.st }
func (u *fakeUpdater) Click()                { u.mu.Lock(); defer u.mu.Unlock(); u.clicks++ }
func (u *fakeUpdater) RestartPending() bool  { u.mu.Lock(); defer u.mu.Unlock(); return u.pending }
func (u *fakeUpdater) set(st update.Status, pending bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.st, u.pending = st, pending
}

// fakeConn is a connection that does nothing but stay open until closed, or, with refused
// set, one the host hangs up on as soon as it has read the hello.
type fakeConn struct {
	done    chan struct{}
	closed  bool
	sent    []proto.Input
	mu      sync.Mutex
	refused error
}

func newFakeConn() *fakeConn { return &fakeConn{done: make(chan struct{})} }
func (c *fakeConn) Run(ctx context.Context, _ func(*tile.Frame, []tile.Rect), _ func(uint64, byte)) error {
	if c.refused != nil {
		return c.refused
	}
	select {
	case <-c.done:
	case <-ctx.Done():
	}
	return errors.New("closed")
}
func (c *fakeConn) SendInput(in proto.Input) error {
	c.mu.Lock()
	c.sent = append(c.sent, in)
	c.mu.Unlock()
	return nil
}
func (c *fakeConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
}

// fakeConnector decides how connections go.
type fakeConnector struct {
	mu        sync.Mutex
	pinned    map[string]string
	probeFP   string
	probeErr  error
	dialErr   error
	dials     int
	conns     []*fakeConn
	forgot    []string
	passwords []string
	block     chan struct{} // if set, Probe waits for it to be closed
	refuse    error         // if set, the host refuses each connection after the hello with this
}

func newFakeConnector() *fakeConnector {
	return &fakeConnector{pinned: map[string]string{}, probeFP: "a1b2c3d4e5f67a8b99aabbccddeeff00"}
}
func (c *fakeConnector) Pinned(addr string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fp, ok := c.pinned[addr]
	return fp, ok
}
func (c *fakeConnector) Probe(ctx context.Context, _ string) (string, error) {
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return c.probeFP, c.probeErr
}
func (c *fakeConnector) Pin(addr, fp string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinned[addr] = fp
	return nil
}
func (c *fakeConnector) Forget(addr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pinned, addr)
	c.forgot = append(c.forgot, addr)
	return nil
}
func (c *fakeConnector) Dial(_ context.Context, addr, password string) (Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dials++
	c.passwords = append(c.passwords, password)
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	cn := newFakeConn()
	cn.refused = c.refuse
	c.conns = append(c.conns, cn)
	return cn, nil
}

// rig is an App with everything faked, driven frame by frame.
type rig struct {
	t     *testing.T
	a     *App
	host  *fakeHost
	set   *memSettings
	plat  *fakePlatform
	conn  *fakeConnector
	upd   *fakeUpdater
	store *profiles.List
	logs  *logging.Logger
	now   time.Time
	x, y  float64
	in    bool
	down  bool
}

type rigOption func(*rig)

func withLang(l string) rigOption { return func(r *rig) { r.set.Set(keyLang, l) } }
func withTheme(m string) rigOption {
	return func(r *rig) { r.set.Set(keyTheme, m) }
}
func withUpdater() rigOption { return func(r *rig) { r.upd = &fakeUpdater{} } }

func newRig(t *testing.T, opts ...rigOption) *rig {
	t.Helper()
	r := &rig{t: t, host: newFakeHost(), set: &memSettings{}, plat: &fakePlatform{langs: []string{"en-US"}}, conn: newFakeConnector(),
		logs: logging.New(), now: time.Date(2026, 10, 28, 9, 30, 0, 0, time.UTC)}
	store, err := profiles.Load(filepath.Join(t.TempDir(), "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	r.store = store
	for _, o := range opts {
		o(r)
	}
	cfg := Config{Version: "0.7.0", Host: r.host, Profiles: r.store, Settings: r.set, Connector: r.conn, Platform: r.plat, Logs: r.logs, LogPath: `C:\Users\me\AppData\Roaming\go-nx\go-nx.log`,
		Now: func() time.Time { return r.now }}
	if r.upd != nil {
		cfg.Updater = r.upd
	}
	r.a = New(cfg, FixedText{})
	r.a.Resize(1100, 1800, 1) // tall enough for a whole page: scrolling is tested on its own
	r.step(kit.Input{})
	return r
}

func (r *rig) step(in kit.Input) {
	r.t.Helper()
	r.now = r.now.Add(20 * time.Millisecond)
	in.Now = r.now
	in.X, in.Y, in.Pointer = r.x, r.y, r.in
	in.Down[kit.Left] = r.down
	in.Focused = true
	r.a.Step(in)
}

func (r *rig) move(x, y float64) { r.x, r.y, r.in = x, y, true; r.step(kit.Input{}) }
func (r *rig) click(x, y float64) {
	r.move(x, y)
	r.down = true
	in := kit.Input{}
	in.Pressed[kit.Left] = true
	r.step(in)
	r.down = false
	in = kit.Input{}
	in.Released[kit.Left] = true
	r.step(in)
}
func (r *rig) key(keys ...kit.KeyPress) { r.step(kit.Input{Keys: keys}) }
func (r *rig) typ(s string)             { r.step(kit.Input{Text: s}) }

// paint returns what the application draws now.
func (r *rig) paint() *kit.Recorder {
	rec := &kit.Recorder{}
	r.a.ui.Paint(rec)
	return rec
}

// texts is every string drawn.
func (r *rig) texts() []string { return r.paint().Texts() }

// has reports whether s is on screen: a drawn string, or part of what the strings say
// when a long sentence has been wrapped onto several lines.
func (r *rig) has(s string) bool {
	texts := r.texts()
	for _, x := range texts {
		if x == s {
			return true
		}
	}
	return len(s) > 2 && strings.Contains(strings.Join(texts, " "), s)
}

// clickText clicks the centre of the first drawn text that is exactly s.
func (r *rig) clickText(s string) {
	r.t.Helper()
	op, ok := r.paint().Text1(s)
	if !ok {
		r.t.Fatalf("no text %q on screen; there is %q", s, r.texts())
	}
	w := (FixedText{}).Width(s, op.Style)
	r.click(op.R.X+w/2, op.R.Y+4)
}

// pressButton activates the button with this ID through the keyboard path (focus and
// Enter), which does not depend on where it is on the screen.
func (r *rig) pressButton(id kit.ID) {
	r.t.Helper()
	r.a.ui.Focus(id, true)
	r.key(kit.KeyPress{Key: kit.KeyEnter})
}

func (r *rig) tr() *i18n.Translator { return r.a.tr }

// wait runs frames until cond holds (other goroutines, like a dial, take a moment).
func (r *rig) wait(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second) // generous: the machine may be busy
	for time.Now().Before(deadline) {
		r.step(kit.Input{})
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}
