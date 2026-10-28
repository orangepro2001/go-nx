// Package view holds the arithmetic of showing a remote screen in a window and
// turning what the user does there into input for it. It does not touch the
// window system, so it can be tested anywhere.
package view

import (
	"sort"
	"time"
)

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

// BGRAToRGBA converts pixels from the order frames arrive in (blue, green, red, and
// a fourth byte the protocol leaves undefined) to the order a display wants (red,
// green, blue, opaque). dst and src may be the same slice; a trailing partial pixel
// is left alone. Every viewer converts exactly once: the conversion is not its own
// inverse for the fourth byte.
func BGRAToRGBA(dst, src []byte) {
	for i := 0; i+3 < len(src) && i+3 < len(dst); i += 4 {
		dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 255
	}
}

// Mode is how a remote screen is shown in its window.
type Mode int

const (
	// ModeFit scales the picture to fill the window as far as its aspect ratio allows.
	ModeFit Mode = iota
	// ModeActual shows it at its own size, one remote pixel to one device pixel, so that
	// small text is not smeared by scaling. A picture larger than the window is panned.
	ModeActual
)

// Placement is where the picture goes in a viewport: its scale, and the offset of its
// top-left corner from the viewport's.
type Placement struct{ Scale, OX, OY float64 }

// Place works out where a fw x fh frame goes in a vpW x vpH viewport. In ModeActual the
// picture is centred if it is smaller than the viewport on an axis, and otherwise
// shifted by the pan (how far into the picture the viewport's left and top edges are,
// in device pixels), which is held to what the picture has to give.
func Place(mode Mode, vpW, vpH, fw, fh int, panX, panY float64) Placement {
	if mode != ModeActual {
		s, ox, oy := Fit(vpW, vpH, fw, fh)
		return Placement{s, ox, oy}
	}
	axis := func(vp, f int, pan float64) float64 {
		if f <= vp {
			return float64(vp-f) / 2
		}
		return -min(max(pan, 0), float64(f-vp))
	}
	return Placement{1, axis(vpW, fw, panX), axis(vpH, fh, panY)}
}

// MaxPan is how far a picture can be panned in ModeActual: the part of it outside the
// viewport, on each axis, which is zero where it fits.
func MaxPan(vpW, vpH, fw, fh int) (x, y float64) {
	return float64(max(fw-vpW, 0)), float64(max(fh-vpH, 0))
}

// ToHostAt converts a position in the viewport (relative to its top-left corner) to a pixel
// of the host's screen, for a picture placed as p. The result is clamped to the frame.
func (p Placement) ToHostAt(x, y float64, fw, fh int) (hx, hy int32) {
	return int32(min(max(int((x-p.OX)/p.Scale), 0), fw-1)), int32(min(max(int((y-p.OY)/p.Scale), 0), fh-1))
}

// EdgePan is how far to pan, in device pixels, during dt when the pointer is at (x, y) in a
// vpW x vpH viewport showing a picture bigger than it: a pointer within margin of an edge
// moves the picture the other way, faster the nearer the edge, up to speed pixels a second.
// Only edges the picture extends past are panned, which the caller says with canX, canY.
func EdgePan(x, y float64, vpW, vpH int, margin, speed float64, dt time.Duration, canX, canY bool) (dx, dy float64) {
	axis := func(pos float64, size int) float64 {
		switch {
		case pos < margin:
			return -(margin - max(pos, 0)) / margin
		case pos > float64(size)-margin:
			return (margin - max(float64(size)-pos, 0)) / margin
		}
		return 0
	}
	f := speed * dt.Seconds()
	if canX {
		dx = axis(x, vpW) * f
	}
	if canY {
		dy = axis(y, vpH) * f
	}
	return
}
