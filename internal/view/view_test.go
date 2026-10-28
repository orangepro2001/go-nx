package view

import (
	"bytes"
	"math"
	"testing"
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
