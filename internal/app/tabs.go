package app

import (
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

type tabState int

const (
	tabConnecting tabState = iota
	tabLive
	tabError
	// tabReconnecting is a session that was live and lost its connection to a network
	// problem: its last picture stays, dimmed, while the connection is brought back.
	tabReconnecting
)

// tab is one connection to another PC. It is created as soon as a connection is started,
// so several can be connecting or live at once.
type tab struct {
	serial      int
	title       string
	addr        string // normalized host:port
	password    string
	profile     string // name of the saved device this tab came from, or ""
	state       tabState
	err         string // why the tab is in the error state, in the language of the interface
	hostChanged bool   // the error is a changed host identity, which can be forgotten
	askPw       bool   // the host refused the password or code sent: the error card asks for another
	sess        *session
	dial        chan connResult

	confirmReq chan *hostConfirm // the dial goroutine asks the person through this
	confirm    *hostConfirm      // the question being shown, or nil
	cancel     chan struct{}     // closed when the tab is closed while connecting

	lastClick time.Time // for the double click that renames

	// The session view.
	barShown bool
	barUntil time.Time
	textOpen bool   // the box for sending text is open
	text     string // what is in it
	stats    bool   // the statistics are showing

	// A connection being brought back.
	old      *session // the session that was lost, kept for its last picture
	attempts atomic.Int32
	stopRe   chan struct{}
}

// hostConfirm is a first-time host waiting for the person to compare its fingerprint with
// the one the host displays. The answer goes to reply.
type hostConfirm struct {
	fp    string
	reply chan bool
}

type connResult struct {
	conn Conn
	err  error
}

// normalizeAddr adds the default port when none is given.
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, "7000")
	}
	return addr
}

// dialWaits are the handshake time limits of successive attempts. QUIC resends its first
// packet with growing gaps (0.2, 0.6, 1.4, 3, 6 s...), so on a path that takes several
// seconds to come up (a Tailscale route that is not yet direct) one long attempt can miss
// the moment the path opens. Several short attempts, each sending its first packet at
// once, catch it.
var dialWaits = []time.Duration{2 * time.Second, 3 * time.Second, 4 * time.Second, 6 * time.Second}

// hostConfirmWait is how long the prompt waits for an answer before it counts as no.
const hostConfirmWait = 2 * time.Minute

// tabAt returns the session tab at index i of the tab strip (0 is the pages).
func (a *App) tabAt(i int) *tab {
	if i < 1 || i > len(a.tabs) {
		return nil
	}
	return a.tabs[i-1]
}

// openTab starts connecting in a new tab. The host accepts one controller at a time, so a
// second connection from here would kick the first one off: if this address already has a
// tab, switch to it (and retry it if it had failed).
func (a *App) openTab(title, addr, password, profile string) {
	addr = normalizeAddr(addr)
	for i, t := range a.tabs {
		if t.addr == addr {
			if t.state == tabError { // retry in the same tab
				t.password = password
				a.startDial(t)
			}
			a.setActive(i + 1)
			return
		}
	}
	if title == "" {
		title = addr
	}
	a.serial++
	t := &tab{serial: a.serial, title: title, addr: addr, password: password, profile: profile}
	a.tabs = append(a.tabs, t)
	a.startDial(t)
	a.setActive(len(a.tabs))
	if profile != "" {
		if err := a.cfg.Profiles.Touch(profile, a.now); err != nil {
			log.Printf("save connections: %v", err)
		}
	}
}

func (a *App) startDial(t *tab) {
	t.state, t.err, t.hostChanged, t.askPw = tabConnecting, "", false, false
	t.dial = make(chan connResult, 1)
	t.confirmReq, t.confirm, t.cancel = make(chan *hostConfirm, 1), nil, make(chan struct{})
	go a.dialTab(t.addr, t.password, t.dial, t.confirmReq, t.cancel)
	a.Invalidate()
}

// dialTab connects to addr. A host seen for the first time is identified and confirmed by
// the person before anything is sent to it: the password or pairing code only ever goes to
// a pinned identity.
func (a *App) dialTab(addr, password string, out chan<- connResult, ask chan<- *hostConfirm, cancel <-chan struct{}) {
	defer a.Invalidate()
	conn := a.cfg.Connector
	if _, pinned := conn.Pinned(addr); !pinned {
		fp, err := a.probe(addr)
		if err != nil {
			out <- connResult{nil, err}
			return
		}
		if !a.askHostConfirm(ask, cancel, fp) {
			out <- connResult{nil, errHostNotConfirmed}
			return
		}
		if err := conn.Pin(addr, fp); err != nil {
			out <- connResult{nil, err}
			return
		}
		log.Printf("pinned %s after the user confirmed %s", addr, shortID(fp))
	}
	var c Conn
	var err error
	for n, wait := range dialWaits {
		start := time.Now()
		ctx, stop := context.WithTimeout(context.Background(), wait)
		c, err = conn.Dial(ctx, addr, password)
		stop()
		log.Printf("dial %s attempt %d/%d: %v (%dms)", addr, n+1, len(dialWaits), err, time.Since(start).Milliseconds())
		if err == nil || isHostChanged(err) {
			break // connected, or retrying cannot help
		}
	}
	out <- connResult{c, err}
}

// probe learns the host's fingerprint, retrying like a normal dial because the path may
// take a few seconds to come up.
func (a *App) probe(addr string) (string, error) {
	var fp string
	var err error
	for n, wait := range dialWaits {
		ctx, stop := context.WithTimeout(context.Background(), wait)
		fp, err = a.cfg.Connector.Probe(ctx, addr)
		stop()
		log.Printf("probe %s attempt %d/%d: %v", addr, n+1, len(dialWaits), err)
		if err == nil {
			return fp, nil
		}
	}
	return "", err
}

// askHostConfirm puts the question to the interface and waits for the answer. A tab that
// is closed, or an answer that never comes, is a no.
func (a *App) askHostConfirm(ask chan<- *hostConfirm, cancel <-chan struct{}, fp string) bool {
	req := &hostConfirm{fp: fp, reply: make(chan bool, 1)}
	select {
	case ask <- req:
		a.Invalidate()
	case <-cancel:
		return false
	}
	select {
	case ok := <-req.reply:
		return ok
	case <-cancel:
		return false
	case <-time.After(hostConfirmWait):
		return false
	}
}

// shortID is the fingerprint prefix a host's This PC page shows as its ID, which is what
// the person compares.
func shortID(fp string) string { return fp[:min(16, len(fp))] }

// pollTabs moves tabs between connecting, live and error as results arrive.
func (a *App) pollTabs() {
	for i, t := range a.tabs {
		before := t.state
		hadConfirm := t.confirm != nil
		a.pollTab(i, t)
		if t.state != before || (t.confirm != nil) != hadConfirm {
			a.ui.Invalidate() // what the tab shows has changed
		}
	}
}

// pollTab moves one tab along.
func (a *App) pollTab(i int, t *tab) {
	switch t.state {
	case tabConnecting:
		if t.confirm == nil {
			select {
			case req := <-t.confirmReq:
				t.confirm = req
			default:
			}
		}
		select {
		case r := <-t.dial:
			if r.err != nil {
				t.state = tabError
				t.err, t.hostChanged = describe(a.tr, r.err)
				a.askAgain(t, r.err)
				return
			}
			t.sess = newSession(r.conn, &a.nextOp, a.Invalidate)
			t.state = tabLive
			t.sess.setVisible(a.active == i+1)
		default:
		}
	case tabLive:
		select {
		case <-t.sess.done:
			t.sess.mu.Lock()
			err := t.sess.err
			t.sess.mu.Unlock()
			t.sess.setVisible(false)
			liveSessions.Delete(t.sess)
			log.Printf("session %s ended: %v", t.addr, err)
			reason, _ := describe(a.tr, err)
			t.err = a.tr.T(keyErrDisconnected, "reason", reason)
			if a.autoReconnect() && reconnectable(err) && t.sess.hasPicture() {
				a.startReconnect(t)
			} else {
				t.state, t.sess = tabError, nil
				a.askAgain(t, err)
			}
		default:
		}
	case tabReconnecting:
		select {
		case r := <-t.dial:
			switch {
			case r.err == nil:
				ns := newSession(r.conn, &a.nextOp, a.Invalidate)
				ns.mode, ns.sized = t.old.mode, true // the window keeps its size, the picture its mode
				t.sess, t.old, t.state = ns, nil, tabLive
				ns.setVisible(a.active == i+1)
				log.Printf("session %s is back", t.addr)
			case errors.Is(r.err, errStopped):
				t.state, t.sess, t.old = tabError, nil, nil
			case errors.Is(r.err, errGaveUp):
				t.err = a.tr.T(i18n.KeyReconnectGaveUp, "n", maxReconnects) + " " + t.err
				t.state, t.sess, t.old = tabError, nil, nil
			default:
				reason, changed := describe(a.tr, r.err)
				t.err, t.hostChanged = reason, changed
				t.state, t.sess, t.old = tabError, nil, nil
				a.askAgain(t, r.err)
			}
		default:
		}
	}
}

// askAgain makes a failed tab ask for a new password when the host refused the one it sent:
// Retry alone would only send the same one again. A password the host called wrong is
// cleared, so the next one is typed afresh instead of being added to it.
func (a *App) askAgain(t *tab, err error) {
	t.askPw = refusedAuth(err)
	if t.askPw && refusedCode(err) == remote.CloseBadPassword {
		t.password = ""
	}
}

// errStopped and errGaveUp are why a reconnection ended without a connection.
var (
	errStopped = errors.New("reconnecting was stopped")
	errGaveUp  = errors.New("could not reconnect")
)

// maxReconnects is how many times a lost connection is tried again before giving up; with
// the waits below that is about a minute and a half.
const maxReconnects = 15

var defaultReconnectWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// reconnectable reports whether a session ended in a way that is worth trying again: the
// network let go of it (no word from the host for too long, a reset), or the host closed it
// normally (it was restarted, say). A refusal, a removed device, a changed identity or a
// host that ended the session on purpose are not: trying again cannot help or is unwelcome.
func reconnectable(err error) bool {
	var idle *quic.IdleTimeoutError
	var reset *quic.StatelessResetError
	var ae *quic.ApplicationError
	var op *net.OpError
	switch {
	case errors.As(err, &ae): // the host said something: only an ordinary close is worth another go
		return ae.ErrorCode == 0
	case errors.As(err, &idle), errors.As(err, &reset), errors.As(err, &op):
		return true
	}
	return false
}

// fatalDial reports whether a failed attempt to bring a connection back says it is no use
// trying: the host answered, and refused.
func fatalDial(err error) bool {
	var ae *quic.ApplicationError
	if errors.As(err, &ae) {
		return ae.ErrorCode != 0
	}
	return isHostChanged(err)
}

// startReconnect keeps the picture of a lost session, dimmed, and tries to bring the
// connection back in the background.
func (a *App) startReconnect(t *tab) {
	t.old, t.sess, t.state = t.sess, nil, tabReconnecting
	t.attempts.Store(0)
	t.stopRe = make(chan struct{})
	t.dial = make(chan connResult, 1)
	go a.reconnectLoop(t.addr, t.password, a.reconnectWaits, t.dial, t.stopRe, &t.attempts)
	a.Invalidate()
}

func (a *App) reconnectLoop(addr, password string, waits []time.Duration, out chan<- connResult, stop <-chan struct{}, attempts *atomic.Int32) {
	defer a.Invalidate()
	for n := 1; n <= maxReconnects; n++ {
		wait := waits[min(n-1, len(waits)-1)]
		select {
		case <-stop:
			out <- connResult{nil, errStopped}
			return
		case <-time.After(wait):
		}
		attempts.Store(int32(n))
		a.Invalidate()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		c, err := a.cfg.Connector.Dial(ctx, addr, password)
		cancel()
		log.Printf("reconnect %s attempt %d/%d: %v", addr, n, maxReconnects, err)
		if err == nil {
			select {
			case <-stop:
				c.Close()
				out <- connResult{nil, errStopped}
			default:
				out <- connResult{c, nil}
			}
			return
		}
		if fatalDial(err) {
			out <- connResult{nil, err}
			return
		}
	}
	out <- connResult{nil, errGaveUp}
}

// stopReconnect gives up bringing a connection back.
func (a *App) stopReconnect(t *tab) {
	if t.stopRe != nil {
		close(t.stopRe)
		t.stopRe = nil
	}
	a.ui.Invalidate()
}

// setActive shows tab i (0 is the pages). Leaving a session releases its held input.
func (a *App) setActive(i int) {
	a.active = i
	for j, t := range a.tabs {
		if t.sess != nil {
			t.sess.setVisible(j+1 == i)
		}
	}
	a.Invalidate()
}

// cycleTab moves to the next (delta 1) or previous (-1) tab, wrapping round.
func (a *App) cycleTab(delta int) {
	n := len(a.tabs) + 1
	a.setActive((a.active + delta + n) % n)
}

// closeTab disconnects and removes tab i.
func (a *App) closeTab(i int) {
	t := a.tabAt(i)
	if t == nil {
		return
	}
	switch {
	case t.sess != nil:
		t.sess.close()
	case t.state == tabConnecting:
		close(t.cancel)                 // releases a dial that is waiting on the fingerprint prompt
		go func(ch <-chan connResult) { // a late connection must not leak
			if r := <-ch; r.conn != nil {
				r.conn.Close()
			}
		}(t.dial)
	case t.state == tabReconnecting:
		a.stopReconnect(t)
		go func(ch <-chan connResult) {
			if r := <-ch; r.conn != nil {
				r.conn.Close()
			}
		}(t.dial)
	}
	a.tabs = append(a.tabs[:i-1], a.tabs[i:]...)
	switch {
	case a.active == i:
		a.setActive(i - 1)
	case a.active > i:
		a.setActive(a.active - 1)
	}
	a.Invalidate()
}
