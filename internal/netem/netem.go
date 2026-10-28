// Package netem is a UDP proxy that behaves like a bad network: it delays,
// drops and rate-limits the datagrams that pass through it, in each direction. It
// is pure Go and needs no privileges (no tc, no root, no network namespaces), so a
// test can put a host and a viewer on a link of 1 Mbit/s, 80 ms and 1% loss with a
// loopback address and a few lines.
//
// A viewer is pointed at the proxy's address instead of the host's. Every distinct
// viewer address gets its own socket towards the host, so replies find their way
// back, and all viewers share one queue per direction, as they would a bottleneck.
//
// The emulation is deliberately simple. Delay is fixed (plus optional jitter, which
// can reorder), loss is independent per datagram and drawn from a seeded generator,
// and the rate limit is a single FIFO that drops the newest datagram when the bytes
// waiting would exceed QueueBytes. That is enough to ask whether the protocol stays
// usable on a poor link; it is not a model of any particular network.
package netem

import (
	"container/heap"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Path describes one direction.
type Path struct {
	Delay      time.Duration // added to every datagram
	Jitter     time.Duration // each datagram's delay varies by up to this much either way; may reorder
	Loss       float64       // chance, 0 to 1, that a datagram is dropped
	RateBps    int           // bandwidth in bits a second; 0 means unlimited
	QueueBytes int           // bytes that may wait for the rate limit before the newest is dropped; default 64 KiB
}

// Config is both directions and the seed for the loss draws.
type Config struct {
	ToHost   Path // viewer to host
	ToViewer Path // host to viewer
	Seed     int64
}

func (p Path) validate(name string) error {
	switch {
	case p.Loss < 0 || p.Loss > 1:
		return fmt.Errorf("netem: %s loss %v is not between 0 and 1", name, p.Loss)
	case p.Delay < 0 || p.Jitter < 0:
		return fmt.Errorf("netem: %s delay and jitter cannot be negative", name)
	case p.Jitter > p.Delay:
		return fmt.Errorf("netem: %s jitter %v is larger than the delay %v", name, p.Jitter, p.Delay)
	case p.RateBps < 0 || p.QueueBytes < 0:
		return fmt.Errorf("netem: %s rate and queue cannot be negative", name)
	}
	return nil
}

func (c Config) validate() error {
	if err := c.ToHost.validate("to-host"); err != nil {
		return err
	}
	return c.ToViewer.validate("to-viewer")
}

// DirStats counts what happened to the datagrams of one direction.
type DirStats struct {
	In           int // datagrams received for this direction
	Delivered    int // sent on
	Lost         int // dropped by the loss draw
	QueueDropped int // dropped because the queue was full
	Bytes        int // bytes delivered
}

// Stats is both directions.
type Stats struct{ ToHost, ToViewer DirStats }

type pending struct {
	at   time.Time
	seq  uint64
	data []byte
	send func([]byte)
}

type pendingHeap []*pending

func (h pendingHeap) Len() int { return len(h) }
func (h pendingHeap) Less(i, j int) bool {
	if h[i].at.Equal(h[j].at) {
		return h[i].seq < h[j].seq
	}
	return h[i].at.Before(h[j].at)
}
func (h pendingHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *pendingHeap) Push(x any)   { *h = append(*h, x.(*pending)) }
func (h *pendingHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// link is one direction: loss, then the rate-limited queue, then the delay.
type link struct {
	mu        sync.Mutex
	path      Path
	rng       *rand.Rand
	busyUntil time.Time // when the last queued datagram finishes being "transmitted"
	q         pendingHeap
	seq       uint64
	wake      chan struct{}
	closed    chan struct{}
	stats     struct{ in, delivered, lost, qdrop, bytes atomic.Int64 }
}

func newLink(p Path, seed int64, closed chan struct{}) *link {
	l := &link{path: p, rng: rand.New(rand.NewSource(seed)), wake: make(chan struct{}, 1), closed: closed}
	go l.run()
	return l
}

func (l *link) set(p Path) {
	l.mu.Lock()
	l.path = p
	l.mu.Unlock()
}

// submit takes a datagram that arrived now. send is called, from the link's own
// goroutine, when it is due out; the slice is the link's to give away.
func (l *link) submit(data []byte, send func([]byte)) {
	l.stats.in.Add(1)
	l.mu.Lock()
	p := l.path
	if p.Loss > 0 && l.rng.Float64() < p.Loss {
		l.mu.Unlock()
		l.stats.lost.Add(1)
		return
	}
	now := time.Now()
	depart := now
	if p.RateBps > 0 {
		limit := p.QueueBytes
		if limit == 0 {
			limit = 64 << 10
		}
		start := l.busyUntil
		if start.Before(now) {
			start = now
		}
		// Bytes still waiting to be sent, as the time the line stays busy implies.
		waiting := int(start.Sub(now).Seconds() * float64(p.RateBps) / 8)
		if waiting+len(data) > limit && waiting > 0 {
			l.mu.Unlock()
			l.stats.qdrop.Add(1)
			return
		}
		tx := time.Duration(float64(len(data)) * 8 / float64(p.RateBps) * float64(time.Second))
		l.busyUntil = start.Add(tx)
		depart = l.busyUntil
	}
	delay := p.Delay
	if p.Jitter > 0 {
		delay += time.Duration((l.rng.Float64()*2 - 1) * float64(p.Jitter))
	}
	l.seq++
	heap.Push(&l.q, &pending{at: depart.Add(delay), seq: l.seq, data: data, send: send})
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *link) run() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		l.mu.Lock()
		var next *pending
		if len(l.q) > 0 {
			next = l.q[0]
		}
		l.mu.Unlock()
		switch {
		case next == nil:
			select {
			case <-l.wake:
			case <-l.closed:
				return
			}
		case time.Until(next.at) > 0:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Until(next.at))
			select {
			case <-timer.C:
			case <-l.wake: // something earlier may have arrived
			case <-l.closed:
				return
			}
		default:
			l.mu.Lock()
			heap.Pop(&l.q)
			l.mu.Unlock()
			next.send(next.data)
			l.stats.delivered.Add(1)
			l.stats.bytes.Add(int64(len(next.data)))
		}
	}
}

func (l *link) snapshot() DirStats {
	return DirStats{
		In: int(l.stats.in.Load()), Delivered: int(l.stats.delivered.Load()), Lost: int(l.stats.lost.Load()),
		QueueDropped: int(l.stats.qdrop.Load()), Bytes: int(l.stats.bytes.Load()),
	}
}

// Proxy forwards UDP between viewers and one upstream host.
type Proxy struct {
	front    *net.UDPConn
	upstream *net.UDPAddr
	toHost   *link
	toViewer *link
	closed   chan struct{}
	once     sync.Once
	wg       sync.WaitGroup

	mu      sync.Mutex
	viewers map[string]*net.UDPConn // viewer address -> its socket towards the host
}

// Listen starts a proxy on listen (for example "127.0.0.1:0") that forwards to
// upstream.
func Listen(listen, upstream string, cfg Config) (*Proxy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	up, err := net.ResolveUDPAddr("udp", upstream)
	if err != nil {
		return nil, fmt.Errorf("netem: upstream: %w", err)
	}
	la, err := net.ResolveUDPAddr("udp", listen)
	if err != nil {
		return nil, fmt.Errorf("netem: listen address: %w", err)
	}
	front, err := net.ListenUDP("udp", la)
	if err != nil {
		return nil, err
	}
	closed := make(chan struct{})
	p := &Proxy{
		front: front, upstream: up, closed: closed, viewers: map[string]*net.UDPConn{},
		toHost:   newLink(cfg.ToHost, cfg.Seed, closed),
		toViewer: newLink(cfg.ToViewer, cfg.Seed+1, closed),
	}
	p.wg.Add(1)
	go p.fromViewers()
	return p, nil
}

// Addr is where viewers should connect.
func (p *Proxy) Addr() string { return p.front.LocalAddr().String() }

// Set changes both directions while the proxy runs, for example to simulate a
// blackout. Datagrams already queued keep the delay they were given.
func (p *Proxy) Set(cfg Config) {
	if err := cfg.validate(); err != nil {
		panic(err)
	}
	p.toHost.set(cfg.ToHost)
	p.toViewer.set(cfg.ToViewer)
}

// Stats reports what has happened so far.
func (p *Proxy) Stats() Stats {
	return Stats{ToHost: p.toHost.snapshot(), ToViewer: p.toViewer.snapshot()}
}

// Close stops the proxy; datagrams still waiting are dropped. It may be called twice.
func (p *Proxy) Close() error {
	p.once.Do(func() {
		close(p.closed)
		p.front.Close()
		p.mu.Lock()
		for _, c := range p.viewers {
			c.Close()
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	return nil
}

func (p *Proxy) fromViewers() {
	defer p.wg.Done()
	buf := make([]byte, 65536)
	for {
		n, from, err := p.front.ReadFromUDP(buf)
		if err != nil {
			return
		}
		back, err := p.backSocket(from)
		if err != nil {
			continue
		}
		data := append([]byte(nil), buf[:n]...)
		p.toHost.submit(data, func(b []byte) { back.Write(b) })
	}
}

// backSocket is the socket that carries this viewer's datagrams to the host and the
// host's replies back, made on first use.
func (p *Proxy) backSocket(viewer *net.UDPAddr) (*net.UDPConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.closed:
		return nil, errors.New("netem: closed")
	default:
	}
	if c, ok := p.viewers[viewer.String()]; ok {
		return c, nil
	}
	c, err := net.DialUDP("udp", nil, p.upstream)
	if err != nil {
		return nil, err
	}
	p.viewers[viewer.String()] = c
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		buf := make([]byte, 65536)
		for {
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			data := append([]byte(nil), buf[:n]...)
			p.toViewer.submit(data, func(b []byte) { p.front.WriteToUDP(b, viewer) })
		}
	}()
	return c, nil
}
