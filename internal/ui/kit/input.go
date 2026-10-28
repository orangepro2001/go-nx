package kit

import "time"

// ID names an interactive node so that focus, a scroll position or a half-typed word
// survive the tree being rebuilt. An ID is chosen by the screen that builds the node and
// is unique within the tree.
type ID string

// Mouse buttons.
const (
	Left = iota
	Right
	Middle
)

// Key is a key the toolkit acts on.
type Key uint8

const (
	KeyNone Key = iota
	KeyTab
	KeyEnter
	KeyEscape
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeySpace
	KeyA // with Ctrl: select all
	KeyC // with Ctrl: copy
	KeyV // with Ctrl: paste
	KeyX // with Ctrl: cut
	KeyK // with Ctrl: quick switch
)

// KeyPress is one key event, including auto-repeats.
type KeyPress struct {
	Key              Key
	Shift, Ctrl, Alt bool
}

// Input is everything the outside world did since the last frame.
type Input struct {
	Now               time.Time
	X, Y              float64 // pointer position
	Pointer           bool    // the pointer is over the window
	Down              [3]bool
	Pressed, Released [3]bool // transitions since the last frame
	WheelX, WheelY    float64 // in lines; up is positive Y
	Keys              []KeyPress
	Text              string // text typed or committed by the input method
	Composing         string // the input method's unfinished text
	Focused           bool   // the window has the keyboard
}

// Cursor is the shape the pointer should have over a node.
type Cursor uint8

const (
	CursorDefault Cursor = iota
	CursorPointer
	CursorText
)

// Clipboard is the system clipboard. A nil Clipboard in the UI means there is none.
type Clipboard interface {
	Get() string
	Set(string)
}

// Interactive is what a node registers while it is placed so that input can find it.
type Interactive struct {
	ID        ID
	R         Rect // where it can be hit; the UI clips it to the visible part
	Focusable bool
	Disabled  bool
	Cursor    Cursor
	// OnClick runs when the primary button is pressed and released over the node, or
	// when Enter or Space is pressed while it has focus.
	OnClick func()
	// OnPress runs when the primary button goes down over the node.
	OnPress func()
	// OnDrag runs while the primary button is held after it went down over the node,
	// with the pointer position, until it is released.
	OnDrag func(x, y float64)
	// OnWheel is offered the wheel when the pointer is over the node; true means used.
	OnWheel func(dx, dy float64) bool
	// OnKey is offered every key while the node has focus; true means used.
	OnKey func(k KeyPress) bool
	// OnText receives text typed (or committed by the input method) while it has focus.
	OnText func(s string)
	// OnFocus and OnBlur tell the node it gained or lost the keyboard.
	OnFocus, OnBlur func()
	// Group, for a node inside a modal layer, is true: Escape and focus stay inside.
}
