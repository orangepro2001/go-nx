package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/orangepro2001/go-nx/internal/synth"
)

func measureSmall(t *testing.T, scene synth.Scene) Result {
	t.Helper()
	r, err := measure(scene, 640, 480, 12)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The shape of the numbers, not their size: they depend on the machine, but a still
// screen must cost nothing after its first picture, a video must cost the most, and
// the first picture is the whole screen whatever the scene.
func TestMeasurementsHaveTheShapeTheScenesImply(t *testing.T) {
	results := map[string]Result{}
	for _, sc := range synth.Scenes() {
		results[sc.String()] = measureSmall(t, sc)
	}
	static := results["static"]
	if static.SteadyBytesMean != 0 || static.DirtyTilesMean != 0 {
		t.Errorf("a still screen sent %d bytes and %.1f tiles per frame after the first", static.SteadyBytesMean, static.DirtyTilesMean)
	}
	for name, r := range results {
		if r.FirstBytes == 0 || r.FirstDirtyTiles != r.Tiles {
			t.Errorf("%s: the first update has %d bytes and %d of %d tiles", name, r.FirstBytes, r.FirstDirtyTiles, r.Tiles)
		}
		if name != "static" && r.SteadyBytesMean == 0 {
			t.Errorf("%s: nothing was sent after the first picture", name)
		}
		if r.EncodeMS < 0 || r.DecodeMS < 0 || r.DiffMS < 0 || r.GrabMS < 0 {
			t.Errorf("%s: negative time", name)
		}
	}
	if !(results["video"].SteadyBytesMean > results["blocks"].SteadyBytesMean) {
		t.Errorf("video (%d bytes a frame) should cost more than blocks (%d)", results["video"].SteadyBytesMean, results["blocks"].SteadyBytesMean)
	}
	if results["video"].DirtyShare < 0.9 || results["blocks"].DirtyShare > 0.2 {
		t.Errorf("dirty share: video %.2f, blocks %.2f", results["video"].DirtyShare, results["blocks"].DirtyShare)
	}
	// Bandwidth is the bytes a frame times the rate, in kilobytes a second.
	v := results["video"]
	if want := float64(v.SteadyBytesMean) * 10 / 1000; v.KBpsAt10 < want*0.99 || v.KBpsAt10 > want*1.01 {
		t.Errorf("video bandwidth at 10 fps = %.1f KB/s, want %.1f", v.KBpsAt10, want)
	}
}

func TestMeasuringTheSameThingTwiceGivesTheSameSizes(t *testing.T) {
	a, b := measureSmall(t, synth.SceneTerminal), measureSmall(t, synth.SceneTerminal)
	if a.FirstBytes != b.FirstBytes || a.SteadyBytesMean != b.SteadyBytesMean || a.SteadyBytesP95 != b.SteadyBytesP95 {
		t.Errorf("sizes differ between runs: %+v vs %+v", a, b)
	}
}

func TestTheCommandPrintsATableOrJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-sizes", "640x480", "-scenes", "static,blocks", "-frames", "4"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"| scene |", "| static |", "| blocks |", "640x480"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the table lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"-sizes", "640x480", "-scenes", "blocks", "-frames", "4", "-json"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var doc struct {
		Machine Machine  `json:"machine"`
		Results []Result `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("-json is not JSON: %v\n%s", err, out.String())
	}
	if len(doc.Results) != 1 || doc.Results[0].Scene != "blocks" || doc.Machine.CPUs == 0 || doc.Machine.Go == "" {
		t.Errorf("doc = %+v", doc)
	}
}

func TestBadArgumentsExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{"-sizes", "wide"}, {"-sizes", "64x64"}, {"-scenes", "screensaver"}, {"-frames", "0"}, {"-fps", "0"}, {"-nonsense"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
