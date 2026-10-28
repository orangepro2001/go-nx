//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/ui"
	"github.com/orangepro2001/go-nx/internal/view"
)

const (
	repeatDelay    = 30 // ticks a key is held before auto-repeat starts
	repeatEvery    = 3
	receiptWarnAt  = 3 * time.Second
	keepAliveEvery = 5 * time.Second // a held button with a still pointer says it is alive this often
)

// session is one outgoing connection to another go-nx.
type session struct {
	cl *remote.Client

	mu      sync.Mutex
	rgba    []byte // full-frame RGBA, kept only for whole-frame uploads
	fw, fh  int
	full    bool                 // upload the whole of rgba next
	patches []patch              // changed tiles to upload after that, in order
	pending map[uint64]time.Time // actions awaiting a receipt
	failed  int
	err     error // set when the connection ends
	done    chan struct{}

	img          *ebiten.Image
	sized        bool        // window already fitted to the first frame
	visible      atomic.Bool // this session's tab is the one on screen
	lastX, lastY int32
	held         map[uint16]bool
	down         map[int32]bool // mouse buttons pressed on the remote
	nextOp       *uint64
	lastSent     time.Time // when anything was last sent, for the keep-alive

	// inputMu guards held, down, lastX/lastY and lastSent. The window goroutine
	// uses them every tick; quitting closes sessions from the tray's goroutine.
	inputMu sync.Mutex
}

// liveSessions is every session that has not been closed, so that quitting can
// close them all whichever goroutine asks.
var liveSessions sync.Map

// closeAllSessions lets go of everything held on every remote and closes the
// connections, so each host releases at once instead of waiting out its timers,
// and gives the close frames a moment to leave before the process exits.
func closeAllSessions() {
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

func newSession(cl *remote.Client, nextOp *uint64) *session {
	s := &session{cl: cl, pending: map[uint64]time.Time{}, held: map[uint16]bool{}, down: map[int32]bool{}, done: make(chan struct{}), nextOp: nextOp}
	liveSessions.Store(s, struct{}{})
	go func() {
		err := cl.Run(context.Background(), s.onFrame, s.onReceipt)
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	}()
	return s
}

// onFrame converts only what changed. When most of the screen changed (first
// frame, page switch) one whole-frame upload is cheaper than many small ones.
// A session whose tab is not showing keeps no patches, so nothing piles up while
// it is in the background: its next visit uploads the whole frame once.
func (s *session) onFrame(f *tile.Frame, dirty []tile.Rect) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	defer s.mu.Unlock()
	delete(s.pending, id)
	if status != proto.StatusExecuted {
		s.failed++
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

// status describes receipts that look wrong, for the status line.
func (s *session) status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	stale := 0
	for _, t := range s.pending {
		if time.Since(t) > receiptWarnAt {
			stale++
		}
	}
	switch {
	case stale > 0:
		return fmt.Sprintf("%d action(s) unconfirmed - check the screen before repeating", stale)
	case s.failed > 0:
		return fmt.Sprintf("%d action(s) failed on the host", s.failed)
	}
	return ""
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

// update uploads new frames and, if input is true, forwards mouse and keyboard
// input. vp is the area of the window the remote screen is shown in.
func (s *session) update(vp ui.Rect, input bool) {
	fw, fh := s.upload()
	if fw == 0 || vp.W == 0 {
		return
	}
	if !s.sized {
		s.sized = true
		fitWindow(fw, fh+ui.BarHeight)
	}
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if !input || !ebiten.IsFocused() { // never leave keys stuck down on the host
		s.releaseAllLocked()
		return
	}

	cx, cy := ebiten.CursorPosition()
	x, y, inside := view.ToHost(cx, cy, vp.X, vp.Y, vp.W, vp.H, fw, fh)
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
	if dx, dy := ebiten.Wheel(); inside {
		if dy != 0 {
			s.action(proto.Input{Kind: proto.KindWheel, X: x, Y: y, Arg: int32(dy * 120)})
		}
		if dx != 0 { // tilt wheels and touchpads; a host that does not know it just answers Failed
			s.action(proto.Input{Kind: proto.KindWheelH, X: x, Y: y, Arg: int32(dx * 120)})
		}
	}
	// Collect what happened to the keys this tick and send it in keyboard order.
	var events []view.KeyEvent
	for _, k := range vkKeys {
		code := vk[k]
		switch {
		case inpututil.IsKeyJustPressed(k):
			events = append(events, view.KeyEvent{Code: code, Down: true})
		case inpututil.IsKeyJustReleased(k):
			events = append(events, view.KeyEvent{Code: code, Down: false})
		case ebiten.IsKeyPressed(k):
			if d := inpututil.KeyPressDuration(k); d > repeatDelay && d%repeatEvery == 0 {
				s.send(proto.Input{Kind: proto.KindKeyDown, Arg: int32(code)}) // auto-repeat
			}
		}
	}
	view.OrderKeyEvents(events)
	for _, e := range events {
		if e.Down {
			s.held[e.Code] = true
			s.action(proto.Input{Kind: proto.KindKeyDown, Arg: int32(e.Code)})
		} else {
			delete(s.held, e.Code)
			s.action(proto.Input{Kind: proto.KindKeyUp, Arg: int32(e.Code)})
		}
	}
}

func (s *session) draw(screen *ebiten.Image, vp ui.Rect) {
	if s.img == nil {
		return
	}
	scale, ox, oy := view.Fit(vp.W, vp.H, s.fw, s.fh)
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(scale, scale)
	op.GeoM.Translate(float64(vp.X)+ox, float64(vp.Y)+oy)
	// Shrinking with nearest-neighbour drops pixel rows and columns and breaks
	// small text, so only enlarge with it.
	op.Filter = ebiten.FilterLinear
	if scale >= 1 {
		op.Filter = ebiten.FilterNearest
	}
	screen.DrawImage(s.img, op)
}

// fitWindow sizes the window to show the frame 1:1 if it fits on the monitor,
// otherwise as large as fits, keeping the frame's aspect ratio.
func fitWindow(fw, fh int) {
	m := ebiten.Monitor()
	k := m.DeviceScaleFactor()
	mw, mh := m.Size() // device-independent pixels
	scale := min(1, float64(mw)*0.95/(float64(fw)/k), float64(mh)*0.9/(float64(fh)/k))
	ebiten.SetWindowSize(int(float64(fw)/k*scale), int(float64(fh)/k*scale))
}

// hostChangedMsg explains a changed host certificate and the one deliberate way out.
const hostChangedMsg = "The host's identity changed (new install, or another PC at this address). " +
	"If you expect that, press Ctrl+F to forget the saved identity of this host, then Enter to connect and pin the new one. " +
	"If you do not expect it, do not connect."

// describe turns a connection error into something the user can act on.
func describe(err error) string {
	var ae *quic.ApplicationError
	if errors.As(err, &ae) {
		switch ae.ErrorCode {
		case remote.CloseUntrusted:
			return "This PC is not paired with the host. On the host click \"Pair new device\", then connect again with the 6-digit code it shows as the Password (or enter the host's password)"
		case remote.CloseBadPassword:
			return "Wrong password or pairing code"
		case remote.CloseLocked:
			return "Too many wrong passwords; try again in a minute"
		case remote.CloseReplaced:
			return "Another client took over this host"
		case remote.CloseUnverified:
			return "The host switched to strict mode and this PC is not verified. Pair it (the host's \"Pair new device\" shows a code) or enter the host's password, then connect again"
		case remote.CloseBusy:
			return "The host is busy with other connection attempts; try again in a moment"
		case remote.CloseRevoked:
			return "This PC was removed from the host's paired devices. Pair it again, or enter the host's password"
		}
	}
	if errors.Is(err, errHostNotConfirmed) {
		return "You did not confirm this host's identity, so nothing was sent to it"
	}
	if strings.Contains(err.Error(), remote.ErrHostChanged.Error()) {
		return hostChangedMsg
	}
	var idle *quic.IdleTimeoutError
	if errors.As(err, &idle) {
		return "Connection timed out (is the address right and UDP open?)"
	}
	return err.Error()
}
