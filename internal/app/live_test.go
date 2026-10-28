package app

import (
	"context"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
)

// pictureConn is a connection that shows a picture the test hands it.
type pictureConn struct {
	mu      sync.Mutex
	onFrame func(*tile.Frame, []tile.Rect)
	ready   chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newPictureConn() *pictureConn {
	return &pictureConn{ready: make(chan struct{}), done: make(chan struct{})}
}

func (c *pictureConn) Run(ctx context.Context, onFrame func(*tile.Frame, []tile.Rect), _ func(uint64, byte)) error {
	c.mu.Lock()
	c.onFrame = onFrame
	c.mu.Unlock()
	close(c.ready)
	<-c.done
	return nil
}
func (c *pictureConn) SendInput(proto.Input) error { return nil }
func (c *pictureConn) Close()                      { c.once.Do(func() { close(c.done) }) }

func (c *pictureConn) show(f *tile.Frame, dirty []tile.Rect) {
	<-c.ready
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onFrame(f, dirty)
}

// quadrants paints a frame with four colours, in the BGRA byte order frames travel in.
func quadrants(w, h int) *tile.Frame {
	f := tile.NewFrame(w, h)
	set := func(x0, y0, x1, y1 int, r, g, b byte) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				o := (y*w + x) * 4
				f.Pix[o], f.Pix[o+1], f.Pix[o+2], f.Pix[o+3] = b, g, r, 0 // alpha is undefined on the wire
			}
		}
	}
	set(0, 0, w/2, h/2, 255, 0, 0)     // top left: red
	set(w/2, 0, w, h/2, 0, 255, 0)     // top right: green
	set(0, h/2, w/2, h, 0, 0, 255)     // bottom left: blue
	set(w/2, h/2, w, h, 255, 255, 255) // bottom right: white
	return f
}

// The remote picture reaches the screen with its colours and its orientation right, and is
// fitted into the room the interface leaves it: the contract the host's pixels (BGRA, alpha
// undefined) and the viewer's must keep (docs/protocol.md).
func TestTheRemotePictureIsDrawnWithItsColoursAndOrientation(t *testing.T) {
	r := newRig(t)
	conn := newPictureConn()
	t.Cleanup(conn.Close)
	r.a.Resize(800, 600, 1)
	sess := newSession(conn, &r.a.nextOp, r.a.Invalidate)
	r.a.tabs = []*tab{{serial: 1, title: "Remote", addr: "10.0.0.1:7000", state: tabLive, sess: sess}}
	r.a.active = 1
	sess.setVisible(true)
	r.a.ui.Invalidate()
	r.step(kit.Input{})
	vp := r.a.viewportPx()
	if vp.W <= 0 || vp.H <= 0 || vp.Y < 30 {
		t.Fatalf("the viewport is %+v: it should be the window below the tab strip", vp)
	}

	conn.show(quadrants(200, 150), nil) // a picture a quarter of the viewport's area, same aspect
	var fitted [2]int
	onGPU(t, func(*ebiten.Image) {
		r.a.StepLive(func(w, h int) { fitted = [2]int{w, h} })
		screen := ebiten.NewImage(800, 600)
		r.a.drawLive(screen)
		px := func(x, y int) color.RGBA {
			var b [4]byte
			screen.SubImage(rectOf(x, y)).(*ebiten.Image).ReadPixels(b[:])
			return color.RGBA{b[0], b[1], b[2], b[3]}
		}
		qw, qh := vp.W/4, vp.H/4
		for _, c := range []struct {
			name string
			x, y int
			want color.RGBA
		}{
			{"top left", vp.X + qw, vp.Y + qh, color.RGBA{255, 0, 0, 255}},
			{"top right", vp.X + 3*qw, vp.Y + qh, color.RGBA{0, 255, 0, 255}},
			{"bottom left", vp.X + qw, vp.Y + 3*qh, color.RGBA{0, 0, 255, 255}},
			{"bottom right", vp.X + 3*qw, vp.Y + 3*qh, color.RGBA{255, 255, 255, 255}},
		} {
			if got := px(c.x, c.y); got != c.want {
				t.Errorf("%s of the picture is %v, want %v: the host's BGRA must arrive as the right colours, opaque, the right way up", c.name, got, c.want)
			}
		}
		if got := px(2, 2); got.A != 0 {
			t.Errorf("a pixel outside the viewport (the tab strip) was drawn on: %v", got)
		}
	})
	if fitted != [2]int{200, 150} {
		t.Errorf("the window was told to fit a %v picture, want 200x150", fitted)
	}

	// A change to one tile goes through as a patch, in the right place.
	f := quadrants(200, 150)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			o := (y*200 + x) * 4
			f.Pix[o], f.Pix[o+1], f.Pix[o+2] = 0, 255, 255 // BGRA: yellow
		}
	}
	conn.show(f, []tile.Rect{{X: 0, Y: 0, W: 64, H: 64}})
	onGPU(t, func(*ebiten.Image) {
		r.a.StepLive(func(int, int) {})
		screen := ebiten.NewImage(800, 600)
		r.a.drawLive(screen)
		var b [4]byte
		screen.SubImage(rectOf(vp.X+vp.W/16, vp.Y+vp.H/16)).(*ebiten.Image).ReadPixels(b[:])
		if (color.RGBA{b[0], b[1], b[2], b[3]}) != (color.RGBA{255, 255, 0, 255}) {
			t.Errorf("a patched tile is %v, want yellow", b)
		}
	})
	_ = time.Second
}

func rectOf(x, y int) image.Rectangle { return image.Rect(x, y, x+1, y+1) }
