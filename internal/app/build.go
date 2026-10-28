package app

import (
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// build makes the tree for the state the application is in.
func (a *App) build() kit.Node {
	p := a.ui.Pal
	pt, _, _ := a.pictureTab()
	live := pt != nil
	col := &kit.Col{Align: kit.AlignStretch}
	if !(live && a.fullscreen) { // a full-screen picture has the whole screen
		if b := a.banner(); b != nil {
			col.Children = append(col.Children, b)
		}
		if len(a.tabs) > 0 {
			col.Children = append(col.Children, a.tabStrip())
		}
	}
	col.Children = append(col.Children, kit.Grow(1, a.content()))

	var root kit.Node = col
	if !live {
		root = &kit.Box{Fill: p.BG, Child: col}
	}
	layers := []kit.Node{root}
	if a.dialog != nil {
		layers = append(layers, a.dialog())
	}
	if len(a.toasts) > 0 {
		items := make([]kit.ToastItem, len(a.toasts))
		for i, t := range a.toasts {
			items[i] = kit.ToastItem{ID: t.id, Text: t.text, Tone: t.tone}
		}
		layers = append(layers, &kit.Toasts{Items: items})
	}
	return kit.Stack{Children: layers}
}

// liveTab is the session tab showing a picture, or nil.
func (a *App) liveTab() *tab {
	if t := a.tabAt(a.active); t != nil && t.state == tabLive {
		return t
	}
	return nil
}

// Typography.

func (a *App) title(s string) *kit.Label {
	return &kit.Label{Text: s, Size: theme.SizeTitle, Weight: text.Semibold}
}
func (a *App) heading(s string) *kit.Label {
	return &kit.Label{Text: s, Size: theme.SizeSub, Weight: text.Semibold}
}
func (a *App) body(s string) *kit.Label { return &kit.Label{Text: s, Wrap: true} }
func (a *App) dim(s string) *kit.Label {
	return &kit.Label{Text: s, Color: a.ui.Pal.TextDim, Wrap: true}
}
func (a *App) caption(s string) *kit.Label {
	return &kit.Label{Text: s, Size: theme.SizeCaption, Color: a.ui.Pal.TextDim, Wrap: true}
}

func gap(h float64) kit.Node  { return kit.Sized{H: h, Child: kit.Empty{}} }
func hgap(w float64) kit.Node { return kit.Sized{W: w, Child: kit.Empty{}} }

// card is a titled panel. border, if it has alpha, replaces the usual outline.
func (a *App) card(title string, border kit.Color, children ...kit.Node) kit.Node {
	p := a.ui.Pal
	kids := make([]kit.Node, 0, len(children)+1)
	if title != "" {
		kids = append(kids, a.heading(title))
	}
	kids = append(kids, children...)
	if border.A == 0 {
		border = p.Border
	}
	return &kit.Box{
		Fill: p.Surface, Border: border, Radius: theme.RadiusMedium, Pad: kit.All(theme.S4),
		Child: &kit.Col{Gap: theme.S3, Align: kit.AlignStretch, Children: kids},
	}
}

// pageFrame puts a page's content in a scrolling column with its title.
func (a *App) pageFrame(id kit.ID, title string, children ...kit.Node) kit.Node {
	kids := append([]kit.Node{a.title(title)}, children...)
	return &kit.Scroll{ID: id, Child: kit.Pad{
		In:    kit.All(theme.S5),
		Child: kit.Sized{MaxW: 960, Child: &kit.Col{Gap: theme.S4, Align: kit.AlignStretch, Children: kids}},
	}}
}

// labelled is a form row: a small caption above its control.
func (a *App) labelled(label string, control kit.Node) kit.Node {
	return &kit.Col{Gap: theme.S1, Align: kit.AlignStretch, Children: []kit.Node{a.caption(label), control}}
}

// secretField is a password field with a button that shows what was typed, so that a code
// or password can be checked before it is sent. It never uses the input method, shown or
// not: an input method would turn the letters of a password into a word.
func (a *App) secretField(f *kit.TextField) kit.Node {
	shown := a.revealed[f.ID]
	f.Mask, f.NoIME = !shown, true
	label := a.tr.T(i18n.KeyShowPw)
	if shown {
		label = a.tr.T(i18n.KeyHidePw)
	}
	id := f.ID
	return &kit.Row{Gap: theme.S1, Align: kit.AlignCenter, Children: []kit.Node{
		kit.Grow(1, f),
		&kit.Button{ID: id + "/show", Label: label, Kind: kit.Ghost, Compact: true, OnClick: func() {
			a.revealed[id] = !shown
			a.ui.Invalidate()
		}},
	}}
}

// banner is the strip shown on every page while the host is in open mode: the dangerous
// state stays in front of the person for as long as it lasts.
func (a *App) banner() kit.Node {
	info := a.cfg.Host.Info()
	if info.Err != "" || info.RequireAuth {
		return nil
	}
	p := a.ui.Pal
	return &kit.Box{
		Fill: p.Bad, Pad: kit.Sym(theme.S4, theme.S2),
		Child: &kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconWarning, Color: p.OnBad},
			kit.Grow(1, &kit.Label{Text: a.tr.T(i18n.KeyBannerOpen), Color: p.OnBad, Weight: text.Semibold, Wrap: true}),
			&kit.Button{ID: "banner.strict", Label: a.tr.T(i18n.KeyBannerSwitch), Compact: true, OnClick: func() { a.setStrict(true) }},
		}},
	}
}

// content is everything below the banner and the tab strip.
func (a *App) content() kit.Node {
	t := a.tabAt(a.active)
	switch {
	case t == nil:
		return &kit.Row{Align: kit.AlignStretch, Children: []kit.Node{a.sidebar(), kit.Grow(1, a.pageContent())}}
	}
	if pt, ps, dim := a.pictureTab(); pt != nil && ps != nil {
		return kit.Stack{Children: []kit.Node{&viewport{a: a}, a.sessionOverlay(pt, ps, dim)}}
	}
	return a.sessionCard(t)
}

func (a *App) pageContent() kit.Node {
	switch a.page {
	case PageThisPC:
		return a.pageThisPC()
	case PageLogs:
		return a.pageLogs()
	case PageSettings:
		return a.pageSettings()
	}
	return a.pageDevices()
}

// viewport is the part of the window the remote picture is shown in. It draws nothing
// itself (the picture is drawn under the interface layer) and only records where it is.
type viewport struct {
	a *App
	r kit.Rect
}

func (v *viewport) Measure(*kit.UI, float64) kit.Size { return kit.Size{} }
func (v *viewport) Place(u *kit.UI, r kit.Rect)       { v.a.viewRect = r }
func (v *viewport) Paint(*kit.UI, kit.Canvas)         {}

// sidebar is the navigation down the left, with the version and the update button at the
// bottom. Below 760 logical pixels of window it shrinks to icons.
func (a *App) sidebar() kit.Node {
	p := a.ui.Pal
	narrow := a.ui.Size().W < theme.NarrowAt
	w := theme.SidebarW
	if narrow {
		w = theme.SidebarWs
	}
	tr := a.tr
	items := []struct {
		page  Page
		icon  kit.Icon
		label string
		id    kit.ID
	}{
		{PageDevices, kit.IconMonitor, tr.T(i18n.KeyNavDevices), "nav.devices"},
		{PageThisPC, kit.IconUser, tr.T(i18n.KeyNavThisPC), "nav.pc"},
		{PageLogs, kit.IconLogs, tr.T(i18n.KeyNavLogs), "nav.logs"},
		{PageSettings, kit.IconSettings, tr.T(i18n.KeyNavSettings), "nav.settings"},
	}
	col := &kit.Col{Gap: theme.S1, Align: kit.AlignStretch}
	if !narrow {
		col.Children = append(col.Children,
			kit.Pad{In: kit.Insets{L: theme.S3, T: theme.S2, B: theme.S4}, Child: &kit.Label{Text: tr.T(i18n.KeyAppName), Size: theme.SizeTitle, Weight: text.Semibold, Color: p.Accent}})
	} else {
		col.Children = append(col.Children, gap(theme.S4))
	}
	for _, it := range items {
		it := it
		sel := a.page == it.page
		fg := p.TextDim
		if sel {
			fg = p.Text
		}
		row := &kit.Row{Gap: theme.S3, Align: kit.AlignCenter}
		row.Children = append(row.Children, &kit.IconNode{Icon: it.icon, Color: func() kit.Color {
			if sel {
				return p.Accent
			}
			return fg
		}()})
		if !narrow {
			row.Children = append(row.Children, &kit.Label{Text: it.label, Color: fg, Weight: func() text.Weight {
				if sel {
					return text.Semibold
				}
				return text.Regular
			}()})
		}
		var inner kit.Node = kit.Pad{In: kit.Sym(theme.S3, 0), Child: kit.Sized{H: 40, Child: kit.Aligned{V: kit.AlignCenter, H: func() kit.Align {
			if narrow {
				return kit.AlignCenter
			}
			return kit.AlignStart
		}(), Child: row}}}
		if sel {
			inner = &kit.Box{Fill: p.Surface2, Radius: theme.RadiusMedium, Child: inner}
		}
		col.Children = append(col.Children, &kit.Clickable{ID: it.id, Radius: theme.RadiusMedium, Hover: !sel, Child: inner,
			OnClick: func() { a.page = it.page; a.ui.Invalidate() }})
	}
	col.Children = append(col.Children, kit.Spacer())
	col.Children = append(col.Children, a.sidebarFooter(narrow))
	return kit.Sized{W: w, Child: &kit.Row{Align: kit.AlignStretch, Children: []kit.Node{
		kit.Grow(1, &kit.Box{Fill: p.Surface, Pad: kit.Sym(theme.S2, theme.S2), Child: col}),
		&kit.Divider{Color: p.Border, Vertical: true},
	}}}
}

func (a *App) sidebarFooter(narrow bool) kit.Node {
	p := a.ui.Pal
	tr := a.tr
	col := &kit.Col{Gap: theme.S2, Align: kit.AlignStretch}
	ver := tr.T(i18n.KeyVersionLabel, "version", a.cfg.Version)
	if a.cfg.Version == "dev" {
		ver = tr.T(i18n.KeyDevBuild)
	}
	if u := a.updateButton(narrow); u != nil {
		col.Children = append(col.Children, u)
	}
	if !narrow {
		col.Children = append(col.Children, kit.Pad{In: kit.Sym(theme.S3, 0), Child: &kit.Label{Text: ver, Size: theme.SizeCaption, Color: p.TextDim}})
	}
	col.Children = append(col.Children, &kit.Button{
		ID: "nav.quit", Label: tr.T(i18n.KeyQuit), Icon: kit.IconPower, Kind: kit.Ghost, Compact: true, IconOnly: narrow,
		OnClick: func() { a.openQuitDialog() },
	})
	return col
}

// updateButton is the sidebar's update control; nil in a build that does not update.
func (a *App) updateButton(narrow bool) kit.Node {
	u := a.cfg.Updater
	if u == nil {
		return nil
	}
	p := a.ui.Pal
	st := u.Status()
	label, ready := a.updateLabel(st)
	bg := p.Surface2
	fg := p.Text
	if ready {
		bg, fg = p.Accent, p.OnAccent
	}
	var inner kit.Node
	if narrow {
		inner = kit.Aligned{H: kit.AlignCenter, Child: &kit.IconNode{Icon: kit.IconDownload, Color: fg}}
	} else {
		inner = &kit.Row{Gap: theme.S2, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconDownload, Color: fg},
			kit.Grow(1, &kit.Label{Text: label, Size: theme.SizeCaption + 1, Color: fg, Wrap: true}),
		}}
	}
	return &kit.Clickable{ID: "nav.update", Radius: theme.RadiusMedium, Child: &kit.Box{
		Fill: bg, Radius: theme.RadiusMedium, Pad: kit.Sym(theme.S3, theme.S2), Child: inner,
	}, OnClick: a.updateClicked}
}

// tabStrip lists the pages and the sessions.
func (a *App) tabStrip() kit.Node {
	p := a.ui.Pal
	row := &kit.Row{Gap: theme.S1, Align: kit.AlignCenter}
	row.Children = append(row.Children, a.tabButton("tab.pages", a.tr.T(i18n.KeyNavDevices), p.TextDim, a.active == 0, -1, func() { a.setActive(0) }))
	for i, t := range a.tabs {
		i, t := i, t
		dot := p.Good
		switch t.state {
		case tabConnecting:
			dot = p.Warn
		case tabError:
			dot = p.Bad
		}
		row.Children = append(row.Children, a.tabButton(kit.ID("tab."+itoa(t.serial)), t.title, dot, a.active == i+1, i+1, func() {
			now := a.now
			if a.active == i+1 && now.Sub(t.lastClick) < 400*time.Millisecond {
				a.openRenameDialog(t)
			}
			t.lastClick = now
			a.setActive(i + 1)
		}))
	}
	return &kit.Box{Fill: p.Surface, Pad: kit.Sym(theme.S2, theme.S1), Child: &kit.Row{Align: kit.AlignCenter, Children: []kit.Node{
		kit.Grow(1, row),
	}}}
}

func (a *App) tabButton(id kit.ID, label string, dot kit.Color, active bool, closeIdx int, onClick func()) kit.Node {
	p := a.ui.Pal
	fg := p.TextDim
	if active {
		fg = p.Text
	}
	row := &kit.Row{Gap: theme.S2, Align: kit.AlignCenter}
	if closeIdx >= 0 {
		row.Children = append(row.Children, &kit.Dot{Color: dot})
	} else {
		row.Children = append(row.Children, &kit.IconNode{Icon: kit.IconMonitor, Color: fg})
	}
	row.Children = append(row.Children, kit.Grow(1, &kit.Label{Text: label, Color: fg, Ellipsis: true}))
	if closeIdx >= 0 {
		row.Children = append(row.Children, &kit.Button{ID: id + "/close", Icon: kit.IconClose, IconOnly: true, Kind: kit.Ghost, Compact: true, MinW: 28,
			OnClick: func() { a.closeTab(closeIdx) }})
	}
	var inner kit.Node = kit.Pad{In: kit.Sym(theme.S3, 0), Child: kit.Sized{H: 34, MinW: 96, MaxW: 220, Child: kit.Aligned{V: kit.AlignCenter, Child: row}}}
	if active {
		inner = &kit.Box{Fill: p.Surface2, Border: p.Border, Radius: theme.RadiusMedium, Child: inner}
	}
	return &kit.Clickable{ID: id, Radius: theme.RadiusMedium, Hover: !active, Child: inner, OnClick: onClick}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
