package remote

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/netem"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// stoppable shows a moving synthetic screen until Freeze, and the last picture it
// showed from then on.
type stoppable struct {
	src    *synth.Source
	mu     sync.Mutex
	frozen *tile.Frame
}

func (s *stoppable) Size() (int, int) { return s.src.Size() }

func (s *stoppable) Grab(dst *tile.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frozen != nil {
		copy(dst.Pix, s.frozen.Pix)
		return nil
	}
	return s.src.Grab(dst)
}

func (s *stoppable) Freeze() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, h := s.src.Size()
	s.frozen = tile.NewFrame(w, h)
	s.src.Grab(s.frozen)
	return append([]byte(nil), s.frozen.Pix...)
}

// slowLink is the link the plan's weak-network acceptance names: 1 Mbit/s each
// way, 80 ms round trip, 1% of datagrams lost each way, a queue of about a quarter
// second at that rate.
func slowLink() netem.Config {
	p := netem.Path{Delay: 40 * time.Millisecond, Loss: 0.01, RateBps: 1_000_000, QueueBytes: 32 << 10}
	return netem.Config{ToHost: p, ToViewer: p, Seed: 42}
}

type weakRun struct {
	proxy    *netem.Proxy
	src      *stoppable
	inj      *fakeInjector
	client   *Client
	started  time.Time
	mu       sync.Mutex
	view     []byte
	frames   int
	firstAt  time.Duration
	receipts map[uint64]time.Time
	statuses map[uint64]byte
	runErr   chan error
}

func startWeak(t *testing.T, scene synth.Scene, link netem.Config) *weakRun {
	t.Helper()
	hostID, viewerID := identity(t), identity(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	ln, err := Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	syn, err := synth.NewScene(scene, 640, 480)
	if err != nil {
		t.Fatal(err)
	}
	w := &weakRun{src: &stoppable{src: syn}, inj: &fakeInjector{}, receipts: map[uint64]time.Time{}, statuses: map[uint64]byte{}, runErr: make(chan error, 1)}
	dd := proto.NewDedup()
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		Serve(ctx, conn, w.src, w.inj, dd, Options{FPS: 10, Check: func(f, _, _ string) (trust.Verdict, error) {
			if f == fp(viewerID) {
				return trust.Allowed, nil
			}
			return trust.Untrusted, nil
		}})
	}()
	w.proxy, err = netem.Listen("127.0.0.1:0", ln.Addr().String(), link)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.proxy.Close() })

	w.started = time.Now()
	w.client, err = DialWith(ctx, w.proxy.Addr(), viewerID, nil, DialOptions{ExpectFingerprint: fp(hostID)})
	if err != nil {
		t.Fatalf("connecting over the slow link: %v", err)
	}
	t.Cleanup(w.client.Close)
	go func() {
		w.runErr <- w.client.Run(ctx, func(f *tile.Frame, _ []tile.Rect) {
			w.mu.Lock()
			if w.frames == 0 {
				w.firstAt = time.Since(w.started)
			}
			w.frames++
			w.view = append(w.view[:0], f.Pix...)
			w.mu.Unlock()
		}, func(id uint64, st byte) {
			w.mu.Lock()
			if _, seen := w.receipts[id]; !seen {
				w.receipts[id] = time.Now()
			}
			w.statuses[id] = st
			w.mu.Unlock()
		})
	}()
	return w
}

func (w *weakRun) framesSeen() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.frames
}

func (w *weakRun) viewEquals(want []byte) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Equal(w.view, want)
}

func quantile(d []time.Duration, q float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[min(len(s)-1, int(float64(len(s))*q))]
}

// sendWithRetries is how an application uses operation numbers on a bad link: send
// the operation, and if no receipt has come back in a while, send it again with the
// same number. Each one is also sent twice at once, so that the host is certain to
// be offered an operation it has already run.
func (w *weakRun) sendWithRetries(t *testing.T, in proto.Input) (latency time.Duration) {
	t.Helper()
	first := time.Now()
	for attempt := 0; attempt < 12; attempt++ {
		if err := w.client.SendInput(in); err != nil {
			t.Fatalf("send %d: %v", in.OpID, err)
		}
		if attempt == 0 {
			w.client.SendInput(in) // an immediate duplicate
		}
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			w.mu.Lock()
			at, ok := w.receipts[in.OpID]
			w.mu.Unlock()
			if ok {
				return at.Sub(first)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Fatalf("operation %d never got a receipt", in.OpID)
	return 0
}

// The weak-network acceptance: on a link of 1 Mbit/s, 80 ms and 1% loss the session
// is usable, which here means it connects, shows the screen, converges on it when it
// stops moving, answers every input, and never runs an operation twice however many
// times it is sent. Two screens: a light one, and a terminal that prints faster than
// the link can carry (the host must lower the frame rate, not queue without end).
func TestUsableOnAOneMegabitEightyMillisecondOnePercentLossLink(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about ten seconds a scene")
	}
	for _, scene := range []synth.Scene{synth.SceneBlocks, synth.SceneTerminal} {
		t.Run(scene.String(), func(t *testing.T) {
			w := startWeak(t, scene, slowLink())
			waitFor(t, "the first picture", func() bool { return w.framesSeen() > 0 })
			t.Logf("connected and showing the screen after %v", w.firstAt)
			if w.firstAt > 8*time.Second {
				t.Errorf("the first picture took %v", w.firstAt)
			}

			// Work for a while: 60 operations among the pictures.
			var latencies []time.Duration
			for i := 1; i <= 60; i++ {
				kind := proto.KindKeyDown
				if i%2 == 0 {
					kind = proto.KindKeyUp
				}
				latencies = append(latencies, w.sendWithRetries(t, proto.Input{OpID: uint64(i), Kind: kind, Arg: int32(65 + i%20)}))
			}
			p50, p95 := quantile(latencies, 0.5), quantile(latencies, 0.95)
			t.Logf("60 operations: receipt after %v (median), %v (95th percentile), %v (worst)", p50, p95, quantile(latencies, 1))
			if p95 > 2*time.Second {
				t.Errorf("95%% of operations were answered within %v, want 2 s", p95)
			}

			// Exactly once.
			counts := map[uint64]int{}
			w.inj.mu.Lock()
			for _, in := range w.inj.seen {
				counts[in.OpID]++
			}
			w.inj.mu.Unlock()
			for i := uint64(1); i <= 60; i++ {
				if counts[i] != 1 {
					t.Errorf("operation %d ran %d times, want exactly once", i, counts[i])
				}
				if w.statuses[i] != proto.StatusExecuted {
					t.Errorf("operation %d: receipt status %d", i, w.statuses[i])
				}
			}

			// The screen stops moving; the viewer catches up completely.
			still := w.src.Freeze()
			waitFor(t, "the viewer to show the final screen", func() bool { return w.viewEquals(still) })

			st := w.proxy.Stats()
			t.Logf("the link carried %d bytes to the host and %d to the viewer; it lost %d and %d datagrams",
				st.ToHost.Bytes, st.ToViewer.Bytes, st.ToHost.Lost, st.ToViewer.Lost)
			if st.ToHost.Lost+st.ToViewer.Lost == 0 {
				t.Error("the link lost nothing, so the test did not test loss")
			}
			select {
			case err := <-w.runErr:
				t.Fatalf("the session ended: %v", err)
			default:
			}
		})
	}
}

// A link that goes dead for a few seconds and comes back: the session survives,
// what was sent during the outage is carried out once the link returns, and the
// picture catches up.
func TestSurvivesAFewSecondsOfNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about ten seconds")
	}
	w := startWeak(t, synth.SceneBlocks, slowLink())
	waitFor(t, "the first picture", func() bool { return w.framesSeen() > 0 })

	w.proxy.Set(netem.Config{ToHost: netem.Path{Loss: 1}, ToViewer: netem.Path{Loss: 1}})
	for i := uint64(1); i <= 5; i++ {
		w.client.SendInput(proto.Input{OpID: i, Kind: proto.KindKeyDown, Arg: 65})
	}
	time.Sleep(4 * time.Second)
	if n := w.inj.count(proto.KindKeyDown); n != 0 {
		t.Fatalf("%d inputs got through a dead link", n)
	}
	w.proxy.Set(slowLink())
	waitFor(t, "the input sent during the outage", func() bool { return w.inj.count(proto.KindKeyDown) == 5 })
	waitFor(t, "the receipts", func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.receipts) == 5
	})
	still := w.src.Freeze()
	waitFor(t, "the viewer to catch up", func() bool { return w.viewEquals(still) })
	select {
	case err := <-w.runErr:
		t.Fatalf("the session ended: %v", err)
	default:
	}
}
