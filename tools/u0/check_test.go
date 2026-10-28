package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestSDFFormulaGoesFromInsideToOutsideAcrossOnePixel(t *testing.T) {
	// A 160x60 rectangle centred at 100,50 with corners of radius 14.
	at := func(x, y int) float64 { return sdfAlpha(x, y, 100, 50, 80, 30, 14) }
	if a := at(100, 50); a != 1 {
		t.Errorf("centre = %v, want 1", a)
	}
	if a := at(2, 2); a != 0 {
		t.Errorf("a pixel outside the rounded corner = %v, want 0", a)
	}
	if a := at(100, 5); a != 0 {
		t.Errorf("far outside = %v", a)
	}
	// The top edge is at y=20: pixel row 19 has its centre half a pixel outside, row 20 half a pixel inside.
	if out, in := at(100, 19), at(100, 20); out >= 0.01 || in <= 0.99 {
		t.Errorf("across the edge: %v then %v", out, in)
	}
	// The corner is rounded: the bounding box's corner pixel is outside, the same
	// distance along an edge is inside.
	if at(20, 20) != 0 || at(34, 20) < 0.5 {
		t.Errorf("corner %v, along the edge %v", at(20, 20), at(34, 20))
	}
	// Antialiasing: between inside and outside there are intermediate values.
	mid := false
	for x := 18; x < 40 && !mid; x++ {
		for y := 18; y < 40; y++ {
			if a := at(x, y); a > 0.2 && a < 0.8 {
				mid = true
			}
		}
	}
	if !mid {
		t.Error("no antialiased pixels around the corner")
	}
}

func TestDistributionAndMedian(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Microsecond)
	}
	d := dist(ds)
	if d.N != 100 || math.Abs(d.P50-51) > 1.01 || math.Abs(d.P99-100) > 1.01 || d.Max != 100 {
		t.Errorf("dist = %+v", d)
	}
	if got := median([]time.Duration{3, 1, 2}); got != 2 {
		t.Errorf("median = %v", got)
	}
	if (dist(nil) != Dist{}) {
		t.Error("an empty distribution should be empty")
	}
}

func TestUnpremultiplyDrawsOverTheBackground(t *testing.T) {
	px := []byte{0, 0, 0, 0 /* transparent */, 255, 255, 255, 255 /* opaque white */, 100, 0, 0, 128 /* half red */}
	out := unpremultiply(px)
	if out[0] != 24 || out[1] != 28 || out[2] != 36 || out[3] != 255 {
		t.Errorf("transparent became %v, want the background", out[0:4])
	}
	if out[4] != 255 || out[5] != 255 || out[6] != 255 {
		t.Errorf("opaque white became %v", out[4:8])
	}
	if out[8] < 100 || out[8] > 130 {
		t.Errorf("half red (premultiplied 100) became %v", out[8])
	}
}

func TestHardFailuresNameWhatIsBroken(t *testing.T) {
	good := Report{
		Render: &RenderResult{ReadbackOK: true, SDF: SDFResult{Pass: true}, Text: []TextCheck{{Script: "han", Covered: true, Ink: 500, Pass: true}}},
		Tiles:  &TileResult{TileSubmit: Dist{N: 10, P50: 6}},
		Idle:   &IdleReport{},
	}
	if f := hardFailures(good); len(f) != 0 {
		t.Errorf("a good report fails: %v", f)
	}
	bad := good
	bad.Render = &RenderResult{ReadbackOK: true, SDF: SDFResult{MaxAbsDiff: 90}, Text: []TextCheck{
		{Script: "han", Covered: true, Ink: 3},
		{Script: "hangul", Covered: false, Ink: 3, Note: "no font"}, // no font: not a failure
	}}
	bad.Errors = []string{"idle: exit status 1"}
	bad.Tiles = &TileResult{TileSubmit: Dist{N: 10, P50: 20000}}
	got := strings.Join(hardFailures(bad), "\n")
	for _, want := range []string{"idle: exit status 1", "SDF shader differs", "han text", "64x64 tile took"} {
		if !strings.Contains(got, want) {
			t.Errorf("failures lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hangul") {
		t.Errorf("a script without a font was counted as a failure:\n%s", got)
	}
}

func TestSummaryMentionsTheScheduleFrameVerdict(t *testing.T) {
	r := Report{Idle: &IdleReport{Schedule: ScheduleResult{Scheduled: 8, DrawsAfterEach: 1, Works: true}}}
	if s := summary(r); !strings.Contains(s, "ScheduleFrame: works") {
		t.Errorf("summary = %q", s)
	}
	r.Idle.Schedule = ScheduleResult{Note: "did not produce a frame"}
	if s := summary(r); !strings.Contains(s, "ASSUMPTION FAILED") {
		t.Errorf("summary = %q", s)
	}
}
