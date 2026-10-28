package app

import (
	"context"
	"image"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/ui/gfx"
	"github.com/orangepro2001/go-nx/internal/view"
)

const (
	receiptWarnAt  = 3 * time.Second
	keepAliveEvery = 5 * time.Second // a held button with a still pointer says it is alive this often
)

// session is one outgoing connection to another go-nx.
type session struct {
	cl Conn

	mu        sync.Mutex
	rgba      []byte // full-frame RGBA, kept only for whole-frame uploads
	fw, fh    int
	full      bool                 // upload the whole of rgba next
	patches   []patch              // changed tiles to upload after that, in order
	pending   map[uint64]time.Time // actions awaiting a receipt
	failed    int
	frames    []time.Time // when the pictures of the last second arrived
	statAt    time.Time
	statBytes uint64
	rate      float64
	err       error // set when the connection ends
	done      chan struct{}

	img          *ebiten.Image
	sized        bool        // window already fitted to the first frame
	visible      atomic.Bool // this session's tab is the one on screen
	lastX, lastY int32
	held         map[uint16]bool
	nextRepeat   map[uint16]time.Time // when each held key repeats next
	mode         view.Mode            // how the picture is shown
	panX, panY   float64              // how far into the picture the window's left and top edges are (actual size)
	lastPan      time.Time
	panning      bool
	down         map[int32]bool // mouse buttons pressed on the remote
	nextOp       *uint64
	changed      func()    // something the interface shows has changed: a picture arrived, the session ended
	lastSent     time.Time // when anything was last sent, for the keep-alive

	// inputMu guards held, down, lastX/lastY and lastSent. The window goroutine
	// uses them every tick; quitting closes sessions from the tray's goroutine.
	inputMu sync.Mutex
}

// liveSessions is every session that has not been closed, so that quitting can
// close them all whichever goroutine asks.
var liveSessions sync.Map

// CloseAllSessions lets go of everything held on every remote and closes the
// connections, so each host releases at once instead of waiting out its timers,
// and gives the close frames a moment to leave before the process exits.
func CloseAllSessions() {
	n := 0
	liveSessions.Range(func(k, _ any) bool {
		k.(*session).close()
		n++
		return true
	})
	if n > 0 {
		time.Sleep(150 * time.Millisecond)
	}
}

// patch is one changed rectangle, already converted to RGBA.
type patch struct {
	r   tile.Rect
	pix []byte
}

func newSession(cl Conn, nextOp *uint64, changed func()) *session {
	s := &session{cl: cl, pending: map[uint64]time.Time{}, held: map[uint16]bool{}, nextRepeat: map[uint16]time.Time{}, down: map[int32]bool{}, done: make(chan struct{}), nextOp: nextOp, changed: changed}
	liveSessions.Store(s, struct{}{})
	go func() {
		err := cl.Run(context.Background(), s.onFrame, s.onReceipt)
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
		s.notify()
	}()
	return s
}

// onFrame converts only what changed. When most of the screen changed (first
// frame, page switch) one whole-frame upload is cheaper than many small ones.
// A session whose tab is not showing keeps no patches, so nothing piles up while
// it is in the background: its next visit uploads the whole frame once.
func (s *session) onFrame(f *tile.Frame, dirty []tile.Rect) {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.frames = append(s.frames, now)
	s.trimFrames(now)
	if s.fw != f.W || s.fh != f.H || len(dirty)*4 > f.Cols()*f.Rows() || !s.visible.Load() {
		if s.fw != f.W || s.fh != f.H {
			s.fw, s.fh = f.W, f.H
			s.rgba = make([]byte, f.W*f.H*4)
		}
		view.BGRAToRGBA(s.rgba, f.Pix)
		s.full = true
		s.patches = s.patches[:0] // the whole frame supersedes earlier patches
		return
	}
	for _, r := range dirty {
		pix := make([]byte, r.W*r.H*4)
		for y := 0; y < r.H; y++ {
			o := ((r.Y+y)*f.W + r.X) * 4
			view.BGRAToRGBA(pix[y*r.W*4:(y+1)*r.W*4], f.Pix[o:o+r.W*4])
		}
		s.patches = append(s.patches, patch{r, pix})
	}
}

func (s *session) onReceipt(id uint64, status byte) {
	s.mu.Lock()
	delete(s.pending, id)
	if status != proto.StatusExecuted {
		s.failed++
	}
	s.mu.Unlock()
	s.notify()
}

// notify tells the interface that something it shows has changed.
func (s *session) notify() {
	if s.changed != nil {
		s.changed()
	}
}

// action sends an input that needs a receipt.
func (s *session) action(in proto.Input) {
	*s.nextOp++
	in.OpID = *s.nextOp
	s.mu.Lock()
	s.pending[in.OpID] = time.Now()
	s.mu.Unlock()
	s.send(in)
}

func (s *session) send(in proto.Input) {
	s.lastSent = time.Now()
	if err := s.cl.SendInput(in); err != nil {
		log.Printf("send input: %v", err) // Run reports the broken connection
	}
}

// releaseAll lets go of every key and mouse button this session holds down on
// the remote, so nothing stays stuck when the tab is left or the window loses focus.
func (s *session) releaseAll() {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	s.releaseAllLocked()
}

// releaseAllLocked is releaseAll for a caller that holds inputMu.
func (s *session) releaseAllLocked() {
	// Buttons first, then keys with the modifiers last, in an order that does not
	// depend on how a map happens to iterate.
	for _, b := range []int32{0, 1, 2} {
		if s.down[b] {
			s.send(proto.Input{Kind: proto.KindMouseUp, X: s.lastX, Y: s.lastY, Arg: b})
			delete(s.down, b)
		}
	}
	codes := make([]uint16, 0, len(s.held))
	for k := range s.held {
		codes = append(codes, k)
	}
	for _, k := range view.ReleaseOrder(codes) {
		s.send(proto.Input{Kind: proto.KindKeyUp, Arg: int32(k)})
		delete(s.held, k)
		delete(s.nextRepeat, k)
	}
}

// setVisible marks whether this session's tab is showing; leaving it releases input.
func (s *session) setVisible(v bool) {
	if s.visible.Swap(v) && !v {
		s.releaseAll()
	}
}

func (s *session) close() {
	liveSessions.Delete(s)
	s.releaseAll()
	s.cl.Close()
}

// problems counts actions that look wrong: stale ones have had no receipt for a while,
// failed ones were refused by the host.
func (s *session) problems() (stale, failed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.pending {
		if time.Since(t) > receiptWarnAt {
			stale++
		}
	}
	return stale, s.failed
}

// upload sends pending frame changes to the GPU and reports the frame size.
func (s *session) upload() (fw, fh int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		if s.img == nil || s.img.Bounds().Dx() != s.fw || s.img.Bounds().Dy() != s.fh {
			s.img = ebiten.NewImage(s.fw, s.fh)
		}
		s.img.WritePixels(s.rgba)
		s.full = false
	}
	for _, p := range s.patches {
		r := image.Rect(p.r.X, p.r.Y, p.r.X+p.r.W, p.r.Y+p.r.H)
		s.img.SubImage(r).(*ebiten.Image).WritePixels(p.pix)
	}
	s.patches = s.patches[:0]
	return s.fw, s.fh
}

// gate says which of the person's input belongs to the remote PC right now. The pointer
// does not while it is over the interface (the toolbar), the keyboard does not while the
// interface has it (the box for sending text).
type gate struct{ keys, pointer bool }

const (
	// Keys held down on the remote repeat after typeDelay and then every typeEvery, the
	// way a keyboard does, by the clock: frames come when something happens, so counting
	// them would make a mouse moved with a key held repeat the key much too fast.
	typeDelay = 500 * time.Millisecond
	typeEvery = 33 * time.Millisecond

	panMargin = 40.0   // device pixels from the edge in which the picture pans
	panSpeed  = 1200.0 // device pixels a second at the very edge
)

// update uploads new frames and forwards what the person does: the pointer, the wheel and
// the keyboard, as far as g lets them through. vp is the part of the window the picture
// is in. window is told the size of the first picture, to fit the window to it.
func (s *session) update(vp rect, g gate, window func(fw, fh int)) {
	fw, fh := s.upload()
	if fw == 0 || vp.W == 0 {
		return
	}
	if !s.sized {
		s.sized = true
		window(fw, fh)
	}
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	now := time.Now()
	if !ebiten.IsFocused() { // never leave keys stuck down on the host
		s.releaseAllLocked()
		return
	}
	if !g.keys {
		s.releaseKeysLocked()
	}

	cx, cy := ebiten.CursorPosition()
	px, py := float64(cx-vp.X), float64(cy-vp.Y)
	over := cx >= vp.X && cx < vp.X+vp.W && cy >= vp.Y && cy < vp.Y+vp.H

	// A picture shown at its own size and bigger than the window is panned by the pointer
	// near its edges.
	s.panning = false
	if s.mode == view.ModeActual {
		maxX, maxY := view.MaxPan(vp.W, vp.H, fw, fh)
		if over && g.pointer && (maxX > 0 || maxY > 0) {
			dt := min(now.Sub(s.lastPan), 50*time.Millisecond)
			if !s.lastPan.IsZero() {
				dx, dy := view.EdgePan(px, py, vp.W, vp.H, panMargin, panSpeed, dt, maxX > 0, maxY > 0)
				s.panX, s.panY = min(max(s.panX+dx, 0), maxX), min(max(s.panY+dy, 0), maxY)
				s.panning = dx != 0 || dy != 0
			}
			s.lastPan = now
		} else {
			s.lastPan = time.Time{}
		}
		s.panX, s.panY = min(max(s.panX, 0), maxX), min(max(s.panY, 0), maxY)
	}
	pl := view.Place(s.mode, vp.W, vp.H, fw, fh, s.panX, s.panY)
	hx, hy := pl.ToHostAt(px, py, fw, fh)
	x, y := hx, hy
	inside := over && g.pointer

	if g.pointer || len(s.down) > 0 {
		// While a button is held the pointer is followed even outside the picture, to
		// its edge: otherwise a drag that leaves the window ends with the host's
		// pointer wherever it last was inside, and the release then jumps it.
		if (inside || len(s.down) > 0) && (x != s.lastX || y != s.lastY) {
			s.lastX, s.lastY = x, y
			s.send(proto.Input{Kind: proto.KindMouseMove, X: x, Y: y})
		}
		// A button that has been held a while with the pointer still sends nothing; say
		// we are alive, or the host lets go of it (it cannot tell a still viewer from a
		// vanished one).
		if len(s.down) > 0 && time.Since(s.lastSent) > keepAliveEvery {
			s.send(proto.Input{Kind: proto.KindMouseMove, X: s.lastX, Y: s.lastY})
		}
		for _, b := range []struct {
			btn ebiten.MouseButton
			id  int32
		}{{ebiten.MouseButtonLeft, 0}, {ebiten.MouseButtonRight, 1}, {ebiten.MouseButtonMiddle, 2}} {
			if inside && inpututil.IsMouseButtonJustPressed(b.btn) {
				s.down[b.id] = true
				s.action(proto.Input{Kind: proto.KindMouseDown, X: x, Y: y, Arg: b.id})
			}
			// A release always goes through, even outside the picture, for a button we pressed.
			if s.down[b.id] && inpututil.IsMouseButtonJustReleased(b.btn) {
				delete(s.down, b.id)
				s.action(proto.Input{Kind: proto.KindMouseUp, X: x, Y: y, Arg: b.id})
			}
		}
	}
	if dx, dy := ebiten.Wheel(); inside {
		if dy != 0 {
			s.action(proto.Input{Kind: proto.KindWheel, X: x, Y: y, Arg: int32(dy * 120)})
		}
		if dx != 0 { // tilt wheels and touchpads; a host that does not know it just answers Failed
			s.action(proto.Input{Kind: proto.KindWheelH, X: x, Y: y, Arg: int32(dx * 120)})
		}
	}
	if !g.keys {
		return
	}
	// Collect what happened to the keys this frame and send it in keyboard order.
	var events []view.KeyEvent
	for _, k := range vkKeys {
		code := vk[k]
		switch {
		case inpututil.IsKeyJustPressed(k):
			events = append(events, view.KeyEvent{Code: code, Down: true})
		case inpututil.IsKeyJustReleased(k):
			events = append(events, view.KeyEvent{Code: code, Down: false})
		}
	}
	view.OrderKeyEvents(events)
	for _, e := range events {
		if e.Down {
			s.held[e.Code] = true
			s.nextRepeat[e.Code] = now.Add(typeDelay)
			s.action(proto.Input{Kind: proto.KindKeyDown, Arg: int32(e.Code)})
		} else {
			delete(s.held, e.Code)
			delete(s.nextRepeat, e.Code)
			s.action(proto.Input{Kind: proto.KindKeyUp, Arg: int32(e.Code)})
		}
	}
	// Auto-repeat of what is still held (not the modifiers: holding Shift is not typing).
	for code, due := range s.nextRepeat {
		if view.IsModifier(code) || now.Before(due) {
			continue
		}
		s.send(proto.Input{Kind: proto.KindKeyDown, Arg: int32(code)})
		s.nextRepeat[code] = now.Add(typeEvery)
	}
}

// releaseKeysLocked lets go of the keys (not the buttons) held on the remote.
func (s *session) releaseKeysLocked() {
	codes := make([]uint16, 0, len(s.held))
	for k := range s.held {
		codes = append(codes, k)
	}
	for _, k := range view.ReleaseOrder(codes) {
		s.send(proto.Input{Kind: proto.KindKeyUp, Arg: int32(k)})
		delete(s.held, k)
		delete(s.nextRepeat, k)
	}
}

// draw puts the picture in the viewport, resampled with filter by sc. dim darkens it (a
// connection that is being brought back).
func (s *session) draw(screen *ebiten.Image, vp rect, dim bool, sc *gfx.Scaler, filter gfx.ScaleFilter) {
	if s.img == nil {
		return
	}
	pl := view.Place(s.mode, vp.W, vp.H, s.fw, s.fh, s.panX, s.panY)
	d := float32(1)
	if dim {
		d = 0.45
	}
	// The picture is clipped to the viewport, which matters when it is shown at its own
	// size and is bigger than the window (and not to draw over the tab strip).
	sc.Draw(screen, s.img, float64(vp.X)+pl.OX, float64(vp.Y)+pl.OY, pl.Scale, image.Rect(vp.X, vp.Y, vp.X+vp.W, vp.Y+vp.H), filter, d)
}

// rect is a rectangle in device pixels.
type rect struct{ X, Y, W, H int }

func (r rect) Contains(x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

// holding reports whether a key or a mouse button is held down on the remote PC, so that
// the interface must keep running to repeat the key and keep the button alive.
func (s *session) holding() bool {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	return len(s.held) > 0 || len(s.down) > 0
}

// modifiers reports which of Ctrl, Alt, Shift and the Windows key are held down on the remote.
func (s *session) modifiers() (ctrl, alt, shift, win bool) {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	for code := range s.held {
		switch code & 0xFF {
		case 0x11, 0xA2, 0xA3:
			ctrl = true
		case 0x12, 0xA4, 0xA5:
			alt = true
		case 0x10, 0xA0, 0xA1:
			shift = true
		case 0x5B, 0x5C:
			win = true
		}
	}
	return
}

// releaseKeys lets go of every key held on the remote (the toolbar's button for a key that
// seems stuck), leaving the mouse buttons alone.
func (s *session) releaseKeys() {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	s.releaseKeysLocked()
}

// pressKey taps a key on the remote: the Korean mode switch, say, which Ebiten cannot see.
func (s *session) pressKey(code uint16) {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	s.action(proto.Input{Kind: proto.KindKeyDown, Arg: int32(code)})
	s.action(proto.Input{Kind: proto.KindKeyUp, Arg: int32(code)})
}

// The optional abilities of a connection. *remote.Client has all of them; a connection that
// lacks one simply does not offer the control that needs it.
type (
	textConn interface {
		Supports(proto.Caps) bool
		SendText(opID uint64, text string) error
	}
	statsConn interface{ Stats() remote.Stats }
	hostConn  interface {
		Host() (remote.HostInfo, bool)
	}
)

// canText reports whether the host can type text sent to it, which only a host that said so
// is asked to (an older one ends the session when it meets a message it does not know).
func (s *session) canText() bool {
	tc, ok := s.cl.(textConn)
	return ok && tc.Supports(proto.CapText)
}

// sendText has the host type text, as Unicode, whatever its keyboard layout or input method.
func (s *session) sendText(text string) {
	tc, ok := s.cl.(textConn)
	if !ok || text == "" {
		return
	}
	*s.nextOp++
	id := *s.nextOp
	s.mu.Lock()
	s.pending[id] = time.Now()
	s.mu.Unlock()
	if err := tc.SendText(id, text); err != nil {
		log.Printf("send text: %v", err)
	}
}

// hostName is the name the host gave itself, if it did.
func (s *session) hostName() string {
	if hc, ok := s.cl.(hostConn); ok {
		if h, ok := hc.Host(); ok {
			return h.Name
		}
	}
	return ""
}

// fps is how many pictures arrived in the last second.
func (s *session) fps() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trimFrames(time.Now())
	return len(s.frames)
}

func (s *session) trimFrames(now time.Time) {
	i := 0
	for i < len(s.frames) && now.Sub(s.frames[i]) > time.Second {
		i++
	}
	s.frames = s.frames[i:]
}

// link is the round-trip time and the rate the host's data has been arriving at (bytes a
// second since the last time it was asked), for the statistics.
func (s *session) link() (rtt time.Duration, bytesPerSec float64, ok bool) {
	sc, has := s.cl.(statsConn)
	if !has {
		return 0, 0, false
	}
	st := sc.Stats()
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.statAt.IsZero() && now.Sub(s.statAt) >= 400*time.Millisecond {
		s.rate = float64(st.BytesReceived-s.statBytes) / now.Sub(s.statAt).Seconds()
		s.statAt, s.statBytes = now, st.BytesReceived
	} else if s.statAt.IsZero() {
		s.statAt, s.statBytes = now, st.BytesReceived
	}
	return st.RTT, s.rate, true
}

// hasPicture reports whether a picture has arrived from the host.
func (s *session) hasPicture() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fw > 0
}
