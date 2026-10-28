package kit

import (
	"math"

	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

const (
	scrollBarW   = 10.0
	scrollStep   = 48.0 // pixels per wheel line
	minThumbSize = 28.0
)

// Scroll shows a tall child through a window of the room it is given, and scrolls it with
// the wheel or by dragging its bar. Its position is kept in the UI under ID.
type Scroll struct {
	ID    ID
	Child Node

	r        Rect
	contentH float64
	max      float64
	st       *WState
	track    Rect
}

func (s *Scroll) Measure(u *UI, maxW float64) Size {
	cs := s.Child.Measure(u, maxW-scrollBarW)
	return Size{cs.W + scrollBarW, cs.H}
}

func (s *Scroll) Place(u *UI, r Rect) {
	s.r = r
	s.st = u.State(s.ID)
	cw := math.Max(r.W-scrollBarW, 0)
	s.contentH = s.Child.Measure(u, cw).H
	s.max = math.Max(s.contentH-r.H, 0)
	s.st.ScrollY = math.Min(math.Max(s.st.ScrollY, 0), s.max)
	s.st.Max = s.max

	u.Register(Interactive{ID: s.ID + "/wheel", R: r, OnWheel: func(dx, dy float64) bool {
		if s.max <= 0 {
			return false
		}
		old := s.st.ScrollY
		s.st.ScrollY = math.Min(math.Max(s.st.ScrollY-dy*scrollStep, 0), s.max)
		return s.st.ScrollY != old
	}})
	s.track = Rect{r.Right() - scrollBarW, r.Y, scrollBarW, r.H}
	if s.max > 0 {
		u.Register(Interactive{ID: s.ID + "/bar", R: s.track, Cursor: CursorPointer,
			OnPress: func() { s.dragTo(u.ptrY) },
			OnDrag:  func(x, y float64) { s.dragTo(y) },
		})
	}
	u.PushClip(r)
	s.Child.Place(u, Rect{r.X, r.Y - s.st.ScrollY, cw, s.contentH})
	u.PopClip()
}

// thumb is the draggable part of the bar.
func (s *Scroll) thumb() Rect {
	h := math.Max(s.r.H*s.r.H/s.contentH, minThumbSize)
	h = math.Min(h, s.r.H)
	y := s.r.Y
	if s.max > 0 {
		y += (s.r.H - h) * s.st.ScrollY / s.max
	}
	return Rect{s.track.X + 3, y, 4, h}
}

func (s *Scroll) dragTo(y float64) {
	t := s.thumb()
	span := s.r.H - t.H
	if span <= 0 {
		return
	}
	frac := (y - s.r.Y - t.H/2) / span
	s.st.ScrollY = math.Min(math.Max(frac*s.max, 0), s.max)
}

func (s *Scroll) Paint(u *UI, cv Canvas) {
	cv.PushClip(s.r)
	s.Child.Paint(u, cv)
	cv.PopClip()
	if s.max > 0 {
		c := theme.WithAlpha(u.Pal.TextDim, 120)
		if u.IsHot(s.ID+"/bar") || u.IsActive(s.ID+"/bar") {
			c = theme.WithAlpha(u.Pal.TextDim, 220)
		}
		cv.RoundRect(s.thumb(), 2, c)
	}
}

// ScrollTo scrolls the node with this ID to y (clamped when it is next placed).
func (u *UI) ScrollTo(id ID, y float64) {
	u.State(id).ScrollY = y
	u.changed = true
}
