//go:build windows

package vdisplay

import (
	"os"
	"runtime"
	"testing"
	"unsafe"
)

func TestWindowsStructuresHaveTheirSizes(t *testing.T) {
	if n := unsafe.Sizeof(devMode{}); n != 220 {
		t.Errorf("DEVMODEW is %d bytes, want 220", n)
	}
	if n := unsafe.Sizeof(displayDeviceW{}); n != 840 {
		t.Errorf("DISPLAY_DEVICEW is %d bytes, want 840", n)
	}
	if n := unsafe.Sizeof(shellExecuteInfo{}); runtime.GOARCH == "amd64" && n != 112 {
		t.Errorf("SHELLEXECUTEINFOW is %d bytes, want 112", n)
	}
}

// On a PC with a monitor (the CI runner has one): its modes can be listed, the layout can be
// read and names it, and, on CI only, applying it again is accepted.
func TestTheDisplayLayoutCanBeReadAndPutBack(t *testing.T) {
	var active []displayDevice
	for _, d := range displayDevices() {
		if d.active {
			active = append(active, d)
		}
	}
	if len(active) == 0 {
		t.Skip("no active display")
	}
	s := NewWindows()
	defer s.Close()
	modes, err := s.Modes(active[0].name)
	if err != nil || len(modes) == 0 {
		t.Fatalf("modes of %s: %v %v", active[0].name, modes, err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	names, err := sourceNames(snap)
	if err != nil || !containsFold(names, active[0].name) {
		t.Fatalf("the layout names %v (%v); %s is active", names, err, active[0].name)
	}
	if os.Getenv("CI") == "" {
		t.Log("not applying the layout again outside CI")
		return
	}
	if err := s.Restore(snap); err != nil {
		t.Fatalf("applying the current layout again: %v", err)
	}
	if installed, _ := s.Driver(); !installed {
		if err := s.Ready(); err == nil {
			t.Error("ready without the driver")
		}
	}
}
