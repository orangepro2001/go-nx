//go:build windows

package main

import (
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"time"

	"go-nx/internal/remote"
	"go-nx/internal/ui"
)

// tab is one remote session. It is created as soon as a connection is started,
// so several can be connecting or live at once.
type tab struct {
	title       string
	addr        string // normalized host:port
	password    string
	profile     string // name of the saved connection this tab came from, or ""
	state       ui.TabState
	err         string // why the tab is in the error state
	hostChanged bool   // the error is a changed host certificate, which Ctrl+F can clear
	sess        *session
	dial        chan connResult

	confirmReq chan *hostConfirm // the dial goroutine asks the user through this
	confirm    *hostConfirm      // the question being shown, or nil
	cancel     chan struct{}     // closed when the tab is closed while connecting
}

// hostConfirm is a first-time host waiting for the user to compare its
// fingerprint with the one the host displays. The answer goes to reply.
type hostConfirm struct {
	fp    string
	reply chan bool
}

// errHostNotConfirmed means the user declined (or never answered) the prompt for
// a host seen for the first time. Nothing was sent to it.
var errHostNotConfirmed = errors.New("host identity not confirmed")

type connResult struct {
	cl  *remote.Client
	err error
}

// normalizeAddr adds the default port when none is given.
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, "7000")
	}
	return addr
}

// tabAt returns the session tab for tab-bar index i (0 is home).
func (a *app) tabAt(i int) *tab {
	if i < 1 || i > len(a.tabs) {
		return nil
	}
	return a.tabs[i-1]
}

func (a *app) tabInfos() []ui.TabInfo {
	infos := []ui.TabInfo{{Title: "Home"}}
	for _, t := range a.tabs {
		infos = append(infos, ui.TabInfo{Title: t.title, State: t.state})
	}
	return infos
}

// openTab starts connecting in a new tab. The host accepts one controller at a
// time, so a second connection from here would kick the first one off: if this
// address already has a tab, switch to it (and retry it if it had failed).
func (a *app) openTab(title, addr, password, profile string) {
	addr = normalizeAddr(addr)
	for i, t := range a.tabs {
		if t.addr == addr {
			if t.state == ui.TabError { // retry in the same tab
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
	t := &tab{title: title, addr: addr, password: password, profile: profile}
	a.tabs = append(a.tabs, t)
	a.startDial(t)
	a.setActive(len(a.tabs))
}

// dialWaits are the handshake time limits of successive attempts. QUIC resends
// its first packet with growing gaps (0.2, 0.6, 1.4, 3, 6 s...), so on a path that
// takes several seconds to come up (a Tailscale route that is not yet direct) one
// long attempt can miss the moment the path opens. Several short attempts, each
// sending its first packet at once, catch it.
var dialWaits = []time.Duration{2 * time.Second, 3 * time.Second, 4 * time.Second, 6 * time.Second}

func (a *app) startDial(t *tab) {
	t.state, t.err, t.hostChanged = ui.TabConnecting, "", false
	t.dial = make(chan connResult, 1)
	t.confirmReq, t.confirm, t.cancel = make(chan *hostConfirm, 1), nil, make(chan struct{})
	go func(addr, password string, out chan<- connResult, ask chan<- *hostConfirm, cancel <-chan struct{}) {
		// A host seen for the first time is identified and confirmed by the user
		// before anything is sent to it: the password or pairing code only ever goes
		// to a pinned identity.
		if _, pinned := remote.PinnedFingerprint(a.hosts, addr); !pinned {
			fp, err := a.probe(addr)
			if err != nil {
				out <- connResult{nil, err}
				return
			}
			if !askHostConfirm(ask, cancel, fp) {
				out <- connResult{nil, errHostNotConfirmed}
				return
			}
			if err := remote.PinHost(a.hosts, addr, fp); err != nil {
				out <- connResult{nil, err}
				return
			}
			log.Printf("pinned %s after the user confirmed %s", addr, shortID(fp))
		}
		var cl *remote.Client
		var err error
		for n, wait := range dialWaits {
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), wait)
			cl, err = remote.Dial(ctx, addr, a.id, a.hosts, password)
			cancel()
			log.Printf("dial %s attempt %d/%d: %v (%dms)", addr, n+1, len(dialWaits), err, time.Since(start).Milliseconds())
			if err == nil || strings.Contains(err.Error(), remote.ErrHostChanged.Error()) {
				break // connected, or retrying cannot help
			}
		}
		out <- connResult{cl, err}
	}(t.addr, t.password, t.dial, t.confirmReq, t.cancel)
}

// probe learns the host's fingerprint, retrying like a normal dial because the
// path may take a few seconds to come up.
func (a *app) probe(addr string) (string, error) {
	var fp string
	var err error
	for n, wait := range dialWaits {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		fp, err = remote.Probe(ctx, addr, a.id)
		cancel()
		log.Printf("probe %s attempt %d/%d: %v", addr, n+1, len(dialWaits), err)
		if err == nil {
			return fp, nil
		}
	}
	return "", err
}

// hostConfirmWait is how long the prompt waits for an answer before it counts as no.
const hostConfirmWait = 2 * time.Minute

// askHostConfirm puts the question to the UI and waits for the answer. A tab
// that is closed, or an answer that never comes, is a no.
func askHostConfirm(ask chan<- *hostConfirm, cancel <-chan struct{}, fp string) bool {
	req := &hostConfirm{fp: fp, reply: make(chan bool, 1)}
	select {
	case ask <- req:
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

// shortID is the fingerprint prefix the host's home tab shows as its ID, which
// is what the user compares.
func shortID(fp string) string { return fp[:min(16, len(fp))] }

// pollTabs moves tabs between connecting, live and error as results arrive.
func (a *app) pollTabs() {
	for i, t := range a.tabs {
		switch t.state {
		case ui.TabConnecting:
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
					t.state, t.err = ui.TabError, describe(r.err)
					t.hostChanged = strings.Contains(r.err.Error(), remote.ErrHostChanged.Error())
					continue
				}
				t.sess = newSession(r.cl, &a.nextOp)
				t.state = ui.TabLive
				t.sess.setVisible(a.active == i+1)
			default:
			}
		case ui.TabLive:
			select {
			case <-t.sess.done:
				t.sess.mu.Lock()
				err := t.sess.err
				t.sess.mu.Unlock()
				t.sess.setVisible(false)
				log.Printf("session %s ended: %v", t.addr, err)
				t.state, t.err, t.sess = ui.TabError, "Disconnected: "+describe(err), nil
			default:
			}
		}
	}
}

// setActive shows tab i (0 is home). Leaving a session releases its held input.
func (a *app) setActive(i int) {
	if i != a.renaming {
		a.renaming = 0
	}
	a.active = i
	for j, t := range a.tabs {
		if t.sess != nil {
			t.sess.setVisible(j+1 == i)
		}
	}
}

// cycleTab moves to the next (delta 1) or previous (-1) tab, wrapping round.
func (a *app) cycleTab(delta int) {
	n := len(a.tabs) + 1
	a.setActive((a.active + delta + n) % n)
}

// closeTab disconnects and removes tab i.
func (a *app) closeTab(i int) {
	t := a.tabAt(i)
	if t == nil {
		return
	}
	switch {
	case t.sess != nil:
		t.sess.close()
	case t.state == ui.TabConnecting:
		close(t.cancel)                 // releases a dial that is waiting on the fingerprint prompt
		go func(ch <-chan connResult) { // a late connection must not leak
			if r := <-ch; r.cl != nil {
				r.cl.Close()
			}
		}(t.dial)
	}
	a.tabs = append(a.tabs[:i-1], a.tabs[i:]...)
	a.renaming = 0
	switch {
	case a.active == i:
		a.setActive(i - 1)
	case a.active > i:
		a.setActive(a.active - 1)
	}
}

// commitRename applies the edit box to the tab, and to its saved connection.
func (a *app) commitRename() {
	idx := a.renaming
	a.renaming = 0
	name := strings.TrimSpace(a.renameBuf)
	t := a.tabAt(idx)
	if t == nil || name == "" {
		return
	}
	if t.profile != "" {
		final, err := a.profiles.Rename(t.profile, name)
		if err != nil {
			log.Printf("save connections: %v", err)
		}
		t.profile, t.title = final, final
		return
	}
	t.title = name
}
