//go:build windows

package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/orangepro2001/go-nx/internal/app"
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

// startUpdates repairs what an interrupted update can leave behind and, unless
// disabled, starts the background update checks. The backup of the previous build
// is removed by the updater once this build has run for its first delay, not here:
// until then it is the way back from an update that turns out not to work.
func startUpdates(url string, disabled bool) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("update: disabled: cannot locate the running exe: %v", err)
		return
	}
	// Only the backup copy can be left after an update was cut short between its two
	// renames. Started by that name, it takes the real name back.
	if fixed, err := update.Recover(exe); err != nil {
		log.Printf("%v", err)
	} else if fixed != exe {
		log.Printf("update: an earlier update was interrupted; restored %s from the backup copy", fixed)
		exe = fixed
	}
	if disabled {
		update.CleanupOld(exe) // nothing else will manage the backup
		log.Printf("update: disabled by -no-update")
		return
	}
	u, err := update.New(update.Config{BaseURL: url, Version: version, Exe: exe})
	if err != nil {
		update.CleanupOld(exe)
		log.Printf("update: disabled: %v", err)
		return
	}
	updater = u
	go u.Run(context.Background())
	log.Printf("update: v%s checks %s", version, url)
}

func updatesEnabled() bool { return updater != nil }

// updaterOrNil is the updater as the application's service, or a true nil interface when
// this build does not update.
func updaterOrNil() app.UpdateService {
	if updater == nil {
		return nil
	}
	return updateService{}
}

// versionLabel is how this build is named in the tray.
func versionLabel() string {
	if version == "dev" {
		return "(dev build)"
	}
	return "v" + version
}

// restartPending reports whether clicking the update button restarts go-nx.
func restartPending(st update.Status) bool {
	return st.State == update.Ready || st.State == update.Installed
}

// updateClicked installs a ready update and restarts into it; an update that is
// already installed (an earlier restart did not happen) is only restarted into;
// otherwise it asks for a check right away. Restarting ends every remote session,
// which is why it only ever happens on a click.
func updateClicked() {
	u := updater
	if u == nil {
		return
	}
	switch u.Status().State {
	case update.Ready:
		if err := u.Install(); err != nil {
			log.Printf("%v", err)
			messageBox("go-nx", "Cannot install the update: "+err.Error())
			return
		}
		log.Printf("update: installed, restarting")
	case update.Installed:
		log.Printf("update: restarting into the installed update")
	default:
		u.CheckNow()
		return
	}
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

// updateService is the updater as the application uses it (app.UpdateService).
type updateService struct{}

func (updateService) Status() update.Status { return updater.Status() }
func (updateService) RestartPending() bool  { return restartPending(updater.Status()) }

// Click installs and restarts, or checks; installing hashes and moves a whole executable,
// so it does not run on the interface's goroutine.
func (updateService) Click() { go updateClicked() }
