package remote

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ErrBacklog is returned by SendInput and SendText when the host is not keeping up
// and the queue of input waiting to be sent is full.
var ErrBacklog = errors.New("remote: the host is not keeping up with input")

var errOutboxClosed = errors.New("remote: the connection is closed")

// ErrFlushTimeout is returned by Flush when input was still waiting at the deadline.
var ErrFlushTimeout = errors.New("remote: input is still waiting to be sent")

const (
	maxOutbox           = 1024             // messages waiting to be written
	controlWriteTimeout = 10 * time.Second // longest one write to the control stream may take
)

// outMsg is one control message waiting to be written.
type outMsg struct {
	move  bool // a fire-and-forget pointer move, which a later move supersedes
	write func(io.Writer) error
}

// outbox queues what the viewer sends, so that the goroutine that produces input
// (the window's, which must not freeze) never waits for the network. The writer
// goroutine drains it in order.
//
// Only pointer moves are ever coalesced or dropped, because the latest position
// is all that matters. Anything else that cannot be queued is refused with
// ErrBacklog, never lost silently.
type outbox struct {
	mu           sync.Mutex
	wake         chan struct{}
	q            []outMsg
	pending      int   // messages queued or being written
	err          error // set once the connection is unusable
	writeTimeout time.Duration
}

func newOutbox() *outbox {
	return &outbox{wake: make(chan struct{}, 1), writeTimeout: controlWriteTimeout}
}

func (o *outbox) push(m outMsg) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	if m.move && len(o.q) > 0 && o.q[len(o.q)-1].move {
		o.q[len(o.q)-1] = m // the newest position replaces the one that was waiting
		return nil
	}
	if len(o.q) >= maxOutbox {
		if m.move {
			return nil // a position is superseded by the next one anyway
		}
		return ErrBacklog
	}
	o.q = append(o.q, m)
	o.pending++
	select {
	case o.wake <- struct{}{}:
	default:
	}
	return nil
}

// drain takes everything waiting.
func (o *outbox) drain() []outMsg {
	o.mu.Lock()
	defer o.mu.Unlock()
	q := o.q
	o.q = nil
	return q
}

// written records that n drained messages have been written.
func (o *outbox) written(n int) {
	o.mu.Lock()
	o.pending -= n
	o.mu.Unlock()
}

// flush waits until everything accepted so far has been written, the outbox has
// failed, or the timeout has passed.
func (o *outbox) flush(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		o.mu.Lock()
		err, pending := o.err, o.pending
		o.mu.Unlock()
		if err != nil {
			return err
		}
		if pending <= 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return ErrFlushTimeout
		}
		time.Sleep(time.Millisecond)
	}
}

// fail makes every later push return err.
func (o *outbox) fail(err error) {
	o.mu.Lock()
	if o.err == nil {
		o.err = err
	}
	o.q = nil
	o.pending = 0
	o.mu.Unlock()
}
