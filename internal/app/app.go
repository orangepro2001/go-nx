package app

import (
	"fmt"
	"log"
	"math/rand/v2"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// Page is one of the pages behind the sidebar.
type Page int

const (
	PageDevices Page = iota
	PageThisPC
	PageLogs
	PageSettings
)

// Settings keys.
const (
	keyLang  = "lang"  // auto, zh-Hans, en, ko
	keyTheme = "theme" // system, dark, light
	keySort  = "device_sort"
)

// Fonts is what the application asks of the text engine. *text.Engine is one; tests use
// FixedText.
type Fonts interface {
	text.Measurer
	SetLanguage(i18n.Lang)
	SetScale(float64)
	Ready() bool
	TakeChanged() bool
}

// FixedText is Fonts without any fonts: every character has a fixed width. Tests and the
// documentation screenshots of the layout use it.
type FixedText struct{ text.Fake }

func (FixedText) SetLanguage(i18n.Lang) {}
func (FixedText) SetScale(float64)      {}
func (FixedText) Ready() bool           { return true }
func (FixedText) TakeChanged() bool     { return false }

// toast is a notice that goes away by itself.
type toast struct {
	id    kit.ID
	text  string
	tone  kit.Tone
	until time.Time
}

// App is the interface and what stands behind it. Its methods are for one goroutine (the
// game loop's) except Invalidate, which any goroutine may call.
type App struct {
	cfg   Config
	fonts Fonts
	ui    *kit.UI
	tr    *i18n.Translator
	now   time.Time

	dirty   atomic.Bool
	pending atomic.Pointer[func()]          // work other goroutines hand to the game loop
	shared  atomic.Pointer[i18n.Translator] // the translator, for goroutines other than the game loop

	page    Page
	pageNow atomic.Int32 // page, for other goroutines
	tabs    []*tab
	active  int // 0 shows the pages, i shows tabs[i-1]
	serial  int
	nextOp  uint64

	lang      i18n.Lang
	themeMode string
	dark      bool

	scale  float64
	pw, ph int // window size in device pixels

	// state of the pages
	quickAddr, quickPw string
	hostPw             string
	revealed           map[kit.ID]bool // password fields showing what was typed
	setSection         int             // the part of the settings page shown: setGeneral...
	portText           string          // the port typed on the settings page
	netErr             string          // why the last move of the host failed, "" if it did not
	devQuery           string
	devSort            SortMode
	logJump            bool // the next time the log is shown, scroll to its end
	toasts             []toast
	toastSerial        int
	dialog             func() kit.Node // builds the dialog on screen, nil for none
	updateArmedUntil   time.Time
	wantFocus          kit.ID // who gets the keyboard when nothing has it (set while building)
	idCopied           time.Time

	quitting       bool
	reconnectWaits []time.Duration // the pauses before attempts to bring a connection back
	modSig         [4]bool         // which modifiers were shown as held on the remote PC
	fullscreen     bool            // the window is full screen: the picture has all of it
	startFocused   bool

	wakeAt   time.Time // the earliest time something other than input needs a frame
	treeVer  uint64    // counts the trees built, so a painter knows when to paint again
	viewRect kit.Rect  // where the remote picture goes, logical pixels; set while placing a live tab
}

// New makes the application. Fonts are the text engine (or FixedText), and the first
// window size must be given with Resize before the first Step.
func New(cfg Config, fonts Fonts) *App {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	a := &App{cfg: cfg, fonts: fonts, scale: 1, devSort: SortRecent, reconnectWaits: defaultReconnectWaits, revealed: map[kit.ID]bool{}}
	var base [8]byte
	for i := range base {
		base[i] = byte(rand.Uint32())
	}
	for _, b := range base {
		a.nextOp = a.nextOp<<8 | uint64(b)
	}
	a.nextOp >>= 1 // unique per run
	a.quickAddr = cfg.Connect
	a.hostPw = cfg.Host.Password()
	a.portText = strconv.Itoa(remote.DefaultPort)
	if p := cfg.Host.Info().Port; p > 0 {
		a.portText = strconv.Itoa(p)
	}
	if s, ok := cfg.Settings.Get(keySort); ok && SortMode(s) == SortName {
		a.devSort = SortName
	}
	a.ui = kit.NewUI(theme.DarkPalette(), i18n.New(i18n.English), fonts)
	a.ui.Clip = cfg.Platform.Clipboard()
	a.applyPreferences()
	a.now = cfg.Now()
	return a
}

// UI is the toolkit instance, for the shell that draws it.
func (a *App) UI() *kit.UI { return a.ui }

// Tr is the translator of the interface language in force. Only the game loop's goroutine
// may call it; other goroutines (the tray) use SharedTr.
func (a *App) Tr() *i18n.Translator { return a.tr }

// SharedTr is the translator of the interface language in force, safe to call from any
// goroutine.
func (a *App) SharedTr() *i18n.Translator { return a.shared.Load() }

// Lang is the interface language in force.
func (a *App) Lang() i18n.Lang { return a.lang }

// Dark reports whether the dark theme is in force.
func (a *App) Dark() bool { return a.dark }

// Invalidate says that something the interface shows has changed. Any goroutine may call it.
func (a *App) Invalidate() {
	a.dirty.Store(true)
	if a.cfg.Platform == nil || !a.cfg.Platform.WindowHidden() {
		ebiten.ScheduleFrame()
	}
}

// Resize tells the application the window's size in device pixels and its scale.
func (a *App) Resize(pw, ph int, scale float64) {
	if scale <= 0 {
		scale = 1
	}
	if pw == a.pw && ph == a.ph && scale == a.scale {
		return
	}
	a.pw, a.ph = pw, ph
	if scale != a.scale {
		a.scale = scale
		a.fonts.SetScale(scale)
		a.ui.Scale = scale
	}
	a.ui.SetSize(float64(pw)/scale, float64(ph)/scale)
}

// applyPreferences reads the language and theme settings and applies them.
func (a *App) applyPreferences() {
	lang := a.resolveLanguage()
	if a.tr == nil || lang != a.lang {
		a.lang = lang
		a.tr = i18n.New(lang)
		a.shared.Store(a.tr)
		a.ui.Tr = a.tr
		a.fonts.SetLanguage(lang)
		a.ui.Invalidate()
	}
	mode, _ := a.cfg.Settings.Get(keyTheme)
	if mode != "dark" && mode != "light" {
		mode = "system"
	}
	dark := true
	switch mode {
	case "light":
		dark = false
	case "system":
		if d, ok := a.cfg.Platform.SystemDark(); ok {
			dark = d
		}
	}
	if mode != a.themeMode || dark != a.dark || a.ui.Pal.BG == (kit.Color{}) {
		a.themeMode, a.dark = mode, dark
		if dark {
			a.ui.Pal = theme.DarkPalette()
		} else {
			a.ui.Pal = theme.LightPalette()
		}
		a.cfg.Platform.ApplyTheme(dark)
		a.ui.Invalidate()
	}
}

// resolveLanguage is the language the settings and the system ask for.
func (a *App) resolveLanguage() i18n.Lang {
	if s, ok := a.cfg.Settings.Get(keyLang); ok && s != "auto" {
		if l, ok := i18n.Parse(s); ok {
			return l
		}
	}
	return i18n.Match(a.cfg.Platform.PreferredLanguages())
}

// SetLanguage stores the choice ("auto", "zh-Hans", "en", "ko") and applies it at once.
func (a *App) SetLanguage(tag string) {
	if err := a.cfg.Settings.Set(keyLang, tag); err != nil {
		log.Printf("could not save setting: %v", err)
	}
	a.applyPreferences()
}

// SetTheme stores the choice ("system", "dark", "light") and applies it at once.
func (a *App) SetTheme(mode string) {
	if err := a.cfg.Settings.Set(keyTheme, mode); err != nil {
		log.Printf("could not save setting: %v", err)
	}
	a.applyPreferences()
}

// Do runs f on the game loop's goroutine at the next Step. Other goroutines use it to hand
// work to the interface (a tray click, say).
func (a *App) Do(f func()) {
	for {
		old := a.pending.Load()
		next := f
		if old != nil {
			prev := *old
			next = func() { prev(); f() }
		}
		if a.pending.CompareAndSwap(old, &next) {
			break
		}
	}
	a.Invalidate()
}

// ShowPage opens one of the pages (from the tray, say), leaving any session tab.
func (a *App) ShowPage(p Page) {
	a.page = p
	a.setActive(0)
}

// Step runs one frame of the application's logic with this frame's input: it takes work
// handed over by other goroutines, moves tabs along, lets the toolkit handle the input
// and rebuilds the tree if anything changed. Nothing here touches the GPU.
func (a *App) Step(in kit.Input) {
	a.now = in.Now
	a.pageNow.Store(int32(a.page))
	a.wakeAt = time.Time{}
	if f := a.pending.Swap(nil); f != nil {
		(*f)()
	}
	if a.fonts.TakeChanged() {
		a.ui.Invalidate()
	}
	a.pollTabs()
	a.expireToasts()
	if !a.updateArmedUntil.IsZero() && !a.updateArmed() {
		a.updateArmedUntil = time.Time{}
		a.ui.Invalidate()
	}
	a.ui.Handle(in)
	if a.dirty.Swap(false) {
		a.ui.Invalidate()
	}
	for i := 0; i < 3 && a.ui.Dirty(); i++ {
		a.wantFocus = ""
		a.ui.SetRoot(a.build())
		a.treeVer++
		if a.wantFocus != "" && !a.ui.HasFocus() {
			a.ui.Focus(a.wantFocus, true)
		}
	}
	if !a.startFocused {
		a.startFocused = true
		if a.active == 0 && a.page == PageDevices && !a.ui.HasFocus() {
			a.ui.Focus("quick.addr", false)
		}
	}
}

// toastFor is how long a notice stays.
const toastFor = 4 * time.Second

// Toast shows a notice in the corner for a few seconds.
func (a *App) Toast(s string, tone kit.Tone) {
	a.toastSerial++
	a.toasts = append(a.toasts, toast{id: kit.ID(fmt.Sprintf("toast.%d", a.toastSerial)), text: s, tone: tone, until: a.now.Add(toastFor)})
	if len(a.toasts) > 4 {
		a.toasts = a.toasts[len(a.toasts)-4:]
	}
	a.ui.Invalidate()
}

func (a *App) expireToasts() {
	kept := a.toasts[:0]
	for _, t := range a.toasts {
		if t.until.After(a.now) {
			kept = append(kept, t)
		}
	}
	if len(kept) != len(a.toasts) {
		a.ui.Invalidate()
	}
	a.toasts = kept
}

// NextWake is when the application next needs a frame without any input: a notice
// expiring, a caret blinking, a pairing code counting down. The zero time means never.
// Valid after the frame has been painted.
func (a *App) NextWake() time.Time {
	animating, at := a.ui.WantsFrame()
	if animating {
		a.WakeIn(16 * time.Millisecond) // an animation runs at about 60 frames a second, not flat out
	}
	if !a.wakeAt.IsZero() && (at.IsZero() || a.wakeAt.Before(at)) {
		at = a.wakeAt
	}
	for _, t := range a.toasts {
		if at.IsZero() || t.until.Before(at) {
			at = t.until
		}
	}
	if left := a.cfg.Host.Info().PairingLeft; left > 0 {
		next := a.now.Add(time.Second - time.Duration(a.now.Nanosecond()))
		if at.IsZero() || next.Before(at) {
			at = next
		}
	}
	return at
}

// openDialog shows the dialog build makes until closeDialog.
func (a *App) openDialog(build func() kit.Node) {
	a.dialog = build
	a.ui.Invalidate()
}

func (a *App) closeDialog() {
	a.dialog = nil
	a.ui.Invalidate()
}

// Quit stops sharing and exits.
func (a *App) Quit() {
	a.quitting = true
	a.cfg.Platform.Quit()
}

// TreeVersion counts the trees built; it changes when the interface has to be painted again.
func (a *App) TreeVersion() uint64 { return a.treeVer }
