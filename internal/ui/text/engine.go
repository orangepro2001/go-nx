package text

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	etext "github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"
	xlanguage "golang.org/x/text/language"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

// Script is a writing system that needs a font of its own.
type Script uint8

const (
	Latin Script = iota
	Han
	Hangul
	numScripts
)

func (s Script) String() string { return [...]string{"latin", "han", "hangul"}[s%numScripts] }

// FontFile is a font to open: a file, and for a collection (.ttc) the family wanted.
type FontFile struct {
	Path   string
	Family string
}

// Locator finds fonts on the system.
type Locator interface {
	// Fonts lists candidate files for a script and weight, best first. Only Latin has a
	// Semibold; the others are asked for Regular only.
	Fonts(s Script, w Weight) []FontFile
}

// loaded is an open font. The file stays open for as long as the font is in use: the
// font loader reads from it as it needs, which keeps a large CJK font from being held in
// memory twice (docs/ui-baseline.md, fonts).
type loaded struct {
	src  *etext.GoTextFaceSource
	file *os.File
	path string
}

type faceKey struct {
	size   float64
	weight Weight
	gen    uint64
}

type widthKey struct {
	s      string
	size   float64
	weight Weight
	gen    uint64
}

const maxWidthCache = 4096

// Engine is the Measurer of the running program, and the source of the faces text is
// drawn with. It owns the system fonts: the interface language decides which fonts are
// opened (in the background, and one CJK font at a time, because a CJK font costs 28 to
// 37 MB of memory), a string that needs another script's font starts loading it, and until
// a font is there the embedded Go Regular stands in for it.
type Engine struct {
	loc  Locator
	logf func(format string, args ...any)

	mu      sync.Mutex
	scale   float64
	lang    i18n.Lang
	fonts   [numScripts][2]*loaded
	pending [numScripts][2]bool
	failed  [numScripts][2]bool
	lazy    [numScripts]bool // scripts a string asked for, beyond the language's own
	gen     uint64
	faces   map[faceKey]*etext.MultiFace
	widths  map[widthKey]float64
	goSrc   *etext.GoTextFaceSource
	changed atomic.Bool
	wake    func()
}

// NewEngine makes an Engine that finds fonts with loc (nil: only the embedded font).
// logf, if set, is told about fonts that were opened or could not be.
func NewEngine(loc Locator, logf func(format string, args ...any)) (*Engine, error) {
	src, err := etext.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Engine{
		loc: loc, logf: logf, scale: 1, goSrc: src,
		faces: map[faceKey]*etext.MultiFace{}, widths: map[widthKey]float64{},
	}, nil
}

// OnLoad sets a function that is called, from a background goroutine, after a font has
// been opened or has failed to open, so the program can draw again. It must not block.
func (e *Engine) OnLoad(f func()) {
	e.mu.Lock()
	e.wake = f
	e.mu.Unlock()
}

// TakeChanged reports whether fonts have changed since the last call, so that the program
// knows to rebuild what it measured.
func (e *Engine) TakeChanged() bool { return e.changed.Swap(false) }

// SetScale sets the device pixels per logical pixel; text is measured and drawn at the
// size it will have on the screen.
func (e *Engine) SetScale(s float64) {
	if s <= 0 {
		s = 1
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s != e.scale {
		e.scale = s
		e.resetLocked()
	}
}

// Scale is the device pixels per logical pixel.
func (e *Engine) Scale() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.scale
}

func (e *Engine) resetLocked() {
	e.gen++
	clear(e.faces)
	clear(e.widths)
	e.changed.Store(true)
}

// primary is the script of the interface language.
func primary(l i18n.Lang) Script {
	switch l {
	case i18n.Chinese:
		return Han
	case i18n.Korean:
		return Hangul
	}
	return Latin
}

// SetLanguage switches the interface language: the fonts of other scripts that were opened
// are closed, and the ones this language needs are opened in the background.
func (e *Engine) SetLanguage(l i18n.Lang) {
	e.mu.Lock()
	e.lang = l
	var keep [numScripts]bool
	keep[Latin], keep[primary(l)] = true, true
	if l == i18n.Pseudo {
		keep = [numScripts]bool{true, true, true}
	}
	e.lazy = [numScripts]bool{} // what a string once asked for goes with the language
	for s := Script(0); s < numScripts; s++ {
		if keep[s] {
			continue
		}
		for w := range e.fonts[s] {
			if f := e.fonts[s][w]; f != nil {
				e.fonts[s][w] = nil
				e.resetLocked() // before the file goes: no face may outlive it
				f.file.Close()
				e.logf("font: closed %s", f.path)
			}
		}
	}
	e.resetLocked()
	e.mu.Unlock()
	e.need(Latin, Regular)
	e.need(Latin, Semibold)
	e.need(primary(l), Regular)
	if l == i18n.Pseudo {
		e.need(Han, Regular)
		e.need(Hangul, Regular)
	}
}

// Ready reports whether the fonts the interface language needs have been tried: opened, or
// given up on. The program draws a blank first frame rather than boxes where the glyphs
// of a font still being loaded will go.
func (e *Engine) Ready() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, need := range []struct {
		s Script
		w Weight
	}{{Latin, Regular}, {primary(e.lang), Regular}} {
		if e.fonts[need.s][need.w] == nil && !e.failed[need.s][need.w] {
			return false
		}
	}
	return true
}

// Busy reports whether a font is being opened in the background.
func (e *Engine) Busy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for s := range e.pending {
		for w := range e.pending[s] {
			if e.pending[s][w] {
				return true
			}
		}
	}
	return false
}

// Loaded lists the font files open now, for diagnostics.
func (e *Engine) Loaded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for s := range e.fonts {
		for w := range e.fonts[s] {
			if f := e.fonts[s][w]; f != nil {
				out = append(out, fmt.Sprintf("%s/%d: %s", Script(s), w, f.path))
			}
		}
	}
	return out
}

// EnsureFor starts opening the fonts that s needs and has not got.
func (e *Engine) EnsureFor(s string) {
	if len(s) == len([]rune(s)) { // ASCII only: nothing to load
		return
	}
	var hasHan, hasHangul bool
	for _, r := range s {
		switch ScriptOf(r) {
		case Han:
			hasHan = true
		case Hangul:
			hasHangul = true
		}
	}
	e.mu.Lock()
	e.lazy[Han] = e.lazy[Han] || hasHan
	e.lazy[Hangul] = e.lazy[Hangul] || hasHangul
	e.mu.Unlock()
	if hasHan {
		e.need(Han, Regular)
	}
	if hasHangul {
		e.need(Hangul, Regular)
	}
}

// ScriptOf is the script a rune needs a font of: Han for ideographs, kana and CJK
// punctuation; Hangul for Korean letters; Latin for everything else.
func ScriptOf(r rune) Script {
	switch {
	case r >= 0x1100 && r <= 0x11FF, r >= 0x3130 && r <= 0x318F, r >= 0xA960 && r <= 0xA97F, r >= 0xAC00 && r <= 0xD7FF:
		return Hangul
	case r < 0x2E80:
		return Latin
	case r >= 0x2E80 && r <= 0x9FFF, r >= 0xF900 && r <= 0xFAFF, r >= 0xFF00 && r <= 0xFFEF, r >= 0x20000 && r <= 0x2FA1F:
		return Han
	}
	return Latin
}

// need opens the font for a script and weight in the background, once.
func (e *Engine) need(s Script, w Weight) {
	if s != Latin {
		w = Regular
	}
	e.mu.Lock()
	if e.fonts[s][w] != nil || e.pending[s][w] || e.failed[s][w] || e.loc == nil {
		if e.loc == nil {
			e.failed[s][w] = true
		}
		e.mu.Unlock()
		return
	}
	e.pending[s][w] = true
	e.mu.Unlock()
	go e.load(s, w)
}

func (e *Engine) load(s Script, w Weight) {
	var got *loaded
	for _, c := range e.loc.Fonts(s, w) {
		if l, err := openFont(c); err == nil {
			got = l
			break
		} else if !os.IsNotExist(err) {
			e.logf("font: %s: %v", c.Path, err)
		}
	}
	e.mu.Lock()
	e.pending[s][w] = false
	if got == nil {
		e.failed[s][w] = true
		e.logf("font: no %s font found (weight %d): the built-in one stands in", s, w)
	} else if s != Latin && s != primary(e.lang) && !e.lazy[s] && e.lang != i18n.Pseudo {
		got.file.Close() // the language changed while this was loading
	} else {
		e.fonts[s][w] = got
		e.logf("font: %s opened for %s", got.path, s)
	}
	e.resetLocked()
	wake := e.wake
	e.mu.Unlock()
	if wake != nil {
		wake()
	}
}

func openFont(c FontFile) (*loaded, error) {
	f, err := os.Open(c.Path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(c.Path))
	if ext == ".ttc" || ext == ".otc" {
		all, err := etext.NewGoTextFaceSourcesFromCollection(f)
		if err != nil || len(all) == 0 {
			f.Close()
			if err == nil {
				err = fmt.Errorf("an empty font collection")
			}
			return nil, err
		}
		src := all[0]
		for _, s := range all {
			if c.Family != "" && strings.EqualFold(s.Metadata().Family, c.Family) {
				src = s
				break
			}
		}
		return &loaded{src: src, file: f, path: c.Path}, nil
	}
	src, err := etext.NewGoTextFaceSource(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &loaded{src: src, file: f, path: c.Path}, nil
}

// Close closes the font files.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetLocked()
	for s := range e.fonts {
		for w := range e.fonts[s] {
			if f := e.fonts[s][w]; f != nil {
				f.file.Close()
				e.fonts[s][w] = nil
			}
		}
	}
}

func langTag(s Script) xlanguage.Tag {
	switch s {
	case Han:
		return xlanguage.SimplifiedChinese
	case Hangul:
		return xlanguage.Korean
	}
	return xlanguage.English
}

// chainLocked is the faces text of a style is set in, most preferred first: the
// interface language's own script, then Latin, then the other CJK, then the built-in font,
// of which only the ones that are open.
func (e *Engine) chainLocked(st Style) []etext.Face {
	px := st.Size * e.scale
	var out []etext.Face
	add := func(s Script) {
		w := Regular
		if s == Latin {
			w = st.Weight
		}
		l := e.fonts[s][w]
		if l == nil && s == Latin && w == Semibold {
			l = e.fonts[Latin][Regular]
		}
		if l != nil {
			out = append(out, &etext.GoTextFace{Source: l.src, Size: px, Language: langTag(s)})
		}
	}
	p := primary(e.lang)
	order := []Script{p}
	for _, s := range []Script{Latin, Han, Hangul} {
		if s != p {
			order = append(order, s)
		}
	}
	if p == Hangul { // Hanja in a Korean interface take Korean letterforms: Hangul's font first
		order = []Script{Hangul, Latin, Han}
	}
	for _, s := range order {
		add(s)
	}
	out = append(out, &etext.GoTextFace{Source: e.goSrc, Size: px, Language: xlanguage.English})
	return out
}

// Face returns the face a style is drawn with. The size is in device pixels (the style's
// size times the scale). The result is valid until the fonts or the scale change.
func (e *Engine) Face(st Style) etext.Face {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.faceLocked(st)
}

func (e *Engine) faceLocked(st Style) *etext.MultiFace {
	k := faceKey{st.Size * e.scale, st.Weight, e.gen}
	if f := e.faces[k]; f != nil {
		return f
	}
	f, err := etext.NewMultiFace(e.chainLocked(st)...)
	if err != nil { // cannot happen: the chain always ends in the built-in font
		panic(err)
	}
	e.faces[k] = f
	return f
}

// Width implements Measurer.
func (e *Engine) Width(s string, st Style) float64 {
	if s == "" {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	k := widthKey{s, st.Size * e.scale, st.Weight, e.gen}
	if w, ok := e.widths[k]; ok {
		return w
	}
	w := etext.Advance(s, e.faceLocked(st)) / e.scale
	if len(e.widths) >= maxWidthCache {
		clear(e.widths)
	}
	e.widths[k] = w
	return w
}

// LineHeight implements Measurer: the natural height of a line of this style, ascent plus
// descent. (Layout spaces lines by the interface's own rule, kit.UI.LineH.)
func (e *Engine) LineHeight(st Style) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.faceLocked(st).Metrics()
	return (m.HAscent + m.HDescent) / e.scale
}
