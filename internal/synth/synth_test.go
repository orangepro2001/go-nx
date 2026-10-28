package synth

import (
	"bytes"
	"sync"
	"testing"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
)

// changedFraction is the share of tiles that differ from the previous frame.
type tracker struct {
	d   tile.Differ
	n   int
	buf *tile.Frame
	src *Source
}

func newTracker(t *testing.T, scene Scene, w, h int) *tracker {
	t.Helper()
	s, err := NewScene(scene, w, h)
	if err != nil {
		t.Fatal(err)
	}
	tr := &tracker{src: s, buf: tile.NewFrame(w, h)}
	tr.n = tr.buf.Cols() * tr.buf.Rows()
	return tr
}

// step grabs the next frame and reports how many tiles changed since the last.
func (tr *tracker) step(t *testing.T) int {
	t.Helper()
	if err := tr.src.Grab(tr.buf); err != nil {
		t.Fatal(err)
	}
	return len(tr.d.Diff(tr.buf))
}

func TestSizesAreChecked(t *testing.T) {
	if _, err := New(64, 64); err == nil {
		t.Fatal("a frame too small for the pattern was accepted")
	}
	if _, err := New(MinWidth, MinHeight); err != nil {
		t.Fatalf("the minimum size is refused: %v", err)
	}
	if _, err := New(MinWidth-1, MinHeight); err == nil {
		t.Error("one pixel too narrow was accepted")
	}
	if _, err := New(MinWidth, MinHeight-1); err == nil {
		t.Error("one pixel too short was accepted")
	}
	if _, err := New(proto.MaxFrameDim+1, 1080); err == nil {
		t.Error("a frame wider than the protocol carries was accepted")
	}
	for _, sz := range [][2]int{{1920, 1080}, {3840, 2160}} {
		if _, err := New(sz[0], sz[1]); err != nil {
			t.Errorf("%dx%d refused: %v", sz[0], sz[1], err)
		}
	}
	s, _ := New(320, 240)
	if err := s.Grab(tile.NewFrame(320, 256)); err == nil {
		t.Error("Grab accepted a frame of another size")
	}
}

// Two sources of the same kind and size show exactly the same pictures, in the
// same order, whenever and wherever they run: results of runs can be compared.
func TestEverySceneIsDeterministic(t *testing.T) {
	for _, sc := range Scenes() {
		t.Run(sc.String(), func(t *testing.T) {
			a, _ := NewScene(sc, 640, 480)
			b, _ := NewScene(sc, 640, 480)
			fa, fb := tile.NewFrame(640, 480), tile.NewFrame(640, 480)
			for i := 0; i < 40; i++ {
				if err := a.Grab(fa); err != nil {
					t.Fatal(err)
				}
				if err := b.Grab(fb); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(fa.Pix, fb.Pix) {
					t.Fatalf("frame %d differs between two sources", i+1)
				}
			}
			if a.Frames() != 40 {
				t.Errorf("Frames() = %d, want 40", a.Frames())
			}
		})
	}
}

// BGRA, with the fourth byte opaque so that a viewer that shows it as alpha shows
// the picture; and the four corners are distinct colours, so that a flipped,
// mirrored, transposed or red/blue-swapped picture is recognisable.
func TestEveryFrameHasTheOrientationMarkers(t *testing.T) {
	const w, h = 640, 480
	px := func(f *tile.Frame, x, y int) [4]byte {
		o := (y*f.W + x) * 4
		return [4]byte{f.Pix[o], f.Pix[o+1], f.Pix[o+2], f.Pix[o+3]}
	}
	for _, sc := range Scenes() {
		t.Run(sc.String(), func(t *testing.T) {
			s, _ := NewScene(sc, w, h)
			f := tile.NewFrame(w, h)
			for i := 0; i < 5; i++ {
				if err := s.Grab(f); err != nil {
					t.Fatal(err)
				}
				// B, G, R, opaque.
				for _, c := range []struct {
					name string
					x, y int
					want [4]byte
				}{
					{"top-left is red", 4, 4, [4]byte{0, 0, 255, 255}},
					{"top-right is green", w - 5, 4, [4]byte{0, 255, 0, 255}},
					{"bottom-left is blue", 4, h - 5, [4]byte{255, 0, 0, 255}},
					{"bottom-right is yellow", w - 5, h - 5, [4]byte{0, 255, 255, 255}},
				} {
					if got := px(f, c.x, c.y); got != c.want {
						t.Fatalf("frame %d: %s: B,G,R,A = %v, want %v", i+1, c.name, got, c.want)
					}
				}
				for p := 3; p < len(f.Pix); p += 4 {
					if f.Pix[p] != 0xff {
						t.Fatalf("frame %d: pixel %d is not opaque", i+1, p/4)
					}
				}
			}
		})
	}
}

func TestStaticNeverChangesAfterTheFirstFrame(t *testing.T) {
	tr := newTracker(t, SceneStatic, 640, 480)
	if got := tr.step(t); got != tr.n {
		t.Fatalf("the first frame reports %d changed tiles, want all %d", got, tr.n)
	}
	for i := 0; i < 30; i++ {
		if got := tr.step(t); got != 0 {
			t.Fatalf("frame %d: %d tiles changed in a static picture", i+2, got)
		}
	}
}

// Blocks is the original pattern: a moving block and a growing bar change a few
// tiles while the rest stays still.
func TestBlocksChangeAFewTiles(t *testing.T) {
	tr := newTracker(t, SceneBlocks, 1280, 720)
	tr.step(t)
	for i := 0; i < 30; i++ {
		got := tr.step(t)
		if got == 0 || got*100 > tr.n*15 { // some change, but well under a sixth of the picture
			t.Fatalf("frame %d: %d of %d tiles changed", i+2, got, tr.n)
		}
	}
}

// A terminal is quiet between lines (only the cursor blinks) and moves the whole
// text area when a line is printed.
func TestTerminalIsQuietBetweenLinesAndBusyOnScroll(t *testing.T) {
	tr := newTracker(t, SceneTerminal, 1280, 720)
	tr.step(t)
	quiet, busy := 0, 0
	for i := 0; i < 4*TerminalFramesPerLine; i++ {
		got := tr.step(t)
		switch {
		case got*40 <= tr.n: // at most 2.5% of the tiles
			quiet++
		case got*2 >= tr.n: // at least half
			busy++
		default:
			t.Fatalf("frame %d: %d of %d tiles changed, neither quiet nor a scroll", i+2, got, tr.n)
		}
	}
	if busy < 3 || quiet < 3*(TerminalFramesPerLine-1)-1 {
		t.Fatalf("%d busy and %d quiet frames over 4 lines", busy, quiet)
	}
}

// Scrolling a document changes the text area on every frame; the margins stay.
func TestDocumentScrollChangesMostOfThePicture(t *testing.T) {
	tr := newTracker(t, SceneDocScroll, 1280, 720)
	tr.step(t)
	for i := 0; i < 20; i++ {
		got := tr.step(t)
		if got*2 < tr.n || got == tr.n { // the text column is not the whole width, and the bars stay put
			t.Fatalf("frame %d: %d of %d tiles changed, want over half but not all (the bars and gutters are fixed)", i+2, got, tr.n)
		}
	}
}

// Video is the worst case: everything changes every frame.
func TestVideoChangesEverythingEveryFrame(t *testing.T) {
	tr := newTracker(t, SceneVideo, 1280, 720)
	tr.step(t)
	for i := 0; i < 10; i++ {
		got := tr.step(t)
		if got*100 < tr.n*90 {
			t.Fatalf("frame %d: %d of %d tiles changed, want at least 90%%", i+2, got, tr.n)
		}
	}
}

func TestParseScene(t *testing.T) {
	for _, sc := range Scenes() {
		got, err := ParseScene(sc.String())
		if err != nil || got != sc {
			t.Errorf("ParseScene(%q) = %v, %v", sc.String(), got, err)
		}
	}
	if got, err := ParseScene(""); err != nil || got != SceneBlocks {
		t.Errorf("ParseScene(\"\") = %v, %v, want the default scene", got, err)
	}
	if _, err := ParseScene("screensaver"); err == nil {
		t.Error("an unknown scene name was accepted")
	}
	if got := Scene(99).String(); got == "" {
		t.Error("an unknown scene has no name at all")
	}
	if _, err := NewScene(Scene(99), 640, 480); err == nil {
		t.Error("an unknown scene was accepted")
	}
}

func TestGrabMayBeCalledFromSeveralGoroutines(t *testing.T) {
	s, _ := New(640, 480)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := tile.NewFrame(640, 480)
			for i := 0; i < 20; i++ {
				if err := s.Grab(f); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if s.Frames() != 80 {
		t.Fatalf("Frames() = %d, want 80", s.Frames())
	}
}

// A viewer's picture can be checked against the markers: a swapped red and blue,
// a flipped or mirrored picture, or tiles in the wrong place all show.
func TestVerifyMarkers(t *testing.T) {
	const w, h = 640, 480
	good := func() *tile.Frame {
		s, _ := New(w, h)
		f := tile.NewFrame(w, h)
		if err := s.Grab(f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	if err := VerifyMarkers(good()); err != nil {
		t.Fatalf("a correct picture is refused: %v", err)
	}
	for name, damage := range map[string]func(f *tile.Frame){
		"red and blue swapped": func(f *tile.Frame) {
			for i := 0; i < len(f.Pix); i += 4 {
				f.Pix[i], f.Pix[i+2] = f.Pix[i+2], f.Pix[i]
			}
		},
		"flipped upside down": func(f *tile.Frame) {
			row := make([]byte, w*4)
			for y := 0; y < h/2; y++ {
				a, b := f.Pix[y*w*4:(y+1)*w*4], f.Pix[(h-1-y)*w*4:(h-y)*w*4]
				copy(row, a)
				copy(a, b)
				copy(b, row)
			}
		},
		"mirrored": func(f *tile.Frame) {
			for y := 0; y < h; y++ {
				for x := 0; x < w/2; x++ {
					a, b := (y*w+x)*4, (y*w+w-1-x)*4
					for k := 0; k < 4; k++ {
						f.Pix[a+k], f.Pix[b+k] = f.Pix[b+k], f.Pix[a+k]
					}
				}
			}
		},
		"one corner painted over": func(f *tile.Frame) {
			for i := 0; i < 12*w*4; i++ { // the top 12 rows, which hold the two top markers
				f.Pix[i] = 0
			}
		},
	} {
		f := good()
		damage(f)
		if err := VerifyMarkers(f); err == nil {
			t.Errorf("%s: not noticed", name)
		}
	}
	if err := VerifyMarkers(tile.NewFrame(8, 8)); err == nil {
		t.Error("a frame too small to hold the markers was accepted")
	}
}
