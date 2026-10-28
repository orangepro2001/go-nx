package remote

import (
	"sync/atomic"
	"time"
)

// DefaultFPS is the capture rate when Options.FPS is not set, and MaxFPS the
// fastest the host will go whatever FPSFn asks for.
const (
	DefaultFPS = 10
	MaxFPS     = 120
)

// Boost raises the capture rate for a while after the viewer last did something,
// and lets it fall back to save the host's CPU and the link while the viewer only
// watches. Hand Touch to Options.OnInput and Rate to Options.FPSFn. The zero value
// never boosts and reports DefaultFPS. It is safe for concurrent use.
type Boost struct {
	Base   int           // the rate while nothing happens; default DefaultFPS
	Active int           // the rate while the viewer is working; a value not above Base means no boost
	Hold   time.Duration // how long after its last input the viewer counts as working; default 2 s

	now  func() time.Time // for tests
	last atomic.Int64     // unix nanoseconds of the last input; 0 for none
}

// Touch records that the viewer just did something.
func (b *Boost) Touch() { b.last.Store(b.clock().UnixNano()) }

// Rate is the capture rate to use now.
func (b *Boost) Rate() int { return b.RateAt(b.Base) }

// RateAt is Rate for a base rate chosen at the moment of asking, for a host whose
// idle rate can be changed while it runs; Base is not consulted or changed.
func (b *Boost) RateAt(base int) int {
	if base <= 0 {
		base = DefaultFPS
	}
	if b.Active <= base {
		return base
	}
	hold := b.Hold
	if hold <= 0 {
		hold = 2 * time.Second
	}
	if last := b.last.Load(); last != 0 && b.clock().Sub(time.Unix(0, last)) < hold {
		return b.Active
	}
	return base
}

func (b *Boost) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}
