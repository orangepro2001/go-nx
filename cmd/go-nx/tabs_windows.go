//go:build windows

package main

import (
	"context"
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
	title    string
	addr     string // normalized host:port
	password string
	profile  string // name of the saved connection this tab came from, or ""
	state    ui.TabState
	err      string // why the tab is in the error state
	sess     *session
	dial     chan connResult
}

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
	t.state, t.err = ui.TabConnecting, ""
	t.dial = make(chan connResult, 1)
	go func(addr, password string, out chan<- connResult) {
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
	}(t.addr, t.password, t.dial)
}

// pollTabs moves tabs between connecting, live and error as results arrive.
func (a *app) pollTabs() {
	for i, t := range a.tabs {
		switch t.state {
		case ui.TabConnecting:
			select {
			case r := <-t.dial:
				if r.err != nil {
					t.state, t.err = ui.TabError, describe(r.err)
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
