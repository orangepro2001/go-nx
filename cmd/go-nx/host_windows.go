//go:build windows

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/app"
	"github.com/orangepro2001/go-nx/internal/capture"
	"github.com/orangepro2001/go-nx/internal/inject"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/trust"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

const pairingWindow = 2 * time.Minute

// hostState is the hosting side: who may connect, and what the UI shows about it.
// The decisions themselves are made by trust.Policy.
type hostState struct {
	mu         sync.Mutex
	Port       int
	Addrs      []string
	ID         string // this PC's short fingerprint, for display
	Err        string // non-empty if hosting could not start
	Controller string // remote address currently controlling this PC
	CtrlFP     string // its certificate fingerprint, so revoking that device can end its session
	CtrlOpen   bool   // it was let in without verification (open mode)
	Rejected   string // address of the last device turned away
	RejectedBy app.Reason
	policy     *trust.Policy
	trusted    *trust.Store
	settings   *trust.Store
	seen       map[string]seenPeer    // by remote address: who is connecting and how they were judged
	notifyFn   atomic.Pointer[func()] // something the interface shows has changed
	rates      remote.Rates
	kick       func(code quic.ApplicationErrorCode, reason string) // closes the current controller; set by acceptLoop
	sessions   *remote.Sessions                                    // which live sessions were let in without verification
}

func (h *hostState) fail(err error) {
	log.Printf("host disabled: %v", err)
	h.mu.Lock()
	h.Err = err.Error()
	h.mu.Unlock()
}

// seenPeer is a connection that is being judged, or was.
type seenPeer struct {
	fp       string
	verified bool
}

// changed tells the interface that something about the host moved.
func (h *hostState) changed() {
	if f := h.notifyFn.Load(); f != nil {
		(*f)()
	}
}

// onChange sets what is called when something about the host moves (a device connects, the
// mode changes, pairing opens or ends).
func (h *hostState) onChange(f func()) { h.notifyFn.Store(&f) }

// StartPairing opens a pairing window with a fresh six-digit code, which the This PC page
// shows and the new device enters as its password.
func (h *hostState) StartPairing() {
	h.policy.OpenPairing(trust.NewPairingCode(), pairingWindow)
	h.mu.Lock()
	h.Rejected = ""
	h.mu.Unlock()
	log.Print("pairing opened")
	h.changed()
}

// ClosePairing ends the pairing window without a device having used it.
func (h *hostState) ClosePairing() {
	h.policy.ClosePairing()
	log.Print("pairing closed")
	h.changed()
}

// RevokeAll removes every paired device and drops the controller that is connected now, so
// a revoked device does not keep a session it already has. It returns how many devices were
// forgotten.
func (h *hostState) RevokeAll() (int, error) {
	n, err := h.policy.RevokeAll()
	if h.kick != nil {
		h.kick(remote.CloseRevoked, "removed from the paired devices")
	}
	log.Printf("forgot %d paired device(s)", n)
	h.changed()
	return n, err
}

// Revoke removes one paired device, ending its session if it has one.
func (h *hostState) Revoke(fp string) error {
	err := h.policy.Revoke(fp)
	h.mu.Lock()
	controlling := h.CtrlFP == fp
	h.mu.Unlock()
	if controlling && h.kick != nil {
		h.kick(remote.CloseRevoked, "removed from the paired devices")
	}
	log.Printf("forgot paired device %s…", fp[:min(12, len(fp))])
	h.changed()
	return err
}

// DisconnectController ends the session of the device controlling this PC.
func (h *hostState) DisconnectController() {
	if h.kick != nil {
		h.kick(remote.CloseKicked, "disconnected from the host")
	}
}

// Paired lists the paired devices, most recent first. The store keeps, for each device's
// fingerprint, the address and the time it paired ("10.0.0.7:5000 2026-10-28 09:30").
func (h *hostState) Paired() []app.PairedDevice {
	var out []app.PairedDevice
	for _, fp := range h.trusted.Keys() {
		v, _ := h.trusted.Get(fp)
		d := app.PairedDevice{Fingerprint: fp, Addr: v}
		if i := strings.LastIndex(v, " "); i > 0 {
			if j := strings.LastIndex(v[:i], " "); j > 0 {
				if t, err := time.ParseInLocation("2006-01-02 15:04", v[j+1:], time.Local); err == nil {
					d.Addr, d.When = v[:j], t
				}
			}
		}
		if i := strings.LastIndex(d.Addr, ":"); i > 0 { // the source port means nothing to a person
			d.Addr = d.Addr[:i]
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out
}

// Info is a snapshot for the interface.
func (h *hostState) Info() app.HostInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return app.HostInfo{
		Err: h.Err, Addrs: h.Addrs, Port: h.Port, Paired: h.policy.TrustedCount(), ID: h.ID,
		Controller: h.Controller, ControllerVerified: h.Controller != "" && !h.CtrlOpen,
		Rejected: h.Rejected, RejectedWhy: h.RejectedBy,
		PairingLeft: h.policy.PairingLeft(), PairingCode: h.policy.PairingCode(),
		RequireAuth: h.requireAuth(),
	}
}

func (h *hostState) pairingLeft() time.Duration { return h.policy.PairingLeft() }
func (h *hostState) trustedCount() int          { return h.policy.TrustedCount() }

// requireAuthDefault is what a host that never chose gets: pairing or a password
// is required. A saved "0" or "1" from the settings screen always wins.
const requireAuthDefault = true

// requireAuth reports whether pairing/password checks are on.
func (h *hostState) requireAuth() bool {
	return h.settings.Bool("require_auth", requireAuthDefault)
}

// setRequireAuth switches between strict mode (on) and open mode (off) and saves
// the choice. Going strict ends the sessions that were let in only because the host
// was open, at once, and returns how many; paired devices and sessions that were
// verified stay connected, so moving between the modes costs them nothing.
func (h *hostState) SetRequireAuth(on bool) (dropped int) {
	h.policy.SetRequireAuth(on)
	v := "0"
	if on {
		v = "1"
	}
	if err := h.settings.Set("require_auth", v); err != nil {
		log.Printf("could not save setting: %v", err)
	}
	if on {
		dropped = h.sessions.DropUnverified(remote.CloseUnverified, "the host now requires verification")
	}
	log.Printf("mode: %s (ended %d unverified session(s))", map[bool]string{true: "strict", false: "open"}[on], dropped)
	h.changed()
	return dropped
}

func (h *hostState) Password() string {
	pw, _ := h.settings.Get("password")
	return pw
}

// SetPassword changes the optional password and saves it. Empty disables it.
func (h *hostState) SetPassword(pw string) {
	h.policy.SetPassword(pw)
	if err := h.settings.Set("password", pw); err != nil {
		log.Printf("could not save password: %v", err)
	}
}

// check is the remote.Options.Check callback.
func (h *hostState) check(fp, addr, password string) (trust.Verdict, error) {
	v, err := h.policy.Check(fp, addr, password)
	h.mu.Lock()
	if v.Admitted() {
		h.Rejected, h.RejectedBy = "", app.ReasonNone
		if h.seen == nil {
			h.seen = map[string]seenPeer{}
		}
		h.seen[addr] = seenPeer{fp: fp, verified: v != trust.AllowedOpen}
	} else {
		h.Rejected = addr
		h.RejectedBy = map[trust.Verdict]app.Reason{
			trust.Untrusted: app.ReasonNotPaired, trust.BadPassword: app.ReasonWrongPassword, trust.Locked: app.ReasonLockedOut,
		}[v]
	}
	h.mu.Unlock()
	if !v.Admitted() {
		log.Printf("refused %s: %s", addr, map[trust.Verdict]string{
			trust.Untrusted: "not paired", trust.BadPassword: "wrong password", trust.Locked: "locked out",
		}[v])
		h.changed()
	}
	return v, err
}

// tooltip is the tray icon's text, in the language of the interface.
func (h *hostState) tooltip(tr *i18n.Translator) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.Err != "":
		return tr.T(i18n.KeyTrayTipUnavailable)
	case h.Controller != "":
		return tr.T(i18n.KeyTrayTipControlled, "addr", h.Controller)
	case h.pairingLeft() > 0:
		return tr.T(i18n.KeyTrayTipPairing)
	case h.Rejected != "":
		return tr.T(i18n.KeyTrayTipRefused, "what", h.Rejected)
	case !h.requireAuth():
		return tr.T(i18n.KeyTrayTipOpenMode)
	}
	return tr.T(i18n.KeyTrayTipReady)
}

// startHost begins sharing the primary screen in the background.
//
// listen restricts it to one local IP (see remote.ParseListen); empty means every
// interface on port. The addresses the home tab shows are the ones that can reach it.
func startHost(listen string, port int, rates remote.Rates, display string, layered bool, id tls.Certificate, trusted, settings *trust.Store) *hostState {
	h := &hostState{
		policy: trust.NewPolicy(trusted), trusted: trusted, settings: settings,
		ID: trust.Fingerprint(id.Certificate[0])[:16],
	}
	h.sessions = remote.NewSessions(h.policy.RequireAuth)
	bind, port, only, err := remote.ParseListen(listen, port)
	if err != nil {
		h.fail(err)
		return h
	}
	h.Port = port
	if only != "" {
		h.Addrs = []string{only}
	} else {
		h.Addrs = localIPs()
	}
	h.policy.SetPassword(h.Password())
	h.policy.SetRequireAuth(h.requireAuth())
	t0 := time.Now()
	if display == "" {
		display, _ = h.settings.Get("display")
	}
	scr, err := capture.Open(display)
	if err != nil {
		h.fail(err)
		return h
	}
	scr.SetLayered(layered && h.settings.Bool("capture_layered", true))
	log.Printf("startup: sharing %s; layered windows %v; screen capture ready in %dms",
		scr.Display(), layered && h.settings.Bool("capture_layered", true), time.Since(t0).Milliseconds())
	t0 = time.Now()
	ln, err := remote.Listen(bind, id)
	if err != nil {
		h.fail(errors.New(explainListenError(err, port)))
		return h
	}
	log.Printf("startup: QUIC listener ready in %dms", time.Since(t0).Milliseconds())
	w, ht := scr.Size()
	log.Printf("hosting %dx%d on UDP %s", w, ht, bind)
	inj := inject.New(w, ht)
	retargetInjector(inj, scr.Display())
	h.rates = rates.Normalized()
	log.Printf("startup: frame rate %s", h.rates)
	go acceptLoop(h, ln, scr, inj)
	return h
}

// retargetInjector points the injector at monitor d. The primary monitor keeps the
// plain mapping; any other needs the virtual-desktop one, with its position in it.
func retargetInjector(inj *inject.Injector, d capture.Display) {
	if d.Primary {
		inj.SetPrimary(int(d.W), int(d.H))
		return
	}
	vx, vy, vw, vh := capture.VirtualScreen()
	inj.SetDisplay(int(d.W), int(d.H), int(d.X), int(d.Y), vx, vy, vw, vh)
}

func acceptLoop(h *hostState, ln *quic.Listener, scr *capture.Screen, inj *inject.Injector) {
	var (
		mu              sync.Mutex
		active          *quic.Conn
		dd              = proto.NewDedup()
		inputController = remote.NewInputController(inj)
		gate            = remote.NewGate(16, 4) // connections waiting to be authenticated: in all, and per address
	)
	h.mu.Lock()
	h.kick = func(code quic.ApplicationErrorCode, reason string) {
		mu.Lock()
		defer mu.Unlock()
		if active != nil {
			active.CloseWithError(code, reason)
		}
	}
	h.mu.Unlock()
	// Win+L locks this PC and the viewer cannot unlock it, so it is refused unless
	// the setting says otherwise.
	inputController.SetAllowWinL(h.settings.Bool("allow_win_l", false))
	for {
		conn, err := ln.Accept(context.Background())
		if err != nil {
			h.fail(err)
			return
		}
		log.Printf("handshake complete with %s", conn.RemoteAddr())
		// The frame-rate policy is read when a session begins, so a change in the settings
		// applies to the next connection.
		rates := h.currentRates()
		boost := rates.Boost()
		go func() {
			err := remote.Serve(context.Background(), conn, scr, inj, dd, remote.Options{
				FPS:             rates.Base,
				FPSFn:           boost.Rate,
				OnInput:         boost.Touch,
				IdleFPS:         rates.Idle,
				Check:           h.check,
				InputController: inputController,
				Gate:            gate,
				Sessions:        h.sessions,
				HostName:        remote.ComputerName(),
				OnSourceChanged: func(w, h int) {
					d := scr.Display()
					log.Printf("shared screen is now %s", d)
					retargetInjector(inj, d)
				},
				OnPeer: func(p remote.PeerInfo) {
					log.Printf("viewer %s speaks control protocol v%d (name %q)", conn.RemoteAddr(), p.Version, p.Name)
				},
				OnError: func(err error) { log.Printf("session cleanup: %v", err) },
				OnAuthed: func() {
					mu.Lock()
					defer mu.Unlock()
					if active != nil {
						active.CloseWithError(remote.CloseReplaced, "replaced by a new client")
					}
					active = conn
					addr := conn.RemoteAddr().String()
					h.mu.Lock()
					h.Controller = addr
					peer := h.seen[addr]
					h.CtrlFP, h.CtrlOpen = peer.fp, !peer.verified
					h.Rejected, h.RejectedBy = "", app.ReasonNone
					delete(h.seen, addr)
					h.mu.Unlock()
					log.Printf("client connected: %s", addr)
					h.changed()
				},
			})
			if err == remote.ErrDenied {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if active == conn {
				active = nil
				h.mu.Lock()
				h.Controller, h.CtrlFP, h.CtrlOpen = "", "", false
				h.mu.Unlock()
			}
			log.Printf("client %s disconnected: %v", conn.RemoteAddr(), err)
			h.changed()
		}()
	}
}

// currentRates is the frame-rate policy for a session that is starting: the one the program
// was started with, or a fixed rate if the person chose that in the settings.
func (h *hostState) currentRates() remote.Rates {
	if app.FixedFrameRates(h.settings) {
		return remote.Rates{Base: h.rates.Base}.Normalized()
	}
	return h.rates
}

func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}
