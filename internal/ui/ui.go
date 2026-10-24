// Package ui draws go-nx's tab bar and home tab. It has no Windows-specific code,
// so layout and hit-testing can be tested and rendered anywhere. All sizes are
// physical pixels.
package ui

import (
	"bytes"
	"image/color"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"
)

const FontSize = 22

var (
	ColBG     = color.RGBA{28, 30, 36, 255}
	ColText   = color.RGBA{230, 230, 230, 255}
	ColDim    = color.RGBA{150, 150, 160, 255}
	ColWarn   = color.RGBA{255, 190, 90, 255}
	ColBox    = color.RGBA{50, 52, 60, 255}
	ColInput  = color.RGBA{18, 19, 24, 255}
	ColHot    = color.RGBA{80, 130, 220, 255}
	ColDanger = color.RGBA{120, 40, 40, 255}
	ColGood   = color.RGBA{90, 200, 120, 255}
	ColBad    = color.RGBA{230, 90, 90, 255}
)

// Rect is a pixel rectangle.
type Rect struct{ X, Y, W, H int }

func (r Rect) Contains(x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

// NewFace returns the interface font.
func NewFace() (*text.GoTextFace, error) {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		return nil, err
	}
	return &text.GoTextFace{Source: src, Size: FontSize}, nil
}

// Text draws s with its top-left corner at (x, y).
func Text(dst *ebiten.Image, face *text.GoTextFace, s string, x, y int, c color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(float64(x), float64(y))
	op.ColorScale.ScaleWithColor(c)
	text.Draw(dst, s, face, op)
}

// Width is the drawn width of s.
func Width(face *text.GoTextFace, s string) float64 {
	w, _ := text.Measure(s, face, 0)
	return w
}

// Truncate shortens s with an ellipsis so that it fits in maxW pixels.
func Truncate(face *text.GoTextFace, s string, maxW float64) string {
	if Width(face, s) <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 0 {
		r = r[:len(r)-1]
		if t := string(r) + "…"; Width(face, t) <= maxW {
			return t
		}
	}
	return ""
}

// Wrap breaks s into lines no wider than maxW, at spaces where possible.
func Wrap(face *text.GoTextFace, s string, maxW float64) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		next := word
		if cur != "" {
			next = cur + " " + word
		}
		if cur == "" || Width(face, next) <= maxW {
			cur = next
			continue
		}
		lines = append(lines, cur)
		cur = word
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

var white *ebiten.Image

// Fill paints a solid rectangle.
func Fill(dst *ebiten.Image, x, y, w, h int, c color.Color) {
	if white == nil {
		white = ebiten.NewImage(1, 1)
		white.Fill(color.White)
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(w), float64(h))
	op.GeoM.Translate(float64(x), float64(y))
	op.ColorScale.ScaleWithColor(c)
	dst.DrawImage(white, op)
}
