package kit

import (
	"math"

	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// Dialog is a modal card over a dimmed page. While it is in the tree, nothing below it
// can be clicked or reached with Tab, Escape calls OnCancel, and keyboard focus starts on
// DefaultFocus. Put it last in the root Stack so that it is drawn on top.
type Dialog struct {
	ID           ID
	Title        string
	Body         Node
	Actions      []Node // buttons, shown right-aligned in this order
	OnCancel     func()
	DefaultFocus ID
	W            float64 // default 440
	Danger       bool    // the title is drawn in the danger colour

	scrim, card Rect
	content     *Box
	title       *Label
}

func (d *Dialog) Measure(*UI, float64) Size { return Size{} }

func (d *Dialog) build(u *UI) {
	p := u.Pal
	d.title = &Label{Text: d.Title, Size: theme.SizeTitle, Weight: text.Semibold, Wrap: true}
	if d.Danger {
		d.title.Color = p.Bad
	}
	kids := []Node{d.title, Sized{H: theme.S3, Child: Empty{}}, d.Body}
	if len(d.Actions) > 0 {
		row := &Row{Gap: theme.S2, Align: AlignCenter, Children: append([]Node{Spacer()}, d.Actions...)}
		kids = append(kids, Sized{H: theme.S5, Child: Empty{}}, row)
	}
	d.content = &Box{
		Fill: p.Surface, Border: p.Border, Radius: theme.RadiusLarge, Shadow: true,
		ShadowC: theme.WithAlpha(p.Shadow, 90), ShadowB: 24, ShadowY: 8,
		Pad:   All(theme.S5),
		Child: &Col{Align: AlignStretch, Children: kids},
	}
}

func (d *Dialog) Place(u *UI, r Rect) {
	u.BlockBelow()
	d.scrim = r
	u.Register(Interactive{ID: d.ID + "/scrim", R: r})
	if d.OnCancel != nil {
		u.OnEscape(d.OnCancel)
	} else {
		u.OnEscape(func() {})
	}
	d.build(u)
	w := d.W
	if w == 0 {
		w = 440
	}
	w = math.Min(w, math.Max(r.W-2*theme.S4, 200))
	s := d.content.Measure(u, w)
	h := math.Min(s.H, math.Max(r.H-2*theme.S4, 120))
	d.card = Rect{r.X + (r.W-w)/2, r.Y + (r.H-h)/2, w, h}
	d.content.Place(u, d.card)
	if !u.HasFocus() && d.DefaultFocus != "" {
		u.Focus(d.DefaultFocus, true)
	}
}

func (d *Dialog) Paint(u *UI, cv Canvas) {
	cv.FillRect(d.scrim, u.Pal.Scrim)
	d.content.Paint(u, cv)
}

// ToastItem is one notice.
type ToastItem struct {
	ID       ID
	Text     string
	Tone     Tone
	Action   string // optional button label
	OnAction func()
}

// Toasts is the stack of notices in the bottom-right corner. It takes no room in the
// layout; it floats over everything, and a notice receives clicks only on its action.
type Toasts struct {
	Items []ToastItem

	boxes []*Box
	rects []Rect
}

func (t *Toasts) Measure(*UI, float64) Size { return Size{} }

func (t *Toasts) Place(u *UI, r Rect) {
	t.boxes, t.rects = t.boxes[:0], t.rects[:0]
	p := u.Pal
	w := math.Min(380, r.W-2*theme.S4)
	y := r.Bottom() - theme.S4
	for _, it := range t.Items {
		row := &Row{Gap: theme.S3, Align: AlignCenter, Children: []Node{
			&Dot{Color: ToneColor(p, it.Tone)},
			Grow(1, &Label{Text: it.Text, Wrap: true}),
		}}
		if it.Action != "" {
			row.Children = append(row.Children, &Button{ID: it.ID + "/action", Label: it.Action, Kind: Ghost, Compact: true, OnClick: it.OnAction})
		}
		b := &Box{Fill: theme.Mix(p.Surface, p.Text, 0.05), Border: p.Border, Radius: theme.RadiusMedium, Shadow: true,
			ShadowC: theme.WithAlpha(p.Shadow, 70), ShadowB: 16, ShadowY: 6, Pad: Sym(theme.S4, theme.S3), Child: row}
		s := b.Measure(u, w)
		rect := Rect{r.Right() - theme.S4 - w, y - s.H, w, s.H}
		b.Place(u, rect)
		t.boxes = append(t.boxes, b)
		t.rects = append(t.rects, rect)
		y -= s.H + theme.S2
	}
}

func (t *Toasts) Paint(u *UI, cv Canvas) {
	for _, b := range t.boxes {
		b.Paint(u, cv)
	}
}
