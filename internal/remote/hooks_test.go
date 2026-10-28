package remote

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
)

// grabsDuring is how many times the host looked at the screen in d.
func grabsDuring(r *rig, d time.Duration) int64 {
	before := r.src.grabs.Load()
	time.Sleep(d)
	return r.src.grabs.Load() - before
}

// FPSFn lets the caller change the capture rate while a session runs, so that a
// viewer who is working gets a smoother picture than one who is only watching.
func TestFPSFnChangesTheCaptureRate(t *testing.T) {
	var rate atomic.Int64
	rate.Store(5)
	r := start(t, Options{FPS: 5, FPSFn: func() int { return int(rate.Load()) }}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	slow := grabsDuring(r, 800*time.Millisecond) // about 4 at 5 per second

	rate.Store(100)
	time.Sleep(300 * time.Millisecond) // the new rate applies from the next tick, at most 200 ms away
	fast := grabsDuring(r, 800*time.Millisecond)

	if slow > 8 {
		t.Errorf("%d grabs in 0.8 s at 5 per second", slow)
	}
	if fast < 25 || fast < 3*slow {
		t.Errorf("%d grabs in 0.8 s after asking for 100 per second (%d when slow)", fast, slow)
	}

	rate.Store(5)
	time.Sleep(300 * time.Millisecond)
	if again := grabsDuring(r, 800*time.Millisecond); again > 12 {
		t.Errorf("the rate did not come back down: %d grabs in 0.8 s", again)
	}
}

// A rate that makes no sense keeps the current one: zero would be a division by
// zero, a huge one a busy loop.
func TestFPSFnNonsenseIsIgnoredOrClamped(t *testing.T) {
	for name, nonsense := range map[string]int{"zero": 0, "negative": -7} {
		t.Run(name, func(t *testing.T) {
			r := start(t, Options{FPS: 40, FPSFn: func() int { return nonsense }}, true, 0)
			if r.dialErr != nil {
				t.Fatal(r.dialErr)
			}
			if n := grabsDuring(r, 800*time.Millisecond); n < 12 {
				t.Errorf("only %d grabs in 0.8 s: the configured 40 per second was lost", n)
			}
		})
	}
	t.Run("huge", func(t *testing.T) {
		r := start(t, Options{FPS: 10, FPSFn: func() int { return 1 << 30 }}, true, 0)
		if r.dialErr != nil {
			t.Fatal(r.dialErr)
		}
		time.Sleep(300 * time.Millisecond)
		n := grabsDuring(r, time.Second)
		if n == 0 || n > int64(MaxFPS)*3/2 {
			t.Errorf("%d grabs in a second with a request for 2^30 per second, want at most about %d", n, MaxFPS)
		}
	})
}

// OnInput tells the application that the viewer is doing something; OnFrame what
// went onto the wire.
func TestOnInputAndOnFrame(t *testing.T) {
	var inputs atomic.Int32
	var mu sync.Mutex
	var seqs []uint32
	var sizes []int
	r := start(t, Options{
		OnInput: func() { inputs.Add(1) },
		OnFrame: func(seq uint32, n int) {
			mu.Lock()
			seqs, sizes = append(seqs, seq), append(sizes, n)
			mu.Unlock()
		},
	}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.waitView(t, nil)
	if inputs.Load() != 0 {
		t.Fatalf("OnInput ran %d times before the viewer did anything (acks are not input)", inputs.Load())
	}

	r.client.SendInput(proto.Input{Kind: proto.KindMouseMove, X: 5, Y: 5})
	r.client.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: 0x41, OpID: 9})
	r.client.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: 0x41, OpID: 9}) // a retry: still input
	if err := r.client.SendText(10, "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the four inputs to reach the host", func() bool { return inputs.Load() == 4 })

	r.src.paint(3, 3, 200)
	r.waitView(t, nil)
	mu.Lock()
	defer mu.Unlock()
	if len(seqs) < 2 {
		t.Fatalf("OnFrame ran %d times, want at least 2 (the first picture and a change)", len(seqs))
	}
	for i, s := range seqs {
		if s != uint32(i+1) {
			t.Fatalf("frame sequence numbers = %v, want 1, 2, 3...", seqs)
		}
		if sizes[i] <= 0 {
			t.Fatalf("frame %d reported %d bytes", s, sizes[i])
		}
	}
	if sizes[0] <= sizes[len(sizes)-1] {
		t.Errorf("the first, complete picture (%d bytes) should be larger than the last small change (%d bytes)", sizes[0], sizes[len(sizes)-1])
	}
}

// Boost is the usual use of both hooks: the application hands Touch to OnInput and
// Rate to FPSFn, and the picture speeds up for a while after the viewer's last act.
func TestBoostRaisesTheRateAfterInputAndLetsItFall(t *testing.T) {
	clk := newClock()
	b := &Boost{Base: 10, Active: 30, Hold: 2 * time.Second, now: clk.now}
	if got := b.Rate(); got != 10 {
		t.Fatalf("rate before any input = %d, want 10", got)
	}
	b.Touch()
	if got := b.Rate(); got != 30 {
		t.Fatalf("rate right after input = %d, want 30", got)
	}
	clk.advance(1900 * time.Millisecond)
	if got := b.Rate(); got != 30 {
		t.Fatalf("rate 1.9 s after input = %d, want 30", got)
	}
	b.Touch() // more input extends it
	clk.advance(1900 * time.Millisecond)
	if got := b.Rate(); got != 30 {
		t.Fatalf("rate 1.9 s after the second input = %d, want 30", got)
	}
	clk.advance(200 * time.Millisecond)
	if got := b.Rate(); got != 10 {
		t.Fatalf("rate 2.1 s after the last input = %d, want 10", got)
	}
}

func TestBoostDefaultsAndSafety(t *testing.T) {
	var b Boost
	if got := b.Rate(); got != DefaultFPS {
		t.Fatalf("zero Boost rate = %d, want %d", got, DefaultFPS)
	}
	b.Touch()
	if got := b.Rate(); got != DefaultFPS {
		t.Fatalf("zero Boost rate after input = %d: with no Active it must not boost", got)
	}
	slower := &Boost{Base: 20, Active: 5} // a boost below the base is no boost
	slower.Touch()
	if got := slower.Rate(); got != 20 {
		t.Fatalf("rate = %d, want the base 20", got)
	}
	// Used from two goroutines, as it is: input arrives on one, frames are timed on another.
	var wg sync.WaitGroup
	shared := &Boost{Base: 10, Active: 30, Hold: time.Second}
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				shared.Touch()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				shared.Rate()
			}
		}()
	}
	wg.Wait()
}

func TestBoostRateAtFollowsABaseThatChanges(t *testing.T) {
	clk := newClock()
	b := &Boost{Active: 40, Hold: time.Second, now: clk.now}
	if got := b.RateAt(5); got != 5 {
		t.Fatalf("idle RateAt(5) = %d", got)
	}
	b.Touch()
	if got := b.RateAt(5); got != 40 {
		t.Fatalf("working RateAt(5) = %d, want 40", got)
	}
	if got := b.RateAt(50); got != 50 { // the base rose above the boost: no boost
		t.Fatalf("working RateAt(50) = %d, want 50", got)
	}
	clk.advance(2 * time.Second)
	if got := b.RateAt(25); got != 25 {
		t.Fatalf("idle RateAt(25) = %d", got)
	}
}
