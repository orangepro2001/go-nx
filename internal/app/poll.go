package app

import (
	"time"
)

// pollEvery is how often the application looks for changes nobody tells it about: the
// updater's progress and new lines in the log. Things that can call Invalidate do; this is
// for the ones that cannot.
const pollEvery = 500 * time.Millisecond

type pollState struct {
	updState              int
	updVersion            string
	updPercent            int
	updMandatory          bool
	updErr                string
	logRevision           uint64
	hostPairingSeconds    int
	updaterRestartPending bool
}

// snapshot reads what the poll watches. It is called from the polling goroutine, so it may
// only touch what is safe from there.
func (a *App) snapshot() pollState {
	var s pollState
	if u := a.cfg.Updater; u != nil {
		st := u.Status()
		s.updState, s.updVersion, s.updPercent, s.updMandatory = int(st.State), st.Version, st.Percent, st.Mandatory
		if st.Err != nil {
			s.updErr = st.Err.Error()
		}
		s.updaterRestartPending = u.RestartPending()
	}
	if Page(a.pageNow.Load()) == PageLogs && a.cfg.Logs != nil {
		s.logRevision = a.cfg.Logs.Revision()
	}
	return s
}

// StartPolling begins watching for changes the application is not told about, and asks for
// a frame when there is one. It does nothing while the window is hidden in the tray, and
// stops when stop is closed.
func (a *App) StartPolling(stop <-chan struct{}) {
	go func() {
		last := a.snapshot()
		t := time.NewTicker(pollEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if a.cfg.Platform.WindowHidden() {
					continue
				}
				if s := a.snapshot(); s != last {
					last = s
					a.Invalidate()
				}
			}
		}
	}()
}
