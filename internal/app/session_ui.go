package app

import (
	"log"
	"strings"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// sessionCard is what a tab shows while it is not a picture yet (or no longer): the
// connection being made, the question about a host seen for the first time, or why it
// failed, each with the next thing to do.
func (a *App) sessionCard(t *tab) kit.Node {
	p, tr := a.ui.Pal, a.tr
	var card kit.Node
	switch {
	case t.state == tabConnecting && t.confirm != nil:
		card = a.verifyCard(t)
	case t.state == tabConnecting:
		card = a.card("", kit.Color{},
			&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
				&kit.IconNode{Icon: kit.IconPlug, Color: p.Accent},
				kit.Grow(1, &kit.Label{Text: tr.T(i18n.KeySessConnecting, "addr", t.addr), Size: theme.SizeSub, Weight: text.Semibold, Wrap: true}),
			}},
			&kit.Progress{Indeterminate: true},
			&kit.Row{Children: []kit.Node{
				&kit.Button{ID: "sess.cancel", Label: tr.T(i18n.KeyCancel), OnClick: func() { a.closeTab(a.active) }},
			}},
		)
		a.wantFocus = "sess.cancel"
	default:
		card = a.errorCard(t)
	}
	return &kit.Stack{Children: []kit.Node{
		kit.Aligned{H: kit.AlignCenter, V: kit.AlignCenter, Child: kit.Pad{In: kit.All(theme.S5), Child: kit.Sized{MaxW: 560, Child: card}}},
		kit.EscapeHandler{OnEscape: func() { a.sessionEscape(t) }},
	}}
}

// sessionEscape is Escape in a tab that is not showing a picture: it declines the
// question, or leaves a failed tab.
func (a *App) sessionEscape(t *tab) {
	switch {
	case t.confirm != nil:
		t.confirm.reply <- false
		t.confirm = nil
		a.ui.Invalidate()
	case t.state == tabError:
		a.closeTab(a.active)
	}
}

func (a *App) verifyCard(t *tab) kit.Node {
	tr, p := a.tr, a.ui.Pal
	yes := func() {
		t.confirm.reply <- true
		t.confirm = nil
		a.ui.Invalidate()
	}
	a.wantFocus = "sess.trust"
	return a.card("", p.Accent,
		&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconShield, Color: p.Accent},
			kit.Grow(1, &kit.Label{Text: tr.T(i18n.KeySessVerifyTitle, "addr", t.addr), Size: theme.SizeSub, Weight: text.Semibold, Wrap: true}),
		}},
		a.body(tr.T(i18n.KeySessVerifyBody)),
		&kit.Row{Children: []kit.Node{
			&kit.Code{Text: groupID(shortID(t.confirm.fp)), Size: theme.SizeCode, Color: p.Text},
			kit.Spacer(),
		}},
		a.dim(tr.T(i18n.KeySessVerifyHow)),
		&kit.Row{Gap: theme.S2, Children: []kit.Node{
			&kit.Button{ID: "sess.trust", Label: tr.T(i18n.KeySessTrust), Kind: kit.Primary, OnClick: yes},
			&kit.Button{ID: "sess.decline", Label: tr.T(i18n.KeyCancel), OnClick: func() { a.sessionEscape(t) }},
		}},
	)
}

func (a *App) errorCard(t *tab) kit.Node {
	tr, p := a.tr, a.ui.Pal
	title := tr.T(i18n.KeySessFailedTitle)
	if strings.HasPrefix(t.err, tr.T(i18n.KeyErrDisconnected, "reason", "")) {
		title = tr.T(i18n.KeySessEndedTitle)
	}
	buttons := &kit.Row{Gap: theme.S2, Children: []kit.Node{
		&kit.Button{ID: "sess.retry", Label: tr.T(i18n.KeyRetry), Kind: kit.Primary, Icon: kit.IconRefresh, OnClick: func() { a.startDial(t) }},
		&kit.Button{ID: "sess.close", Label: tr.T(i18n.KeyClose), OnClick: func() { a.closeTab(a.active) }},
	}}
	if t.hostChanged {
		buttons.Children = append(buttons.Children, &kit.Button{ID: "sess.forget", Label: tr.T(i18n.KeySessForgetIdentity), Kind: kit.Danger, OnClick: func() { a.forgetIdentity(t) }})
	}
	a.wantFocus = "sess.retry"
	kids := []kit.Node{
		&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconUnplug, Color: p.Bad},
			kit.Grow(1, &kit.Label{Text: title, Size: theme.SizeSub, Weight: text.Semibold, Color: p.Bad}),
		}},
		&kit.Label{Text: t.err, Wrap: true},
		a.caption(t.addr),
	}
	if t.askPw {
		// The host refused what was sent: take another here, so Retry (or Enter) sends it.
		id := kit.ID("sess.pw." + itoa(t.serial))
		kids = append(kids,
			a.labelled(tr.T(i18n.KeySessPwAgain), a.secretField(&kit.TextField{ID: id, Value: &t.password, MaxLen: 64, OnSubmit: func() { a.startDial(t) }})),
			a.caption(tr.T(i18n.KeySessPwHint)),
		)
		a.wantFocus = id
	}
	return a.card("", p.Bad, append(kids, buttons)...)
}

// forgetIdentity drops the saved identity of a host that presented a different one, which
// is the one deliberate way to accept it.
func (a *App) forgetIdentity(t *tab) {
	tr := a.tr
	if err := a.cfg.Connector.Forget(t.addr); err != nil {
		t.err = tr.T(i18n.KeySessForgetFailed, "error", err.Error())
	} else {
		t.hostChanged = false
		t.err = tr.T(i18n.KeySessForgotIdentity)
		log.Printf("forgot saved identity of %s", t.addr)
	}
	a.ui.Invalidate()
}
