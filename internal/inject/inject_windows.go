//go:build windows

package inject

import (
	"errors"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/orangepro2001/go-nx/internal/proto"
)

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	sendInput     = user32.NewProc("SendInput")
	mapVirtualKey = user32.NewProc("MapVirtualKeyW")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove        = 0x0001
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020
	mouseMiddleUp    = 0x0040
	mouseWheel       = 0x0800
	mouseHWheel      = 0x1000
	mouseVirtualDesk = 0x4000
	mouseAbsolute    = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyUnicode  = 0x0004
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

// Injector sends input to one monitor. It can be retargeted while a session is
// running (the monitor's resolution changed), so the mapping is swapped atomically.
type Injector struct{ m atomic.Pointer[mapping] }

// New returns an Injector for the primary monitor, which is w x h pixels.
func New(w, h int) *Injector {
	j := &Injector{}
	j.SetPrimary(w, h)
	return j
}

// NewDisplay returns an Injector for a monitor that is w x h pixels with its
// top-left at (originX, originY) in virtual-desktop coordinates, on a virtual
// desktop of vw x vh pixels starting at (vx, vy). Any monitor other than the
// primary needs this: absolute coordinates are otherwise relative to the primary
// monitor alone.
func NewDisplay(w, h, originX, originY, vx, vy, vw, vh int) *Injector {
	j := &Injector{}
	j.SetDisplay(w, h, originX, originY, vx, vy, vw, vh)
	return j
}

// SetPrimary retargets j at the primary monitor, w x h pixels.
func (j *Injector) SetPrimary(w, h int) { j.m.Store(&mapping{w: w, h: h}) }

// SetDisplay retargets j; see NewDisplay for the arguments.
func (j *Injector) SetDisplay(w, h, originX, originY, vx, vy, vw, vh int) {
	j.m.Store(&mapping{w: w, h: h, originX: originX, originY: originY, vx: vx, vy: vy, vw: vw, vh: vh, virtual: true})
}

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
	case proto.KindWheelH:
		return j.mouse(in, mouseHWheel, uint32(in.Arg))
	case proto.KindKeyDown, proto.KindKeyUp:
		// Arg: virtual-key code in the low byte, 0x100 set for an extended key.
		return key(uint16(in.Arg&0xFF), in.Arg&0x100 != 0, in.Kind == proto.KindKeyUp)
	}
	return errors.New("inject: unknown input kind")
}

// mouse moves the pointer to (X, Y) and then applies flags, in one SendInput call
// so nothing can slip in between. If X and Y are both proto.NoMove the pointer is
// not moved first.
func (j *Injector) mouse(in proto.Input, flags, data uint32) error {
	var ev [2]mouseEvent
	n := 0
	if in.X != proto.NoMove || in.Y != proto.NoMove {
		m := j.m.Load()
		ax, ay := m.abs(int(in.X), int(in.Y))
		moveFlags := uint32(mouseMove | mouseAbsolute)
		if m.virtual {
			moveFlags |= mouseVirtualDesk
		}
		ev[n] = mouseEvent{typ: inputMouse, mi: mouseInput{dx: ax, dy: ay, flags: moveFlags}}
		n++
	}
	if flags != mouseMove {
		ev[n] = mouseEvent{typ: inputMouse, mi: mouseInput{flags: flags, data: data}}
		n++
	}
	if n == 0 {
		return nil
	}
	return send(unsafe.Pointer(&ev[0]), n, unsafe.Sizeof(ev[0]))
}

// InjectText types text as literal Unicode with KEYEVENTF_UNICODE, so it does not
// depend on the keyboard layout or on whether an input method is active: each
// UTF-16 unit is sent as a key down and a key up. Long text goes in chunks that
// never split a surrogate pair.
func (j *Injector) InjectText(text string) error {
	for _, piece := range splitUnits(textUnits(text), textChunk) {
		ev := make([]keyEvent, 0, 2*len(piece))
		for _, u := range piece {
			ev = append(ev,
				keyEvent{typ: inputKeyboard, ki: keybdInput{scan: u, flags: keyUnicode}},
				keyEvent{typ: inputKeyboard, ki: keybdInput{scan: u, flags: keyUnicode | keyUp}})
		}
		if err := send(unsafe.Pointer(&ev[0]), len(ev), unsafe.Sizeof(ev[0])); err != nil {
			return err
		}
	}
	return nil
}

func key(vk uint16, ext, up bool) error {
	scan, _, _ := mapVirtualKey.Call(uintptr(vk), 0)
	var flags uint32
	if up {
		flags |= keyUp
	}
	if ext || Extended(vk) {
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

// Compile-time check: both events must match sizeof(INPUT) on amd64 (40 bytes).
var (
	_ [40]byte = [unsafe.Sizeof(mouseEvent{})]byte{}
	_ [40]byte = [unsafe.Sizeof(keyEvent{})]byte{}
)
