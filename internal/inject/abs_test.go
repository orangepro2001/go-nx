package inject

import "testing"

// Every pixel of every span maps to a number that Windows maps back to the same
// pixel. The spans are the widths and heights people actually have.
func TestNormalizeIsExactForEveryPixel(t *testing.T) {
	for _, n := range []int{1, 2, 3, 100, 320, 640, 800, 1024, 1080, 1280, 1366, 1440, 1600, 1680, 1920, 2048, 2160,
		2560, 3000, 3440, 3840, 4096, 5120, 6016, 7680, 8192, 11520, 16384} {
		prev := int32(-1)
		for p := 0; p < n; p++ {
			abs := normalize(p, n)
			if abs < 0 || abs > 65535 {
				t.Fatalf("span %d pixel %d: abs %d is outside 0..65535", n, p, abs)
			}
			if got := denormalize(abs, n); got != p {
				t.Fatalf("span %d: pixel %d -> abs %d -> pixel %d", n, p, abs, got)
			}
			if abs < prev {
				t.Fatalf("span %d: abs went backwards at pixel %d", n, p)
			}
			prev = abs
		}
	}
}

func TestNormalizeClampsAndHandlesTinySpans(t *testing.T) {
	if normalize(-5, 1920) != normalize(0, 1920) || normalize(5000, 1920) != normalize(1919, 1920) {
		t.Error("positions outside the span are not clamped to its ends")
	}
	if normalize(0, 1920) != 0 {
		t.Error("the first pixel is not 0")
	}
	if normalize(7, 0) != 0 || normalize(7, 1) != 0 {
		t.Error("a span of 0 or 1 pixels must give 0, not divide by zero")
	}
}

// The old formula, kept to show what changed: it is not exact for every span.
func TestTheOldFormulaMissedPixels(t *testing.T) {
	misses := 0
	for _, n := range []int{1280, 1366, 1920} {
		for p := 0; p < n; p++ {
			if denormalize(int32(p*65535/(n-1)), n) != p {
				misses++
			}
		}
	}
	if misses == 0 {
		t.Skip("the old formula happened to be exact for these spans; the new one is still checked above")
	}
	t.Logf("the old p*65535/(n-1) formula misses %d pixels over these three spans", misses)
}

func TestVirtualAbsAddressesMonitorsLeftAndAboveThePrimary(t *testing.T) {
	// Virtual desktop: a 1920-wide monitor left of a 2560-wide primary.
	const vx, vw = -1920, 1920 + 2560
	for _, tc := range []struct {
		name      string
		originX   int
		x         int
		wantPixel int // in virtual coordinates, relative to vx
	}{
		{"left monitor, first pixel", -1920, 0, 0},
		{"left monitor, last pixel", -1920, 1919, 1919},
		{"primary, first pixel", 0, 0, 1920},
		{"primary, middle", 0, 1280, 1920 + 1280},
		{"primary, last pixel", 0, 2559, 1920 + 2559},
	} {
		abs := virtualAbs(tc.x, tc.originX, vx, vw)
		if got := denormalize(abs, vw); got != tc.wantPixel {
			t.Errorf("%s: landed on virtual pixel %d, want %d", tc.name, got, tc.wantPixel)
		}
	}
}

// A two-monitor desktop with the primary on the right: every pixel of both
// monitors lands where it should, and positions outside a monitor are clamped to it.
func TestMappingAddressesEachMonitorExactly(t *testing.T) {
	left := mapping{w: 1920, h: 1080, originX: -1920, originY: -200, vx: -1920, vy: -200, vw: 1920 + 2560, vh: 1640, virtual: true}
	right := mapping{w: 2560, h: 1440, originX: 0, originY: 0, vx: -1920, vy: -200, vw: 1920 + 2560, vh: 1640, virtual: true}
	for name, m := range map[string]mapping{"left": left, "right": right} {
		for x := 0; x < m.w; x++ {
			ax, _ := m.abs(x, 0)
			if got := denormalize(ax, m.vw) + m.vx; got != m.originX+x {
				t.Fatalf("%s: x=%d landed on virtual x=%d, want %d", name, x, got, m.originX+x)
			}
		}
		for y := 0; y < m.h; y++ {
			_, ay := m.abs(0, y)
			if got := denormalize(ay, m.vh) + m.vy; got != m.originY+y {
				t.Fatalf("%s: y=%d landed on virtual y=%d, want %d", name, y, got, m.originY+y)
			}
		}
		ax, ay := m.abs(-50, 99999)
		lx, ly := m.abs(0, m.h-1)
		if ax != mustAbsX(m, 0) || ay != ly {
			t.Errorf("%s: out-of-range position not clamped: got %d,%d want %d,%d", name, ax, ay, mustAbsX(m, 0), ly)
		}
		_ = lx
	}
	// Without virtual set only the primary monitor's own span counts.
	primary := mapping{w: 1366, h: 768}
	for x := 0; x < primary.w; x++ {
		ax, _ := primary.abs(x, 0)
		if denormalize(ax, primary.w) != x {
			t.Fatalf("primary: x=%d did not round-trip", x)
		}
	}
}

func mustAbsX(m mapping, x int) int32 {
	ax, _ := m.abs(x, 0)
	return ax
}
