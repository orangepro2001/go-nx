package app

import (
	"image"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/orangepro2001/go-nx/internal/ui/gfx"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
)

// Game is the application as an Ebiten game: it gathers the input of a frame, steps the
// application, and draws it — the interface into a cached layer that is painted again only
// when the tree or an animation changed, and the remote picture under it.
//
// It runs in "minimum" mode: a frame happens when there is input or when something asks for
// one (a picture arriving, a notice expiring, a caret blinking), so an idle window costs
// nothing (docs/ui-baseline.md).
type Game struct {
	app *App
	eng *text.Engine
	cv  *gfx.Canvas

	// BeforeUpdate, if set, runs at the start of every Update; the Windows program uses it to
	// find the window and hide it to the tray.
	BeforeUpdate func()

	pw, ph int
	scale  float64
	title  string
	cursor ebiten.CursorShapeType

	layer      *ebiten.Image
	layerVer   uint64
	layerSize  image.Point
	layerScale float64
	animating  bool
	paintDue   time.Time

	keys      keyRepeater
	lastChars string // plain characters typed in the previous frame
	ime       *ime
	imeNext   time.Time
	lastTime  time.Time

	t0        time.Time
	sawFrame  atomic.Bool
	sawDraw   bool
	everReady bool

	mu      sync.Mutex
	timer   *time.Timer
	timerAt time.Time
}

// NewGame makes the game for app, drawing text with eng. The fonts' loading, which happens in
// the background, asks for a frame when it is done.
func NewGame(app *App, eng *text.Engine) (*Game, error) {
	cv, err := gfx.New(eng)
	if err != nil {
		return nil, err
	}
	g := &Game{app: app, eng: eng, cv: cv, ime: newIME(), scale: 1, t0: time.Now(), cursor: ebiten.CursorShapeDefault}
	eng.OnLoad(func() { app.Invalidate() })
	app.StartPolling(nil)
	ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum)
	g.kickFirstFrame()
	return g, nil
}

// kickFirstFrame asks for frames until the first one has run. In "minimum" mode a frame
// happens only when something asks for it, and a request made before the window exists is
// forgotten (the fonts, opened in the background, ask as soon as they are done, which is
// before the window is up): on Windows, where nothing else stirs at start-up, the window
// was never drawn at all. This was found by starting the real program on the CI runner.
func (g *Game) kickFirstFrame() {
	go func() {
		for !g.sawFrame.Load() {
			ebiten.ScheduleFrame()
			time.Sleep(25 * time.Millisecond)
		}
	}()
}

// Layout works in device pixels: returning the logical size instead would make Windows
// enlarge the finished picture on scaled displays, blurring small text.
func (g *Game) Layout(ow, oh int) (int, int) {
	g.scale = ebiten.Monitor().DeviceScaleFactor()
	g.pw, g.ph = int(float64(ow)*g.scale+0.5), int(float64(oh)*g.scale+0.5)
	return g.pw, g.ph
}

var mouseButtons = [3]ebiten.MouseButton{ebiten.MouseButtonLeft, ebiten.MouseButtonRight, ebiten.MouseButtonMiddle}

var keyMap = []struct {
	e ebiten.Key
	k kit.Key
}{
	{ebiten.KeyTab, kit.KeyTab}, {ebiten.KeyEnter, kit.KeyEnter}, {ebiten.KeyNumpadEnter, kit.KeyEnter},
	{ebiten.KeyEscape, kit.KeyEscape}, {ebiten.KeyBackspace, kit.KeyBackspace}, {ebiten.KeyDelete, kit.KeyDelete},
	{ebiten.KeyArrowLeft, kit.KeyLeft}, {ebiten.KeyArrowRight, kit.KeyRight}, {ebiten.KeyArrowUp, kit.KeyUp}, {ebiten.KeyArrowDown, kit.KeyDown},
	{ebiten.KeyHome, kit.KeyHome}, {ebiten.KeyEnd, kit.KeyEnd}, {ebiten.KeyPageUp, kit.KeyPageUp}, {ebiten.KeyPageDown, kit.KeyPageDown},
	{ebiten.KeySpace, kit.KeySpace},
	{ebiten.KeyA, kit.KeyA}, {ebiten.KeyC, kit.KeyC}, {ebiten.KeyV, kit.KeyV}, {ebiten.KeyX, kit.KeyX}, {ebiten.KeyK, kit.KeyK},
}

// gather reads this frame's input into the toolkit's form.
func (g *Game) gather(now time.Time) kit.Input {
	k := g.scale
	cx, cy := ebiten.CursorPosition()
	in := kit.Input{
		Now: now, X: float64(cx) / k, Y: float64(cy) / k,
		Pointer: cx >= 0 && cy >= 0 && cx < g.pw && cy < g.ph,
		Focused: ebiten.IsFocused(),
	}
	for i, b := range mouseButtons {
		in.Down[i] = ebiten.IsMouseButtonPressed(b)
		in.Pressed[i] = inpututil.IsMouseButtonJustPressed(b)
		in.Released[i] = inpututil.IsMouseButtonJustReleased(b)
	}
	in.WheelX, in.WheelY = ebiten.Wheel()
	if !g.app.keysToUI() {
		g.ime.update(false, image.Rectangle{})
		g.keys = keyRepeater{}
		return in
	}

	caret, wanted := g.app.ui.IME()
	cr := image.Rect(int(caret.X*k), int(caret.Y*k), int((caret.X+caret.W)*k), int((caret.Y+caret.H)*k))
	composing, committed, handled := g.ime.update(wanted && in.Focused, cr)
	in.Composing = composing
	// Typed characters reach us two ways: as Ebiten's plain characters, and (while the input
	// method has a session) as text it commits. Take one: the commit if there is one; and a
	// commit that only repeats the characters of the frame before is the same keystroke
	// arriving late, not a second one. (Whether Windows ever delivers them a frame apart is
	// for ACCEPTANCE.md H29 to find out.)
	typed := ""
	switch {
	case committed != "":
		if committed != g.lastChars {
			typed = committed
		}
		g.lastChars = ""
	case composing == "" && !handled:
		typed = string(ebiten.AppendInputChars(nil))
		g.lastChars = typed
	default:
		g.lastChars = ""
	}
	in.Text = typed
	if inpututil.IsKeyJustPressed(ebiten.KeyF3) && g.app.dialog == nil {
		g.app.ShowPage(PageLogs)
		g.app.logJump = true
	}

	if !handled {
		var just, down []kit.Key
		seen := map[kit.Key]bool{}
		for _, m := range keyMap {
			if ebiten.IsKeyPressed(m.e) && !seen[m.k] {
				down = append(down, m.k)
			}
			if inpututil.IsKeyJustPressed(m.e) && !seen[m.k] {
				just = append(just, m.k)
				seen[m.k] = true
			}
		}
		events, next := g.keys.poll(now, just, down)
		g.imeNext = next
		ctrl := ebiten.IsKeyPressed(ebiten.KeyControl) || ebiten.IsKeyPressed(ebiten.KeyMeta)
		shift, alt := ebiten.IsKeyPressed(ebiten.KeyShift), ebiten.IsKeyPressed(ebiten.KeyAlt)
		for _, e := range events {
			in.Keys = append(in.Keys, kit.KeyPress{Key: e, Shift: shift, Ctrl: ctrl, Alt: alt})
		}
	}
	return in
}

// Update steps the application.
func (g *Game) Update() error {
	if !g.sawFrame.Swap(true) {
		log.Printf("startup +%dms first Update (window system and GPU ready)", time.Since(g.t0).Milliseconds())
	}
	if g.BeforeUpdate != nil {
		g.BeforeUpdate()
	}
	a := g.app
	if ebiten.IsWindowBeingClosed() {
		a.cfg.Platform.HideWindow() // sessions stay connected in the background
	}
	a.Resize(g.pw, g.ph, g.scale)
	now := time.Now()
	in := g.gather(now)
	a.Step(in)
	a.StepLive(g.fitWindow)
	if want := a.Fullscreen(); want != ebiten.IsFullscreen() {
		ebiten.SetFullscreen(want)
	}
	if !g.imeNext.IsZero() {
		a.WakeIn(g.imeNext.Sub(now))
		g.imeNext = time.Time{}
	}
	g.applyCursor()
	if t := a.WindowTitle(); t != g.title {
		g.title = t
		ebiten.SetWindowTitle(t)
	}
	return nil
}

func (g *Game) applyCursor() {
	want := ebiten.CursorShapeDefault
	switch g.app.ui.CurrentCursor() {
	case kit.CursorText:
		want = ebiten.CursorShapeText
	case kit.CursorPointer:
		want = ebiten.CursorShapePointer
	}
	if want != g.cursor {
		g.cursor = want
		ebiten.SetCursorShape(want)
	}
}

// Draw paints a frame.
func (g *Game) Draw(screen *ebiten.Image) {
	a := g.app
	if !g.eng.Ready() && !g.everReady { // the fonts are being opened; a frame will be asked for when they are
		screen.Fill(a.ui.Pal.BG)
		return
	}
	g.everReady = true // a later language change shows the old glyphs for a moment, not a blank window
	if !g.sawDraw {
		g.sawDraw = true
		log.Printf("startup +%dms first Draw", time.Since(g.t0).Milliseconds())
	}
	w, h := screen.Bounds().Dx(), screen.Bounds().Dy()
	size := image.Pt(w, h)
	now := time.Now()
	if g.layer == nil || size != g.layerSize {
		if g.layer != nil {
			g.layer.Deallocate()
		}
		g.layer, g.layerSize, g.layerVer = ebiten.NewImage(w, h), size, 0
	}
	if g.layerVer != a.TreeVersion()+1 || g.layerScale != g.scale || g.animating || (!g.paintDue.IsZero() && !now.Before(g.paintDue)) {
		g.layer.Clear()
		g.cv.Begin(g.layer, g.scale)
		a.ui.Paint(g.cv)
		g.layerVer, g.layerScale = a.TreeVersion()+1, g.scale
		g.animating, g.paintDue = a.ui.WantsFrame()
	}
	if pt, _, _ := a.pictureTab(); pt != nil {
		screen.Fill(kit.Color{A: 255})
		a.drawLive(screen)
	}
	screen.DrawImage(g.layer, nil)
	g.setWake(a.NextWake())
}

// setWake arranges for a frame at t (the zero time cancels). Frames are not asked for
// "now" in a loop: an animation or a held key wakes at 60 a second.
func (g *Game) setWake(t time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t.IsZero() {
		if g.timer != nil {
			g.timer.Stop()
		}
		g.timerAt = time.Time{}
		return
	}
	if !g.timerAt.IsZero() && g.timerAt.Equal(t) {
		return
	}
	d := max(time.Until(t), 0)
	if g.timer == nil {
		g.timer = time.AfterFunc(d, g.fire)
	} else {
		g.timer.Reset(d)
	}
	g.timerAt = t
}

func (g *Game) fire() {
	g.mu.Lock()
	g.timerAt = time.Time{}
	g.mu.Unlock()
	if !g.app.cfg.Platform.WindowHidden() {
		ebiten.ScheduleFrame()
	}
}

// fitWindow sizes the window to show the picture 1:1 if it fits on the monitor, otherwise
// as large as fits, keeping the picture's aspect ratio.
func (g *Game) fitWindow(fw, fh int) {
	m := ebiten.Monitor()
	k := m.DeviceScaleFactor()
	mw, mh := m.Size() // device-independent pixels
	chrome := g.app.viewRect.Y * k
	wantW, wantH := float64(fw)/k, (float64(fh)+chrome)/k
	scale := min(1, float64(mw)*0.95/wantW, float64(mh)*0.9/wantH)
	ebiten.SetWindowSize(int(wantW*scale), int(wantH*scale))
}
