package gfx

import (
	"image"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
)

// ScaleFilter is how the remote picture is resampled to the size it is shown at.
type ScaleFilter int

const (
	// ScaleSharp enlarges with Catmull-Rom (bicubic) held to the range of the four nearest
	// pixels, so edges stay sharp without the halos plain bicubic leaves, and shrinks by
	// averaging every pixel a screen pixel covers, so small text does not shimmer.
	ScaleSharp ScaleFilter = iota
	// ScaleSmooth is bilinear both ways.
	ScaleSmooth
	// ScalePixel enlarges with nearest neighbour (square pixels) and shrinks bilinearly.
	ScalePixel
)

// scaleShaderSrc resamples the source image. Scale is destination pixels per source pixel;
// the vertex colour carries the dimming of a connection that is being brought back.
const scaleShaderSrc = `//kage:unit pixels
package main

var Scale float

// texel is the pixel of the picture that contains position p (relative to the picture),
// clamped to the picture so that its edges do not fade into nothing.
func texel(p vec2) vec4 {
	q := clamp(floor(p), vec2(0), imageSrc0Size()-vec2(1))
	return imageSrc0UnsafeAt(imageSrc0Origin() + q + vec2(0.5))
}

// weights are the Catmull-Rom weights of the four pixels around a point f of the way
// from the second to the third.
func weights(f float) vec4 {
	f2 := f * f
	f3 := f2 * f
	return vec4(-0.5*f3+f2-0.5*f, 1.5*f3-2.5*f2+1, -1.5*f3+2*f2+0.5*f, 0.5*f3-0.5*f2)
}

func row(i vec2, dy float, w vec4) vec4 {
	return w.x*texel(i+vec2(-1, dy)) + w.y*texel(i+vec2(0, dy)) + w.z*texel(i+vec2(1, dy)) + w.w*texel(i+vec2(2, dy))
}

func enlarge(p vec2) vec4 {
	t := p - vec2(0.5)
	i := floor(t)
	f := t - i
	wx := weights(f.x)
	wy := weights(f.y)
	c := wy.x*row(i, -1, wx) + wy.y*row(i, 0, wx) + wy.z*row(i, 1, wx) + wy.w*row(i, 2, wx)
	a := texel(i)
	b := texel(i + vec2(1, 0))
	d := texel(i + vec2(0, 1))
	e := texel(i + vec2(1, 1))
	return clamp(c, min(min(a, b), min(d, e)), max(max(a, b), max(d, e)))
}

// shrink averages the pixels under the footprint of one destination pixel, sampled on an
// even grid of up to 8 by 8.
func shrink(p vec2) vec4 {
	foot := 1 / Scale
	n := min(ceil(foot), 8)
	step := foot / n
	start := p - vec2(foot*0.5)
	var sum vec4
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if float(x) < n && float(y) < n {
				sum += texel(start + vec2((float(x)+0.5)*step, (float(y)+0.5)*step))
			}
		}
	}
	return sum / (n * n)
}

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	p := src - imageSrc0Origin()
	if Scale >= 1 {
		return enlarge(p) * color
	}
	return shrink(p) * color
}
`

// Scaler draws the remote picture at any size with ScaleSharp. Make one with NewScaler once
// the game is running.
type Scaler struct {
	shader *ebiten.Shader
}

// NewScaler compiles the resampling shader.
func NewScaler() (*Scaler, error) {
	sh, err := ebiten.NewShader([]byte(scaleShaderSrc))
	if err != nil {
		return nil, err
	}
	return &Scaler{shader: sh}, nil
}

// Draw draws src on dst scaled by scale, with its top-left corner at (x, y), only the part
// inside clip, with its colour multiplied by dim (1 for none). Filter chooses the
// resampling; a nil Scaler (its shader did not compile) draws ScaleSharp as ScaleSmooth. The
// sharp filter draws on the whole of dst and keeps to clip by drawing only the visible part:
// what a sub-image's origin is to a shader differs between Ebiten's backends (see
// Canvas.shape).
func (s *Scaler) Draw(dst, src *ebiten.Image, x, y, scale float64, clip image.Rectangle, filter ScaleFilter, dim float32) {
	sb := src.Bounds()
	w, h := float64(sb.Dx())*scale, float64(sb.Dy())*scale
	if scale <= 0 || w <= 0 || h <= 0 {
		return
	}
	if filter != ScaleSharp || scale == 1 || s == nil {
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(scale, scale)
		op.GeoM.Translate(x, y)
		// Shrinking with nearest neighbour drops pixel rows and columns and breaks small
		// text, so only enlarge with it.
		op.Filter = ebiten.FilterLinear
		if filter == ScalePixel && scale >= 1 || scale == 1 {
			op.Filter = ebiten.FilterNearest
		}
		op.ColorScale.Scale(dim, dim, dim, 1)
		dst.SubImage(clip).(*ebiten.Image).DrawImage(src, op)
		return
	}
	x0, y0 := math.Max(x, float64(clip.Min.X)), math.Max(y, float64(clip.Min.Y))
	x1, y1 := math.Min(x+w, float64(clip.Max.X)), math.Min(y+h, float64(clip.Max.Y))
	if x1 <= x0 || y1 <= y0 {
		return
	}
	// Where the visible corners fall in the source, in its own coordinates.
	sx := func(dx float64) float32 { return float32(float64(sb.Min.X) + (dx-x)/scale) }
	sy := func(dy float64) float32 { return float32(float64(sb.Min.Y) + (dy-y)/scale) }
	v := func(dx, dy float64) ebiten.Vertex {
		return ebiten.Vertex{DstX: float32(dx), DstY: float32(dy), SrcX: sx(dx), SrcY: sy(dy), ColorR: dim, ColorG: dim, ColorB: dim, ColorA: 1}
	}
	vs := []ebiten.Vertex{v(x0, y0), v(x1, y0), v(x0, y1), v(x1, y1)}
	op := &ebiten.DrawTrianglesShaderOptions{Uniforms: map[string]any{"Scale": float32(scale)}}
	op.Images[0] = src
	dst.DrawTrianglesShader(vs, []uint16{0, 1, 2, 1, 2, 3}, s.shader, op)
}

// Close releases the shader.
func (s *Scaler) Close() { s.shader.Deallocate() }
