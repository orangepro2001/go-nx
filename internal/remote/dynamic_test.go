package remote

import (
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
)

func TestCaptureRate(t *testing.T) {
	const after = 3 * time.Second
	for _, c := range []struct {
		name         string
		want, idle   int
		idleFor      time.Duration
		wantCaptured int
	}{
		{"working", 30, 2, 0, 30},
		{"just short of idle", 10, 2, after - time.Millisecond, 10},
		{"idle", 10, 2, after, 2},
		{"idle for long", 10, 2, time.Hour, 2},
		{"no idle rate", 10, 0, time.Hour, 10},
		{"a negative idle rate is no idle rate", 10, -3, time.Hour, 10},
		{"an idle rate that is not slower is ignored", 10, 10, time.Hour, 10},
		{"an idle rate that is faster is ignored", 10, 30, time.Hour, 10},
		{"the boost is idle too", 30, 2, time.Hour, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := captureRate(c.want, c.idle, c.idleFor, after); got != c.wantCaptured {
				t.Errorf("captureRate(%d, %d, %v, %v) = %d, want %d", c.want, c.idle, c.idleFor, after, got, c.wantCaptured)
			}
		})
	}
}

// A viewer that only watches a still screen costs the host little: after IdleAfter
// without a change or an input the host looks at the screen at the idle rate.
func TestIdleSessionIsCapturedAtTheIdleRate(t *testing.T) {
	r := start(t, Options{FPS: 40, IdleFPS: 2, IdleAfter: 300 * time.Millisecond}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.waitView(t, nil)

	busy := grabsDuring(r, 250*time.Millisecond) // still inside IdleAfter of the first picture: 40 per second
	time.Sleep(900 * time.Millisecond)           // well past IdleAfter, and the rate change has taken hold
	idle := grabsDuring(r, time.Second)

	if busy < 4 {
		t.Errorf("%d grabs in 0.25 s soon after connecting, want about 10 (the host must not start slow)", busy)
	}
	if idle < 1 || idle > 4 {
		t.Errorf("%d grabs in a second of idleness at 2 per second", idle)
	}
}

func TestWithoutAnIdleRateTheCaptureNeverSlows(t *testing.T) {
	r := start(t, Options{FPS: 40, IdleAfter: 100 * time.Millisecond}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.waitView(t, nil)
	time.Sleep(500 * time.Millisecond)
	if n := grabsDuring(r, time.Second); n < 25 {
		t.Errorf("%d grabs in a second at 40 per second with no idle rate set", n)
	}
}

// Idleness ends with the viewer's first input, at once: a host that is looking at the
// screen once a second must not make the viewer wait for the next look.
func TestInputEndsIdlenessAtOnce(t *testing.T) {
	r := start(t, Options{FPS: 40, IdleFPS: 1, IdleAfter: 200 * time.Millisecond}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.waitView(t, nil)
	time.Sleep(600 * time.Millisecond) // idle by now, and the next look is up to a second away
	// Align with a look so the next one is a full second away, whatever the phase.
	before := r.src.grabs.Load()
	waitFor(t, "a look at the screen", func() bool { return r.src.grabs.Load() > before })

	sent := time.Now()
	look := r.src.grabs.Load()
	r.client.SendInput(proto.Input{Kind: proto.KindMouseMove, X: 5, Y: 5})
	waitFor(t, "the host to look at once", func() bool { return r.src.grabs.Load() > look })
	if took := time.Since(sent); took > 400*time.Millisecond {
		t.Errorf("the host took %v to look at the screen after the viewer's input; the idle rate is 1 per second", took)
	}
	time.Sleep(100 * time.Millisecond)
	if n := grabsDuring(r, 150*time.Millisecond); n < 2 {
		t.Errorf("%d grabs in 0.15 s after input: the rate did not come back up to 40 per second", n)
	}
}

// A change on the screen ends idleness too, though the host only finds out at its
// next idle look.
func TestAChangeOnTheScreenEndsIdleness(t *testing.T) {
	r := start(t, Options{FPS: 40, IdleFPS: 4, IdleAfter: 200 * time.Millisecond}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.waitView(t, nil)
	time.Sleep(700 * time.Millisecond)
	if n := grabsDuring(r, time.Second); n > 6 {
		t.Fatalf("%d grabs in a second: the session never went idle", n)
	}

	r.src.paint(3, 3, 77)
	r.waitView(t, nil) // the change is noticed by the next idle look, at most 0.25 s away
	if n := grabsDuring(r, 150*time.Millisecond); n < 2 {
		t.Errorf("%d grabs in 0.15 s after the change was sent: the rate did not come back up", n)
	}
}

// The idle rate is a way to save work, never a way to be slower than asked: a viewer
// who is working gets the rate FPSFn names even from an idle start.
func TestFPSFnStillRulesWhileTheViewerIsWorking(t *testing.T) {
	// IdleAfter is longer than the boost's Hold, as it should be: a viewer who is
	// still inside its working time is not idle.
	boost := &Boost{Base: 5, Active: 40, Hold: 500 * time.Millisecond}
	r := start(t, Options{FPS: 5, FPSFn: boost.Rate, OnInput: boost.Touch, IdleFPS: 1, IdleAfter: 700 * time.Millisecond}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.waitView(t, nil)
	time.Sleep(1200 * time.Millisecond) // idle: 1 per second

	r.client.SendInput(proto.Input{Kind: proto.KindMouseMove, X: 5, Y: 5})
	time.Sleep(100 * time.Millisecond)
	if n := grabsDuring(r, 350*time.Millisecond); n < 8 {
		t.Errorf("%d grabs in 0.35 s while the viewer was working with a boost of 40 per second", n)
	}
}
