package text

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

func waitReady(t *testing.T, e *Engine) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e.Ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the fonts were not ready in 10 s")
}

func TestScriptOf(t *testing.T) {
	for r, want := range map[rune]Script{
		'a': Latin, 'Z': Latin, '7': Latin, 'é': Latin, '…': Latin, '·': Latin, '→': Latin,
		'中': Han, '文': Han, '、': Han, '。': Han, '，': Han, 'あ': Han,
		'한': Hangul, '글': Hangul, 'ㄱ': Hangul, 'ᄀ': Hangul,
	} {
		if got := ScriptOf(r); got != want {
			t.Errorf("ScriptOf(%q) = %v, want %v", r, got, want)
		}
	}
}

// With no font on the system the built-in one stands in, and everything still measures.
func TestEngineWithoutSystemFontsUsesTheBuiltInOne(t *testing.T) {
	e, err := NewEngine(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.SetLanguage(i18n.English)
	if !e.Ready() {
		t.Error("an engine with no locator is never ready")
	}
	st := Style{Size: 14}
	w := e.Width("Remote control", st)
	if w < 50 || w > 200 {
		t.Errorf("width of a 14 px phrase is %v", w)
	}
	if e.Width("", st) != 0 {
		t.Error("empty text has width")
	}
	if e.Width("MM", st) <= e.Width("ii", st) {
		t.Error("a proportional font should make MM wider than ii")
	}
	if e.LineHeight(st) < 14 || e.LineHeight(st) > 24 {
		t.Errorf("line height %v for size 14", e.LineHeight(st))
	}
	if a, b := e.Width("Remote control", Style{Size: 14}), e.Width("Remote control", Style{Size: 28}); math.Abs(b/a-2) > 0.05 {
		t.Errorf("doubling the size changed the width by %v", b/a)
	}
}

// Logical sizes do not depend on the device scale: text drawn at 150% is measured at
// 150% and reported back in logical pixels, within hinting.
func TestWidthIsInLogicalPixelsAtAnyScale(t *testing.T) {
	e, _ := NewEngine(nil, nil)
	defer e.Close()
	st := Style{Size: 16}
	base := e.Width("Connect to this PC", st)
	for _, scale := range []float64{1.25, 1.5, 2} {
		e.SetScale(scale)
		if got := e.Width("Connect to this PC", st); math.Abs(got-base)/base > 0.03 {
			t.Errorf("at scale %v the width is %v, at 1 it is %v", scale, got, base)
		}
	}
	e.SetScale(-3)
	if e.Scale() != 1 {
		t.Errorf("a nonsense scale was kept: %v", e.Scale())
	}
}

func TestEngineWrapsWithRealMeasurements(t *testing.T) {
	e, _ := NewEngine(nil, nil)
	defer e.Close()
	st := Style{Size: 14}
	for _, line := range Wrap(e, "The host switched to strict mode and this PC is not verified. Pair it or enter the password.", st, 180) {
		if w := e.Width(line, st); w > 180 {
			t.Errorf("line %q is %v wide", line, w)
		}
	}
	got := Truncate(e, "192.168.100.200:7000 with a very long name", st, 120)
	if w := e.Width(got, st); w > 120 || !strings.HasSuffix(got, Ellipsis) {
		t.Errorf("truncated to %q (%v wide)", got, w)
	}
}

// With the fonts of the machine (Noto here, Segoe UI and the others on Windows) the
// interface language decides which CJK font is open, and only one at a time.
func TestOnlyTheInterfaceLanguagesFontIsKeptOpen(t *testing.T) {
	loc := SystemLocator()
	if len(loc.Fonts(Han, Regular)) == 0 {
		t.Skip("no locator")
	}
	have := func(s Script) bool {
		for _, f := range loc.Fonts(s, Regular) {
			if _, err := os.Stat(f.Path); err == nil {
				return true
			}
		}
		return false
	}
	if !have(Han) || !have(Hangul) {
		t.Skip("this machine has no CJK fonts to open")
	}
	e, _ := NewEngine(loc, nil) // no logger: a font opens in the background and could report after the test
	defer e.Close()

	e.SetLanguage(i18n.Chinese)
	waitReady(t, e)
	loaded := strings.Join(e.Loaded(), "\n")
	if !strings.Contains(loaded, "han/") {
		t.Fatalf("Chinese interface: no Han font open:\n%s", loaded)
	}
	if strings.Contains(loaded, "hangul/") {
		t.Errorf("Chinese interface opened the Korean font as well:\n%s", loaded)
	}
	zh := e.Width("远程控制 连接", Style{Size: 16})
	if zh < 16*5 { // at least five em-wide characters
		t.Errorf("Chinese text measures %v: the glyphs are missing", zh)
	}

	// A Korean device name in the Chinese interface loads the Korean font on demand.
	e.EnsureFor("车间 한국어")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(strings.Join(e.Loaded(), "\n"), "hangul/") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(strings.Join(e.Loaded(), "\n"), "hangul/") {
		t.Fatal("the Korean font was not opened for Korean text")
	}

	// Switching to Korean drops what the Chinese interface had opened.
	e.SetLanguage(i18n.Korean)
	waitReady(t, e)
	loaded = strings.Join(e.Loaded(), "\n")
	if strings.Contains(loaded, "han/") || !strings.Contains(loaded, "hangul/") {
		t.Errorf("Korean interface has these fonts open:\n%s", loaded)
	}
	if ko := e.Width("원격 제어 연결", Style{Size: 16}); ko < 16*5 {
		t.Errorf("Korean text measures %v", ko)
	}
	e.SetLanguage(i18n.English)
	waitReady(t, e)
	if loaded = strings.Join(e.Loaded(), "\n"); strings.Contains(loaded, "han/") || strings.Contains(loaded, "hangul/") {
		t.Errorf("English interface still holds CJK fonts:\n%s", loaded)
	}
	if !e.TakeChanged() || e.TakeChanged() {
		t.Error("TakeChanged should report a change once")
	}
}

func TestMissingFontsAreGivenUpOnOnce(t *testing.T) {
	e, _ := NewEngine(nowhere{}, nil)
	defer e.Close()
	e.SetLanguage(i18n.Chinese)
	waitReady(t, e)
	if len(e.Loaded()) != 0 {
		t.Errorf("loaded %v from nowhere", e.Loaded())
	}
	// Still measures, with the built-in font.
	if e.Width("abc", Style{Size: 14}) <= 0 {
		t.Error("no width without fonts")
	}
}

type nowhere struct{}

func (nowhere) Fonts(Script, Weight) []FontFile { return []FontFile{{Path: "/no/such/font.ttf"}} }
