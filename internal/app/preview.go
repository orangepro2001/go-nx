package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/orangepro2001/go-nx/internal/logging"
	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/update"
)

// Preview is the application with made-up data, standing still in one of its screens: for
// screenshots (tools/uipreview) and for tests that sweep every screen. Nothing in it
// connects anywhere or touches the machine.
type Preview struct {
	App  *App
	host *previewHost
	upd  *previewUpdater
	logs *logging.Logger
	prof *profiles.List
	now  time.Time
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
		host: &previewHost{info: HostInfo{Addrs: []string{"192.168.1.5", "10.0.0.7"}, Port: 7000, ID: "9f3a1c77d0e24b58", RequireAuth: true}},
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
	{"this-pc-unavailable", func(p *Preview) {
		p.App.page = PageThisPC
		p.host.info.Err = "UDP port 7000 is already in use by another program"
	}},
	{"settings", func(p *Preview) { p.App.page = PageSettings }},
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
func (h *previewHost) Password() string           { return "" }
func (h *previewHost) SetPassword(string)         {}
func (h *previewHost) Paired() []PairedDevice     { return h.paired }
func (h *previewHost) Revoke(string) error        { return nil }
func (h *previewHost) RevokeAll() (int, error)    { return 0, nil }
func (h *previewHost) DisconnectController()      {}

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

type previewClip struct{ s string }

func (c *previewClip) Get() string  { return c.s }
func (c *previewClip) Set(s string) { c.s = s }

// previewConnector never connects.
type previewConnector struct{}

func (previewConnector) Pinned(string) (string, bool) { return "", false }
func (previewConnector) Probe(context.Context, string) (string, error) {
	return "", errors.New("preview")
}
func (previewConnector) Pin(string, string) error { return nil }
func (previewConnector) Forget(string) error      { return nil }
func (previewConnector) Dial(context.Context, string, string) (Conn, error) {
	return nil, errors.New("preview")
}
