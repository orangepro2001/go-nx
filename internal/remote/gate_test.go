package remote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"go-nx/internal/proto"
	"go-nx/internal/tile"
	"go-nx/internal/trust"
)

func TestGateLimitsPerSourceAndTotal(t *testing.T) {
	g := NewGate(3, 2)
	a1, ok := g.Enter("10.0.0.1:1000")
	if !ok {
		t.Fatal("first connection refused")
	}
	if _, ok := g.Enter("10.0.0.1:1001"); !ok { // same IP, other port
		t.Fatal("second connection from one source refused")
	}
	if _, ok := g.Enter("10.0.0.1:1002"); ok {
		t.Fatal("third connection from one source accepted, limit is 2")
	}
	b, ok := g.Enter("10.0.0.2:1000")
	if !ok {
		t.Fatal("another source refused while below the total")
	}
	if _, ok := g.Enter("10.0.0.3:1000"); ok {
		t.Fatal("fourth connection accepted, total limit is 3")
	}
	a1()
	if _, ok := g.Enter("10.0.0.3:1000"); !ok {
		t.Fatal("slot not released by leave")
	}
	a1() // leaving twice must not free a second slot
	b()
	if _, ok := g.Enter("10.0.0.4:1000"); !ok {
		t.Fatal("expected a free slot")
	}
	if _, ok := g.Enter("10.0.0.5:1000"); ok {
		t.Fatal("double leave freed an extra slot")
	}
}

// A viewer that connects and then says nothing holds a slot only until the host
// gives up on its Hello; while it does, a host with a full gate turns others away
// with CloseBusy instead of letting them pile up.
func TestServeTurnsAwayConnectionsWhenGateIsFull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	gate := NewGate(1, 1)
	opt := Options{Gate: gate, Check: func(_, _, _ string) (trust.Verdict, error) { return trust.Allowed, nil }}
	results := make(chan error, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept(ctx)
			if err != nil {
				results <- err
				return
			}
			go func() { results <- Serve(ctx, conn, newFakeSource(64, 64), &fakeInjector{}, proto.NewDedup(), opt) }()
		}
	}()

	// First connection: completes the handshake, then stays silent.
	cfg, _ := clientTLS(identity(t), "127.0.0.1", hostsStore(t))
	silent, err := quic.DialAddr(ctx, ln.Addr().String(), cfg, quicConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer silent.CloseWithError(0, "")
	time.Sleep(200 * time.Millisecond) // let Serve take the only slot

	// Second connection: a normal viewer. It must be refused as busy. Depending
	// on timing the host's close reaches the viewer while it is still dialling or
	// only once it is running, so either call may report it.
	c, err := Dial(ctx, ln.Addr().String(), identity(t), hostsStore(t), "")
	if err == nil {
		defer c.Close()
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx, func(*tile.Frame, []tile.Rect) {}, nil) }()
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("second viewer was not turned away")
		}
	}
	var ae *quic.ApplicationError
	if !errors.As(err, &ae) || ae.ErrorCode != CloseBusy {
		t.Fatalf("second viewer got %v, want application close code CloseBusy", err)
	}
}
