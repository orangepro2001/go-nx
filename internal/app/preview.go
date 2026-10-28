package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/orangepro2001/go-nx/internal/logging"
	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/update"
)

// Preview is the application with made-up data, standing still in one of its screens: for
// screenshots (tools/uipreview) and for tests that sweep every screen. Nothing in it
// connects anywhere or touches the machine.
type Preview struct {
	App   *App
	host  *previewHost
	upd   *previewUpdater
	logs  *logging.Logger
	prof  *profiles.List
	now   time.Time
	conns []*previewConn
}

// Close ends the connections of the screens that were shown.
func (p *Preview) Close() {
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = nil
}

// SceneNames are the screens a Preview can show.
func SceneNames() []string {
	names := make([]string, len(previewScenes))
	for i, s := range previewScenes {
		names[i] = s.name
	}
	return names
}

// NewPreview makes the application in language lang and the dark or light theme, w by h
// device pixels at the given scale, with fonts supplying the text. dir is a scratch
// directory for the saved devices.
func NewPreview(fonts Fonts, lang i18n.Lang, dark bool, w, h int, scale float64, dir string) (*Preview, error) {
	p := &Preview{
		host: &previewHost{info: HostInfo{Addrs: []string{"192.168.1.5", "10.0.0.7"}, LocalIPs: []string{"192.168.1.5", "10.0.0.7", "100.109.77.29"},
			Port: 7000, ID: "9f3a1c77d0e24b58", RequireAuth: true, Lockout: true}},
		upd:  &previewUpdater{},
		logs: logging.New(),
		now:  time.Date(2026, 10, 28, 9, 30, 0, 0, time.Local),
	}
	prof, err := profiles.Load(filepath.Join(dir, "connections.json"))
	if err != nil {
		return nil, err
	}
	p.prof = prof
	theme := "light"
	if dark {
		theme = "dark"
	}
	set := &previewSettings{m: map[string]string{keyLang: lang.Tag(), keyTheme: theme}}
	p.App = New(Config{
		Version: "0.7.0", Host: p.host, Profiles: prof, Settings: set, Connector: previewConnector{}, Updater: p.upd,
		Platform: previewPlatform{}, Logs: p.logs, LogPath: `C:\Users\you\AppData\Roaming\go-nx\go-nx.log`,
		Now: func() time.Time { return p.now },
	}, fonts)
	p.App.Resize(w, h, scale)
	return p, nil
}

// Show puts the application in the screen called name and settles it.
func (p *Preview) Show(name string) error {
	for _, s := range previewScenes {
		if s.name == name {
			s.setup(p)
			p.App.ui.Invalidate()
			p.Step()
			p.Step()
			return nil
		}
	}
	return fmt.Errorf("no screen called %q", name)
}

// Step runs one frame with no input.
func (p *Preview) Step() {
	p.now = p.now.Add(20 * time.Millisecond)
	p.App.Step(kit.Input{Now: p.now, Focused: true})
}

type previewScene struct {
	name  string
	setup func(p *Preview)
}

func (p *Preview) devices() {
	names := []string{"Line 1 - Pixel Inspector", "办公室 8 号", "공장 3호기 최종 검사", "Final inspection station with a very long name indeed", "Office"}
	for i, n := range names {
		p.prof.Upsert(profiles.Profile{Name: n, Addr: fmt.Sprintf("192.168.1.%d:7000", 20+i),
			LastUsed: p.now.Add(-time.Duration(i) * 30 * time.Hour).Unix(), Pinned: i == 1})
	}
	p.prof.Items[4].LastUsed = 0
}

var previewScenes = []previewScene{
	{"devices-empty", func(p *Preview) {}},
	{"devices", func(p *Preview) { p.devices(); p.App.quickAddr = "192.168.1.31" }},
	{"device-dialog", func(p *Preview) { p.devices(); p.App.openDeviceDialog("Office", "192.168.1.24:7000", "secret") }},
	{"this-pc", func(p *Preview) {
		p.App.page = PageThisPC
		p.host.info.Controller, p.host.info.ControllerVerified = "192.168.1.55:51822", true
		p.host.info.Rejected, p.host.info.RejectedWhy = "10.0.0.5:50122", ReasonWrongPassword
		p.host.paired = []PairedDevice{{"a", "10.0.0.7", p.now.Add(-26 * time.Hour)}, {"b", "10.0.0.9", time.Time{}}}
		p.host.info.Paired = 2
	}},
	{"this-pc-pairing", func(p *Preview) {
		p.App.page = PageThisPC
		p.host.info.PairingLeft, p.host.info.PairingCode = 102*time.Second, "123456"
	}},
	{"this-pc-open", func(p *Preview) { p.App.page = PageThisPC; p.host.info.RequireAuth = false }},
	{"this-pc-shared", func(p *Preview) {
		p.App.page = PageThisPC
		p.host.info.PortShared = []string{`nx-host.exe (process 4120) on 100.109.77.29`}
	}},
	{"this-pc-unavailable", func(p *Preview) {
		p.App.page = PageThisPC
		p.host.info.Err = "UDP port 7000 is already in use by another program"
	}},
	{"settings", func(p *Preview) { p.App.page = PageSettings }},
	{"settings-sharing", func(p *Preview) { p.App.page, p.App.setSection = PageSettings, setSharing }},
	{"settings-sharing-virtual", func(p *Preview) {
		p.App.page, p.App.setSection = PageSettings, setSharing
		p.host.info.VirtualAllowed, p.host.info.DriverInstalled, p.host.info.DriverVersion = true, true, "0.45"
		p.host.info.VirtualActive = `\\.\DISPLAY9 2560x1440@60`
	}},
	{"settings-network", func(p *Preview) {
		p.App.page, p.App.setSection = PageSettings, setNetwork
		p.host.info.ListenIP, p.host.info.Port, p.App.portText = "100.109.77.29", 7001, "7001"
		p.App.netErr = p.App.tr.T(i18n.KeySetListenFailed, "error", "UDP port 7000 is in use by nx-host.exe (process 4120). Close that program, or choose another port in Settings > Network.")
	}},
	{"settings-session", func(p *Preview) { p.App.page, p.App.setSection = PageSettings, setSession }},
	{"settings-about", func(p *Preview) { p.App.page, p.App.setSection = PageSettings, setAbout }},
	{"logs", func(p *Preview) {
		p.App.page = PageLogs
		for i := 0; i < 60; i++ {
			p.logs.Write([]byte(fmt.Sprintf("2026/10/28 09:%02d:00 startup +%dms something happened here that is a little longer than most\n", i, i*7)))
		}
	}},
	{"connecting", func(p *Preview) {
		p.App.tabs = []*tab{{serial: 1, title: "Press 3", addr: "10.0.0.1:7000", state: tabConnecting, cancel: make(chan struct{})}}
		p.App.serial, p.App.active = 1, 1
	}},
	{"verify", func(p *Preview) {
		p.App.tabs = []*tab{{serial: 1, title: "10.0.0.1:7000", addr: "10.0.0.1:7000", state: tabConnecting, cancel: make(chan struct{}),
			confirm: &hostConfirm{fp: "a1b2c3d4e5f67a8b99aabbccddeeff00", reply: make(chan bool, 1)}}}
		p.App.serial, p.App.active = 1, 1
	}},
	{"error", func(p *Preview) {
		p.App.tabs = []*tab{
			{serial: 1, title: "Line 1", addr: "10.0.0.2:7000", state: tabLive, sess: nil},
			{serial: 2, title: "Press 3", addr: "10.0.0.1:7000", state: tabError, hostChanged: true,
				err: p.App.tr.T(i18n.KeyErrHostChanged)},
		}
		p.App.tabs[0].state = tabConnecting
		p.App.tabs[0].cancel = make(chan struct{})
		p.App.serial, p.App.active = 2, 2
	}},
	{"error-refused", func(p *Preview) {
		p.App.tabs = []*tab{{serial: 1, title: "Office", addr: "100.109.77.29:7000", state: tabError, askPw: true,
			err: p.App.tr.T(keyErrDisconnected, "reason", p.App.tr.T(i18n.KeyErrBadPassword))}}
		p.App.serial, p.App.active = 1, 1
	}},
	{"dialog-open-mode", func(p *Preview) { p.App.page = PageThisPC; p.App.setStrict(false) }},
	{"update-ready", func(p *Preview) {
		p.upd.st = update.Status{State: update.Ready, Version: "0.12.3", Mandatory: true}
		p.upd.pending = true
	}},
	{"toasts", func(p *Preview) {
		p.App.Toast(p.App.tr.T(i18n.KeyDevSaved, "name", "Line 1"), kit.ToneGood)
		p.App.Toast(p.App.tr.Count(i18n.KeyToastStrictDroppedOne, i18n.KeyToastStrictDroppedOther, 2), kit.ToneWarn)
	}},
}

// The stand-ins.

type previewHost struct {
	info   HostInfo
	paired []PairedDevice
}

func (h *previewHost) Info() HostInfo             { return h.info }
func (h *previewHost) StartPairing()              {}
func (h *previewHost) ClosePairing()              {}
func (h *previewHost) SetRequireAuth(on bool) int { h.info.RequireAuth = on; return 0 }
func (h *previewHost) SetLockout(on bool)         { h.info.Lockout = on }
func (h *previewHost) SetAllowWinL(on bool)       { h.info.AllowWinL = on }
func (h *previewHost) SetVirtualDisplay(on bool)  { h.info.VirtualAllowed = on }
func (h *previewHost) InstallDriver() error       { return nil }
func (h *previewHost) SetListen(listen string, port int) error {
	h.info.ListenIP, h.info.Port = listen, port
	return nil
}
func (h *previewHost) Password() string        { return "" }
func (h *previewHost) SetPassword(string)      {}
func (h *previewHost) Paired() []PairedDevice  { return h.paired }
func (h *previewHost) Revoke(string) error     { return nil }
func (h *previewHost) RevokeAll() (int, error) { return 0, nil }
func (h *previewHost) DisconnectController()   {}

type previewSettings struct{ m map[string]string }

func (s *previewSettings) Get(k string) (string, bool) { v, ok := s.m[k]; return v, ok }
func (s *previewSettings) Set(k, v string) error       { s.m[k] = v; return nil }

type previewUpdater struct {
	st      update.Status
	pending bool
}

func (u *previewUpdater) Status() update.Status { return u.st }
func (u *previewUpdater) Click()                {}
func (u *previewUpdater) RestartPending() bool  { return u.pending }

type previewPlatform struct{}

func (previewPlatform) Quit()                        {}
func (previewPlatform) HideWindow()                  {}
func (previewPlatform) WindowHidden() bool           { return false }
func (previewPlatform) Clipboard() kit.Clipboard     { return &previewClip{} }
func (previewPlatform) SystemDark() (bool, bool)     { return true, true }
func (previewPlatform) PreferredLanguages() []string { return []string{"en-US"} }
func (previewPlatform) ApplyTheme(bool)              {}
func (previewPlatform) MessageBox(string, string)    {}
func (previewPlatform) Autostart() bool              { return true }
func (previewPlatform) SetAutostart(bool) error      { return nil }

type previewClip struct{ s string }

func (c *previewClip) Get() string  { return c.s }
func (c *previewClip) Set(s string) { c.s = s }

// previewConnector never connects.
type previewConnector struct{}

func (previewConnector) Pinned(addr string) (string, bool) {
	if addr == "100.109.77.29:7000" { // the host of the error-refused scene
		return "28aff5aca80377e726755252f750d491196c7abad0393bc8b9015fc5c257047a", true
	}
	return "", false
}
func (previewConnector) Probe(context.Context, string) (string, error) {
	return "", errors.New("preview")
}
func (previewConnector) Pin(string, string) error { return nil }
func (previewConnector) Forget(string) error      { return nil }
func (previewConnector) Dial(context.Context, string, string) (Conn, error) {
	return nil, errors.New("preview")
}

// previewConn is a connection that shows one picture and does nothing else, with every
// optional ability, so that the session view can be drawn.
type previewConn struct {
	done chan struct{}
	once sync.Once
}

func (c *previewConn) Run(ctx context.Context, onFrame func(*tile.Frame, []tile.Rect), _ func(uint64, byte)) error {
	src, err := synth.NewScene(synth.SceneDocScroll, 1280, 800)
	if err == nil {
		f := tile.NewFrame(1280, 800)
		if src.Grab(f) == nil {
			onFrame(f, nil)
		}
	}
	select {
	case <-c.done:
	case <-ctx.Done():
	}
	return errors.New("preview ended")
}
func (c *previewConn) SendInput(proto.Input) error { return nil }
func (c *previewConn) Close()                      { close(c.done) }
func (c *previewConn) Supports(caps proto.Caps) bool {
	return (proto.CapText | proto.CapVirtualDisplay).Has(caps) // the toolbar with every button
}
func (c *previewConn) SendViewSize(int, int) error              { return nil }
func (c *previewConn) DisplayState() (proto.DisplayState, bool) { return proto.DisplayState{}, false }
func (c *previewConn) OnDisplayState(func())                    {}
func (c *previewConn) SendText(uint64, string) error            { return nil }
func (c *previewConn) Stats() remote.Stats {
	return remote.Stats{RTT: 38 * time.Millisecond, BytesReceived: 3 << 20}
}
func (c *previewConn) Host() (remote.HostInfo, bool) {
	return remote.HostInfo{Name: "WORKSHOP-PC"}, true
}

// liveTab makes a tab with a picture on it, like a connection that has just got its first
// picture (the picture arrives on the connection's goroutine; this waits for it).
func (p *Preview) liveTab(title string) *tab {
	conn := &previewConn{done: make(chan struct{})}
	p.conns = append(p.conns, conn)
	s := newSession(conn, &p.App.nextOp, nil)
	for i := 0; i < 500 && !s.hasPicture(); i++ {
		time.Sleep(2 * time.Millisecond)
	}
	s.setVisible(true)
	p.App.serial++
	t := &tab{serial: p.App.serial, title: title, addr: "192.168.1.31:7000", state: tabLive, sess: s}
	p.App.tabs = append(p.App.tabs, t)
	p.App.active = len(p.App.tabs)
	p.App.fonts.SetScale(p.App.scale)
	return t
}

// DrawPicture draws the picture of the tab on show, if there is one, under where the
// interface will be painted. It needs the GPU.
func (p *Preview) DrawPicture(dst *ebiten.Image) {
	p.App.StepLive(func(int, int) {})
	p.App.drawLive(dst)
}

func init() {
	previewScenes = append(previewScenes,
		previewScene{"session", func(p *Preview) {
			t := p.liveTab("Press 3")
			p.App.showBar(t)
		}},
		previewScene{"session-handle", func(p *Preview) { p.liveTab("Press 3") }},
		previewScene{"session-text", func(p *Preview) {
			t := p.liveTab("Press 3")
			t.stats = true
			t.text = "你好 hello 안녕"
			p.App.openText(t)
			t.sess.held[0xA2] = true
			t.sess.held[0xA0] = true
			p.App.noteModifiers(t.sess)
		}},
		previewScene{"session-reconnecting", func(p *Preview) {
			t := p.liveTab("Press 3")
			p.App.reconnectWaits = []time.Duration{time.Hour} // never gets as far as dialling
			p.App.startReconnect(t)
			t.attempts.Store(3)
		}},
		previewScene{"session-fullscreen", func(p *Preview) {
			t := p.liveTab("Press 3")
			p.App.showBar(t)
			p.App.fullscreen = true
		}},
	)
}
