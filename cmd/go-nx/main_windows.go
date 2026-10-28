//go:build windows

// Command go-nx is both ends of a remote-control session: it shares this PC's
// screen with devices it has been paired with, and can view and control other
// PCs. It lives in the notification area; closing the window hides it there.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"go-nx/internal/logging"
	"go-nx/internal/profiles"
	"go-nx/internal/remote"
	"go-nx/internal/trust"
	"go-nx/internal/ui"
	"go-nx/internal/update"
)

const (
	activeTPS   = 60 // while the window is visible
	idleTPS     = 5  // while hidden in the tray: nothing to draw, hosting runs elsewhere
	doubleClick = 400 * time.Millisecond
)

// logsRequested lets the tray goroutine ask the update loop to open the log
// viewer without touching app fields across goroutines.
var logsRequested atomic.Bool

type app struct {
	host      *hostState
	id        tls.Certificate
	hosts     *trust.Store // fingerprints of hosts we have connected to
	profiles  *profiles.List
	face      *text.GoTextFace
	logs      *logging.Logger
	logPath   string
	showLogs  bool
	logScroll int
	logRev    uint64
	logLines  []string
	logErr    error

	tabs   []*tab
	active int // 0 is the home tab, i is tabs[i-1]

	fields      [ui.NumFields]ui.Field
	focus       int
	scroll      int    // first visible saved connection
	delArmed    int    // saved connection waiting for a second click to delete, or -1
	forgetArmed bool   // "forget paired devices" waiting for a second click
	message     string // last note or error, shown on the home tab

	renaming     int // tab-bar index being renamed, 0 for none
	renameBuf    string
	lastClickTab int
	lastClickAt  time.Time

	nextOp     uint64
	ww, wh     int // window size in physical pixels
	title      string
	hidden     bool // -hidden: start in the tray
	hiddenDone bool
	tps        int
	t0         time.Time // process start, for startup timing
	drew       bool
}

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
	connect := flag.String("connect", "", "prefill the address to connect to")
	port := flag.Int("port", 7000, "UDP port to host on")
	fps := flag.Int("fps", 10, "capture frames per second when hosting")
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
		messageBox("go-nx", "go-nx is already running. Look for its icon in the notification area (tray).")
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

	// A file that exists but is damaged (a crash or power loss while it was
	// written, a stray edit) is set aside and the program starts with it empty;
	// the user is told once everything is loaded.
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
	face, err := ui.NewFace()
	if err != nil {
		log.Fatal(err)
	}
	mark("identity, settings and saved connections loaded")

	a := &app{
		host:     startHost(*port, *fps, id, trusted, settings),
		id:       id,
		hosts:    hosts,
		profiles: saved,
		face:     face,
		logs:     logs,
		logPath:  logPath,
		delArmed: -1,
		hidden:   *hidden,
		fields: [ui.NumFields]ui.Field{
			{Label: "My password"},
			{Label: "Name (to save)"},
			{Label: "Connect to", Value: *connect},
			{Label: "Password", Mask: true},
		},
	}
	a.fields[ui.FHostPw].Value = a.host.password()
	var base [8]byte
	rand.Read(base[:])
	a.nextOp = binary.LittleEndian.Uint64(base[:]) >> 1 // unique per run
	mark("host started")
	startTray(a.host, showMainWindow)
	mark("tray started, entering window loop")
	a.t0 = t0

	if img, err := png.Decode(bytes.NewReader(iconPNG)); err == nil {
		ebiten.SetWindowIcon([]image.Image{img})
	}
	ebiten.SetWindowTitle("go-nx")
	setHomeSize()
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowClosingHandled(true) // closing hides to the tray instead
	ebiten.SetRunnableOnUnfocused(true)  // keep going while another window is active
	if err := ebiten.RunGame(a); err != nil {
		log.Fatal(err)
	}
}

// Layout works in physical pixels. Returning the logical size instead would make
// Windows enlarge the finished picture on scaled displays, blurring small text.
func (a *app) Layout(ow, oh int) (int, int) {
	k := ebiten.Monitor().DeviceScaleFactor()
	a.ww, a.wh = int(float64(ow)*k), int(float64(oh)*k)
	return a.ww, a.wh
}

// setHomeSize makes the window fit the home tab (physical pixels).
func setHomeSize() {
	k := ebiten.Monitor().DeviceScaleFactor()
	ebiten.SetWindowSize(int(ui.HomeW/k), int((ui.HomeH+ui.BarHeight)/k))
}

// viewport is the part of the window below the tab bar.
func (a *app) viewport() ui.Rect {
	return ui.Rect{X: 0, Y: ui.BarHeight, W: a.ww, H: a.wh - ui.BarHeight}
}

func (a *app) Update() error {
	if a.tps == 0 {
		log.Printf("startup +%dms first Update (window system and GPU ready)", time.Since(a.t0).Milliseconds())
	}
	want := activeTPS
	if windowHidden.Load() {
		want = idleTPS
	}
	if want != a.tps {
		a.tps = want
		ebiten.SetTPS(want)
	}
	// Ebiten shows its window only after the first frame, so look for it until found.
	if mainWindow.Load() == 0 {
		findMainWindow()
	}
	if a.hidden && !a.hiddenDone && mainWindow.Load() != 0 {
		a.hiddenDone = true
		hideMainWindow()
	}
	if ebiten.IsWindowBeingClosed() {
		hideMainWindow() // sessions stay connected in the background
	}

	a.pollTabs()
	a.updateTabBar()
	if logsRequested.Swap(false) {
		a.showLogs = true
	}
	if a.showLogs {
		a.updateLogs()
		a.setTitle()
		return nil
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF3) {
		a.showLogs = true
		return nil
	}
	switch {
	case a.renaming > 0:
		a.updateRename()
		if t := a.tabAt(a.active); t != nil && t.sess != nil {
			t.sess.update(a.viewport(), false) // keep the picture current, forward nothing
		}
	case a.active == 0:
		a.updateHome()
	default:
		a.updateTab(a.tabAt(a.active))
	}
	a.setTitle()
	return nil
}

// updateLogs handles input while the log viewer is open.
func (a *app) updateLogs() {
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyF3) {
		a.showLogs = false
		return
	}
	cx, cy := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) && ui.LogsBackRect().Contains(cx, cy-ui.BarHeight) {
		a.showLogs = false
		return
	}
	if _, dy := ebiten.Wheel(); dy != 0 {
		a.refreshLogs()
		back := max(len(a.logLines)-ui.LogsVisibleLines(), 0)
		a.logScroll = min(max(a.logScroll+int(dy)*3, 0), back)
	}
}

// refreshLogs copies the in-memory log only when it has changed, so the draw
// loop does not allocate a snapshot every frame.
func (a *app) refreshLogs() {
	if rev := a.logs.Revision(); rev != a.logRev || a.logLines == nil {
		a.logRev = rev
		a.logLines = a.logs.Lines()
		a.logErr = a.logs.FileError()
	}
}

func (a *app) setTitle() {
	if a.showLogs {
		if a.title != "go-nx - logs" {
			a.title = "go-nx - logs"
			ebiten.SetWindowTitle(a.title)
		}
		return
	}
	t := "go-nx"
	if tb := a.tabAt(a.active); tb != nil {
		t += " - " + tb.title
		if tb.sess != nil {
			if s := tb.sess.status(); s != "" {
				t += " - " + s
			}
		}
	}
	if t != a.title {
		a.title = t
		ebiten.SetWindowTitle(t)
	}
}

// updateTabBar handles clicks on tabs: select, close, double-click to rename.
func (a *app) updateTabBar() {
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	cx, cy := ebiten.CursorPosition()
	idx, onClose := ui.TabAt(len(a.tabs)+1, a.ww, cx, cy)
	if a.renaming > 0 && idx != a.renaming {
		a.commitRename()
	}
	if idx < 0 {
		return
	}
	now := time.Now()
	switch {
	case onClose:
		a.closeTab(idx)
		idx = -1
	case idx == a.renaming:
		// clicking inside the edit box keeps editing
	case idx > 0 && idx == a.lastClickTab && now.Sub(a.lastClickAt) < doubleClick:
		a.setActive(idx)
		a.renaming, a.renameBuf = idx, a.tabAt(idx).title
		idx = -1
	default:
		a.setActive(idx)
	}
	a.lastClickTab, a.lastClickAt = idx, now
}

// editText applies typed characters and Backspace to s.
func editText(s string, max int) string {
	for _, r := range ebiten.AppendInputChars(nil) {
		if r >= 32 && r != 127 && utf8.RuneCountInString(s) < max {
			s += string(r)
		}
	}
	if d := inpututil.KeyPressDuration(ebiten.KeyBackspace); d == 1 || (d > repeatDelay && d%repeatEvery == 0) {
		if _, n := utf8.DecodeLastRuneInString(s); n > 0 {
			s = s[:len(s)-n]
		}
	}
	return s
}

func (a *app) updateRename() {
	a.renameBuf = editText(a.renameBuf, 48)
	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter):
		a.commitRename()
	case inpututil.IsKeyJustPressed(ebiten.KeyEscape):
		a.renaming = 0
	}
}

// updateTab handles the tab showing a session.
func (a *app) updateTab(t *tab) {
	switch t.state {
	case ui.TabLive:
		// These shortcuts are never forwarded to the remote PC.
		if ebiten.IsKeyPressed(ebiten.KeyControl) && ebiten.IsKeyPressed(ebiten.KeyAlt) {
			switch {
			case inpututil.IsKeyJustPressed(ebiten.KeyQ):
				a.closeTab(a.active)
				return
			case inpututil.IsKeyJustPressed(ebiten.KeyArrowRight):
				a.cycleTab(1)
				return
			case inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft):
				a.cycleTab(-1)
				return
			}
		}
		t.sess.update(a.viewport(), true)
	case ui.TabError:
		if t.hostChanged && ebiten.IsKeyPressed(ebiten.KeyControl) && inpututil.IsKeyJustPressed(ebiten.KeyF) {
			if err := remote.ForgetHost(a.hosts, t.addr); err != nil {
				t.err = "Could not forget the saved identity: " + err.Error()
			} else {
				t.hostChanged = false
				t.err = "Forgot the saved identity of this host. Press Enter to connect and pin the identity it presents now."
				log.Printf("forgot saved identity of %s", t.addr)
			}
			return
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter) {
			a.startDial(t)
		}
	}
}

func (a *app) updateHome() {
	cx, cy := ebiten.CursorPosition()
	hy := cy - ui.BarHeight
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) && hy >= 0 {
		armed := a.delArmed
		a.delArmed = -1
		forgetArmed := a.forgetArmed
		a.forgetArmed = false
		hit := ui.HomeHit(cx, hy, len(a.profiles.Items), a.scroll)
		switch hit.Kind {
		case ui.HitCheckbox:
			a.host.setRequireAuth(!a.host.requireAuth())
		case ui.HitPair:
			a.host.startPairing()
		case ui.HitQuit:
			quitApp()
		case ui.HitForget:
			switch {
			case a.host.trustedCount() == 0:
				a.message = "No paired devices to forget"
			case !forgetArmed:
				a.forgetArmed = true
			default:
				n, err := a.host.forgetAll()
				if err != nil {
					a.message = "Forgot the paired devices in memory but could not save: " + err.Error()
				} else {
					a.message = fmt.Sprintf("Forgot %d paired device(s)", n)
				}
			}
		case ui.HitField:
			a.focus = hit.Index
		case ui.HitConnect:
			a.connectForm()
		case ui.HitSave:
			a.saveForm()
		case ui.HitProfile:
			p := a.profiles.Items[hit.Index]
			a.openTab(p.Name, p.Addr, p.Password, p.Name)
		case ui.HitLogs:
			a.showLogs, a.logScroll = true, 0
		case ui.HitDelete:
			if armed != hit.Index {
				a.delArmed = hit.Index
				break
			}
			if err := a.profiles.Delete(hit.Index); err != nil {
				a.message = "Could not save connections: " + err.Error()
			}
			a.scroll = min(a.scroll, max(len(a.profiles.Items)-ui.VisibleRows(), 0))
		}
	}
	if _, dy := ebiten.Wheel(); dy != 0 && cx >= ui.HomeW-500 {
		a.scroll = min(max(a.scroll-int(dy), 0), max(len(a.profiles.Items)-ui.VisibleRows(), 0))
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		a.focus = (a.focus + 1) % ui.NumFields
	}
	f := &a.fields[a.focus]
	before := f.Value
	f.Value = editText(f.Value, 64)
	if a.focus == ui.FHostPw && f.Value != before {
		a.host.setPassword(f.Value)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter) {
		if a.focus == ui.FPeerPw { // Enter moves on; only the last field connects
			a.connectForm()
		} else {
			a.focus++
		}
	}
}

// formTarget reads the connection form.
func (a *app) formTarget() (name, addr string, ok bool) {
	addr = strings.TrimSpace(a.fields[ui.FAddr].Value)
	if addr == "" {
		a.message = "Enter the other PC's address"
		return "", "", false
	}
	name = strings.TrimSpace(a.fields[ui.FName].Value)
	if name == "" {
		name = addr
	}
	return name, addr, true
}

func (a *app) connectForm() {
	name, addr, ok := a.formTarget()
	if !ok {
		return
	}
	profile := ""
	if _, saved := a.profiles.Find(name); saved {
		profile = name
	}
	a.message = ""
	a.openTab(name, addr, a.fields[ui.FPeerPw].Value, profile)
}

func (a *app) saveForm() {
	name, addr, ok := a.formTarget()
	if !ok {
		return
	}
	if err := a.profiles.Upsert(profiles.Profile{Name: name, Addr: addr, Password: a.fields[ui.FPeerPw].Value}); err != nil {
		a.message = "Could not save: " + err.Error()
		return
	}
	a.message = "Saved \"" + name + "\""
}

func (a *app) homeData() *ui.HomeData {
	return &ui.HomeData{
		Host: a.host.info(), Fields: a.fields, Focus: a.focus, Profiles: a.profiles.Items,
		Scroll: a.scroll, DelArmed: a.delArmed, ForgetArmed: a.forgetArmed, Message: a.message,
		Caret: time.Now().UnixMilli()/500%2 == 0,
	}
}

func (a *app) Draw(screen *ebiten.Image) {
	if !a.drew {
		a.drew = true
		log.Printf("startup +%dms first Draw", time.Since(a.t0).Milliseconds())
	}
	screen.Fill(ui.ColBG)
	if a.showLogs {
		a.refreshLogs()
		ui.DrawLogs(screen, a.face, a.logLines, a.logScroll, a.logPath, a.logErr, ui.BarHeight)
		ui.DrawBar(screen, a.face, a.tabInfos(), a.active, a.renaming, a.renameBuf,
			time.Now().UnixMilli()/500%2 == 0, a.ww)
		return
	}
	vp := a.viewport()
	if t := a.tabAt(a.active); t == nil {
		ui.DrawHome(screen, a.face, a.homeData(), ui.BarHeight)
	} else if t.state == ui.TabLive {
		screen.SubImage(image.Rect(vp.X, vp.Y, vp.X+vp.W, vp.Y+vp.H)).(*ebiten.Image).Fill(color.Black)
		t.sess.draw(screen, vp)
	} else {
		msg, c := "Connecting to "+t.addr+" ...", ui.ColWarn
		if t.state == ui.TabError {
			msg, c = t.err+". Press Enter to reconnect, or close this tab.", ui.ColBad
		}
		for i, line := range ui.Wrap(a.face, msg, float64(min(vp.W-80, 900))) {
			ui.Text(screen, a.face, line, 40, vp.Y+60+i*30, c)
		}
	}
	ui.DrawBar(screen, a.face, a.tabInfos(), a.active, a.renaming, a.renameBuf,
		time.Now().UnixMilli()/500%2 == 0, a.ww)
}
