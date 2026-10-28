// Package gfx paints a kit tree onto an Ebiten image: rounded rectangles, outlines and
// soft shadows from one signed-distance shader (antialiased at any size, no bitmaps),
// text from the font engine, and a vector icon set.
package gfx

import (
	"image"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	etext "github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
)

// shaderSrc draws a rounded rectangle three ways, chosen by the uniforms: filled, as an
// outline of a given width, or as a soft shadow. The distance d is negative inside the
// shape; the coverage of a pixel is a ramp across its edge. (This is the shader U0
// compared against the same formula in Go: docs/ui-baseline.md.)
const shaderSrc = `//kage:unit pixels
package main

var Center vec2
var Half vec2
var Radius float
var Width float
var Blur float
var Color vec4

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	// dst.xy is a position on the destination's texture, and an image is only a part of
	// its texture: the origin has to come off, or a shape lands wherever the image happens
	// to sit (the first image of a run is at the origin, so a test of one image misses it).
	p := dst.xy - imageDstOrigin() - Center
	q := abs(p) - (Half - vec2(Radius, Radius))
	d := length(max(q, vec2(0, 0))) + min(max(q.x, q.y), 0) - Radius
	a := clamp(0.5-d, 0, 1)
	if Blur > 0 {
		a = 1 - smoothstep(-Blur*0.5, Blur*0.5, d)
	} else if Width > 0 {
		a = a * clamp(d+Width+0.5, 0, 1)
	}
	return Color * a
}
`

// Canvas implements kit.Canvas on an Ebiten image. Create one with New, then call Begin
// each time before painting a frame.
type Canvas struct {
	eng   *text.Engine
	shade *ebiten.Shader
	px    *ebiten.Image // one white pixel, scaled to draw rectangles
	icons map[iconKey]*ebiten.Image

	root  *ebiten.Image
	cur   *ebiten.Image
	clips []image.Rectangle
	scale float64
}

// New compiles the shader. It needs a running game (Ebiten creates GPU resources
// lazily, so it can be called before the first frame, but not painted with before it).
func New(eng *text.Engine) (*Canvas, error) {
	sh, err := ebiten.NewShader([]byte(shaderSrc))
	if err != nil {
		return nil, err
	}
	px := ebiten.NewImage(1, 1)
	px.Fill(color.White)
	return &Canvas{eng: eng, shade: sh, px: px, icons: map[iconKey]*ebiten.Image{}, scale: 1}, nil
}

// Close releases the GPU resources: the shader, the pixel and the rasterised icons.
func (c *Canvas) Close() {
	c.shade.Deallocate()
	c.px.Deallocate()
	for _, img := range c.icons {
		img.Deallocate()
	}
	clear(c.icons)
}

// Begin sets the image to paint on and the device pixels per logical pixel.
func (c *Canvas) Begin(dst *ebiten.Image, scale float64) {
	if scale != c.scale {
		clear(c.icons) // rasterised at the old size
	}
	c.root, c.cur, c.scale = dst, dst, scale
	c.clips = c.clips[:0]
}

func snap(v float64) float64 { return math.Round(v) }

// device converts a logical rectangle to device pixels, with its edges on whole pixels so
// that fills and outlines are crisp.
func (c *Canvas) device(r kit.Rect) (x, y, w, h float64) {
	x0, y0 := snap(r.X*c.scale), snap(r.Y*c.scale)
	x1, y1 := snap((r.X+r.W)*c.scale), snap((r.Y+r.H)*c.scale)
	return x0, y0, math.Max(x1-x0, 0), math.Max(y1-y0, 0)
}

func (c *Canvas) FillRect(r kit.Rect, col kit.Color) {
	x, y, w, h := c.device(r)
	if w == 0 || h == 0 {
		return
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(w, h)
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(col)
	c.cur.DrawImage(c.px, op)
}

// clipRect is the part of the image that may be drawn on now, in the image's own pixels.
func (c *Canvas) clipRect() image.Rectangle {
	if n := len(c.clips); n > 0 {
		return c.clips[n-1]
	}
	return c.root.Bounds()
}

// shape draws the shader over the rectangle (x, y, w, h) grown by grow pixels, within the
// clip. It draws on the whole image and keeps to the clip by drawing only the part of the
// rectangle inside it, never on a sub-image: what a sub-image's origin is to a shader
// differs between Ebiten's backends (on DirectX it is the sub-image's, on OpenGL the whole
// image's), which put every clipped shape in the wrong place on Windows. Positions in the
// shader are those of the whole image, whatever backend.
func (c *Canvas) shape(x, y, w, h, radius, width, blur, grow float64, col kit.Color) {
	if w <= 0 || h <= 0 {
		return
	}
	radius = math.Min(radius, math.Min(w, h)/2)
	r := image.Rect(int(math.Floor(x-grow)), int(math.Floor(y-grow)), int(math.Ceil(x+w+grow)), int(math.Ceil(y+h+grow))).Intersect(c.clipRect())
	if r.Empty() {
		return
	}
	a := float32(col.A) / 255
	op := &ebiten.DrawRectShaderOptions{
		Uniforms: map[string]any{
			"Center": []float32{float32(x + w/2), float32(y + h/2)},
			"Half":   []float32{float32(w / 2), float32(h / 2)},
			"Radius": float32(radius),
			"Width":  float32(width),
			"Blur":   float32(blur),
			"Color":  []float32{float32(col.R) / 255 * a, float32(col.G) / 255 * a, float32(col.B) / 255 * a, a},
		},
	}
	op.GeoM.Translate(float64(r.Min.X), float64(r.Min.Y))
	c.root.DrawRectShader(r.Dx(), r.Dy(), c.shade, op)
}

func (c *Canvas) RoundRect(r kit.Rect, radius float64, col kit.Color) {
	x, y, w, h := c.device(r)
	c.shape(x, y, w, h, radius*c.scale, 0, 0, 1, col)
}

func (c *Canvas) StrokeRoundRect(r kit.Rect, radius, width float64, col kit.Color) {
	x, y, w, h := c.device(r)
	c.shape(x, y, w, h, radius*c.scale, math.Max(width*c.scale, 1), 0, 1, col)
}

func (c *Canvas) Shadow(r kit.Rect, radius, blur, dy float64, col kit.Color) {
	r.Y += dy
	x, y, w, h := c.device(r)
	b := blur * c.scale
	c.shape(x, y, w, h, radius*c.scale, 0, b, b, col)
}

func (c *Canvas) Text(s string, x, y float64, st text.Style, col kit.Color) {
	if s == "" {
		return
	}
	c.eng.EnsureFor(s)
	op := &etext.DrawOptions{}
	op.GeoM.Translate(snap(x*c.scale), snap(y*c.scale))
	op.ColorScale.ScaleWithColor(col)
	etext.Draw(c.cur, s, c.eng.Face(st), op)
}

func (c *Canvas) Icon(id kit.Icon, r kit.Rect, col kit.Color) {
	x, y, w, h := c.device(r)
	size := int(math.Min(w, h))
	if size < 2 {
		return
	}
	img := c.iconImage(id, size)
	if img == nil {
		return
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(x+(w-float64(size))/2, y+(h-float64(size))/2)
	op.ColorScale.ScaleWithColor(col)
	c.cur.DrawImage(img, op)
}

func (c *Canvas) PushClip(r kit.Rect) {
	x, y, w, h := c.device(r)
	rect := image.Rect(int(x), int(y), int(x+w), int(y+h))
	if n := len(c.clips); n > 0 {
		rect = rect.Intersect(c.clips[n-1])
	} else {
		rect = rect.Intersect(c.root.Bounds())
	}
	c.clips = append(c.clips, rect)
	c.cur = c.root.SubImage(rect).(*ebiten.Image)
}

func (c *Canvas) PopClip() {
	c.clips = c.clips[:len(c.clips)-1]
	if n := len(c.clips); n > 0 {
		c.cur = c.root.SubImage(c.clips[n-1]).(*ebiten.Image)
	} else {
		c.cur = c.root
	}
}

var _ kit.Canvas = (*Canvas)(nil)
