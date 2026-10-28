package remote

import (
	"errors"
	"sync"

	"github.com/orangepro2001/go-nx/internal/proto"
)

// InputController serializes remote input and ensures a disconnect or takeover
// releases keys and mouse buttons held by the previous connection.
type InputController struct {
	mu     sync.Mutex
	inj    Injector
	active *sessionInputState
}

func NewInputController(inj Injector) *InputController {
	return &InputController{inj: inj}
}

type sessionInputState struct {
	controller *InputController
	keys       map[int32]struct{}
	buttons    map[int32]proto.Input
}

func (c *InputController) newSession() *sessionInputState {
	return &sessionInputState{
		controller: c,
		keys:       make(map[int32]struct{}),
		buttons:    make(map[int32]proto.Input),
	}
}

// Activate makes this authenticated connection the input owner. Any inputs
// held by its predecessor are released before this session can inject input.
// onActivate runs under the same lock so connection takeover and input ownership
// cannot get out of order when two peers authenticate concurrently.
func (s *sessionInputState) Activate(onActivate func()) error {
	c := s.controller
	c.mu.Lock()
	defer c.mu.Unlock()
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
		return errors.New("remote: input session is not active")
	}
	if err := c.inj.Inject(in); err != nil {
		return err
	}
	switch in.Kind {
	case proto.KindKeyDown:
		s.keys[in.Arg] = struct{}{}
	case proto.KindKeyUp:
		delete(s.keys, in.Arg)
	case proto.KindMouseDown:
		release := in
		release.Kind = proto.KindMouseUp
		s.buttons[in.Arg] = release
	case proto.KindMouseUp:
		delete(s.buttons, in.Arg)
	}
	return nil
}

// InjectText types text through the host's injector, if it can, on behalf of the
// active session only.
func (s *sessionInputState) InjectText(text string) error {
	c := s.controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != s {
		return errors.New("remote: input session is not active")
	}
	t, ok := c.inj.(TextInjector)
	if !ok {
		return errTextUnsupported
	}
	return t.InjectText(text)
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

func (s *sessionInputState) releaseLocked() error {
	c := s.controller
	var errs []error
	for key := range s.keys {
		if err := c.inj.Inject(proto.Input{Kind: proto.KindKeyUp, Arg: key}); err != nil {
			errs = append(errs, err)
		}
	}
	for _, release := range s.buttons {
		if err := c.inj.Inject(release); err != nil {
			errs = append(errs, err)
		}
	}
	clear(s.keys)
	clear(s.buttons)
	return errors.Join(errs...)
}
