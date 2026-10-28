package kit

import (
	"strings"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// harness drives a UI the way the program does: input in, tree rebuilt when the UI says
// so, paint into a recorder.
type harness struct {
	t     *testing.T
	u     *UI
	build func(u *UI) Node
	now   time.Time
	x, y  float64
	in    bool
	down  bool
}

func newHarness(t *testing.T, w, h float64, build func(u *UI) Node) *harness {
	t.Helper()
	u := NewUI(theme.DarkPalette(), i18n.New(i18n.English), text.Fake{})
	u.SetSize(w, h)
	hn := &harness{t: t, u: u, build: build, now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	hn.step(Input{Focused: true})
	return hn
}

// step handles one frame of input and rebuilds the tree if the UI asks.
func (h *harness) step(in Input) {
	h.t.Helper()
	h.now = h.now.Add(20 * time.Millisecond)
	in.Now = h.now
	in.X, in.Y, in.Pointer = h.x, h.y, h.in
	in.Down[Left] = h.down
	if in.Focused == false {
		in.Focused = true
	}
	h.u.Handle(in)
	for i := 0; i < 3 && (h.u.Dirty() || i == 0); i++ {
		h.u.SetRoot(h.build(h.u))
	}
}

func (h *harness) move(x, y float64) {
	h.x, h.y, h.in = x, y, true
	h.step(Input{})
}

func (h *harness) press() {
	h.down = true
	in := Input{}
	in.Pressed[Left] = true
	h.step(in)
}

func (h *harness) release() {
	h.down = false
	in := Input{}
	in.Released[Left] = true
	h.step(in)
}

func (h *harness) click(x, y float64) {
	h.move(x, y)
	h.press()
	h.release()
}

func (h *harness) key(keys ...KeyPress) { h.step(Input{Keys: keys}) }
func (h *harness) typ(s string)         { h.step(Input{Text: s}) }
func (h *harness) wheel(dy float64)     { h.step(Input{WheelY: dy}) }

func (h *harness) paint() *Recorder {
	rec := &Recorder{}
	h.u.Paint(rec)
	return rec
}

func k(key Key) KeyPress { return KeyPress{Key: key} }

type memClip struct{ s string }

func (c *memClip) Get() string  { return c.s }
func (c *memClip) Set(s string) { c.s = s }

func TestRowSharesTheRoomAmongGrowingChildren(t *testing.T) {
	var a, b, c *Label
	h := newHarness(t, 300, 100, func(u *UI) Node {
		a, b, c = &Label{Text: "aaaa"}, &Label{Text: "b"}, &Label{Text: "cc"}
		return &Row{Gap: 10, Children: []Node{a, Grow(1, b), Grow(3, c)}}
	})
	// "aaaa" is 4 narrow runes of 14px: 28 wide. 300 - 28 - 2*10 = 252 shared 1:3.
	if a.r.W != 28 || b.r.W != 63 || c.r.W != 189 {
		t.Errorf("widths %v %v %v, want 28 63 189", a.r.W, b.r.W, c.r.W)
	}
	if a.r.X != 0 || b.r.X != 38 || c.r.X != 111 {
		t.Errorf("x positions %v %v %v, want 0 38 111", a.r.X, b.r.X, c.r.X)
	}
	_ = h
}

func TestColStacksAndStretches(t *testing.T) {
	var top, mid, rest *Label
	newHarness(t, 200, 300, func(u *UI) Node {
		top, mid, rest = &Label{Text: "top"}, &Label{Text: "mid"}, &Label{Text: "rest"}
		return &Col{Gap: 4, Align: AlignStretch, Children: []Node{top, mid, Grow(1, rest)}}
	})
	lh := 20.0 // 14 * 1.4 = 19.6, rounded
	if top.r.Y != 0 || mid.r.Y != lh+4 {
		t.Errorf("y positions %v %v, want 0 and %v", top.r.Y, mid.r.Y, lh+4)
	}
	if top.r.W != 200 || mid.r.W != 200 {
		t.Errorf("stretched widths %v %v, want 200", top.r.W, mid.r.W)
	}
	if rest.r.Y != 2*lh+8 || rest.r.H != 300-(2*lh+8) {
		t.Errorf("the growing child is at %v with height %v", rest.r.Y, rest.r.H)
	}
}

func TestPadSizedAlignedAndStack(t *testing.T) {
	var l *Label
	newHarness(t, 400, 200, func(u *UI) Node {
		l = &Label{Text: "hi"}
		return Pad{In: All(10), Child: Aligned{H: AlignEnd, V: AlignCenter, Child: Sized{W: 100, H: 40, Child: l}}}
	})
	if l.r.W != 100 || l.r.H != 40 || l.r.X != 400-10-100 || l.r.Y != 10+(180-40)/2 {
		t.Errorf("label at %+v", l.r)
	}
	var back, front *Button
	clicks := ""
	h := newHarness(t, 200, 100, func(u *UI) Node {
		back = &Button{ID: "back", Label: "Back", OnClick: func() { clicks += "back" }}
		front = &Button{ID: "front", Label: "Front", OnClick: func() { clicks += "front" }}
		return Stack{Children: []Node{back, front}}
	})
	h.click(10, 10)
	if clicks != "front" {
		t.Errorf("a click on two stacked buttons went to %q: the later one is on top", clicks)
	}
}

func TestLabelWrapsAndTruncates(t *testing.T) {
	var l *Label
	newHarness(t, 60, 200, func(u *UI) Node {
		l = &Label{Text: "one two three four five six", Wrap: true}
		return l
	})
	if len(l.lines) < 3 {
		t.Fatalf("wrapped into %q", l.lines)
	}
	for _, line := range l.lines {
		if w := (text.Fake{}).Width(line, l.st); w > 60 {
			t.Errorf("line %q is %v wide", line, w)
		}
	}
	var one *Label
	newHarness(t, 50, 50, func(u *UI) Node {
		one = &Label{Text: "a long single line", Ellipsis: true}
		return one
	})
	if got := one.lines[0]; !strings.HasSuffix(got, "…") || (text.Fake{}).Width(got, one.st) > 50 {
		t.Errorf("truncated to %q", got)
	}
	var capped *Label
	newHarness(t, 100, 200, func(u *UI) Node {
		capped = &Label{Text: "one two three four five six seven eight nine ten", Wrap: true, MaxLines: 2}
		return capped
	})
	if len(capped.lines) != 2 || !strings.HasSuffix(capped.lines[1], "…") {
		t.Errorf("MaxLines gave %q", capped.lines)
	}
}

func TestChineseAndKoreanGetTallerLinesAndNoSmallType(t *testing.T) {
	u := NewUI(theme.DarkPalette(), i18n.New(i18n.English), text.Fake{})
	if got := u.Style(12, text.Regular).Size; got != 12 {
		t.Errorf("English caption is %v", got)
	}
	lat := u.LineH(u.Style(14, text.Regular))
	for _, l := range []i18n.Lang{i18n.Chinese, i18n.Korean} {
		u.Tr = i18n.New(l)
		if got := u.Style(12, text.Regular).Size; got != theme.MinCJKSize {
			t.Errorf("%v caption is %v, want at least %v", l, got, theme.MinCJKSize)
		}
		if got := u.LineH(u.Style(14, text.Regular)); got <= lat {
			t.Errorf("%v line height %v is not taller than Latin %v", l, got, lat)
		}
	}
}

func TestButtonClicksOnPressAndReleaseInside(t *testing.T) {
	clicks := 0
	h := newHarness(t, 300, 100, func(u *UI) Node {
		return Aligned{Child: &Button{ID: "go", Label: "Go", OnClick: func() { clicks++ }}}
	})
	h.click(20, 18)
	if clicks != 1 {
		t.Fatalf("a click gave %d clicks", clicks)
	}
	// Press inside, drag out, release outside: no click.
	h.move(20, 18)
	h.press()
	h.move(250, 90)
	h.release()
	if clicks != 1 {
		t.Errorf("a press that was released elsewhere clicked (%d)", clicks)
	}
	// Press outside, drag in, release inside: no click either.
	h.move(250, 90)
	h.press()
	h.move(20, 18)
	h.release()
	if clicks != 1 {
		t.Errorf("a press that began elsewhere clicked (%d)", clicks)
	}
}

func TestDisabledButtonIsDeadAndSkippedByTab(t *testing.T) {
	var log []string
	h := newHarness(t, 400, 100, func(u *UI) Node {
		return &Row{Gap: 8, Children: []Node{
			&Button{ID: "a", Label: "A", OnClick: func() { log = append(log, "a") }},
			&Button{ID: "b", Label: "B", Disabled: true, OnClick: func() { log = append(log, "b") }},
			&Button{ID: "c", Label: "C", OnClick: func() { log = append(log, "c") }},
		}}
	})
	h.click(60, 18) // on the disabled B
	if len(log) != 0 || h.u.Focused() != "" {
		t.Fatalf("a click on a disabled button did something: %v, focus %q", log, h.u.Focused())
	}
	h.key(k(KeyTab))
	if h.u.Focused() != "a" {
		t.Fatalf("first Tab focused %q", h.u.Focused())
	}
	h.key(k(KeyTab))
	if h.u.Focused() != "c" {
		t.Errorf("second Tab focused %q: the disabled button must be skipped", h.u.Focused())
	}
	h.key(k(KeyEnter))
	h.key(KeyPress{Key: KeyTab, Shift: true})
	h.key(k(KeySpace))
	if strings.Join(log, "") != "ca" {
		t.Errorf("activated %q, want c then a", log)
	}
}

func TestFocusRingShowsForTheKeyboardOnly(t *testing.T) {
	h := newHarness(t, 200, 100, func(u *UI) Node {
		return Aligned{Child: &Button{ID: "x", Label: "X"}}
	})
	h.click(15, 18)
	ring := func() bool {
		for _, o := range h.paint().Ops {
			if o.Kind == "stroke" && o.Width == theme.FocusRing {
				return true
			}
		}
		return false
	}
	if h.u.Focused() != "x" || ring() {
		t.Errorf("after a mouse click: focus %q, ring %v; the ring is for keyboard focus", h.u.Focused(), ring())
	}
	h.key(k(KeyTab)) // moves on (wraps to the same single button), now from the keyboard
	if !ring() {
		t.Error("no focus ring after Tab")
	}
	h.step(Input{Focused: true})
	// Clicking nothing drops the focus.
	h.click(190, 90)
	if h.u.Focused() != "" {
		t.Errorf("clicking on empty space left focus on %q", h.u.Focused())
	}
}

func TestHoverEasesButNotOnFirstAppearance(t *testing.T) {
	h := newHarness(t, 200, 100, func(u *UI) Node { return Aligned{Child: &Button{ID: "x", Label: "X"}} })
	fill := func() Color {
		for _, o := range h.paint().Ops {
			if o.Kind == "round" {
				return o.Color
			}
		}
		return Color{}
	}
	rest := fill()
	h.move(10, 10)
	if got := fill(); got != rest {
		t.Errorf("the fill jumped at the instant of hovering: %v, was %v (it eases in)", got, rest)
	}
	h.step(Input{})
	mid := fill()
	if mid == rest {
		t.Error("hovering did not change the fill")
	}
	for i := 0; i < 30; i++ {
		h.step(Input{})
		h.paint()
	}
	if end := fill(); end == mid || end == rest {
		t.Errorf("the hover fill did not settle on its own colour: rest %v mid %v end %v", rest, mid, end)
	}
	h.u.ReduceMotion = true
	h.move(190, 90)
	if got := fill(); got != rest {
		t.Errorf("with reduced motion the fill should return to rest at once, got %v want %v", got, rest)
	}
}

func TestTextFieldEditing(t *testing.T) {
	val := ""
	changes, submits := 0, 0
	clip := &memClip{}
	h := newHarness(t, 300, 60, func(u *UI) Node {
		u.Clip = clip
		return &TextField{ID: "f", Value: &val, MaxLen: 12, OnChange: func() { changes++ }, OnSubmit: func() { submits++ }}
	})
	h.click(50, 18)
	if h.u.Focused() != "f" {
		t.Fatal("clicking the field did not focus it")
	}
	h.typ("hello")
	h.typ(" wörld")
	if val != "hello wörld" {
		t.Errorf("typed text = %q", val)
	}
	h.typ("0123456789") // only one more character fits under MaxLen 12
	if val != "hello wörld0" {
		t.Errorf("MaxLen: %q", val)
	}
	h.key(k(KeyBackspace), k(KeyBackspace))
	if val != "hello wörl" {
		t.Errorf("after two backspaces %q", val)
	}
	h.key(k(KeyHome), k(KeyDelete))
	if val != "ello wörl" {
		t.Errorf("after Home, Delete %q", val)
	}
	// Shift+Right twice selects "el"; typing replaces the selection.
	h.key(KeyPress{Key: KeyRight, Shift: true}, KeyPress{Key: KeyRight, Shift: true})
	h.typ("E")
	if val != "Elo wörl" {
		t.Errorf("replacing a selection: %q", val)
	}
	// Select all, cut, paste back twice.
	h.key(KeyPress{Key: KeyA, Ctrl: true})
	h.key(KeyPress{Key: KeyX, Ctrl: true})
	if val != "" || clip.s != "Elo wörl" {
		t.Errorf("cut: value %q clipboard %q", val, clip.s)
	}
	h.key(KeyPress{Key: KeyV, Ctrl: true})
	h.key(KeyPress{Key: KeyV, Ctrl: true})
	if val != "Elo wörlElo " { // the second paste is cut at MaxLen 12
		t.Errorf("paste: %q", val)
	}
	// Control characters never get in.
	val = ""
	h.u.State("f").Caret, h.u.State("f").Anchor = 0, 0
	h.typ("a\tb\nc\x01d")
	if val != "abcd" {
		t.Errorf("control characters: %q", val)
	}
	h.key(k(KeyEnter))
	if submits != 1 || changes == 0 {
		t.Errorf("submits %d changes %d", submits, changes)
	}
}

func TestTextFieldCaretFollowsTheMouseAndIsKeptInRange(t *testing.T) {
	val := "abcdef"
	h := newHarness(t, 300, 60, func(u *UI) Node { return &TextField{ID: "f", Value: &val} })
	// Narrow runes are 7px wide (size 14); text starts at fieldPad.
	h.click(fieldPad+3*7+1, 18)
	if c := h.u.State("f").Caret; c != 3 {
		t.Errorf("clicked between the third and fourth character: caret %d", c)
	}
	h.click(290, 18)
	if c := h.u.State("f").Caret; c != 6 {
		t.Errorf("clicked past the end: caret %d", c)
	}
	val = "ab" // changed from outside; the caret must not point past the end
	h.u.Invalidate()
	h.step(Input{})
	if c := h.u.State("f").Caret; c != 2 {
		t.Errorf("caret %d after the value shrank", c)
	}
	// Dragging selects.
	val = "abcdef"
	h.u.Invalidate()
	h.step(Input{})
	h.move(fieldPad+1*7, 18)
	h.press()
	h.move(fieldPad+4*7, 18)
	h.release()
	st := h.u.State("f")
	if lo, hi := min(st.Anchor, st.Caret), max(st.Anchor, st.Caret); lo != 1 || hi != 4 {
		t.Errorf("dragged selection is %d..%d, want 1..4", lo, hi)
	}
}

func TestTextFieldMaskAndIME(t *testing.T) {
	val := "secret"
	clip := &memClip{s: "keep"}
	h := newHarness(t, 300, 60, func(u *UI) Node {
		u.Clip = clip
		return &TextField{ID: "pw", Value: &val, Mask: true}
	})
	h.click(50, 18)
	rec := h.paint()
	if _, shown := rec.Text1("secret"); shown {
		t.Error("a masked field drew its text")
	}
	if _, ok := rec.Text1("••••••"); !ok {
		t.Errorf("no bullets drawn: %v", rec.Texts())
	}
	h.key(KeyPress{Key: KeyA, Ctrl: true}, KeyPress{Key: KeyC, Ctrl: true})
	if clip.s != "keep" {
		t.Errorf("a masked field copied %q", clip.s)
	}
	if _, wanted := h.u.IME(); wanted {
		t.Error("a masked field asked for the input method")
	}

	word := ""
	h2 := newHarness(t, 300, 60, func(u *UI) Node { return &TextField{ID: "n", Value: &word} })
	h2.click(50, 18)
	h2.step(Input{Composing: "한글"})
	rec2 := h2.paint()
	if _, ok := rec2.Text1("한글"); !ok {
		t.Errorf("the unfinished text is not drawn: %v", rec2.Texts())
	}
	if word != "" {
		t.Error("unfinished text went into the value")
	}
	if caret, wanted := h2.u.IME(); !wanted || caret.Empty() {
		t.Errorf("IME() = %+v, %v: the input method needs the caret", caret, wanted)
	}
	h2.step(Input{Text: "한글"}) // committed
	if word != "한글" {
		t.Errorf("committed text = %q", word)
	}
}

func TestSegmentedArrowsAndClicks(t *testing.T) {
	sel := 0
	h := newHarness(t, 400, 60, func(u *UI) Node {
		return &Segmented{ID: "mode", Options: []string{"Strict", "Open", "Other"}, Selected: sel, Disabled: []bool{false, false, true},
			OnChange: func(i int) { sel = i }}
	})
	h.key(k(KeyTab))
	if h.u.Focused() != "mode/a" {
		t.Fatalf("Tab reached %q: only the chosen option is in the Tab order", h.u.Focused())
	}
	h.key(k(KeyRight))
	if sel != 1 || h.u.Focused() != "mode/b" {
		t.Errorf("Right: selected %d focus %q", sel, h.u.Focused())
	}
	h.key(k(KeyRight)) // the next one is disabled
	if sel != 1 {
		t.Errorf("moved onto a disabled option (%d)", sel)
	}
	h.key(k(KeyLeft))
	if sel != 0 {
		t.Errorf("Left: selected %d", sel)
	}
	h.click(110, 18)
	if sel != 1 {
		t.Errorf("clicking the second option selected %d", sel)
	}
}

func TestScrollWheelBarAndClipping(t *testing.T) {
	clicked := ""
	var rows []Node
	h := newHarness(t, 200, 100, func(u *UI) Node {
		rows = rows[:0]
		for i := 0; i < 10; i++ {
			name := string(rune('a' + i))
			rows = append(rows, Sized{H: 40, Child: &Button{ID: ID(name), Label: name, OnClick: func() { clicked += name }}})
		}
		return &Scroll{ID: "list", Child: &Col{Children: append([]Node(nil), rows...)}}
	})
	h.click(20, 20)
	if clicked != "a" {
		t.Fatalf("first row click: %q", clicked)
	}
	// Row "c" is at y 80..120 and only partly visible; its hidden part must not take clicks,
	// and row "e" (y 160) is not reachable at all.
	h.click(20, 99)
	h.click(20, 150) // beyond the window: the pointer is outside it
	if strings.Contains(clicked, "e") {
		t.Errorf("a row outside the viewport was clicked: %q", clicked)
	}
	h.move(20, 50)
	h.wheel(-1) // down one step
	if y := h.u.State("list").ScrollY; y != scrollStep {
		t.Errorf("scrolled to %v, want %v", y, scrollStep)
	}
	h.wheel(-100)
	if y := h.u.State("list").ScrollY; y != 300 {
		t.Errorf("scrolled past the end: %v, want the maximum 300", y)
	}
	h.wheel(100)
	if y := h.u.State("list").ScrollY; y != 0 {
		t.Errorf("scrolled past the start: %v", y)
	}
	// Dragging the bar.
	h.move(195, 10)
	h.press()
	h.move(195, 95)
	h.release()
	if y := h.u.State("list").ScrollY; y < 250 {
		t.Errorf("dragging the bar to the bottom scrolled only to %v", y)
	}
	// No overflow, no scrolling and no wheel use.
	h2 := newHarness(t, 200, 500, func(u *UI) Node {
		return &Scroll{ID: "short", Child: &Col{Children: []Node{&Label{Text: "x"}}}}
	})
	h2.move(10, 10)
	h2.wheel(-3)
	if y := h2.u.State("short").ScrollY; y != 0 {
		t.Errorf("a list that fits scrolled to %v", y)
	}
}

func TestDialogBlocksWhatIsBelowAndEscapeCancels(t *testing.T) {
	under, ok, cancel := 0, 0, 0
	open := true
	h := newHarness(t, 600, 400, func(u *UI) Node {
		kids := []Node{Aligned{Child: &Button{ID: "under", Label: "Under", OnClick: func() { under++ }}}}
		if open {
			kids = append(kids, &Dialog{
				ID: "dlg", Title: "Sure?", Body: Para("This cannot be undone."),
				Actions: []Node{
					&Button{ID: "cancel", Label: "Cancel", OnClick: func() { cancel++; open = false }},
					&Button{ID: "ok", Label: "OK", Kind: Primary, OnClick: func() { ok++; open = false }},
				},
				OnCancel: func() { cancel++; open = false }, DefaultFocus: "ok",
			})
		}
		return Stack{Children: kids}
	})
	if h.u.Focused() != "ok" {
		t.Errorf("the dialog's default focus is %q", h.u.Focused())
	}
	h.click(20, 18) // where the button under the dialog is
	if under != 0 {
		t.Error("a button below the dialog was clicked")
	}
	h.key(k(KeyTab))
	if h.u.Focused() != "cancel" {
		t.Errorf("Tab inside the dialog went to %q (wraps within the dialog)", h.u.Focused())
	}
	h.key(k(KeyEscape))
	if cancel != 1 || open {
		t.Errorf("Escape: cancel %d open %v", cancel, open)
	}
	h.click(20, 18)
	if under != 1 {
		t.Error("the button is not reachable again once the dialog is gone")
	}
	// Enter on the default button confirms.
	open = true
	h.u.Invalidate()
	h.step(Input{})
	h.key(k(KeyEnter))
	if ok != 1 {
		t.Errorf("Enter on the default button: ok %d", ok)
	}
}

func TestToastsStackAtTheBottomRightWithAnAction(t *testing.T) {
	acted := 0
	h := newHarness(t, 800, 600, func(u *UI) Node {
		return &Toasts{Items: []ToastItem{
			{ID: "t1", Text: "Saved", Tone: ToneGood},
			{ID: "t2", Text: "Ended 2 sessions", Tone: ToneWarn, Action: "Undo", OnAction: func() { acted++ }},
		}}
	})
	rec := h.paint()
	a, ok1 := rec.Text1("Saved")
	b, ok2 := rec.Text1("Ended 2 sessions")
	if !ok1 || !ok2 {
		t.Fatalf("toasts not drawn: %v", rec.Texts())
	}
	if a.R.X < 400 || a.R.Y < 400 {
		t.Errorf("the first toast is at %v,%v, not bottom right", a.R.X, a.R.Y)
	}
	if b.R.Y >= a.R.Y {
		t.Errorf("the second toast (%v) is not above the first (%v)", b.R.Y, a.R.Y)
	}
	undo, _ := rec.Text1("Undo")
	h.click(undo.R.X+5, undo.R.Y+5)
	if acted != 1 {
		t.Errorf("the action ran %d times", acted)
	}
}

func TestDumpIsStable(t *testing.T) {
	build := func(u *UI) Node {
		return Pad{In: All(16), Child: &Col{Gap: 8, Children: []Node{
			&Label{Text: "Title", Size: 20, Weight: text.Semibold},
			&Row{Gap: 8, Children: []Node{&Button{ID: "a", Label: "Cancel"}, &Button{ID: "b", Label: "OK", Kind: Primary}}},
		}}}
	}
	h1 := newHarness(t, 300, 200, build)
	h2 := newHarness(t, 300, 200, build)
	d1, d2 := h1.paint().Dump(), h2.paint().Dump()
	if d1 != d2 || d1 == "" {
		t.Errorf("two identical trees dumped differently:\n%s\n%s", d1, d2)
	}
	if !strings.Contains(d1, `text 16,16 size=20 semibold "Title"`) {
		t.Errorf("unexpected dump:\n%s", d1)
	}
}

func TestChipsAreReadableInBothThemes(t *testing.T) {
	for _, p := range []theme.Palette{theme.DarkPalette(), theme.LightPalette()} {
		for _, tone := range []Tone{ToneNeutral, ToneAccent, ToneGood, ToneWarn, ToneBad} {
			bg, fg := ChipBackground(p, tone), ToneColor(p, tone)
			if r := theme.Contrast(fg, bg); r < 4.5 {
				t.Errorf("%v chip tone %d: contrast %.2f", p.Mode, tone, r)
			}
		}
	}
}
