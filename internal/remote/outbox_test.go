package remote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// A host that stops reading must never freeze the caller of SendInput: the call
// returns at once, input that cannot be sent is refused with ErrBacklog instead of
// being dropped silently, and moves (which a later one supersedes) are coalesced.
func TestSendInputNeverBlocksOnAStalledHost(t *testing.T) {
	stalled := make(chan struct{})
	addr := hostileHost(t, func(ctx context.Context, conn *quic.Conn) {
		<-stalled // accepted the control stream, will not read it
	})
	defer close(stalled)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, addr, identity(t), hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	start := time.Now()
	var worst time.Duration
	backlog := 0
	for i := 0; i < 300_000; i++ { // far more than a stream's flow-control window, 26 bytes each
		t0 := time.Now()
		err := c.SendInput(proto.Input{OpID: uint64(i + 1), Kind: proto.KindKeyDown, Arg: 65})
		if d := time.Since(t0); d > worst {
			worst = d
		}
		if errors.Is(err, ErrBacklog) {
			backlog++
		} else if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if worst > 250*time.Millisecond {
		t.Errorf("one SendInput took %v while the host was stalled", worst)
	}
	if backlog == 0 {
		t.Error("300000 inputs were accepted by a host that reads nothing: nothing was refused")
	}
	t.Logf("300000 sends in %v, slowest %v, %d refused with ErrBacklog", time.Since(start), worst, backlog)

	// Moves never fill the queue: each replaces the one before it.
	for i := 0; i < 100_000; i++ {
		if err := c.SendInput(proto.Input{Kind: proto.KindMouseMove, X: int32(i % 800), Y: 1}); err != nil && !errors.Is(err, ErrBacklog) {
			t.Fatalf("move %d: %v", i, err)
		}
	}
}

// With a host that does read, a burst of moves arrives as far fewer, ending at the
// last position, and everything with an opID arrives in the order it was sent.
func TestQueuedInputKeepsItsOrderAndMovesAreCoalesced(t *testing.T) {
	r := start(t, Options{}, true, 0)
	const moves = 5000
	for i := 1; i <= moves; i++ {
		if err := r.client.SendInput(proto.Input{Kind: proto.KindMouseMove, X: int32(i % 64), Y: int32(i % 32)}); err != nil {
			t.Fatal(err)
		}
	}
	for op := uint64(1); op <= 3; op++ { // after the moves, in this order
		if err := r.client.SendInput(proto.Input{OpID: op, Kind: proto.KindKeyDown, Arg: int32(64 + op)}); err != nil {
			t.Fatal(err)
		}
	}
	for op := uint64(1); op <= 3; op++ {
		if st := r.receipt(t, op); st != proto.StatusExecuted {
			t.Fatalf("op %d: receipt %d", op, st)
		}
	}
	r.inj.mu.Lock()
	defer r.inj.mu.Unlock()
	var keys []int32
	lastMove := -1
	nMoves := 0
	for i, in := range r.inj.seen {
		switch in.Kind {
		case proto.KindMouseMove:
			nMoves++
			lastMove = i
		case proto.KindKeyDown:
			keys = append(keys, in.Arg)
			if lastMove < 0 {
				t.Errorf("a key arrived before any move")
			}
		}
	}
	if len(keys) != 3 || keys[0] != 65 || keys[1] != 66 || keys[2] != 67 {
		t.Errorf("keys arrived as %v, want 65 66 67 in order", keys)
	}
	if nMoves >= moves {
		t.Errorf("%d moves arrived for %d sent: none were coalesced", nMoves, moves)
	}
	last := r.inj.seen[lastMove]
	if last.X != int32(moves%64) || last.Y != int32(moves%32) {
		t.Errorf("the final move is %d,%d, want %d,%d", last.X, last.Y, moves%64, moves%32)
	}
}

// Text goes through the same queue, in order with the inputs around it.
func TestTextKeepsItsPlaceInTheQueue(t *testing.T) {
	r := start(t, Options{}, true, 0)
	r.client.SendInput(proto.Input{OpID: 1, Kind: proto.KindKeyDown, Arg: 65})
	if err := r.client.SendText(2, "x"); err != nil {
		t.Fatal(err)
	}
	r.client.SendInput(proto.Input{OpID: 3, Kind: proto.KindKeyUp, Arg: 65})
	for op := uint64(1); op <= 3; op++ {
		r.receipt(t, op)
	}
	r.inj.mu.Lock()
	defer r.inj.mu.Unlock()
	if len(r.inj.seen) != 2 || r.inj.seen[0].Kind != proto.KindKeyDown || r.inj.seen[1].Kind != proto.KindKeyUp || len(r.inj.texts) != 1 {
		t.Errorf("injected %+v and %q", r.inj.seen, r.inj.texts)
	}
}

// A control stream that cannot be written for too long is dead; the connection is
// closed rather than left half-written, because a partial message would make
// everything after it unreadable.
func TestAStalledControlStreamClosesTheConnection(t *testing.T) {
	stalled := make(chan struct{})
	addr := hostileHost(t, func(ctx context.Context, conn *quic.Conn) { <-stalled })
	defer close(stalled)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Dial(ctx, addr, identity(t), hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.out.writeTimeout = 300 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, func(*tile.Frame, []tile.Rect) {}, nil) }()
	// Enough to fill the stream's window and make the writer block.
	for i := 0; i < 400_000; i++ {
		err := c.SendInput(proto.Input{OpID: uint64(i + 1), Kind: proto.KindKeyDown, Arg: 65})
		if err != nil && !errors.Is(err, ErrBacklog) {
			break // the connection has been closed
		}
		if i%64 == 0 {
			time.Sleep(50 * time.Microsecond) // let the writer drain, so the stream's window really fills
		}
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the connection stayed open although the control stream could not be written")
	}
	if err := c.SendInput(proto.Input{OpID: 1 << 40, Kind: proto.KindKeyDown, Arg: 66}); err == nil {
		t.Error("SendInput succeeded after the connection was closed")
	}
}

// Input accepted before Close is on its way before the connection goes: a key that
// was released a moment before the program quit has to reach the host, or it stays
// pressed there until the host's own watchdog lets go of it.
func TestCloseDeliversWhatWasQueued(t *testing.T) {
	r := start(t, Options{}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	const pairs = 300
	for i := 0; i < pairs; i++ {
		r.client.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: int32(65 + i%20)})
		r.client.SendInput(proto.Input{Kind: proto.KindKeyUp, Arg: int32(65 + i%20)})
	}
	if err := r.client.SendText(1, "last words"); err != nil {
		t.Fatal(err)
	}
	r.client.Close() // straight away, with most of it still waiting to be written

	waitForInputCount(t, r.inj, proto.KindKeyUp, pairs)
	waitFor(t, "the last text to arrive", func() bool { return len(r.inj.typed()) == 1 })
	if err := r.client.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: 1}); err == nil {
		t.Error("SendInput succeeded after Close")
	}
}

// On a link that is dead Close must not wait for the input to be sent: it gives
// what is waiting a moment and then goes.
func TestCloseGivesUpOnADeadLinkQuickly(t *testing.T) {
	stalled := make(chan struct{})
	addr := hostileHost(t, func(ctx context.Context, conn *quic.Conn) { <-stalled })
	defer close(stalled)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Dial(ctx, addr, identity(t), hostsStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300_000; i++ { // more than the stream's window: the writer is stuck
		c.SendInput(proto.Input{OpID: uint64(i + 1), Kind: proto.KindKeyDown, Arg: 65})
	}
	start := time.Now()
	c.Close()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Close took %v on a link that was not draining", d)
	}
}

func TestFlushReportsWhetherEverythingWasWritten(t *testing.T) {
	r := start(t, Options{}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	if err := r.client.Flush(time.Second); err != nil {
		t.Fatalf("Flush of an empty queue = %v", err)
	}
	for i := 0; i < 200; i++ {
		r.client.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: 65})
	}
	if err := r.client.Flush(5 * time.Second); err != nil {
		t.Fatalf("Flush = %v, want the queue written", err)
	}
	waitForInputCount(t, r.inj, proto.KindKeyDown, 200)
}

// slowInjector is a host that takes a moment over every input.
type slowInjector struct {
	fakeInjector
	delay time.Duration
}

func (s *slowInjector) Inject(in proto.Input) error {
	time.Sleep(s.delay)
	return s.fakeInjector.Inject(in)
}

// An acknowledgement that is written after the control stream has been ended (the
// viewer was busy with a picture when the program quit) must not take the
// connection down with the input still on its way. The host here takes 60 ms to get
// through the input, the viewer 20 ms over its first picture, so the acknowledgement
// is written after Close has ended the stream and before the host has read it all.
func TestCloseWhileAnAcknowledgementIsStillToCome(t *testing.T) {
	for round := 0; round < 5; round++ {
		hostID, viewerID := identity(t), identity(t)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		ln, err := Listen("127.0.0.1:0", hostID)
		if err != nil {
			t.Fatal(err)
		}
		inj := &slowInjector{delay: 150 * time.Microsecond}
		go func() {
			conn, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			Serve(ctx, conn, newFakeSource(150, 90), inj, proto.NewDedup(), Options{FPS: 50, Check: func(f, _, _ string) (trust.Verdict, error) {
				if f == fp(viewerID) {
					return trust.Allowed, nil
				}
				return trust.Untrusted, nil
			}})
		}()
		c, err := Dial(ctx, ln.Addr().String(), viewerID, hostsStore(t), "")
		if err != nil {
			t.Fatal(err)
		}
		go c.Run(ctx, func(*tile.Frame, []tile.Rect) { time.Sleep(20 * time.Millisecond) }, func(uint64, byte) {})
		select {
		case <-c.welcome:
		case <-time.After(5 * time.Second):
			t.Fatal("no welcome")
		}

		const pairs = 200
		for i := 0; i < pairs; i++ {
			c.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: int32(65 + i%20)})
			c.SendInput(proto.Input{Kind: proto.KindKeyUp, Arg: int32(65 + i%20)})
		}
		c.SendText(1, "last words")
		c.Close()
		waitForInputCount(t, &inj.fakeInjector, proto.KindKeyUp, pairs)
		waitFor(t, "the last text to arrive", func() bool { return len(inj.typed()) == 1 })
		cancel()
		ln.Close()
	}
}

// A viewer that closes politely ends its control stream; for the host that is the
// session ending normally, not an error to log or to report.
func TestAViewerThatClosesPolitelyEndsTheSessionWithoutAnError(t *testing.T) {
	r := start(t, Options{}, true, 0)
	if r.dialErr != nil {
		t.Fatal(r.dialErr)
	}
	r.client.SendInput(proto.Input{Kind: proto.KindKeyDown, Arg: 65})
	r.client.Close()
	select {
	case err := <-r.served:
		if err != nil {
			t.Fatalf("Serve returned %v after the viewer closed politely, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
}
