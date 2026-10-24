package tile

import (
	"bytes"
	"math/rand"
	"testing"
)

// 150x90 is not a multiple of 64, so edge tiles are exercised.
const tw, th = 150, 90

func fill(f *Frame, x0, y0, w, h int, c [4]byte) {
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			copy(f.Pix[(y*f.W+x)*bpp:], c[:])
		}
	}
}

func TestDiffUnchangedAndLocalChange(t *testing.T) {
	var d Differ
	f := NewFrame(tw, th)
	if got := len(d.Diff(f)); got != f.Cols()*f.Rows() {
		t.Fatalf("first diff = %d tiles, want all %d", got, f.Cols()*f.Rows())
	}
	if got := d.Diff(f); len(got) != 0 {
		t.Fatalf("unchanged frame reported %v", got)
	}
	f.Pix[(80*f.W+140)*bpp] = 9 // bottom-right edge tile (tx=2, ty=1)
	got := d.Diff(f)
	if len(got) != 1 || got[0] != 1*f.Cols()+2 {
		t.Fatalf("diff = %v, want [%d]", got, 1*f.Cols()+2)
	}
}

func TestRoundTripAllTileTypes(t *testing.T) {
	src := NewFrame(tw, th)
	fill(src, 0, 0, tw, th, [4]byte{1, 2, 3, 255})      // solid
	fill(src, 0, 0, 30, 30, [4]byte{200, 100, 50, 255}) // palette (2 colors)
	r := rand.New(rand.NewSource(1))
	r.Read(src.Pix[(0*src.W+64)*bpp : (0*src.W+128)*bpp]) // noisy row segment -> raw

	var d Differ
	idxs := d.Diff(src)
	data, err := Encode(src, idxs)
	if err != nil {
		t.Fatal(err)
	}
	dst := NewFrame(tw, th)
	rects, err := Decode(dst, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rects) != len(idxs) {
		t.Fatalf("got %d rects for %d tiles", len(rects), len(idxs))
	}
	if !bytes.Equal(src.Pix, dst.Pix) {
		t.Fatal("decoded frame differs from source")
	}
}

func TestIncrementalUpdate(t *testing.T) {
	src, dst := NewFrame(tw, th), NewFrame(tw, th)
	var d Differ
	sync := func() int {
		idxs := d.Diff(src)
		data, err := Encode(src, idxs)
		if err != nil {
			t.Fatal(err)
		}
		rects, err := Decode(dst, data)
		if err != nil {
			t.Fatal(err)
		}
		if len(rects) != len(idxs) {
			t.Fatalf("got %d rects for %d tiles", len(rects), len(idxs))
		}
		return len(idxs)
	}
	sync()
	fill(src, 70, 10, 20, 20, [4]byte{0, 0, 255, 255})
	if n := sync(); n != 1 {
		t.Fatalf("sent %d tiles, want 1", n)
	}
	if !bytes.Equal(src.Pix, dst.Pix) {
		t.Fatal("frames diverged")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	dst := NewFrame(tw, th)
	for _, bad := range [][]byte{
		{1, 2, 3},
		{0, 0, 0, 0, typeSolid, 9, 0, 0, 0, 1},
		{99, 0, 0, 0, typeSolid, 4, 0, 0, 0, 1, 2, 3, 4},
		{0, 0, 0, 0, 7, 0, 0, 0, 0},
	} {
		if _, err := Decode(dst, bad); err == nil {
			t.Errorf("Decode(%v) = nil, want error", bad)
		}
	}
}

func TestDecodeReportsClippedEdgeRect(t *testing.T) {
	src, dst := NewFrame(tw, th), NewFrame(tw, th)
	fill(src, 140, 80, 5, 5, [4]byte{9, 9, 9, 255})
	var d Differ
	d.Diff(NewFrame(tw, th))
	idxs := d.Diff(src)
	data, _ := Encode(src, idxs)
	rects, err := Decode(dst, data)
	if err != nil {
		t.Fatal(err)
	}
	// Bottom-right tile of a 150x90 frame starts at (128, 64) and is 22x26.
	if len(rects) != 1 || rects[0] != (Rect{128, 64, 22, 26}) {
		t.Fatalf("rects = %+v", rects)
	}
}
