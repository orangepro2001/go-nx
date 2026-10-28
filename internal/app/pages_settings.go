package app

import (
	"fmt"
	"log"
	"runtime"
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

func (a *App) pageSettings() kit.Node {
	tr := a.tr
	return a.pageFrame("page.settings", tr.T(i18n.KeyNavSettings),
		a.appearanceCard(), a.languageCard(), a.frameRateCard(), a.updatesCard(), a.diagnosticsCard())
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
