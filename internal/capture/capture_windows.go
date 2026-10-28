//go:build windows

package capture

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/orangepro2001/go-nx/internal/cursor"
	"github.com/orangepro2001/go-nx/internal/tile"
)

// Capture is GDI BitBlt from the screen device context: no Desktop Duplication,
// no HDR tone mapping and no protected-content path. A monitor is addressed by its
// rectangle in virtual-desktop coordinates, which is negative for a monitor left
// of or above the primary one.

var (
	user32    = syscall.NewLazyDLL("user32.dll")
	gdi32     = syscall.NewLazyDLL("gdi32.dll")
	shcore    = syscall.NewLazyDLL("shcore.dll")
	getDC     = user32.NewProc("GetDC")
	releaseDC = user32.NewProc("ReleaseDC")
	enumDisp  = user32.NewProc("EnumDisplayMonitors")
	getMonInf = user32.NewProc("GetMonitorInfoW")
	getMetric = user32.NewProc("GetSystemMetrics")
	createDC  = gdi32.NewProc("CreateCompatibleDC")
	createDIB = gdi32.NewProc("CreateDIBSection")
	selectObj = gdi32.NewProc("SelectObject")
	deleteObj = gdi32.NewProc("DeleteObject")
	deleteDC  = gdi32.NewProc("DeleteDC")
	bitBlt    = gdi32.NewProc("BitBlt")
	drawIcon  = user32.NewProc("DrawIconEx")
	getCursor = user32.NewProc("GetCursorInfo")
	getIcon   = user32.NewProc("GetIconInfo")
	getDPI    = shcore.NewProc("GetDpiForMonitor")
	setCtx    = user32.NewProc("SetProcessDpiAwarenessContext")
	setAware  = user32.NewProc("SetProcessDPIAware")

	dpiAwareOnce sync.Once
)

const (
	smXVirtual              = 76
	smYVirtual              = 77
	smCXVirtual             = 78
	smCYVirtual             = 79
	monitorDefaultToPrimary = 0x00000001
	srccopy                 = 0x00CC0020
	captureBlt              = 0x40000000  // include layered windows
	dpiAwarePerMonitorV2    = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4
	cursorShowing           = 0x00000001
	diNormal                = 0x00000003
)

// EnableDPIAwareness makes the process per-monitor DPI aware, so the pixel sizes
// the capture reports, and the coordinates the injector is given, are the
// physical ones. Call it before any window is created: the first call that works
// decides, and later ones are ignored by Windows. It is safe to call more than
// once.
func EnableDPIAwareness() {
	dpiAwareOnce.Do(func() {
		if setCtx.Find() == nil {
			if r, _, _ := setCtx.Call(dpiAwarePerMonitorV2); r != 0 {
				return
			}
		}
		// Windows before the per-monitor context API (or an awareness that was
		// already set, which this then fails to change and leaves alone).
		setAware.Call()
	})
}

type rect struct{ Left, Top, Right, Bottom int32 }

type point struct{ X, Y int32 }

type cursorInfo struct {
	Size, Flags uint32
	Cursor      uintptr
	Position    point
}

type iconInfo struct {
	IsIcon             uint32
	HotspotX, HotspotY uint32
	Mask, Color        uintptr
}

type monitorInfoEx struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

// enumeration state. syscall.NewCallback may be called only a limited number of
// times in a process (about 2000) and never frees what it makes, and Displays runs
// every second while a viewer is connected, so there is exactly one callback and
// the results go through these package variables.
var (
	enumMu  sync.Mutex
	enumOut []Display
	enumCB  = syscall.NewCallback(func(hmon, _, _, _ uintptr) uintptr {
		var mi monitorInfoEx
		mi.Size = uint32(unsafe.Sizeof(mi))
		if r, _, _ := getMonInf.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
			return 1
		}
		d := Display{
			Index:   len(enumOut),
			Device:  syscall.UTF16ToString(mi.Device[:]),
			X:       mi.Monitor.Left,
			Y:       mi.Monitor.Top,
			W:       mi.Monitor.Right - mi.Monitor.Left,
			H:       mi.Monitor.Bottom - mi.Monitor.Top,
			Primary: mi.Flags&monitorDefaultToPrimary != 0,
			DPI:     96,
		}
		var dx, dy uint32
		if getDPI.Find() == nil {
			const mdtEffectiveDPI = 0
			if r, _, _ := getDPI.Call(hmon, mdtEffectiveDPI, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
				d.DPI = int(dx)
			}
		}
		enumOut = append(enumOut, d)
		return 1
	})
)

// Displays lists the active monitors in the order EnumDisplayMonitors reports.
func Displays() ([]Display, error) {
	EnableDPIAwareness()
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = nil
	if r, _, err := enumDisp.Call(0, 0, enumCB, 0); r == 0 {
		return nil, fmt.Errorf("capture: EnumDisplayMonitors failed: %v", err)
	}
	if len(enumOut) == 0 {
		return nil, errors.New("capture: no monitors reported")
	}
	return append([]Display(nil), enumOut...), nil
}

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

// Screen captures one monitor into a reusable BGRA buffer.
type Screen struct {
	mu      sync.Mutex // serializes Grab, Refresh, Size and Close
	display Display
	w, h    int
	screen  uintptr
	memDC   uintptr
	bmp     uintptr
	old     uintptr
	pixels  unsafe.Pointer

	pointer atomic.Bool // draw the system pointer into each grab
	noBlt   atomic.Bool // leave CAPTUREBLT out (layered windows then may not be captured)
}

// NewScreen opens the primary monitor.
func NewScreen() (*Screen, error) { return Open("primary") }

// Open opens a monitor chosen by SelectDisplay's selector: "primary", an index,
// or a device name such as `\\.\DISPLAY2`.
func Open(selector string) (*Screen, error) {
	all, err := Displays()
	if err != nil {
		return nil, err
	}
	d, err := SelectDisplay(all, selector)
	if err != nil {
		return nil, err
	}
	s := &Screen{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.build(d); err != nil {
		s.release()
		return nil, err
	}
	return s, nil
}

// build creates the device contexts and the bitmap for monitor d. Callers hold s.mu.
func (s *Screen) build(d Display) error {
	if d.W <= 0 || d.H <= 0 {
		return fmt.Errorf("capture: monitor %s reports a %dx%d size", d.Device, d.W, d.H)
	}
	s.display, s.w, s.h = d, int(d.W), int(d.H)
	s.screen, _, _ = getDC.Call(0)
	if s.screen == 0 {
		return errors.New("capture: GetDC failed")
	}
	s.memDC, _, _ = createDC.Call(s.screen)
	if s.memDC == 0 {
		return errors.New("capture: CreateCompatibleDC failed")
	}
	bi := bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(s.w),
		Height: -int32(s.h), Planes: 1, Bits: 32, // negative height = top-down
	}
	s.bmp, _, _ = createDIB.Call(s.memDC, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&s.pixels)), 0, 0)
	if s.bmp == 0 || s.pixels == nil {
		return errors.New("capture: CreateDIBSection failed")
	}
	s.old, _, _ = selectObj.Call(s.memDC, s.bmp)
	return nil
}

// release frees what build made. Callers hold s.mu.
func (s *Screen) release() {
	if s.old != 0 {
		selectObj.Call(s.memDC, s.old)
		s.old = 0
	}
	if s.bmp != 0 {
		deleteObj.Call(s.bmp)
		s.bmp = 0
	}
	if s.memDC != 0 {
		deleteDC.Call(s.memDC)
		s.memDC = 0
	}
	if s.screen != 0 {
		releaseDC.Call(0, s.screen)
		s.screen = 0
	}
	s.pixels = nil
}

// Display returns the monitor this screen is capturing, as of the last Refresh.
func (s *Screen) Display() Display {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.display
}

// Size is the captured monitor's size in physical pixels.
func (s *Screen) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

// Refresh looks the monitor up again and rebuilds the capture surfaces if its
// size or position changed, or just renews the screen device context if not (a
// context can go stale across a desktop switch). It reports whether the geometry
// changed. An error means the monitor is gone. It implements remote.Refresher.
func (s *Screen) Refresh() (bool, error) {
	all, err := Displays()
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var d Display
	found := false
	for _, c := range all {
		if strings.EqualFold(c.Device, s.display.Device) {
			d, found = c, true
			break
		}
	}
	if !found {
		return false, fmt.Errorf("capture: monitor %s is gone", s.display.Device)
	}
	changed := d.X != s.display.X || d.Y != s.display.Y || d.W != s.display.W || d.H != s.display.H
	if changed || s.screen == 0 {
		s.release()
		if err := s.build(d); err != nil {
			s.release()
			return false, err
		}
		return changed, nil
	}
	releaseDC.Call(0, s.screen)
	s.screen, _, _ = getDC.Call(0)
	if s.screen == 0 {
		return false, errors.New("capture: GetDC failed")
	}
	s.display = d
	return false, nil
}

// SetCursorVisible makes later grabs include the system pointer, which BitBlt
// leaves out. It implements remote.CursorSetter.
func (s *Screen) SetCursorVisible(on bool) { s.pointer.Store(on) }

// SetLayered chooses whether grabs pass CAPTUREBLT, which includes layered
// windows (tooltips, some overlays and menus). It is on by default. Turning it
// off is an experiment for hosts whose own pointer flickers while it is captured,
// at the cost of those windows possibly missing from the picture.
func (s *Screen) SetLayered(on bool) { s.noBlt.Store(!on) }

// Grab copies the current monitor contents into dst (which must be Size() large).
func (s *Screen) Grab(dst *tile.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.screen == 0 || s.memDC == 0 || s.pixels == nil {
		return errors.New("capture: no capture surface")
	}
	if dst.W != s.w || dst.H != s.h {
		return errors.New("capture: frame size mismatch")
	}
	rop := uintptr(srccopy)
	if !s.noBlt.Load() {
		rop |= captureBlt
	}
	r, _, err := bitBlt.Call(s.memDC, 0, 0, uintptr(s.w), uintptr(s.h),
		s.screen, uintptr(s.display.X), uintptr(s.display.Y), rop)
	if r == 0 {
		return fmt.Errorf("capture: BitBlt failed: %v", err)
	}
	if s.pointer.Load() {
		s.drawCursor()
	}
	copy(dst.Pix, unsafe.Slice((*byte)(s.pixels), len(dst.Pix)))
	return nil
}

// drawCursor composites the Windows pointer after BitBlt, which does not capture
// it, so it is visible in a viewer that has no pointer of its own.
func (s *Screen) drawCursor() {
	info := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if r, _, _ := getCursor.Call(uintptr(unsafe.Pointer(&info))); r == 0 ||
		info.Flags&cursorShowing == 0 || info.Cursor == 0 {
		return
	}
	var hotX, hotY uint32
	var icon iconInfo
	if r, _, _ := getIcon.Call(info.Cursor, uintptr(unsafe.Pointer(&icon))); r != 0 {
		hotX, hotY = icon.HotspotX, icon.HotspotY
		// GetIconInfo hands over copies of the bitmaps that the caller must free.
		if icon.Mask != 0 {
			defer deleteObj.Call(icon.Mask)
		}
		if icon.Color != 0 {
			defer deleteObj.Call(icon.Color)
		}
	}
	x, y := cursor.DrawPosition(s.display.X, s.display.Y, info.Position.X, info.Position.Y, hotX, hotY)
	drawIcon.Call(s.memDC, uintptr(x), uintptr(y), info.Cursor, 0, 0, 0, 0, diNormal)
}

// Close frees the capture surfaces.
func (s *Screen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.release()
}

// VirtualScreen returns the virtual-desktop rectangle, which the injector needs
// to turn monitor-local coordinates into absolute ones.
func VirtualScreen() (x, y, w, h int) {
	EnableDPIAwareness()
	rx, _, _ := getMetric.Call(smXVirtual)
	ry, _, _ := getMetric.Call(smYVirtual)
	rw, _, _ := getMetric.Call(smCXVirtual)
	rh, _, _ := getMetric.Call(smCYVirtual)
	return int(int32(rx)), int(int32(ry)), int(rw), int(rh)
}
