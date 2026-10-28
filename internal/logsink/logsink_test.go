package logsink

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
)

var (
	_ remote.Injector     = (*Sink)(nil)
	_ remote.TextInjector = (*Sink)(nil)
)

func TestSinkRecordsInputsAndText(t *testing.T) {
	var buf bytes.Buffer
	s := New(log.New(&buf, "", 0))
	for _, in := range []proto.Input{
		{OpID: 1, Kind: proto.KindMouseMove, X: 10, Y: 20},
		{OpID: 2, Kind: proto.KindKeyDown, Arg: 0x41},
		{OpID: 3, Kind: proto.KindWheelH, Arg: -120},
	} {
		if err := s.Inject(in); err != nil {
			t.Fatalf("Inject(%+v): %v", in, err)
		}
	}
	if err := s.InjectText("你好 go-nx"); err != nil {
		t.Fatal(err)
	}
	n, last := s.Snapshot()
	if n != 4 || !strings.Contains(last, "go-nx") {
		t.Fatalf("Snapshot() = %d, %q", n, last)
	}
	if !strings.Contains(buf.String(), "TEST INPUT:") {
		t.Fatalf("no log line was written: %q", buf.String())
	}
	if got := s.Typed(); len(got) != 1 || got[0] != "你好 go-nx" {
		t.Fatalf("Typed() = %q", got)
	}
	if got := s.Inputs(); len(got) != 3 || got[2].Kind != proto.KindWheelH {
		t.Fatalf("Inputs() = %+v", got)
	}
	// The slices are copies.
	s.Inputs()[0].Kind = 99
	if s.Inputs()[0].Kind != proto.KindMouseMove {
		t.Error("Inputs() exposes the sink's own storage")
	}
}

// Input the real injector could not make sense of is an error here too, and is
// not counted as accepted.
func TestSinkRefusesWhatARealInjectorWould(t *testing.T) {
	s := New(nil)
	for _, in := range []proto.Input{{Kind: 0}, {Kind: 99}, {Kind: proto.KindWheelH + 1}} {
		if err := s.Inject(in); err == nil {
			t.Errorf("kind %d was accepted", in.Kind)
		}
	}
	if n, _ := s.Snapshot(); n != 0 {
		t.Fatalf("%d refused inputs were counted", n)
	}
}

// With the screen size known, a pointer position outside it is a bug of the
// sender, which a real desktop would hide by clamping. NoMove (let go where the
// pointer is) is fine.
func TestSinkChecksCoordinatesAgainstTheScreen(t *testing.T) {
	s := New(nil)
	s.SetScreen(640, 480)
	ok := []proto.Input{
		{Kind: proto.KindMouseMove, X: 0, Y: 0},
		{Kind: proto.KindMouseMove, X: 639, Y: 479},
		{Kind: proto.KindMouseDown, X: 100, Y: 100},
		{Kind: proto.KindMouseUp, X: proto.NoMove, Y: proto.NoMove},
		{Kind: proto.KindKeyDown, X: -5, Y: 9999, Arg: 0x41}, // keys carry no position
		{Kind: proto.KindWheel, X: 320, Y: 240, Arg: 120},
	}
	for _, in := range ok {
		if err := s.Inject(in); err != nil {
			t.Errorf("Inject(%+v): %v", in, err)
		}
	}
	bad := []proto.Input{
		{Kind: proto.KindMouseMove, X: 640, Y: 0},
		{Kind: proto.KindMouseMove, X: 0, Y: 480},
		{Kind: proto.KindMouseDown, X: -1, Y: 0},
		{Kind: proto.KindMouseUp, X: 0, Y: -1},
	}
	for _, in := range bad {
		err := s.Inject(in)
		if !errors.Is(err, ErrOutOfScreen) {
			t.Errorf("Inject(%+v) = %v, want ErrOutOfScreen", in, err)
		}
	}
	if n, _ := s.Snapshot(); n != len(ok) {
		t.Errorf("%d inputs counted, want %d", n, len(ok))
	}
}

// The history is bounded: a long soak run must not grow without limit.
func TestSinkKeepsOnlyRecentHistory(t *testing.T) {
	s := New(nil)
	for i := 0; i < 3*HistoryLen; i++ {
		s.Inject(proto.Input{Kind: proto.KindMouseMove, X: int32(i)})
	}
	n, _ := s.Snapshot()
	if n != 3*HistoryLen {
		t.Fatalf("count = %d, want the total %d", n, 3*HistoryLen)
	}
	got := s.Inputs()
	if len(got) != HistoryLen || got[len(got)-1].X != int32(3*HistoryLen-1) || got[0].X != int32(2*HistoryLen) {
		t.Fatalf("history holds %d inputs from X=%d to X=%d", len(got), got[0].X, got[len(got)-1].X)
	}
}

func TestSinkIsSafeForConcurrentUse(t *testing.T) {
	s := New(nil)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				s.Inject(proto.Input{Kind: proto.KindKeyDown, Arg: 0x41})
				s.InjectText("x")
				s.Snapshot()
				s.Inputs()
				s.Typed()
			}
		}()
	}
	wg.Wait()
	if n, _ := s.Snapshot(); n != 800 {
		t.Fatalf("count = %d, want 800", n)
	}
}
