package app

import (
	"log"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// pageDevices is where a connection starts: the quick-connect row, then the saved devices.
func (a *App) pageDevices() kit.Node {
	tr := a.tr
	return a.pageFrame("page.devices", tr.T(i18n.KeyNavDevices),
		a.quickConnect(),
		a.savedDevices(),
	)
}

func (a *App) quickConnect() kit.Node {
	tr := a.tr
	return a.card(tr.T(i18n.KeyDevQuickTitle), kit.Color{},
		&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			kit.Grow(3, &kit.TextField{ID: "quick.addr", Value: &a.quickAddr, Placeholder: tr.T(i18n.KeyDevAddrPlaceholder), MaxLen: 128, OnSubmit: a.connectQuick}),
			kit.Grow(2, a.secretField(&kit.TextField{ID: "quick.pw", Value: &a.quickPw, Placeholder: tr.T(i18n.KeyDevPwPlaceholder), MaxLen: 64, OnSubmit: a.connectQuick})),
			&kit.Button{ID: "quick.connect", Label: tr.T(i18n.KeyConnect), Kind: kit.Primary, Icon: kit.IconPlug, MinW: 120, OnClick: a.connectQuick},
		}},
		&kit.Row{Children: []kit.Node{
			&kit.Button{ID: "quick.save", Label: tr.T(i18n.KeyDevSaveAs), Kind: kit.Ghost, Compact: true, Icon: kit.IconPlus, OnClick: func() {
				a.openDeviceDialog("", strings.TrimSpace(a.quickAddr), a.quickPw)
			}},
		}},
	)
}

// connectQuick connects to what the quick-connect row holds.
func (a *App) connectQuick() {
	addr := strings.TrimSpace(a.quickAddr)
	if addr == "" {
		a.Toast(a.tr.T(i18n.KeyDevNeedAddress), kit.ToneWarn)
		a.ui.Focus("quick.addr", true)
		return
	}
	name, profile := "", ""
	for _, p := range a.profileItems() {
		if p.Addr == addr || normalizeAddr(p.Addr) == normalizeAddr(addr) {
			name, profile = p.Name, p.Name
			break
		}
	}
	a.openTab(name, addr, a.quickPw, profile)
}

// profileItems is the saved devices (the store may not expose them as a slice).
func (a *App) profileItems() []profiles.Profile {
	if l, ok := a.cfg.Profiles.(interface{ All() []profiles.Profile }); ok {
		return l.All()
	}
	return nil
}

func (a *App) savedDevices() kit.Node {
	tr := a.tr
	items := a.profileItems()
	idx := visibleDevices(items, a.devSort, a.devQuery)

	head := &kit.Col{Gap: theme.S3, Align: kit.AlignStretch, Children: []kit.Node{a.heading(tr.T(i18n.KeyDevSavedTitle))}}
	if len(items) > 0 {
		sel := 0
		if a.devSort == SortName {
			sel = 1
		}
		head.Children = append(head.Children, &kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			kit.Grow(1, kit.Sized{MaxW: 320, Child: &kit.TextField{ID: "dev.search", Value: &a.devQuery, Placeholder: tr.T(i18n.KeyDevSearchPlaceholder), Leading: kit.IconSearch, MaxLen: 64}}),
			&kit.Segmented{ID: "dev.sort", Options: []string{tr.T(i18n.KeyDevSortRecent), tr.T(i18n.KeyDevSortName)}, Selected: sel, OnChange: func(i int) {
				a.devSort = SortRecent
				if i == 1 {
					a.devSort = SortName
				}
				if err := a.cfg.Settings.Set(keySort, string(a.devSort)); err != nil {
					log.Printf("could not save setting: %v", err)
				}
				a.ui.Invalidate()
			}},
		}})
	}

	grid := kit.Grid{Gap: theme.S3, MinCell: 250}
	for _, i := range idx {
		grid.Children = append(grid.Children, a.deviceCard(items[i], i))
	}
	grid.Children = append(grid.Children, a.addCard())

	kids := []kit.Node{head}
	switch {
	case len(items) == 0:
		kids = append(kids, a.dim(tr.T(i18n.KeyDevEmpty)))
	case len(idx) == 0:
		kids = append(kids, a.dim(tr.T(i18n.KeyDevNoMatch, "query", strings.TrimSpace(a.devQuery))))
	}
	kids = append(kids, grid)
	return &kit.Col{Gap: theme.S3, Align: kit.AlignStretch, Children: kids}
}

func (a *App) deviceCard(pf profiles.Profile, index int) kit.Node {
	p := a.ui.Pal
	tr := a.tr
	id := kit.ID("dev." + itoa(index))
	fill := p.Surface
	if a.ui.IsHot(id) {
		fill = theme.Mix(p.Surface, p.Text, 0.05)
	}
	last := tr.T(i18n.KeyDevNever)
	if pf.LastUsed > 0 {
		last = tr.T(i18n.KeyDevLastAt, "when", whenText(tr, a.now, time.Unix(pf.LastUsed, 0)))
	}
	star := kit.IconStar
	starTip := tr.T(i18n.KeyDevPin)
	if pf.Pinned {
		star, starTip = kit.IconStarFilled, tr.T(i18n.KeyDevUnpin)
	}
	_ = starTip
	name := pf.Name
	connect := func() { a.openTab(name, pf.Addr, pf.Password, name) }
	starColor := p.TextDim
	if pf.Pinned {
		starColor = p.Warn
	}
	_ = starColor
	body := &kit.Col{Gap: theme.S2, Align: kit.AlignStretch, Children: []kit.Node{
		&kit.Row{Gap: theme.S3, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconMonitor, Size: 22, Color: p.Accent},
			kit.Grow(1, &kit.Col{Align: kit.AlignStretch, Children: []kit.Node{
				&kit.Label{Text: pf.Name, Weight: text.Semibold, Ellipsis: true},
				&kit.Label{Text: pf.Addr, Color: p.TextDim, Size: theme.SizeCaption + 1, Ellipsis: true},
			}}),
			&kit.Button{ID: id + "/pin", Icon: star, IconOnly: true, Kind: kit.Ghost, Compact: true, MinW: 32,
				OnClick: func() {
					if err := a.cfg.Profiles.SetPinned(name, !pf.Pinned); err != nil {
						log.Printf("save connections: %v", err)
					}
					a.ui.Invalidate()
				}},
		}},
		&kit.Label{Text: last, Size: theme.SizeCaption, Color: p.TextDim, Ellipsis: true},
		&kit.Row{Gap: theme.S1, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.Button{ID: id + "/connect", Label: tr.T(i18n.KeyConnect), Kind: kit.Primary, Compact: true, OnClick: connect},
			kit.Spacer(),
			&kit.Button{ID: id + "/edit", Icon: kit.IconEdit, IconOnly: true, Kind: kit.Ghost, Compact: true, MinW: 32,
				OnClick: func() { a.openDeviceDialog(name, pf.Addr, pf.Password) }},
			&kit.Button{ID: id + "/delete", Icon: kit.IconTrash, IconOnly: true, Kind: kit.Ghost, Compact: true, MinW: 32,
				OnClick: func() { a.openDeleteDialog(name) }},
		}},
	}}
	return &kit.Clickable{ID: id, Radius: theme.RadiusMedium, OnClick: connect, Child: &kit.Box{
		Fill: fill, Border: p.Border, Radius: theme.RadiusMedium, Pad: kit.All(theme.S4), Child: body,
	}}
}

func (a *App) addCard() kit.Node {
	p := a.ui.Pal
	fill := p.Surface
	if a.ui.IsHot("dev.add") {
		fill = theme.Mix(p.Surface, p.Text, 0.05)
	}
	return &kit.Clickable{ID: "dev.add", Radius: theme.RadiusMedium, OnClick: func() { a.openDeviceDialog("", "", "") }, Child: &kit.Box{
		Fill: fill, Border: p.Border, Radius: theme.RadiusMedium, Pad: kit.All(theme.S4),
		Child: kit.Sized{MinH: 92, Child: kit.Aligned{H: kit.AlignCenter, V: kit.AlignCenter, Child: &kit.Row{Gap: theme.S2, Align: kit.AlignCenter, Children: []kit.Node{
			&kit.IconNode{Icon: kit.IconPlus, Color: p.Accent},
			&kit.Label{Text: a.tr.T(i18n.KeyDevAdd), Weight: text.Semibold, Color: p.Accent},
		}}}},
	}}
}

// openDeviceDialog shows the form for a saved device: a new one when editing is empty, else
// the device of that name.
func (a *App) openDeviceDialog(editing, addr, pw string) {
	tr := a.tr
	name := editing
	if editing == "" && addr != "" {
		name = ""
	}
	title := tr.T(i18n.KeyDevDialogAdd)
	if editing != "" {
		title = tr.T(i18n.KeyDevDialogEdit)
	}
	var problem string
	var save func()
	save = func() {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			problem = tr.T(i18n.KeyDevNeedAddress)
			a.ui.Invalidate()
			return
		}
		n := strings.TrimSpace(name)
		if n == "" {
			n = addr
		}
		prof := profiles.Profile{Name: n, Addr: addr, Password: pw}
		if old, ok := a.cfg.Profiles.Find(editing); ok && editing != "" {
			prof.LastUsed, prof.Pinned = old.LastUsed, old.Pinned
		}
		final, err := a.cfg.Profiles.Replace(editing, prof)
		if err != nil {
			problem = tr.T(i18n.KeyDevSaveFailed, "error", err.Error())
			a.ui.Invalidate()
			return
		}
		a.closeDialog()
		a.Toast(tr.T(i18n.KeyDevSaved, "name", final), kit.ToneGood)
	}
	a.openDialog(func() kit.Node {
		fields := &kit.Col{Gap: theme.S3, Align: kit.AlignStretch, Children: []kit.Node{
			a.labelled(tr.T(i18n.KeyName), &kit.TextField{ID: "dlg.name", Value: &name, MaxLen: 48, OnSubmit: save}),
			a.labelled(tr.T(i18n.KeyAddress), &kit.TextField{ID: "dlg.addr", Value: &addr, Placeholder: tr.T(i18n.KeyDevAddrPlaceholder), MaxLen: 128, OnSubmit: save}),
			a.labelled(tr.T(i18n.KeyPassword), a.secretField(&kit.TextField{ID: "dlg.pw", Value: &pw, Placeholder: tr.T(i18n.KeyDevPwPlaceholder), MaxLen: 64, OnSubmit: save})),
		}}
		if problem != "" {
			fields.Children = append(fields.Children, &kit.Label{Text: problem, Color: a.ui.Pal.Bad, Wrap: true})
		}
		focus := kit.ID("dlg.name")
		if editing == "" && addr == "" {
			focus = "dlg.addr"
		}
		return &kit.Dialog{
			ID: "dlg.device", Title: title, Body: fields, DefaultFocus: focus, OnCancel: a.closeDialog,
			Actions: []kit.Node{
				&kit.Button{ID: "dlg.cancel", Label: tr.T(i18n.KeyCancel), OnClick: a.closeDialog},
				&kit.Button{ID: "dlg.save", Label: tr.T(i18n.KeySave), Kind: kit.Primary, OnClick: save},
			},
		}
	})
}

func (a *App) openDeleteDialog(name string) {
	tr := a.tr
	a.openDialog(func() kit.Node {
		del := func() {
			items := a.profileItems()
			for i, p := range items {
				if p.Name == name {
					if err := a.cfg.Profiles.Delete(i); err != nil {
						log.Printf("save connections: %v", err)
					}
					break
				}
			}
			a.closeDialog()
		}
		return &kit.Dialog{
			ID: "dlg.delete", Title: tr.T(i18n.KeyDelete), Danger: true, W: 400, OnCancel: a.closeDialog, DefaultFocus: "dlg.cancel",
			Body: a.body(tr.T(i18n.KeyDevDeleteAsk, "name", name)),
			Actions: []kit.Node{
				&kit.Button{ID: "dlg.cancel", Label: tr.T(i18n.KeyCancel), OnClick: a.closeDialog},
				&kit.Button{ID: "dlg.delete", Label: tr.T(i18n.KeyDelete), Kind: kit.Danger, OnClick: del},
			},
		}
	})
}

// openRenameDialog renames a session tab (and the saved device it came from).
func (a *App) openRenameDialog(t *tab) {
	tr := a.tr
	name := t.title
	var save func()
	save = func() {
		n := strings.TrimSpace(name)
		if n != "" {
			if t.profile != "" {
				final, err := a.cfg.Profiles.Rename(t.profile, n)
				if err != nil {
					log.Printf("save connections: %v", err)
				}
				t.profile, t.title = final, final
			} else {
				t.title = n
			}
		}
		a.closeDialog()
	}
	a.openDialog(func() kit.Node {
		return &kit.Dialog{
			ID: "dlg.rename", Title: tr.T(i18n.KeyRename), W: 380, OnCancel: a.closeDialog, DefaultFocus: "dlg.name",
			Body: &kit.TextField{ID: "dlg.name", Value: &name, MaxLen: 48, OnSubmit: save},
			Actions: []kit.Node{
				&kit.Button{ID: "dlg.cancel", Label: tr.T(i18n.KeyCancel), OnClick: a.closeDialog},
				&kit.Button{ID: "dlg.save", Label: tr.T(i18n.KeySave), Kind: kit.Primary, OnClick: save},
			},
		}
	})
}
