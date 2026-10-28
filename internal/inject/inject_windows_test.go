//go:build windows

package inject

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/wintest"
)

// These tests move the real cursor and press real keys, so they only run when
// GO_NX_WINDOWS_E2E=1 (set by the CI windows job). On a machine someone is
// working at, leave it unset. See package wintest.

var (
	getCursorPos     = user32.NewProc("GetCursorPos")
	getAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	getSystemMetrics = user32.NewProc("GetSystemMetrics")
	setDPIAware      = user32.NewProc("SetProcessDPIAware")
)

type point struct{ x, y int32 }

const (
	vkLButton = 0x01
	vkF24     = 0x87 // harmless: no application binds it
)

func screenSize(t *testing.T) (int, int) {
	t.Helper()
	wintest.Skip(t)
	setDPIAware.Call() // make GetCursorPos report physical pixels like the host does
	w, _, _ := getSystemMetrics.Call(0)
	h, _, _ := getSystemMetrics.Call(1)
	if w == 0 || h == 0 {
		t.Fatal("GetSystemMetrics returned 0: no interactive desktop")
	}
	return int(w), int(h)
}

// cursor waits briefly for the cursor to reach want (SendInput is asynchronous
// with respect to GetCursorPos on some systems) and returns where it ended up.
func cursor(want point) point {
	var p point
	for i := 0; i < 40; i++ {
		getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
		if p == want {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	return p
}

func down(vk uintptr) bool {
	r, _, _ := getAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

func eventually(f func() bool) bool {
	for i := 0; i < 40; i++ {
		if f() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestAbsoluteMoveLandsOnPixel records where the cursor lands for a spread of
// target pixels. The host maps pixels to 0..65535 with truncating division, so
// a one-pixel miss is possible; more than that is a bug. Exact misses are only
// logged for now so the size of the problem is visible in CI output.
func TestAbsoluteMoveLandsOnPixel(t *testing.T) {
	w, h := screenSize(t)
	j := New(w, h)
	y := h / 2
	exact, off := 0, 0
	for _, x := range []int{0, 1, 2, 3, w / 3, w / 2, w/2 + 1, w - 3, w - 2, w - 1} {
		want := point{int32(x), int32(y)}
		if err := j.Inject(proto.Input{Kind: proto.KindMouseMove, X: int32(x), Y: int32(y)}); err != nil {
			t.Fatalf("move to %d,%d: %v", x, y, err)
		}
		got := cursor(want)
		if got == want {
			exact++
			continue
		}
		off++
		t.Logf("target %d,%d landed at %d,%d", want.x, want.y, got.x, got.y)
		if abs(int(got.x)-x) > 1 || abs(int(got.y)-y) > 1 {
			t.Errorf("target %d,%d landed at %d,%d: more than one pixel away", want.x, want.y, got.x, got.y)
		}
	}
	t.Logf("%d targets exact, %d off by one pixel (screen %dx%d)", exact, off, w, h)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestButtonUpKeepsPosition is the invariant that went wrong in another client
// (release jumped the pointer to 0,0): a press and release carry their own
// coordinates and must leave the cursor where they said.
func TestButtonUpKeepsPosition(t *testing.T) {
	w, h := screenSize(t)
	j := New(w, h)
	x, y := w/2, h/2
	for _, in := range []proto.Input{
		{Kind: proto.KindMouseDown, X: int32(x), Y: int32(y), Arg: 0},
		{Kind: proto.KindMouseUp, X: int32(x), Y: int32(y), Arg: 0},
	} {
		if err := j.Inject(in); err != nil {
			t.Fatalf("inject kind %d: %v", in.Kind, err)
		}
		if in.Kind == proto.KindMouseDown && !eventually(func() bool { return down(vkLButton) }) {
			t.Error("left button not reported down after KindMouseDown")
		}
	}
	if !eventually(func() bool { return !down(vkLButton) }) {
		t.Error("left button still down after KindMouseUp")
	}
	got := cursor(point{int32(x), int32(y)})
	if abs(int(got.x)-x) > 1 || abs(int(got.y)-y) > 1 {
		t.Errorf("cursor at %d,%d after click at %d,%d", got.x, got.y, x, y)
	}
}

func TestKeyDownUp(t *testing.T) {
	w, h := screenSize(t)
	j := New(w, h)
	if err := j.Inject(proto.Input{Kind: proto.KindKeyDown, Arg: vkF24}); err != nil {
		t.Fatal(err)
	}
	if !eventually(func() bool { return down(vkF24) }) {
		t.Error("F24 not reported down after KindKeyDown")
	}
	if err := j.Inject(proto.Input{Kind: proto.KindKeyUp, Arg: vkF24}); err != nil {
		t.Fatal(err)
	}
	if !eventually(func() bool { return !down(vkF24) }) {
		t.Error("F24 still down after KindKeyUp")
	}
}

func TestUnknownKindFails(t *testing.T) {
	// No desktop needed: the kind is rejected before any Win32 call.
	if err := New(100, 100).Inject(proto.Input{Kind: 99}); err == nil {
		t.Error("unknown input kind was accepted")
	}
}

// Text typed with InjectText must arrive in the focused window as the same UTF-16
// units, whatever the keyboard layout or input method: Latin with accents, Hangul,
// Han, and an astral character that travels as a surrogate pair.
func TestInjectTextReachesTheFocusedWindow(t *testing.T) {
	wintest.Skip(t)
	w, h := screenSize(t)
	win := wintest.New(t, wintest.Options{X: 0, Y: 0, W: 200, H: 120, R: 30, G: 30, B: 30})
	defer win.Close()
	win.Focus(t)
	win.Reset()

	text := "aé 한국어 中文 😀 end"
	if err := New(w, h).InjectText(text); err != nil {
		t.Fatal(err)
	}
	win.Pump(500 * time.Millisecond)
	want := utf16.Encode([]rune(text))
	if got := win.Chars(); !slices.Equal(got, want) {
		t.Fatalf("window received %x\nwant %x", got, want)
	}
}

// The longest text the protocol allows is typed whole and in order, which means
// more than one SendInput call.
func TestInjectTextOfMaximumLength(t *testing.T) {
	wintest.Skip(t)
	w, h := screenSize(t)
	win := wintest.New(t, wintest.Options{X: 0, Y: 0, W: 200, H: 120, R: 30, G: 30, B: 30})
	defer win.Close()
	win.Focus(t)
	win.Reset()

	text := strings.Repeat("ab한😀", proto.MaxTextLen/len("ab한😀"))
	if err := New(w, h).InjectText(text); err != nil {
		t.Fatal(err)
	}
	win.Pump(1500 * time.Millisecond)
	want := utf16.Encode([]rune(text))
	got := win.Chars()
	if !slices.Equal(got, want) {
		t.Fatalf("received %d units, want %d (first difference at %d)", len(got), len(want), firstDiff(got, want))
	}
}

func firstDiff(a, b []uint16) int {
	for i := 0; i < min(len(a), len(b)); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// A horizontal wheel notch reaches the focused window with the right sign, and a
// vertical one is not confused with it. Windows merges consecutive wheel messages
// of one axis that are still in its queue by adding their deltas (+120 and -120
// would arrive as one message of 0), so each is delivered before the next is
// sent, and the values are chosen so that a merge could not hide a mistake.
func TestHorizontalWheelIsDelivered(t *testing.T) {
	wintest.Skip(t)
	w, h := screenSize(t)
	win := wintest.New(t, wintest.Options{X: 0, Y: 0, W: 300, H: 200, R: 30, G: 30, B: 30})
	defer win.Close()
	win.Focus(t)
	win.Reset()

	j := New(w, h)
	for _, in := range []proto.Input{
		{Kind: proto.KindWheelH, X: 100, Y: 100, Arg: 120},
		{Kind: proto.KindWheelH, X: 100, Y: 100, Arg: -240},
		{Kind: proto.KindWheel, X: 100, Y: 100, Arg: 360},
	} {
		if err := j.Inject(in); err != nil {
			t.Fatal(err)
		}
		win.Pump(300 * time.Millisecond)
	}
	if got, want := win.WheelDeltas(wintest.WMMouseHWheel), []int{120, -240}; !slices.Equal(got, want) {
		t.Errorf("horizontal wheel deltas = %v, want %v", got, want)
	}
	if got, want := win.WheelDeltas(wintest.WMMouseWheel), []int{360}; !slices.Equal(got, want) {
		t.Errorf("vertical wheel deltas = %v, want %v", got, want)
	}
}
