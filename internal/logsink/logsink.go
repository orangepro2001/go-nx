// Package logsink is an Injector that touches nothing but its own memory and a
// log. It exists for safe end-to-end runs of the real host code: input is decoded,
// checked and acknowledged exactly as in production, but nothing reaches a desktop.
package logsink

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/orangepro2001/go-nx/internal/proto"
)

// HistoryLen is how many of the most recent inputs (and texts) a Sink remembers;
// the count of everything it accepted is kept regardless.
const HistoryLen = 256

// ErrOutOfScreen is returned for a pointer position outside the screen size given
// to SetScreen: something a real desktop would quietly clamp, which a test of the
// sender should not.
var ErrOutOfScreen = errors.New("logsink: pointer position outside the screen")

// Sink records every action it is given. It implements remote.Injector and
// remote.TextInjector, and is safe for concurrent use.
type Sink struct {
	mu     sync.Mutex
	log    *log.Logger
	n      int
	last   string
	inputs []proto.Input
	texts  []string
	w, h   int
}

// New returns a sink that logs one line per action to logger; nil logs nothing.
func New(logger *log.Logger) *Sink { return &Sink{log: logger} }

// SetScreen makes pointer inputs be checked against a w by h screen.
func (s *Sink) SetScreen(w, h int) {
	s.mu.Lock()
	s.w, s.h = w, h
	s.mu.Unlock()
}

// Inject implements remote.Injector.
func (s *Sink) Inject(in proto.Input) error {
	if in.Kind < proto.KindMouseMove || in.Kind > proto.KindWheelH {
		return fmt.Errorf("logsink: unknown input kind %d", in.Kind)
	}
	s.mu.Lock()
	if err := s.checkPosition(in); err != nil {
		s.mu.Unlock()
		return err
	}
	s.inputs = append(s.inputs, in)
	if len(s.inputs) > HistoryLen {
		s.inputs = append(s.inputs[:0], s.inputs[len(s.inputs)-HistoryLen:]...)
	}
	s.mu.Unlock()
	s.record(fmt.Sprintf("input op=%d kind=%d x=%d y=%d arg=%d", in.OpID, in.Kind, in.X, in.Y, in.Arg))
	return nil
}

// checkPosition applies to the kinds that carry a position the sender chose.
// Callers hold s.mu.
func (s *Sink) checkPosition(in proto.Input) error {
	if s.w == 0 {
		return nil
	}
	switch in.Kind {
	case proto.KindMouseMove, proto.KindMouseDown, proto.KindMouseUp, proto.KindWheel, proto.KindWheelH:
	default:
		return nil
	}
	if in.X == proto.NoMove && in.Y == proto.NoMove {
		return nil
	}
	if in.X < 0 || in.Y < 0 || int(in.X) >= s.w || int(in.Y) >= s.h {
		return fmt.Errorf("%w: %d,%d on a %dx%d screen", ErrOutOfScreen, in.X, in.Y, s.w, s.h)
	}
	return nil
}

// InjectText implements remote.TextInjector.
func (s *Sink) InjectText(text string) error {
	s.mu.Lock()
	s.texts = append(s.texts, text)
	if len(s.texts) > HistoryLen {
		s.texts = append(s.texts[:0], s.texts[len(s.texts)-HistoryLen:]...)
	}
	s.mu.Unlock()
	s.record(fmt.Sprintf("text %q", text))
	return nil
}

// Snapshot reports how many actions were accepted and the last one's description.
func (s *Sink) Snapshot() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n, s.last
}

// Inputs returns a copy of the most recent inputs, oldest first.
func (s *Sink) Inputs() []proto.Input {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]proto.Input(nil), s.inputs...)
}

// Typed returns a copy of the most recent texts, oldest first.
func (s *Sink) Typed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

func (s *Sink) record(desc string) {
	s.mu.Lock()
	s.n++
	s.last = desc
	logger := s.log
	s.mu.Unlock()
	if logger != nil {
		logger.Printf("TEST INPUT: %s", desc)
	}
}
