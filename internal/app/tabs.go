package app

import (
	"context"
	"log"
	"net"
	"strings"
	"time"
)

type tabState int

const (
	tabConnecting tabState = iota
	tabLive
	tabError
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
	sess        *session
	dial        chan connResult

	confirmReq chan *hostConfirm // the dial goroutine asks the person through this
	confirm    *hostConfirm      // the question being shown, or nil
	cancel     chan struct{}     // closed when the tab is closed while connecting

	lastClick time.Time // for the double click that renames
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
	t.state, t.err, t.hostChanged = tabConnecting, "", false
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
					continue
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
				log.Printf("session %s ended: %v", t.addr, err)
				reason, _ := describe(a.tr, err)
				t.state, t.err, t.sess = tabError, a.tr.T(keyErrDisconnected, "reason", reason), nil
			default:
			}
		}
	}
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
