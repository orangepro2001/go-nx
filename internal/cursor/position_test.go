package cursor

import "testing"

func TestDrawPosition(t *testing.T) {
	for _, c := range []struct {
		name           string
		mx, my, cx, cy int32
		hx, hy         uint32
		wantX, wantY   int32
	}{
		{"primary at origin, arrow with a 0,0 hotspot", 0, 0, 500, 400, 0, 0, 500, 400},
		{"hotspot moves the image up and left", 0, 0, 500, 400, 3, 5, 497, 395},
		{"monitor to the right of the primary", 1920, 0, 2000, 300, 0, 0, 80, 300},
		{"monitor to the left of the primary has a negative origin", -1920, 0, -1000, 300, 3, 5, 917, 295},
		{"monitor above the primary", 0, -1080, 100, -1000, 0, 0, 100, 80},
		{"pointer on another monitor lands off this one", 0, 0, 2500, 100, 0, 0, 2500, 100},
		{"hotspot near the top-left edge draws partly off the monitor", 0, 0, 2, 1, 10, 10, -8, -9},
	} {
		x, y := DrawPosition(c.mx, c.my, c.cx, c.cy, c.hx, c.hy)
		if x != c.wantX || y != c.wantY {
			t.Errorf("%s: DrawPosition = %d,%d, want %d,%d", c.name, x, y, c.wantX, c.wantY)
		}
	}
}
