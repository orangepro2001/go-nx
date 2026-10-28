package app

import (
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/kit"
)

// Key repeat for the interface's own keys: a key held down starts repeating after
// uiRepeatAfter and then repeats every uiRepeatEvery. It is done by the clock, not by counting
// frames, because the interface draws a frame only when something happens, so the number
// of frames says nothing about how long a key has been down.
const (
	uiRepeatAfter = 400 * time.Millisecond
	uiRepeatEvery = 35 * time.Millisecond
)

type keyHold struct {
	pressed time.Time
	last    time.Time
}

// keyRepeater turns "which keys are down" into key events with repeats.
type keyRepeater struct {
	held map[kit.Key]*keyHold
}

// poll returns the keys to act on now, given the keys that went down since the last call
// (just) and the ones that are down (down). next is when a held key repeats next, the zero
// time if no key is held.
func (r *keyRepeater) poll(now time.Time, just, down []kit.Key) (events []kit.Key, next time.Time) {
	if r.held == nil {
		r.held = map[kit.Key]*keyHold{}
	}
	isDown := map[kit.Key]bool{}
	for _, k := range down {
		isDown[k] = true
	}
	for k := range r.held {
		if !isDown[k] {
			delete(r.held, k)
		}
	}
	for _, k := range just {
		r.held[k] = &keyHold{pressed: now, last: now}
		events = append(events, k)
	}
	for k, h := range r.held {
		if !repeats(k) {
			continue
		}
		due := h.last.Add(uiRepeatEvery)
		if h.last.Equal(h.pressed) {
			due = h.pressed.Add(uiRepeatAfter)
		}
		if !now.Before(due) {
			events = append(events, k)
			h.last = now
			due = now.Add(uiRepeatEvery)
		}
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	return events, next
}

// repeats reports whether holding the key repeats it. Enter, Tab, Escape and Space act
// once per press: a held Enter must not click a button over and over.
func repeats(k kit.Key) bool {
	switch k {
	case kit.KeyBackspace, kit.KeyDelete, kit.KeyLeft, kit.KeyRight, kit.KeyUp, kit.KeyDown, kit.KeyHome, kit.KeyEnd, kit.KeyPageUp, kit.KeyPageDown:
		return true
	}
	return false
}
