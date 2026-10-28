package vdisplay

import (
	"encoding/binary"
	"testing"
)

// layout makes a topology of one path whose source and target modes are at si and ti.
func layout(si, ti uint32, kinds ...uint32) Topology {
	t := Topology{Paths: make([]byte, pathSize), Modes: make([]byte, modeSize*len(kinds))}
	binary.LittleEndian.PutUint32(t.Paths[12:], si)
	binary.LittleEndian.PutUint32(t.Paths[32:], ti)
	for i, k := range kinds {
		binary.LittleEndian.PutUint32(t.Modes[i*modeSize:], k)
	}
	return t
}

func TestOnlyWellFormedLayoutsGoToWindows(t *testing.T) {
	if err := validTopology(layout(0, 1, 1, 2)); err != nil {
		t.Errorf("a good layout: %v", err)
	}
	if err := validTopology(layout(0xFFFFFFFF, 0, 2)); err != nil {
		t.Errorf("a path without a source mode: %v", err)
	}
	bad := map[string]Topology{
		"source index past the modes": layout(5, 1, 1, 2),
		"source index at a target":    layout(1, 0, 1, 2),
		"no paths":                    {Modes: make([]byte, modeSize)},
		"a cut path":                  {Paths: make([]byte, pathSize-1), Modes: make([]byte, modeSize)},
		"too many paths":              {Paths: make([]byte, 129*pathSize), Modes: make([]byte, modeSize)},
	}
	for what, l := range bad {
		if validTopology(l) == nil {
			t.Errorf("%s passed", what)
		}
	}
}
