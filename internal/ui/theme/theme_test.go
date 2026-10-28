package theme

import "testing"

func rgbaT(r, g, b, a uint8) Color { return Color{R: r, G: g, B: b, A: a} }

// Every pair of text and background the interface uses has to be readable in both
// themes: 4.5:1 is WCAG AA for normal text. Good, Warn and Bad as text are only used
// on BG and Surface, never on Surface2 (some of those pairs fall just under).
func TestTextColoursAreReadable(t *testing.T) {
	for _, p := range []Palette{DarkPalette(), LightPalette()} {
		type pair struct {
			name   string
			fg, bg Color
		}
		pairs := []pair{
			{"text on bg", p.Text, p.BG}, {"text on surface", p.Text, p.Surface}, {"text on surface2", p.Text, p.Surface2},
			{"dim on bg", p.TextDim, p.BG}, {"dim on surface", p.TextDim, p.Surface}, {"dim on surface2", p.TextDim, p.Surface2},
			{"accent on bg", p.Accent, p.BG}, {"accent on surface", p.Accent, p.Surface},
			{"good on bg", p.Good, p.BG}, {"good on surface", p.Good, p.Surface},
			{"warn on bg", p.Warn, p.BG}, {"warn on surface", p.Warn, p.Surface},
			{"bad on bg", p.Bad, p.BG}, {"bad on surface", p.Bad, p.Surface},
			{"on-accent on accent", p.OnAccent, p.Accent},
			{"on-bad on bad", p.OnBad, p.Bad},
		}
		for _, c := range pairs {
			if r := Contrast(c.fg, c.bg); r < 4.5 {
				t.Errorf("%s: %s contrast %.2f, want at least 4.5", p.Mode, c.name, r)
			}
		}
	}
}

func TestContrastKnownValues(t *testing.T) {
	if r := Contrast(rgbaT(0, 0, 0, 255), rgbaT(255, 255, 255, 255)); r < 20.9 || r > 21.1 {
		t.Errorf("black on white = %.2f, want 21", r)
	}
	if r := Contrast(rgbaT(9, 9, 9, 255), rgbaT(9, 9, 9, 255)); r != 1 {
		t.Errorf("a colour on itself = %.2f, want 1", r)
	}
	// The values in docs/ui-redesign.md, section 6, for the dark theme on Surface.
	d := DarkPalette()
	for _, c := range []struct {
		name string
		fg   Color
		want float64
	}{{"text", d.Text, 14.56}, {"dim", d.TextDim, 6.86}, {"accent", d.Accent, 5.39}, {"good", d.Good, 9.20}, {"warn", d.Warn, 8.45}, {"bad", d.Bad, 4.94}} {
		if r := Contrast(c.fg, d.Surface); r < c.want-0.01 || r > c.want+0.01 {
			t.Errorf("%s on surface = %.2f, the document says %.2f", c.name, r, c.want)
		}
	}
}

func TestMixAndAlpha(t *testing.T) {
	a, b := rgbaT(0, 0, 0, 200), rgbaT(100, 200, 50, 10)
	if got := Mix(a, b, 0); got != a {
		t.Errorf("Mix(a, b, 0) = %v", got)
	}
	if got := Mix(a, b, 1); got != (rgbaT(100, 200, 50, 200)) {
		t.Errorf("Mix(a, b, 1) = %v: the result keeps a's alpha", got)
	}
	if got := Mix(a, b, 0.5); got != (rgbaT(50, 100, 25, 200)) {
		t.Errorf("Mix(a, b, 0.5) = %v", got)
	}
	if got := Mix(a, b, 7); got != Mix(a, b, 1) {
		t.Errorf("t is not clamped: %v", got)
	}
	if got := WithAlpha(rgbaT(1, 2, 3, 255), 9); got != (rgbaT(1, 2, 3, 9)) {
		t.Errorf("WithAlpha = %v", got)
	}
}

func TestTheTwoThemesAreDifferentAndComplete(t *testing.T) {
	d, l := DarkPalette(), LightPalette()
	if d.BG == l.BG || d.Text == l.Text || d.Accent == l.Accent {
		t.Error("the themes share colours that must differ")
	}
	for _, p := range []Palette{d, l} {
		for name, c := range map[string]Color{"BG": p.BG, "Surface": p.Surface, "Surface2": p.Surface2, "Border": p.Border,
			"Text": p.Text, "TextDim": p.TextDim, "Accent": p.Accent, "OnAccent": p.OnAccent, "Good": p.Good, "Warn": p.Warn, "Bad": p.Bad, "OnBad": p.OnBad} {
			if c.A != 255 {
				t.Errorf("%s %s is not opaque: %v", p.Mode, name, c)
			}
		}
	}
	if PaletteFor(Light).Mode != Light || PaletteFor(Dark).Mode != Dark {
		t.Error("PaletteFor returns the wrong theme")
	}
}
