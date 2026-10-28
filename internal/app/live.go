package app

import (
	"log"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/ui/gfx"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/view"
)

// viewportPx is where the remote picture goes, in device pixels.
func (a *App) viewportPx() rect {
	k := a.scale
	return rect{int(a.viewRect.X*k + 0.5), int(a.viewRect.Y*k + 0.5), int(a.viewRect.W*k + 0.5), int(a.viewRect.H*k + 0.5)}
}

// keysToUI reports whether the keyboard belongs to the interface. In a tab showing a
// picture it belongs to the remote PC, apart from the shortcuts, and unless a dialog is open
// or the box for sending text has the keyboard.
func (a *App) keysToUI() bool {
	t, _, _ := a.pictureTab()
	if t == nil || a.dialog != nil {
		return true
	}
	return t.textOpen && a.ui.Focused() == "bar.text"
}

// StepLive is the part of a frame that needs the GPU and the real input devices: the
// picture of the tab on show is brought up to date and, as far as the keyboard and the
// pointer belong to it, what the person does is sent to the remote PC. fit sizes the
// window to the picture the first time one arrives.
func (a *App) StepLive(fit func(fw, fh int)) {
	t := a.liveTab()
	if t == nil {
		a.syncFullscreen()
		return
	}
	s := t.sess
	forward := a.dialog == nil
	if forward && a.shortcuts(t, s) {
		return
	}
	s.update(a.viewportPx(), gate{keys: forward && a.keysToRemote(t), pointer: forward && !a.pointerOnBar()}, fit)
	if s.holding() || s.panning {
		a.WakeIn(16 * time.Millisecond) // keep sending: a held key repeats, a held button stays alive, the picture pans
	}
	a.tickBar(t)
	if t.barShown && !t.textOpen {
		a.wakeAtTime(t.barUntil)
	}
	if t.stats {
		a.WakeIn(500 * time.Millisecond)
		a.ui.Invalidate()
	}
	a.noteModifiers(s)
}

// keysToRemote: the keyboard goes to the remote PC unless the box for sending text has it.
func (a *App) keysToRemote(t *tab) bool { return !(t.textOpen && a.ui.Focused() == "bar.text") }

func (a *App) wakeAtTime(at time.Time) {
	if d := at.Sub(a.now); d > 0 {
		a.WakeIn(d)
	}
}

// noteModifiers asks for the interface to be built again when the set of modifier keys held
// on the remote PC changed, so that the badges follow.
func (a *App) noteModifiers(s *session) {
	c, al, sh, w := s.modifiers()
	sig := [4]bool{c, al, sh, w}
	if sig != a.modSig {
		a.modSig = sig
		a.Invalidate()
	}
}

// shortcuts handles the Ctrl+Alt+... keys of a session, which are never sent to the remote
// PC. It reports whether one was used (the rest of the frame's input is then dropped).
func (a *App) shortcuts(t *tab, s *session) bool {
	if !ebiten.IsKeyPressed(ebiten.KeyControl) || !ebiten.IsKeyPressed(ebiten.KeyAlt) {
		return false
	}
	used := true
	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeyQ):
		s.releaseKeys()
		a.closeTab(a.active)
		return true
	case inpututil.IsKeyJustPressed(ebiten.KeyArrowRight):
		s.releaseKeys()
		a.cycleTab(1)
		return true
	case inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft):
		s.releaseKeys()
		a.cycleTab(-1)
		return true
	case inpututil.IsKeyJustPressed(ebiten.KeyF):
		a.setFullscreen(!a.fullscreen)
	case inpututil.IsKeyJustPressed(ebiten.KeyZ):
		a.toggleMode(s)
	case inpututil.IsKeyJustPressed(ebiten.KeyR):
		// released below
	case inpututil.IsKeyJustPressed(ebiten.KeyT):
		if s.canText() {
			if t.textOpen {
				a.closeText(t)
			} else {
				a.openText(t)
			}
		}
	case inpututil.IsKeyJustPressed(ebiten.KeyI):
		t.stats = !t.stats
		a.ui.Invalidate()
	case inpututil.IsKeyJustPressed(ebiten.KeyB):
		a.showBar(t)
	default:
		used = false
		for i := 0; i < 9; i++ { // Ctrl+Alt+1..9: the tabs
			if inpututil.IsKeyJustPressed(ebiten.KeyDigit1 + ebiten.Key(i)) {
				if i+1 <= len(a.tabs) {
					s.releaseKeys()
					a.setActive(i + 1)
					return true
				}
				if i == 0 {
					s.releaseKeys()
					a.setActive(0)
					return true
				}
			}
		}
	}
	if used {
		s.releaseKeys() // the Ctrl and Alt that were already sent must not stay down
	}
	return used
}

// drawLive draws the picture of the tab on show, if there is one.
func (a *App) drawLive(screen *ebiten.Image) {
	if _, s, dim := a.pictureTab(); s != nil {
		if a.scaler == nil && !a.scalerFailed {
			sc, err := gfx.NewScaler()
			if err != nil {
				log.Printf("the sharp scaling shader did not compile, so pictures are scaled bilinearly: %v", err)
			}
			a.scaler, a.scalerFailed = sc, err != nil
		}
		s.draw(screen, a.viewportPx(), dim, a.scaler, a.scaleFilter())
	}
}

// sizeSettle is how long the window must keep its size before the host is asked for a
// virtual display of it: dragging the window's edge must not remake the display at every
// step.
const sizeSettle = 500 * time.Millisecond

// syncViewSize keeps the host's virtual display at the size of the window for the session on
// show, if it wants one, and acts on what the host says: the picture is shown at its own
// size while it is on a virtual display (it was made to fit), and a failure is reported.
func (a *App) syncViewSize(s *session) {
	vc, ok := s.cl.(vdConn)
	if !ok || !vc.Supports(proto.CapVirtualDisplay) {
		return
	}
	if !s.matchSet {
		s.match, s.matchSet = a.matchResDefault(), true
	}
	w, h := 0, 0
	if s.match {
		vp := a.viewportPx()
		w, h = vp.W, vp.H
	}
	if w != s.wantW || h != s.wantH {
		s.wantW, s.wantH, s.wantSince = w, h, a.now
	}
	if w != s.sentW || h != s.sentH {
		// Back to the host's own screen at once; a new size once it has stayed put.
		if due := s.wantSince.Add(sizeSettle); w > 0 && a.now.Before(due) {
			a.wakeAtTime(due)
		} else if err := vc.SendViewSize(w, h); err != nil {
			log.Printf("asking for a virtual display: %v", err)
		} else {
			s.sentW, s.sentH = w, h
		}
	}
	st, ok := vc.DisplayState()
	if !ok || st == s.vstate {
		return
	}
	was := s.vstate.Virtual
	s.vstate = st
	switch {
	case st.Virtual && !was:
		s.modeBefore, s.mode = s.mode, view.ModeActual
		a.Toast(a.tr.T(i18n.KeyVDOn, "w", int(st.W), "h", int(st.H)), kit.ToneGood)
	case !st.Virtual && was:
		s.mode = s.modeBefore
	}
	if st.Message != "" {
		a.Toast(a.tr.T(i18n.KeyVDFailed, "reason", st.Message), kit.ToneWarn)
		if !st.Virtual {
			s.match = false // it did not work: the button shows it is off
		}
	}
	a.ui.Invalidate()
}

// Settings key for how the picture is scaled to the window: "sharp" (the default), "smooth"
// or "pixel".
const keyScaling = "view_scaling"

var scalingNames = []string{"sharp", "smooth", "pixel"} // in the order of gfx.ScaleFilter

// scaleFilter is the resampling the person chose for the remote picture.
func (a *App) scaleFilter() gfx.ScaleFilter {
	v, _ := a.cfg.Settings.Get(keyScaling)
	for i, n := range scalingNames {
		if v == n {
			return gfx.ScaleFilter(i)
		}
	}
	return gfx.ScaleSharp
}

// Fullscreen reports whether the window should be full screen; the game applies it.
func (a *App) Fullscreen() bool { return a.fullscreen }

// syncFullscreen leaves full screen when there is no picture to have it: the pages are not
// made for it.
func (a *App) syncFullscreen() {
	if t, _, _ := a.pictureTab(); t == nil && a.fullscreen {
		a.fullscreen = false
		a.ui.Invalidate()
	}
}

// WindowTitle is the title of the window: the name of the program, and of the tab on show
// with any trouble its session has.
func (a *App) WindowTitle() string {
	title := a.tr.T(i18n.KeyAppName)
	t := a.tabAt(a.active)
	if t == nil {
		return title
	}
	title += " - " + t.title
	if t.sess != nil {
		stale, failed := t.sess.problems()
		switch {
		case stale > 0:
			title += " - " + a.tr.Count(i18n.KeySessStaleOne, i18n.KeySessStaleOther, stale)
		case failed > 0:
			title += " - " + a.tr.Count(i18n.KeySessFailedOne, i18n.KeySessFailedOther, failed)
		}
	}
	return title
}

// WakeIn asks for a frame after d, though nothing will have happened by then.
func (a *App) WakeIn(d time.Duration) {
	at := a.now.Add(d)
	if a.wakeAt.IsZero() || at.Before(a.wakeAt) {
		a.wakeAt = at
	}
}

func logf(format string, args ...any) { log.Printf(format, args...) }
