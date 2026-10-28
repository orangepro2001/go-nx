//go:build windows

// Package wintest has helpers for tests that need a real window on the
// interactive desktop: a window that records the messages it receives, can be
// given the keyboard focus, and can be filled with a solid colour (optionally as
// a layered window). It is imported only from tests, so it is never linked into
// a release build.
//
// Everything here moves the real cursor or paints on the real desktop, so tests
// that use it must call Skip first: they only run when GO_NX_WINDOWS_E2E=1,
// which CI sets and a developer's own machine does not.
package wintest

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Env is the environment variable that turns the real-desktop tests on.
const Env = "GO_NX_WINDOWS_E2E"

// holding records the tests that hold the desktop, so Skip can be called more
// than once in one test (helpers do).
var holding sync.Map

// Skip skips the test unless real-desktop tests are enabled. Otherwise it takes
// the desktop for the rest of the test: go test runs packages in parallel, and
// two tests that paint windows or move the cursor at the same time would see each
// other's windows. The lock is a named semaphore, so it works across the separate
// test processes of different packages, and is released when the test ends.
func Skip(t *testing.T) {
	t.Helper()
	if os.Getenv(Env) != "1" {
		t.Skipf("set %s=1 to run: this test paints on, or sends input to, the real desktop", Env)
	}
	if _, again := holding.LoadOrStore(t, true); again {
		return // this test already holds the desktop; taking it twice would wait forever
	}
	t.Cleanup(func() { holding.Delete(t) })
	name, _ := syscall.UTF16PtrFromString(`Local\go-nx-wintest-desktop`)
	h, _, err := createSemaphore.Call(0, 1, 1, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		t.Fatalf("wintest: CreateSemaphoreW: %v", err)
	}
	const waitObject0, timeoutMs = 0, 3 * 60 * 1000
	if r, _, _ := waitForSingleObject.Call(h, timeoutMs); r != waitObject0 {
		closeHandle.Call(h)
		t.Fatalf("wintest: another real-desktop test held the desktop for over %d s (result %#x)", timeoutMs/1000, r)
	}
	t.Cleanup(func() {
		releaseSemaphore.Call(h, 1, 0)
		closeHandle.Call(h)
	})
}

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	gdi32               = syscall.NewLazyDLL("gdi32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	getModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	registerClass       = user32.NewProc("RegisterClassExW")
	unregisterClass     = user32.NewProc("UnregisterClassW")
	createWindow        = user32.NewProc("CreateWindowExW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	peekMessage         = user32.NewProc("PeekMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	setLayeredAttr      = user32.NewProc("SetLayeredWindowAttributes")
	setForeground       = user32.NewProc("SetForegroundWindow")
	getForeground       = user32.NewProc("GetForegroundWindow")
	setFocus            = user32.NewProc("SetFocus")
	keybdEvent          = user32.NewProc("keybd_event")
	getSystemMetrics    = user32.NewProc("GetSystemMetrics")
	setDPIAware         = user32.NewProc("SetProcessDPIAware")
	createSemaphore     = kernel32.NewProc("CreateSemaphoreW")
	waitForSingleObject = kernel32.NewProc("WaitForSingleObject")
	releaseSemaphore    = kernel32.NewProc("ReleaseSemaphore")
	closeHandle         = kernel32.NewProc("CloseHandle")
	setCursorPos        = user32.NewProc("SetCursorPos")
	loadCursor          = user32.NewProc("LoadCursorW")
	showCursor          = user32.NewProc("ShowCursor")
	getCursorInfo       = user32.NewProc("GetCursorInfo")
	getCursorPos        = user32.NewProc("GetCursorPos")
	createSolidBrush    = gdi32.NewProc("CreateSolidBrush")
	deleteObject        = gdi32.NewProc("DeleteObject")
)

// Window messages and styles used by the tests.
const (
	WMChar        = 0x0102
	WMMouseWheel  = 0x020A
	WMMouseHWheel = 0x020E
	WMKeyDown     = 0x0100
	WMKeyUp       = 0x0101

	wsPopup     = 0x80000000
	wsVisible   = 0x10000000
	wsExTopmost = 0x00000008
	wsExLayered = 0x00080000
	wsExTool    = 0x00000080
	pmRemove    = 0x0001
	lwaAlpha    = 0x00000002
)

// Msg is one window message as the window procedure saw it.
type Msg struct {
	Msg    uint32
	WParam uintptr
	LParam uintptr
}

// Options describes a test window.
type Options struct {
	X, Y, W, H int
	R, G, B    byte // background colour
	Layered    bool // make it a layered window (what CAPTUREBLT is for)
	Alpha      byte // opacity of a layered window; 0 means fully opaque
}

// Window is a top-level window that records the messages it receives. Create it
// with New, from a goroutine that stays on one OS thread, and Close it there.
type Window struct {
	HWND uintptr

	mu    sync.Mutex
	msgs  []Msg
	class *uint16
	inst  uintptr
	brush uintptr
}

var (
	cbOnce sync.Once
	cb     uintptr
	regMu  sync.Mutex
	byHWND = map[uintptr]*Window{}
	seq    int
)

func wndProc(hwnd, msg, wp, lp uintptr) uintptr {
	regMu.Lock()
	w := byHWND[hwnd]
	regMu.Unlock()
	if w != nil {
		w.mu.Lock()
		w.msgs = append(w.msgs, Msg{uint32(msg), wp, lp})
		w.mu.Unlock()
	}
	r, _, _ := defWindowProc.Call(hwnd, msg, wp, lp)
	return r
}

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

// New creates and shows a window, and pins the calling goroutine to its OS
// thread (Close releases it): a window's messages belong to the thread that
// created it.
func New(t *testing.T, o Options) *Window {
	t.Helper()
	runtime.LockOSThread()
	cbOnce.Do(func() { cb = syscall.NewCallback(wndProc) })
	setDPIAware.Call() // physical pixels, like the host process

	regMu.Lock()
	seq++
	name := fmt.Sprintf("gonxwintest%d", seq)
	regMu.Unlock()

	w := &Window{}
	w.inst, _, _ = getModuleHandle.Call(0)
	w.brush, _, _ = createSolidBrush.Call(uintptr(o.R) | uintptr(o.G)<<8 | uintptr(o.B)<<16) // COLORREF is 0x00BBGGRR
	if w.brush == 0 {
		runtime.UnlockOSThread()
		t.Fatal("wintest: CreateSolidBrush failed")
	}
	w.class, _ = syscall.UTF16PtrFromString(name)
	arrow, _, _ := loadCursor.Call(0, 32512) // IDC_ARROW: without a class cursor the pointer has no shape over the window
	wc := wndClassEx{wndProc: cb, instance: w.inst, background: w.brush, className: w.class, cursor: arrow}
	wc.size = uint32(unsafe.Sizeof(wc))
	if a, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); a == 0 {
		deleteObject.Call(w.brush)
		runtime.UnlockOSThread()
		t.Fatalf("wintest: RegisterClassExW: %v", err)
	}
	ex := uintptr(wsExTopmost | wsExTool)
	if o.Layered {
		ex |= wsExLayered
	}
	title, _ := syscall.UTF16PtrFromString(name)
	hwnd, _, err := createWindow.Call(ex, uintptr(unsafe.Pointer(w.class)), uintptr(unsafe.Pointer(title)),
		wsPopup|wsVisible, uintptr(o.X), uintptr(o.Y), uintptr(o.W), uintptr(o.H), 0, 0, w.inst, 0)
	if hwnd == 0 {
		unregisterClass.Call(uintptr(unsafe.Pointer(w.class)), w.inst)
		deleteObject.Call(w.brush)
		runtime.UnlockOSThread()
		t.Fatalf("wintest: CreateWindowExW: %v", err)
	}
	w.HWND = hwnd
	regMu.Lock()
	byHWND[hwnd] = w
	regMu.Unlock()
	if o.Layered {
		alpha := o.Alpha
		if alpha == 0 {
			alpha = 255
		}
		setLayeredAttr.Call(hwnd, 0, uintptr(alpha), lwaAlpha)
	}
	w.Pump(300 * time.Millisecond) // let it be painted
	return w
}

// Pump runs the thread's message loop for d: messages are translated (so a
// VK_PACKET key from a Unicode injection becomes WM_CHAR) and dispatched.
func (w *Window) Pump(d time.Duration) {
	var msg [48]byte // MSG on amd64
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		r, _, _ := peekMessage.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0, pmRemove)
		if r == 0 {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
	}
}

// Focus gives the window the keyboard focus and the foreground, which a process
// that is not already in the foreground is normally refused; a brief Alt key
// press is the usual way to be granted it. It fails the test if the window did
// not end up in the foreground, because input sent after that goes nowhere.
func (w *Window) Focus(t *testing.T) {
	t.Helper()
	const vkMenu, keyUp = 0x12, 0x0002
	keybdEvent.Call(vkMenu, 0, 0, 0)
	setForeground.Call(w.HWND)
	setFocus.Call(w.HWND)
	keybdEvent.Call(vkMenu, 0, keyUp, 0)
	w.Pump(200 * time.Millisecond)
	if fg, _, _ := getForeground.Call(); fg != w.HWND {
		t.Fatalf("wintest: window %#x is not in the foreground (foreground is %#x)", w.HWND, fg)
	}
}

// Messages returns the messages received so far.
func (w *Window) Messages() []Msg {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Msg(nil), w.msgs...)
}

// Reset forgets the messages received so far.
func (w *Window) Reset() {
	w.mu.Lock()
	w.msgs = nil
	w.mu.Unlock()
}

// Chars returns the UTF-16 code units delivered as WM_CHAR, in order.
func (w *Window) Chars() []uint16 {
	var out []uint16
	for _, m := range w.Messages() {
		if m.Msg == WMChar {
			out = append(out, uint16(m.WParam))
		}
	}
	return out
}

// WheelDeltas returns the wheel deltas of messages of the given kind
// (WMMouseWheel or WMMouseHWheel), in order.
func (w *Window) WheelDeltas(kind uint32) []int {
	var out []int
	for _, m := range w.Messages() {
		if m.Msg == kind {
			out = append(out, int(int16(m.WParam>>16)))
		}
	}
	return out
}

// Close destroys the window and releases the OS thread.
func (w *Window) Close() {
	w.Pump(30 * time.Millisecond)
	regMu.Lock()
	delete(byHWND, w.HWND)
	regMu.Unlock()
	destroyWindow.Call(w.HWND)
	w.Pump(30 * time.Millisecond)
	unregisterClass.Call(uintptr(unsafe.Pointer(w.class)), w.inst)
	deleteObject.Call(w.brush)
	runtime.UnlockOSThread()
}

// IsWine reports whether this is Wine rather than Windows. Wine is good enough to
// debug a test with, but its input and capture details differ, so a test that
// checks them exactly can relax there.
func IsWine() bool {
	return syscall.NewLazyDLL("ntdll.dll").NewProc("wine_get_version").Find() == nil
}

// SetCursorPos moves the pointer to a screen pixel.
func SetCursorPos(x, y int) {
	setCursorPos.Call(uintptr(x), uintptr(y))
}

// ShowPointer makes sure the system pointer is showing, which a desktop without a
// mouse (a VM, a CI runner) does not by default: with no mouse installed the
// pointer's display count starts at -1, and each ShowCursor(TRUE) raises it. It
// returns whether the pointer is showing now, and a function that puts the count
// back. Call it on the thread that created the test window.
func ShowPointer() (showing bool, undo func()) {
	raised := 0
	for ; raised < 4 && !pointerShowing(); raised++ {
		showCursor.Call(1)
	}
	return pointerShowing(), func() {
		for ; raised > 0; raised-- {
			showCursor.Call(0)
		}
	}
}

func pointerShowing() bool {
	var ci struct {
		Size, Flags uint32
		Cursor      uintptr
		X, Y        int32
	}
	ci.Size = uint32(unsafe.Sizeof(ci))
	if r, _, _ := getCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 {
		return false
	}
	return ci.Flags&1 != 0 // CURSOR_SHOWING
}

// CursorPos returns the pointer's screen position.
func CursorPos() (x, y int) {
	var p [2]int32
	getCursorPos.Call(uintptr(unsafe.Pointer(&p[0])))
	return int(p[0]), int(p[1])
}

// ScreenSize is the primary screen in physical pixels.
func ScreenSize() (w, h int) {
	setDPIAware.Call()
	cw, _, _ := getSystemMetrics.Call(0)
	ch, _, _ := getSystemMetrics.Call(1)
	return int(cw), int(ch)
}
