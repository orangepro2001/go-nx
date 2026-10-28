package remote

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
)

// ErrBlockedKey is returned for input the host refuses on purpose: Win+L, which
// locks the host and cannot be undone from the viewer.
var ErrBlockedKey = errors.New("remote: this key combination is blocked on this host")

var errNotActive = errors.New("remote: input session is not active")

const (
	vkL         = 0x4C
	vkLWin      = 0x5B
	vkRWin      = 0x5C
	maxStuck    = 64 // releases that failed and are being retried
	defaultIdle = 20 * time.Second
)

// InputController serializes remote input and ensures a disconnect or takeover
// releases keys and mouse buttons held by the previous connection.
//
// Letting go always goes the same way: mouse buttons first, then keys, each
// starting with the one pressed last, which is how a person would do it (the
// click, then Shift, then Ctrl). A release that the desktop refuses is kept and
// tried again the next time the controller is used, because a key left pressed
// on the host is worse than a delay.
type InputController struct {
	mu        sync.Mutex
	inj       Injector
	active    *sessionInputState
	stuck     []proto.Input
	allowWinL bool
	now       func() time.Time
}

func NewInputController(inj Injector) *InputController {
	return &InputController{inj: inj, now: time.Now}
}

// SetAllowWinL lets Win+L through. It is refused by default: it locks the host,
// and the viewer cannot unlock it.
func (c *InputController) SetAllowWinL(allow bool) {
	c.mu.Lock()
	c.allowWinL = allow
	c.mu.Unlock()
}

type sessionInputState struct {
	controller *InputController
	keys       []int32   // keys held, in the order they went down
	buttons    []int32   // mouse buttons held, in the order they went down
	last       time.Time // when this session last sent input
}

func (c *InputController) newSession() *sessionInputState {
	return &sessionInputState{controller: c}
}

// retryStuckLocked tries again the releases that failed earlier. Callers hold c.mu.
func (c *InputController) retryStuckLocked() {
	if len(c.stuck) == 0 {
		return
	}
	var still []proto.Input
	for _, in := range c.stuck {
		if c.inj.Inject(in) != nil {
			still = append(still, in)
		}
	}
	c.stuck = still
}

// Activate makes this authenticated connection the input owner. Any inputs
// held by its predecessor are released before this session can inject input.
// onActivate runs under the same lock so connection takeover and input ownership
// cannot get out of order when two peers authenticate concurrently.
func (s *sessionInputState) Activate(onActivate func()) error {
	c := s.controller
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retryStuckLocked()
	s.last = c.now()
	if c.active != s {
		var err error
		if c.active != nil {
			err = c.active.releaseLocked()
		}
		c.active = s
		if onActivate != nil {
			onActivate()
		}
		return err
	}
	if onActivate != nil {
		onActivate()
	}
	return nil
}

func (s *sessionInputState) Inject(in proto.Input) error {
	c := s.controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != s {
		return errNotActive
	}
	c.retryStuckLocked()
	s.last = c.now()
	if in.Kind == proto.KindKeyDown && !c.allowWinL && in.Arg&0xFF == vkL && s.winHeld() {
		return ErrBlockedKey
	}
	if err := c.inj.Inject(in); err != nil {
		return err
	}
	switch in.Kind {
	case proto.KindKeyDown:
		s.keys = addOnce(s.keys, in.Arg)
	case proto.KindKeyUp:
		s.keys = slices.DeleteFunc(s.keys, func(k int32) bool { return k == in.Arg })
	case proto.KindMouseDown:
		s.buttons = addOnce(s.buttons, in.Arg)
	case proto.KindMouseUp:
		s.buttons = slices.DeleteFunc(s.buttons, func(b int32) bool { return b == in.Arg })
	}
	return nil
}

// addOnce appends v unless it is there already: a key's auto-repeat is more
// KeyDowns of a key that is already held, not more keys.
func addOnce(list []int32, v int32) []int32 {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// winHeld reports whether a Windows key is held. The viewer sends the extended
// bit (0x100) with it, so only the low byte is the key.
func (s *sessionInputState) winHeld() bool {
	return slices.ContainsFunc(s.keys, func(k int32) bool { return k&0xFF == vkLWin || k&0xFF == vkRWin })
}

// InjectText types text through the host's injector, if it can, on behalf of the
// active session only.
func (s *sessionInputState) InjectText(text string) error {
	c := s.controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != s {
		return errNotActive
	}
	c.retryStuckLocked()
	s.last = c.now()
	t, ok := c.inj.(TextInjector)
	if !ok {
		return errTextUnsupported
	}
	return t.InjectText(text)
}

// ReleaseIfIdle lets go of everything this session holds if it has sent no input
// for idle. A viewer that vanished while holding a key is noticed by the
// connection only after the QUIC idle timeout, and the key stays down on the host
// until then; a viewer that is alive and holding a key keeps sending the key's
// auto-repeat, so it is never mistaken for one that is gone. It reports whether
// it released anything.
func (s *sessionInputState) ReleaseIfIdle(idle time.Duration) (bool, error) {
	c := s.controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != s || (len(s.keys) == 0 && len(s.buttons) == 0) || c.now().Sub(s.last) < idle {
		return false, nil
	}
	return true, s.releaseLocked()
}

// Release ends this session's ownership. A stale session cannot release input
// that a newer authenticated connection has since pressed.
func (s *sessionInputState) Release() error {
	c := s.controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != s {
		return nil
	}
	err := s.releaseLocked()
	c.active = nil
	return err
}

// releaseLocked lets go of what the session holds: buttons, then keys, most
// recently pressed first, each where the pointer is (proto.NoMove) so that
// cleaning up does not drag the pointer to where a button went down. What the
// desktop refuses is kept for another try. Callers hold c.mu.
func (s *sessionInputState) releaseLocked() error {
	c := s.controller
	var errs []error
	try := func(in proto.Input) {
		if err := c.inj.Inject(in); err != nil {
			errs = append(errs, err)
			if len(c.stuck) < maxStuck {
				c.stuck = append(c.stuck, in)
			}
		}
	}
	for _, b := range slices.Backward(s.buttons) {
		try(proto.Input{Kind: proto.KindMouseUp, X: proto.NoMove, Y: proto.NoMove, Arg: b})
	}
	for _, k := range slices.Backward(s.keys) {
		try(proto.Input{Kind: proto.KindKeyUp, Arg: k})
	}
	s.keys, s.buttons = nil, nil
	return errors.Join(errs...)
}
