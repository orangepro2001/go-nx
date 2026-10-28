// Package view holds the arithmetic of showing a remote screen in a window and
// turning what the user does there into input for it. It does not touch the
// window system, so it can be tested anywhere.
package view

import "sort"

// Fit returns the scale and offset that show a fw x fh frame centred in a
// ww x wh area without distorting its aspect ratio. Sizes are physical pixels.
func Fit(ww, wh, fw, fh int) (scale, ox, oy float64) {
	scale = min(float64(ww)/float64(fw), float64(wh)/float64(fh))
	return scale, (float64(ww) - float64(fw)*scale) / 2, (float64(wh) - float64(fh)*scale) / 2
}

// ToHost converts a window position (cx, cy) to a pixel of the host's screen,
// for a frame of fw x fh shown with Fit in the viewport at (vpX, vpY) of size
// vpW x vpH. The result is clamped to the frame, so a drag that leaves the window
// still ends at its edge; inside says whether the position was in the viewport.
func ToHost(cx, cy, vpX, vpY, vpW, vpH, fw, fh int) (x, y int32, inside bool) {
	scale, ox, oy := Fit(vpW, vpH, fw, fh)
	x = int32(min(max(int((float64(cx-vpX)-ox)/scale), 0), fw-1))
	y = int32(min(max(int((float64(cy-vpY)-oy)/scale), 0), fh-1))
	inside = cx >= vpX && cx < vpX+vpW && cy >= vpY && cy < vpY+vpH
	return
}

// KeyEvent is a key going down or up. Code is a Windows virtual-key code, with
// 0x100 set for an extended key.
type KeyEvent struct {
	Code uint16
	Down bool
}

// IsModifier reports whether code is Shift, Ctrl, Alt or a Windows key.
func IsModifier(code uint16) bool {
	switch code & 0xFF {
	case 0x10, 0x11, 0x12, 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0x5B, 0x5C:
		return true
	}
	return false
}

// OrderKeyEvents sorts, in place, key events that happened in the same instant
// into the order a keyboard would deliver them, so the host sees Ctrl+Shift+A as
// Ctrl, Shift, A and not in whatever order a map happened to iterate:
//
//  1. releases of ordinary keys
//  2. presses of modifiers
//  3. presses of ordinary keys
//  4. releases of modifiers
//
// Within each group the order is by key code, so the result does not depend on
// the order the events came in.
func OrderKeyEvents(ev []KeyEvent) {
	rank := func(e KeyEvent) int {
		switch {
		case !e.Down && !IsModifier(e.Code):
			return 0
		case e.Down && IsModifier(e.Code):
			return 1
		case e.Down:
			return 2
		}
		return 3
	}
	sort.SliceStable(ev, func(i, j int) bool {
		if ri, rj := rank(ev[i]), rank(ev[j]); ri != rj {
			return ri < rj
		}
		return ev[i].Code < ev[j].Code
	})
}

// ReleaseOrder returns codes in the order to let go of them: ordinary keys first,
// modifiers last, each group by code.
func ReleaseOrder(codes []uint16) []uint16 {
	out := append([]uint16(nil), codes...)
	sort.Slice(out, func(i, j int) bool {
		if mi, mj := IsModifier(out[i]), IsModifier(out[j]); mi != mj {
			return !mi
		}
		return out[i] < out[j]
	})
	return out
}
