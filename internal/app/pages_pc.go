package app

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// pageThisPC is the sharing side: whether it works, how to reach it, who may connect, and
// who is connected.
func (a *App) pageThisPC() kit.Node {
	info := a.cfg.Host.Info()
	kids := []kit.Node{a.statusCard(info)}
	if info.Err == "" {
		kids = append(kids, a.securityCard(info), a.pairingCard(info), a.pairedCard(), a.controllerCard(info), a.passwordCard())
	}
	return a.pageFrame("page.pc", a.tr.T(i18n.KeyNavThisPC), kids...)
}

func (a *App) copyButton(id kit.ID, s string) kit.Node {
	return &kit.Button{ID: id, Icon: kit.IconCopy, IconOnly: true, Kind: kit.Ghost, Compact: true, MinW: 32, OnClick: func() { a.copyText(s) }}
}

func (a *App) copyText(s string) {
	if c := a.ui.Clip; c != nil {
		c.Set(s)
		a.Toast(a.tr.T(i18n.KeyCopied), kit.ToneGood)
	}
}

func (a *App) statusCard(info HostInfo) kit.Node {
	tr, p := a.tr, a.ui.Pal
	if info.Err != "" {
		return a.card("", p.Warn,
			&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
				&kit.IconNode{Icon: kit.IconWarning, Color: p.Warn},
				kit.Grow(1, &kit.Label{Text: tr.T(i18n.KeyPCUnavailable, "reason", info.Err), Wrap: true, Color: p.Warn}),
			}})
	}
	addrs := tr.T(i18n.KeyPCNoAddress)
	if len(info.Addrs) > 0 {
		addrs = strings.Join(info.Addrs, ", ")
	}
	line := tr.T(i18n.KeyPCAddressLine, "addresses", addrs, "port", info.Port)
	return a.card("", kit.Color{},
		&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.Dot{Color: p.Good, Size: 10},
			kit.Grow(1, &kit.Label{Text: tr.T(i18n.KeyPCSharing), Size: theme.SizeSub, Weight: text.Semibold}),
		}},
		&kit.Row{Gap: theme.S2, Align: kit.AlignCenter, Children: []kit.Node{
			kit.Grow(1, &kit.Label{Text: line, Color: p.TextDim, Wrap: true}),
			a.copyButton("pc.copy.addr", line),
		}},
		&kit.Col{Gap: theme.S1, Align: kit.AlignStretch, Children: []kit.Node{
			a.caption(tr.T(i18n.KeyPCIDTitle)),
			&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
				&kit.Code{Text: groupID(info.ID), Size: theme.SizeTitle + 4, Color: p.Text},
				a.copyButton("pc.copy.id", groupID(info.ID)),
				kit.Spacer(),
			}},
			a.caption(tr.T(i18n.KeyPCIDHint)),
		}},
	)
}

func (a *App) securityCard(info HostInfo) kit.Node {
	tr, p := a.tr, a.ui.Pal
	sel, desc := 0, tr.T(i18n.KeyModeStrictDesc)
	border := kit.Color{}
	if !info.RequireAuth {
		sel, desc, border = 1, tr.T(i18n.KeyModeOpenDesc), p.Bad
	}
	icon := kit.IconShield
	iconColor := p.Good
	if !info.RequireAuth {
		icon, iconColor = kit.IconShieldOpen, p.Bad
	}
	return a.card(tr.T(i18n.KeyPCSecurity), border,
		&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: icon, Size: 22, Color: iconColor},
			&kit.Segmented{ID: "pc.mode", Options: []string{tr.T(i18n.KeyModeStrict), tr.T(i18n.KeyModeOpen)}, Selected: sel,
				Tones:    []kit.Tone{kit.ToneAccent, kit.ToneBad},
				OnChange: func(i int) { a.setStrict(i == 0) }},
			kit.Spacer(),
		}},
		a.body(desc),
		a.caption(tr.T(i18n.KeyModeSwitchNote)),
	)
}

// setStrict switches the host between strict and open mode. Going open is the dangerous
// direction and asks first; going strict ends the sessions that were let in unverified.
func (a *App) setStrict(on bool) {
	tr := a.tr
	if !on {
		a.openDialog(func() kit.Node {
			return &kit.Dialog{
				ID: "dlg.open", Title: tr.T(i18n.KeyModeOpenAskTitle), Danger: true, W: 460, OnCancel: a.closeDialog, DefaultFocus: "dlg.cancel",
				Body: a.body(tr.T(i18n.KeyModeOpenAskBody)),
				Actions: []kit.Node{
					&kit.Button{ID: "dlg.cancel", Label: tr.T(i18n.KeyCancel), OnClick: a.closeDialog},
					&kit.Button{ID: "dlg.open.yes", Label: tr.T(i18n.KeyModeOpenAskYes), Kind: kit.Danger, OnClick: func() {
						a.cfg.Host.SetRequireAuth(false)
						a.closeDialog()
						a.Toast(tr.T(i18n.KeyToastOpen), kit.ToneBad)
					}},
				},
			}
		})
		return
	}
	n := a.cfg.Host.SetRequireAuth(true)
	msg := tr.T(i18n.KeyToastStrict)
	if n > 0 {
		msg = tr.Count(i18n.KeyToastStrictDroppedOne, i18n.KeyToastStrictDroppedOther, n)
	}
	a.Toast(msg, kit.ToneGood)
	a.ui.Invalidate()
}

func (a *App) passwordCard() kit.Node {
	tr := a.tr
	return a.card(tr.T(i18n.KeyPCPassword), kit.Color{},
		kit.Sized{MaxW: 360, Child: &kit.TextField{ID: "pc.password", Value: &a.hostPw, Mask: true, MaxLen: 64, OnChange: func() { a.cfg.Host.SetPassword(a.hostPw) }}},
		a.caption(tr.T(i18n.KeyPCPasswordHint)),
	)
}

func (a *App) pairingCard(info HostInfo) kit.Node {
	tr, p := a.tr, a.ui.Pal
	if info.PairingLeft <= 0 {
		return a.card(tr.T(i18n.KeyPairTitle), kit.Color{},
			a.dim(tr.T(i18n.KeyPairIdle)),
			&kit.Row{Children: []kit.Node{
				&kit.Button{ID: "pc.pair.start", Label: tr.T(i18n.KeyPairStart), Kind: kit.Primary, Icon: kit.IconPlus, OnClick: func() { a.cfg.Host.StartPairing(); a.ui.Invalidate() }},
			}},
		)
	}
	m, s := countdown(info.PairingLeft)
	return a.card(tr.T(i18n.KeyPairTitle), p.Accent,
		&kit.Row{Gap: theme.S5, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.Code{Text: groupCode(info.PairingCode), Size: theme.SizeCode + 8, Color: p.Accent},
			&kit.Label{Text: tr.T(i18n.KeyPairLeft, "minutes", m, "seconds", s), Color: p.TextDim},
			kit.Spacer(),
		}},
		a.body(tr.T(i18n.KeyPairHow)),
		&kit.Row{Children: []kit.Node{
			&kit.Button{ID: "pc.pair.cancel", Label: tr.T(i18n.KeyPairCancel), OnClick: func() { a.cfg.Host.ClosePairing(); a.ui.Invalidate() }},
		}},
	)
}

func (a *App) pairedCard() kit.Node {
	tr, p := a.tr, a.ui.Pal
	devs := a.cfg.Host.Paired()
	title := tr.T(i18n.KeyPairedTitle, "count", len(devs))
	if len(devs) == 0 {
		return a.card(title, kit.Color{}, a.dim(tr.T(i18n.KeyPairedNone)))
	}
	rows := &kit.Col{Gap: theme.S1, Align: kit.AlignStretch}
	for i, d := range devs {
		d := d
		when := ""
		if !d.When.IsZero() {
			when = tr.T(i18n.KeyPairedAt, "when", d.When.Format("2006-01-02 15:04"))
		}
		rows.Children = append(rows.Children, &kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconUser, Color: p.TextDim},
			kit.Grow(1, &kit.Label{Text: d.Addr, Ellipsis: true}),
			&kit.Label{Text: when, Color: p.TextDim, Size: theme.SizeCaption + 1},
			&kit.Button{ID: kit.ID("pc.forget." + itoa(i)), Label: tr.T(i18n.KeyForget), Kind: kit.Ghost, Compact: true, OnClick: func() { a.forgetDevices(d.Fingerprint) }},
		}})
	}
	return a.card(title, kit.Color{}, rows,
		&kit.Row{Children: []kit.Node{
			&kit.Button{ID: "pc.forget.all", Label: tr.T(i18n.KeyPairedForgetAll), Kind: kit.Ghost, Compact: true, OnClick: func() { a.forgetDevices("") }},
		}},
	)
}

// forgetDevices asks, then forgets one paired device (fp) or all of them (fp == "").
func (a *App) forgetDevices(fp string) {
	tr := a.tr
	n := 1
	if fp == "" {
		n = len(a.cfg.Host.Paired())
	}
	if n == 0 {
		return
	}
	a.openDialog(func() kit.Node {
		return &kit.Dialog{
			ID: "dlg.forget", Title: tr.T(i18n.KeyForget), W: 400, OnCancel: a.closeDialog, DefaultFocus: "dlg.cancel",
			Body: a.body(tr.Count(i18n.KeyForgetAskOne, i18n.KeyForgetAskOther, n)),
			Actions: []kit.Node{
				&kit.Button{ID: "dlg.cancel", Label: tr.T(i18n.KeyCancel), OnClick: a.closeDialog},
				&kit.Button{ID: "dlg.forget.yes", Label: tr.T(i18n.KeyForget), Kind: kit.Danger, OnClick: func() {
					a.closeDialog()
					var err error
					if fp == "" {
						n, err = a.cfg.Host.RevokeAll()
					} else {
						err = a.cfg.Host.Revoke(fp)
					}
					switch {
					case err != nil:
						log.Printf("forget paired devices: %v", err)
						a.Toast(tr.T(i18n.KeyToastForgotUnsaved, "error", err.Error()), kit.ToneWarn)
					default:
						a.Toast(tr.Count(i18n.KeyToastForgotOne, i18n.KeyToastForgotOther, n), kit.ToneGood)
					}
				}},
			},
		}
	})
}

func (a *App) controllerCard(info HostInfo) kit.Node {
	tr, p := a.tr, a.ui.Pal
	kids := []kit.Node{}
	if info.Controller == "" {
		kids = append(kids, a.dim(tr.T(i18n.KeyCtrlNone)))
	} else {
		chip := &kit.Chip{Text: tr.T(i18n.KeyCtrlUnverified), Tone: kit.ToneWarn}
		if info.ControllerVerified {
			chip = &kit.Chip{Text: tr.T(i18n.KeyCtrlVerified), Tone: kit.ToneGood}
		}
		kids = append(kids, &kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.Dot{Color: p.Good},
			kit.Grow(1, &kit.Label{Text: info.Controller, Ellipsis: true}),
			chip,
			&kit.Button{ID: "pc.kick", Label: tr.T(i18n.KeyDisconnect), Kind: kit.Danger, Compact: true, OnClick: func() { a.cfg.Host.DisconnectController(); a.ui.Invalidate() }},
		}})
	}
	if info.Rejected != "" {
		why := ""
		switch info.RejectedWhy {
		case ReasonNotPaired:
			why = tr.T(i18n.KeyReasonNotPaired)
		case ReasonWrongPassword:
			why = tr.T(i18n.KeyReasonWrongPassword)
		case ReasonLockedOut:
			why = tr.T(i18n.KeyReasonLockedOut)
		}
		what := info.Rejected
		if why != "" {
			what = fmt.Sprintf("%s (%s)", info.Rejected, why)
		}
		kids = append(kids, &kit.Label{Text: tr.T(i18n.KeyRefusedLast, "what", what), Color: p.Warn, Wrap: true, Size: theme.SizeCaption + 1})
	}
	return a.card(tr.T(i18n.KeyCtrlTitle), kit.Color{}, kids...)
}

// pageLogs shows the program's log.
func (a *App) pageLogs() kit.Node {
	tr, p := a.tr, a.ui.Pal
	lines := a.cfg.Logs.Lines()
	head := &kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
		kit.Grow(1, a.title(tr.T(i18n.KeyNavLogs))),
		&kit.Button{ID: "logs.copy", Label: tr.T(i18n.KeyLogsCopyAll), Icon: kit.IconCopy, Compact: true, OnClick: func() { a.copyText(strings.Join(lines, "\n")) }},
	}}
	info := []kit.Node{head}
	if a.cfg.LogPath != "" {
		info = append(info, a.caption(tr.T(i18n.KeyLogsFile, "path", a.cfg.LogPath)))
	}
	if err := a.cfg.Logs.FileError(); err != nil {
		info = append(info, &kit.Label{Text: tr.T(i18n.KeyLogsFileError, "error", err.Error()), Color: p.Warn, Wrap: true, Size: theme.SizeCaption + 1})
	}
	var body kit.Node
	if len(lines) == 0 {
		body = a.dim(tr.T(i18n.KeyLogsNone))
	} else {
		st := a.ui.State("logs.scroll")
		atEnd := st.Max == 0 || st.ScrollY >= st.Max-24
		if atEnd || a.logJump {
			a.ui.ScrollTo("logs.scroll", 1e9)
			a.logJump = false
		}
		body = &kit.Scroll{ID: "logs.scroll", Child: &logList{lines: lines}}
	}
	return kit.Pad{In: kit.All(theme.S5), Child: &kit.Col{Gap: theme.S3, Align: kit.AlignStretch, Children: append(info,
		kit.Grow(1, &kit.Box{Fill: p.Surface, Border: p.Border, Radius: theme.RadiusMedium, Pad: kit.All(theme.S3), Child: body}))}}
}

// logList draws lines of log, only the ones in view: a log can be two thousand lines long.
type logList struct {
	lines   []string
	r       kit.Rect
	visible kit.Rect
	st      text.Style
	lh      float64
}

func (l *logList) Measure(u *kit.UI, maxW float64) kit.Size {
	l.st = u.Style(theme.SizeCaption+1, text.Regular)
	l.lh = u.LineH(l.st)
	return kit.Size{W: maxW, H: l.lh * float64(len(l.lines))}
}
func (l *logList) Place(u *kit.UI, r kit.Rect) {
	l.r, l.visible = r, u.ClipRect()
	l.Measure(u, r.W)
}
func (l *logList) Paint(u *kit.UI, cv kit.Canvas) {
	first := max(int((l.visible.Y-l.r.Y)/l.lh)-1, 0)
	last := min(int((l.visible.Bottom()-l.r.Y)/l.lh)+1, len(l.lines))
	pad := (l.lh - u.M.LineHeight(l.st)) / 2
	for i := first; i < last; i++ {
		cv.Text(text.Truncate(u.M, l.lines[i], l.st, l.r.W), l.r.X, l.r.Y+float64(i)*l.lh+pad, l.st, u.Pal.Text)
	}
}

var _ = time.Second
