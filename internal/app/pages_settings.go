package app

import (
	"fmt"
	"log"
	"net/netip"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
	"github.com/orangepro2001/go-nx/internal/update"
)

// Settings key for the frame-rate policy: "dynamic" (the default) or "fixed".
const keyFPSMode = "fps_mode"

// FixedFrameRates reports whether the person chose a fixed frame rate over the dynamic one.
// The host reads it when a session begins.
func FixedFrameRates(s Settings) bool {
	v, _ := s.Get(keyFPSMode)
	return v == "fixed"
}

// The parts of the settings page, in the order of its tabs.
const (
	setGeneral = iota
	setSharing
	setNetwork
	setSession
	setAbout
)

func (a *App) pageSettings() kit.Node {
	tr := a.tr
	// One button per part; in a narrow window they go on two lines.
	tabs := &kit.Grid{Gap: theme.S1, MinCell: 112}
	for i, k := range []i18n.Key{i18n.KeySetSecGeneral, i18n.KeySetSecSharing, i18n.KeySetSecNetwork, i18n.KeySetSecSession, i18n.KeySetSecAbout} {
		tabs.Children = append(tabs.Children, &kit.Button{ID: kit.ID("set.section." + itoa(i)), Label: tr.T(k), Selected: i == a.setSection, Compact: true,
			OnClick: func() { a.setSection = i; a.ui.Invalidate() }})
	}
	var cards []kit.Node
	switch a.setSection {
	case setSharing:
		cards = []kit.Node{a.sharingCard(), a.virtualCard(), a.frameRateCard()}
	case setNetwork:
		cards = []kit.Node{a.networkCard()}
	case setSession:
		cards = []kit.Node{a.sessionCard2(), a.scalingCard()}
	case setAbout:
		cards = []kit.Node{a.updatesCard(), a.diagnosticsCard()}
	default:
		cards = []kit.Node{a.appearanceCard(), a.languageCard(), a.startupCard()}
	}
	return a.pageFrame("page.settings", tr.T(i18n.KeyNavSettings), append([]kit.Node{tabs}, cards...)...)
}

// startupCard is whether go-nx starts with Windows.
func (a *App) startupCard() kit.Node {
	tr := a.tr
	return a.card(tr.T(i18n.KeySetStartup), kit.Color{},
		a.switchRow("set.autostart", tr.T(i18n.KeySetAutostart), a.cfg.Platform.Autostart(), func(on bool) {
			if err := a.cfg.Platform.SetAutostart(on); err != nil {
				log.Printf("start with Windows: %v", err)
				a.Toast(tr.T(i18n.KeySetAutostartFailed, "error", err.Error()), kit.ToneWarn)
			}
		}),
		a.caption(tr.T(i18n.KeySetAutostartNote)),
	)
}

// sharingCard is what a viewer may do to this PC.
func (a *App) sharingCard() kit.Node {
	tr := a.tr
	return a.card(tr.T(i18n.KeySetSharing), kit.Color{},
		a.switchRow("set.winl", tr.T(i18n.KeySetAllowWinL), a.cfg.Host.Info().AllowWinL, a.cfg.Host.SetAllowWinL),
		a.caption(tr.T(i18n.KeySetAllowWinLNote)),
	)
}

// virtualCard is whether a viewer may put its session on a virtual display of its size, and
// the driver that makes one.
func (a *App) virtualCard() kit.Node {
	tr, p := a.tr, a.ui.Pal
	info := a.cfg.Host.Info()
	kids := []kit.Node{
		a.switchRow("set.virtual", tr.T(i18n.KeySetVirtualAllow), info.VirtualAllowed, a.cfg.Host.SetVirtualDisplay),
		a.caption(tr.T(i18n.KeySetVirtualNote)),
	}
	switch {
	case info.DriverInstalling:
		kids = append(kids, &kit.Label{Text: tr.T(i18n.KeyDriverInstalling), Color: p.Warn, Wrap: true})
	case info.DriverInstalled:
		kids = append(kids, a.body(tr.T(i18n.KeyDriverInstalled, "version", info.DriverVersion)))
	default:
		kids = append(kids,
			a.body(tr.T(i18n.KeyDriverMissing)),
			&kit.Row{Children: []kit.Node{
				&kit.Button{ID: "set.driver", Label: tr.T(i18n.KeyDriverInstall), Icon: kit.IconDownload, OnClick: a.installDriver},
				kit.Spacer(),
			}},
			a.caption(tr.T(i18n.KeyDriverInstallNote)),
		)
	}
	if info.VirtualActive != "" {
		kids = append(kids, a.dim(tr.T(i18n.KeyVirtualInUse, "what", info.VirtualActive)))
	}
	return a.card(tr.T(i18n.KeySetVirtual), kit.Color{}, kids...)
}

// installDriver runs the driver's installer in the background (Windows' prompt can wait for
// minutes) and says how it went.
func (a *App) installDriver() {
	tr := a.tr
	go func() {
		err := a.cfg.Host.InstallDriver()
		a.Do(func() {
			if err != nil {
				a.Toast(tr.T(i18n.KeyDriverFailed, "error", err.Error()), kit.ToneWarn)
			} else {
				a.Toast(tr.T(i18n.KeyDriverInstalledToast), kit.ToneGood)
			}
		})
	}()
	a.ui.Invalidate()
}

// networkCard is where this PC waits for connections: the port, and every address or one.
// A change applies at once; if the host cannot listen there it stays where it was.
func (a *App) networkCard() kit.Node {
	tr, p := a.tr, a.ui.Pal
	info := a.cfg.Host.Info()
	if info.ListenFixed {
		where := tr.T(i18n.KeySetListenAll)
		if info.ListenIP != "" {
			where = info.ListenIP
		}
		return a.card(tr.T(i18n.KeySetNetwork), kit.Color{},
			a.body(fmt.Sprintf("%s · UDP %d", where, info.Port)),
			a.caption(tr.T(i18n.KeySetListenFixed)),
		)
	}
	move := func(listen string) {
		port, err := strconv.Atoi(strings.TrimSpace(a.portText))
		if err != nil || port < 1 || port > 65535 {
			a.netErr = tr.T(i18n.KeySetPortBad)
		} else if err := a.cfg.Host.SetListen(listen, port); err != nil {
			a.netErr = tr.T(i18n.KeySetListenFailed, "error", err.Error())
		} else {
			a.netErr, a.portText = "", strconv.Itoa(port)
			where := tr.T(i18n.KeySetListenAll)
			if listen != "" {
				where = listen
			}
			a.Toast(tr.T(i18n.KeySetListenMoved, "where", fmt.Sprintf("%s · UDP %d", where, port)), kit.ToneGood)
		}
		a.ui.Invalidate()
	}
	choices := append([]string{""}, info.LocalIPs...)
	if info.ListenIP != "" && !slices.Contains(info.LocalIPs, info.ListenIP) {
		choices = append(choices, info.ListenIP) // saved, but the address is gone (or not there yet)
	}
	addrs := &kit.Col{Gap: theme.S1, Align: kit.AlignStretch}
	for i, ip := range choices {
		label := ip
		if ip == "" {
			label = tr.T(i18n.KeySetListenAll)
		}
		chosen := ip == info.ListenIP
		dot := p.Border
		if chosen {
			dot = p.Accent
		}
		row := &kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{&kit.Dot{Color: dot, Size: 12}, &kit.Label{Text: label}}}
		switch {
		case ip != "" && !slices.Contains(info.LocalIPs, ip):
			row.Children = append(row.Children, &kit.Chip{Text: tr.T(i18n.KeySetListenMissing), Tone: kit.ToneWarn})
		case isTailscale(ip):
			row.Children = append(row.Children, &kit.Chip{Text: tr.T(i18n.KeyTailscale), Tone: kit.ToneAccent})
		}
		row.Children = append(row.Children, kit.Spacer())
		addrs.Children = append(addrs.Children, &kit.Clickable{ID: kit.ID("set.listen." + itoa(i)), Hover: true, Radius: theme.RadiusSmall,
			OnClick: func() {
				if !chosen {
					move(ip)
				}
			},
			Child: kit.Pad{In: kit.Sym(theme.S2, theme.S1), Child: row}})
	}
	kids := []kit.Node{
		a.labelled(tr.T(i18n.KeySetPort), &kit.Row{Gap: theme.S2, Align: kit.AlignCenter, Children: []kit.Node{
			kit.Sized{MaxW: 120, Child: &kit.TextField{ID: "set.port", Value: &a.portText, MaxLen: 5, NoIME: true, Mono: true, OnSubmit: func() { move(info.ListenIP) }}},
			&kit.Button{ID: "set.port.apply", Label: tr.T(i18n.KeySetPortApply), OnClick: func() { move(info.ListenIP) }},
			kit.Spacer(),
		}}),
		a.caption(tr.T(i18n.KeySetPortNote)),
		a.labelled(tr.T(i18n.KeySetListenOn), addrs),
		a.caption(tr.T(i18n.KeySetListenNote)),
	}
	if a.netErr != "" {
		kids = append(kids, &kit.Label{Text: a.netErr, Color: p.Warn, Wrap: true})
	}
	return a.card(tr.T(i18n.KeySetNetwork), kit.Color{}, kids...)
}

// isTailscale reports whether ip is in the range Tailscale gives its devices (100.64.0.0/10).
func isTailscale(ip string) bool {
	a, err := netip.ParseAddr(ip)
	return err == nil && netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

func (a *App) appearanceCard() kit.Node {
	tr := a.tr
	sel := 0
	switch a.themeMode {
	case "dark":
		sel = 1
	case "light":
		sel = 2
	}
	return a.card(tr.T(i18n.KeySetAppearance), kit.Color{},
		&kit.Row{Children: []kit.Node{
			&kit.Segmented{ID: "set.theme", Options: []string{tr.T(i18n.KeyThemeSystem), tr.T(i18n.KeyThemeDark), tr.T(i18n.KeyThemeLight)}, Selected: sel,
				OnChange: func(i int) { a.SetTheme([]string{"system", "dark", "light"}[i]) }},
			kit.Spacer(),
		}},
	)
}

func (a *App) languageCard() kit.Node {
	tr := a.tr
	opts := []string{tr.T(i18n.KeyLangAuto)}
	tags := []string{"auto"}
	for _, l := range i18n.Langs {
		opts = append(opts, l.Name())
		tags = append(tags, l.Tag())
	}
	cur, _ := a.cfg.Settings.Get(keyLang)
	sel := 0
	for i, t := range tags {
		if t == cur {
			sel = i
		}
	}
	return a.card(tr.T(i18n.KeySetLanguage), kit.Color{},
		&kit.Row{Children: []kit.Node{
			&kit.Segmented{ID: "set.lang", Options: opts, Selected: sel, OnChange: func(i int) { a.SetLanguage(tags[i]) }},
			kit.Spacer(),
		}},
		a.caption(tr.T(i18n.KeySetLanguageNote)),
	)
}

// sessionCard2 is the settings of the session view (sessionCard is the connecting card).
func (a *App) sessionCard2() kit.Node {
	tr := a.tr
	toggle := func(id kit.ID, label, key string, on bool) kit.Node {
		return a.switchRow(id, label, on, func(v bool) {
			val := "0"
			if v {
				val = "1"
			}
			if err := a.cfg.Settings.Set(key, val); err != nil {
				log.Printf("could not save setting: %v", err)
			}
		})
	}
	return a.card(tr.T(i18n.KeySetSession), kit.Color{},
		toggle("set.reconnect", tr.T(i18n.KeySetAutoReconnect), keyAutoReconnect, a.autoReconnect()),
		toggle("set.korean", tr.T(i18n.KeySetKoreanKeys), keyKoreanKeys, a.koreanKeys()),
		toggle("set.matchres", tr.T(i18n.KeySetMatchRes), keyMatchRes, a.matchResDefault()),
		a.caption(tr.T(i18n.KeySetMatchResNote)),
	)
}

// scalingCard is how the remote picture is resized to the window.
func (a *App) scalingCard() kit.Node {
	tr := a.tr
	return a.card(tr.T(i18n.KeySetScaling), kit.Color{},
		&kit.Row{Children: []kit.Node{
			&kit.Segmented{ID: "set.scaling", Options: []string{tr.T(i18n.KeyScaleSharp), tr.T(i18n.KeyScaleSmooth), tr.T(i18n.KeyScalePixel)},
				Selected: int(a.scaleFilter()),
				OnChange: func(i int) {
					if err := a.cfg.Settings.Set(keyScaling, scalingNames[i]); err != nil {
						log.Printf("could not save setting: %v", err)
					}
					a.ui.Invalidate()
				}},
			kit.Spacer(),
		}},
		a.caption(tr.T(i18n.KeySetScalingNote)),
	)
}

func (a *App) frameRateCard() kit.Node {
	tr := a.tr
	sel, desc := 0, tr.T(i18n.KeyFpsDynamicDesc)
	if FixedFrameRates(a.cfg.Settings) {
		sel, desc = 1, tr.T(i18n.KeyFpsFixedDesc, "fps", 10)
	}
	return a.card(tr.T(i18n.KeySetFrameRate), kit.Color{},
		&kit.Row{Children: []kit.Node{
			&kit.Segmented{ID: "set.fps", Options: []string{tr.T(i18n.KeyFpsDynamic), tr.T(i18n.KeyFpsFixed)}, Selected: sel,
				OnChange: func(i int) {
					v := "dynamic"
					if i == 1 {
						v = "fixed"
					}
					if err := a.cfg.Settings.Set(keyFPSMode, v); err != nil {
						log.Printf("could not save setting: %v", err)
					}
					a.ui.Invalidate()
				}},
			kit.Spacer(),
		}},
		a.body(desc),
		a.caption(tr.T(i18n.KeyFpsApplyNote)),
	)
}

func (a *App) updatesCard() kit.Node {
	tr, p := a.tr, a.ui.Pal
	ver := tr.T(i18n.KeyVersionLabel, "version", a.cfg.Version)
	if a.cfg.Version == "dev" {
		ver = tr.T(i18n.KeyDevBuild)
	}
	kids := []kit.Node{&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
		&kit.Label{Text: tr.T(i18n.KeySetVersion), Color: p.TextDim},
		&kit.Label{Text: ver},
	}}}
	if a.cfg.Updater == nil {
		kids = append(kids, a.dim(tr.T(i18n.KeyUpdNone)))
	} else {
		st := a.cfg.Updater.Status()
		label, ready := a.updateLabel(st)
		kind := kit.Secondary
		if ready {
			kind = kit.Primary
		}
		kids = append(kids, &kit.Row{Children: []kit.Node{
			&kit.Button{ID: "set.update", Label: label, Kind: kind, Icon: kit.IconDownload, OnClick: a.updateClicked},
			kit.Spacer(),
		}})
		if st.State == update.Failed && st.Err != nil {
			kids = append(kids, &kit.Label{Text: tr.T(i18n.KeyUpdProblem, "error", st.Err.Error()), Color: p.Warn, Wrap: true, Size: theme.SizeCaption + 1})
		}
	}
	return a.card(tr.T(i18n.KeySetUpdates), kit.Color{}, kids...)
}

func (a *App) diagnosticsCard() kit.Node {
	tr := a.tr
	return a.card(tr.T(i18n.KeySetDiagnostics), kit.Color{},
		&kit.Row{Children: []kit.Node{
			&kit.Button{ID: "set.diag", Label: tr.T(i18n.KeyDiagCopy), Icon: kit.IconCopy, OnClick: func() { a.copyText(a.Diagnostics()) }},
			kit.Spacer(),
		}},
		a.caption(tr.T(i18n.KeyDiagNote)),
	)
}

// updateLabel is the text of the update button, and whether a restart into a newer build
// is waiting.
func (a *App) updateLabel(st update.Status) (label string, ready bool) {
	pending := a.cfg.Updater != nil && a.cfg.Updater.RestartPending()
	return UpdateLabel(a.tr, st, pending, pending && a.updateArmed())
}

// UpdateLabel is the words for the state of the updater: on the update button, and in the
// tray menu. pending says clicking restarts go-nx; armed that it was clicked once already.
func UpdateLabel(tr *i18n.Translator, st update.Status, pending, armed bool) (label string, ready bool) {
	if pending && armed {
		return tr.T(i18n.KeyUpdConfirm), true
	}
	switch st.State {
	case update.Checking:
		return tr.T(i18n.KeyUpdChecking), false
	case update.Current:
		return tr.T(i18n.KeyUpdCurrent), false
	case update.Downloading:
		return tr.T(i18n.KeyUpdDownloading, "version", st.Version, "percent", st.Percent), false
	case update.Ready:
		if st.Mandatory {
			return tr.T(i18n.KeyUpdReadyRequired, "version", st.Version), true
		}
		return tr.T(i18n.KeyUpdReady, "version", st.Version), true
	case update.Failed:
		return tr.T(i18n.KeyUpdFailed), false
	case update.Installed:
		return tr.T(i18n.KeyUpdInstalled, "version", st.Version), true
	}
	return tr.T(i18n.KeyUpdCheck), false
}

// armFor is how long the first click on a restart button waits for the second.
const armFor = 5 * time.Second

func (a *App) updateArmed() bool { return a.now.Before(a.updateArmedUntil) }

// updateClicked is the update button. Restarting ends every remote session, so when a
// restart is what the click would do, it takes a second click.
func (a *App) updateClicked() {
	u := a.cfg.Updater
	if u == nil {
		a.Toast(a.tr.T(i18n.KeyUpdNone), kit.ToneWarn)
		return
	}
	if u.RestartPending() && !a.updateArmed() {
		a.updateArmedUntil = a.now.Add(armFor)
		a.ui.Invalidate()
		return
	}
	a.updateArmedUntil = time.Time{}
	u.Click()
	a.ui.Invalidate()
}

// openQuitDialog asks before quitting when quitting would end something: a session of
// ours, or someone controlling this PC.
func (a *App) openQuitDialog() {
	tr := a.tr
	live := len(a.tabs) > 0 || a.cfg.Host.Info().Controller != ""
	if !live {
		a.Quit()
		return
	}
	a.openDialog(func() kit.Node {
		return &kit.Dialog{
			ID: "dlg.quit", Title: tr.T(i18n.KeyQuit), W: 420, OnCancel: a.closeDialog, DefaultFocus: "dlg.cancel",
			Body: a.body(tr.T(i18n.KeyQuitAsk)),
			Actions: []kit.Node{
				&kit.Button{ID: "dlg.cancel", Label: tr.T(i18n.KeyCancel), OnClick: a.closeDialog},
				&kit.Button{ID: "dlg.quit.yes", Label: tr.T(i18n.KeyQuit), Kind: kit.Danger, OnClick: a.Quit},
			},
		}
	})
}

// Diagnostics is what to give whoever investigates a problem: versions, system, settings
// and the last lines of the log. It contains no passwords, no addresses of other PCs
// beyond what the log lines already hold, and is in English.
func (a *App) Diagnostics() string {
	var b strings.Builder
	info := a.cfg.Host.Info()
	fmt.Fprintf(&b, "go-nx %s (%s/%s, %s)\n", a.cfg.Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Fprintf(&b, "language: %s, theme: %s (dark: %v), scale: %.2f\n", a.lang.Tag(), a.themeMode, a.dark, a.scale)
	fmt.Fprintf(&b, "sharing: error=%q, addresses=%v, port=%d, id=%s\n", info.Err, info.Addrs, info.Port, info.ID)
	fmt.Fprintf(&b, "strict mode: %v, paired devices: %d, controller: %q, pairing open: %v\n", info.RequireAuth, info.Paired, info.Controller, info.PairingLeft > 0)
	fmt.Fprintf(&b, "frame rate: fixed=%v, sessions: %d\n", FixedFrameRates(a.cfg.Settings), len(a.tabs))
	if a.cfg.Updater != nil {
		st := a.cfg.Updater.Status()
		fmt.Fprintf(&b, "update: state=%d version=%q percent=%d mandatory=%v err=%v\n", st.State, st.Version, st.Percent, st.Mandatory, st.Err)
	}
	lines := a.cfg.Logs.Lines()
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	b.WriteString("\nlast log lines:\n")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}
