//go:build windows

package capture

import (
	"strings"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/wintest"
)

// These tests need a real interactive Windows desktop and paint on it, so they
// only run when GO_NX_WINDOWS_E2E=1 (set by the CI windows job). On a machine
// someone is working at, leave it unset. See package wintest.

const (
	swatchSize   = 64
	swatchSample = swatchSize / 2
)

func TestGrabSolidColours(t *testing.T) {
	wintest.Skip(t)
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
		win := wintest.New(t, wintest.Options{X: 0, Y: 0, W: swatchSize, H: swatchSize, R: c.r, G: c.g, B: c.b})
		err := s.Grab(f)
		win.Close()
		if err != nil {
			t.Fatalf("%s: Grab: %v", c.name, err)
		}
		o := (swatchSample*f.W + swatchSample) * 4
		px := f.Pix[o : o+4]
		t.Logf("%s: bytes B,G,R,X = %d,%d,%d,%d (the 4th byte is not alpha)", c.name, px[0], px[1], px[2], px[3])
		if px[0] != c.b || px[1] != c.g || px[2] != c.r {
			t.Errorf("%s: captured B,G,R = %d,%d,%d, want %d,%d,%d", c.name, px[0], px[1], px[2], c.b, c.g, c.r)
		}
	}
}

func TestGrabRepeatedly(t *testing.T) {
	wintest.Skip(t)
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

func TestDisplaysAndOpenAgree(t *testing.T) {
	wintest.Skip(t)
	all, err := Displays()
	if err != nil {
		t.Fatal(err)
	}
	primaries := 0
	for _, d := range all {
		t.Logf("display: %s", d)
		if d.W <= 0 || d.H <= 0 || d.DPI < 96 {
			t.Errorf("implausible display %s", d)
		}
		if d.Primary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Fatalf("%d primary displays, want 1", primaries)
	}
	w, h := wintest.ScreenSize()
	prim, err := SelectDisplay(all, "primary")
	if err != nil {
		t.Fatal(err)
	}
	if int(prim.W) != w || int(prim.H) != h {
		t.Errorf("primary display is %dx%d but the system says %dx%d", prim.W, prim.H, w, h)
	}
	for _, sel := range []string{"primary", "", prim.Device, strings.ToLower(prim.Device), "0"} {
		s, err := Open(sel)
		if err != nil {
			t.Errorf("Open(%q): %v", sel, err)
			continue
		}
		if sw, sh := s.Size(); len(all) == 1 && (sw != w || sh != h) {
			t.Errorf("Open(%q) captures %dx%d, want %dx%d", sel, sw, sh, w, h)
		}
		s.Close()
	}
	if _, err := Open("DISPLAY99"); err == nil {
		t.Error("Open of a monitor that does not exist succeeded")
	}
}

func TestRefreshOfAnUnchangedMonitorChangesNothing(t *testing.T) {
	wintest.Skip(t)
	s, err := NewScreen()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, h := s.Size()
	for i := 0; i < 3; i++ {
		changed, err := s.Refresh()
		if err != nil || changed {
			t.Fatalf("Refresh = %v, %v on an unchanged monitor", changed, err)
		}
	}
	if nw, nh := s.Size(); nw != w || nh != h {
		t.Errorf("size changed from %dx%d to %dx%d", w, h, nw, nh)
	}
	f := tile.NewFrame(w, h)
	if err := s.Grab(f); err != nil {
		t.Errorf("Grab after Refresh: %v", err)
	}
}

// Displays is called every second while someone is connected. It must not use up
// the process's limited supply of callbacks.
func TestDisplaysCanBeCalledManyTimes(t *testing.T) {
	wintest.Skip(t)
	for i := 0; i < 3000; i++ { // more than syscall.NewCallback would ever allow
		if _, err := Displays(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

// The pointer is left out of a grab by default and drawn in when asked, at the
// right place: inside the window painted under it, covering its hotspot.
func TestThePointerIsDrawnOnlyWhenAsked(t *testing.T) {
	wintest.Skip(t)
	s, err := NewScreen()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, h := s.Size()
	const wx, wy, ww, wh = 300, 200, 300, 200
	if w < wx+ww || h < wy+wh {
		t.Skipf("screen %dx%d is too small for the test window", w, h)
	}
	win := wintest.New(t, wintest.Options{X: wx, Y: wy, W: ww, H: wh, R: 20, G: 60, B: 100})
	defer win.Close()
	const px, py = 400, 300 // the pointer's hotspot, well inside the window
	wintest.SetCursorPos(px, py)
	win.Pump(200 * time.Millisecond)

	grab := func(on bool) *tile.Frame {
		s.SetCursorVisible(on)
		f := tile.NewFrame(w, h)
		if err := s.Grab(f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	off1, off2, on := grab(false), grab(false), grab(true)

	minX, minY, maxX, maxY, n := w, h, -1, -1, 0
	for y := wy; y < wy+wh; y++ {
		for x := wx; x < wx+ww; x++ {
			o := (y*w + x) * 4
			if string(off1.Pix[o:o+3]) != string(off2.Pix[o:o+3]) {
				t.Fatalf("two grabs without the pointer differ at %d,%d", x, y)
			}
			if string(off1.Pix[o:o+3]) != string(on.Pix[o:o+3]) {
				n++
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if n == 0 {
		// A desktop with no mouse attached hides the pointer; there is nothing to draw.
		t.Skip("the pointer is not showing on this desktop, so there is nothing to composite")
	}
	t.Logf("the pointer covers %d pixels in %d,%d - %d,%d", n, minX, minY, maxX, maxY)
	if px < minX-1 || px > maxX+1 || py < minY-1 || py > maxY+1 {
		t.Errorf("the hotspot %d,%d is outside the drawn pointer's box %d,%d - %d,%d", px, py, minX, minY, maxX, maxY)
	}
	if maxX-minX > 64 || maxY-minY > 64 {
		t.Errorf("the drawn pointer is %dx%d pixels, which is not a pointer", maxX-minX+1, maxY-minY+1)
	}
}

// CAPTUREBLT is what includes layered windows in a grab. This records, on real
// Windows, what leaving it out costs, as evidence for deciding whether the pointer
// flicker on a host is worth that. Only the with-flag result is asserted.
func TestLayeredWindowsAndCaptureBlt(t *testing.T) {
	wintest.Skip(t)
	s, err := NewScreen()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, h := s.Size()
	win := wintest.New(t, wintest.Options{X: 0, Y: 0, W: 64, H: 64, R: 255, G: 0, B: 0, Layered: true})
	defer win.Close()

	f := tile.NewFrame(w, h)
	at := func() (b, g, r byte) {
		o := (32*w + 32) * 4
		return f.Pix[o], f.Pix[o+1], f.Pix[o+2]
	}
	s.SetLayered(true)
	if err := s.Grab(f); err != nil {
		t.Fatal(err)
	}
	if b, g, r := at(); b != 0 || g != 0 || r != 255 {
		t.Errorf("with CAPTUREBLT a layered red window was captured as B,G,R = %d,%d,%d", b, g, r)
	}
	s.SetLayered(false)
	if err := s.Grab(f); err != nil {
		t.Fatal(err)
	}
	b, g, r := at()
	t.Logf("WITHOUT CAPTUREBLT the same layered red window was captured as B,G,R = %d,%d,%d (red would be 0,0,255)", b, g, r)
}
