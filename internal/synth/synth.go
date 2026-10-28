// Package synth renders deterministic, animated BGRA test pictures. They let the
// real host and viewer code run end to end, and be measured, on a machine where
// capturing the desktop is unsafe or unavailable. A Source never touches the
// desktop, and it shows the same pictures in the same order whenever and wherever
// it runs, so the results of two runs can be compared.
//
// The scenes imitate how much of a screen changes in common work (a small widget,
// a terminal, a scrolling document, video). They are not recordings: the numbers
// they produce are good for noticing that something got better or worse, not for
// predicting what a particular person's desktop will cost.
package synth

import (
	"errors"
	"fmt"
	"sync"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
)

// Scene is what a Source shows.
type Scene int

const (
	// SceneBlocks is mostly still, with a small moving block and a growing bar: a
	// few tiles change per frame. It is what New makes.
	SceneBlocks Scene = iota
	// SceneStatic never changes after its first picture.
	SceneStatic
	// SceneDocScroll scrolls a page of text smoothly: most of the picture changes
	// on every frame, but a toolbar and a status bar stay put.
	SceneDocScroll
	// SceneTerminal is a screen full of text that is quiet (only a cursor blinks)
	// and prints a line, moving all the text up, every TerminalFramesPerLine frames.
	SceneTerminal
	// SceneVideo changes every pixel on every frame: the worst case.
	SceneVideo
)

var sceneNames = [...]string{"blocks", "static", "docscroll", "terminal", "video"}

// Scenes lists every scene.
func Scenes() []Scene {
	return []Scene{SceneBlocks, SceneStatic, SceneDocScroll, SceneTerminal, SceneVideo}
}

func (s Scene) String() string {
	if s >= 0 && int(s) < len(sceneNames) {
		return sceneNames[s]
	}
	return fmt.Sprintf("scene(%d)", int(s))
}

// ParseScene is the inverse of String; the empty name is SceneBlocks.
func ParseScene(name string) (Scene, error) {
	if name == "" {
		return SceneBlocks, nil
	}
	for i, n := range sceneNames {
		if n == name {
			return Scene(i), nil
		}
	}
	return 0, fmt.Errorf("synth: no scene called %q (have %v)", name, sceneNames)
}

const (
	// DefaultWidth and DefaultHeight match a common 1080p desktop.
	DefaultWidth  = 1920
	DefaultHeight = 1080

	// MinWidth and MinHeight leave room for the moving block, margins and bar.
	MinWidth  = 256
	MinHeight = 240

	// TerminalFramesPerLine is how often SceneTerminal prints a line.
	TerminalFramesPerLine = 6
)

const (
	blockW, blockH = 192, 96
	barH           = 24
	border         = 2  // width of the amber frame around every picture
	markerSize     = 16 // the four coloured corner squares, inset by border
	margin         = 24 // keeps moving content clear of the border and markers
)

// Source produces the pictures of one Scene. It is safe for concurrent use.
type Source struct {
	W, H  int
	scene Scene

	mu    sync.Mutex
	frame uint64
	bg    []byte // the scene's background; never written after New
}

// New returns a SceneBlocks source of the given size.
func New(w, h int) (*Source, error) { return NewScene(SceneBlocks, w, h) }

// NewScene returns a source of the given scene and size. The size must fit the
// pattern and the protocol.
func NewScene(scene Scene, w, h int) (*Source, error) {
	if scene < 0 || int(scene) >= len(sceneNames) {
		return nil, fmt.Errorf("synth: unknown scene %d", int(scene))
	}
	if w < MinWidth || h < MinHeight {
		return nil, fmt.Errorf("synth: a %dx%d frame is too small for the test pattern (at least %dx%d)", w, h, MinWidth, MinHeight)
	}
	if !proto.ValidFrameSize(w, h) {
		return nil, fmt.Errorf("synth: a %dx%d frame is outside what the protocol carries", w, h)
	}
	s := &Source{W: w, H: h, scene: scene, bg: make([]byte, w*h*4)}
	s.paintBackground()
	return s, nil
}

// Size implements remote.Source.
func (s *Source) Size() (int, int) { return s.W, s.H }

// Frames reports how many pictures have been produced.
func (s *Source) Frames() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame
}

// Grab draws the next picture into dst, which the caller owns.
func (s *Source) Grab(dst *tile.Frame) error {
	if dst.W != s.W || dst.H != s.H {
		return errors.New("synth: destination frame size does not match the source")
	}
	s.mu.Lock()
	s.frame++
	step := s.frame
	s.mu.Unlock()

	copy(dst.Pix, s.bg)
	switch s.scene {
	case SceneBlocks:
		s.drawBlocks(dst, step)
	case SceneDocScroll:
		s.drawDocument(dst, step)
	case SceneTerminal:
		s.drawTerminal(dst, step)
	case SceneVideo:
		s.drawVideo(dst, step)
	}
	s.stamp(dst.Pix)
	return nil
}

// --- shared drawing ---

func (s *Source) put(pix []byte, x, y int, b, g, r byte) {
	o := (y*s.W + x) * 4
	pix[o], pix[o+1], pix[o+2], pix[o+3] = b, g, r, 0xff
}

func (s *Source) fill(pix []byte, x0, y0, x1, y1 int, b, g, r byte) {
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, s.W), min(y1, s.H)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			s.put(pix, x, y, b, g, r)
		}
	}
}

// stamp draws what orientation and colour checks rely on, on top of any scene:
// an amber frame, and a red, green, blue and yellow square in the top-left,
// top-right, bottom-left and bottom-right corners (as B,G,R: red is 0,0,255).
func (s *Source) stamp(pix []byte) {
	w, h := s.W, s.H
	s.fill(pix, 0, 0, w, border, 0x00, 0xc0, 0xff)
	s.fill(pix, 0, h-border, w, h, 0x00, 0xc0, 0xff)
	s.fill(pix, 0, 0, border, h, 0x00, 0xc0, 0xff)
	s.fill(pix, w-border, 0, w, h, 0x00, 0xc0, 0xff)
	m0, m1 := border, border+markerSize
	s.fill(pix, m0, m0, m1, m1, 0x00, 0x00, 0xff)         // top-left: red
	s.fill(pix, w-m1, m0, w-m0, m1, 0x00, 0xff, 0x00)     // top-right: green
	s.fill(pix, m0, h-m1, m1, h-m0, 0xff, 0x00, 0x00)     // bottom-left: blue
	s.fill(pix, w-m1, h-m1, w-m0, h-m0, 0x00, 0xff, 0xff) // bottom-right: yellow
}

func (s *Source) paintBackground() {
	w, h := s.W, s.H
	switch s.scene {
	case SceneTerminal:
		s.fill(s.bg, 0, 0, w, h, 14, 12, 12)
	case SceneDocScroll:
		s.fill(s.bg, 0, 0, w, h, 244, 246, 246)
		s.fill(s.bg, 0, 0, w, toolbarH(), 120, 70, 40)  // toolbar
		s.fill(s.bg, 0, h-toolbarH(), w, h, 90, 60, 50) // status bar
	default:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				s.put(s.bg, x, y, byte(20+y*60/h), byte(26+y*20/h), byte(34+x*40/w))
			}
		}
	}
	s.stamp(s.bg)
}

// toolbarH is the height of the fixed bars of SceneDocScroll: one tile row, so
// that whole tiles stay unchanged.
func toolbarH() int { return tile.Size }

// mix is a small integer hash; the scenes use it instead of a random source so
// that every run is the same.
func mix(a, b, c uint64) uint64 {
	h := a*0x9E3779B97F4A7C15 ^ b*0xC2B2AE3D27D4EB4F ^ c*0x165667B19E3779F9
	h ^= h >> 32
	h *= 0xD6E8FEB86659FD93
	h ^= h >> 32
	h *= 0xD6E8FEB86659FD93
	h ^= h >> 32
	return h
}

// --- SceneBlocks ---

func (s *Source) drawBlocks(dst *tile.Frame, step uint64) {
	w, h := s.W, s.H
	// The block's colour and position change every frame.
	bx := margin + int(step*16)%(w-2*margin-blockW+1)
	by := 64 + int(step*8)%(h-128-blockH+1)
	s.fill(dst.Pix, bx, by, bx+blockW, by+blockH, byte(step*9), byte(90+step*3), byte(200-step))
	// The bar grows and wraps, changing a couple of tiles.
	bw := int(step*24) % (w - 2*margin + 1)
	s.fill(dst.Pix, margin, h-barH-margin, margin+bw, h-margin, 0xff, 0xff, 0xff)
}

// --- text ---

// A glyph is a 6x12 bitmap in an 8-pixel-wide cell. There are glyphCount of them,
// drawn from a fixed pseudo-random pattern: text is a few shapes repeated, in two
// colours, which is what makes real text cheap to send.
const (
	glyphCount          = 64
	glyphW, glyphH      = 6, 12
	cellW               = 8
	termLineH, docLineH = 16, 20
)

var glyphs = func() (g [glyphCount][glyphH]uint8) {
	for i := range g {
		for row := 0; row < glyphH; row++ {
			var bits uint8
			for col := 0; col < glyphW; col++ {
				if mix(uint64(i), uint64(row), uint64(col))%100 < 38 {
					bits |= 1 << col
				}
			}
			g[i][row] = bits
		}
	}
	return
}()

// drawLine draws line number idx of the endless text between x0 and xEnd with its
// top at y, in cells of cellW, some of them blank, and only where the row is within
// [top, bottom). It returns the x just after the last cell.
func (s *Source) drawLine(pix []byte, idx int64, x0, xEnd, y, top, bottom int, b, g, r byte) int {
	x := x0
	for cell := 0; x+cellW <= xEnd; cell++ {
		if mix(uint64(idx), uint64(cell), 2)%7 != 0 { // a space every few cells, at places that depend on the line
			n := int(mix(uint64(idx), uint64(cell), 3) % glyphCount)
			for row := 0; row < glyphH; row++ {
				py := y + 2 + row
				if py < top || py >= bottom {
					continue
				}
				bits := glyphs[n][row]
				for col := 0; col < glyphW; col++ {
					if bits&(1<<col) != 0 {
						s.put(pix, x+1+col, py, b, g, r)
					}
				}
			}
		}
		x += cellW
	}
	return x
}

// --- SceneTerminal ---

func (s *Source) drawTerminal(dst *tile.Frame, step uint64) {
	top, bottom := margin, s.H-margin
	rows := (bottom - top) / termLineH
	printed := int64(step / TerminalFramesPerLine) // lines printed since the start
	xEnd := s.W - margin
	var endX, endY int
	for i := 0; i < rows; i++ {
		idx := printed - int64(rows-1-i) // the newest line is at the bottom
		y := top + i*termLineH
		endX = s.drawLine(dst.Pix, idx, margin, xEnd, y, 0, s.H, 0xc8, 0xc8, 0xc8)
		endY = y
	}
	// The cursor sits after the newest line and blinks every three frames.
	if (step/3)%2 == 0 {
		s.fill(dst.Pix, endX, endY+1, endX+cellW, endY+termLineH-1, 0xff, 0xff, 0xff)
	}
}

// --- SceneDocScroll ---

func (s *Source) drawDocument(dst *tile.Frame, step uint64) {
	top, bottom := toolbarH(), s.H-toolbarH()
	colW := min(900, s.W-200)
	x0 := (s.W - colW) / 2
	off := int(step) * 8 // pixels scrolled; the page is endless
	first := off / docLineH
	for l := first; ; l++ {
		y := top + l*docLineH - off
		if y >= bottom {
			break
		}
		if y+docLineH <= top {
			continue
		}
		// A blank line between paragraphs; the last line of a paragraph is short.
		kind := mix(uint64(l), 0, 9) % 9
		if kind == 0 {
			continue
		}
		xEnd := x0 + colW
		if kind == 1 {
			xEnd = x0 + colW*(1+int(mix(uint64(l), 0, 10)%3))/4
		}
		s.drawLine(dst.Pix, int64(l), x0, xEnd, y, top, bottom, 0x30, 0x30, 0x30)
	}
}

// --- SceneVideo ---

func (s *Source) drawVideo(dst *tile.Frame, step uint64) {
	w, h := s.W, s.H
	st := int(step)
	for y := 0; y < h; y++ {
		gy := byte(y*255/h + st*3)
		for x := 0; x < w; x++ {
			n := byte(mix(uint64(x), uint64(y), step) & 7) // sensor noise
			s.put(dst.Pix, x, y, byte(x*255/w+st*5)+n, gy+n, byte((x+y)*255/(w+h)+st*11)+n)
		}
	}
}

// VerifyMarkers checks that a picture received from a synthetic host still has
// its orientation markers (see stamp): the right colour in each corner, B,G,R
// order, the right way up and not mirrored. It does not look at the fourth byte,
// which the protocol leaves undefined.
func VerifyMarkers(f *tile.Frame) error {
	if f.W < 2*(border+markerSize) || f.H < 2*(border+markerSize) {
		return fmt.Errorf("synth: a %dx%d picture is too small to carry the markers", f.W, f.H)
	}
	in := border + markerSize/2 // the middle of a marker
	for _, m := range []struct {
		name    string
		x, y    int
		b, g, r byte
	}{
		{"top-left", in, in, 0x00, 0x00, 0xff},
		{"top-right", f.W - 1 - in, in, 0x00, 0xff, 0x00},
		{"bottom-left", in, f.H - 1 - in, 0xff, 0x00, 0x00},
		{"bottom-right", f.W - 1 - in, f.H - 1 - in, 0x00, 0xff, 0xff},
	} {
		o := (m.y*f.W + m.x) * 4
		if f.Pix[o] != m.b || f.Pix[o+1] != m.g || f.Pix[o+2] != m.r {
			return fmt.Errorf("synth: %s marker is B,G,R = %d,%d,%d, want %d,%d,%d (picture flipped, mirrored or colour order wrong?)",
				m.name, f.Pix[o], f.Pix[o+1], f.Pix[o+2], m.b, m.g, m.r)
		}
	}
	return nil
}
