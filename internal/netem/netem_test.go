package netem

import (
	"bytes"
	"encoding/binary"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// echoServer replies to every datagram with the same bytes.
func echoServer(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			c.WriteToUDP(buf[:n], from)
		}
	}()
	return c
}

// sink counts what arrives and when.
type sink struct {
	c    *net.UDPConn
	mu   sync.Mutex
	seqs []uint32
	at   []time.Time
	n    atomic.Int64
}

func newSink(t *testing.T) *sink {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{c: c}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 65536)
		for {
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var seq uint32
			if n >= 4 {
				seq = binary.BigEndian.Uint32(buf)
			}
			s.mu.Lock()
			s.seqs = append(s.seqs, seq)
			s.at = append(s.at, time.Now())
			s.mu.Unlock()
			s.n.Add(1)
		}
	}()
	return s
}

func (s *sink) addr() string { return s.c.LocalAddr().String() }

func (s *sink) received() []uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint32(nil), s.seqs...)
}

func dial(t *testing.T, addr string) *net.UDPConn {
	t.Helper()
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func packet(seq uint32, size int) []byte {
	p := make([]byte, size)
	binary.BigEndian.PutUint32(p, seq)
	return p
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestWithNothingConfiguredItIsAWire(t *testing.T) {
	s := newSink(t)
	p, err := Listen("127.0.0.1:0", s.addr(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c := dial(t, p.Addr())
	for i := 0; i < 200; i++ {
		c.Write(packet(uint32(i), 100+i))
		time.Sleep(200 * time.Microsecond) // loopback itself may drop a burst
	}
	waitFor(t, "200 datagrams", func() bool { return s.n.Load() == 200 })
	got := s.received()
	for i, seq := range got {
		if seq != uint32(i) {
			t.Fatalf("datagram %d arrived as %d: a wire keeps the order", i, seq)
		}
	}
	if st := p.Stats(); st.ToHost.Delivered != 200 || st.ToHost.Lost != 0 || st.ToHost.QueueDropped != 0 {
		t.Errorf("stats = %+v", st.ToHost)
	}
}

func TestRepliesFindTheirWayBackToTheRightClient(t *testing.T) {
	echo := echoServer(t)
	p, err := Listen("127.0.0.1:0", echo.LocalAddr().String(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a, b := dial(t, p.Addr()), dial(t, p.Addr())
	for i := 0; i < 20; i++ {
		a.Write([]byte("from a"))
		b.Write([]byte("from b"))
	}
	for name, c := range map[string]*net.UDPConn{"a": a, "b": b} {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 100)
		for i := 0; i < 20; i++ {
			n, err := c.Read(buf)
			if err != nil {
				t.Fatalf("%s: reply %d: %v", name, i, err)
			}
			if want := "from " + name; !bytes.Equal(buf[:n], []byte(want)) {
				t.Fatalf("%s received %q, want %q", name, buf[:n], want)
			}
		}
	}
}

func TestLossDropsAboutTheConfiguredShare(t *testing.T) {
	s := newSink(t)
	p, err := Listen("127.0.0.1:0", s.addr(), Config{ToHost: Path{Loss: 0.3}, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c := dial(t, p.Addr())
	const n = 3000
	for i := 0; i < n; i++ {
		c.Write(packet(uint32(i), 64))
		if i%50 == 49 {
			time.Sleep(time.Millisecond)
		}
	}
	waitFor(t, "the proxy to see all of them", func() bool { st := p.Stats().ToHost; return st.Delivered+st.Lost == n })
	time.Sleep(100 * time.Millisecond)
	st := p.Stats().ToHost
	share := float64(st.Lost) / n
	if share < 0.27 || share > 0.33 { // 30% of 3000 is 900, one standard deviation about 25
		t.Errorf("%.1f%% were dropped, want about 30%% (stats %+v)", share*100, st)
	}
	if got := s.n.Load(); got != int64(st.Delivered) {
		t.Errorf("the sink got %d, the proxy says it delivered %d", got, st.Delivered)
	}
	// The same seed drops the same ones.
	s2 := newSink(t)
	p2, _ := Listen("127.0.0.1:0", s2.addr(), Config{ToHost: Path{Loss: 0.3}, Seed: 7})
	defer p2.Close()
	c2 := dial(t, p2.Addr())
	for i := 0; i < n; i++ {
		c2.Write(packet(uint32(i), 64))
		if i%50 == 49 {
			time.Sleep(time.Millisecond)
		}
	}
	waitFor(t, "the second proxy", func() bool { st := p2.Stats().ToHost; return st.Delivered+st.Lost == n })
	if p2.Stats().ToHost.Lost != st.Lost {
		t.Errorf("the same seed lost %d and then %d", st.Lost, p2.Stats().ToHost.Lost)
	}
}

func TestDelayIsAddedInEachDirection(t *testing.T) {
	echo := echoServer(t)
	p, err := Listen("127.0.0.1:0", echo.LocalAddr().String(), Config{ToHost: Path{Delay: 40 * time.Millisecond}, ToViewer: Path{Delay: 25 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c := dial(t, p.Addr())
	buf := make([]byte, 100)
	var rtts []time.Duration
	for i := 0; i < 10; i++ {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		t0 := time.Now()
		c.Write([]byte("ping"))
		if _, err := c.Read(buf); err != nil {
			t.Fatal(err)
		}
		rtts = append(rtts, time.Since(t0))
	}
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	if rtts[0] < 65*time.Millisecond || rtts[len(rtts)/2] > 110*time.Millisecond {
		t.Errorf("round trips %v: want about 65 ms (40 there, 25 back)", rtts)
	}
}

func TestTheRateLimitSpacesDatagramsOut(t *testing.T) {
	s := newSink(t)
	p, err := Listen("127.0.0.1:0", s.addr(), Config{ToHost: Path{RateBps: 1_000_000, QueueBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c := dial(t, p.Addr())
	const n, size = 80, 1200 // 96000 bytes: 0.768 s at 1 Mbit/s
	start := time.Now()
	for i := 0; i < n; i++ {
		c.Write(packet(uint32(i), size))
	}
	waitFor(t, "the burst to drain", func() bool { return s.n.Load() == n })
	took := time.Since(start)
	if took < 650*time.Millisecond || took > 1100*time.Millisecond {
		t.Errorf("%d bytes at 1 Mbit/s took %v, want about 770 ms", n*size, took)
	}
	got := s.received()
	for i, seq := range got {
		if seq != uint32(i) {
			t.Fatalf("a rate limit keeps the order; datagram %d arrived as %d", i, seq)
		}
	}
}

// A queue that is full drops the newest, as a router's does.
func TestAFullQueueDropsTheTail(t *testing.T) {
	s := newSink(t)
	p, err := Listen("127.0.0.1:0", s.addr(), Config{ToHost: Path{RateBps: 200_000, QueueBytes: 6000}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c := dial(t, p.Addr())
	const n = 60
	for i := 0; i < n; i++ {
		c.Write(packet(uint32(i), 1200))
	}
	waitFor(t, "the proxy to decide on all", func() bool { st := p.Stats().ToHost; return st.Delivered+st.QueueDropped == n })
	st := p.Stats().ToHost
	if st.QueueDropped < 40 || st.Delivered < 3 || st.Delivered > 15 {
		t.Errorf("stats %+v: with room for about 5 datagrams most of 60 sent at once must be dropped", st)
	}
	waitFor(t, "the survivors", func() bool { return s.n.Load() == int64(st.Delivered) })
	got := s.received()
	if got[0] != 0 {
		t.Errorf("the first datagram did not survive: %v", got)
	}
}

func TestPathsCanBeChangedWhileRunning(t *testing.T) {
	s := newSink(t)
	p, err := Listen("127.0.0.1:0", s.addr(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c := dial(t, p.Addr())
	c.Write(packet(1, 50))
	waitFor(t, "the first", func() bool { return s.n.Load() == 1 })

	p.Set(Config{ToHost: Path{Loss: 1}}) // a blackout
	for i := 0; i < 30; i++ {
		c.Write(packet(uint32(100+i), 50))
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if s.n.Load() != 1 {
		t.Fatalf("%d datagrams got through a blackout", s.n.Load()-1)
	}
	p.Set(Config{})
	c.Write(packet(2, 50))
	waitFor(t, "recovery", func() bool { return s.n.Load() == 2 })
}

func TestBadConfigIsRefused(t *testing.T) {
	for name, cfg := range map[string]Config{
		"loss above one":    {ToHost: Path{Loss: 1.5}},
		"negative loss":     {ToViewer: Path{Loss: -0.1}},
		"negative delay":    {ToHost: Path{Delay: -time.Second}},
		"negative rate":     {ToHost: Path{RateBps: -1}},
		"negative queue":    {ToHost: Path{QueueBytes: -1}},
		"jitter over delay": {ToHost: Path{Delay: time.Millisecond, Jitter: time.Second}},
	} {
		if p, err := Listen("127.0.0.1:0", "127.0.0.1:9", cfg); err == nil {
			p.Close()
			t.Errorf("%s was accepted", name)
		}
	}
	if p, err := Listen("127.0.0.1:0", "not an address", Config{}); err == nil {
		p.Close()
		t.Error("a bad upstream address was accepted")
	}
}

func TestCloseStopsEverythingAndIsRepeatable(t *testing.T) {
	echo := echoServer(t)
	p, err := Listen("127.0.0.1:0", echo.LocalAddr().String(), Config{ToHost: Path{Delay: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	c := dial(t, p.Addr())
	c.Write([]byte("stuck for an hour"))
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() { p.Close(); p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return with a datagram waiting out its delay")
	}
}
