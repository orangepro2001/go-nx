package remote

import (
	"context"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

func allowAll(_, _, _ string) (trust.Verdict, error) { return trust.Allowed, nil }

func (r *rig) receipt(t *testing.T, opID uint64) byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		st, ok := r.recv[opID]
		r.mu.Unlock()
		if ok {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no receipt for op %d", opID)
	return 0
}

func TestTextInputReceiptAndDedup(t *testing.T) {
	r := start(t, Options{}, true, 0)
	for i := 0; i < 3; i++ { // the same opID, as a retry would send it
		if err := r.client.SendText(11, "héllo 한국어 中文"); err != nil {
			t.Fatal(err)
		}
	}
	if st := r.receipt(t, 11); st != proto.StatusExecuted {
		t.Fatalf("receipt = %d, want Executed", st)
	}
	time.Sleep(100 * time.Millisecond) // let the duplicates arrive
	if got := r.inj.typed(); len(got) != 1 || got[0] != "héllo 한국어 中文" {
		t.Fatalf("typed = %q, want the text exactly once", got)
	}
}

func TestHorizontalWheelReachesTheInjector(t *testing.T) {
	r := start(t, Options{}, true, 0)
	if err := r.client.SendInput(proto.Input{OpID: 21, Kind: proto.KindWheelH, X: 5, Y: 6, Arg: -120}); err != nil {
		t.Fatal(err)
	}
	if st := r.receipt(t, 21); st != proto.StatusExecuted {
		t.Fatalf("receipt = %d", st)
	}
	if got := r.inj.count(proto.KindWheelH); got != 1 {
		t.Fatalf("injector saw %d horizontal wheel events, want 1", got)
	}
}

// onlyInject has Inject but no InjectText: a host that cannot type text.
type onlyInject struct{ f *fakeInjector }

func (o onlyInject) Inject(in proto.Input) error { return o.f.Inject(in) }

func TestTextFromAHostThatCannotTypeFailsWithoutEndingTheSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	inj := &fakeInjector{}
	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			served <- err
			return
		}
		served <- Serve(ctx, conn, newFakeSource(64, 64), onlyInject{inj}, proto.NewDedup(), Options{Check: allowAll})
	}()
	c, err := Dial(ctx, ln.Addr().String(), identity(t), hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := make(chan byte, 4)
	go c.Run(ctx, func(*tile.Frame, []tile.Rect) {}, func(id uint64, st byte) { got <- st })

	if err := c.SendText(31, "x"); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-got:
		if st != proto.StatusFailed {
			t.Fatalf("receipt = %d, want Failed", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no receipt for text sent to a host that cannot type")
	}
	// The session is still usable afterwards.
	if err := c.SendInput(proto.Input{OpID: 32, Kind: proto.KindKeyDown, Arg: 65}); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-got:
		if st != proto.StatusExecuted {
			t.Fatalf("receipt for the follow-up key = %d, want Executed", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session ended after an unsupported text")
	}
	select {
	case err := <-served:
		t.Fatalf("Serve ended: %v", err)
	default:
	}
}

// A session that was replaced by a newer controller must not be able to type.
func TestTextFromAReplacedSessionIsRejected(t *testing.T) {
	inj := &fakeInjector{}
	controller := NewInputController(inj)
	first, second := controller.newSession(), controller.newSession()
	if err := first.Activate(nil); err != nil {
		t.Fatal(err)
	}
	if err := second.Activate(nil); err != nil {
		t.Fatal(err)
	}
	if err := first.InjectText("late"); err == nil {
		t.Error("a replaced session typed text")
	}
	if err := second.InjectText("now"); err != nil {
		t.Errorf("the active session could not type: %v", err)
	}
	if got := inj.typed(); len(got) != 1 || got[0] != "now" {
		t.Errorf("typed = %q, want only the active session's text", got)
	}
}
