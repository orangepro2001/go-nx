package main

import (
	"fmt"
	"strings"
)

// hardFailures are the things that mean the spike itself, or what it verifies about
// correctness, is broken: a part that did not run, a shader that draws something other
// than its formula, a script that has a font and still draws nothing, an upload that
// is slow by orders of magnitude. Whether a budget is met is not decided here; that
// is for people to read in the summary.
func hardFailures(r Report) []string {
	var out []string
	out = append(out, r.Errors...)
	if r.Render != nil {
		switch {
		case r.Render.Err != "":
			out = append(out, "render: "+r.Render.Err)
		case !r.Render.ReadbackOK:
			out = append(out, "render: pixels could not be read back, so no screenshot is possible")
		case !r.Render.SDF.Pass:
			out = append(out, fmt.Sprintf("render: the SDF shader differs from its formula by %d/255 (err %q)", r.Render.SDF.MaxAbsDiff, r.Render.SDF.Err))
		}
		for _, t := range r.Render.Text {
			if t.Covered && !t.Pass {
				out = append(out, fmt.Sprintf("render: %s text has a font and drew only %d pixels of ink", t.Script, t.Ink))
			}
		}
	}
	if r.Tiles != nil {
		switch {
		case r.Tiles.Err != "":
			out = append(out, "tiles: "+r.Tiles.Err)
		case r.Tiles.TileSubmit.N == 0:
			out = append(out, "tiles: nothing was measured")
		case r.Tiles.TileSubmitMeanUS > 5000: // 5 ms for a 64x64 tile would be a different world
			out = append(out, fmt.Sprintf("tiles: a 64x64 tile took %.0f us on average", r.Tiles.TileSubmitMeanUS))
		}
	}
	if r.Idle != nil && r.Idle.Err != "" {
		out = append(out, "idle: "+r.Idle.Err)
	}
	if r.Input != nil && r.Input.Err != "" {
		out = append(out, "input: "+r.Input.Err)
	}
	return out
}

// summary says in a few lines what was found, against the budgets of
// docs/ui-redesign.md section 3, without deciding anything.
func summary(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s, %s, Go %s, Ebiten %s\n", r.Machine.OS, r.Machine.Arch, r.Machine.CPU, r.Machine.Go, r.Machine.Ebiten)
	for _, f := range r.Fonts {
		fmt.Fprintf(&b, "font   %s\n", f)
	}
	if x := r.Render; x != nil {
		fmt.Fprintf(&b, "render %s\n", x.summary())
		for _, t := range x.Text {
			fmt.Fprintf(&b, "       %-6s covered=%v ink=%d pass=%v %s\n", t.Script, t.Covered, t.Ink, t.Pass, t.Note)
		}
	}
	if x := r.Idle; x != nil {
		for _, s := range x.Strategies {
			fmt.Fprintf(&b, "idle   %s\n", s)
		}
		s := x.Schedule
		verdict := "works"
		if !s.Works {
			verdict = "ASSUMPTION FAILED: " + s.Note
		}
		fmt.Fprintf(&b, "idle   FPSModeVsyncOffMinimum + ScheduleFrame: %s (%d scheduled, %.1f frames each, latency median %.1f ms max %.1f ms; waiting costs %.1f draws/s and %.3f%% of a core)\n",
			verdict, s.Scheduled, s.DrawsAfterEach, s.MedianLatencyMS, s.MaxLatencyMS, s.IdleDrawsPerSec, s.IdleCPUPercent)
	}
	if x := r.Tiles; x != nil {
		fmt.Fprintf(&b, "tiles  %s: 64x64 tile %.1f us a call (budget 200 us), 1080p picture %.2f ms, 4K %.2f ms, DrawImage of a 1080p picture %.1f us a call; clock step %.1f us%s\n",
			x.GraphicsLibrary, x.TileSubmitMeanUS, x.Full1080Synced, x.Full2160Synced, x.DrawImageMeanUS, x.ClockResolutionUS, coarse(x.ClockResolutionUS))
	}
	if x := r.Input; x != nil {
		if x.Skipped != "" {
			fmt.Fprintf(&b, "input  skipped: %s\n", x.Skipped)
		} else {
			fmt.Fprintf(&b, "input  focused=%v keys=%v chars=%q field=%q unicode=%q no-key-for-hangul-hanja=%v\n", x.Focused, x.Keys, x.Chars, x.FieldText, x.HangulChars, x.NoKeyForHangul)
		}
	}
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "ERROR  %s\n", e)
	}
	return b.String()
}

func coarse(us float64) string {
	if us >= 100 {
		return " (coarse: per-call percentiles are quantised, the means are not)"
	}
	return ""
}
