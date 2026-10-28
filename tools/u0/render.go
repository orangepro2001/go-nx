package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"
)

// The rounded rectangle every control of the planned interface is drawn with: a
// signed-distance field evaluated per pixel, so that corners are antialiased at any
// size without a bitmap. (docs/ui-redesign.md, section on drawing.)
const sdfShader = `//kage:unit pixels
package main

var Center vec2
var Half vec2
var Radius float

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	p := dst.xy - Center
	q := abs(p) - (Half - vec2(Radius, Radius))
	d := length(max(q, vec2(0, 0))) + min(max(q.x, q.y), 0) - Radius
	a := clamp(0.5-d, 0, 1)
	return vec4(0.2, 0.5, 1.0, 1.0) * a
}
`

// sdfAlpha is the same formula in Go: what the shader should produce at the centre
// of pixel (x, y).
func sdfAlpha(x, y int, cx, cy, hx, hy, r float64) float64 {
	px, py := float64(x)+0.5-cx, float64(y)+0.5-cy
	qx, qy := math.Abs(px)-(hx-r), math.Abs(py)-(hy-r)
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
	return math.Min(math.Max(0.5-d, 0), 1)
}

// SDFResult is the shader check.
type SDFResult struct {
	CompileMS  float64 `json:"compileMs"`
	Pixels     int     `json:"pixels"`
	MaxAbsDiff int     `json:"maxAbsDiff"` // largest difference in any channel, 0-255
	Pass       bool    `json:"pass"`
	Err        string  `json:"error,omitempty"`
}

// TextCheck is one script's sample drawn with the font chain.
type TextCheck struct {
	Script  string `json:"script"`
	Sample  string `json:"sample"`
	Covered bool   `json:"covered"` // some face in the chain has a glyph for every rune
	Ink     int    `json:"inkPixels"`
	Pass    bool   `json:"pass"`
	Note    string `json:"note,omitempty"`
}

// RenderResult is the offscreen rendering check.
type RenderResult struct {
	GraphicsLibrary string      `json:"graphicsLibrary"`
	FirstDrawMS     float64     `json:"firstDrawMs"` // from the start of the program to the first Draw
	SDF             SDFResult   `json:"sdf"`
	Text            []TextCheck `json:"text"`
	FontChain       []string    `json:"fontChain"`
	PNG             string      `json:"png,omitempty"`
	ReadbackOK      bool        `json:"readbackOk"` // pixels could be read back, which is what a screenshot needs
	Err             string      `json:"error,omitempty"`
}

type renderGame struct {
	res    *RenderResult
	chain  []text.Face
	names  []string
	covers func(r rune) bool
	pngOut string
	tick   int
	draws  int
}

var samples = []struct{ script, text string }{
	{"latin", "Remote control 0123456789"},
	{"han", "简体中文 远程控制 连接 断开"},
	{"hangul", "한국어 원격 제어 연결 끊기"},
}

// fontChain opens the plan's fonts that exist in dir, then any extra files, then the
// embedded Go Regular, and returns faces for them in that order, with a function that
// says whether some face in the chain has a glyph for a rune.
func fontChain(dir string, extra []string) (faces []text.Face, names []string, covers func(rune) bool, err error) {
	var sources []*text.GoTextFaceSource
	add := func(src *text.GoTextFaceSource, name string) {
		sources = append(sources, src)
		faces = append(faces, &text.GoTextFace{Source: src, Size: 20})
		names = append(names, name)
	}
	open := func(path string, collection bool, want string) {
		f, e := os.Open(path) // stays open: the source reads from it
		if e != nil {
			return
		}
		if collection {
			all, e := text.NewGoTextFaceSourcesFromCollection(f)
			if e != nil || len(all) == 0 {
				f.Close()
				return
			}
			src := all[0]
			for _, s := range all {
				if want != "" && strings.EqualFold(s.Metadata().Family, want) {
					src = s
				}
			}
			add(src, filepath.Base(path)+" ("+src.Metadata().Family+")")
			return
		}
		src, e := text.NewGoTextFaceSource(f)
		if e != nil {
			f.Close()
			return
		}
		add(src, filepath.Base(path)+" ("+src.Metadata().Family+")")
	}
	for _, s := range defaultFontSpecs {
		open(filepath.Join(dir, s.File), s.Collection, s.Want)
	}
	for _, p := range extra {
		ext := strings.ToLower(filepath.Ext(p))
		open(p, ext == ".ttc" || ext == ".otc", "")
	}
	goSrc, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		return nil, nil, nil, err
	}
	add(goSrc, "Go Regular (embedded)")
	covers = func(r rune) bool {
		for _, s := range sources {
			if hasGlyph(s, r) {
				return true
			}
		}
		return false
	}
	return faces, names, covers, nil
}

func (g *renderGame) Layout(w, h int) (int, int) { return w, h }

func (g *renderGame) Update() error {
	g.tick++
	if g.tick == 1 {
		g.run()
	}
	if g.draws >= 2 { // the work is done and Draw has really run
		return ebiten.Termination
	}
	return nil
}

func (g *renderGame) Draw(screen *ebiten.Image) {
	g.draws++
	if g.res.FirstDrawMS == 0 {
		g.res.FirstDrawMS = ms(time.Since(processStart))
		g.res.GraphicsLibrary = graphicsLibrary()
	}
}

func (g *renderGame) run() {
	r := g.res
	r.FontChain = g.names

	// The shader against its Go twin, over a whole 200x100 image.
	t0 := time.Now()
	sh, err := ebiten.NewShader([]byte(sdfShader))
	r.SDF.CompileMS = ms(time.Since(t0))
	if err != nil {
		r.SDF.Err = err.Error()
		return
	}
	const w, h = 200, 100
	img := ebiten.NewImage(w, h)
	cx, cy, hx, hy, rad := 100.0, 50.0, 80.0, 30.0, 14.0
	img.DrawRectShader(w, h, sh, &ebiten.DrawRectShaderOptions{Uniforms: map[string]any{
		"Center": []float32{float32(cx), float32(cy)}, "Half": []float32{float32(hx), float32(hy)}, "Radius": float32(rad),
	}})
	pix := make([]byte, 4*w*h)
	img.ReadPixels(pix)
	r.ReadbackOK = true
	maxDiff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := sdfAlpha(x, y, cx, cy, hx, hy, rad)
			want := [4]float64{0.2 * a, 0.5 * a, 1.0 * a, a} // premultiplied R, G, B, A
			for c := 0; c < 4; c++ {
				got := float64(pix[(y*w+x)*4+c])
				maxDiff = max(maxDiff, int(math.Round(math.Abs(got-want[c]*255))))
			}
		}
	}
	r.SDF.Pixels, r.SDF.MaxAbsDiff = w*h, maxDiff
	r.SDF.Pass = maxDiff <= 4

	// Text in three scripts through the chain.
	mf, err := text.NewMultiFace(g.chain...)
	if err != nil {
		r.Err = err.Error()
		return
	}
	scene := ebiten.NewImage(640, 24+len(samples)*40+120)
	scene.DrawImage(img, &ebiten.DrawImageOptions{GeoM: translate(20, 150)})
	for i, s := range samples {
		covered := true
		for _, c := range s.text {
			if c != ' ' && !g.covers(c) {
				covered = false
			}
		}
		line := ebiten.NewImage(620, 34)
		op := &text.DrawOptions{}
		op.GeoM.Translate(4, 4)
		op.ColorScale.ScaleWithColor(color.White)
		text.Draw(line, s.text, mf, op)
		lp := make([]byte, 4*620*34)
		line.ReadPixels(lp)
		ink := 0
		for p := 3; p < len(lp); p += 4 {
			if lp[p] != 0 {
				ink++
			}
		}
		tc := TextCheck{Script: s.script, Sample: s.text, Covered: covered, Ink: ink, Pass: covered && ink > 60}
		if !covered {
			tc.Note = "no font in the chain has all these glyphs on this machine, so what was drawn is not the script"
		}
		r.Text = append(r.Text, tc)
		o2 := &ebiten.DrawImageOptions{GeoM: translate(10, float64(12+i*40))}
		scene.DrawImage(line, o2)
	}
	if g.pngOut != "" {
		sp := make([]byte, 4*scene.Bounds().Dx()*scene.Bounds().Dy())
		scene.ReadPixels(sp)
		im := &image.NRGBA{Pix: unpremultiply(sp), Stride: 4 * scene.Bounds().Dx(), Rect: scene.Bounds()}
		var buf bytes.Buffer
		if err := png.Encode(&buf, im); err == nil && os.WriteFile(g.pngOut, buf.Bytes(), 0o644) == nil {
			r.PNG = g.pngOut
		}
	}
}

func translate(x, y float64) (m ebiten.GeoM) {
	m.Translate(x, y)
	return m
}

// unpremultiply turns ReadPixels' premultiplied RGBA into the straight RGBA that PNG wants,
// on a dark background so that the text can be seen.
func unpremultiply(p []byte) []byte {
	out := make([]byte, len(p))
	for i := 0; i < len(p); i += 4 {
		a := int(p[i+3])
		// Composite over (24, 28, 36).
		out[i] = byte(int(p[i]) + (24*(255-a))/255)
		out[i+1] = byte(int(p[i+1]) + (28*(255-a))/255)
		out[i+2] = byte(int(p[i+2]) + (36*(255-a))/255)
		out[i+3] = 255
	}
	return out
}

func runRender(fontDir string, extra []string, pngOut string) (*RenderResult, error) {
	res := &RenderResult{}
	faces, names, covers, err := fontChain(fontDir, extra)
	if err != nil {
		return res, err
	}
	g := &renderGame{res: res, chain: faces, names: names, covers: covers, pngOut: pngOut}
	if err := runWindow("go-nx u0 render", 320, 200, g); err != nil {
		res.Err = err.Error()
		return res, err
	}
	return res, nil
}

func (r *RenderResult) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, first Draw at %.0f ms, shader compile %.1f ms, SDF max diff %d/255 (pass %v), readback %v",
		r.GraphicsLibrary, r.FirstDrawMS, r.SDF.CompileMS, r.SDF.MaxAbsDiff, r.SDF.Pass, r.ReadbackOK)
	return b.String()
}
