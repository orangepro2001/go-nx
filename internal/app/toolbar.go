package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
	"github.com/orangepro2001/go-nx/internal/view"
)

// Settings keys of the session view.
const (
	keyAutoReconnect = "auto_reconnect" // "0" turns it off; the default is on
	keyKoreanKeys    = "korean_keys"    // "1" shows the 한/영 and 한자 buttons whatever the language
)

// barShowFor is how long the toolbar stays after the pointer last touched it.
const barShowFor = 3500 * time.Millisecond

// VK codes of the two Korean mode keys, which Ebiten cannot see (the keys exist on Korean
// keyboards and switch the input method of the PC they are typed on).
const (
	vkHangul = 0x15
	vkHanja  = 0x19
)

func (a *App) autoReconnect() bool {
	v, ok := a.cfg.Settings.Get(keyAutoReconnect)
	return !ok || v != "0"
}

func (a *App) koreanKeys() bool {
	if v, ok := a.cfg.Settings.Get(keyKoreanKeys); ok {
		return v == "1"
	}
	return a.lang == i18n.Korean
}

// pictureTab is the tab whose remote picture is on show, the session that has it, and
// whether it is dimmed (the connection is being brought back).
func (a *App) pictureTab() (t *tab, s *session, dim bool) {
	t = a.tabAt(a.active)
	switch {
	case t == nil:
		return nil, nil, false
	case t.state == tabLive:
		return t, t.sess, false
	case t.state == tabReconnecting && t.old != nil:
		return t, t.old, true
	}
	return nil, nil, false
}

// pointerOnBar reports whether the pointer is over the session's own controls, so that what
// it does is not sent to the remote PC.
func (a *App) pointerOnBar() bool { return strings.HasPrefix(string(a.ui.Hot()), "bar.") }

// showBar brings the toolbar up for a few seconds.
func (a *App) showBar(t *tab) {
	t.barShown = true
	t.barUntil = a.now.Add(barShowFor)
	a.ui.Invalidate()
}

// tickBar lets the toolbar go away by itself.
func (a *App) tickBar(t *tab) {
	if !t.barShown || t.textOpen {
		return
	}
	if a.pointerOnBar() {
		t.barUntil = a.now.Add(barShowFor)
		return
	}
	if !a.now.Before(t.barUntil) {
		t.barShown = false
		a.ui.Invalidate()
	}
}

// setFullscreen takes the window to full screen and back. In full screen the tab strip and
// the banner give way to the picture.
func (a *App) setFullscreen(on bool) {
	if a.fullscreen != on {
		a.fullscreen = on
		a.ui.Invalidate()
	}
}

func (a *App) toggleMode(s *session) {
	if s.mode == view.ModeFit {
		s.mode = view.ModeActual
	} else {
		s.mode = view.ModeFit
	}
	s.panX, s.panY = 0, 0
	a.ui.Invalidate()
}

func (a *App) openText(t *tab) {
	t.textOpen = true
	t.barShown = true
	a.wantFocus = "bar.text"
	a.ui.Invalidate()
}

func (a *App) closeText(t *tab) {
	t.textOpen, t.text = false, ""
	a.ui.Invalidate()
}

// sendTyped sends the text in the box to the remote PC and empties the box.
func (a *App) sendTyped(t *tab, s *session) {
	if strings.TrimSpace(t.text) == "" {
		return
	}
	s.sendText(t.text)
	t.text = ""
	a.ui.State("bar.text").Caret, a.ui.State("bar.text").Anchor = 0, 0
	a.ui.Invalidate()
}

// sessionOverlay is what floats over a picture: the toolbar (or the handle that brings it
// back), the box for sending text, the statistics, the modifier keys held down, and notes
// about the connection.
func (a *App) sessionOverlay(t *tab, s *session, dim bool) kit.Node {
	p := a.ui.Pal
	layers := []kit.Node{}
	if !dim {
		top := &kit.Col{Gap: theme.S2, Align: kit.AlignCenter}
		if t.barShown || t.textOpen {
			top.Children = append(top.Children, a.toolbar(t, s))
			if t.textOpen && s.canText() {
				top.Children = append(top.Children, a.textBar(t, s))
			}
		} else {
			top.Children = append(top.Children, a.barHandle(t))
		}
		layers = append(layers, kit.Aligned{H: kit.AlignCenter, V: kit.AlignStart, Child: kit.Pad{In: kit.Insets{T: theme.S2}, Child: top}})
		if t.stats {
			layers = append(layers, kit.Aligned{H: kit.AlignStart, V: kit.AlignEnd, Child: kit.Pad{In: kit.All(theme.S4), Child: a.statsCard(s)}})
		}
		if badges := a.modifierBadges(s); badges != nil {
			layers = append(layers, kit.Aligned{H: kit.AlignEnd, V: kit.AlignEnd, Child: kit.Pad{In: kit.All(theme.S4), Child: badges}})
		}
		if stale, failed := s.problems(); stale > 0 || failed > 0 {
			msg := a.tr.Count(i18n.KeySessStaleOne, i18n.KeySessStaleOther, stale)
			if stale == 0 {
				msg = a.tr.Count(i18n.KeySessFailedOne, i18n.KeySessFailedOther, failed)
			}
			layers = append(layers, kit.Aligned{H: kit.AlignCenter, V: kit.AlignEnd, Child: kit.Pad{In: kit.All(theme.S4), Child: &kit.Box{
				Fill: theme.Mix(p.Surface, p.Warn, 0.12), Border: p.Warn, Radius: theme.RadiusMedium, Pad: kit.Sym(theme.S4, theme.S3),
				Child: &kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
					&kit.IconNode{Icon: kit.IconWarning, Color: p.Warn},
					&kit.Label{Text: msg, Color: p.Text},
				}}}}})
		}
	}
	if t.state == tabReconnecting {
		layers = append(layers, kit.Aligned{H: kit.AlignCenter, V: kit.AlignCenter, Child: kit.Pad{In: kit.All(theme.S5), Child: kit.Sized{MaxW: 480, Child: a.reconnectCard(t)}}})
	}
	return kit.Stack{Children: layers}
}

func (a *App) barHandle(t *tab) kit.Node {
	p := a.ui.Pal
	fill := theme.WithAlpha(p.Surface, 215)
	if a.ui.IsHot("bar.handle") {
		fill = p.Surface
	}
	return &kit.Clickable{ID: "bar.handle", Radius: theme.RadiusMedium, OnClick: func() { a.showBar(t) }, Child: &kit.Box{
		Fill: fill, Border: theme.WithAlpha(p.Border, 200), Radius: theme.RadiusMedium, Pad: kit.Sym(theme.S4, 2),
		Child: &kit.IconNode{Icon: kit.IconChevronDown, Size: 16, Color: p.TextDim},
	}}
}

// toolbar is the controls of a session, in a card at the top of the picture. It is made
// in up to three sizes and the first that fits the window is used: with the buttons'
// words, with only their icons, and with only the controls (no name, no "host checked"):
// in a language with long words, or a narrow window, the first does not fit.
func (a *App) toolbar(t *tab, s *session) kit.Node {
	room := a.ui.Size().W - 2*theme.S4
	var last kit.Node
	for _, v := range []struct{ words, who bool }{{true, true}, {false, true}, {false, false}} {
		last = a.toolbarVariant(t, s, v.words, v.who)
		if last.Measure(a.ui, kit.Inf).W <= room {
			return last
		}
	}
	return last
}

func (a *App) toolbarVariant(t *tab, s *session, words, who bool) kit.Node {
	p, tr := a.ui.Pal, a.tr
	btn := func(id kit.ID, icon kit.Icon, label string, selected bool, kind kit.ButtonKind, on func()) kit.Node {
		return &kit.Button{ID: "bar." + id, Icon: icon, Label: label, IconOnly: !words, Kind: kind, Compact: true, Selected: selected, OnClick: on}
	}
	row := &kit.Row{Gap: theme.S1, Align: kit.AlignCenter}
	if who {
		name := t.title
		if n := s.hostName(); n != "" && n != name {
			name = name + " · " + n
		}
		row.Children = append(row.Children,
			&kit.Dot{Color: p.Good},
			hgap(theme.S1),
			kit.Sized{MaxW: 220, Child: &kit.Label{Text: name, Weight: text.Semibold, Ellipsis: true}},
			hgap(theme.S1),
			&kit.Chip{Text: tr.T(i18n.KeyBarHostChecked), Tone: kit.ToneGood},
			hgap(theme.S2),
			&kit.Divider{Color: p.Border, Vertical: true},
			hgap(theme.S1),
		)
	}
	row.Children = append(row.Children,
		btn("fit", kit.IconFit, tr.T(i18n.KeyBarFit), s.mode == view.ModeFit, kit.Ghost, func() {
			if s.mode != view.ModeFit {
				a.toggleMode(s)
			}
		}),
		btn("actual", kit.IconActualSize, tr.T(i18n.KeyBarActual), s.mode == view.ModeActual, kit.Ghost, func() {
			if s.mode != view.ModeActual {
				a.toggleMode(s)
			}
		}),
	)
	fsLabel := tr.T(i18n.KeyBarFullscreen)
	if a.fullscreen {
		fsLabel = tr.T(i18n.KeyBarExitFullscreen)
	}
	row.Children = append(row.Children, btn("fullscreen", kit.IconFullscreen, fsLabel, a.fullscreen, kit.Ghost, func() { a.setFullscreen(!a.fullscreen) }))
	if s.canText() {
		row.Children = append(row.Children, btn("text", kit.IconText, tr.T(i18n.KeyBarSendText), t.textOpen, kit.Ghost, func() {
			if t.textOpen {
				a.closeText(t)
			} else {
				a.openText(t)
			}
		}))
	}
	row.Children = append(row.Children, btn("release", kit.IconKeyRelease, tr.T(i18n.KeyBarReleaseKeys), false, kit.Ghost, s.releaseKeys))
	if a.koreanKeys() {
		row.Children = append(row.Children,
			&kit.Button{ID: "bar.hangul", Label: tr.T(i18n.KeyBarHangul), Kind: kit.Ghost, Compact: true, OnClick: func() { s.pressKey(vkHangul) }},
			&kit.Button{ID: "bar.hanja", Label: tr.T(i18n.KeyBarHanja), Kind: kit.Ghost, Compact: true, OnClick: func() { s.pressKey(vkHanja) }},
		)
	}
	row.Children = append(row.Children,
		btn("stats", kit.IconInfo, tr.T(i18n.KeyBarStats), t.stats, kit.Ghost, func() { t.stats = !t.stats; a.ui.Invalidate() }),
		hgap(theme.S1),
		&kit.Divider{Color: p.Border, Vertical: true},
		hgap(theme.S1),
		btn("close", kit.IconUnplug, tr.T(i18n.KeyDisconnect), false, kit.Danger, func() { a.closeTab(a.active) }),
	)
	return &kit.Box{
		Fill: theme.WithAlpha(p.Surface, 240), Border: p.Border, Radius: theme.RadiusLarge, Shadow: true, ShadowC: theme.WithAlpha(p.Shadow, 90), ShadowB: 18, ShadowY: 6,
		Pad: kit.Sym(theme.S3, theme.S2), Child: row,
	}
}

// textBar is the box under the toolbar for typing text that is then typed on the other PC.
func (a *App) textBar(t *tab, s *session) kit.Node {
	p, tr := a.ui.Pal, a.tr
	send := func() { a.sendTyped(t, s) }
	return &kit.Stack{Children: []kit.Node{kit.EscapeHandler{OnEscape: func() { a.closeText(t) }}, &kit.Box{
		Fill: theme.WithAlpha(p.Surface, 245), Border: p.Border, Radius: theme.RadiusLarge, Shadow: true, ShadowC: theme.WithAlpha(p.Shadow, 90), ShadowB: 18, ShadowY: 6,
		Pad: kit.Sym(theme.S3, theme.S2),
		Child: kit.Sized{W: 520, Child: &kit.Row{Gap: theme.S2, Align: kit.AlignCenter, Children: []kit.Node{
			kit.Grow(1, &kit.TextField{ID: "bar.text", Value: &t.text, Placeholder: tr.T(i18n.KeyBarTextPlaceholder), MaxLen: 512, OnSubmit: send}),
			&kit.Button{ID: "bar.send", Label: tr.T(i18n.KeyBarSend), Kind: kit.Primary, Compact: true, OnClick: send},
			&kit.Button{ID: "bar.textclose", Icon: kit.IconClose, IconOnly: true, Kind: kit.Ghost, Compact: true, OnClick: func() { a.closeText(t) }},
		}}},
	}}}
}

func (a *App) statsCard(s *session) kit.Node {
	p, tr := a.ui.Pal, a.tr
	rtt, rate, ok := s.link()
	line := ""
	if ok {
		line = tr.T(i18n.KeyStatsLine, "rtt", int(rtt.Milliseconds()), "fps", s.fps(), "rate", humanBytes(rate))
	}
	s.mu.Lock()
	fw, fh := s.fw, s.fh
	s.mu.Unlock()
	kids := []kit.Node{}
	if line != "" {
		kids = append(kids, &kit.Label{Text: line})
	}
	kids = append(kids, &kit.Label{Text: tr.T(i18n.KeyStatsPicture, "w", fw, "h", fh), Color: p.TextDim})
	return &kit.Box{
		Fill: theme.WithAlpha(p.Surface, 235), Border: p.Border, Radius: theme.RadiusMedium, Pad: kit.Sym(theme.S3, theme.S2),
		Child: &kit.Col{Gap: theme.S1, Children: kids},
	}
}

// humanBytes writes a number of bytes as KB or MB.
func humanBytes(b float64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", b/(1<<10))
	}
	return fmt.Sprintf("%.0f B", b)
}

// modifierBadges shows which modifier keys are held down on the remote PC, so that a Ctrl
// that stayed down is seen.
func (a *App) modifierBadges(s *session) kit.Node {
	ctrl, alt, shift, win := s.modifiers()
	row := &kit.Row{Gap: theme.S1, Align: kit.AlignCenter}
	for _, m := range []struct {
		on   bool
		name string
	}{{ctrl, "Ctrl"}, {alt, "Alt"}, {shift, "Shift"}, {win, "Win"}} {
		if m.on {
			row.Children = append(row.Children, &kit.Chip{Text: m.name, Tone: kit.ToneAccent})
		}
	}
	if len(row.Children) == 0 {
		return nil
	}
	return row
}

func (a *App) reconnectCard(t *tab) kit.Node {
	p, tr := a.ui.Pal, a.tr
	return a.card("", p.Warn,
		&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconRefresh, Color: p.Warn},
			kit.Grow(1, &kit.Label{Text: tr.T(i18n.KeyReconnectTitle, "n", int(t.attempts.Load())), Weight: text.Semibold, Wrap: true}),
		}},
		&kit.Progress{Indeterminate: true},
		&kit.Row{Children: []kit.Node{
			&kit.Button{ID: "bar.stop", Label: tr.T(i18n.KeyReconnectStop), OnClick: func() { a.stopReconnect(t) }},
		}},
	)
}
