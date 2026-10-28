// Command perf measures what the host and the viewer spend on a picture, on
// synthetic screens (package synth): how big the first update is, how much a
// steady stream costs, and how long diffing, encoding and decoding take. It runs
// the same code the host and viewer run (tile.Differ, tile.Encode, tile.Decode)
// without a network or a window, so the numbers are repeatable on any machine.
//
// What the numbers are not: a prediction for a person's desktop. The scenes are
// patterns that change about as much as common work does, not recordings, and the
// synthetic grab is far cheaper than a real GDI capture (which only a Windows
// machine can measure). They are for noticing that a change made things better or
// worse, and for knowing which kind of picture is the expensive one.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/tile"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// Machine says where the numbers were taken.
type Machine struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	CPUs int    `json:"cpus"`
	CPU  string `json:"cpu,omitempty"`
	Go   string `json:"go"`
}

// Result is one scene at one size. Times are milliseconds per frame, the mean of
// the frames after the first (so they describe a steady stream); the first update,
// which carries the whole screen, is reported separately.
type Result struct {
	Scene string `json:"scene"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Tiles int    `json:"tiles"`

	FirstBytes      int     `json:"firstBytes"`
	FirstDirtyTiles int     `json:"firstDirtyTiles"`
	FirstEncodeMS   float64 `json:"firstEncodeMs"`
	FirstDecodeMS   float64 `json:"firstDecodeMs"`

	Frames          int     `json:"frames"` // frames measured after the first
	SteadyBytesMean int     `json:"steadyBytesMean"`
	SteadyBytesP95  int     `json:"steadyBytesP95"`
	DirtyTilesMean  float64 `json:"dirtyTilesMean"`
	DirtyShare      float64 `json:"dirtyShare"` // DirtyTilesMean / Tiles

	GrabMS   float64 `json:"grabMs"` // the synthetic source; a real GDI grab costs something else
	DiffMS   float64 `json:"diffMs"`
	EncodeMS float64 `json:"encodeMs"`
	DecodeMS float64 `json:"decodeMs"`

	KBpsAt10       float64 `json:"kbpsAt10"`       // steady bandwidth at 10 frames a second, kilobytes a second
	KBpsAt30       float64 `json:"kbpsAt30"`       // and at 30
	HostCPUPctAt10 float64 `json:"hostCpuPctAt10"` // diff + encode as a share of one core at 10 frames a second
	ViewCPUPctAt10 float64 `json:"viewCpuPctAt10"` // decode, likewise
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// measure runs the host's per-frame work on a synthetic screen: the first frame
// and then frames more, and averages the latter.
func measure(scene synth.Scene, w, h, frames int) (Result, error) {
	src, err := synth.NewScene(scene, w, h)
	if err != nil {
		return Result{}, err
	}
	cur, view := tile.NewFrame(w, h), tile.NewFrame(w, h)
	var diff tile.Differ
	r := Result{Scene: scene.String(), W: w, H: h, Tiles: cur.Cols() * cur.Rows(), Frames: frames}

	var grab, dif, enc, dec time.Duration
	var sizes []int
	var dirty int
	for i := 0; i <= frames; i++ {
		t0 := time.Now()
		if err := src.Grab(cur); err != nil {
			return r, err
		}
		tg := time.Since(t0)
		t1 := time.Now()
		idxs := diff.Diff(cur)
		td := time.Since(t1)
		var data []byte
		var te, tdec time.Duration
		if len(idxs) > 0 {
			t2 := time.Now()
			if data, err = tile.Encode(cur, idxs); err != nil {
				return r, err
			}
			te = time.Since(t2)
			t3 := time.Now()
			if _, err = tile.Decode(view, data); err != nil {
				return r, err
			}
			tdec = time.Since(t3)
		}
		if i == 0 {
			r.FirstBytes, r.FirstDirtyTiles, r.FirstEncodeMS, r.FirstDecodeMS = len(data), len(idxs), ms(te), ms(tdec)
			continue
		}
		grab, dif, enc, dec = grab+tg, dif+td, enc+te, dec+tdec
		sizes = append(sizes, len(data))
		dirty += len(idxs)
	}
	n := float64(frames)
	r.GrabMS, r.DiffMS, r.EncodeMS, r.DecodeMS = ms(grab)/n, ms(dif)/n, ms(enc)/n, ms(dec)/n
	r.DirtyTilesMean = float64(dirty) / n
	r.DirtyShare = r.DirtyTilesMean / float64(r.Tiles)
	total := 0
	for _, s := range sizes {
		total += s
	}
	r.SteadyBytesMean = total / frames
	sort.Ints(sizes)
	r.SteadyBytesP95 = sizes[min(len(sizes)-1, len(sizes)*95/100)]
	r.KBpsAt10 = float64(r.SteadyBytesMean) * 10 / 1000
	r.KBpsAt30 = float64(r.SteadyBytesMean) * 30 / 1000
	// ms of work a frame x 10 frames a second, out of 1000 ms, as a percentage: the
	// factors cancel, so at 10 frames a second the share in percent is the ms a frame.
	r.HostCPUPctAt10 = r.DiffMS + r.EncodeMS
	r.ViewCPUPctAt10 = r.DecodeMS
	return r, nil
}

func machine() Machine {
	m := Machine{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Go: runtime.Version()}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
				m.CPU = strings.TrimSpace(v)
				break
			}
		}
	}
	return m
}

func parseSizes(s string) ([][2]int, error) {
	var out [][2]int
	for _, f := range strings.Split(s, ",") {
		a, b, ok := strings.Cut(strings.ToLower(strings.TrimSpace(f)), "x")
		w, err1 := strconv.Atoi(a)
		h, err2 := strconv.Atoi(b)
		if !ok || err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%q is not WIDTHxHEIGHT", f)
		}
		out = append(out, [2]int{w, h})
	}
	return out, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("perf", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sizes := fs.String("sizes", "1920x1080,3840x2160", "screen sizes, comma separated")
	scenes := fs.String("scenes", "all", "scenes, comma separated, or all")
	frames := fs.Int("frames", 20, "frames to measure after the first")
	fps := fs.Int("fps", 10, "frame rate the CPU shares are computed at")
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	bad := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "perf: "+format+"\n", a...)
		return 2
	}
	if *frames < 1 || *fps < 1 {
		return bad("-frames and -fps must be at least 1")
	}
	sz, err := parseSizes(*sizes)
	if err != nil {
		return bad("%v", err)
	}
	var list []synth.Scene
	if *scenes == "all" {
		list = synth.Scenes()
	} else {
		for _, n := range strings.Split(*scenes, ",") {
			sc, err := synth.ParseScene(strings.TrimSpace(n))
			if err != nil {
				return bad("%v", err)
			}
			list = append(list, sc)
		}
	}

	var results []Result
	for _, s := range sz {
		for _, sc := range list {
			r, err := measure(sc, s[0], s[1], *frames)
			if err != nil {
				return bad("%v", err)
			}
			// The shares were computed at 10 frames a second; scale them to -fps.
			r.HostCPUPctAt10 *= float64(*fps) / 10
			r.ViewCPUPctAt10 *= float64(*fps) / 10
			results = append(results, r)
		}
	}
	m := machine()
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			Machine Machine  `json:"machine"`
			FPS     int      `json:"fps"`
			Results []Result `json:"results"`
		}{m, *fps, results})
		return 0
	}
	fmt.Fprintf(stdout, "%s/%s, %d CPUs, %s, %s\n\n", m.OS, m.Arch, m.CPUs, m.CPU, m.Go)
	fmt.Fprintf(stdout, "| scene | size | tiles | first update | steady a frame (mean / p95) | tiles changed | diff ms | encode ms | decode ms | KB/s at 10 / 30 fps | host CPU at %d fps | viewer CPU at %d fps |\n", *fps, *fps)
	fmt.Fprintln(stdout, "|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, r := range results {
		fmt.Fprintf(stdout, "| %s | %dx%d | %d | %s in %.0f ms | %s / %s | %.1f (%.0f%%) | %.2f | %.2f | %.2f | %.0f / %.0f | %.1f%% | %.1f%% |\n",
			r.Scene, r.W, r.H, r.Tiles, human(r.FirstBytes), r.FirstEncodeMS, human(r.SteadyBytesMean), human(r.SteadyBytesP95),
			r.DirtyTilesMean, r.DirtyShare*100, r.DiffMS, r.EncodeMS, r.DecodeMS, r.KBpsAt10, r.KBpsAt30, r.HostCPUPctAt10, r.ViewCPUPctAt10)
	}
	return 0
}

func human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
