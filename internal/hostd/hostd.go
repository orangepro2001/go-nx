// Package hostd is the headless go-nx host: the same QUIC session, pairing and
// trust rules as the windowed program, driven by commands and reporting by events
// instead of a window. cmd/nx-host wraps it in a process that talks JSON Lines on
// stdin and stdout (docs/nx-host.md); an application that embeds it as a sidecar
// starts that process and never touches the screen or the input itself.
//
// The package has no Windows code. What is shared (the screen and the injector)
// is handed in as a Backend, so the whole host can run, and be tested, on a
// synthetic screen and an injector that only logs.
package hostd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/logsink"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// Backend is the part of the host that touches a machine: what to show and where
// input goes. A synthetic one never touches the desktop.
type Backend struct {
	Source   remote.Source
	Injector remote.Injector

	Name       string // the monitor's name, for events
	W, H, DPI  int    // its size and scaling when the host started
	SourceKind string // "gdi" or "pattern"
	InputKind  string // "sendinput" or "log"
	TestMode   bool   // synthetic screen and logged input only

	// Retarget, if set, is called with the new size when a source that can follow
	// resolution changes reports one, so that the injector can follow it too.
	Retarget func(w, h int)
	// Close, if set, releases what the backend holds.
	Close func()
}

// PatternBackend is a synthetic screen of the given scene and size with an
// injector that only logs. It never touches the desktop, so it is safe to run
// anywhere; the sink is returned so that tests can look at what was "injected".
func PatternBackend(scene synth.Scene, w, h int, logger *log.Logger) (Backend, *logsink.Sink, error) {
	src, err := synth.NewScene(scene, w, h)
	if err != nil {
		return Backend{}, nil, err
	}
	sink := logsink.New(logger)
	sink.SetScreen(w, h)
	return Backend{
		Source: src, Injector: sink, Name: "pattern", W: w, H: h, DPI: 96,
		SourceKind: "pattern", InputKind: "log", TestMode: true,
	}, sink, nil
}

// Config is how a host is set up.
type Config struct {
	Listen    string        // UDP address to listen on; port 0 lets the system choose
	StateDir  string        // identity, paired devices; created if missing
	FPS       int           // capture rate while nothing happens; default remote.DefaultFPS
	Boost     int           // capture rate while the viewer is working; 0 means no boost
	Idle      int           // capture rate once nothing has happened for IdleAfter; 0, or not below FPS, means no slow-down
	IdleAfter time.Duration // how long nothing must happen before Idle applies; default 3 s
	NoAuth    bool          // open mode from the start: anyone who can reach the host is let in
	AllowWinL bool          // let a viewer press Win+L (it locks the host)
	Version   string        // the program's version, for the ready event
	Backend   Backend       // what to show and where input goes
	Log       *log.Logger
}

// Host is one running host.
type Host struct {
	cfg      Config
	em       Emitter
	log      *log.Logger
	id       tls.Certificate
	identity string
	policy   *trust.Policy
	store    *trust.Store
	sessions *remote.Sessions
	input    *remote.InputController
	gate     *remote.Gate
	dedup    *proto.Dedup
	ln       *quic.Listener
	boost    *remote.Boost
	started  time.Time

	fps    atomic.Int32
	frames atomic.Uint64
	inputs atomic.Uint64

	mu         sync.Mutex
	active     *quic.Conn
	controller string // address of the connected viewer
	ctrlFP     string // its fingerprint
	startOnce  sync.Once
	stopReason string
	cancel     context.CancelFunc
	runCtx     context.Context
	stopped    chan struct{}
}

// New binds the listener and loads the identity and the paired devices. Nothing
// is announced and nobody is let in until Start.
func New(cfg Config, em Emitter) (*Host, error) {
	if cfg.Backend.Source == nil || cfg.Backend.Injector == nil {
		return nil, errors.New("hostd: the backend needs a source and an injector")
	}
	if cfg.StateDir == "" {
		return nil, errors.New("hostd: a state directory is required")
	}
	if cfg.FPS <= 0 {
		cfg.FPS = remote.DefaultFPS
	}
	cfg.FPS = min(cfg.FPS, MaxControlFPS)
	logger := cfg.Log
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("hostd: state directory: %w", err)
	}
	id, err := trust.Identity(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("hostd: identity: %w", err)
	}
	store, backup, err := trust.OpenStoreRecovering(filepath.Join(cfg.StateDir, "trusted.json"))
	if err != nil {
		return nil, fmt.Errorf("hostd: paired devices: %w", err)
	}
	if backup != "" {
		logger.Printf("the paired-devices file was damaged and was set aside as %s; no devices are paired", backup)
		em.Emit(Event{Event: "warning", Message: "the paired-devices file was damaged and was set aside as " + backup})
	}
	policy := trust.NewPolicy(store)
	policy.SetRequireAuth(!cfg.NoAuth)
	ln, err := remote.Listen(cfg.Listen, id)
	if err != nil {
		return nil, fmt.Errorf("hostd: listen on %s: %w (is another process using it?)", cfg.Listen, err)
	}
	h := &Host{
		cfg: cfg, em: em, log: logger, id: id, identity: trust.Fingerprint(id.Certificate[0]),
		policy: policy, store: store, ln: ln, started: time.Now(),
		sessions: remote.NewSessions(policy.RequireAuth),
		input:    remote.NewInputController(cfg.Backend.Injector),
		gate:     remote.NewGate(16, 4),
		dedup:    proto.NewDedup(),
		stopped:  make(chan struct{}),
	}
	h.input.SetAllowWinL(cfg.AllowWinL)
	h.fps.Store(int32(cfg.FPS))
	if cfg.Boost > cfg.FPS {
		h.boost = &remote.Boost{Active: min(cfg.Boost, MaxControlFPS)}
	}
	return h, nil
}

// MaxControlFPS is the highest capture rate a controller can ask for.
const MaxControlFPS = 60

// Addr is the address the host is listening on, with the port the system chose.
func (h *Host) Addr() string { return h.ln.Addr().String() }

// Identity is the host's certificate fingerprint: what a viewer pins.
func (h *Host) Identity() string { return h.identity }

// Counts reports how many pictures were sent and how many inputs arrived.
func (h *Host) Counts() (frames, inputs uint64) { return h.frames.Load(), h.inputs.Load() }

// Start announces the host (the ready event) and starts letting viewers in. It
// returns at once; Wait blocks until the host has stopped.
func (h *Host) Start(ctx context.Context) {
	h.startOnce.Do(func() {
		h.runCtx, h.cancel = context.WithCancel(ctx)
		h.em.Emit(h.ready())
		if h.cfg.Backend.TestMode {
			h.em.Emit(Event{Event: "warning", Message: "TEST MODE: synthetic screen and logged input; no screen capture or OS input"})
		}
		if h.cfg.NoAuth {
			h.em.Emit(Event{Event: "warning", Message: "open mode: anyone who can reach this host is let in without verification"})
		}
		go h.run()
	})
}

// Stop ends the host. The first reason given is the one reported.
func (h *Host) Stop(reason string) {
	h.mu.Lock()
	if h.stopReason == "" {
		h.stopReason = reason
	}
	h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
	}
}

// Wait blocks until the host has stopped and said so.
func (h *Host) Wait() { <-h.stopped }

func (h *Host) ready() Event {
	b := h.cfg.Backend
	return Event{
		Schema: SchemaVersion, Event: "ready", Version: h.cfg.Version, PID: os.Getpid(),
		Listen: h.Addr(), Display: b.Name, Width: b.W, Height: b.H, DPI: b.DPI,
		Identity: h.identity, FPS: int(h.fps.Load()), RequireAuth: ptr(h.policy.RequireAuth()),
		TestMode: b.TestMode, Source: b.SourceKind, InputMode: b.InputKind, Paired: ptr(h.policy.TrustedCount()),
	}
}

func ptr[T any](v T) *T { return &v }

func (h *Host) run() {
	defer close(h.stopped)
	go func() { <-h.runCtx.Done(); h.ln.Close() }()
	for {
		conn, err := h.ln.Accept(h.runCtx)
		if err != nil {
			break
		}
		go h.serve(conn)
	}
	h.mu.Lock()
	if h.active != nil {
		h.active.CloseWithError(0, "host stopped")
	}
	reason := h.stopReason
	h.mu.Unlock()
	if reason == "" {
		reason = "shutdown"
	}
	if h.cfg.Backend.Close != nil {
		h.cfg.Backend.Close()
	}
	h.em.Emit(Event{Event: "stopped", Reason: reason})
}

// captureFPS is the capture rate to use now: the configured one, or the boost
// while the viewer is working.
func (h *Host) captureFPS() int {
	if h.boost != nil {
		return h.boost.RateAt(int(h.fps.Load()))
	}
	return int(h.fps.Load())
}

func (h *Host) check(fp, addr, password string) (trust.Verdict, error) {
	v, err := h.policy.Check(fp, addr, password)
	if !v.Admitted() {
		reason := map[trust.Verdict]string{
			trust.Untrusted: "untrusted", trust.BadPassword: "bad-code", trust.Locked: "locked",
		}[v]
		if reason == "" {
			reason = "refused"
		}
		h.em.Emit(Event{Event: "denied", Addr: addr, Reason: reason, Identity: fp})
		h.log.Printf("refused %s: %s (%s; %s)", addr, reason, fp, h.policy.Explain(password))
	}
	return v, err
}

func (h *Host) serve(conn *quic.Conn) {
	addr := conn.RemoteAddr().String()
	peerFP := peerFingerprint(conn)
	announced := false
	err := remote.Serve(h.runCtx, conn, h.cfg.Backend.Source, h.cfg.Backend.Injector, h.dedup, remote.Options{
		FPS:             h.cfg.FPS,
		FPSFn:           h.captureFPS,
		IdleFPS:         h.cfg.Idle,
		IdleAfter:       h.cfg.IdleAfter,
		Check:           h.check,
		InputController: h.input,
		Gate:            h.gate,
		Sessions:        h.sessions,
		HostName:        remote.ComputerName(),
		OnInput: func() {
			h.inputs.Add(1)
			if h.boost != nil {
				h.boost.Touch()
			}
		},
		OnFrame: func(uint32, int) { h.frames.Add(1) },
		OnSourceChanged: func(w, hh int) {
			if h.cfg.Backend.Retarget != nil {
				h.cfg.Backend.Retarget(w, hh)
			}
		},
		OnPeer: func(p remote.PeerInfo) {
			announced = true
			h.em.Emit(Event{Event: "connected", Addr: addr, Identity: peerFP, Name: p.Name, Protocol: ptr(p.Version)})
			h.log.Printf("viewer %s connected (protocol %d, name %q)", addr, p.Version, p.Name)
		},
		OnError: func(err error) { h.log.Printf("session: %v", err) },
		OnAuthed: func() {
			h.mu.Lock()
			previous := h.active
			h.active, h.controller, h.ctrlFP = conn, addr, peerFP
			h.mu.Unlock()
			if previous != nil {
				previous.CloseWithError(remote.CloseReplaced, "replaced by a new viewer")
			}
		},
	})
	h.mu.Lock()
	if h.active == conn {
		h.active, h.controller, h.ctrlFP = nil, "", ""
	}
	h.mu.Unlock()
	if errors.Is(err, remote.ErrDenied) || !announced {
		if err != nil && !errors.Is(err, remote.ErrDenied) {
			h.log.Printf("viewer %s: %v", addr, err)
		}
		return
	}
	reason, msg := disconnectReason(context.Cause(conn.Context()), err)
	h.em.Emit(Event{Event: "disconnected", Addr: addr, Reason: reason, Message: msg})
	h.log.Printf("viewer %s disconnected: %s", addr, reason)
}

// disconnectReason names why a session ended with a stable word and, if there is
// more to say, a sentence. cause is why the connection was closed (it carries the
// code this host closed it with, when it did) and err is what Serve returned.
func disconnectReason(cause, err error) (reason, message string) {
	var ae *quic.ApplicationError
	if errors.As(cause, &ae) && !ae.Remote {
		switch ae.ErrorCode {
		case remote.CloseReplaced:
			return "replaced", ""
		case remote.CloseRevoked:
			return "revoked", ""
		case remote.CloseUnverified:
			return "unverified", ""
		}
	}
	// A viewer that hangs up politely closes with code 0, which Serve may see as
	// an error from the read or as a closed connection, depending on timing.
	if errors.As(err, &ae) && ae.Remote && ae.ErrorCode == 0 {
		return "closed", ""
	}
	var idle *quic.IdleTimeoutError
	if errors.As(err, &idle) || errors.As(cause, &idle) {
		return "timeout", "the viewer went silent"
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return "error", err.Error()
	}
	return "closed", ""
}

func peerFingerprint(conn *quic.Conn) string {
	if certs := conn.ConnectionState().TLS.PeerCertificates; len(certs) > 0 {
		return trust.Fingerprint(certs[0].Raw)
	}
	return ""
}

// kick ends the session of the connected viewer, if there is one and (when fp is
// not empty) it is that device. It reports whether it ended one.
func (h *Host) kick(fp string, code quic.ApplicationErrorCode, why string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active == nil || (fp != "" && h.ctrlFP != fp) {
		return false
	}
	h.active.CloseWithError(code, why)
	return true
}

// status is a snapshot for the status event.
func (h *Host) status() Event {
	h.mu.Lock()
	ctrl := h.controller
	h.mu.Unlock()
	b := h.cfg.Backend
	trusted := h.store.Keys()
	slices.Sort(trusted)
	frames, inputs := h.Counts()
	return Event{
		Event: "status", FPS: int(h.fps.Load()), Trusted: trusted, RequireAuth: ptr(h.policy.RequireAuth()),
		Listen: h.Addr(), Display: b.Name, Width: b.W, Height: b.H, Identity: h.identity, Addr: ctrl,
		Source: b.SourceKind, InputMode: b.InputKind, TestMode: b.TestMode,
		Frames: &frames, Inputs: &inputs, Uptime: ptr(int(time.Since(h.started).Seconds())),
	}
}
