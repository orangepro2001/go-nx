// Package kit is go-nx's small interface toolkit: a tree of nodes that is measured, laid
// out, painted onto a Canvas and given pointer and keyboard input. All sizes are logical
// pixels; the Canvas turns them into device pixels. Nothing here touches a window, a GPU
// or the clock, so every behaviour can be driven by a test that hands in an Input and
// looks at what a recording Canvas was asked to draw.
//
// The tree is rebuilt whenever something changed (an input handled, new data) and is
// otherwise left alone; what persists between rebuilds is in the UI, keyed by ID: focus,
// the text being edited, how far a list is scrolled, how far a fade has run.
package kit

import "math"

// Size is a width and a height.
type Size struct{ W, H float64 }

// Rect is a rectangle: X, Y is the top-left corner.
type Rect struct{ X, Y, W, H float64 }

func (r Rect) Right() float64  { return r.X + r.W }
func (r Rect) Bottom() float64 { return r.Y + r.H }

// Contains reports whether the point is inside r (the right and bottom edges are not).
func (r Rect) Contains(x, y float64) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Inset shrinks r by in on every side; the result is never negative in size.
func (r Rect) Inset(in Insets) Rect {
	return Rect{r.X + in.L, r.Y + in.T, math.Max(r.W-in.L-in.R, 0), math.Max(r.H-in.T-in.B, 0)}
}

// Intersect is the overlap of r and o; empty (zero size) if there is none.
func (r Rect) Intersect(o Rect) Rect {
	x0, y0 := math.Max(r.X, o.X), math.Max(r.Y, o.Y)
	x1, y1 := math.Min(r.Right(), o.Right()), math.Min(r.Bottom(), o.Bottom())
	if x1 <= x0 || y1 <= y0 {
		return Rect{X: x0, Y: y0}
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// Empty reports whether r has no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Insets are distances from the four edges.
type Insets struct{ L, T, R, B float64 }

// All is the same inset on every side.
func All(v float64) Insets { return Insets{v, v, v, v} }

// Sym is h on the left and right and v on the top and bottom.
func Sym(h, v float64) Insets { return Insets{h, v, h, v} }

func (i Insets) Horizontal() float64 { return i.L + i.R }
func (i Insets) Vertical() float64   { return i.T + i.B }

// Inf is "no limit" for a width.
var Inf = math.Inf(1)

// Align places a child within the space the parent gives it across the layout's
// direction.
type Align uint8

const (
	AlignStart Align = iota
	AlignCenter
	AlignEnd
	AlignStretch
)

// place returns where a size-s child goes within r along one axis.
func placeAxis(a Align, start, space, size float64) (pos, length float64) {
	switch a {
	case AlignCenter:
		return start + (space-size)/2, size
	case AlignEnd:
		return start + space - size, size
	case AlignStretch:
		return start, space
	}
	return start, size
}
