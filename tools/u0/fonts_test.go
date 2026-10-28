package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func writeFont(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAFontIsFoundLoadedAndItsCoverageChecked(t *testing.T) {
	dir := t.TempDir()
	writeFont(t, dir, "segoeui.ttf")
	r := checkFont(dir, fontSpec{Role: "latin", File: "segoeui.ttf", Sample: "Remote control 0123456789"})
	if r.Err != "" || !r.Found {
		t.Fatalf("result = %+v", r)
	}
	if r.Bytes != int64(len(goregular.TTF)) || r.Faces != 1 || r.LoadMS <= 0 {
		t.Errorf("bytes %d faces %d load %v ms", r.Bytes, r.Faces, r.LoadMS)
	}
	if !r.Covers || r.Missing != "" {
		t.Errorf("Go Regular should cover Latin: %+v", r)
	}
	if !strings.Contains(r.Family, "Go") {
		t.Errorf("family = %q", r.Family)
	}
}

func TestMissingGlyphsAreNamed(t *testing.T) {
	dir := t.TempDir()
	writeFont(t, dir, "msyh.ttf")
	r := checkFont(dir, fontSpec{Role: "zh", File: "msyh.ttf", Sample: "ab简体中文"})
	if r.Covers {
		t.Fatalf("a Latin font covers Han? %+v", r)
	}
	for _, want := range []string{"简", "体", "中", "文"} {
		if !strings.Contains(r.Missing, want) {
			t.Errorf("Missing = %q lacks %q", r.Missing, want)
		}
	}
	if strings.ContainsAny(r.Missing, "ab") {
		t.Errorf("Missing = %q names glyphs the font has", r.Missing)
	}
}

func TestAFontThatIsNotThereIsAFindNotAnError(t *testing.T) {
	r := checkFont(t.TempDir(), fontSpec{Role: "ko", File: "malgun.ttf", Sample: "한국어"})
	if r.Found || r.Err != "" {
		t.Fatalf("result = %+v: a missing font is something to report, not a failure", r)
	}
}

func TestAFileThatIsNotAFontIsReported(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "broken.ttf"), []byte("this is not a font"), 0o644)
	r := checkFont(dir, fontSpec{Role: "latin", File: "broken.ttf", Sample: "a"})
	if !r.Found || r.Err == "" {
		t.Fatalf("result = %+v", r)
	}
}

// Opening the file and handing the file to the font loader must not copy the whole
// font into memory: that is the point of the rule for the three CJK fonts, each of
// which is several megabytes. Go Regular is small, so the check is on the shape (the
// naive way holds at least the file; the file way is reported beside it) and the real
// numbers come from the Windows fonts.
func TestMemoryOfBothWaysOfLoadingIsReported(t *testing.T) {
	dir := t.TempDir()
	writeFont(t, dir, "segoeui.ttf")
	r := checkFont(dir, fontSpec{Role: "latin", File: "segoeui.ttf", Sample: "a"})
	if r.NaiveHeapDeltaKB < int64(len(goregular.TTF)>>10)-8 {
		t.Errorf("reading the whole file into memory should hold at least the file (%d KB), got %d KB", len(goregular.TTF)>>10, r.NaiveHeapDeltaKB)
	}
}

func TestDefaultSpecsCoverTheThreeLanguages(t *testing.T) {
	roles := map[string]bool{}
	for _, s := range defaultFontSpecs {
		roles[s.Role] = true
		if s.File == "" || s.Sample == "" {
			t.Errorf("spec %+v is incomplete", s)
		}
	}
	for _, want := range []string{"latin", "zh", "ko"} {
		if !roles[want] {
			t.Errorf("no spec for %s", want)
		}
	}
}
