//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"time"

	"fyne.io/systray"
)

//go:embed icon.ico
var iconICO []byte

//go:embed icon.png
var iconPNG []byte

// startTray shows the notification-area icon. Left click opens the window.
// systray.Run takes over its goroutine's OS thread for the message loop.
func startTray(h *hostState, onOpen func()) {
	go systray.Run(func() {
		systray.SetIcon(iconICO)
		systray.SetTitle("go-nx")
		systray.SetOnTapped(onOpen)
		systray.AddMenuItem("go-nx "+versionLabel(), "").Disable()
		open := systray.AddMenuItem("Open go-nx", "Show the window")
		logs := systray.AddMenuItem("View logs", "Show recent connection and error messages")
		pair := systray.AddMenuItem("Pair new device", "Let the next device that connects be trusted")
		// A nil channel never fires, so without an updater the select below ignores it.
		var upd *systray.MenuItem
		var updClicks <-chan struct{}
		if updatesEnabled() {
			upd = systray.AddMenuItem(updateLabel(), "Look for a newer go-nx, or restart into one that is ready")
			updClicks = upd.ClickedCh
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Stop sharing and exit")

		go func() {
			var lastTip, lastPair, lastUpd string
			for range time.Tick(time.Second) {
				if tip := h.tooltip(); tip != lastTip {
					lastTip = tip
					systray.SetTooltip(tip)
				}
				title := "Pair new device"
				if left := h.pairingLeft(); left > 0 {
					title = fmt.Sprintf("Pairing open: %d:%02d left", int(left.Minutes()), int(left.Seconds())%60)
				}
				if title != lastPair {
					lastPair = title
					pair.SetTitle(title)
				}
				if upd != nil {
					if label := updateLabel(); label != lastUpd {
						lastUpd = label
						upd.SetTitle(label)
					}
				}
			}
		}()
		for {
			select {
			case <-open.ClickedCh:
				onOpen()
			case <-logs.ClickedCh:
				logsRequested.Store(true)
				onOpen()
			case <-pair.ClickedCh:
				h.startPairing()
			case <-updClicks:
				updateClicked()
			case <-quit.ClickedCh:
				quitApp()
			}
		}
	}, nil)
}

// quitApp stops sharing and exits. The tray icon is removed first so it does not
// linger in the notification area until the mouse passes over it.
func quitApp() {
	systray.Quit()
	os.Exit(0)
}
