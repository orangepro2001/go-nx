package app

import (
	"log"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

// viewportPx is where the remote picture goes, in device pixels.
func (a *App) viewportPx() rect {
	k := a.scale
	return rect{int(a.viewRect.X*k + 0.5), int(a.viewRect.Y*k + 0.5), int(a.viewRect.W*k + 0.5), int(a.viewRect.H*k + 0.5)}
}

// keysToUI reports whether the keyboard belongs to the interface. In a tab showing a
// picture it belongs to the remote PC, apart from the shortcuts below and unless a dialog
// is open.
func (a *App) keysToUI() bool { return a.liveTab() == nil || a.dialog != nil }

// StepLive is the part of a frame that needs the GPU and the real input devices: the
// picture of the tab on show is brought up to date and, if the keyboard and the pointer
// belong to it, what the person does is sent to the remote PC. fit sizes the window to
// the picture the first time one arrives.
func (a *App) StepLive(fit func(fw, fh int)) {
	t := a.liveTab()
	if t == nil {
		return
	}
	forward := a.dialog == nil
	// These shortcuts are never forwarded to the remote PC.
	if forward && ebiten.IsKeyPressed(ebiten.KeyControl) && ebiten.IsKeyPressed(ebiten.KeyAlt) {
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
	t.sess.update(a.viewportPx(), forward, fit)
	if t.sess.holding() {
		a.WakeIn(16 * time.Millisecond) // keep sending: a held key repeats, a held button stays alive
	}
}

// drawLive draws the picture of the tab on show, if there is one.
func (a *App) drawLive(screen *ebiten.Image) {
	if t := a.liveTab(); t != nil {
		t.sess.draw(screen, a.viewportPx())
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
