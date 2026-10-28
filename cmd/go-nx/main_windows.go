//go:build windows

// Command go-nx is both ends of a remote-control session: it shares this PC's screen with
// devices it has been paired with, and can view and control other PCs. It lives in the
// notification area; closing the window hides it there. The interface itself is package app.
package main

import (
	"bytes"
	"flag"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/orangepro2001/go-nx/internal/app"
	"github.com/orangepro2001/go-nx/internal/capture"
	"github.com/orangepro2001/go-nx/internal/logging"
	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/trust"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/update"
)

// configDir is where identity, trust lists, saved connections and the log live.
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		if exe, err := os.Executable(); err == nil {
			return filepath.Dir(exe)
		}
		return "."
	}
	return filepath.Join(dir, "go-nx")
}

func main() {
	// Before any window exists: the first DPI awareness that sticks decides, and the
	// capture and the injector both count in physical pixels.
	capture.EnableDPIAwareness()
	connect := flag.String("connect", "", "prefill the address to connect to")
	port := flag.Int("port", 7000, "UDP port to host on")
	listen := flag.String("listen", "", "restrict hosting to one local IP (optionally IP:port), e.g. 192.168.1.5; default is every interface")
	display := flag.String("display", "", `monitor to share: "primary", an index, or a device name such as \\.\DISPLAY2 (default: the "display" setting, else primary)`)
	layered := flag.Bool("layered", true, "include layered windows (tooltips, menus) in the capture; false is an experiment for a host whose own pointer flickers")
	fps := flag.Int("fps", remote.DefaultFPS, "capture frames per second while a viewer is watching a picture that changes")
	fpsActive := flag.Int("fps-active", remote.DefaultActiveFPS, "capture frames per second for a couple of seconds after the viewer's last input; 0 for no change")
	fpsIdle := flag.Int("fps-idle", remote.DefaultIdleFPS, "capture frames per second once nothing has changed and the viewer has done nothing for 3 s; 0 for no change")
	hidden := flag.Bool("hidden", false, "start in the tray without showing the window")
	updateURL := flag.String("update-url", update.BaseURL, "release server to look for updates on")
	noUpdate := flag.Bool("no-update", false, "never check for updates")
	restarted := flag.Bool("restarted", false, "set by the updater: wait for the previous instance to exit")
	flag.Parse()

	t0 := time.Now()
	var wait time.Duration
	if *restarted {
		wait = restartWait
	}
	if alreadyRunning(wait) {
		messageBox("go-nx", i18n.New(i18n.Match(winPlatform{}.PreferredLanguages())).T(i18n.KeyAlreadyRunning))
		return
	}
	dir := configDir()
	os.MkdirAll(dir, 0o700)
	logPath := filepath.Join(dir, "go-nx.log")
	logs, logErr := logging.Open(logPath)
	log.SetOutput(logs)
	defer logs.Close()
	mark := func(what string) { log.Printf("startup +%dms %s", time.Since(t0).Milliseconds(), what) }
	if logErr != nil {
		mark("log file unavailable: " + logErr.Error())
	} else {
		mark("log opened at " + logPath)
	}
	startUpdates(*updateURL, *noUpdate)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v", r)
			panic(r)
		}
	}()

	// A file that exists but is damaged (a crash or power loss while it was written, a
	// stray edit) is set aside and the program starts with it empty; the user is told once
	// everything is loaded.
	var recovered []string
	open := func(name string) *trust.Store {
		st, backup, err := trust.OpenStoreRecovering(filepath.Join(dir, name))
		if err != nil {
			messageBox("go-nx", "Cannot read "+name+": "+err.Error())
			log.Fatal(err)
		}
		if backup != "" {
			log.Printf("%s was unreadable; moved to %s", name, backup)
			recovered = append(recovered, name+" -> "+filepath.Base(backup))
		}
		return st
	}
	id, err := trust.Identity(dir)
	if err != nil {
		messageBox("go-nx", "Cannot create this PC's identity: "+err.Error())
		log.Fatal(err)
	}
	trusted, settings, hosts := open("trusted.json"), open("settings.json"), open("hosts.json")
	saved, backup, err := profiles.LoadRecovering(filepath.Join(dir, "connections.json"))
	if err != nil {
		messageBox("go-nx", "Cannot read connections.json: "+err.Error())
		log.Fatal(err)
	}
	if backup != "" {
		log.Printf("connections.json was unreadable; moved to %s", backup)
		recovered = append(recovered, "connections.json -> "+filepath.Base(backup))
	}
	if len(recovered) > 0 {
		messageBox("go-nx", "These files in "+dir+" could not be read, so they were set aside and go-nx started with them empty "+
			"(paired devices, saved hosts or saved connections may need to be set up again):\n\n"+strings.Join(recovered, "\n"))
	}
	mark("identity, settings and saved connections loaded")

	host := startHost(*listen, *port, remote.Rates{Base: *fps, Active: *fpsActive, Idle: *fpsIdle}, *display, *layered, id, trusted, settings)
	mark("host started")

	fonts, err := text.NewEngine(text.SystemLocator(), log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	a := app.New(app.Config{
		Version:   version,
		Host:      host,
		Profiles:  saved,
		Settings:  settings,
		Connector: app.NewConnector(id, hosts),
		Updater:   updaterOrNil(),
		Platform:  winPlatform{},
		Logs:      logs,
		LogPath:   logPath,
		Connect:   *connect,
	}, fonts)
	host.onChange(a.Invalidate)
	game, err := app.NewGame(a, fonts)
	if err != nil {
		log.Fatal(err)
	}
	game.BeforeUpdate = func() { windowTick(*hidden) }
	startTray(a, host)
	mark("tray started, entering window loop")

	if img, err := png.Decode(bytes.NewReader(iconPNG)); err == nil {
		ebiten.SetWindowIcon([]image.Image{img})
	}
	ebiten.SetWindowTitle("go-nx")
	ebiten.SetWindowSize(1120, 740)
	ebiten.SetWindowSizeLimits(640, 480, -1, -1)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowClosingHandled(true) // closing hides to the tray instead
	ebiten.SetRunnableOnUnfocused(true)  // keep going while another window is active
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}

var hiddenDone bool

// windowTick does what needs the window's handle, which Ebiten only makes visible after
// its first frame: finding it, honouring -hidden, and colouring the title bar.
func windowTick(startHidden bool) {
	if mainWindow.Load() == 0 {
		findMainWindow()
		if mainWindow.Load() == 0 {
			time.AfterFunc(50*time.Millisecond, ebiten.ScheduleFrame) // look again soon
		}
	}
	h := mainWindow.Load()
	if h == 0 {
		return
	}
	if startHidden && !hiddenDone {
		hiddenDone = true
		hideMainWindow()
	}
	if themePending.Swap(false) {
		setTitleBarDark(h, themeWanted.Load())
	}
}
