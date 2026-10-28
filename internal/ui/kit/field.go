package kit

import (
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// TextField is a single-line text input. Its text lives in *Value, which the screen owns;
// the caret and the selection (rune offsets) live in the UI's per-ID state.
//
// Typed or committed text arrives through the UI (Input.Text) and is inserted at the
// caret, replacing the selection. The input method's unfinished text (Input.Composing)
// is drawn at the caret with an underline and is not part of Value until it is
// committed. A masked field (a password) shows bullets, ignores the input method, and
// refuses to copy.
type TextField struct {
	ID          ID
	Value       *string
	Placeholder string
	Mask        bool
	MaxLen      int // in characters; default 256
	OnChange    func()
	OnSubmit    func()
	Leading     Icon // an icon inside the left edge (a magnifier for a search box)
	NoIME       bool // never enable the input method (pairing codes)
	Mono        bool // draw digits at equal spacing (codes)

	st    text.Style
	r     Rect
	inner Rect
	disp  string
}

const fieldPad = 10.0

func (f *TextField) maxLen() int {
	if f.MaxLen > 0 {
		return f.MaxLen
	}
	return 256
}

func (f *TextField) Measure(u *UI, maxW float64) Size {
	if math.IsInf(maxW, 1) {
		maxW = 240
	}
	return Size{maxW, theme.ControlH}
}

// display is what is shown for the value: bullets for a mask.
func (f *TextField) display() string {
	if f.Mask {
		return strings.Repeat("•", utf8.RuneCountInString(*f.Value))
	}
	return *f.Value
}

// advance is the width of the first n runes of s.
func (f *TextField) advance(u *UI, s string, n int) float64 {
	if n <= 0 {
		return 0
	}
	rs := []rune(s)
	if n > len(rs) {
		n = len(rs)
	}
	return u.M.Width(string(rs[:n]), f.st)
}

// caretAt is the rune offset whose position is nearest x (relative to the text origin).
func (f *TextField) caretAt(u *UI, s string, x float64) int {
	rs := []rune(s)
	best, bestD := 0, math.Inf(1)
	for i := 0; i <= len(rs); i++ {
		if d := math.Abs(f.advance(u, s, i) - x); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func (f *TextField) Place(u *UI, r Rect) {
	f.r = r
	f.st = u.Style(theme.SizeBody, text.Regular)
	left := fieldPad
	if f.Leading != IconNone {
		left += 22
	}
	f.inner = Rect{r.X + left, r.Y, math.Max(r.W-left-fieldPad, 0), r.H}
	f.disp = f.display()
	n := utf8.RuneCountInString(f.disp)
	st := u.State(f.ID)
	st.Caret = min(max(st.Caret, 0), n)
	st.Anchor = min(max(st.Anchor, 0), n)

	// Keep the caret in view.
	cx := f.advance(u, f.disp, st.Caret)
	if cx-st.ScrollX > f.inner.W-2 {
		st.ScrollX = cx - f.inner.W + 2
	}
	if cx-st.ScrollX < 0 {
		st.ScrollX = cx
	}
	if total := u.M.Width(f.disp, f.st); total <= f.inner.W {
		st.ScrollX = 0
	}

	u.Register(Interactive{
		ID: f.ID, R: r, Focusable: true, Cursor: CursorText,
		OnPress: func() {
			x := u.ptrX - f.inner.X + st.ScrollX
			st.Caret = f.caretAt(u, f.disp, x)
			st.Anchor = st.Caret
		},
		OnDrag: func(x, y float64) {
			st.Caret = f.caretAt(u, f.disp, x-f.inner.X+st.ScrollX)
		},
		OnKey:  func(k KeyPress) bool { return f.key(u, st, k) },
		OnText: func(s string) { f.insert(u, st, s) },
	})
}

func (f *TextField) selection(st *WState) (lo, hi int) {
	lo, hi = st.Anchor, st.Caret
	if lo > hi {
		lo, hi = hi, lo
	}
	return
}

// insert puts s at the caret, replacing the selection, within the length limit and
// without control characters.
func (f *TextField) insert(u *UI, st *WState, s string) {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			if r == '\t' || r == '\n' || r == '\r' {
				continue
			}
			continue
		}
		b.WriteRune(r)
	}
	ins := b.String()
	if ins == "" {
		return
	}
	rs := []rune(*f.Value)
	lo, hi := f.selection(st)
	room := f.maxLen() - (len(rs) - (hi - lo))
	if room <= 0 {
		return
	}
	if ir := []rune(ins); len(ir) > room {
		ins = string(ir[:room])
	}
	f.set(st, string(rs[:lo])+ins+string(rs[hi:]), lo+utf8.RuneCountInString(ins))
}

func (f *TextField) set(st *WState, v string, caret int) {
	changed := v != *f.Value
	*f.Value = v
	st.Caret, st.Anchor = caret, caret
	if changed && f.OnChange != nil {
		f.OnChange()
	}
}

func (f *TextField) key(u *UI, st *WState, k KeyPress) bool {
	rs := []rune(*f.Value)
	n := len(rs)
	lo, hi := f.selection(st)
	move := func(to int) {
		to = min(max(to, 0), n)
		st.Caret = to
		if !k.Shift {
			st.Anchor = to
		}
	}
	switch {
	case k.Ctrl && k.Key == KeyA:
		st.Anchor, st.Caret = 0, n
	case k.Ctrl && (k.Key == KeyC || k.Key == KeyX):
		if f.Mask || lo == hi || u.Clip == nil {
			return true
		}
		u.Clip.Set(string(rs[lo:hi]))
		if k.Key == KeyX {
			f.set(st, string(rs[:lo])+string(rs[hi:]), lo)
		}
	case k.Ctrl && k.Key == KeyV:
		if u.Clip != nil {
			f.insert(u, st, u.Clip.Get())
		}
	case k.Key == KeyBackspace:
		switch {
		case lo != hi:
			f.set(st, string(rs[:lo])+string(rs[hi:]), lo)
		case st.Caret > 0:
			f.set(st, string(rs[:st.Caret-1])+string(rs[st.Caret:]), st.Caret-1)
		}
	case k.Key == KeyDelete:
		switch {
		case lo != hi:
			f.set(st, string(rs[:lo])+string(rs[hi:]), lo)
		case st.Caret < n:
			f.set(st, string(rs[:st.Caret])+string(rs[st.Caret+1:]), st.Caret)
		}
	case k.Key == KeyLeft:
		if lo != hi && !k.Shift {
			move(lo)
		} else {
			move(st.Caret - 1)
		}
	case k.Key == KeyRight:
		if lo != hi && !k.Shift {
			move(hi)
		} else {
			move(st.Caret + 1)
		}
	case k.Key == KeyHome:
		move(0)
	case k.Key == KeyEnd:
		move(n)
	case k.Key == KeyEnter:
		if f.OnSubmit != nil {
			f.OnSubmit()
		}
	default:
		return false // Tab, Escape, and everything else belong to the UI
	}
	return true
}

func (f *TextField) Paint(u *UI, cv Canvas) {
	p := u.Pal
	focus := u.IsFocus(f.ID)
	cv.RoundRect(f.r, theme.RadiusSmall, p.Surface2)
	border := p.Border
	bw := 1.0
	if focus {
		border, bw = p.Accent, 2
	} else if u.IsHot(f.ID) {
		border = theme.Mix(p.Border, p.Text, 0.25)
	}
	cv.StrokeRoundRect(f.r, theme.RadiusSmall, bw, border)
	if f.Leading != IconNone {
		cv.Icon(f.Leading, Rect{f.r.X + fieldPad, f.r.Y + (f.r.H-16)/2, 16, 16}, p.TextDim)
	}
	st := u.State(f.ID)
	lh := u.LineH(f.st)
	ty := f.r.Y + (f.r.H-lh)/2 + (lh-u.M.LineHeight(f.st))/2
	cv.PushClip(f.inner)
	defer cv.PopClip()

	if f.disp == "" && !(focus && u.Composing() != "") {
		if f.Placeholder != "" {
			cv.Text(text.Truncate(u.M, f.Placeholder, f.st, f.inner.W), f.inner.X, ty, f.st, theme.WithAlpha(p.TextDim, 200))
		}
	}
	x0 := f.inner.X - st.ScrollX
	rs := []rune(f.disp)
	if focus {
		lo, hi := f.selection(st)
		if lo != hi {
			ax, bx := f.advance(u, f.disp, lo), f.advance(u, f.disp, hi)
			cv.FillRect(Rect{x0 + ax, f.r.Y + 6, bx - ax, f.r.H - 12}, theme.WithAlpha(p.Accent, 110))
		}
	}
	comp := ""
	if focus && !f.Mask && !f.NoIME {
		comp = u.Composing()
	}
	if comp == "" {
		if len(rs) > 0 {
			cv.Text(f.disp, x0, ty, f.st, p.Text)
		}
	} else {
		// The unfinished text is drawn between the two halves, underlined.
		before, after := string(rs[:st.Caret]), string(rs[st.Caret:])
		bw := u.M.Width(before, f.st)
		cw := u.M.Width(comp, f.st)
		cv.Text(before, x0, ty, f.st, p.Text)
		cv.Text(comp, x0+bw, ty, f.st, p.Text)
		cv.FillRect(Rect{x0 + bw, ty + lh - 3, cw, 1}, p.Text)
		cv.Text(after, x0+bw+cw, ty, f.st, p.Text)
	}
	if focus {
		// A caret that blinks twice a second; it stays solid while typing is going on.
		cx := x0 + f.advance(u, f.disp, st.Caret)
		if comp != "" {
			cx += u.M.Width(comp, f.st)
		}
		on := (u.Now().UnixMilli()/500)%2 == 0
		if on {
			cv.FillRect(Rect{cx, f.r.Y + 7, 1.5, f.r.H - 14}, p.Text)
		}
		next := u.Now().Truncate(500 * time.Millisecond).Add(500 * time.Millisecond)
		u.WakeAt(next)
		if !f.Mask && !f.NoIME {
			u.imeRect, u.imeOK = Rect{cx, f.r.Y + 4, 2, f.r.H - 8}, true
		}
	}
}
