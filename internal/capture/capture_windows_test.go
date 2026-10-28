//go:build windows

package capture

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"go-nx/internal/tile"
)

// These tests need a real interactive Windows desktop and paint on it, so they
// only run when GO_NX_WINDOWS_E2E=1 (set by the CI windows job). On a machine
// someone is working at, leave it unset.
const e2eEnv = "GO_NX_WINDOWS_E2E"

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv(e2eEnv) != "1" {
		t.Skipf("set %s=1 to run: this test paints on the real desktop", e2eEnv)
	}
}

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	getModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	registerClass    = user32.NewProc("RegisterClassExW")
	unregisterClass  = user32.NewProc("UnregisterClassW")
	createWindow     = user32.NewProc("CreateWindowExW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	peekMessage      = user32.NewProc("PeekMessageW")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	createSolidBrush = gdi32.NewProc("CreateSolidBrush")
)

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

const (
	wsPopup       = 0x80000000
	wsVisible     = 0x10000000
	wsExTopmost   = 0x00000008
	wsExToolWin   = 0x00000080
	pmRemove      = 0x0001
	swatchSize    = 64
	pumpDuration  = 400 * time.Millisecond
	swatchSampleX = swatchSize / 2
)

// pump runs the calling thread's message loop for d so the window is painted.
func pump(d time.Duration) {
	var msg [48]byte // MSG on amd64
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		r, _, _ := peekMessage.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0, pmRemove)
		if r == 0 {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
	}
}

// showSwatch puts a solid-colour topmost window at the desktop's top-left corner
// and returns a function that removes it. It must run on a locked OS thread.
func showSwatch(t *testing.T, name string, r, g, b byte) func() {
	t.Helper()
	inst, _, _ := getModuleHandle.Call(0)
	brush, _, _ := createSolidBrush.Call(uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16) // COLORREF is 0x00BBGGRR
	if brush == 0 {
		t.Fatal("CreateSolidBrush failed")
	}
	class, _ := syscall.UTF16PtrFromString("gonxswatch" + name)
	proc := syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
		ret, _, _ := defWindowProc.Call(hwnd, msg, wp, lp)
		return ret
	})
	wc := wndClassEx{wndProc: proc, instance: inst, background: brush, className: class}
	wc.size = uint32(unsafe.Sizeof(wc))
	if a, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); a == 0 {
		deleteObj.Call(brush)
		t.Fatalf("RegisterClassExW: %v", err)
	}
	title, _ := syscall.UTF16PtrFromString(name)
	hwnd, _, err := createWindow.Call(wsExTopmost|wsExToolWin, uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(title)), wsPopup|wsVisible, 0, 0, swatchSize, swatchSize, 0, 0, inst, 0)
	if hwnd == 0 {
		unregisterClass.Call(uintptr(unsafe.Pointer(class)), inst)
		deleteObj.Call(brush)
		t.Fatalf("CreateWindowExW: %v", err)
	}
	pump(pumpDuration)
	return func() {
		destroyWindow.Call(hwnd)
		pump(50 * time.Millisecond)
		unregisterClass.Call(uintptr(unsafe.Pointer(class)), inst)
		deleteObj.Call(brush)
	}
}

func TestGrabSolidColours(t *testing.T) {
	requireE2E(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	s, err := NewScreen()
	if err != nil {
		t.Fatalf("NewScreen: %v", err)
	}
	defer s.Close()
	w, h := s.Size()
	if w < swatchSize || h < swatchSize {
		t.Fatalf("screen %dx%d is smaller than the %d px swatch", w, h, swatchSize)
	}
	t.Logf("primary screen %dx%d", w, h)
	f := tile.NewFrame(w, h)

	// The frame is BGRA: an asymmetric set of colours catches any R/B swap.
	cases := []struct {
		name    string
		r, g, b byte
	}{{"red", 255, 0, 0}, {"green", 0, 255, 0}, {"blue", 0, 0, 255}, {"orange", 255, 128, 0}}
	for _, c := range cases {
		remove := showSwatch(t, c.name, c.r, c.g, c.b)
		err := s.Grab(f)
		remove()
		if err != nil {
			t.Fatalf("%s: Grab: %v", c.name, err)
		}
		o := (swatchSampleX*f.W + swatchSampleX) * 4
		px := f.Pix[o : o+4]
		t.Logf("%s: bytes B,G,R,X = %d,%d,%d,%d (the 4th byte is not alpha)", c.name, px[0], px[1], px[2], px[3])
		if px[0] != c.b || px[1] != c.g || px[2] != c.r {
			t.Errorf("%s: captured B,G,R = %d,%d,%d, want %d,%d,%d", c.name, px[0], px[1], px[2], c.b, c.g, c.r)
		}
	}
}

func TestGrabRepeatedly(t *testing.T) {
	requireE2E(t)
	s, err := NewScreen()
	if err != nil {
		t.Fatalf("NewScreen: %v", err)
	}
	defer s.Close()
	w, h := s.Size()
	f := tile.NewFrame(w, h)
	for i := 0; i < 20; i++ {
		if err := s.Grab(f); err != nil {
			t.Fatalf("grab %d: %v", i, err)
		}
	}
	if err := s.Grab(tile.NewFrame(w+1, h)); err == nil {
		t.Error("Grab accepted a frame of the wrong size")
	}
}
