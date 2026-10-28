package hostd

import (
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Emitter receives the host's events.
type Emitter interface {
	Emit(Event)
}

// LineEmitter writes events as JSON Lines, one object per line, in the order they
// were emitted, from one goroutine. A host never blocks on a slow controller: Emit
// only queues. A controller that stops reading, or whose pipe is broken, is taken
// to be gone, and the owner is told once (onGone) so that it can shut the host down;
// a host that lived on without anyone able to hear it would be a host nobody
// can stop.
type LineEmitter struct {
	w       io.Writer
	timeout time.Duration
	onGone  func(reason string)

	ch       chan []byte
	quit     chan struct{}
	done     chan struct{}
	mu       sync.RWMutex // guards closed against sends on ch
	closed   bool
	writing  atomic.Int64 // unix nanoseconds the current write began; 0 when idle
	once     sync.Once
	quitOnce sync.Once
}

const emitQueue = 1024

// NewLineEmitter starts a writer for w. A write that takes longer than timeout, or
// fails, calls onGone ("write-timeout" or "write-failed") once. timeout <= 0 means
// 30 s.
func NewLineEmitter(w io.Writer, timeout time.Duration, onGone func(reason string)) *LineEmitter {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	e := &LineEmitter{
		w: w, timeout: timeout, onGone: onGone,
		ch: make(chan []byte, emitQueue), quit: make(chan struct{}), done: make(chan struct{}),
	}
	go e.write()
	go e.watch()
	return e
}

func (e *LineEmitter) gone(reason string) {
	e.once.Do(func() {
		if e.onGone != nil {
			e.onGone(reason)
		}
	})
}

// Emit queues one event. It blocks only when the queue is full, and then for at
// most the timeout, after which the controller is taken to be gone.
func (e *LineEmitter) Emit(ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	b = append(b, '\n')
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return
	}
	select {
	case e.ch <- b:
	default:
		t := time.NewTimer(e.timeout)
		defer t.Stop()
		select {
		case e.ch <- b:
		case <-t.C:
			e.gone("write-timeout")
		case <-e.quit:
		}
	}
}

func (e *LineEmitter) write() {
	defer close(e.done)
	failed := false
	for b := range e.ch {
		if failed {
			continue // keep draining so that Emit never blocks on a dead pipe
		}
		e.writing.Store(time.Now().UnixNano())
		_, err := e.w.Write(b)
		e.writing.Store(0)
		if err != nil {
			failed = true
			e.gone("write-failed")
		}
	}
}

// watch notices a write that never returns: the controller's pipe is full and
// nobody is reading it.
func (e *LineEmitter) watch() {
	tick := max(e.timeout/4, 5*time.Millisecond)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-e.quit:
			return
		case <-t.C:
			if since := e.writing.Load(); since != 0 && time.Since(time.Unix(0, since)) > e.timeout {
				e.gone("write-timeout")
				return
			}
		}
	}
}

// Close stops accepting events, and waits (at most the timeout) for the queued ones
// to be written.
func (e *LineEmitter) Close() {
	e.quitOnce.Do(func() { close(e.quit) }) // wakes an Emit that is waiting for room
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	close(e.ch)
	e.mu.Unlock()
	select {
	case <-e.done:
	case <-time.After(e.timeout):
	}
}
