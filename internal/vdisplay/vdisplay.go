// Package vdisplay shows a remote session on a virtual display made at the size of the
// viewer's window, with the Parsec Virtual Display Driver (docs/plan-virtual-display.md).
//
// While the session is on it, the virtual display is the only active one: the person at the
// other end sees the whole desktop at their own resolution, and the host's own monitors are
// dark. Everything is put back when the viewer asks for the host's screen again, when the
// session ends and when go-nx exits; the topology to go back to is kept on disk first, so a
// crash is mended at the next start.
//
// The order of the steps is the point of the package, and it is tested here with a fake
// System; system_windows.go is the real one.
package vdisplay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/orangepro2001/go-nx/internal/proto"
)

// Mode is a display mode.
type Mode struct{ W, H, Hz int }

func (m Mode) String() string { return fmt.Sprintf("%dx%d@%d", m.W, m.H, m.Hz) }

// Pick chooses, from the modes a display offers, the one to use for a viewer whose window is
// w x h: the largest that fits inside it (so nothing has to be shrunk), at 60 Hz if that
// size has it, else its highest rate. If none fits, the smallest. ok is false for no modes.
func Pick(modes []Mode, w, h int) (best Mode, ok bool) {
	better := func(a, b Mode) bool { // is a a better choice than b?
		fa, fb := a.W <= w && a.H <= h, b.W <= w && b.H <= h
		switch {
		case fa != fb:
			return fa
		case a.W*a.H != b.W*b.H:
			if fa {
				return a.W*a.H > b.W*b.H
			}
			return a.W*a.H < b.W*b.H
		case (a.Hz == 60) != (b.Hz == 60):
			return a.Hz == 60
		}
		return a.Hz > b.Hz
	}
	for _, m := range modes {
		if m.W <= 0 || m.H <= 0 {
			continue
		}
		if !ok || better(m, best) {
			best, ok = m, true
		}
	}
	return best, ok
}

// Topology is a saved arrangement of the active displays, in Windows' own encoding
// (QueryDisplayConfig's paths and modes), so it can be put back exactly.
type Topology struct {
	Paths []byte `json:"paths"`
	Modes []byte `json:"modes"`
}

// System is what the manager needs from the operating system and the driver.
type System interface {
	// Ready says why a virtual display cannot be used now (no driver, a remote desktop
	// session, another program managing the driver, no monitor to come back to), or nil.
	Ready() error
	// Add makes a virtual display and returns the index the driver knows it by and the
	// device name Windows gave it.
	Add() (index int, device string, err error)
	Remove(index int) error
	Modes(device string) ([]Mode, error)
	SetMode(device string, m Mode) error
	// Snapshot is the active topology; UseOnly leaves device as the only active display;
	// Restore puts a snapshot back.
	Snapshot() (Topology, error)
	UseOnly(device string) error
	Restore(Topology) error
}

// Capture is the screen capture of the host: which monitor it shows, and switching it.
type Capture interface {
	Device() string
	Use(device string) error
}

// Store keeps the topology to go back to across a crash.
type Store interface {
	Save(Topology) error
	Load() (Topology, bool)
	Clear() error
}

// FileStore is a Store in one file.
type FileStore string

func (f FileStore) Save(t Topology) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(string(f)), 0o700); err != nil {
		return err
	}
	tmp := string(f) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, string(f))
}

func (f FileStore) Load() (Topology, bool) {
	b, err := os.ReadFile(string(f))
	if err != nil {
		return Topology{}, false
	}
	var t Topology
	if json.Unmarshal(b, &t) != nil || len(t.Paths) == 0 {
		return Topology{}, false
	}
	return t, true
}

func (f FileStore) Clear() error {
	if err := os.Remove(string(f)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Manager puts sessions on a virtual display and back. Its methods may be called from any
// goroutine; the work is done one request at a time.
type Manager struct {
	sys   System
	cap   Capture
	store Store
	logf  func(format string, args ...any)

	mu      sync.Mutex
	pending *request // the latest request not yet handled; older ones are dropped
	busy    bool
	cur     *active
	idle    *sync.Cond
}

type request struct {
	owner  any // the session that asked; nil from Release(nil)
	w, h   int
	report func(proto.DisplayState)
}

// active is a virtual display in use.
type active struct {
	index    int
	device   string
	mode     Mode
	original string   // the monitor the capture showed before
	saved    Topology // what to put back
	owner    any      // the session using it
}

// NewManager makes a manager. logf may be nil.
func NewManager(sys System, cap Capture, store Store, logf func(string, ...any)) *Manager {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	m := &Manager{sys: sys, cap: cap, store: store, logf: logf}
	m.idle = sync.NewCond(&m.mu)
	return m
}

// Recover puts back a topology left on disk by a go-nx that ended while a session was on a
// virtual display. Call it once at start, before anything else.
func (m *Manager) Recover() error {
	t, ok := m.store.Load()
	if !ok {
		return nil
	}
	m.logf("virtual display: putting back the displays as they were before go-nx last ended")
	if err := m.sys.Restore(t); err != nil {
		return fmt.Errorf("putting the displays back: %w", err)
	}
	return m.store.Clear()
}

// Request asks, for the session owner, for a virtual display for a w x h window, or with
// 0 x 0 for the host's own screen. It returns at once; report is told what came of it. A
// request that arrives while another is being handled replaces any still waiting. A
// session that asks while another one's virtual display is in use (it took over the host)
// takes the display over.
func (m *Manager) Request(owner any, w, h int, report func(proto.DisplayState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = &request{owner, w, h, report}
	if !m.busy {
		m.busy = true
		go m.run()
	}
}

// Release puts the host's screen back if owner's virtual display is in use, and waits until
// it is done: for the end of a session. A nil owner means whoever's: for go-nx exiting, and
// for the host no longer allowing virtual displays. A session that ended after another took
// the host over leaves the new one's display alone.
func (m *Manager) Release(owner any) {
	m.mu.Lock()
	if p := m.pending; p != nil && owner != nil && p.owner != owner {
		m.mu.Unlock() // someone else's request is next: theirs stands
		return
	}
	if owner != nil && m.cur != nil && m.cur.owner != owner && !m.busy {
		m.mu.Unlock()
		return
	}
	m.pending = &request{owner: owner, report: func(proto.DisplayState) {}}
	if !m.busy {
		m.busy = true
		go m.run()
	}
	for m.busy {
		m.idle.Wait()
	}
	m.mu.Unlock()
}

// Active reports the virtual display in use, if any.
func (m *Manager) Active() (device string, mode Mode, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return "", Mode{}, false
	}
	return m.cur.device, m.cur.mode, true
}

func (m *Manager) run() {
	m.mu.Lock()
	for m.pending != nil {
		r := m.pending
		m.pending = nil
		m.mu.Unlock()
		st := m.handle(r.owner, r.w, r.h)
		if r.report != nil {
			r.report(st)
		}
		m.mu.Lock()
	}
	m.busy = false
	m.idle.Broadcast()
	m.mu.Unlock()
}

// handle does one request. Only run calls it, so only one runs at a time.
func (m *Manager) handle(owner any, w, h int) proto.DisplayState {
	m.mu.Lock()
	cur := m.cur
	if cur != nil && owner != nil && w > 0 && h > 0 {
		cur.owner = owner // a session that took the host over takes its display too
	}
	m.mu.Unlock()
	if w <= 0 || h <= 0 {
		if cur == nil || owner != nil && cur.owner != owner {
			return proto.DisplayState{}
		}
		if err := m.putBack(cur); err != nil {
			return m.state(err.Error())
		}
		return proto.DisplayState{}
	}
	if cur != nil {
		return m.resize(cur, w, h)
	}
	if err := m.sys.Ready(); err != nil {
		return proto.DisplayState{Message: err.Error()}
	}
	a, err := m.start(w, h)
	if err != nil {
		m.logf("virtual display: %v", err)
		return proto.DisplayState{Message: err.Error()}
	}
	a.owner = owner
	m.mu.Lock()
	m.cur = a
	m.mu.Unlock()
	m.logf("virtual display: session on %s at %s", a.device, a.mode)
	return proto.DisplayState{Virtual: true, W: uint16(a.mode.W), H: uint16(a.mode.H)}
}

// state is what to report while a virtual display may or may not be in use.
func (m *Manager) state(msg string) proto.DisplayState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return proto.DisplayState{Message: msg}
	}
	return proto.DisplayState{Virtual: true, W: uint16(m.cur.mode.W), H: uint16(m.cur.mode.H), Message: msg}
}

// start makes the virtual display and moves the session onto it. Each step that fails
// undoes the ones before it.
func (m *Manager) start(w, h int) (*active, error) {
	saved, err := m.sys.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("reading the display layout: %w", err)
	}
	if err := m.store.Save(saved); err != nil {
		return nil, fmt.Errorf("saving the display layout: %w", err)
	}
	index, device, err := m.sys.Add()
	if err != nil {
		m.store.Clear()
		return nil, fmt.Errorf("making a virtual display: %w", err)
	}
	a := &active{index: index, device: device, original: m.cap.Device(), saved: saved}
	undo := func(step string, err error) (*active, error) {
		if a.original != "" && m.cap.Device() != a.original {
			if e := m.cap.Use(a.original); e != nil {
				m.logf("virtual display: back to %s: %v", a.original, e)
			}
		}
		if e := m.sys.Restore(saved); e != nil {
			m.logf("virtual display: putting the layout back after a failure: %v", e)
			// The layout on disk is kept for the next start, and so is the virtual
			// display: it may be the only one showing anything.
			return nil, fmt.Errorf("%s: %w (and putting the displays back failed: %v)", step, err, e)
		}
		if e := m.sys.Remove(index); e != nil {
			m.logf("virtual display: removing it after a failure: %v", e)
		}
		m.store.Clear()
		return nil, fmt.Errorf("%s: %w", step, err)
	}
	modes, err := m.sys.Modes(device)
	if err != nil {
		return undo("reading the modes of the virtual display", err)
	}
	mode, ok := Pick(modes, w, h)
	if !ok {
		return undo("choosing a mode", errors.New("the virtual display offers none"))
	}
	if err := m.sys.SetMode(device, mode); err != nil {
		return undo("setting "+mode.String(), err)
	}
	a.mode = mode
	if err := m.cap.Use(device); err != nil {
		return undo("capturing the virtual display", err)
	}
	if err := m.sys.UseOnly(device); err != nil {
		return undo("turning the other displays off", err)
	}
	return a, nil
}

// resize moves the virtual display to the mode that suits a new window size.
func (m *Manager) resize(a *active, w, h int) proto.DisplayState {
	modes, err := m.sys.Modes(a.device)
	if err != nil {
		return m.state(err.Error())
	}
	mode, ok := Pick(modes, w, h)
	if !ok || mode == a.mode {
		return m.state("")
	}
	if err := m.sys.SetMode(a.device, mode); err != nil {
		return m.state(fmt.Sprintf("setting %s: %v", mode, err))
	}
	m.mu.Lock()
	a.mode = mode
	m.mu.Unlock()
	m.logf("virtual display: now %s", mode)
	return m.state("")
}

// putBack restores the displays as they were and removes the virtual one. If the layout
// cannot be put back, the virtual display stays (it may be the only one showing anything)
// and so does the layout on disk.
func (m *Manager) putBack(a *active) error {
	if err := m.sys.Restore(a.saved); err != nil {
		m.logf("virtual display: putting the displays back: %v", err)
		return fmt.Errorf("putting the displays back: %w", err)
	}
	if err := m.cap.Use(a.original); err != nil {
		m.logf("virtual display: capturing %s again: %v", a.original, err)
	}
	if err := m.sys.Remove(a.index); err != nil {
		m.logf("virtual display: removing it: %v", err)
	}
	if err := m.store.Clear(); err != nil {
		m.logf("virtual display: %v", err)
	}
	m.mu.Lock()
	m.cur = nil
	m.mu.Unlock()
	m.logf("virtual display: the host's own screen is back")
	return nil
}
