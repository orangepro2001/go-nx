package gfx

import (
	"image"
	"image/color"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

// rowOf reads row y of img as grey levels (the red channel).
func rowOf(img *ebiten.Image, y int) []int {
	b := img.Bounds()
	buf := make([]byte, 4*b.Dx())
	img.SubImage(image.Rect(b.Min.X, y, b.Max.X, y+1)).(*ebiten.Image).ReadPixels(buf)
	out := make([]int, b.Dx())
	for i := range out {
		out[i] = int(buf[4*i])
	}
	return out
}

// ramp counts the pixels of a row that are neither the dark nor the light side of an edge.
func ramp(row []int) int {
	n := 0
	for _, v := range row {
		if v > 12 && v < 243 {
			n++
		}
	}
	return n
}

// A black-to-white edge enlarged 3.5 times: the sharp filter keeps it narrower than
// bilinear does, never overshoots (no halo), and leaves the flat parts exactly as they were.
func TestSharpEnlargingKeepsEdgesWithoutHalos(t *testing.T) {
	gpu(t, func(*ebiten.Image) {
		s, err := NewScaler()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		src := ebiten.NewImage(8, 4)
		src.Fill(color.Black)
		src.SubImage(image.Rect(4, 0, 8, 4)).(*ebiten.Image).Fill(color.White)
		draw := func(f ScaleFilter) []int {
			dst := ebiten.NewImage(40, 20)
			dst.Fill(color.RGBA{255, 0, 255, 255}) // anything not drawn over shows up
			s.Draw(dst, src, 2, 2, 3.5, image.Rect(0, 0, 40, 20), f, 1)
			return rowOf(dst, 8)
		}
		sharp, smooth := draw(ScaleSharp), draw(ScaleSmooth)
		// Steeper than bilinear, but still a ramp: a hard step is what nearest neighbour
		// gives, and at 3.5 times it makes some pixels of the source 3 wide and some 4.
		if ramp(sharp) >= ramp(smooth) || ramp(sharp) == 0 {
			t.Errorf("the sharp edge is %d pixels wide, bilinear's %d: %v / %v", ramp(sharp), ramp(smooth), sharp, smooth)
		}
		for x := 3; x < 9; x++ {
			if sharp[x] != 0 {
				t.Errorf("x=%d inside the black part is %d", x, sharp[x])
			}
		}
		for x := 24; x < 30; x++ {
			if sharp[x] != 255 {
				t.Errorf("x=%d inside the white part is %d", x, sharp[x])
			}
		}
		for x := 3; x < 29; x++ {
			if sharp[x+1] < sharp[x] {
				t.Errorf("the edge rings: %v", sharp)
				break
			}
		}
	})
}

// A one-pixel checkerboard shrunk to a quarter averages to grey everywhere, where picking
// pixels would leave black and white blotches.
func TestSharpShrinkingAverages(t *testing.T) {
	gpu(t, func(*ebiten.Image) {
		s, err := NewScaler()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		const n = 64
		pix := make([]byte, 4*n*n)
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				v := byte(0)
				if (x+y)%2 == 1 {
					v = 255
				}
				i := 4 * (y*n + x)
				pix[i], pix[i+1], pix[i+2], pix[i+3] = v, v, v, 255
			}
		}
		src := ebiten.NewImage(n, n)
		src.WritePixels(pix)
		dst := ebiten.NewImage(16, 16)
		s.Draw(dst, src, 0, 0, 0.25, dst.Bounds(), ScaleSharp, 1)
		for y := 1; y < 15; y++ {
			for x, v := range rowOf(dst, y)[1:15] {
				if v < 107 || v > 148 {
					t.Fatalf("pixel (%d,%d) is %d, want about 128", x+1, y, v)
				}
			}
		}
	})
}

// Only the part inside the clip is drawn, on any backend, and dimming darkens it.
func TestSharpScalingKeepsToTheClipAndDims(t *testing.T) {
	gpu(t, func(*ebiten.Image) {
		s, err := NewScaler()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		src := ebiten.NewImage(10, 10)
		src.Fill(color.White)
		dst := ebiten.NewImage(60, 60)
		s.Draw(dst, src, 5, 5, 4, image.Rect(20, 20, 40, 40), ScaleSharp, 0.5)
		if c := pixel(dst, 10, 10); c.A != 0 {
			t.Errorf("drawn outside the clip: %v", c)
		}
		if c := pixel(dst, 30, 30); c.R < 120 || c.R > 135 || c.A != 255 {
			t.Errorf("inside the clip, dimmed to half: %v", c)
		}
		if c := pixel(dst, 41, 30); c.A != 0 {
			t.Errorf("drawn right of the clip: %v", c)
		}
	})
}
