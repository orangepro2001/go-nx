package vdisplay

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
)

func TestPickTheLargestModeThatFitsTheWindow(t *testing.T) {
	modes := []Mode{{1280, 720, 60}, {1920, 1080, 60}, {1920, 1080, 144}, {2560, 1440, 60}, {1600, 900, 30}, {1600, 900, 75}, {3840, 2160, 60}}
	for _, c := range []struct {
		w, h int
		want Mode
	}{
		{2000, 1200, Mode{1920, 1080, 60}}, // fits, and 60 Hz is preferred over a faster rate
		{2560, 1440, Mode{2560, 1440, 60}}, // exactly
		{1700, 1000, Mode{1600, 900, 75}},  // no 60 Hz at that size: the highest rate
		{1000, 600, Mode{1280, 720, 60}},   // nothing fits: the smallest
		{1920, 1000, Mode{1600, 900, 75}},  // 1080 is too tall
	} {
		if got, ok := Pick(modes, c.w, c.h); !ok || got != c.want {
			t.Errorf("Pick for %dx%d = %v, want %v", c.w, c.h, got, c.want)
		}
	}
	if _, ok := Pick(nil, 800, 600); ok {
		t.Error("a mode picked from none")
	}
}

// fakeSystem records the steps in order and can be made to fail at one of them.
type fakeSystem struct {
	mu     sync.Mutex
	steps  []string
	failAt string
	modes  []Mode
	notOK  error
}

func (f *fakeSystem) step(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, s)
	if strings.HasPrefix(s, f.failAt) && f.failAt != "" {
		return errors.New(s + " failed")
	}
	return nil
}
func (f *fakeSystem) Ready() error { return f.notOK }
func (f *fakeSystem) Add() (int, string, error) {
	return 3, `\\.\DISPLAY9`, f.step("add")
}
func (f *fakeSystem) Remove(i int) error             { return f.step("remove") }
func (f *fakeSystem) Modes(string) ([]Mode, error)   { return f.modes, f.step("modes") }
func (f *fakeSystem) SetMode(d string, m Mode) error { return f.step("mode " + m.String()) }
func (f *fakeSystem) Snapshot() (Topology, error) {
	return Topology{Paths: []byte{1}, Modes: []byte{2}}, f.step("snapshot")
}
func (f *fakeSystem) UseOnly(string) error   { return f.step("only virtual") }
func (f *fakeSystem) Restore(Topology) error { return f.step("restore") }
func (f *fakeSystem) taken() []string        { f.mu.Lock(); defer f.mu.Unlock(); return slices.Clone(f.steps) }
func (f *fakeSystem) failing(at string)      { f.mu.Lock(); f.failAt, f.steps = at, nil; f.mu.Unlock() }
func (f *fakeCapture) Device() string        { f.mu.Lock(); defer f.mu.Unlock(); return f.device }
func (f *fakeCapture) Use(d string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.device = d
	f.sys.step("capture " + d)
	return nil
}

type fakeCapture struct {
	mu     sync.Mutex
	device string
	sys    *fakeSystem
}

func newManager(t *testing.T) (*Manager, *fakeSystem, *fakeCapture, FileStore) {
	sys := &fakeSystem{modes: []Mode{{1920, 1080, 60}, {2560, 1440, 60}, {1280, 720, 60}}}
	cap := &fakeCapture{device: `\\.\DISPLAY1`, sys: sys}
	store := FileStore(filepath.Join(t.TempDir(), "display-layout.json"))
	return NewManager(sys, cap, store, t.Logf), sys, cap, store
}

// ask makes a request and waits for its report.
func ask(t *testing.T, m *Manager, w, h int) proto.DisplayState {
	t.Helper()
	got := make(chan proto.DisplayState, 1)
	m.Request("viewer", w, h, func(st proto.DisplayState) { got <- st })
	select {
	case st := <-got:
		return st
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
	}
	return proto.DisplayState{}
}

// The layout is on disk before anything changes; the capture is on the virtual display
// before the others are turned off; and on the way back the layout is restored before the
// capture leaves the virtual display and before the display is removed.
func TestTheStepsHappenInAnOrderThatNeverLosesTheScreen(t *testing.T) {
	m, sys, cap, store := newManager(t)
	st := ask(t, m, 2000, 1200)
	if !st.Virtual || st.W != 1920 || st.H != 1080 || st.Message != "" {
		t.Fatalf("state = %+v", st)
	}
	want := []string{"snapshot", "add", "modes", "mode 1920x1080@60", `capture \\.\DISPLAY9`, "only virtual"}
	if got := sys.taken(); !slices.Equal(got, want) {
		t.Fatalf("steps %q, want %q", got, want)
	}
	if _, ok := store.Load(); !ok {
		t.Fatal("the layout to go back to is not on disk while the virtual display is in use")
	}
	sys.failing("")
	if st := ask(t, m, 2600, 1500); st.W != 2560 || !st.Virtual {
		t.Errorf("after the window grew: %+v", st)
	}
	if st := ask(t, m, 2600, 1500); st.W != 2560 || len(sys.taken()) != 3 {
		t.Errorf("the same size again changed something: %+v %q", st, sys.taken())
	}
	sys.failing("")
	if st := ask(t, m, 0, 0); st.Virtual || st.Message != "" {
		t.Fatalf("back: %+v", st)
	}
	want = []string{"restore", `capture \\.\DISPLAY1`, "remove"}
	if got := sys.taken(); !slices.Equal(got, want) {
		t.Fatalf("steps back %q, want %q", got, want)
	}
	if _, ok := store.Load(); ok {
		t.Error("the layout is still on disk after it was put back")
	}
	if cap.Device() != `\\.\DISPLAY1` {
		t.Errorf("capturing %s", cap.Device())
	}
}

// A failure on the way undoes what was done, and the session stays on the host's screen.
func TestAFailureOnTheWayUndoesWhatWasDone(t *testing.T) {
	m, sys, cap, store := newManager(t)
	sys.failing("only virtual")
	st := ask(t, m, 1920, 1080)
	if st.Virtual || !strings.Contains(st.Message, "turning the other displays off") {
		t.Fatalf("state = %+v", st)
	}
	got := sys.taken()
	tail := got[len(got)-3:]
	if want := []string{`capture \\.\DISPLAY1`, "restore", "remove"}; !slices.Equal(tail, want) {
		t.Errorf("undo steps %q, want %q (all: %q)", tail, want, got)
	}
	if cap.Device() != `\\.\DISPLAY1` {
		t.Errorf("left capturing %s", cap.Device())
	}
	if _, ok := store.Load(); ok {
		t.Error("the layout is still on disk after the undo")
	}
	if _, _, ok := m.Active(); ok {
		t.Error("a virtual display is reported in use")
	}
}

// When the layout cannot be put back, the virtual display stays (it may be all there is to
// see) and so does the layout on disk, for the next start.
func TestALayoutThatCannotBePutBackIsKept(t *testing.T) {
	m, sys, _, store := newManager(t)
	ask(t, m, 1920, 1080)
	sys.failing("restore")
	if st := ask(t, m, 0, 0); !st.Virtual || !strings.Contains(st.Message, "putting the displays back") {
		t.Fatalf("state = %+v", st)
	}
	if slices.Contains(sys.taken(), "remove") {
		t.Error("the virtual display was removed although nothing else may be showing")
	}
	if _, ok := store.Load(); !ok {
		t.Error("the layout was dropped from disk")
	}
	// The next start puts it back.
	sys.failing("")
	if err := NewManager(sys, &fakeCapture{sys: sys}, store, t.Logf).Recover(); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Load(); ok || !slices.Contains(sys.taken(), "restore") {
		t.Errorf("Recover did not restore and clear: %q", sys.taken())
	}
}

func TestNotReadyChangesNothing(t *testing.T) {
	m, sys, _, _ := newManager(t)
	sys.notOK = errors.New("the Parsec Virtual Display Driver is not installed")
	if st := ask(t, m, 1920, 1080); st.Virtual || st.Message != sys.notOK.Error() || len(sys.taken()) != 0 {
		t.Errorf("state %+v, steps %q", st, sys.taken())
	}
}

// Release, at the end of a session, puts the screen back and waits for it; a stream of
// resizes while the window is dragged is handled latest first, not one by one.
func TestReleaseWaitsAndResizesCollapse(t *testing.T) {
	m, sys, _, _ := newManager(t)
	ask(t, m, 1920, 1080)
	sys.failing("")
	for i := 0; i < 50; i++ {
		m.Request("viewer", 1280+i, 720+i, nil)
	}
	m.Release("viewer")
	if _, _, ok := m.Active(); ok {
		t.Fatal("still on the virtual display after Release")
	}
	if n := strings.Count(strings.Join(sys.taken(), ","), "modes"); n > 3 {
		t.Errorf("%d resizes were handled one by one: %q", n, sys.taken())
	}
	m.Release("viewer") // nothing to do: returns at once
}

// A session replaced by another (the host takes one controller at a time) ends after the
// new one has its virtual display: that display stays.
func TestAnEndingSessionLeavesItsSuccessorsDisplayAlone(t *testing.T) {
	m, sys, _, _ := newManager(t)
	ask(t, m, 1920, 1080) // "viewer"
	got := make(chan proto.DisplayState, 1)
	m.Request("successor", 2560, 1440, func(st proto.DisplayState) { got <- st })
	if st := <-got; !st.Virtual || st.W != 2560 {
		t.Fatalf("the successor's request: %+v", st)
	}
	sys.failing("")
	m.Release("viewer")
	if _, mode, ok := m.Active(); !ok || mode.W != 2560 || slices.Contains(sys.taken(), "restore") {
		t.Fatalf("the old session's end put the screen back under the successor: %v %v %q", mode, ok, sys.taken())
	}
	m.Release("successor")
	if _, _, ok := m.Active(); ok {
		t.Error("the successor's end left the virtual display")
	}
	ask(t, m, 1920, 1080)
	m.Release(nil) // go-nx exiting: whoever's
	if _, _, ok := m.Active(); ok {
		t.Error("Release(nil) left the virtual display")
	}
}
