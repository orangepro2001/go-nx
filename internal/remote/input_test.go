package remote

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
)

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *manualClock { return &manualClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

// failingInjector records everything and fails the first fails calls that match.
type failingInjector struct {
	fakeInjector
	failKeyUps int
	keyUpTries int // every attempt to release a key, failed or not
}

func (f *failingInjector) Inject(in proto.Input) error {
	f.mu.Lock()
	if in.Kind == proto.KindKeyUp {
		f.keyUpTries++
		if f.failKeyUps > 0 {
			f.failKeyUps--
			f.mu.Unlock()
			return errors.New("injector: the desktop refused the key (secure desktop?)")
		}
	}
	f.mu.Unlock()
	return f.fakeInjector.Inject(in)
}

func (f *failingInjector) log() []proto.Input {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Input(nil), f.seen...)
}

func press(t *testing.T, s *sessionInputState, in proto.Input) {
	t.Helper()
	if err := s.Inject(in); err != nil {
		t.Fatalf("Inject(%+v): %v", in, err)
	}
}

// Whatever order a map happened to iterate in, letting go must be: buttons first,
// then keys, each starting with the one pressed last. Ctrl+Shift+click is released
// as the click, then Shift, then Ctrl, as a keyboard and mouse would do it.
func TestReleaseOrderIsButtonsFirstThenKeysMostRecentFirst(t *testing.T) {
	for run := 0; run < 20; run++ { // a map would pass by luck some of the time
		inj := &fakeInjector{}
		c := NewInputController(inj)
		s := c.newSession()
		s.Activate(nil)
		for _, in := range []proto.Input{
			{Kind: proto.KindKeyDown, Arg: 0xA2}, // Ctrl
			{Kind: proto.KindKeyDown, Arg: 0xA0}, // Shift
			{Kind: proto.KindKeyDown, Arg: 0x41}, // A
			{Kind: proto.KindMouseDown, X: 10, Y: 10, Arg: 2},
			{Kind: proto.KindMouseDown, X: 10, Y: 10, Arg: 0},
			{Kind: proto.KindKeyDown, Arg: 0x41}, // auto-repeat of A: no new entry
		} {
			press(t, s, in)
		}
		inj.mu.Lock()
		inj.seen = nil
		inj.mu.Unlock()
		if err := s.Release(); err != nil {
			t.Fatal(err)
		}
		got := inj.seen
		want := []proto.Input{
			{Kind: proto.KindMouseUp, X: proto.NoMove, Y: proto.NoMove, Arg: 0},
			{Kind: proto.KindMouseUp, X: proto.NoMove, Y: proto.NoMove, Arg: 2},
			{Kind: proto.KindKeyUp, Arg: 0x41},
			{Kind: proto.KindKeyUp, Arg: 0xA0},
			{Kind: proto.KindKeyUp, Arg: 0xA2},
		}
		if len(got) != len(want) {
			t.Fatalf("run %d: released %d things, want %d: %+v", run, len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("run %d: release %d = %+v, want %+v", run, i, got[i], want[i])
			}
		}
	}
}

// Letting go on a viewer's behalf must not drag the pointer back to where the
// button went down.
func TestCleanupReleaseDoesNotMoveThePointer(t *testing.T) {
	inj := &fakeInjector{}
	c := NewInputController(inj)
	s := c.newSession()
	s.Activate(nil)
	press(t, s, proto.Input{Kind: proto.KindMouseDown, X: 111, Y: 222, Arg: 1})
	inj.mu.Lock()
	inj.seen = nil
	inj.mu.Unlock()
	s.Release()
	if len(inj.seen) != 1 || inj.seen[0].X != proto.NoMove || inj.seen[0].Y != proto.NoMove {
		t.Fatalf("clean-up release = %+v, want one mouse-up with NoMove coordinates", inj.seen)
	}
}

// A release that fails must not be forgotten: a key left pressed on the host is
// worse than a delay. The next time the controller is used it tries again.
func TestFailedReleasesAreRetried(t *testing.T) {
	inj := &failingInjector{failKeyUps: 2}
	c := NewInputController(inj)
	first := c.newSession()
	first.Activate(nil)
	press(t, first, proto.Input{Kind: proto.KindKeyDown, Arg: 0xA2})
	if err := first.Release(); err == nil {
		t.Fatal("Release did not report the failure")
	}
	// The next session's first act is to retry; it fails once more (the second
	// failure), and succeeds on the attempt after.
	second := c.newSession()
	second.Activate(nil)
	press(t, second, proto.Input{Kind: proto.KindKeyDown, Arg: 0x41})
	press(t, second, proto.Input{Kind: proto.KindKeyDown, Arg: 0x42})

	inj.mu.Lock()
	tries := inj.keyUpTries
	inj.mu.Unlock()
	if tries != 3 { // two refused attempts and the one that worked
		t.Fatalf("Ctrl release was attempted %d times, want 3 (2 refused, then success): %+v", tries, inj.log())
	}
	succeeded := 0
	for _, in := range inj.log() {
		if in.Kind == proto.KindKeyUp && in.Arg == 0xA2 {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("Ctrl was released %d times, want exactly once", succeeded)
	}
	c.mu.Lock()
	left := len(c.stuck)
	c.mu.Unlock()
	if left != 0 {
		t.Errorf("%d releases are still waiting after a success", left)
	}
}

// A viewer that vanishes while holding keys is noticed by the connection only
// after the QUIC idle timeout. The input side does not wait that long.
func TestIdleWatchdogReleasesWhatASilentViewerHolds(t *testing.T) {
	clk := newClock()
	inj := &fakeInjector{}
	c := NewInputController(inj)
	c.now = clk.now
	s := c.newSession()
	s.Activate(nil)
	press(t, s, proto.Input{Kind: proto.KindKeyDown, Arg: 0xA0})
	press(t, s, proto.Input{Kind: proto.KindMouseDown, X: 1, Y: 2, Arg: 0})

	clk.advance(10 * time.Second)
	if released, err := s.ReleaseIfIdle(20 * time.Second); released || err != nil {
		t.Fatalf("released after 10 s of a 20 s allowance: %v, %v", released, err)
	}
	press(t, s, proto.Input{Kind: proto.KindKeyDown, Arg: 0xA0}) // an auto-repeat counts as activity
	clk.advance(15 * time.Second)
	if released, _ := s.ReleaseIfIdle(20 * time.Second); released {
		t.Fatal("released although the viewer was heard from 15 s ago")
	}
	clk.advance(10 * time.Second)
	released, err := s.ReleaseIfIdle(20 * time.Second)
	if !released || err != nil {
		t.Fatalf("not released after 25 s of silence: %v, %v", released, err)
	}
	if inj.count(proto.KindKeyUp) != 1 || inj.count(proto.KindMouseUp) != 1 {
		t.Errorf("released %d keys and %d buttons, want 1 and 1", inj.count(proto.KindKeyUp), inj.count(proto.KindMouseUp))
	}
	// Nothing held: silence is not an event.
	clk.advance(time.Hour)
	if released, _ := s.ReleaseIfIdle(time.Second); released {
		t.Error("released with nothing held")
	}
}

// Win+L locks the host and nobody can unlock it from the viewer. It is refused by
// default; Win alone, L alone, and Win+other keys are untouched.
func TestWinLIsRefusedUnlessAllowed(t *testing.T) {
	for _, allow := range []bool{false, true} {
		inj := &fakeInjector{}
		c := NewInputController(inj)
		c.SetAllowWinL(allow)
		s := c.newSession()
		s.Activate(nil)
		press(t, s, proto.Input{Kind: proto.KindKeyDown, Arg: 0x4C}) // L alone is fine
		press(t, s, proto.Input{Kind: proto.KindKeyUp, Arg: 0x4C})
		press(t, s, proto.Input{Kind: proto.KindKeyDown, Arg: 0x15B}) // left Win, extended bit set as the viewer sends it
		press(t, s, proto.Input{Kind: proto.KindKeyDown, Arg: 0x45})  // Win+E is fine
		err := s.Inject(proto.Input{Kind: proto.KindKeyDown, Arg: 0x4C})
		if allow && err != nil {
			t.Errorf("allowed, but Win+L failed: %v", err)
		}
		if !allow && !errors.Is(err, ErrBlockedKey) {
			t.Errorf("Win+L = %v, want ErrBlockedKey", err)
		}
		sent := inj.count(proto.KindKeyDown)
		want := 4 // L, Win, E and (only if allowed) the second L
		if !allow {
			want = 3
		}
		if sent != want {
			t.Errorf("allow=%v: %d key downs reached the injector, want %d", allow, sent, want)
		}
		s.Release()
	}
}
