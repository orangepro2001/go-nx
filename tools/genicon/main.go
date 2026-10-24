// Command genicon draws the go-nx icon and writes cmd/go-nx/icon.ico (tray and
// exe), icon.png (window) and app.rc (resource script for windres).
//
// Run from the repository root: go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

const outDir = "cmd/go-nx"

// Shapes are described in a unit square so they scale to any size.
type pt struct{ x, y float64 }

var arrow = []pt{{0.50, 0.40}, {0.50, 0.76}, {0.595, 0.675}, {0.655, 0.80}, {0.725, 0.77}, {0.665, 0.65}, {0.79, 0.65}}

func roundRectDist(p pt, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	dx := math.Abs(p.x-cx) - ((x1-x0)/2 - r)
	dy := math.Abs(p.y-cy) - ((y1-y0)/2 - r)
	return math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - r
}

func inPoly(p pt, poly []pt) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a.y > p.y) != (b.y > p.y) && p.x < (b.x-a.x)*(p.y-a.y)/(b.y-a.y)+a.x {
			in = !in
		}
	}
	return in
}

func segDist(p, a, b pt) float64 {
	vx, vy := b.x-a.x, b.y-a.y
	t := math.Max(0, math.Min(1, ((p.x-a.x)*vx+(p.y-a.y)*vy)/(vx*vx+vy*vy)))
	return math.Hypot(p.x-(a.x+t*vx), p.y-(a.y+t*vy))
}

func polyDist(p pt, poly []pt) float64 {
	d := math.Inf(1)
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		d = math.Min(d, segDist(p, poly[i], poly[j]))
	}
	return d
}

func over(dst, src [4]float64) [4]float64 { // premultiplied-free "over"
	a := src[3] + dst[3]*(1-src[3])
	if a == 0 {
		return [4]float64{}
	}
	var out [4]float64
	for i := 0; i < 3; i++ {
		out[i] = (src[i]*src[3] + dst[i]*dst[3]*(1-src[3])) / a
	}
	out[3] = a
	return out
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// sample returns the colour at p in the unit square.
func sample(p pt) [4]float64 {
	var c [4]float64
	if roundRectDist(p, 0.02, 0.02, 0.98, 0.98, 0.22) > 0 {
		return c
	}
	t := (p.x + p.y) / 2 // blue gradient background
	c = [4]float64{lerp(0.22, 0.09, t), lerp(0.47, 0.22, t), lerp(0.95, 0.62, t), 1}
	white := [4]float64{1, 1, 1, 1}
	screenBlue := [4]float64{0.07, 0.17, 0.48, 1}
	if roundRectDist(p, 0.15, 0.20, 0.85, 0.66, 0.05) <= 0 { // monitor frame
		c = over(c, white)
		if roundRectDist(p, 0.20, 0.25, 0.80, 0.61, 0.02) <= 0 {
			c = over(c, screenBlue)
		}
	}
	if p.x > 0.44 && p.x < 0.56 && p.y > 0.66 && p.y < 0.76 { // stand
		c = over(c, white)
	}
	if roundRectDist(p, 0.32, 0.74, 0.68, 0.80, 0.025) <= 0 { // base
		c = over(c, white)
	}
	if inPoly(p, arrow) { // cursor: orange fill, white outline
		c = over(c, [4]float64{1, 0.72, 0.15, 1})
	} else if polyDist(p, arrow) < 0.022 {
		c = over(c, white)
	}
	return c
}

// render draws the icon at n x n pixels with 4x4 supersampling.
func render(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	const ss = 4
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := sample(pt{(float64(x) + (float64(sx)+0.5)/ss) / float64(n), (float64(y) + (float64(sy)+0.5)/ss) / float64(n)})
					r, g, b, a = r+c[0]*c[3], g+c[1]*c[3], b+c[2]*c[3], a+c[3]
				}
			}
			if a > 0 {
				img.SetNRGBA(x, y, color.NRGBA{uint8(r/a*255 + 0.5), uint8(g/a*255 + 0.5), uint8(b/a*255 + 0.5), uint8(a/(ss*ss)*255 + 0.5)})
			}
		}
	}
	return img
}

// bmpEntry encodes img as an ICO DIB image (32-bit BGRA, bottom-up, AND mask).
func bmpEntry(img *image.NRGBA) []byte {
	n := img.Bounds().Dx()
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, struct {
		Size          uint32
		W, H          int32
		Planes, Bits  uint16
		Compression   uint32
		SizeImage     uint32
		XPPM, YPPM    int32
		ClrUsed, ClrI uint32
	}{Size: 40, W: int32(n), H: int32(2 * n), Planes: 1, Bits: 32})
	for y := n - 1; y >= 0; y-- {
		for x := 0; x < n; x++ {
			c := img.NRGBAAt(x, y)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	b.Write(make([]byte, ((n+31)/32*4)*n)) // AND mask: alpha channel decides
	return b.Bytes()
}

func writeICO(path string, sizes []int) {
	var datas [][]byte
	for _, n := range sizes {
		img := render(n)
		if n >= 256 {
			var pb bytes.Buffer
			if err := png.Encode(&pb, img); err != nil {
				log.Fatal(err)
			}
			datas = append(datas, pb.Bytes())
		} else {
			datas = append(datas, bmpEntry(img))
		}
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, n := range sizes {
		dim := byte(n)
		if n >= 256 {
			dim = 0
		}
		out.Write([]byte{dim, dim, 0, 0})
		binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, [2]uint32{uint32(len(datas[i])), uint32(off)})
		off += len(datas[i])
	}
	for _, d := range datas {
		out.Write(d)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

func main() {
	writeICO(filepath.Join(outDir, "icon.ico"), []int{16, 24, 32, 48, 64, 256})
	var pb bytes.Buffer
	if err := png.Encode(&pb, render(256)); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "icon.png"), pb.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "app.rc"), []byte("1 ICON \"icon.ico\"\n"), 0o644); err != nil {
		log.Fatal(err)
	}
}
