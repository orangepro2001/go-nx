// Package theme holds the design tokens of go-nx's interface: colours for the dark and
// light themes, spacing, corner radii, type sizes and control heights. It is data
// only, so that every screen draws from one place and the contrast of every pair of
// colours that is actually used can be checked by a test.
//
// The accent is blue. Blue means "can be acted on, selected, or focused"; whether
// something is good, needs attention or is dangerous is said by Good, Warn and Bad,
// and the two are never mixed (docs/ui-redesign.md, section 6).
package theme

import (
	"image/color"
	"math"
)

// Color is a non-premultiplied colour.
type Color = color.NRGBA

// Mode is the light or dark theme.
type Mode uint8

const (
	Dark Mode = iota
	Light
)

func (m Mode) String() string {
	if m == Light {
		return "light"
	}
	return "dark"
}

// Palette is the colours of one theme.
type Palette struct {
	Mode Mode

	BG       Color // the page
	Surface  Color // cards, the sidebar
	Surface2 Color // inputs, hovered rows
	Border   Color

	Text    Color
	TextDim Color

	Accent   Color // the one blue
	OnAccent Color // text on a filled accent
	Good     Color // verified, online
	Warn     Color // needs attention
	Bad      Color // error, open mode, danger
	OnBad    Color // text on a filled Bad

	Scrim  Color // behind a dialog
	Shadow Color // base colour of shadows; the alpha is set where they are drawn
}

func rgb(r, g, b uint8) Color { return Color{R: r, G: g, B: b, A: 255} }

func rgba(r, g, b, a uint8) Color { return Color{R: r, G: g, B: b, A: a} }

// DarkPalette is the dark theme.
func DarkPalette() Palette {
	return Palette{
		Mode: Dark,
		BG:   rgb(0x0F, 0x12, 0x18), Surface: rgb(0x17, 0x1B, 0x23), Surface2: rgb(0x1F, 0x24, 0x30), Border: rgb(0x2B, 0x32, 0x40),
		Text: rgb(0xE8, 0xEC, 0xF3), TextDim: rgb(0x9A, 0xA4, 0xB5),
		Accent: rgb(0x4C, 0x8D, 0xFF), OnAccent: rgb(0x0B, 0x12, 0x20),
		Good: rgb(0x3D, 0xD6, 0x8C), Warn: rgb(0xF5, 0xA5, 0x24), Bad: rgb(0xF0, 0x52, 0x4D), OnBad: rgb(0x0B, 0x12, 0x20),
		Scrim: rgba(0, 0, 0, 150), Shadow: rgba(0, 0, 0, 255),
	}
}

// LightPalette is the light theme.
func LightPalette() Palette {
	return Palette{
		Mode: Light,
		BG:   rgb(0xF4, 0xF6, 0xFA), Surface: rgb(0xFF, 0xFF, 0xFF), Surface2: rgb(0xEE, 0xF1, 0xF6), Border: rgb(0xD5, 0xDB, 0xE6),
		Text: rgb(0x16, 0x1B, 0x26), TextDim: rgb(0x5A, 0x65, 0x78),
		Accent: rgb(0x25, 0x63, 0xEB), OnAccent: rgb(0xFF, 0xFF, 0xFF),
		Good: rgb(0x12, 0x80, 0x5A), Warn: rgb(0xB4, 0x53, 0x09), Bad: rgb(0xC6, 0x28, 0x28), OnBad: rgb(0xFF, 0xFF, 0xFF),
		Scrim: rgba(10, 14, 24, 120), Shadow: rgba(20, 30, 50, 255),
	}
}

// PaletteFor returns the palette of a mode.
func PaletteFor(m Mode) Palette {
	if m == Light {
		return LightPalette()
	}
	return DarkPalette()
}

// Spacing steps, on a 4 px grid, in logical pixels.
const (
	S1 = 4.0
	S2 = 8.0
	S3 = 12.0
	S4 = 16.0
	S5 = 24.0
	S6 = 32.0
)

// Corner radii.
const (
	RadiusSmall  = 6.0  // chips, inputs
	RadiusMedium = 10.0 // cards, buttons
	RadiusLarge  = 16.0 // dialogs
)

// Type sizes. CJK text is never set below MinCJKSize: the strokes of a Han or Hangul
// character at 12 px are not legible.
const (
	SizeCaption = 12.0
	SizeBody    = 14.0
	SizeSub     = 16.0
	SizeTitle   = 20.0
	SizeDisplay = 28.0
	SizeCode    = 32.0 // the pairing code, the host ID
	MinCJKSize  = 13.0
)

// Line height as a multiple of the font size.
const (
	LineLatin = 1.4
	LineCJK   = 1.5
)

// Controls.
const (
	ControlH  = 36.0 // buttons, inputs
	RowH      = 48.0 // list rows
	MinHit    = 32.0 // smallest clickable area in either direction
	SidebarW  = 220.0
	SidebarWs = 64.0  // the collapsed sidebar
	NarrowAt  = 760.0 // window widths below this collapse the sidebar
	TabH      = 40.0  // the session tab strip
	FocusRing = 2.0
)

// Animation durations in milliseconds.
const (
	AnimHover = 120
	AnimPanel = 180
)

// Contrast is the WCAG contrast ratio of two colours, from 1 (none) to 21.
func Contrast(a, b Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c Color) float64 {
	f := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// Mix blends a toward b by t (0 is a, 1 is b), keeping a's alpha.
func Mix(a, b Color, t float64) Color {
	t = math.Min(math.Max(t, 0), 1)
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return Color{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: a.A}
}

// WithAlpha returns c with alpha a (0 to 255).
func WithAlpha(c Color, a uint8) Color { c.A = a; return c }
