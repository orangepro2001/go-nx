package inject

// Windows takes an absolute mouse position as a number from 0 to 65535 across
// the whole span being addressed (one monitor, or the virtual desktop with
// MOUSEEVENTF_VIRTUALDESK) and turns it back into a pixel by scaling:
//
//	pixel = floor(abs * span / 65536)
//
// So to land on pixel p the number sent must be one that scales back to p. The
// smallest such number is ceil(p * 65536 / span); because 65536/span is at least 1
// for any span up to 65536 there is always one, and this choice is exact for every
// pixel, whereas the common p*65535/(span-1) misses by a pixel for some spans.
// The CI job checks this model against real Windows for every pixel of the
// runner's screen.

// normalize is the absolute coordinate to send to land on pixel pos of a span of n
// pixels. pos is clamped into [0, n-1].
func normalize(pos, n int) int32 {
	if n <= 1 {
		return 0
	}
	pos = min(max(pos, 0), n-1)
	return int32((int64(pos)*65536 + int64(n) - 1) / int64(n))
}

// denormalize is what Windows does with an absolute coordinate: the pixel of a
// span of n pixels it addresses.
func denormalize(abs int32, n int) int {
	return int(int64(abs) * int64(n) / 65536)
}

// virtualAbs is the absolute coordinate, over the virtual desktop that starts at
// virtualX and is virtualW pixels wide, for pixel x of a monitor whose left edge
// is originX in virtual-desktop coordinates. The same works for y.
func virtualAbs(x, originX, virtualX, virtualW int) int32 {
	return normalize(originX+x-virtualX, virtualW)
}

// clamp limits v to a pixel of a span of n pixels.
func clamp(v, n int) int { return min(max(v, 0), n-1) }

// mapping says where a monitor's pixels sit in absolute coordinates.
type mapping struct {
	w, h             int  // the monitor's size
	originX, originY int  // its top-left in virtual-desktop pixels
	vx, vy, vw, vh   int  // the virtual desktop
	virtual          bool // address the virtual desktop; needed for any monitor but the primary
}

// abs returns the absolute coordinates for pixel (x, y) of the monitor, clamped to
// it. With virtual set they address the whole virtual desktop, otherwise the
// primary monitor alone.
func (m *mapping) abs(x, y int) (ax, ay int32) {
	x, y = clamp(x, m.w), clamp(y, m.h)
	if m.virtual && m.vw > 0 && m.vh > 0 {
		return virtualAbs(x, m.originX, m.vx, m.vw), virtualAbs(y, m.originY, m.vy, m.vh)
	}
	return normalize(x, m.w), normalize(y, m.h)
}
