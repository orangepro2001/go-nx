package app

import (
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/ui/kit"
)

func TestKeyRepeatUsesTheClockNotTheFrameCount(t *testing.T) {
	var r keyRepeater
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ev, next := r.poll(t0, []kit.Key{kit.KeyBackspace}, []kit.Key{kit.KeyBackspace})
	if len(ev) != 1 || ev[0] != kit.KeyBackspace {
		t.Fatalf("press = %v", ev)
	}
	if want := t0.Add(uiRepeatAfter); !next.Equal(want) {
		t.Errorf("next = %v, want the first repeat at %v", next, want)
	}
	// Frames at odd times before the delay: nothing.
	for _, ms := range []int{10, 130, 399} {
		if ev, _ := r.poll(t0.Add(time.Duration(ms)*time.Millisecond), nil, []kit.Key{kit.KeyBackspace}); len(ev) != 0 {
			t.Errorf("repeated after %d ms: %v", ms, ev)
		}
	}
	ev, next = r.poll(t0.Add(uiRepeatAfter), nil, []kit.Key{kit.KeyBackspace})
	if len(ev) != 1 {
		t.Fatalf("no repeat at the delay: %v", ev)
	}
	if want := t0.Add(uiRepeatAfter + uiRepeatEvery); !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
	// A frame that is late: one repeat, not a burst to catch up.
	ev, _ = r.poll(t0.Add(2*time.Second), nil, []kit.Key{kit.KeyBackspace})
	if len(ev) != 1 {
		t.Errorf("a late frame gave %d repeats", len(ev))
	}
	// Released: forgotten, and nothing more.
	ev, next = r.poll(t0.Add(3*time.Second), nil, nil)
	if len(ev) != 0 || !next.IsZero() {
		t.Errorf("after release: %v, next %v", ev, next)
	}
}

func TestEnterTabEscapeAndSpaceDoNotRepeat(t *testing.T) {
	var r keyRepeater
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	keys := []kit.Key{kit.KeyEnter, kit.KeyTab, kit.KeyEscape, kit.KeySpace}
	if ev, next := r.poll(t0, keys, keys); len(ev) != 4 || !next.IsZero() {
		t.Fatalf("press: %v, next %v (they act once, and need no wake-up)", ev, next)
	}
	if ev, _ := r.poll(t0.Add(5*time.Second), nil, keys); len(ev) != 0 {
		t.Errorf("held keys repeated: %v", ev)
	}
}

func TestPressAgainAfterReleaseIsANewPress(t *testing.T) {
	var r keyRepeater
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r.poll(t0, []kit.Key{kit.KeyLeft}, []kit.Key{kit.KeyLeft})
	r.poll(t0.Add(50*time.Millisecond), nil, nil)
	ev, next := r.poll(t0.Add(100*time.Millisecond), []kit.Key{kit.KeyLeft}, []kit.Key{kit.KeyLeft})
	if len(ev) != 1 || !next.Equal(t0.Add(100*time.Millisecond+uiRepeatAfter)) {
		t.Errorf("second press: %v, next %v", ev, next)
	}
}
