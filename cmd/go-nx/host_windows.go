//go:build windows

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/app"
	"github.com/orangepro2001/go-nx/internal/capture"
	"github.com/orangepro2001/go-nx/internal/inject"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/trust"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/vdisplay"
)

// pairingWindow is long enough to walk to the other PC, confirm the host's ID there and type
// the code. Guessing is limited by the number of wrong codes (trust.maxPairFails), not by time.
const pairingWindow = 5 * time.Minute

// hostState is the hosting side: who may connect, and what the UI shows about it.
// The decisions themselves are made by trust.Policy.
type hostState struct {
	mu         sync.Mutex
	Port       int
	Addrs      []string // the addresses that reach it
	ListenIP   string   // the one IP hosting is restricted to; "" for every address
	ID         string   // this PC's short fingerprint, for display
	Err        string   // non-empty if hosting could not start
	Controller string   // remote address currently controlling this PC
	CtrlFP     string   // its certificate fingerprint, so revoking that device can end its session
	CtrlOpen   bool     // it was let in without verification (open mode)
	Rejected   string   // address of the last device turned away
	RejectedBy app.Reason
	Sharers    []string // other programs bound to the hosting port, which take the connections to their addresses
	policy     *trust.Policy
	trusted    *trust.Store
	settings   *trust.Store
	seen       map[string]seenPeer    // by remote address: who is connecting and how they were judged
	notifyFn   atomic.Pointer[func()] // something the interface shows has changed
	rates      remote.Rates
	kick       func(code quic.ApplicationErrorCode, reason string) // closes the current controller; set by newAcceptor
	sessions   *remote.Sessions                                    // which live sessions were let in without verification
	input      *remote.InputController                             // set by newAcceptor

	id      tls.Certificate
	fixed   bool                       // -listen or -port was given: the settings do not decide where it listens
	local   []string                   // this PC's addresses, refreshed by watchPort
	ln      *remote.HostListener       // nil while not listening
	serve   func(*remote.HostListener) // accepts on a listener; nil until the screen is open
	bindGen int                        // counts moves, so a wait for an address gives up when the host moves again

	vsys       *vdisplay.Windows // the Parsec driver and the displays
	vd         *vdisplay.Manager // puts a session on a virtual display; nil until the screen is open
	vddOK      bool              // the driver is installed (watchPort keeps it up to date)
	vddVersion string            // its version
	installing atomic.Bool       // the driver's installer is running
}

// exitHost is the host, for quitApp: the displays are put back before go-nx exits.
var exitHost *hostState

// stopVirtual puts the host's own screen back and lets go of the driver.
func (h *hostState) stopVirtual() {
	if h.vd != nil {
		h.vd.Release(nil)
	}
	if h.vsys != nil {
		h.vsys.Close()
	}
}

// virtualAllowed reports whether a viewer may put its session on a virtual display.
func (h *hostState) virtualAllowed() bool { return h.settings.Bool("virtual_display", false) }

// SetVirtualDisplay lets viewers use a virtual display, or not, from their next session, and
// saves the choice. Turning it off puts the host's own screen back at once.
func (h *hostState) SetVirtualDisplay(on bool) {
	h.save("virtual_display", on)
	if !on && h.vd != nil {
		go func() { h.vd.Release(nil); h.changed() }()
	}
	log.Printf("virtual display for viewers: %v", on)
	h.changed()
}

// InstallDriver runs the Parsec driver's installer, which Windows asks the person at this PC
// to allow, and waits for it.
func (h *hostState) InstallDriver() error {
	if !h.installing.CompareAndSwap(false, true) {
		return errors.New("the installer is already running")
	}
	defer h.installing.Store(false)
	h.changed()
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "go-nx", "driver")
	err := vdisplay.Install(dir)
	ok, ver := h.vsys.Driver()
	h.mu.Lock()
	h.vddOK, h.vddVersion = ok, ver
	h.mu.Unlock()
	log.Printf("Parsec driver installer: %v; installed now: %v %s", err, ok, ver)
	h.changed()
	if err == nil && !ok {
		err = errors.New("the installer finished but the driver is not there")
	}
	return err
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
// fingerprint, the address and the time it paired.
func (h *hostState) Paired() []app.PairedDevice {
	var out []app.PairedDevice
	for _, fp := range h.trusted.Keys() {
		v, _ := h.trusted.Get(fp)
		out = append(out, app.ParsePaired(fp, v, time.Local))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out
}

// Info is a snapshot for the interface.
func (h *hostState) Info() app.HostInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return app.HostInfo{
		Err: h.Err, Addrs: h.Addrs, Port: h.Port, ListenIP: h.ListenIP, LocalIPs: h.local, ListenFixed: h.fixed,
		AllowWinL: h.settings.Bool("allow_win_l", false), Paired: h.policy.TrustedCount(), ID: h.ID,
		VirtualAllowed: h.virtualAllowed(), DriverInstalled: h.vddOK, DriverVersion: h.vddVersion,
		DriverInstalling: h.installing.Load(), VirtualActive: h.virtualActive(),
		Controller: h.Controller, ControllerVerified: h.Controller != "" && !h.CtrlOpen,
		Rejected: h.Rejected, RejectedWhy: h.RejectedBy,
		PairingLeft: h.policy.PairingLeft(), PairingCode: h.policy.PairingCode(),
		RequireAuth: h.requireAuth(), Lockout: h.policy.Lockout(), PortShared: h.Sharers,
	}
}

// virtualActive describes the virtual display a session is on ("\\.\DISPLAY9 2560x1440@60"), or "".
func (h *hostState) virtualActive() string {
	if h.vd == nil {
		return ""
	}
	if dev, m, ok := h.vd.Active(); ok {
		return dev + " " + m.String()
	}
	return ""
}

// portCheckEvery is how often the host looks for another program bound to its port.
const portCheckEvery = 10 * time.Second

// watchPort keeps Sharers up to date, and this PC's addresses. Another host started with one
// of this PC's addresses (a second go-nx, an nx-host) gets every connection sent to that
// address, and nothing here (pairing, open mode, the password) applies to them: the person
// has to be told.
func (h *hostState) watchPort() {
	for {
		if h.vsys != nil && !h.installing.Load() {
			ok, ver := h.vsys.Driver()
			h.mu.Lock()
			driverMoved := ok != h.vddOK || ver != h.vddVersion
			h.vddOK, h.vddVersion = ok, ver
			h.mu.Unlock()
			if driverMoved {
				h.changed()
			}
		}
		local := localIPs()
		h.mu.Lock()
		port, on := h.Port, h.ln != nil
		moved := !slices.Equal(local, h.local)
		h.local = local
		if h.ListenIP == "" {
			h.Addrs = local
		}
		h.mu.Unlock()
		var s []string
		if on {
			s = portSharers(port)
		}
		h.mu.Lock()
		same := slices.Equal(s, h.Sharers)
		h.Sharers = s
		h.mu.Unlock()
		if moved {
			h.changed()
		}
		if !same {
			if len(s) > 0 {
				log.Printf("UDP port %d is also bound by %s: connections to those addresses go there, not here", port, strings.Join(s, ", "))
			} else {
				log.Printf("UDP port %d is no longer bound by another program", port)
			}
			h.changed()
		}
		time.Sleep(portCheckEvery)
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

// SetAllowWinL lets the viewer lock this PC with Win+L, or not, from its next key press,
// and saves the choice.
func (h *hostState) SetAllowWinL(on bool) {
	h.save("allow_win_l", on)
	h.mu.Lock()
	in := h.input
	h.mu.Unlock()
	if in != nil {
		in.SetAllowWinL(on)
	}
	log.Printf("Win+L from the viewer: %v", on)
	h.changed()
}

// save stores a yes/no setting.
func (h *hostState) save(key string, on bool) {
	v := "0"
	if on {
		v = "1"
	}
	if err := h.settings.Set(key, v); err != nil {
		log.Printf("could not save setting: %v", err)
	}
}

// SetLockout turns the lockout after wrong passwords on or off and saves the choice.
// Turning it off also lets in at once a device that is locked out now.
func (h *hostState) SetLockout(on bool) {
	h.policy.SetLockout(on)
	h.save("lockout", on)
	h.mu.Lock()
	if h.RejectedBy == app.ReasonLockedOut {
		h.Rejected, h.RejectedBy = "", app.ReasonNone
	}
	h.mu.Unlock()
	log.Printf("lockout after wrong passwords: %v", on)
	h.changed()
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
		if len(h.seen) >= 64 { // connections that were let in and never became the controller
			clear(h.seen)
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
		log.Printf("refused %s: %s (%s)", addr, map[trust.Verdict]string{
			trust.Untrusted: "not paired", trust.BadPassword: "wrong password", trust.Locked: "locked out",
		}[v], h.policy.Explain(password))
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
// interface on port. Unless fixed (-listen or -port was given), the place saved in the
// settings is used instead. The addresses the home tab shows are the ones that can reach it.
func startHost(listen string, port int, fixed bool, rates remote.Rates, display string, layered bool, id tls.Certificate, trusted, settings *trust.Store) *hostState {
	h := &hostState{
		policy: trust.NewPolicy(trusted), trusted: trusted, settings: settings,
		ID: trust.Fingerprint(id.Certificate[0])[:16], id: id, fixed: fixed, local: localIPs(),
	}
	h.sessions = remote.NewSessions(h.policy.RequireAuth)
	if !fixed {
		listen, port = h.savedListen()
	}
	_, port, only, err := remote.ParseListen(listen, port)
	if err != nil {
		h.fail(err)
		return h
	}
	h.Port, h.ListenIP, h.Addrs = port, only, h.reachable(only)
	h.policy.SetPassword(h.Password())
	h.policy.SetRequireAuth(h.requireAuth())
	h.policy.SetLockout(h.settings.Bool("lockout", true))
	// A go-nx that ended while a session was on a virtual display left the layout to go back
	// to on disk: put it back before anything looks at the monitors.
	h.vsys = vdisplay.NewWindows()
	layout := vdisplay.FileStore(filepath.Join(configDir(), "display-layout.json"))
	if err := vdisplay.NewManager(h.vsys, nil, layout, log.Printf).Recover(); err != nil {
		log.Printf("startup: %v", err)
	}
	h.vddOK, h.vddVersion = h.vsys.Driver()
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
	w, ht := scr.Size()
	inj := inject.New(w, ht)
	retargetInjector(inj, scr.Display())
	h.rates = rates.Normalized()
	log.Printf("startup: frame rate %s", h.rates)
	h.vd = vdisplay.NewManager(h.vsys, scr, layout, log.Printf)
	h.serve = newAcceptor(h, scr, inj)
	t0 = time.Now()
	if h.bind(listen, port) == nil {
		log.Printf("startup: QUIC listener ready in %dms; hosting %dx%d", time.Since(t0).Milliseconds(), w, ht)
	}
	go h.watchPort()
	return h
}

// reachable is the addresses that reach a host restricted to only ("" for none).
func (h *hostState) reachable(only string) []string {
	if only != "" {
		return []string{only}
	}
	return h.local
}

// savedListen is where the settings say to listen: an IP or "" for every address, and the port.
func (h *hostState) savedListen() (listen string, port int) {
	listen, _ = h.settings.Get("listen_ip")
	port = remote.DefaultPort
	if v, ok := h.settings.Get("port"); ok {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}
	return listen, port
}

// bind starts listening on listen (an IP, or "" for every address) and port. An IP that is
// not on this PC yet (Tailscale still starting when go-nx starts with Windows) is waited
// for in the background, and the error says so.
func (h *hostState) bind(listen string, port int) error {
	addr, port, only, err := remote.ParseListen(listen, port)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.bindGen++
	gen := h.bindGen
	h.Port, h.ListenIP, h.Addrs = port, only, h.reachable(only)
	h.mu.Unlock()
	ln, err := remote.ListenHost(addr, h.id)
	if err != nil {
		why := explainListenError(err, port)
		if only != "" && errors.Is(err, syscall.Errno(10049)) { // WSAEADDRNOTAVAIL
			why = fmt.Sprintf("%s is not an address of this PC (yet); hosting starts when it is", only)
			go h.bindWhenThere(gen, addr)
		}
		h.fail(errors.New(why))
		h.changed()
		return errors.New(why)
	}
	h.listening(gen, ln, addr)
	return nil
}

// bindWhenThere tries addr again every few seconds until it can be bound or the host has
// moved on.
func (h *hostState) bindWhenThere(gen int, addr string) {
	for {
		time.Sleep(5 * time.Second)
		h.mu.Lock()
		current := h.bindGen == gen
		h.mu.Unlock()
		if !current {
			return
		}
		if ln, err := remote.ListenHost(addr, h.id); err == nil {
			h.listening(gen, ln, addr)
			return
		}
	}
}

// listening makes ln the host's listener, unless the host has moved since gen.
func (h *hostState) listening(gen int, ln *remote.HostListener, addr string) {
	h.mu.Lock()
	if h.bindGen != gen {
		h.mu.Unlock()
		ln.Shutdown()
		return
	}
	h.ln, h.Err = ln, ""
	h.mu.Unlock()
	log.Printf("hosting on UDP %s", addr)
	go h.serve(ln)
	h.changed()
}

// unbind stops listening. The session in progress ends, and so does a wait for an address.
func (h *hostState) unbind() {
	h.mu.Lock()
	old := h.ln
	h.ln = nil
	h.bindGen++
	h.mu.Unlock()
	if old == nil {
		return
	}
	if h.kick != nil {
		h.kick(remote.CloseKicked, "the host moved to another address")
	}
	old.Shutdown()
}

// SetListen moves the host to listen (an IP, or "" for every address) and port, at once, and
// saves the choice. The session in progress ends. If the new place cannot be bound, the host
// goes back to where it was, nothing is saved, and the error says why.
func (h *hostState) SetListen(listen string, port int) error {
	if h.fixed {
		return errors.New("where to listen was given on the command line (-listen / -port)")
	}
	if _, _, _, err := remote.ParseListen(listen, port); err != nil {
		return err
	}
	if h.serve == nil { // no screen to share, so nothing listens: the next start uses it
		return h.saveListen(listen, port)
	}
	h.mu.Lock()
	oldIP, oldPort, was := h.ListenIP, h.Port, h.ln != nil
	h.mu.Unlock()
	h.unbind()
	if err := h.bind(listen, port); err != nil {
		if was {
			h.bind(oldIP, oldPort)
		}
		return err
	}
	log.Printf("moved to listen on %q, UDP port %d", listen, port)
	return h.saveListen(listen, port)
}

func (h *hostState) saveListen(listen string, port int) error {
	if err := h.settings.Set("listen_ip", listen); err != nil {
		return err
	}
	return h.settings.Set("port", strconv.Itoa(port))
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

// newAcceptor sets up what every listener of the host shares (the controller, the input,
// the gate) and returns the loop that accepts on one listener. The loop of a listener the
// host moved away from ends quietly.
func newAcceptor(h *hostState, scr *capture.Screen, inj *inject.Injector) func(*remote.HostListener) {
	var (
		mu              sync.Mutex
		active          *quic.Conn
		dd              = proto.NewDedup()
		inputController = remote.NewInputController(inj)
		gate            = remote.NewGate(16, 4) // connections waiting to be authenticated: in all, and per address
	)
	// Win+L locks this PC and the viewer cannot unlock it, so it is refused unless
	// the setting says otherwise.
	inputController.SetAllowWinL(h.settings.Bool("allow_win_l", false))
	h.mu.Lock()
	h.input = inputController
	h.kick = func(code quic.ApplicationErrorCode, reason string) {
		mu.Lock()
		defer mu.Unlock()
		if active != nil {
			active.CloseWithError(code, reason)
		}
	}
	h.mu.Unlock()
	return func(ln *remote.HostListener) {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				h.mu.Lock()
				current := h.ln == ln
				h.mu.Unlock()
				if current {
					h.fail(err)
				}
				return
			}
			log.Printf("handshake complete with %s", conn.RemoteAddr())
			// The frame-rate policy is read when a session begins, so a change in the settings
			// applies to the next connection.
			rates := h.currentRates()
			boost := rates.Boost()
			// A viewer may put its session on a virtual display if the host allows it now;
			// the display is the session's, and goes when the session ends.
			var onViewSize func(w, hh int, report func(proto.DisplayState))
			if h.vd != nil && h.virtualAllowed() {
				onViewSize = func(w, hh int, report func(proto.DisplayState)) {
					h.vd.Request(conn, w, hh, func(st proto.DisplayState) { report(st); h.changed() })
				}
			}
			go func() {
				if h.vd != nil {
					defer func() { h.vd.Release(conn); h.changed() }()
				}
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
					OnError:    func(err error) { log.Printf("session cleanup: %v", err) },
					OnViewSize: onViewSize,
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
