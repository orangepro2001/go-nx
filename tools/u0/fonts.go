package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"bytes"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
)

// fontSpec is a font the user interface plan relies on (docs/ui-redesign.md 4.3).
type fontSpec struct {
	Role       string // latin, zh, ko ...
	File       string // file name in the fonts directory
	Collection bool   // a .ttc: take the face whose family is Want
	Want       string
	Sample     string // text the font must be able to show
}

var defaultFontSpecs = []fontSpec{
	{Role: "latin", File: "segoeui.ttf", Sample: "Remote control 0123456789"},
	{Role: "latin-semibold", File: "seguisb.ttf", Sample: "Remote control 0123456789"},
	{Role: "zh", File: "msyh.ttc", Collection: true, Want: "Microsoft YaHei UI", Sample: "简体中文 远程控制 连接 断开"},
	{Role: "ko", File: "malgun.ttf", Sample: "한국어 원격 제어 연결 끊기"},
}

// FontResult is what was found out about one font.
type FontResult struct {
	Role   string  `json:"role"`
	File   string  `json:"file"`
	Found  bool    `json:"found"`
	Bytes  int64   `json:"bytes,omitempty"`
	Faces  int     `json:"faces,omitempty"`  // faces in the file (more than one for a collection)
	Family string  `json:"family,omitempty"` // the face that was used
	LoadMS float64 `json:"loadMs,omitempty"` // opening the file and parsing it with the file handed over

	// What loading cost in memory, two ways. The file way hands the *os.File to the
	// font loader, which reads from it as needed; the naive way reads all of it first.
	HeapDeltaKB      int64   `json:"heapDeltaKb"`
	PrivateDeltaKB   int64   `json:"privateDeltaKb"`
	NaiveHeapDeltaKB int64   `json:"naiveHeapDeltaKb"`
	NaiveLoadMS      float64 `json:"naiveLoadMs,omitempty"`

	// For a collection: what it costs to take just its first face with the
	// single-font loader, instead of parsing every face in the file.
	FirstFaceLoadMS      float64 `json:"firstFaceLoadMs,omitempty"`
	FirstFaceHeapDeltaKB int64   `json:"firstFaceHeapDeltaKb,omitempty"`
	FirstFaceFamily      string  `json:"firstFaceFamily,omitempty"`
	FirstFaceErr         string  `json:"firstFaceError,omitempty"`

	Covers  bool   `json:"covers"`            // every rune of the sample has a glyph
	Missing string `json:"missing,omitempty"` // runes of the sample that have none
	Err     string `json:"error,omitempty"`
}

func heapAlloc() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// hasGlyph asks the font whether it has a glyph for r, through the loader's own
// face (which Ebiten exposes as "unsafe internal"), by reflection so that the spike
// does not import the typesetting module directly.
func hasGlyph(src *text.GoTextFaceSource, r rune) bool {
	m := reflect.ValueOf(src.UnsafeInternal()).MethodByName("NominalGlyph")
	if !m.IsValid() {
		return true // cannot tell; do not claim a gap
	}
	out := m.Call([]reflect.Value{reflect.ValueOf(r)})
	return len(out) == 2 && out[1].Bool()
}

// checkFont looks for spec.File in dir and measures what loading it costs.
func checkFont(dir string, spec fontSpec) FontResult {
	r := FontResult{Role: spec.Role, File: spec.File}
	path := filepath.Join(dir, spec.File)
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r
		}
		r.Err = err.Error()
		return r
	}
	r.Found, r.Bytes = true, st.Size()

	// The way the plan prescribes: hand over the file; keep it open, because the
	// loader reads from it later.
	f, err := os.Open(path)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	heap0, priv0, t0 := heapAlloc(), privateBytes(), time.Now()
	var src *text.GoTextFaceSource
	if spec.Collection {
		var all []*text.GoTextFaceSource
		if all, err = text.NewGoTextFaceSourcesFromCollection(f); err == nil {
			r.Faces = len(all)
			src = all[0]
			for _, s := range all {
				if strings.EqualFold(s.Metadata().Family, spec.Want) {
					src = s
					break
				}
			}
		}
	} else if src, err = text.NewGoTextFaceSource(f); err == nil {
		r.Faces = 1
	}
	r.LoadMS = float64(time.Since(t0).Microseconds()) / 1000
	heap1, priv1 := heapAlloc(), privateBytes()
	if err != nil {
		f.Close()
		r.Err = err.Error()
		return r
	}
	r.HeapDeltaKB = (int64(heap1) - int64(heap0)) >> 10
	r.PrivateDeltaKB = (int64(priv1) - int64(priv0)) >> 10
	r.Family = src.Metadata().Family
	var missing []rune
	for _, c := range spec.Sample {
		if c != ' ' && !hasGlyph(src, c) {
			missing = append(missing, c)
		}
	}
	r.Covers, r.Missing = len(missing) == 0, string(missing)
	runtime.KeepAlive(src)
	runtime.KeepAlive(f) // stays open for as long as src is used

	if spec.Collection {
		if f2, err := os.Open(path); err == nil {
			h0, t1 := heapAlloc(), time.Now()
			one, err := text.NewGoTextFaceSource(f2)
			r.FirstFaceLoadMS = float64(time.Since(t1).Microseconds()) / 1000
			r.FirstFaceHeapDeltaKB = (int64(heapAlloc()) - int64(h0)) >> 10
			if err != nil {
				r.FirstFaceErr = err.Error()
			} else {
				r.FirstFaceFamily = one.Metadata().Family
			}
			runtime.KeepAlive(one)
			runtime.KeepAlive(f2)
		}
	}

	// The control: read all of it, then parse from memory.
	heap2, t2 := heapAlloc(), time.Now()
	data, err := os.ReadFile(path)
	if err == nil {
		if spec.Collection {
			_, err = text.NewGoTextFaceSourcesFromCollection(bytes.NewReader(data))
		} else {
			_, err = text.NewGoTextFaceSource(bytes.NewReader(data))
		}
	}
	r.NaiveLoadMS = float64(time.Since(t2).Microseconds()) / 1000
	heap3 := heapAlloc()
	r.NaiveHeapDeltaKB = (int64(heap3) - int64(heap2)) >> 10
	runtime.KeepAlive(data)
	return r
}

// systemFontsDir is where the system fonts are.
func systemFontsDir() string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("WINDIR")
		if root == "" {
			root = `C:\Windows`
		}
		return filepath.Join(root, "Fonts")
	}
	return "/usr/share/fonts"
}

func checkFonts(dir string, specs []fontSpec) []FontResult {
	var out []FontResult
	for _, s := range specs {
		out = append(out, checkFont(dir, s))
	}
	return out
}

func (r FontResult) String() string {
	switch {
	case r.Err != "":
		return fmt.Sprintf("%s %s: error: %s", r.Role, r.File, r.Err)
	case !r.Found:
		return fmt.Sprintf("%s %s: not found", r.Role, r.File)
	}
	cover := "covers the sample"
	if !r.Covers {
		cover = "MISSING " + r.Missing
	}
	return fmt.Sprintf("%s %s: %.1f MB, %d face(s), family %q; from the file handle: %.0f ms and +%.1f MB heap; read whole first: %.0f ms and +%.1f MB; %s",
		r.Role, r.File, float64(r.Bytes)/(1<<20), r.Faces, r.Family, r.LoadMS, float64(r.HeapDeltaKB)/1024, r.NaiveLoadMS, float64(r.NaiveHeapDeltaKB)/1024, cover)
}
