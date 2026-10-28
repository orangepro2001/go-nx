//go:build windows

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"go-nx/internal/capture"
	"go-nx/internal/inject"
	"go-nx/internal/proto"
	"go-nx/internal/remote"
	"go-nx/internal/trust"
	"go-nx/internal/ui"
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
}

func (h *hostState) fail(err error) {
	log.Printf("host disabled: %v", err)
	h.mu.Lock()
	h.Err = err.Error()
	h.mu.Unlock()
}

func (h *hostState) startPairing() {
	h.policy.StartPairing(pairingWindow)
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
		Controller: h.Controller, Rejected: h.Rejected, PairingLeft: h.policy.PairingLeft(),
		RequireAuth: h.requireAuth(),
	}
}

func (h *hostState) pairingLeft() time.Duration { return h.policy.PairingLeft() }
func (h *hostState) trustedCount() int          { return h.policy.TrustedCount() }

// requireAuth reports whether pairing/password checks are on. Default: off.
func (h *hostState) requireAuth() bool {
	v, _ := h.settings.Get("require_auth")
	return v == "1"
}

func (h *hostState) setRequireAuth(on bool) {
	h.policy.SetRequireAuth(on)
	v := "0"
	if on {
		v = "1"
	}
	if err := h.settings.Set("require_auth", v); err != nil {
		log.Printf("could not save setting: %v", err)
	}
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
	if v == trust.Allowed {
		h.Rejected = ""
	} else {
		h.Rejected = addr + " (" + reason + ")"
	}
	h.mu.Unlock()
	if v != trust.Allowed {
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
func startHost(port, fps int, id tls.Certificate, trusted, settings *trust.Store) *hostState {
	h := &hostState{
		Port: port, Addrs: localIPs(), policy: trust.NewPolicy(trusted), settings: settings,
		ID: trust.Fingerprint(id.Certificate[0])[:16],
	}
	h.policy.SetPassword(h.password())
	h.policy.SetRequireAuth(h.requireAuth())
	t0 := time.Now()
	scr, err := capture.NewScreen()
	if err != nil {
		h.fail(err)
		return h
	}
	log.Printf("startup: screen capture ready in %dms", time.Since(t0).Milliseconds())
	t0 = time.Now()
	ln, err := remote.Listen(fmt.Sprintf(":%d", port), id)
	if err != nil {
		h.fail(errors.New(explainListenError(err, port)))
		return h
	}
	log.Printf("startup: QUIC listener ready in %dms", time.Since(t0).Milliseconds())
	w, ht := scr.Size()
	log.Printf("hosting %dx%d on UDP %d", w, ht, port)
	go acceptLoop(h, ln, scr, inject.New(w, ht), fps)
	return h
}

func acceptLoop(h *hostState, ln *quic.Listener, scr *capture.Screen, inj *inject.Injector, fps int) {
	var (
		mu              sync.Mutex
		active          *quic.Conn
		dd              = proto.NewDedup()
		inputController = remote.NewInputController(inj)
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
				OnError:         func(err error) { log.Printf("session cleanup: %v", err) },
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
