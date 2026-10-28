//go:build windows

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/capture"
	"github.com/orangepro2001/go-nx/internal/inject"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/trust"
	"github.com/orangepro2001/go-nx/internal/ui"
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
	Rejected   string // last device turned away, with the reason
	policy     *trust.Policy
	settings   *trust.Store
	kick       func(code quic.ApplicationErrorCode, reason string) // closes the current controller; set by acceptLoop
	sessions   *remote.Sessions                                    // which live sessions were let in without verification
}

func (h *hostState) fail(err error) {
	log.Printf("host disabled: %v", err)
	h.mu.Lock()
	h.Err = err.Error()
	h.mu.Unlock()
}

// startPairing opens a pairing window with a fresh six-digit code, which the
// home tab shows and the new device enters as its password.
func (h *hostState) startPairing() {
	h.policy.OpenPairing(trust.NewPairingCode(), pairingWindow)
	h.mu.Lock()
	h.Rejected = ""
	h.mu.Unlock()
	log.Print("pairing opened")
}

// forgetAll removes every paired device and drops the controller that is
// connected now, so a revoked device does not keep a session it already has. It
// returns how many devices were forgotten.
func (h *hostState) forgetAll() (int, error) {
	n, err := h.policy.RevokeAll()
	if h.kick != nil {
		h.kick(remote.CloseRevoked, "removed from the paired devices")
	}
	log.Printf("forgot %d paired device(s)", n)
	return n, err
}

// info is a snapshot for the home tab.
func (h *hostState) info() ui.HostInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return ui.HostInfo{
		Err: h.Err, Addrs: h.Addrs, Port: h.Port, Paired: h.policy.TrustedCount(), ID: h.ID,
		Controller: h.Controller, Rejected: h.Rejected, PairingLeft: h.policy.PairingLeft(), PairingCode: h.policy.PairingCode(),
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
func (h *hostState) setRequireAuth(on bool) (dropped int) {
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
	return dropped
}

func (h *hostState) password() string {
	pw, _ := h.settings.Get("password")
	return pw
}

// setPassword changes the optional password and saves it. Empty disables it.
func (h *hostState) setPassword(pw string) {
	h.policy.SetPassword(pw)
	if err := h.settings.Set("password", pw); err != nil {
		log.Printf("could not save password: %v", err)
	}
}

// check is the remote.Options.Check callback.
func (h *hostState) check(fp, addr, password string) (trust.Verdict, error) {
	v, err := h.policy.Check(fp, addr, password)
	reason := map[trust.Verdict]string{
		trust.Untrusted: "not paired", trust.BadPassword: "wrong password", trust.Locked: "locked out",
	}[v]
	h.mu.Lock()
	if v.Admitted() {
		h.Rejected = ""
	} else {
		h.Rejected = addr + " (" + reason + ")"
	}
	h.mu.Unlock()
	if !v.Admitted() {
		log.Printf("refused %s: %s", addr, reason)
	}
	return v, err
}

func (h *hostState) tooltip() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.Err != "":
		return "go-nx: hosting unavailable"
	case h.Controller != "":
		return "go-nx: controlled by " + h.Controller
	case h.pairingLeft() > 0:
		return "go-nx: pairing open"
	case h.Rejected != "":
		return "go-nx: refused " + h.Rejected
	}
	return "go-nx: ready"
}

// startHost begins sharing the primary screen in the background.
//
// listen restricts it to one local IP (see remote.ParseListen); empty means every
// interface on port. The addresses the home tab shows are the ones that can reach it.
func startHost(listen string, port, fps int, display string, layered bool, id tls.Certificate, trusted, settings *trust.Store) *hostState {
	h := &hostState{
		policy: trust.NewPolicy(trusted), settings: settings,
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
	h.policy.SetPassword(h.password())
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
	go acceptLoop(h, ln, scr, inj, fps)
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

func acceptLoop(h *hostState, ln *quic.Listener, scr *capture.Screen, inj *inject.Injector, fps int) {
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
		go func() {
			err := remote.Serve(context.Background(), conn, scr, inj, dd, remote.Options{
				FPS:             fps,
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
					h.mu.Lock()
					h.Controller = conn.RemoteAddr().String()
					h.Rejected = ""
					h.mu.Unlock()
					log.Printf("client connected: %s", conn.RemoteAddr())
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
				h.Controller = ""
				h.mu.Unlock()
			}
			log.Printf("client %s disconnected: %v", conn.RemoteAddr(), err)
		}()
	}
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
