// Package cursor holds the geometry of drawing the system pointer into a captured
// monitor. It is plain arithmetic so it can be tested anywhere.
package cursor

// DrawPosition converts the pointer's hotspot, in virtual-desktop pixels, into the
// top-left corner at which the pointer image must be drawn inside the captured
// monitor's own pixels: subtract the monitor's origin (which is negative for a
// monitor left of or above the primary one) and the hotspot, which is the offset
// of the pixel the pointer "points at" from the image's top-left. The result can
// be negative or beyond the monitor; drawing clips it.
func DrawPosition(monitorX, monitorY, cursorX, cursorY int32, hotspotX, hotspotY uint32) (int32, int32) {
	return cursorX - monitorX - int32(hotspotX), cursorY - monitorY - int32(hotspotY)
}
