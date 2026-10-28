//go:build windows

package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/orangepro2001/go-nx/internal/update"
)

// version is the release this build was made from. build.sh sets it with
// -ldflags -X; a plain `go build` leaves "dev", which never updates itself.
var version = "dev"

// restartWait is how long a restarted instance waits for its predecessor to exit
// and release the single-instance mutex.
const restartWait = 10 * time.Second

// updater is nil in dev builds, with -no-update, and when it could not be set up.
var updater *update.Updater

// startUpdates removes the build a previous update moved aside and, unless
// disabled, starts the background update checks.
func startUpdates(url string, disabled bool) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("update: disabled: cannot locate the running exe: %v", err)
		return
	}
	update.CleanupOld(exe)
	if disabled {
		log.Printf("update: disabled by -no-update")
		return
	}
	u, err := update.New(update.Config{BaseURL: url, Version: version, Exe: exe})
	if err != nil {
		log.Printf("update: disabled: %v", err)
		return
	}
	updater = u
	go u.Run(context.Background())
	log.Printf("update: v%s checks %s", version, url)
}

func updatesEnabled() bool { return updater != nil }

// versionLabel is how this build is named in the tray.
func versionLabel() string {
	if version == "dev" {
		return "(dev build)"
	}
	return "v" + version
}

// updateLabel is the text of the tray's update entry.
func updateLabel() string {
	if updater == nil {
		return ""
	}
	return updater.Status().Label()
}

// homeUpdate is what the home tab's update button shows: its text, and whether a
// verified update is waiting. armed means the user already clicked once on a
// ready update, which restarts go-nx and so ends every remote session.
func homeUpdate(armed bool) (label string, ready bool) {
	if updater == nil {
		return "", false
	}
	st := updater.Status()
	ready = st.State == update.Ready
	if ready && armed {
		return "Click again to restart now", true
	}
	return st.Label(), ready
}

// updateProblem is why the last update check failed, for the home tab's message
// line; empty when nothing is wrong.
func updateProblem() string {
	if updater == nil {
		return ""
	}
	if st := updater.Status(); st.State == update.Failed && st.Err != nil {
		return "Update check failed: " + st.Err.Error()
	}
	return ""
}

// updateClicked installs a ready update and restarts into it; otherwise it asks
// for a check right away. Restarting ends every remote session, which is why it
// only ever happens on a click.
func updateClicked() {
	u := updater
	if u == nil {
		return
	}
	if u.Status().State != update.Ready {
		u.CheckNow()
		return
	}
	if err := u.Install(); err != nil {
		log.Printf("%v", err)
		messageBox("go-nx", "Cannot install the update: "+err.Error())
		return
	}
	log.Printf("update: installed, restarting")
	// The exe path was taken at startup: after the rename the running process's
	// own idea of its path may point at the .old file.
	exe := u.Exe()
	if err := exec.Command(exe, append(os.Args[1:], "-restarted")...).Start(); err != nil {
		log.Printf("update: restart failed: %v", err)
		messageBox("go-nx", "The update is installed. Please start go-nx again to use it.\n\n"+err.Error())
		return
	}
	quitApp()
}
