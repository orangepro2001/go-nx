//go:build windows

package capture

import (
	"testing"

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
