// Package capture grabs a monitor of the Windows desktop with GDI BitBlt. The
// Windows implementation is in capture_windows.go; monitor descriptions and the
// choice of one live here so they can be tested anywhere.
package capture

import (
	"fmt"
	"strconv"
	"strings"
)

// Display describes one monitor in virtual-desktop coordinates, which are in
// physical pixels once the process is DPI aware.
type Display struct {
	Index   int
	Device  string // "\\.\DISPLAY1"
	X, Y    int32  // top-left in virtual-desktop pixels; negative for a monitor left of or above the primary
	W, H    int32
	Primary bool
	DPI     int // effective DPI, 96 when unknown
}

// String is for logs and the status line.
func (d Display) String() string {
	return fmt.Sprintf("%s %dx%d at %d,%d dpi=%d", d.Device, d.W, d.H, d.X, d.Y, d.DPI)
}

// SelectDisplay picks a monitor. selector is "" or "primary" for the primary one,
// a zero-based index as Displays lists them, or a device name such as
// `\\.\DISPLAY2` (also accepted as `DISPLAY2` or `display2`). The device name is
// the choice that survives a reboot or a monitor being plugged in; an index may
// not.
func SelectDisplay(all []Display, selector string) (Display, error) {
	if len(all) == 0 {
		return Display{}, fmt.Errorf("capture: no monitors")
	}
	sel := strings.TrimSpace(selector)
	switch strings.ToLower(sel) {
	case "", "primary":
		for _, d := range all {
			if d.Primary {
				return d, nil
			}
		}
		return all[0], nil
	}
	if i, err := strconv.Atoi(sel); err == nil {
		if i < 0 || i >= len(all) {
			return Display{}, fmt.Errorf("capture: display index %d out of range (found %d)", i, len(all))
		}
		return all[i], nil
	}
	want := sel
	if !strings.HasPrefix(want, `\\.`) {
		want = `\\.\` + want // "DISPLAY2" is short for \\.\DISPLAY2
	}
	for _, d := range all {
		if strings.EqualFold(d.Device, want) {
			return d, nil
		}
	}
	names := make([]string, len(all))
	for i, d := range all {
		names[i] = d.Device
	}
	return Display{}, fmt.Errorf("capture: display %q not found (have %s)", selector, strings.Join(names, ", "))
}
