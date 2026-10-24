//go:build windows

// Package capture grabs the primary screen with GDI BitBlt.
package capture

import (
	"errors"
	"sync"
	"syscall"
	"unsafe"

	"go-nx/internal/tile"
)

var (
	user32    = syscall.NewLazyDLL("user32.dll")
	gdi32     = syscall.NewLazyDLL("gdi32.dll")
	shcore    = syscall.NewLazyDLL("shcore.dll")
	getDC     = user32.NewProc("GetDC")
	releaseDC = user32.NewProc("ReleaseDC")
	sysMetric = user32.NewProc("GetSystemMetrics")
	setDpiCtx = user32.NewProc("SetProcessDPIAware")
	createDC  = gdi32.NewProc("CreateCompatibleDC")
	createDIB = gdi32.NewProc("CreateDIBSection")
	selectObj = gdi32.NewProc("SelectObject")
	deleteObj = gdi32.NewProc("DeleteObject")
	deleteDC  = gdi32.NewProc("DeleteDC")
	bitBlt    = gdi32.NewProc("BitBlt")
	_         = shcore
)

const (
	smCxScreen = 0
	smCyScreen = 1
	srccopy    = 0x00CC0020
	captureBlt = 0x40000000 // include layered windows
)

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes, Bits  uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// Screen captures the primary monitor into a reusable BGRA buffer.
type Screen struct {
	mu     sync.Mutex // serializes Grab and Close
	w, h   int
	screen uintptr
	memDC  uintptr
	bmp    uintptr
	old    uintptr
	pixels unsafe.Pointer
}

// NewScreen opens the primary screen. The process is made DPI aware so the
// captured size matches physical pixels.
func NewScreen() (*Screen, error) {
	setDpiCtx.Call()
	w, _, _ := sysMetric.Call(smCxScreen)
	h, _, _ := sysMetric.Call(smCyScreen)
	if w == 0 || h == 0 {
		return nil, errors.New("capture: GetSystemMetrics returned 0")
	}
	s := &Screen{w: int(w), h: int(h)}
	s.screen, _, _ = getDC.Call(0)
	if s.screen == 0 {
		return nil, errors.New("capture: GetDC failed")
	}
	s.memDC, _, _ = createDC.Call(s.screen)
	if s.memDC == 0 {
		s.Close()
		return nil, errors.New("capture: CreateCompatibleDC failed")
	}
	bi := bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(s.w),
		Height: -int32(s.h), Planes: 1, Bits: 32, // negative height = top-down
	}
	s.bmp, _, _ = createDIB.Call(s.memDC, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&s.pixels)), 0, 0)
	if s.bmp == 0 || s.pixels == nil {
		s.Close()
		return nil, errors.New("capture: CreateDIBSection failed")
	}
	s.old, _, _ = selectObj.Call(s.memDC, s.bmp)
	return s, nil
}

func (s *Screen) Size() (int, int) { return s.w, s.h }

// Grab copies the current screen into dst (which must be Size() large).
func (s *Screen) Grab(dst *tile.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dst.W != s.w || dst.H != s.h {
		return errors.New("capture: frame size mismatch")
	}
	r, _, _ := bitBlt.Call(s.memDC, 0, 0, uintptr(s.w), uintptr(s.h),
		s.screen, 0, 0, srccopy|captureBlt)
	if r == 0 {
		return errors.New("capture: BitBlt failed")
	}
	copy(dst.Pix, unsafe.Slice((*byte)(s.pixels), len(dst.Pix)))
	return nil
}

func (s *Screen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.old != 0 {
		selectObj.Call(s.memDC, s.old)
	}
	if s.bmp != 0 {
		deleteObj.Call(s.bmp)
	}
	if s.memDC != 0 {
		deleteDC.Call(s.memDC)
	}
	if s.screen != 0 {
		releaseDC.Call(0, s.screen)
	}
}
