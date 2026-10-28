package capture

import (
	"strings"
	"testing"
)

var threeMonitors = []Display{
	{Index: 0, Device: `\\.\DISPLAY2`, X: -1920, Y: 0, W: 1920, H: 1080, DPI: 96},
	{Index: 1, Device: `\\.\DISPLAY1`, X: 0, Y: 0, W: 2560, H: 1440, Primary: true, DPI: 144},
	{Index: 2, Device: `\\.\DISPLAY3`, X: 2560, Y: -200, W: 1280, H: 1024, DPI: 96},
}

func TestSelectDisplay(t *testing.T) {
	for _, c := range []struct {
		sel  string
		want string
	}{
		{"", `\\.\DISPLAY1`},
		{"primary", `\\.\DISPLAY1`},
		{" Primary ", `\\.\DISPLAY1`},
		{"0", `\\.\DISPLAY2`},
		{"2", `\\.\DISPLAY3`},
		{`\\.\DISPLAY3`, `\\.\DISPLAY3`},
		{`\\.\display3`, `\\.\DISPLAY3`},
		{"DISPLAY2", `\\.\DISPLAY2`},
		{"display2", `\\.\DISPLAY2`},
	} {
		d, err := SelectDisplay(threeMonitors, c.sel)
		if err != nil || d.Device != c.want {
			t.Errorf("SelectDisplay(%q) = %q, %v; want %q", c.sel, d.Device, err, c.want)
		}
	}
}

func TestSelectDisplayErrorsSayWhatIsThere(t *testing.T) {
	for _, sel := range []string{"3", "-1", "DISPLAY9", `\\.\DISPLAY0`, "nonsense"} {
		_, err := SelectDisplay(threeMonitors, sel)
		if err == nil {
			t.Errorf("SelectDisplay(%q) succeeded", sel)
			continue
		}
		if strings.HasPrefix(sel, "D") || strings.HasPrefix(sel, `\`) || sel == "nonsense" {
			for _, d := range threeMonitors {
				if !strings.Contains(err.Error(), d.Device) {
					t.Errorf("error for %q does not list %s: %v", sel, d.Device, err)
				}
			}
		}
	}
	if _, err := SelectDisplay(nil, ""); err == nil {
		t.Error("selecting from no monitors succeeded")
	}
}

func TestPrimaryFallsBackToTheFirstWhenNoneIsMarked(t *testing.T) {
	d, err := SelectDisplay([]Display{{Device: "a"}, {Device: "b"}}, "")
	if err != nil || d.Device != "a" {
		t.Fatalf("got %q, %v", d.Device, err)
	}
}

func TestDisplayString(t *testing.T) {
	if got := threeMonitors[0].String(); got != `\\.\DISPLAY2 1920x1080 at -1920,0 dpi=96` {
		t.Errorf("String = %q", got)
	}
}
