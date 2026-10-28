//go:build windows

package main

import (
	_ "embed"
	"os"
	"time"

	"fyne.io/systray"

	"github.com/orangepro2001/go-nx/internal/app"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

//go:embed icon.ico
var iconICO []byte

//go:embed icon.png
var iconPNG []byte

// startTray shows the notification-area icon. Left click opens the window. systray.Run
// takes over its goroutine's OS thread for the message loop. The menu is in the language of
// the interface and follows it when it changes.
func startTray(a *app.App, h *hostState) {
	show := func() {
		showMainWindow()
		a.Invalidate()
	}
	go systray.Run(func() {
		systray.SetIcon(iconICO)
		systray.SetTitle("go-nx")
		systray.SetOnTapped(show)
		tr := a.SharedTr()
		ver := systray.AddMenuItem("go-nx "+versionLabel(), "")
		ver.Disable()
		open := systray.AddMenuItem(tr.T(i18n.KeyTrayOpen), "")
		logs := systray.AddMenuItem(tr.T(i18n.KeyTrayLogs), "")
		pair := systray.AddMenuItem(tr.T(i18n.KeyTrayPair), "")
		// A nil channel never fires, so without an updater the select below ignores it.
		var upd *systray.MenuItem
		var updClicks <-chan struct{}
		if updatesEnabled() {
			upd = systray.AddMenuItem("", "")
			updClicks = upd.ClickedCh
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem(tr.T(i18n.KeyTrayQuit), "")

		go func() {
			var lastTip, lastPair, lastUpd, lastOpen, lastLogs, lastQuit string
			set := func(item *systray.MenuItem, last *string, s string) {
				if s != *last {
					*last = s
					item.SetTitle(s)
				}
			}
			for range time.Tick(time.Second) {
				tr := a.SharedTr()
				if tip := h.tooltip(tr); tip != lastTip {
					lastTip = tip
					systray.SetTooltip(tip)
				}
				title := tr.T(i18n.KeyTrayPair)
				if left := h.policy.PairingLeft(); left > 0 {
					title = tr.T(i18n.KeyTrayPairing, "minutes", int(left.Minutes()), "seconds", twoDigits(int(left.Seconds())%60))
				}
				set(pair, &lastPair, title)
				set(open, &lastOpen, tr.T(i18n.KeyTrayOpen))
				set(logs, &lastLogs, tr.T(i18n.KeyTrayLogs))
				set(quit, &lastQuit, tr.T(i18n.KeyTrayQuit))
				if upd != nil {
					st := updater.Status()
					label, _ := app.UpdateLabel(tr, st, restartPending(st), false)
					set(upd, &lastUpd, label)
				}
			}
		}()
		for {
			select {
			case <-open.ClickedCh:
				show()
			case <-logs.ClickedCh:
				a.Do(func() { a.ShowPage(app.PageLogs) })
				show()
			case <-pair.ClickedCh:
				// While a code is showing, a second click only brings it back into view: a new
				// code would silently invalidate the one the person may already be typing.
				if h.pairingLeft() <= 0 {
					h.StartPairing()
				}
				a.Do(func() { a.ShowPage(app.PageThisPC) })
				show()
			case <-updClicks:
				go updateClicked()
			case <-quit.ClickedCh:
				quitApp()
			}
		}
	}, nil)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// quitApp stops sharing and exits. The tray icon is removed first so it does not linger in
// the notification area until the mouse passes over it.
//
// Every open session is closed first, from whichever goroutine called: the hosts then let
// go of the keys and buttons this PC was holding on them at once, instead of waiting for a
// timeout.
func quitApp() {
	app.CloseAllSessions()
	systray.Quit()
	os.Exit(0)
}
