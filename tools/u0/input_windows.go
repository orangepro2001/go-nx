//go:build windows

package main

import (
	"slices"
	"syscall"
	"time"
	"unsafe"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/exp/textinput"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procSendInput  = user32.NewProc("SendInput")
	procKeybdEvent = user32.NewProc("keybd_event")
	procFindWindow = user32.NewProc("FindWindowW")
	procSetFg      = user32.NewProc("SetForegroundWindow")
	procSetFocus   = user32.NewProc("SetFocus")
)

type keybdInput struct {
	vk, scan uint16
	flags    uint32
	time     uint32
	extra    uintptr
}

// input is the Win32 INPUT structure for a keyboard event (type 1), padded to the
// size of the union.
type input struct {
	typ uint32
	_   uint32
	ki  keybdInput
	_   [8]byte
}

const (
	keyUp   = 0x0002
	unicode = 0x0004
)

func sendKey(vk uint16, flags uint32) {
	in := input{typ: 1, ki: keybdInput{vk: vk, flags: flags}}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func tap(vk uint16) {
	sendKey(vk, 0)
	time.Sleep(15 * time.Millisecond)
	sendKey(vk, keyUp)
	time.Sleep(15 * time.Millisecond)
}

func typeUnicode(r rune) {
	for _, u := range utf16Of(r) {
		in := input{typ: 1, ki: keybdInput{scan: u, flags: unicode}}
		procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
		in.ki.flags |= keyUp
		procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
		time.Sleep(15 * time.Millisecond)
	}
}

func utf16Of(r rune) []uint16 {
	if r < 0x10000 {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))}
}

// foreground brings the window titled title to the front the way wintest does: a tap
// on Alt lets SetForegroundWindow through.
func foreground(title string) bool {
	p, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(p)))
	if hwnd == 0 {
		return false
	}
	procKeybdEvent.Call(0x12, 0, 0, 0)
	procSetFg.Call(hwnd)
	procSetFocus.Call(hwnd)
	procKeybdEvent.Call(0x12, 0, keyUp, 0)
	return true
}

type inputGame struct {
	res       *InputResult
	field     textinput.Field
	keys      []ebiten.Key
	chars     []rune
	tick      int
	startedAt time.Time
	injected  bool
	doneAt    time.Time
}

const inputTitle = "go-nx u0 input"

func (g *inputGame) Layout(w, h int) (int, int) { return w, h }
func (g *inputGame) Draw(*ebiten.Image)         {}

func (g *inputGame) Update() error {
	g.tick++
	if g.startedAt.IsZero() {
		g.startedAt = time.Now()
	}
	g.field.Focus()
	g.field.HandleInput(10, 10)
	g.keys = inpututil.AppendJustPressedKeys(g.keys)
	g.chars = ebiten.AppendInputChars(g.chars)
	if ebiten.IsFocused() {
		g.res.Focused = true
	}

	switch {
	case !g.injected && time.Since(g.startedAt) > 700*time.Millisecond:
		foreground(inputTitle)
		if g.res.Focused {
			g.injected = true
			go g.inject()
		} else if time.Since(g.startedAt) > 6*time.Second {
			g.res.Err = "the window never got the keyboard focus, so nothing could be injected"
			return ebiten.Termination
		}
	case g.injected && !g.doneAt.IsZero() && time.Since(g.doneAt) > 500*time.Millisecond:
		g.finish()
		return ebiten.Termination
	}
	return nil
}

func (g *inputGame) inject() {
	for _, vk := range []uint16{'H', 'E', 'L', 'L', 'O'} {
		tap(vk)
	}
	tap(0x7C) // F13
	tap(0x15) // VK_HANGUL
	tap(0x19) // VK_HANJA
	for _, r := range "한中" {
		typeUnicode(r)
	}
	g.doneAt = time.Now()
}

func (g *inputGame) finish() {
	r := g.res
	for _, k := range g.keys {
		r.Keys = append(r.Keys, k.String())
	}
	r.Chars = string(g.chars)
	r.FieldText = g.field.Text()
	// VK_HANGUL and VK_HANJA are tapped between F13 and the Unicode text. If Ebiten had
	// keys for them they would show up in Keys; it has none, so the keys after F13 are
	// only what that mapping lets through.
	i := slices.Index(r.Keys, "F13")
	r.NoKeyForHangul = i >= 0 && i == len(r.Keys)-1
	for _, c := range g.chars {
		if c > 0x7F {
			r.HangulChars += string(c)
		}
	}
}

func runInput() (*InputResult, error) {
	res := &InputResult{}
	g := &inputGame{res: res}
	if err := runWindow(inputTitle, 320, 120, g); err != nil {
		res.Err = err.Error()
		return res, err
	}
	return res, nil
}
