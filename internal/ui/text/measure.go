// Package text is how go-nx measures, breaks and draws words: fonts that are found on
// the system and loaded when they are needed, a fallback chain per interface language,
// line breaking by Unicode rules (UAX #14) so that a Chinese sentence without spaces
// still wraps, and truncation between grapheme clusters so that a Hangul syllable or an
// emoji is never cut in half.
//
// Everything in this file is pure: it needs a Measurer and nothing else, so wrapping
// and truncation are tested on any machine with a fake one. The Measurer used in the
// program is Engine (engine.go).
package text

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-text/typesetting/segmenter"
)

// Weight is regular or semibold type.
type Weight uint8

const (
	Regular Weight = iota
	Semibold
)

// Style is a size in logical pixels and a weight.
type Style struct {
	Size   float64
	Weight Weight
}

// Measurer measures text on one line, in logical pixels.
type Measurer interface {
	// Width is the advance width of s set on a single line.
	Width(s string, st Style) float64
	// LineHeight is the distance between two lines of this style.
	LineHeight(st Style) float64
}

// Ellipsis ends a truncated string.
const Ellipsis = "…"

// Wrap breaks s into lines no wider than maxW. Lines break where Unicode allows
// (UAX #14), so a run of Chinese or Korean text wraps between characters or words;
// a newline in s always starts a new line; trailing spaces do not count toward a
// line's width and are not kept. A word wider than maxW by itself is broken between
// grapheme clusters, so no line is wider than maxW unless a single cluster is. An
// empty s gives no lines.
func Wrap(m Measurer, s string, st Style, maxW float64) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		para = strings.TrimRight(para, "\r")
		out = append(out, wrapParagraph(m, para, st, maxW)...)
	}
	// A trailing newline in s does not make a trailing empty line.
	for len(out) > 0 && out[len(out)-1] == "" && strings.HasSuffix(s, "\n") {
		out = out[:len(out)-1]
		s = strings.TrimSuffix(s, "\n")
	}
	return out
}

func wrapParagraph(m Measurer, para string, st Style, maxW float64) []string {
	if para == "" {
		return []string{""}
	}
	var seg segmenter.Segmenter
	if err := seg.InitWithString(para); err != nil {
		return []string{para}
	}
	var pieces []string
	for it := seg.LineIterator(); it.Next(); {
		pieces = append(pieces, string(it.Line().Text))
	}
	pieces = keepHangulWords(pieces)
	var lines []string
	cur := ""
	flush := func() {
		if t := strings.TrimRightFunc(cur, unicode.IsSpace); t != "" || len(lines) == 0 {
			lines = append(lines, t)
		}
		cur = ""
	}
	for _, piece := range pieces {
		if fits(m, cur+piece, st, maxW) {
			cur += piece
			continue
		}
		if cur != "" {
			flush()
		}
		if fits(m, piece, st, maxW) {
			cur = piece
			continue
		}
		// One word wider than the line: fill lines with as many clusters as fit.
		for _, part := range breakByCluster(m, piece, st, maxW) {
			cur = part
			flush()
		}
	}
	if cur != "" {
		flush()
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// keepHangulWords joins break opportunities that fall inside a Korean word. UAX #14 lets a
// line break between any two Hangul syllables, as it does between Chinese characters;
// but Korean is written with spaces between words and read in words, so a line is
// broken at a space (CSS calls this word-break: keep-all). A piece that ends in a
// space is a real break and is left alone.
func keepHangulWords(pieces []string) []string {
	var out []string
	for _, p := range pieces {
		if n := len(out); n > 0 && joinsWord(out[n-1], p) {
			out[n-1] += p
			continue
		}
		out = append(out, p)
	}
	return out
}

// joinsWord reports whether a line may not end between prev and next: no space
// between them, both sides are letters or digits, and at least one is Hangul.
func joinsWord(prev, next string) bool {
	a, _ := utf8.DecodeLastRuneInString(prev)
	b, _ := utf8.DecodeRuneInString(next)
	wordy := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	hangul := func(r rune) bool { return unicode.Is(unicode.Hangul, r) }
	return wordy(a) && wordy(b) && (hangul(a) || hangul(b))
}

// fits reports whether s, without its trailing spaces, is at most maxW wide.
func fits(m Measurer, s string, st Style, maxW float64) bool {
	return m.Width(strings.TrimRightFunc(s, unicode.IsSpace), st) <= maxW
}

// breakByCluster splits s into pieces no wider than maxW, between grapheme clusters
// (a piece has at least one cluster, however wide).
func breakByCluster(m Measurer, s string, st Style, maxW float64) []string {
	cl := clusters(s)
	var out []string
	cur := ""
	for _, c := range cl {
		if cur != "" && !fits(m, cur+c, st, maxW) {
			out = append(out, cur)
			cur = ""
		}
		cur += c
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// clusters splits s into grapheme clusters (UAX #29).
func clusters(s string) []string {
	var seg segmenter.Segmenter
	if err := seg.InitWithString(s); err != nil {
		return []string{s}
	}
	var out []string
	it := seg.GraphemeIterator()
	for it.Next() {
		out = append(out, string(it.Grapheme().Text))
	}
	return out
}

// Truncate shortens s so that it is at most maxW wide, ending in an ellipsis when
// something was cut off. It cuts between grapheme clusters. If even the ellipsis does
// not fit, the result is empty.
func Truncate(m Measurer, s string, st Style, maxW float64) string {
	if m.Width(s, st) <= maxW {
		return s
	}
	cl := clusters(s)
	// The longest prefix whose width with the ellipsis fits. Width grows with the prefix,
	// so a binary search finds it in a handful of measurements.
	lo, hi := 0, len(cl) // answer is in [lo, hi)
	prefix := func(n int) string { return strings.Join(cl[:n], "") }
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if m.Width(prefix(mid)+Ellipsis, st) <= maxW {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		if m.Width(Ellipsis, st) <= maxW {
			return Ellipsis
		}
		return ""
	}
	return strings.TrimRightFunc(prefix(lo), unicode.IsSpace) + Ellipsis
}

// Widest is the width of the widest of lines.
func Widest(m Measurer, lines []string, st Style) float64 {
	w := 0.0
	for _, l := range lines {
		w = max(w, m.Width(l, st))
	}
	return w
}

// IsWide reports whether r is drawn about one em wide (Han, Hangul, kana and the
// full-width forms) rather than about half an em.
func IsWide(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		(r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

// Fake is a Measurer that needs no fonts: a wide rune (IsWide) is one em wide, any other
// half an em, and a line is 1.4 em high. Tests use it so that layout can be asserted
// to the pixel on any machine.
type Fake struct{}

func (Fake) Width(s string, st Style) float64 {
	w := 0.0
	for _, r := range s {
		if IsWide(r) {
			w += st.Size
		} else {
			w += st.Size / 2
		}
	}
	return w
}

func (Fake) LineHeight(st Style) float64 { return st.Size * 1.4 }
