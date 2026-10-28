package remote

import "fmt"

// The rates a host uses unless told otherwise.
const (
	DefaultActiveFPS = 30
	DefaultIdleFPS   = 2
	// MaxActiveFPS is the fastest rate a viewer's input can ask for. Capture, comparison
	// and encoding of every frame cost the host more than the picture is worth beyond
	// what a display shows.
	MaxActiveFPS = 60
)

// Rates is the capture-rate policy of a host, which follows what is going on: Idle
// once nothing has changed and the viewer has done nothing for a few seconds, Base
// while the picture is changing, Active for a couple of seconds after the viewer's
// last input. A zero Active or Idle turns that part off. Build the options of a
// session with Boost and Idle.
type Rates struct{ Base, Active, Idle int }

// DefaultRates is the policy of a host that was not told anything: 10 per second
// while watching, 30 while the viewer works, 2 while nothing happens.
func DefaultRates() Rates {
	return Rates{Base: DefaultFPS, Active: DefaultActiveFPS, Idle: DefaultIdleFPS}
}

// Normalized fills in and bounds the rates: Base is at least 1 (zero means
// DefaultFPS) and at most MaxFPS, Active only makes sense faster than Base and at
// most MaxActiveFPS, and Idle only slower than Base.
func (r Rates) Normalized() Rates {
	if r.Base <= 0 {
		r.Base = DefaultFPS
	}
	r.Base = min(r.Base, MaxFPS)
	r.Active = min(r.Active, MaxActiveFPS)
	if r.Active <= r.Base {
		r.Active = 0
	}
	if r.Idle < 0 || r.Idle >= r.Base {
		r.Idle = 0
	}
	return r
}

// Boost is the input-driven part of the policy, to be shared by every session of a
// host: hand its Touch to Options.OnInput and its Rate to Options.FPSFn.
func (r Rates) Boost() *Boost {
	n := r.Normalized()
	return &Boost{Base: n.Base, Active: n.Active}
}

func (r Rates) String() string {
	n := r.Normalized()
	return fmt.Sprintf("watching %d/s, working %s, idle %s", n.Base, perSecond(n.Active, "as watching"), perSecond(n.Idle, "as watching"))
}

func perSecond(n int, none string) string {
	if n == 0 {
		return none
	}
	return fmt.Sprintf("%d/s", n)
}
