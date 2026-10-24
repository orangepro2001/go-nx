//go:build windows

// Package inject performs mouse and keyboard input on the local desktop.
package inject

import (
	"errors"
	"syscall"
	"unsafe"

	"go-nx/internal/proto"
)

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	sendInput     = user32.NewProc("SendInput")
	mapVirtualKey = user32.NewProc("MapVirtualKeyW")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove       = 0x0001
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseWheel      = 0x0800
	mouseAbsolute   = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
)

type mouseInput struct {
	dx, dy int32
	data   uint32
	flags  uint32
	time   uint32
	_      uint32
	extra  uintptr
}

type keybdInput struct {
	vk, scan uint16
	flags    uint32
	time     uint32
	_        uint32
	extra    uintptr
	_        [8]byte // pad to the size of mouseInput
}

type mouseEvent struct {
	typ uint32
	_   uint32
	mi  mouseInput
}

type keyEvent struct {
	typ uint32
	_   uint32
	ki  keybdInput
}

// Injector sends input to the primary screen, which is w x h pixels.
type Injector struct{ w, h int }

func New(w, h int) *Injector { return &Injector{w, h} }

func (j *Injector) Inject(in proto.Input) error {
	switch in.Kind {
	case proto.KindMouseMove:
		return j.mouse(in, mouseMove, 0)
	case proto.KindMouseDown, proto.KindMouseUp:
		down := in.Kind == proto.KindMouseDown
		flag := map[int32][2]uint32{
			0: {mouseLeftDown, mouseLeftUp},
			1: {mouseRightDown, mouseRightUp},
			2: {mouseMiddleDown, mouseMiddleUp},
		}[in.Arg]
		if flag == ([2]uint32{}) {
			return errors.New("inject: unknown mouse button")
		}
		f := flag[1]
		if down {
			f = flag[0]
		}
		return j.mouse(in, f, 0)
	case proto.KindWheel:
		return j.mouse(in, mouseWheel, uint32(in.Arg))
	case proto.KindKeyDown, proto.KindKeyUp:
		// Arg: virtual-key code in the low byte, 0x100 set for an extended key.
		return key(uint16(in.Arg&0xFF), in.Arg&0x100 != 0, in.Kind == proto.KindKeyUp)
	}
	return errors.New("inject: unknown input kind")
}

// mouse moves the cursor to (X, Y) and then applies flags in one SendInput call.
func (j *Injector) mouse(in proto.Input, flags, data uint32) error {
	x := clamp(int(in.X), j.w)
	y := clamp(int(in.Y), j.h)
	ev := [2]mouseEvent{
		{typ: inputMouse, mi: mouseInput{
			dx: int32(x * 65535 / max(j.w-1, 1)), dy: int32(y * 65535 / max(j.h-1, 1)),
			flags: mouseMove | mouseAbsolute,
		}},
		{typ: inputMouse, mi: mouseInput{flags: flags, data: data}},
	}
	n := 2
	if flags == mouseMove {
		n = 1
	}
	return send(unsafe.Pointer(&ev[0]), n, unsafe.Sizeof(ev[0]))
}

func key(vk uint16, ext, up bool) error {
	scan, _, _ := mapVirtualKey.Call(uintptr(vk), 0)
	var flags uint32
	if up {
		flags |= keyUp
	}
	if ext || extended(vk) {
		flags |= keyExtended
	}
	ev := keyEvent{typ: inputKeyboard, ki: keybdInput{vk: vk, scan: uint16(scan), flags: flags}}
	return send(unsafe.Pointer(&ev), 1, unsafe.Sizeof(ev))
}

func send(p unsafe.Pointer, n int, size uintptr) error {
	r, _, err := sendInput.Call(uintptr(n), uintptr(p), size)
	if int(r) != n {
		return errors.New("inject: SendInput failed: " + err.Error())
	}
	return nil
}

func clamp(v, n int) int { return min(max(v, 0), n-1) }

// extended reports whether vk needs KEYEVENTF_EXTENDEDKEY (arrows, navigation
// block, right-hand Ctrl/Alt, numpad divide, Win keys).
func extended(vk uint16) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, // PgUp PgDn End Home arrows
		0x2D, 0x2E, // Insert Delete
		0xA3, 0xA5, // RCtrl RAlt
		0x6F,       // numpad /
		0x5B, 0x5C: // LWin RWin
		return true
	}
	return false
}

// Compile-time check: both events must match sizeof(INPUT) on amd64 (40 bytes).
var (
	_ [40]byte = [unsafe.Sizeof(mouseEvent{})]byte{}
	_ [40]byte = [unsafe.Sizeof(keyEvent{})]byte{}
)
