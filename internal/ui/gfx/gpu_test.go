package gfx

import (
	"image"
	"image/color"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
)

// The tests that draw need a window and a GPU (or Mesa's software one). Ebiten wants its
// game loop on the main thread, so TestMain runs the tests on another goroutine and hands
// them a way to run a function in the loop: gpu(t, f).
//
// With no display (a bare shell on Linux) they are skipped, not failed; CI runs them under
// xvfb on Linux and on the Windows runner's desktop.

var (
	jobs     = make(chan func(*ebiten.Image))
	haveGPU  = true
	gpuStart sync.Once
)

type loop struct{}

// stop is closed when the tests are over: the loop then ends by itself, so that the process
// exits from the main thread after Ebiten has torn its window down. (Exiting from the test
// goroutine while the main thread was inside the GL driver crashed Mesa's software renderer
// about one run in eight, after every test had passed.)
var stop = make(chan struct{})

func (loop) Update() error {
	select {
	case <-stop:
		return ebiten.Termination
	default:
		return nil
	}
}
func (loop) Draw(screen *ebiten.Image) {
	for {
		select {
		case f := <-jobs:
			f(screen)
		default:
			return
		}
	}
}
func (loop) Layout(int, int) (int, int) { return 320, 240 }

func TestMain(m *testing.M) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		haveGPU = false
		os.Exit(m.Run())
	}
	codes := make(chan int, 1)
	go func() {
		codes <- m.Run()
		close(stop)
	}()
	ebiten.SetWindowSize(320, 240)
	err := ebiten.RunGame(loop{})
	code := <-codes
	if err != nil {
		os.Stderr.WriteString("ebiten: " + err.Error() + "\n")
		code = max(code, 3)
	}
	os.Exit(code)
}

// gpu runs f in the game loop, with the screen, and waits for it.
func gpu(t *testing.T, f func(screen *ebiten.Image)) {
	t.Helper()
	if !haveGPU {
		t.Skip("no display: run under xvfb-run")
	}
	done := make(chan struct{})
	select {
	case jobs <- func(s *ebiten.Image) { defer close(done); f(s) }:
	case <-time.After(20 * time.Second):
		t.Fatal("the game loop did not take the job")
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the job did not finish")
	}
}

// canvasFor makes a Canvas and an offscreen image of w by h device pixels, cleared.
func canvasFor(t *testing.T, eng *text.Engine, w, h int, scale float64) (*Canvas, *ebiten.Image) {
	t.Helper()
	c, err := New(eng)
	if err != nil {
		t.Fatal(err)
	}
	img := ebiten.NewImage(w, h)
	c.Begin(img, scale)
	return c, img
}

func pixel(img *ebiten.Image, x, y int) color.RGBA {
	var px [4]byte
	img.SubImage(image.Rect(x, y, x+1, y+1)).(*ebiten.Image).ReadPixels(px[:])
	return color.RGBA{px[0], px[1], px[2], px[3]}
}

func inkIn(img *ebiten.Image, r image.Rectangle) int {
	r = r.Intersect(img.Bounds())
	buf := make([]byte, 4*r.Dx()*r.Dy())
	img.SubImage(r).(*ebiten.Image).ReadPixels(buf)
	n := 0
	for i := 3; i < len(buf); i += 4 {
		if buf[i] > 40 {
			n++
		}
	}
	return n
}

func newEngine(t *testing.T, lang i18n.Lang) *text.Engine {
	t.Helper()
	if !haveGPU {
		t.Skip("no display: run under xvfb-run")
	}
	// No logger: the engine opens fonts in the background and may report one after the test
	// has finished, which the testing package turns into a panic.
	e, err := text.NewEngine(text.SystemLocator(), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.SetLanguage(lang)
	deadline := time.Now().Add(10 * time.Second)
	for !e.Ready() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(e.Close)
	return e
}

var red = kit.Color{R: 255, A: 255}

func TestRoundRectFillOutlineAndShadow(t *testing.T) {
	eng := newEngine(t, i18n.English)
	gpu(t, func(*ebiten.Image) {
		c, img := canvasFor(t, eng, 120, 80, 1)
		c.RoundRect(kit.Rect{X: 20, Y: 20, W: 80, H: 40}, 10, red)
		if p := pixel(img, 60, 40); p.R != 255 || p.A != 255 {
			t.Errorf("centre of a filled rectangle = %v", p)
		}
		if p := pixel(img, 20, 20); p.A > 40 {
			t.Errorf("the corner pixel of a radius-10 rectangle is covered: %v", p)
		}
		if p := pixel(img, 5, 5); p.A != 0 {
			t.Errorf("outside the rectangle = %v", p)
		}
		if p := pixel(img, 22, 40); p.A < 250 {
			t.Errorf("the left edge, 2 px in = %v: should be solid", p)
		}

		img.Clear()
		c.StrokeRoundRect(kit.Rect{X: 20, Y: 20, W: 80, H: 40}, 10, 2, red)
		if p := pixel(img, 60, 40); p.A != 0 {
			t.Errorf("the inside of an outline is covered: %v", p)
		}
		if p := pixel(img, 60, 20); p.A < 250 {
			t.Errorf("the top edge of the outline = %v", p)
		}
		if p := pixel(img, 60, 23); p.A != 0 {
			t.Errorf("3 px inside a 2 px outline = %v", p)
		}

		img.Clear()
		c.Shadow(kit.Rect{X: 40, Y: 25, W: 40, H: 20}, 6, 10, 4, kit.Color{A: 200})
		inside := pixel(img, 60, 39)
		near := pixel(img, 60, 52)
		far := pixel(img, 60, 72)
		if !(inside.A > near.A && near.A > far.A) || inside.A < 60 {
			t.Errorf("shadow alpha inside %d, near %d, far %d: it should fall off with distance", inside.A, near.A, far.A)
		}
		if p := pixel(img, 5, 5); p.A != 0 {
			t.Errorf("shadow reached the far corner: %v", p)
		}
	})
}

func TestScaleAndSnapping(t *testing.T) {
	eng := newEngine(t, i18n.English)
	gpu(t, func(*ebiten.Image) {
		c, img := canvasFor(t, eng, 200, 100, 2)
		c.FillRect(kit.Rect{X: 10, Y: 10, W: 20, H: 10}, red)
		// 1.5 scale: edges land on whole pixels, no half-covered row.
		if p := pixel(img, 20, 20); p.A != 255 {
			t.Errorf("inside = %v", p)
		}
		if p := pixel(img, 19, 20); p.A != 0 {
			t.Errorf("one pixel left of x = 10*2: %v", p)
		}
		if p := pixel(img, 59, 39); p.A != 255 {
			t.Errorf("the last pixel of a 20x10 logical rectangle at scale 2 = %v", p)
		}
		if p := pixel(img, 60, 39); p.A != 0 {
			t.Errorf("one past it = %v", p)
		}
		img.Clear()
		c.Begin(img, 1.5)
		c.FillRect(kit.Rect{X: 3, Y: 3, W: 7, H: 5}, red) // 4.5 → 5, 10.5 → 11 (rounds half up)
		for x := 0; x < 40; x++ {
			if a := pixel(img, x, 6).A; a != 0 && a != 255 {
				t.Errorf("pixel %d is half covered (%d): fills must be on whole pixels", x, a)
			}
		}
	})
}

// The shader is given positions in the coordinates of the whole image; drawing into a
// sub-image (a clipped region) must still put the shape where it belongs.
func TestClipLimitsEverythingDrawn(t *testing.T) {
	eng := newEngine(t, i18n.English)
	gpu(t, func(*ebiten.Image) {
		c, img := canvasFor(t, eng, 120, 80, 1)
		c.PushClip(kit.Rect{X: 40, Y: 0, W: 40, H: 80})
		c.RoundRect(kit.Rect{X: 10, Y: 10, W: 100, H: 60}, 8, red)
		c.FillRect(kit.Rect{X: 0, Y: 0, W: 120, H: 5}, red)
		c.Text("clipped text", 0, 30, text.Style{Size: 16}, red)
		c.PopClip()
		if p := pixel(img, 20, 40); p.A != 0 {
			t.Errorf("left of the clip = %v", p)
		}
		if p := pixel(img, 100, 40); p.A != 0 {
			t.Errorf("right of the clip = %v", p)
		}
		if p := pixel(img, 60, 40); p.A != 255 {
			t.Errorf("inside the clip = %v: the shape was drawn in the wrong place", p)
		}
		if p := pixel(img, 60, 2); p.A != 255 || pixel(img, 30, 2).A != 0 {
			t.Errorf("a clipped fill: inside %v outside %v", p, pixel(img, 30, 2))
		}
		if inkIn(img, image.Rect(0, 25, 40, 55)) != 0 {
			t.Error("text drawn outside the clip")
		}
		// After PopClip, drawing is unrestricted again.
		c.FillRect(kit.Rect{X: 0, Y: 70, W: 10, H: 10}, red)
		if p := pixel(img, 5, 75); p.A != 255 {
			t.Errorf("after PopClip = %v", p)
		}
	})
}

func TestLatinTextHasInkWhereItShouldBe(t *testing.T) {
	eng := newEngine(t, i18n.English)
	gpu(t, func(*ebiten.Image) {
		c, img := canvasFor(t, eng, 200, 60, 1)
		st := text.Style{Size: 16}
		c.Text("Connect", 10, 10, st, kit.Color{R: 255, G: 255, B: 255, A: 255})
		w := int(eng.Width("Connect", st))
		if n := inkIn(img, image.Rect(10, 10, 10+w+2, 10+int(eng.LineHeight(st))+2)); n < 40 {
			t.Errorf("only %d inked pixels for a word", n)
		}
		if n := inkIn(img, image.Rect(0, 30, 200, 60)); n != 0 {
			t.Errorf("%d inked pixels below the line", n)
		}
		if n := inkIn(img, image.Rect(10+w+6, 0, 200, 60)); n != 0 {
			t.Errorf("%d inked pixels right of the measured width", n)
		}
	})
}

func TestChineseAndKoreanTextIsDrawnNotBoxed(t *testing.T) {
	for _, tc := range []struct {
		lang i18n.Lang
		s    string
	}{{i18n.Chinese, "远程控制连接"}, {i18n.Korean, "원격 제어 연결"}} {
		eng := newEngine(t, tc.lang)
		if len(eng.Loaded()) < 2 {
			t.Skipf("no CJK font on this machine: %v", eng.Loaded())
		}
		gpu(t, func(*ebiten.Image) {
			c, img := canvasFor(t, eng, 240, 50, 1)
			st := text.Style{Size: 18}
			c.Text(tc.s, 8, 8, st, kit.Color{R: 255, G: 255, B: 255, A: 255})
			w := eng.Width(tc.s, st)
			if w < 18*float64(len([]rune(tc.s)))*0.5 {
				t.Errorf("%v: %q measures %v, too narrow for CJK", tc.lang, tc.s, w)
			}
			if n := inkIn(img, image.Rect(8, 8, 8+int(w)+2, 40)); n < 150 {
				t.Errorf("%v: %d inked pixels for %q", tc.lang, n, tc.s)
			}
		})
	}
}

func TestEveryIconDrawsAndStaysInItsBox(t *testing.T) {
	eng := newEngine(t, i18n.English)
	gpu(t, func(*ebiten.Image) {
		for id := kit.Icon(1); id < kit.NumIcons; id++ {
			for _, scale := range []float64{1, 1.5, 2} {
				c, img := canvasFor(t, eng, 60, 60, scale)
				c.Icon(id, kit.Rect{X: 10, Y: 10, W: 16, H: 16}, kit.Color{R: 255, G: 255, B: 255, A: 255})
				box := image.Rect(int(10*scale), int(10*scale), int(26*scale)+1, int(26*scale)+1)
				if n := inkIn(img, box); n < 12 {
					t.Errorf("icon %d at scale %v has %d inked pixels", id, scale, n)
				}
				outside := inkIn(img, img.Bounds()) - inkIn(img, box)
				if outside != 0 {
					t.Errorf("icon %d at scale %v drew %d pixels outside its box", id, scale, outside)
				}
			}
		}
	})
}

func TestKitPaintsThroughTheRealCanvas(t *testing.T) {
	eng := newEngine(t, i18n.English)
	gpu(t, func(*ebiten.Image) {
		ui := kit.NewUI(darkPalette(), i18n.New(i18n.English), eng)
		ui.SetSize(240, 80)
		ui.SetRoot(kit.Aligned{H: kit.AlignCenter, V: kit.AlignCenter, Child: &kit.Button{ID: "go", Label: "Connect", Kind: kit.Primary}})
		c, img := canvasFor(t, eng, 240, 80, 1)
		ui.Paint(c)
		// The primary button is the accent colour wherever the label is not, and the
		// label is drawn in the colour for text on an accent.
		pal := darkPalette()
		// (away from the label, whose width depends on the font the machine has)
		for _, pt := range [][2]int{{90, 26}, {150, 26}, {90, 54}, {150, 54}} {
			p := pixel(img, pt[0], pt[1])
			if p.A != 255 || p.R != pal.Accent.R || p.G != pal.Accent.G || p.B != pal.Accent.B {
				t.Errorf("pixel %v = %v, want the accent %v", pt, p, pal.Accent)
			}
		}
		if n := inkIn(img, image.Rect(60, 26, 180, 54)); n < 100 {
			t.Errorf("%d inked pixels in the button's middle", n)
		}
	})
}

// A shape must land in the right place on every image, not only on the first one made: images
// share a texture, and the shader is given positions on the texture (the first version of the
// shader forgot to take the image's origin off, and drew nothing on any image but the first).
func TestShapesLandInTheRightPlaceOnEveryImage(t *testing.T) {
	eng := newEngine(t, i18n.English)
	for i := 0; i < 12; i++ {
		gpu(t, func(*ebiten.Image) {
			// Several images alive at once, of different sizes, so that they cannot all sit at the origin.
			var keep []*ebiten.Image
			for j := 0; j <= i%4; j++ {
				keep = append(keep, ebiten.NewImage(37+j*11, 29+j*7))
			}
			c, img := canvasFor(t, eng, 120, 80, 1)
			c.RoundRect(kit.Rect{X: 10, Y: 10, W: 100, H: 60}, 8, red)
			c.StrokeRoundRect(kit.Rect{X: 2, Y: 2, W: 116, H: 76}, 8, 2, kit.Color{G: 255, A: 255})
			if p := pixel(img, 60, 40); p.R != 255 || p.A != 255 {
				t.Errorf("image %d: centre = %v", i, p)
			}
			if p := pixel(img, 108, 40); p.A < 250 {
				t.Errorf("image %d: 2 px inside the right edge = %v: the shape is not where it should be", i, p)
			}
			if p := pixel(img, 0, 0); p.A != 0 {
				t.Errorf("image %d: outside = %v", i, p)
			}
			if p := pixel(img, 60, 2); p.G < 250 {
				t.Errorf("image %d: the outline's top edge = %v", i, p)
			}
			_ = keep
		})
	}
}
