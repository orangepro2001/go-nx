// Command u0 is the technical-validation spike that comes before any work on the
// new user interface (docs/ui-redesign.md, U0). It checks, on the machine it runs
// on, the things the plan assumes about Ebiten, the fonts and the input stack, and
// writes what it finds as JSON. Nothing here is shipped: it exists so that the
// interface is built on measured facts.
//
//	u0 fonts [-dir D]          font files: present, size, load time and memory, glyph coverage
//	u0 render [-png out.png]   offscreen rendering: an SDF shader, three-language text, a screenshot
//	u0 idle [-seconds N]       CPU and frame counts of the ways to draw only when something changed
//	u0 tiles                   cost of uploading and drawing a 64x64 tile and a whole picture
//	u0 input                   (Windows) keys injected with SendInput reach the Ebiten window
//	u0 all [-png out.png]      everything above that can run here, one JSON report
//
// The graphical ones need a display (on Linux: xvfb-run -a u0 ...).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Machine says where the numbers were taken.
type Machine struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	CPUs   int    `json:"cpus"`
	CPU    string `json:"cpu,omitempty"`
	Go     string `json:"go"`
	Ebiten string `json:"ebiten"`
}

// Report is everything the spike found.
type Report struct {
	Machine Machine       `json:"machine"`
	Fonts   []FontResult  `json:"fonts,omitempty"`
	Render  *RenderResult `json:"render,omitempty"`
	Idle    *IdleReport   `json:"idle,omitempty"`
	Tiles   *TileResult   `json:"tiles,omitempty"`
	Input   *InputResult  `json:"input,omitempty"`
	Errors  []string      `json:"errors,omitempty"` // a part that could not run, and why
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
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/hajimehoshi/ebiten/v2" {
				m.Ebiten = d.Version
			}
		}
	}
	return m
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: u0 fonts|render|idle|tiles|input|all [flags]")
		return 2
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet("u0 "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fontDir := fs.String("dir", systemFontsDir(), "directory with the system fonts")
	oneFont := fs.String("file", "", "fonts: check just this font file instead of the plan's list (a .ttc is a collection)")
	sample := fs.String("sample", "简体中文 한국어 English", "fonts: text the single font must cover")
	want := fs.String("want", "", "fonts: for a collection, the family to use")
	extra := fs.String("extra", "", "render: extra font files for the chain, comma separated, before the embedded fallback")
	pngOut := fs.String("png", "", "render: write the test scene to this PNG file")
	seconds := fs.Float64("seconds", 5, "idle: seconds each way of drawing is left alone and measured")
	check := fs.Bool("check", false, "exit 1 if a correctness check failed (a budget that is not met does not count)")
	out := fs.String("out", "", "also write the JSON report to this file")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	rep := Report{Machine: machine()}
	switch cmd {
	case "fonts":
		if *oneFont != "" {
			ext := strings.ToLower(filepath.Ext(*oneFont))
			rep.Fonts = []FontResult{checkFont(filepath.Dir(*oneFont), fontSpec{
				Role: "ad hoc", File: filepath.Base(*oneFont), Collection: ext == ".ttc" || ext == ".otc", Want: *want, Sample: *sample,
			})}
		} else {
			rep.Fonts = checkFonts(*fontDir, defaultFontSpecs)
		}
	case "render":
		var ex []string
		if *extra != "" {
			ex = strings.Split(*extra, ",")
		}
		res, err := runRender(*fontDir, ex, *pngOut)
		rep.Render = res
		if err != nil {
			fmt.Fprintln(stderr, "u0 render:", err)
		}
	case "idle":
		res, err := runIdle(*seconds)
		rep.Idle = res
		if err != nil {
			fmt.Fprintln(stderr, "u0 idle:", err)
		}
	case "tiles":
		res, err := runTiles()
		rep.Tiles = res
		if err != nil {
			fmt.Fprintln(stderr, "u0 tiles:", err)
		}
	case "all":
		rep = runAll(*fontDir, *extra, *pngOut, *seconds, stderr)
	case "input":
		res, err := runInput()
		rep.Input = res
		if err != nil {
			fmt.Fprintln(stderr, "u0 input:", err)
		}
	default:
		fmt.Fprintf(stderr, "u0: unknown command %q\n", cmd)
		return 2
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
	stdout.Write(buf.Bytes())
	if *out != "" {
		if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
			fmt.Fprintln(stderr, "u0:", err)
			return 1
		}
	}
	if cmd == "all" {
		fmt.Fprint(stderr, summary(rep))
	}
	if *check {
		if fails := hardFailures(rep); len(fails) > 0 {
			for _, f := range fails {
				fmt.Fprintln(stderr, "FAIL:", f)
			}
			return 1
		}
	}
	return 0
}

// runAll runs every part, each GUI part in a process of its own because Ebiten runs
// one window per process, and puts the results in one report.
func runAll(fontDir, extra, pngOut string, seconds float64, stderr io.Writer) Report {
	rep := Report{Machine: machine(), Fonts: checkFonts(fontDir, defaultFontSpecs)}
	child := func(name string, args ...string) *Report {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{name}, args...)...)
		var out strings.Builder
		cmd.Stdout, cmd.Stderr = &out, stderr
		if err := cmd.Run(); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", name, err))
		}
		var r Report
		if err := json.Unmarshal([]byte(out.String()), &r); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: no report: %v", name, err))
			return nil
		}
		return &r
	}
	args := []string{"-dir", fontDir}
	if extra != "" {
		args = append(args, "-extra", extra)
	}
	if pngOut != "" {
		args = append(args, "-png", pngOut)
	}
	if r := child("render", args...); r != nil {
		rep.Render = r.Render
	}
	if r := child("idle", "-seconds", fmt.Sprint(seconds)); r != nil {
		rep.Idle = r.Idle
	}
	if r := child("tiles"); r != nil {
		rep.Tiles = r.Tiles
	}
	if r := child("input"); r != nil {
		rep.Input = r.Input
	}
	return rep
}
