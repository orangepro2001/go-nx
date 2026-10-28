package kit

import (
	"math"
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// Node is one element of the tree.
type Node interface {
	// Measure is the size the node wants when it may be at most maxW wide (maxW is Inf
	// when the parent does not limit it).
	Measure(u *UI, maxW float64) Size
	// Place is called once the node has its rectangle; containers place their children
	// and interactive nodes register themselves with u.Register.
	Place(u *UI, r Rect)
	// Paint draws the node. Everything it needs it decided in Place.
	Paint(u *UI, cv Canvas)
}

// WState is what a node keeps between rebuilds of the tree, found by its ID.
type WState struct {
	ScrollY, ScrollX float64 // scrolling lists and long text fields
	Caret, Anchor    int     // text field: caret and selection anchor, as byte offsets
	Max              float64 // scroll: the largest scroll offset seen when last placed
}

type animKey struct {
	id  ID
	key string
}

type anim struct {
	from, to, cur, start, dur float64
}

// UI holds a tree and what outlives it: focus, hover, per-node state, animations. Create
// one with NewUI; the zero value is not usable.
type UI struct {
	Pal   theme.Palette
	Tr    *i18n.Translator
	M     text.Measurer
	Clip  Clipboard
	Scale float64 // device pixels per logical pixel; the Canvas applies it

	// ReduceMotion makes every animation jump to its end (the system setting).
	ReduceMotion bool

	root Node
	size Size
	now  time.Time

	hits    []Interactive
	order   []ID
	clips   []Rect
	escapes []func()

	ptrX, ptrY float64
	ptrIn      bool
	hot        ID
	active     ID
	focus      ID
	kbd        bool // focus came from the keyboard, so the focus ring shows
	winFocused bool
	composing  string
	cursor     Cursor

	state   map[ID]*WState
	anims   map[animKey]*anim
	changed bool
	animing bool
	wake    time.Time
	imeRect Rect // where the caret of the focused text field is, for the input method's window
	imeOK   bool // a focused field wants the input method
}

// NewUI makes a UI that measures with m and speaks in tr's language.
func NewUI(pal theme.Palette, tr *i18n.Translator, m text.Measurer) *UI {
	return &UI{Pal: pal, Tr: tr, M: m, Scale: 1, state: map[ID]*WState{}, anims: map[animKey]*anim{}, winFocused: true}
}

// SetSize sets the logical size of the surface; the tree is laid out again.
func (u *UI) SetSize(w, h float64) {
	if u.size.W != w || u.size.H != h {
		u.size = Size{w, h}
		u.changed = true
	}
}

// Size is the logical size of the surface.
func (u *UI) Size() Size { return u.size }

// Invalidate says the tree is out of date: something outside the UI changed.
func (u *UI) Invalidate() { u.changed = true }

// Dirty reports whether the tree should be rebuilt before the next Paint.
func (u *UI) Dirty() bool { return u.changed || u.root == nil }

// SetRoot installs a freshly built tree and lays it out.
func (u *UI) SetRoot(n Node) {
	u.root = n
	u.hits = u.hits[:0]
	u.order = u.order[:0]
	u.escapes = u.escapes[:0]
	u.clips = append(u.clips[:0], Rect{0, 0, u.size.W, u.size.H})
	n.Measure(u, u.size.W)
	n.Place(u, Rect{0, 0, u.size.W, u.size.H})
	u.changed = false
	if u.focus != "" && !u.hasID(u.focus) {
		u.focus = ""
	}
	u.updateHot()
}

// HasFocus reports whether the node that has the keyboard is reachable now.
func (u *UI) HasFocus() bool { return u.focus != "" && u.hasID(u.focus) }

func (u *UI) hasID(id ID) bool {
	for i := range u.hits {
		if u.hits[i].ID == id {
			return true
		}
	}
	return false
}

// Register is called by nodes in Place to receive input. The rectangle is clipped to
// what is visible; a node entirely out of view receives nothing.
func (u *UI) Register(i Interactive) {
	if i.ID == "" {
		return
	}
	if n := len(u.clips); n > 0 {
		i.R = i.R.Intersect(u.clips[n-1])
	}
	if i.R.Empty() {
		return
	}
	u.hits = append(u.hits, i)
	if i.Focusable && !i.Disabled {
		u.order = append(u.order, i.ID)
	}
}

// PushClip limits the part of a node that can be hit to r, until PopClip. A node that
// scrolls its children does this around placing them.
func (u *UI) PushClip(r Rect) {
	top := Rect{0, 0, u.size.W, u.size.H}
	if n := len(u.clips); n > 0 {
		top = u.clips[n-1]
	}
	u.clips = append(u.clips, top.Intersect(r))
}

// PopClip ends PushClip.
func (u *UI) PopClip() { u.clips = u.clips[:len(u.clips)-1] }

// BlockBelow makes everything placed so far unreachable: a modal layer calls it before
// placing itself, so the pointer and the keyboard only find the layer's own nodes. Focus
// that was on a node below is dropped when the tree is done (it is no longer reachable),
// so the layer should give it to one of its own with HasFocus and Focus.
func (u *UI) BlockBelow() {
	u.hits = u.hits[:0]
	u.order = u.order[:0]
	u.escapes = u.escapes[:0]
}

// OnEscape registers what the Escape key does when nothing focused uses it. The one
// registered last (the topmost layer) wins.
func (u *UI) OnEscape(f func()) { u.escapes = append(u.escapes, f) }

// State is the persistent state of the node with this ID.
func (u *UI) State(id ID) *WState {
	s := u.state[id]
	if s == nil {
		s = &WState{}
		u.state[id] = s
	}
	return s
}

// Now is the time of the frame being handled or painted.
func (u *UI) Now() time.Time { return u.now }

// Hot is the ID of the node under the pointer, "" for none.
func (u *UI) Hot() ID { return u.hot }

func (u *UI) IsHot(id ID) bool    { return u.hot == id && id != "" }
func (u *UI) IsActive(id ID) bool { return u.active == id && id != "" }
func (u *UI) IsFocus(id ID) bool  { return u.focus == id && id != "" && u.winFocused }

// ShowFocusRing reports whether id has focus that came from the keyboard.
func (u *UI) ShowFocusRing(id ID) bool { return u.IsFocus(id) && u.kbd }

// Focus gives the keyboard to id (it must be registered by the next SetRoot to stay).
func (u *UI) Focus(id ID, fromKeyboard bool) {
	if u.focus == id {
		if u.kbd != fromKeyboard {
			u.kbd = fromKeyboard
			u.changed = true
		}
		return
	}
	u.blur()
	u.focus = id
	u.kbd = fromKeyboard
	u.changed = true
	if h := u.find(id); h != nil && h.OnFocus != nil {
		h.OnFocus()
	}
}

// Focused is the ID that has the keyboard, "" for none.
func (u *UI) Focused() ID { return u.focus }

// Composing is the input method's unfinished text for the focused node.
func (u *UI) Composing() string { return u.composing }

// CurrentCursor is the shape the pointer should have where it is.
func (u *UI) CurrentCursor() Cursor { return u.cursor }

func (u *UI) blur() {
	if h := u.find(u.focus); h != nil && h.OnBlur != nil {
		h.OnBlur()
	}
}

func (u *UI) find(id ID) *Interactive {
	if id == "" {
		return nil
	}
	for i := len(u.hits) - 1; i >= 0; i-- {
		if u.hits[i].ID == id {
			return &u.hits[i]
		}
	}
	return nil
}

func (u *UI) updateHot() {
	old, cur := u.hot, u.cursor
	u.hot, u.cursor = "", CursorDefault
	if u.ptrIn {
		for i := len(u.hits) - 1; i >= 0; i-- {
			h := &u.hits[i]
			if !h.Disabled && h.R.Contains(u.ptrX, u.ptrY) {
				u.hot, u.cursor = h.ID, h.Cursor
				break
			}
		}
	}
	if u.hot != old || u.cursor != cur {
		u.changed = true
	}
}

// Handle gives the UI one frame of input and reports whether anything it shows may have
// changed. Callbacks of the nodes run from here.
func (u *UI) Handle(in Input) bool {
	before := u.changed
	u.now = in.Now
	u.composing = in.Composing
	if in.Focused != u.winFocused {
		u.winFocused = in.Focused
		u.changed = true
	}
	u.ptrX, u.ptrY, u.ptrIn = in.X, in.Y, in.Pointer
	u.updateHot()

	if in.Pressed[Left] {
		u.active = u.hot
		if u.hot != "" {
			h := u.find(u.hot)
			if h != nil && h.Focusable {
				u.Focus(u.hot, false)
			} else if h != nil && u.focus != "" {
				u.blur()
				u.focus = ""
			}
			if h != nil && h.OnPress != nil {
				h.OnPress()
			}
		} else if u.focus != "" {
			u.blur()
			u.focus = ""
		}
		u.changed = true
	}
	if in.Down[Left] && u.active != "" {
		if h := u.find(u.active); h != nil && h.OnDrag != nil && in.Pointer {
			h.OnDrag(in.X, in.Y)
			u.changed = true
		}
	}
	if in.Released[Left] {
		if u.active != "" && u.active == u.hot {
			if h := u.find(u.active); h != nil && h.OnClick != nil && !h.Disabled {
				h.OnClick()
			}
		}
		u.active = ""
		u.changed = true
	}
	if (in.WheelX != 0 || in.WheelY != 0) && in.Pointer {
		for i := len(u.hits) - 1; i >= 0; i-- {
			h := &u.hits[i]
			if h.OnWheel != nil && !h.Disabled && h.R.Contains(in.X, in.Y) && h.OnWheel(in.WheelX, in.WheelY) {
				u.changed = true
				break
			}
		}
	}
	for _, k := range in.Keys {
		u.key(k)
	}
	if in.Text != "" {
		if h := u.find(u.focus); h != nil && h.OnText != nil {
			h.OnText(in.Text)
			u.changed = true
		}
	}
	return u.changed && !before || u.changed
}

func (u *UI) key(k KeyPress) {
	u.changed = true
	if h := u.find(u.focus); h != nil && h.OnKey != nil && h.OnKey(k) {
		return
	}
	switch k.Key {
	case KeyTab:
		d := 1
		if k.Shift {
			d = -1
		}
		u.moveFocus(d)
	case KeyEnter, KeySpace:
		if h := u.find(u.focus); h != nil && h.OnClick != nil && !h.Disabled && !k.Ctrl && !k.Alt {
			h.OnClick()
		}
	case KeyEscape:
		if n := len(u.escapes); n > 0 {
			u.escapes[n-1]()
		}
	}
}

func (u *UI) moveFocus(d int) {
	n := len(u.order)
	if n == 0 {
		return
	}
	i := -1
	for j, id := range u.order {
		if id == u.focus {
			i = j
		}
	}
	switch {
	case i < 0 && d > 0:
		i = 0
	case i < 0:
		i = n - 1
	default:
		i = (i + d + n) % n
	}
	u.Focus(u.order[i], true)
}

// Paint draws the tree.
func (u *UI) Paint(cv Canvas) {
	u.animing = false
	u.wake = time.Time{}
	u.imeOK = false
	if u.root != nil {
		u.root.Paint(u, cv)
	}
}

// IME reports whether the focused node wants the input method, and where its caret is in
// logical pixels so that the candidate window can follow it. Valid after Paint.
func (u *UI) IME() (caret Rect, wanted bool) { return u.imeRect, u.imeOK }

// WantsFrame reports whether another frame is needed at once (an animation is running),
// and otherwise the time of the next change that happens without input (a blinking
// caret), or the zero time for none.
func (u *UI) WantsFrame() (now bool, at time.Time) { return u.animing, u.wake }

// WakeAt asks for a frame at t; the earliest request wins.
func (u *UI) WakeAt(t time.Time) {
	if u.wake.IsZero() || t.Before(u.wake) {
		u.wake = t
	}
}

// Anim eases a value toward target over dur milliseconds and returns its current value.
// The first time an animation is asked for it starts at the target, so nothing fades in
// just because it appeared. While a value is on its way, the UI asks for frames.
func (u *UI) Anim(id ID, key string, target, dur float64) float64 {
	k := animKey{id, key}
	a := u.anims[k]
	if a == nil || u.ReduceMotion || dur <= 0 {
		u.anims[k] = &anim{cur: target, to: target}
		return target
	}
	t := float64(u.now.UnixNano()) / 1e6
	if a.to != target {
		a.from, a.to, a.start, a.dur = a.cur, target, t, dur
	}
	if p := (t - a.start) / a.dur; a.cur != a.to && p < 1 {
		a.cur = a.from + (a.to-a.from)*easeOutCubic(math.Max(p, 0))
		u.animing = true
	} else {
		a.cur = a.to
	}
	return a.cur
}

func easeOutCubic(t float64) float64 { return 1 - math.Pow(1-t, 3) }

// Style is a text style whose size respects the language: CJK text is never below the
// legible minimum.
func (u *UI) Style(size float64, w text.Weight) text.Style {
	if u.Tr != nil && (u.Tr.Lang() == i18n.Chinese || u.Tr.Lang() == i18n.Korean) {
		size = math.Max(size, theme.MinCJKSize)
	}
	return text.Style{Size: size, Weight: w}
}

// LineH is the height of one line of text in st: 1.4 times the size for Latin text, 1.5
// for Chinese and Korean.
func (u *UI) LineH(st text.Style) float64 {
	if u.Tr != nil && (u.Tr.Lang() == i18n.Chinese || u.Tr.Lang() == i18n.Korean) {
		return math.Round(st.Size * theme.LineCJK)
	}
	return math.Round(st.Size * theme.LineLatin)
}

// ClipRect is the part of the surface that can be seen at this point of placing the tree:
// inside a Scroll, the part of its window. A node that has far more to draw than fits
// (a log with thousands of lines) uses it to prepare only what is visible.
func (u *UI) ClipRect() Rect {
	if n := len(u.clips); n > 0 {
		return u.clips[n-1]
	}
	return Rect{0, 0, u.size.W, u.size.H}
}
