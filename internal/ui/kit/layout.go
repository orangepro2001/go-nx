package kit

import "math"

// Empty is a node that takes no room and draws nothing.
type Empty struct{}

func (Empty) Measure(*UI, float64) Size { return Size{} }
func (Empty) Place(*UI, Rect)           {}
func (Empty) Paint(*UI, Canvas)         {}

// grow marks a child of a Row or Col as one that takes a share of the space left over.
type grow struct {
	weight float64
	n      Node
}

func (g grow) Measure(u *UI, maxW float64) Size { return g.n.Measure(u, maxW) }
func (g grow) Place(u *UI, r Rect)              { g.n.Place(u, r) }
func (g grow) Paint(u *UI, cv Canvas)           { g.n.Paint(u, cv) }

// Grow makes n take a share (weight) of what is left over in a Row or Col.
func Grow(weight float64, n Node) Node { return grow{weight, n} }

// Spacer takes up the space left over in a Row or Col.
func Spacer() Node { return Grow(1, Empty{}) }

func unwrap(n Node) (Node, float64) {
	if g, ok := n.(grow); ok {
		return g.n, g.weight
	}
	return n, 0
}

// Row lays its children out left to right.
type Row struct {
	Gap      float64
	Align    Align // of the children across the row (vertically)
	Children []Node
}

// Col lays its children out top to bottom.
type Col struct {
	Gap      float64
	Align    Align // of the children across the column (horizontally)
	Children []Node
}

// visible drops nil children, so a builder can write cond-dependent entries.
func visible(ns []Node) []Node {
	out := ns[:0:0]
	for _, n := range ns {
		if n != nil {
			if _, e := n.(Empty); e {
				continue
			}
			out = append(out, n)
		}
	}
	return out
}

// rowLayout decides each child's width and height for a row at most maxW wide.
func (r Row) layout(u *UI, maxW float64) (ws []float64, sizes []Size, kids []Node) {
	kids = visible(r.Children)
	ws = make([]float64, len(kids))
	sizes = make([]Size, len(kids))
	weights := make([]float64, len(kids))
	used, total := 0.0, 0.0
	for i, k := range kids {
		inner, w := unwrap(k)
		weights[i] = w
		if w == 0 {
			s := inner.Measure(u, Inf)
			ws[i], sizes[i] = s.W, s
			used += s.W
		} else {
			total += w
		}
	}
	used += r.Gap * float64(max(len(kids)-1, 0))
	left := 0.0
	if !math.IsInf(maxW, 1) {
		left = math.Max(maxW-used, 0)
	}
	for i, k := range kids {
		if weights[i] == 0 {
			continue
		}
		inner, _ := unwrap(k)
		if math.IsInf(maxW, 1) {
			s := inner.Measure(u, Inf)
			ws[i], sizes[i] = s.W, s
			continue
		}
		ws[i] = left * weights[i] / total
		sizes[i] = inner.Measure(u, ws[i])
		sizes[i].W = ws[i]
	}
	return
}

func (r Row) Measure(u *UI, maxW float64) Size {
	ws, sizes, _ := r.layout(u, maxW)
	w, h := r.Gap*float64(max(len(ws)-1, 0)), 0.0
	for i := range ws {
		w += ws[i]
		h = math.Max(h, sizes[i].H)
	}
	return Size{w, h}
}

func (r Row) Place(u *UI, rc Rect) {
	ws, sizes, kids := r.layout(u, rc.W)
	x := rc.X
	for i, k := range kids {
		y, h := placeAxis(r.Align, rc.Y, rc.H, sizes[i].H)
		inner, _ := unwrap(k)
		inner.Place(u, Rect{x, y, ws[i], h})
		x += ws[i] + r.Gap
	}
}

func (r Row) Paint(u *UI, cv Canvas) {
	for _, k := range visible(r.Children) {
		inner, _ := unwrap(k)
		inner.Paint(u, cv)
	}
}

func (c Col) layout(u *UI, maxW float64) (hs []float64, sizes []Size, weights []float64, kids []Node) {
	kids = visible(c.Children)
	hs = make([]float64, len(kids))
	sizes = make([]Size, len(kids))
	weights = make([]float64, len(kids))
	for i, k := range kids {
		inner, w := unwrap(k)
		weights[i] = w
		sizes[i] = inner.Measure(u, maxW)
		hs[i] = sizes[i].H
	}
	return
}

func (c Col) Measure(u *UI, maxW float64) Size {
	hs, sizes, _, _ := c.layout(u, maxW)
	w, h := 0.0, c.Gap*float64(max(len(hs)-1, 0))
	for i := range hs {
		w = math.Max(w, sizes[i].W)
		h += hs[i]
	}
	return Size{w, h}
}

func (c Col) Place(u *UI, rc Rect) {
	hs, sizes, weights, kids := c.layout(u, rc.W)
	used, total := c.Gap*float64(max(len(hs)-1, 0)), 0.0
	for i := range hs {
		if weights[i] == 0 {
			used += hs[i]
		} else {
			total += weights[i]
		}
	}
	left := math.Max(rc.H-used, 0)
	y := rc.Y
	for i, k := range kids {
		h := hs[i]
		if weights[i] > 0 {
			h = left * weights[i] / total
		}
		x, w := placeAxis(c.Align, rc.X, rc.W, sizes[i].W)
		inner, _ := unwrap(k)
		inner.Place(u, Rect{x, y, w, h})
		y += h + c.Gap
	}
}

func (c Col) Paint(u *UI, cv Canvas) {
	for _, k := range visible(c.Children) {
		inner, _ := unwrap(k)
		inner.Paint(u, cv)
	}
}

// Pad puts space around a child.
type Pad struct {
	In    Insets
	Child Node
}

func (p Pad) Measure(u *UI, maxW float64) Size {
	s := p.Child.Measure(u, math.Max(maxW-p.In.Horizontal(), 0))
	return Size{s.W + p.In.Horizontal(), s.H + p.In.Vertical()}
}
func (p Pad) Place(u *UI, r Rect)    { p.Child.Place(u, r.Inset(p.In)) }
func (p Pad) Paint(u *UI, cv Canvas) { p.Child.Paint(u, cv) }

// Sized gives a child a fixed or bounded size. A zero field leaves that bound off; W and H
// fix the size, Min and Max bound it.
type Sized struct {
	W, H       float64
	MinW, MinH float64
	MaxW, MaxH float64
	Child      Node
}

func (s Sized) clamp(sz Size) Size {
	if s.W > 0 {
		sz.W = s.W
	}
	if s.H > 0 {
		sz.H = s.H
	}
	if s.MinW > 0 {
		sz.W = math.Max(sz.W, s.MinW)
	}
	if s.MinH > 0 {
		sz.H = math.Max(sz.H, s.MinH)
	}
	if s.MaxW > 0 {
		sz.W = math.Min(sz.W, s.MaxW)
	}
	if s.MaxH > 0 {
		sz.H = math.Min(sz.H, s.MaxH)
	}
	return sz
}

func (s Sized) Measure(u *UI, maxW float64) Size {
	if s.W > 0 {
		maxW = math.Min(maxW, s.W)
	}
	if s.MaxW > 0 {
		maxW = math.Min(maxW, s.MaxW)
	}
	return s.clamp(s.Child.Measure(u, maxW))
}

func (s Sized) Place(u *UI, r Rect) {
	if s.MaxW > 0 && r.W > s.MaxW {
		r.W = s.MaxW
	}
	s.Child.Place(u, r)
}
func (s Sized) Paint(u *UI, cv Canvas) { s.Child.Paint(u, cv) }

// Stack puts its children on top of each other, all in the same rectangle; later ones
// draw over and receive input before earlier ones.
type Stack struct{ Children []Node }

func (s Stack) Measure(u *UI, maxW float64) Size {
	var out Size
	for _, k := range visible(s.Children) {
		sz := k.Measure(u, maxW)
		out.W, out.H = math.Max(out.W, sz.W), math.Max(out.H, sz.H)
	}
	return out
}
func (s Stack) Place(u *UI, r Rect) {
	for _, k := range visible(s.Children) {
		k.Place(u, r)
	}
}
func (s Stack) Paint(u *UI, cv Canvas) {
	for _, k := range visible(s.Children) {
		k.Paint(u, cv)
	}
}

// Align positions a child within the room it is given, without stretching it.
type Aligned struct {
	H, V  Align
	Child Node
}

func (a Aligned) Measure(u *UI, maxW float64) Size { return a.Child.Measure(u, maxW) }
func (a Aligned) Place(u *UI, r Rect) {
	s := a.Child.Measure(u, r.W)
	x, w := placeAxis(a.H, r.X, r.W, s.W)
	y, h := placeAxis(a.V, r.Y, r.H, s.H)
	a.Child.Place(u, Rect{x, y, w, h})
}
func (a Aligned) Paint(u *UI, cv Canvas) { a.Child.Paint(u, cv) }

// Box paints a rounded background, an outline and a shadow behind a child, with padding.
type Box struct {
	Fill    Color   // zero alpha: no fill
	Border  Color   // zero alpha: no outline
	BorderW float64 // default 1
	Radius  float64
	Shadow  bool // a soft shadow under the box (cards, dialogs)
	ShadowC Color
	ShadowB float64 // blur; default 8
	ShadowY float64 // offset; default 2
	Pad     Insets
	Child   Node

	r Rect
}

func (b *Box) Measure(u *UI, maxW float64) Size {
	s := b.Child.Measure(u, math.Max(maxW-b.Pad.Horizontal(), 0))
	return Size{s.W + b.Pad.Horizontal(), s.H + b.Pad.Vertical()}
}

func (b *Box) Place(u *UI, r Rect) {
	b.r = r
	b.Child.Place(u, r.Inset(b.Pad))
}

func (b *Box) Paint(u *UI, cv Canvas) {
	if b.Shadow {
		blur, dy := b.ShadowB, b.ShadowY
		if blur == 0 {
			blur = 8
		}
		if dy == 0 {
			dy = 2
		}
		cv.Shadow(b.r, b.Radius, blur, dy, b.ShadowC)
	}
	if b.Fill.A > 0 {
		cv.RoundRect(b.r, b.Radius, b.Fill)
	}
	if b.Border.A > 0 {
		w := b.BorderW
		if w == 0 {
			w = 1
		}
		cv.StrokeRoundRect(b.r, b.Radius, w, b.Border)
	}
	b.Child.Paint(u, cv)
}

// Divider is a thin line across a Col, or down a Row (set Vertical).
type Divider struct {
	Color    Color
	Vertical bool
	r        Rect
}

func (d *Divider) Measure(u *UI, maxW float64) Size {
	if d.Vertical {
		return Size{1, 0}
	}
	if math.IsInf(maxW, 1) {
		maxW = 0
	}
	return Size{maxW, 1}
}
func (d *Divider) Place(u *UI, r Rect) { d.r = r }
func (d *Divider) Paint(u *UI, cv Canvas) {
	if d.Vertical {
		cv.FillRect(Rect{d.r.X, d.r.Y, 1, d.r.H}, d.Color)
		return
	}
	cv.FillRect(Rect{d.r.X, d.r.Y, d.r.W, 1}, d.Color)
}

// Grid lays its children out in rows of equal-width cells: as many columns as fit with
// each cell at least MinCell wide, the cells then widened to use the whole width. A row is
// as tall as its tallest child.
type Grid struct {
	Gap      float64
	MinCell  float64
	Children []Node
}

func (g Grid) columns(maxW float64) int {
	if math_IsInf(maxW) || g.MinCell <= 0 {
		return max(len(visible(g.Children)), 1)
	}
	return max(int((maxW+g.Gap)/(g.MinCell+g.Gap)), 1)
}

func math_IsInf(f float64) bool { return f > 1e300 }

func (g Grid) cell(maxW float64, cols int) float64 {
	if math_IsInf(maxW) {
		return g.MinCell
	}
	return (maxW - g.Gap*float64(cols-1)) / float64(cols)
}

// rows measures the children at the cell width and returns the height of each row.
func (g Grid) rows(u *UI, maxW float64) (kids []Node, cols int, cw float64, heights []float64) {
	kids = visible(g.Children)
	cols = g.columns(maxW)
	cw = g.cell(maxW, cols)
	for i := 0; i < len(kids); i += cols {
		h := 0.0
		for j := i; j < min(i+cols, len(kids)); j++ {
			h = max(h, kids[j].Measure(u, cw).H)
		}
		heights = append(heights, h)
	}
	return
}

func (g Grid) Measure(u *UI, maxW float64) Size {
	_, cols, cw, hs := g.rows(u, maxW)
	h := g.Gap * float64(max(len(hs)-1, 0))
	for _, x := range hs {
		h += x
	}
	return Size{cw*float64(cols) + g.Gap*float64(cols-1), h}
}

func (g Grid) Place(u *UI, r Rect) {
	kids, cols, cw, hs := g.rows(u, r.W)
	y := r.Y
	for row, h := range hs {
		for c := 0; c < cols; c++ {
			i := row*cols + c
			if i >= len(kids) {
				break
			}
			kids[i].Place(u, Rect{r.X + float64(c)*(cw+g.Gap), y, cw, h})
		}
		y += h + g.Gap
	}
}

func (g Grid) Paint(u *UI, cv Canvas) {
	for _, k := range visible(g.Children) {
		k.Paint(u, cv)
	}
}

// EscapeHandler makes Escape do something when nothing focused uses the key. It takes no
// room and draws nothing.
type EscapeHandler struct{ OnEscape func() }

func (EscapeHandler) Measure(*UI, float64) Size { return Size{} }
func (e EscapeHandler) Place(u *UI, r Rect)     { u.OnEscape(e.OnEscape) }
func (EscapeHandler) Paint(*UI, Canvas)         {}
