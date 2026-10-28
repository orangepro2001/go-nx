package view

import (
	"bytes"
	"math"
	"testing"
	"time"
)

func TestFitKeepsTheAspectRatioAndCentres(t *testing.T) {
	for _, c := range []struct {
		ww, wh, fw, fh int
		wantScale      float64
		wantOX, wantOY float64
	}{
		{1920, 1080, 1920, 1080, 1, 0, 0},
		{960, 540, 1920, 1080, 0.5, 0, 0},
		{1920, 1200, 1920, 1080, 1, 0, 60},                                        // letterbox: the window is taller
		{1600, 1080, 1920, 1080, 1600.0 / 1920, 0, (1080 - 1080*1600.0/1920) / 2}, // pillar: narrower
		{3840, 2160, 1920, 1080, 2, 0, 0},
	} {
		s, ox, oy := Fit(c.ww, c.wh, c.fw, c.fh)
		if math.Abs(s-c.wantScale) > 1e-9 || math.Abs(ox-c.wantOX) > 1e-9 || math.Abs(oy-c.wantOY) > 1e-9 {
			t.Errorf("Fit(%d,%d,%d,%d) = %v,%v,%v want %v,%v,%v", c.ww, c.wh, c.fw, c.fh, s, ox, oy, c.wantScale, c.wantOX, c.wantOY)
		}
	}
}

// Showing a frame larger than it is: every host pixel covers one or more screen
// pixels, so the pixel under the middle of its picture maps back to itself exactly.
func TestToHostIsExactWhenTheFrameIsEnlarged(t *testing.T) {
	const vpX, vpY, vpW, vpH, fw, fh = 0, 40, 3840, 2200, 1920, 1080 // 2x, letterboxed by 20 px
	scale, ox, oy := Fit(vpW, vpH, fw, fh)
	if scale < 1 {
		t.Fatalf("test setup: scale %v is not an enlargement", scale)
	}
	for _, p := range [][2]int{{0, 0}, {1, 1}, {959, 539}, {1919, 1079}, {1000, 3}} {
		cx := int(float64(vpX) + ox + (float64(p[0])+0.5)*scale)
		cy := int(float64(vpY) + oy + (float64(p[1])+0.5)*scale)
		x, y, inside := ToHost(cx, cy, vpX, vpY, vpW, vpH, fw, fh)
		if !inside || int(x) != p[0] || int(y) != p[1] {
			t.Errorf("host pixel %v shown at %d,%d came back as %d,%d inside=%v", p, cx, cy, x, y, inside)
		}
	}
}

// Showing it smaller, one screen pixel covers more than one host pixel, so the
// way back can only be as good as that: within the host pixels one screen pixel spans.
func TestToHostIsWithinOneScreenPixelWhenTheFrameIsShrunk(t *testing.T) {
	const vpX, vpY, vpW, vpH, fw, fh = 0, 40, 1440, 900, 1920, 1080
	scale, ox, oy := Fit(vpW, vpH, fw, fh)
	tol := int(1/scale) + 1
	for _, p := range [][2]int{{0, 0}, {959, 539}, {1919, 1079}, {1000, 3}, {1500, 800}} {
		cx := int(float64(vpX) + ox + (float64(p[0])+0.5)*scale)
		cy := int(float64(vpY) + oy + (float64(p[1])+0.5)*scale)
		x, y, inside := ToHost(cx, cy, vpX, vpY, vpW, vpH, fw, fh)
		if !inside || abs(int(x)-p[0]) > tol || abs(int(y)-p[1]) > tol {
			t.Errorf("host pixel %v shown at %d,%d came back as %d,%d (tolerance %d)", p, cx, cy, x, y, tol)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Outside the viewport the position is clamped to the picture so a drag that left
// the window ends at its edge instead of nowhere; inside tells the caller it left.
func TestToHostClampsOutsideAndSaysSo(t *testing.T) {
	const vpX, vpY, vpW, vpH, fw, fh = 0, 40, 800, 600, 800, 600
	cases := []struct {
		cx, cy       int
		wantX, wantY int32
		wantInside   bool
	}{
		{400, 340, 400, 300, true},
		{-50, 340, 0, 300, false},
		{5000, 340, 799, 300, false},
		{400, 0, 400, 0, false}, // over the tab bar
		{400, 9999, 400, 599, false},
		{800, 640, 799, 599, false}, // exactly one past the corner
	}
	for _, c := range cases {
		x, y, inside := ToHost(c.cx, c.cy, vpX, vpY, vpW, vpH, fw, fh)
		if x != c.wantX || y != c.wantY || inside != c.wantInside {
			t.Errorf("ToHost(%d,%d) = %d,%d inside=%v want %d,%d inside=%v", c.cx, c.cy, x, y, inside, c.wantX, c.wantY, c.wantInside)
		}
	}
}

func TestOrderKeyEventsPutsModifiersAroundOtherKeys(t *testing.T) {
	const ctrl, shift, alt, a, b, win = 0xA2, 0xA0, 0xA4, 0x41, 0x42, 0x5B
	in := []KeyEvent{
		{a, true}, {shift, true}, {b, false}, {ctrl, true}, {alt, false}, {win, true}, {0x43, false},
	}
	want := []KeyEvent{
		{Code: 0x42, Down: false}, {Code: 0x43, Down: false}, // non-modifier releases, by code
		{Code: 0x5B, Down: true}, {Code: 0xA0, Down: true}, {Code: 0xA2, Down: true}, // modifier presses, by code
		{Code: 0x41, Down: true},  // non-modifier presses
		{Code: 0xA4, Down: false}, // modifier releases last
	}
	OrderKeyEvents(in)
	if len(in) != len(want) {
		t.Fatalf("lost events: %v", in)
	}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v (all: %+v)", i, in[i], want[i], in)
		}
	}
}

func TestOrderKeyEventsIsIndependentOfTheInputOrder(t *testing.T) {
	base := []KeyEvent{{0x41, true}, {0xA0, true}, {0xA2, true}, {0x42, false}, {0xA4, false}, {0x5B, true}}
	var first []KeyEvent
	for shift := 0; shift < len(base); shift++ {
		ev := append(append([]KeyEvent(nil), base[shift:]...), base[:shift]...)
		OrderKeyEvents(ev)
		if first == nil {
			first = ev
			continue
		}
		for i := range ev {
			if ev[i] != first[i] {
				t.Fatalf("rotation %d gives a different order: %+v vs %+v", shift, ev, first)
			}
		}
	}
}

func TestReleaseOrderLetsGoOfModifiersLast(t *testing.T) {
	got := ReleaseOrder([]uint16{0xA2, 0x41, 0xA0, 0x42, 0x15B})
	want := []uint16{0x41, 0x42, 0xA0, 0xA2, 0x15B}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReleaseOrder = %x, want %x", got, want)
		}
	}
}

func TestIsModifierIgnoresTheExtendedBit(t *testing.T) {
	for _, c := range []struct {
		code uint16
		want bool
	}{{0xA0, true}, {0xA5, true}, {0x5B, true}, {0x15C, true}, {0x10, true}, {0x41, false}, {0x70, false}, {0x10D, false}} {
		if IsModifier(c.code) != c.want {
			t.Errorf("IsModifier(%#x) = %v", c.code, !c.want)
		}
	}
}

// The pixel contract of the viewer: frames arrive as B,G,R,X and are shown as
// R,G,B,255. The fourth byte of a frame is not defined by the protocol, so
// whatever it holds (a capture that leaves it at 0, one that fills it with
// garbage) must not make the picture transparent.
func TestBGRAToRGBA(t *testing.T) {
	bgra := []byte{
		0, 0, 255, 0, // red, with an alpha of 0
		0, 255, 0, 17, // green
		255, 0, 0, 255, // blue
		0, 128, 255, 99, // orange
		10, 20, 30, 40, // asymmetric: B=10 G=20 R=30
	}
	want := []byte{
		255, 0, 0, 255,
		0, 255, 0, 255,
		0, 0, 255, 255,
		255, 128, 0, 255,
		30, 20, 10, 255,
	}
	got := make([]byte, len(bgra))
	BGRAToRGBA(got, bgra)
	if !bytes.Equal(got, want) {
		t.Fatalf("BGRAToRGBA = %v, want %v", got, want)
	}
	// Converting in place is what a caller that owns one buffer would do.
	buf := append([]byte(nil), bgra...)
	BGRAToRGBA(buf, buf)
	if !bytes.Equal(buf, want) {
		t.Fatalf("in place: %v, want %v", buf, want)
	}
	// Converting twice is not the identity: a caller must convert exactly once.
	BGRAToRGBA(buf, buf)
	if bytes.Equal(buf, want) {
		t.Fatal("two conversions gave the same result as one")
	}
}

// A buffer whose length is not a whole number of pixels converts the whole pixels
// and leaves the tail alone instead of reading or writing out of range.
func TestBGRAToRGBAIgnoresAPartialPixel(t *testing.T) {
	src := []byte{1, 2, 3, 4, 5, 6, 7}
	dst := make([]byte, len(src))
	BGRAToRGBA(dst, src)
	if want := []byte{3, 2, 1, 255, 0, 0, 0}; !bytes.Equal(dst, want) {
		t.Fatalf("got %v, want %v", dst, want)
	}
}

// Every pixel of a larger picture, in the way the viewer converts a tile: row by
// row out of a full frame.
func TestBGRAToRGBAOfAWholeSyntheticPicture(t *testing.T) {
	const w, h = 64, 48
	src := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 4
			src[o], src[o+1], src[o+2], src[o+3] = byte(x), byte(y), byte(x+y), byte(x*y)
		}
	}
	dst := make([]byte, len(src))
	BGRAToRGBA(dst, src)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 4
			if dst[o] != byte(x+y) || dst[o+1] != byte(y) || dst[o+2] != byte(x) || dst[o+3] != 255 {
				t.Fatalf("pixel %d,%d = %v", x, y, dst[o:o+4])
			}
		}
	}
}

func TestPlaceFitCentresAndActualPans(t *testing.T) {
	// Fit is the old Fit.
	if p := Place(ModeFit, 800, 600, 1600, 900, 0, 0); p.Scale != 0.5 || p.OX != 0 || p.OY != 75 {
		t.Errorf("fit = %+v", p)
	}
	// Actual: smaller than the viewport on an axis is centred, whatever the pan.
	if p := Place(ModeActual, 800, 600, 400, 300, 50, 50); p != (Placement{1, 200, 150}) {
		t.Errorf("small picture = %+v", p)
	}
	// Larger: shifted by the pan, held to the picture's edges.
	if p := Place(ModeActual, 800, 600, 1920, 1080, 100, 30); p != (Placement{1, -100, -30}) {
		t.Errorf("panned = %+v", p)
	}
	if p := Place(ModeActual, 800, 600, 1920, 1080, 5000, -5); p != (Placement{1, -1120, 0}) {
		t.Errorf("pan held to the edges = %+v", p)
	}
	// One axis fits, the other does not.
	if p := Place(ModeActual, 800, 600, 700, 1080, 40, 100); p != (Placement{1, 50, -100}) {
		t.Errorf("mixed = %+v", p)
	}
	if x, y := MaxPan(800, 600, 1920, 1080); x != 1120 || y != 480 {
		t.Errorf("MaxPan = %v, %v", x, y)
	}
	if x, y := MaxPan(800, 600, 700, 500); x != 0 || y != 0 {
		t.Errorf("MaxPan of a picture that fits = %v, %v", x, y)
	}
}

// The mapping back to the host's pixel is the inverse of where the picture was drawn.
func TestToHostAtInvertsPlace(t *testing.T) {
	for _, mode := range []Mode{ModeFit, ModeActual} {
		for _, c := range []struct{ vw, vh, fw, fh int }{{800, 600, 1920, 1080}, {1000, 700, 640, 480}, {500, 500, 3000, 2000}} {
			for _, pan := range []float64{0, 137} {
				p := Place(mode, c.vw, c.vh, c.fw, c.fh, pan, pan/2)
				for _, hx := range []int{0, 1, c.fw / 3, c.fw - 2, c.fw - 1} {
					for _, hy := range []int{0, c.fh / 2, c.fh - 1} {
						// The centre of the host pixel, as drawn.
						sx, sy := p.OX+(float64(hx)+0.5)*p.Scale, p.OY+(float64(hy)+0.5)*p.Scale
						if sx < 0 || sy < 0 || sx >= float64(c.vw) || sy >= float64(c.vh) {
							continue // panned out of view: not on the screen to be pointed at
						}
						gx, gy := p.ToHostAt(sx, sy, c.fw, c.fh)
						if int(gx) != hx || int(gy) != hy {
							t.Errorf("mode %d %+v pan %v: host pixel (%d,%d) drawn at (%.1f,%.1f) maps back to (%d,%d)", mode, c, pan, hx, hy, sx, sy, gx, gy)
						}
					}
				}
			}
		}
	}
	// Outside the frame it is held to the edge, so a drag that leaves the window still ends there.
	p := Place(ModeFit, 800, 600, 1600, 900, 0, 0)
	if x, y := p.ToHostAt(-50, 9999, 1600, 900); x != 0 || y != 899 {
		t.Errorf("clamped = %d, %d", x, y)
	}
}

func TestEdgePan(t *testing.T) {
	dt := 100 * time.Millisecond
	// In the middle: nothing.
	if dx, dy := EdgePan(400, 300, 800, 600, 40, 1000, dt, true, true); dx != 0 || dy != 0 {
		t.Errorf("middle = %v, %v", dx, dy)
	}
	// At the left edge: full speed to the left; halfway through the margin: half.
	if dx, _ := EdgePan(0, 300, 800, 600, 40, 1000, dt, true, true); dx != -100 {
		t.Errorf("left edge dx = %v, want -100", dx)
	}
	if dx, _ := EdgePan(20, 300, 800, 600, 40, 1000, dt, true, true); dx != -50 {
		t.Errorf("half way into the margin dx = %v, want -50", dx)
	}
	if _, dy := EdgePan(400, 600, 800, 600, 40, 1000, dt, true, true); dy != 100 {
		t.Errorf("bottom edge dy = %v, want 100", dy)
	}
	// An axis the picture does not extend past is not panned.
	if dx, dy := EdgePan(0, 600, 800, 600, 40, 1000, dt, false, true); dx != 0 || dy != 100 {
		t.Errorf("only y = %v, %v", dx, dy)
	}
	// A pointer outside the viewport is held to full speed, not beyond it.
	if dx, _ := EdgePan(-500, 300, 800, 600, 40, 1000, dt, true, true); dx != -100 {
		t.Errorf("far outside dx = %v", dx)
	}
	// No time, no pan.
	if dx, dy := EdgePan(0, 0, 800, 600, 40, 1000, 0, true, true); dx != 0 || dy != 0 {
		t.Errorf("dt 0 = %v, %v", dx, dy)
	}
}
