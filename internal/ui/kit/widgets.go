package kit

import (
	"math"

	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

var white = Color{R: 255, G: 255, B: 255, A: 255}
var black = Color{A: 255}

// Label is text: one line, or several when Wrap is set.
type Label struct {
	Text     string
	Size     float64     // default theme.SizeBody
	Weight   text.Weight // Regular or Semibold
	Color    Color       // default: the theme's text colour
	Wrap     bool        // break into lines to fit the width
	MaxLines int         // with Wrap: more lines than this are cut and end in an ellipsis
	Align    Align       // within the width: start, center or end
	Ellipsis bool        // a single line too wide for its room ends in an ellipsis

	st    text.Style
	col   Color
	lines []string
	r     Rect
}

func (l *Label) style(u *UI) text.Style {
	size := l.Size
	if size == 0 {
		size = theme.SizeBody
	}
	return u.Style(size, l.Weight)
}

func (l *Label) layout(u *UI, w float64) []string {
	st := l.style(u)
	if !l.Wrap {
		s := l.Text
		if l.Ellipsis && !math.IsInf(w, 1) {
			s = text.Truncate(u.M, s, st, w)
		}
		return []string{s}
	}
	lines := text.Wrap(u.M, l.Text, st, w)
	if l.MaxLines > 0 && len(lines) > l.MaxLines {
		lines = lines[:l.MaxLines]
		last := lines[l.MaxLines-1]
		if !math.IsInf(w, 1) {
			lines[l.MaxLines-1] = text.Truncate(u.M, last+text.Ellipsis+text.Ellipsis, st, w)
			if lines[l.MaxLines-1] == "" || lines[l.MaxLines-1] == text.Ellipsis {
				lines[l.MaxLines-1] = text.Truncate(u.M, last, st, w)
			}
		}
	}
	return lines
}

func (l *Label) Measure(u *UI, maxW float64) Size {
	st := l.style(u)
	lines := l.layout(u, maxW)
	w := 0.0
	for _, s := range lines {
		w = math.Max(w, u.M.Width(s, st))
	}
	return Size{w, float64(len(lines)) * u.LineH(st)}
}

func (l *Label) Place(u *UI, r Rect) {
	l.r, l.st = r, l.style(u)
	l.lines = l.layout(u, r.W)
	l.col = l.Color
	if l.col.A == 0 {
		l.col = u.Pal.Text
	}
}

func (l *Label) Paint(u *UI, cv Canvas) {
	lh := u.LineH(l.st)
	pad := (lh - u.M.LineHeight(l.st)) / 2
	for i, s := range l.lines {
		if s == "" {
			continue
		}
		x, _ := placeAxis(l.Align, l.r.X, l.r.W, u.M.Width(s, l.st))
		cv.Text(s, x, l.r.Y+float64(i)*lh+pad, l.st, l.col)
	}
}

// Text is a one-line label in the normal text colour.
func Text(s string) *Label { return &Label{Text: s} }

// Dim is secondary text.
func Dim(u *UI, s string) *Label { return &Label{Text: s, Color: u.Pal.TextDim} }

// Para is wrapping text.
func Para(s string) *Label { return &Label{Text: s, Wrap: true} }

// IconNode draws an icon in a square.
type IconNode struct {
	Icon  Icon
	Size  float64 // default 16
	Color Color   // default: the theme's text colour
	r     Rect
}

func (i *IconNode) size() float64 {
	if i.Size == 0 {
		return 16
	}
	return i.Size
}
func (i *IconNode) Measure(*UI, float64) Size { return Size{i.size(), i.size()} }
func (i *IconNode) Place(u *UI, r Rect) {
	s := i.size()
	i.r = Rect{r.X + (r.W-s)/2, r.Y + (r.H-s)/2, s, s}
}
func (i *IconNode) Paint(u *UI, cv Canvas) {
	c := i.Color
	if c.A == 0 {
		c = u.Pal.Text
	}
	cv.Icon(i.Icon, i.r, c)
}

// Tone is the meaning of a status colour.
type Tone uint8

const (
	ToneNeutral Tone = iota
	ToneAccent
	ToneGood
	ToneWarn
	ToneBad
)

// ToneColor is the colour that says tone t in this theme.
func ToneColor(p theme.Palette, t Tone) Color {
	switch t {
	case ToneAccent:
		return p.Accent
	case ToneGood:
		return p.Good
	case ToneWarn:
		return p.Warn
	case ToneBad:
		return p.Bad
	}
	return p.TextDim
}

// Chip is a small label with a tinted background.
type Chip struct {
	Text string
	Tone Tone

	st  text.Style
	r   Rect
	col Color
	bg  Color
}

// ChipBackground is the tinted fill of a chip of tone t on a surface: as strong a tint as
// still leaves the chip's text readable (4.5:1), down to none.
func ChipBackground(p theme.Palette, t Tone) Color {
	fg := ToneColor(p, t)
	for _, tint := range []float64{0.16, 0.12, 0.09, 0.06, 0.03} {
		if bg := theme.Mix(p.Surface, fg, tint); theme.Contrast(fg, bg) >= 4.5 {
			return bg
		}
	}
	return p.Surface
}

func (c *Chip) Measure(u *UI, maxW float64) Size {
	c.st = u.Style(theme.SizeCaption, text.Semibold)
	return Size{u.M.Width(c.Text, c.st) + 16, math.Max(u.LineH(c.st)+4, 22)}
}
func (c *Chip) Place(u *UI, r Rect) {
	c.r = r
	c.col = ToneColor(u.Pal, c.Tone)
	c.bg = ChipBackground(u.Pal, c.Tone)
}
func (c *Chip) Paint(u *UI, cv Canvas) {
	cv.RoundRect(c.r, theme.RadiusSmall, c.bg)
	lh := u.LineH(c.st)
	cv.Text(c.Text, c.r.X+8, c.r.Y+(c.r.H-lh)/2+(lh-u.M.LineHeight(c.st))/2, c.st, c.col)
}

// Dot is a small filled circle: a status light.
type Dot struct {
	Color Color
	Size  float64 // default 8
	r     Rect
}

func (d *Dot) size() float64 {
	if d.Size == 0 {
		return 8
	}
	return d.Size
}
func (d *Dot) Measure(*UI, float64) Size { return Size{d.size(), d.size()} }
func (d *Dot) Place(u *UI, r Rect) {
	s := d.size()
	d.r = Rect{r.X + (r.W-s)/2, r.Y + (r.H-s)/2, s, s}
}
func (d *Dot) Paint(u *UI, cv Canvas) { cv.RoundRect(d.r, d.size()/2, d.Color) }

// Progress is a bar. With Indeterminate set, a segment slides along it.
type Progress struct {
	Value         float64 // 0 to 1
	Indeterminate bool
	r             Rect
}

func (p *Progress) Measure(u *UI, maxW float64) Size {
	if math.IsInf(maxW, 1) {
		maxW = 160
	}
	return Size{maxW, 4}
}
func (p *Progress) Place(u *UI, r Rect) { p.r = r }
func (p *Progress) Paint(u *UI, cv Canvas) {
	cv.RoundRect(p.r, 2, u.Pal.Surface2)
	if p.Indeterminate {
		const period = 1400.0
		t := math.Mod(float64(u.Now().UnixMilli()), period) / period
		w := p.r.W * 0.3
		x := p.r.X - w + (p.r.W+w)*t
		cv.PushClip(p.r)
		cv.RoundRect(Rect{x, p.r.Y, w, p.r.H}, 2, u.Pal.Accent)
		cv.PopClip()
		u.animing = true
		return
	}
	v := math.Min(math.Max(p.Value, 0), 1)
	if v > 0 {
		cv.RoundRect(Rect{p.r.X, p.r.Y, p.r.W * v, p.r.H}, 2, u.Pal.Accent)
	}
}

// ButtonKind is how prominent a button is.
type ButtonKind uint8

const (
	Secondary ButtonKind = iota
	Primary
	Danger
	Ghost
)

// Button is a labelled, clickable, focusable control.
type Button struct {
	ID       ID
	Label    string
	Icon     Icon
	Kind     ButtonKind
	OnClick  func()
	Disabled bool
	Selected bool    // drawn as the chosen one of a group
	MinW     float64 // narrower than this and the button is widened
	Compact  bool    // less padding, for toolbars
	IconOnly bool

	st  text.Style
	r   Rect
	pad float64
}

func (b *Button) height() float64 {
	if b.Compact {
		return 32
	}
	return theme.ControlH
}

func (b *Button) Measure(u *UI, maxW float64) Size {
	b.st = u.Style(theme.SizeBody, text.Semibold)
	b.pad = 16
	if b.Compact || b.IconOnly {
		b.pad = 10
	}
	w := 2 * b.pad
	if b.Icon != IconNone {
		w += 16
		if b.Label != "" && !b.IconOnly {
			w += 8
		}
	}
	if b.Label != "" && !b.IconOnly {
		w += u.M.Width(b.Label, b.st)
	}
	return Size{math.Max(w, math.Max(b.MinW, theme.MinHit)), b.height()}
}

func (b *Button) Place(u *UI, r Rect) {
	b.r = r
	b.Measure(u, r.W) // sets st and pad
	u.Register(Interactive{ID: b.ID, R: r, Focusable: true, Disabled: b.Disabled, Cursor: CursorPointer, OnClick: b.OnClick})
}

func (b *Button) colors(u *UI) (fill, border, fg Color) {
	p := u.Pal
	hot := 0.0
	if u.IsHot(b.ID) && !b.Disabled {
		hot = 1
	}
	hot = u.Anim(b.ID, "hover", hot, theme.AnimHover)
	press := u.IsActive(b.ID) && u.IsHot(b.ID) && !b.Disabled
	lift := theme.Mix(p.Surface2, p.Text, 0.07)
	switch b.Kind {
	case Primary:
		fill, fg = p.Accent, p.OnAccent
		fill = theme.Mix(fill, white, 0.12*hot)
		if press {
			fill = theme.Mix(p.Accent, black, 0.15)
		}
	case Danger:
		fill, fg = p.Bad, p.OnBad
		fill = theme.Mix(fill, white, 0.12*hot)
		if press {
			fill = theme.Mix(p.Bad, black, 0.15)
		}
	case Ghost:
		fg = p.TextDim
		if hot > 0 || b.Selected {
			fg = p.Text
		}
		fill = theme.WithAlpha(p.Surface2, uint8(255*hot))
		if b.Selected {
			fill = p.Surface2
		}
		if press {
			fill = lift
		}
	default:
		fill, border, fg = theme.Mix(p.Surface2, p.Text, 0.07*hot), p.Border, p.Text
		if b.Selected {
			fill, border = theme.Mix(p.Surface2, p.Accent, 0.22), p.Accent
		}
		if press {
			fill = lift
		}
	}
	if b.Disabled {
		fg = theme.WithAlpha(fg, 110)
		if b.Kind == Primary || b.Kind == Danger {
			fill = theme.Mix(p.Surface2, fill, 0.35)
		}
	}
	return
}

func (b *Button) Paint(u *UI, cv Canvas) {
	fill, border, fg := b.colors(u)
	if fill.A > 0 {
		cv.RoundRect(b.r, theme.RadiusMedium, fill)
	}
	if border.A > 0 {
		cv.StrokeRoundRect(b.r, theme.RadiusMedium, 1, border)
	}
	if u.ShowFocusRing(b.ID) {
		cv.StrokeRoundRect(Rect{b.r.X - 2, b.r.Y - 2, b.r.W + 4, b.r.H + 4}, theme.RadiusMedium+2, theme.FocusRing, u.Pal.Accent)
	}
	contentW := 0.0
	if b.Icon != IconNone {
		contentW += 16
		if b.Label != "" && !b.IconOnly {
			contentW += 8
		}
	}
	tw := 0.0
	if b.Label != "" && !b.IconOnly {
		tw = u.M.Width(b.Label, b.st)
	}
	contentW += tw
	x := b.r.X + (b.r.W-contentW)/2
	if b.Icon != IconNone {
		cv.Icon(b.Icon, Rect{x, b.r.Y + (b.r.H-16)/2, 16, 16}, fg)
		x += 16 + 8
	}
	if tw > 0 {
		lh := u.LineH(b.st)
		cv.Text(b.Label, x, b.r.Y+(b.r.H-lh)/2+(lh-u.M.LineHeight(b.st))/2, b.st, fg)
	}
}

// Clickable makes a region of other nodes act as one button: it is hit-tested as a
// whole, shows a hover background and is reachable with Tab.
type Clickable struct {
	ID      ID
	Child   Node
	OnClick func()
	Radius  float64
	Hover   bool // draw a hover tint behind the child
	Disable bool

	r Rect
}

func (c *Clickable) Measure(u *UI, maxW float64) Size { return c.Child.Measure(u, maxW) }
func (c *Clickable) Place(u *UI, r Rect) {
	c.r = r
	u.Register(Interactive{ID: c.ID, R: r, Focusable: true, Disabled: c.Disable, Cursor: CursorPointer, OnClick: c.OnClick})
	c.Child.Place(u, r)
}
func (c *Clickable) Paint(u *UI, cv Canvas) {
	if c.Hover {
		h := 0.0
		if u.IsHot(c.ID) && !c.Disable {
			h = 1
		}
		if h = u.Anim(c.ID, "hover", h, theme.AnimHover); h > 0 {
			cv.RoundRect(c.r, c.Radius, theme.WithAlpha(u.Pal.Surface2, uint8(255*h)))
		}
	}
	c.Child.Paint(u, cv)
	if u.ShowFocusRing(c.ID) {
		cv.StrokeRoundRect(Rect{c.r.X - 2, c.r.Y - 2, c.r.W + 4, c.r.H + 4}, c.Radius+2, theme.FocusRing, u.Pal.Accent)
	}
}

// Toggle is an on/off switch.
type Toggle struct {
	ID       ID
	On       bool
	OnChange func(on bool)
	Disabled bool
	r        Rect
}

func (t *Toggle) Measure(*UI, float64) Size { return Size{40, 24} }
func (t *Toggle) Place(u *UI, r Rect) {
	t.r = Rect{r.X, r.Y + (r.H-24)/2, 40, 24}
	u.Register(Interactive{ID: t.ID, R: t.r, Focusable: true, Disabled: t.Disabled, Cursor: CursorPointer,
		OnClick: func() {
			if t.OnChange != nil {
				t.OnChange(!t.On)
			}
		}})
}
func (t *Toggle) Paint(u *UI, cv Canvas) {
	v := 0.0
	if t.On {
		v = 1
	}
	v = u.Anim(t.ID, "on", v, theme.AnimHover)
	track := theme.Mix(u.Pal.Border, u.Pal.Accent, v)
	if t.Disabled {
		track = theme.WithAlpha(track, 110)
	}
	cv.RoundRect(t.r, 12, track)
	cv.RoundRect(Rect{t.r.X + 3 + 16*v, t.r.Y + 3, 18, 18}, 9, white)
	if u.ShowFocusRing(t.ID) {
		cv.StrokeRoundRect(Rect{t.r.X - 2, t.r.Y - 2, t.r.W + 4, t.r.H + 4}, 14, theme.FocusRing, u.Pal.Accent)
	}
}

// Segmented is a row of options, one of which is chosen. Tab reaches the chosen one; the
// arrow keys move the choice.
type Segmented struct {
	ID       ID
	Options  []string
	Selected int
	OnChange func(i int)
	Disabled []bool // optionally, one per option
	Tones    []Tone // optionally, the colour of the chosen option's fill
	MinW     float64

	st    text.Style
	cells []Rect
	r     Rect
}

func (s *Segmented) cellID(i int) ID { return ID(string(s.ID) + "/" + string(rune('a'+i))) }

func (s *Segmented) widths(u *UI) []float64 {
	s.st = u.Style(theme.SizeBody, text.Semibold)
	ws := make([]float64, len(s.Options))
	for i, o := range s.Options {
		ws[i] = math.Max(u.M.Width(o, s.st)+32, math.Max(s.MinW, 56))
	}
	return ws
}

func (s *Segmented) Measure(u *UI, maxW float64) Size {
	w := 0.0
	for _, x := range s.widths(u) {
		w += x
	}
	return Size{w + 4, theme.ControlH}
}

func (s *Segmented) Place(u *UI, r Rect) {
	ws := s.widths(u)
	s.r = Rect{r.X, r.Y, 0, theme.ControlH}
	s.cells = s.cells[:0]
	x := r.X + 2
	for i, w := range ws {
		cell := Rect{x, r.Y + 2, w, theme.ControlH - 4}
		s.cells = append(s.cells, cell)
		i := i
		disabled := i < len(s.Disabled) && s.Disabled[i]
		u.Register(Interactive{
			ID: s.cellID(i), R: cell, Focusable: i == s.Selected, Disabled: disabled, Cursor: CursorPointer,
			OnClick: func() {
				if s.OnChange != nil && i != s.Selected {
					s.OnChange(i)
				}
			},
			OnKey: func(k KeyPress) bool {
				d := 0
				switch k.Key {
				case KeyLeft, KeyUp:
					d = -1
				case KeyRight, KeyDown:
					d = 1
				default:
					return false
				}
				for j := i + d; j >= 0 && j < len(s.Options); j += d {
					if j < len(s.Disabled) && s.Disabled[j] {
						continue
					}
					if s.OnChange != nil {
						s.OnChange(j)
					}
					u.Focus(s.cellID(j), true)
					return true
				}
				return true
			},
		})
		x += w
	}
	s.r.W = x - r.X + 2
}

func (s *Segmented) Paint(u *UI, cv Canvas) {
	p := u.Pal
	cv.RoundRect(s.r, theme.RadiusMedium, p.Surface2)
	cv.StrokeRoundRect(s.r, theme.RadiusMedium, 1, p.Border)
	for i, cell := range s.cells {
		id := s.cellID(i)
		sel := i == s.Selected
		fg := p.TextDim
		if u.IsHot(id) {
			fg = p.Text
		}
		if sel {
			fill, ink := p.Accent, p.OnAccent
			if i < len(s.Tones) && s.Tones[i] == ToneBad {
				fill, ink = p.Bad, p.OnBad
			}
			cv.RoundRect(cell, theme.RadiusMedium-2, fill)
			fg = ink
		}
		if i < len(s.Disabled) && s.Disabled[i] {
			fg = theme.WithAlpha(fg, 110)
		}
		lh := u.LineH(s.st)
		tw := u.M.Width(s.Options[i], s.st)
		cv.Text(s.Options[i], cell.X+(cell.W-tw)/2, cell.Y+(cell.H-lh)/2+(lh-u.M.LineHeight(s.st))/2, s.st, fg)
		if u.ShowFocusRing(id) {
			cv.StrokeRoundRect(Rect{cell.X - 1, cell.Y - 1, cell.W + 2, cell.H + 2}, theme.RadiusMedium, theme.FocusRing, p.Accent)
		}
	}
}
