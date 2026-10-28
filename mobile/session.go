// Package mobile exposes the go-nx controlling core to a phone through gomobile
// bind. It contains nothing specific to Android: the application owns the
// Activity, the drawing surface, the keyboard and the notification, and this
// package owns QUIC, certificate pinning, pairing, tile decoding and input
// encoding. docs/mobile.md describes the contract.
//
// The pixel contract. Every tile arrives as tightly packed B,G,R,X bytes, the
// order a Windows host captures in; the fourth byte is not defined and may be 0.
// An application that draws RGBA bitmaps swaps red and blue and sets the alpha to
// 255 itself (view.BGRAToRGBA is the reference).
//
// The position contract. A host is told where the pointer is with every pointer
// action, never "where it was": Move remembers the last position, and button and
// wheel actions carry it. Doing one of them before any Move is an error, because
// there is no position to act at (the other answer, the corner, is the bug where
// letting go of a button threw the host's pointer to the top-left).
package mobile

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// Session states passed to Callback.OnState.
const (
	StateConnecting = "connecting"
	StateStreaming  = "streaming"
	StateClosed     = "closed"
)

// Receipt statuses passed to Callback.OnReceipt.
const (
	ReceiptExecuted = int(proto.StatusExecuted)
	ReceiptFailed   = int(proto.StatusFailed)
)

// Error codes passed to Callback.OnError: untrusted, bad-code, replaced, locked,
// revoked, busy, unverified, host-identity, identity and disconnected.

// errNoPosition is returned for a button or wheel action before the first Move.
// It is not exported: gomobile would turn an exported variable into a writable
// static of the Java API.
var errNoPosition = errors.New("mobile: no pointer position yet: call Move first")

// Callback receives session events from Go worker goroutines. An Android
// implementation must hand UI work to the main thread and must never retain the
// memory passed to OnTile: pixels is only valid until that method returns. A
// callback that panics is survived, not rethrown.
//
// One update is OnFrameBegin followed by one OnTile call per dirty rectangle.
// Each tile is a tightly packed BGRA rectangle copied from Go's persistent frame.
// Updates are incremental: only changed areas arrive after the first full picture.
type Callback interface {
	OnState(state string)
	OnFrameBegin(width, height int)
	OnTile(x, y, w, h int, pixels []byte)
	OnReceipt(opID int64, status int)
	OnError(code, message string)
}

// Session is one pinned, authenticated go-nx viewer connection.
type Session struct {
	client *remote.Client
	cb     Callback

	ctx    context.Context
	cancel context.CancelFunc

	intentional atomic.Bool
	streamed    atomic.Bool
	closeOnce   sync.Once
	nextOp      atomic.Uint64

	inputMu sync.Mutex
	buttons []int32 // mouse buttons held, in the order they went down
	keys    []int32 // keys held, in the order they went down
	mouseX  int32
	mouseY  int32
	havePos bool

	sizeMu sync.RWMutex
	width  int
	height int

	pixelBuf []byte // used by the frame callback only
}

// NewSession creates or loads this device's persistent client identity, requires
// the host's certificate to have the given SHA-256 fingerprint (the host's ID, as
// in a pairing QR code), sends pairingCode as the password, and starts receiving
// pictures. It blocks only for the QUIC handshake and should be called from a
// worker thread.
func NewSession(storageDir, hostAddr, fingerprint, pairingCode string, callback Callback) (*Session, error) {
	return NewSessionWithOptions(storageDir, hostAddr, fingerprint, pairingCode, false, callback)
}

// NewSessionWithOptions is NewSession with options. wantCursor asks the host to
// draw its pointer into the picture, for a viewer that shows no pointer of its own
// (a phone); a viewer that does draw one should leave it off or show two.
func NewSessionWithOptions(storageDir, hostAddr, fingerprint, pairingCode string, wantCursor bool, callback Callback) (*Session, error) {
	if callback == nil {
		return nil, errors.New("mobile: callback is required")
	}
	if strings.TrimSpace(storageDir) == "" {
		return nil, errors.New("mobile: storage directory is required")
	}
	if _, _, err := net.SplitHostPort(hostAddr); err != nil {
		return nil, fmt.Errorf("mobile: invalid host address: %w", err)
	}
	if _, ok := remote.NormalizeFingerprint(fingerprint); !ok {
		return nil, errors.New("mobile: the host fingerprint must be 64 hexadecimal digits")
	}
	if len(pairingCode) > 255 {
		return nil, errors.New("mobile: pairing code is too long")
	}

	safeState(callback, StateConnecting)
	id, err := trust.Identity(storageDir)
	if err != nil {
		safeError(callback, "identity", "cannot load the client identity")
		safeState(callback, StateClosed)
		return nil, err
	}
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 15*time.Second)
	client, err := remote.DialWith(dialCtx, hostAddr, id, nil, remote.DialOptions{
		Password: pairingCode, ExpectFingerprint: fingerprint, WantCursor: wantCursor,
	})
	cancelDial()
	if err != nil {
		code := "disconnected"
		if errors.Is(err, remote.ErrHostChanged) || strings.Contains(err.Error(), remote.ErrHostChanged.Error()) {
			code = "host-identity"
		}
		safeError(callback, code, err.Error())
		safeState(callback, StateClosed)
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{client: client, cb: callback, ctx: ctx, cancel: cancel}
	go s.run()
	return s, nil
}

func (s *Session) run() {
	err := s.client.Run(s.ctx, s.deliverFrame, func(opID uint64, status byte) {
		safeCall(func() { s.cb.OnReceipt(int64(opID), int(status)) })
	})
	if !s.intentional.Load() {
		code, message := classify(err)
		safeError(s.cb, code, message)
	}
	safeState(s.cb, StateClosed)
}

// classify turns why a session ended into a stable code and a sentence.
func classify(err error) (code, message string) {
	if err == nil {
		return "disconnected", "session ended"
	}
	var ae *quic.ApplicationError
	if errors.As(err, &ae) {
		switch ae.ErrorCode {
		case remote.CloseUntrusted:
			return "untrusted", "the host does not trust this device; pair it again"
		case remote.CloseBadPassword:
			return "bad-code", "the pairing code is wrong or has expired"
		case remote.CloseReplaced:
			return "replaced", "another viewer took over this session"
		case remote.CloseLocked:
			return "locked", "too many wrong pairing attempts; try again later"
		case remote.CloseRevoked:
			return "revoked", "this device was removed from the host's paired devices"
		case remote.CloseBusy:
			return "busy", "the host is busy with other connections; try again shortly"
		case remote.CloseUnverified:
			return "unverified", "the host now requires pairing or a password"
		case 0:
			return "disconnected", "the host ended the session"
		}
	}
	if errors.Is(err, context.Canceled) {
		return "disconnected", "session closed"
	}
	return "disconnected", err.Error()
}

// Close lets go of everything held, then ends the session. It returns at once: the
// releases are queued first, and a background goroutine gives them a moment to
// reach the host before the connection is closed. OnState(StateClosed) says when
// it is over. It is safe to call more than once.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.intentional.Store(true)
		_ = s.ReleaseAll()
		go func() {
			s.client.Close()
			s.cancel()
		}()
	})
}

// ReleaseAll sends a button-up for every mouse button and a key-up for every key
// this session believes is held: buttons first, then keys, each starting with the
// one pressed last. Call it when the remote view loses focus.
func (s *Session) ReleaseAll() error {
	if s == nil {
		return nil
	}
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	var errs []error
	for _, b := range slices.Backward(s.buttons) {
		// Where it was pressed, not "nowhere": a host from before the no-move
		// convention would read that as the corner.
		if err := s.client.SendInput(proto.Input{Kind: proto.KindMouseUp, X: s.mouseX, Y: s.mouseY, Arg: b}); err != nil {
			errs = append(errs, err)
		}
	}
	for _, k := range slices.Backward(s.keys) {
		if err := s.client.SendInput(proto.Input{Kind: proto.KindKeyUp, Arg: k}); err != nil {
			errs = append(errs, err)
		}
	}
	s.buttons, s.keys = nil, nil
	return errors.Join(errs...)
}

// Width and Height report the latest picture size in host pixels, or zero before
// the first picture. Use them to map touches into host coordinates.
func (s *Session) Width() int {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.width
}

func (s *Session) Height() int {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.height
}

// HostName is what the host calls itself; empty for a host that does not say.
func (s *Session) HostName() string {
	h, _ := s.client.Host()
	return h.Name
}

// ProtocolVersion is the control-protocol version in use: 1, or 0 for an original
// host.
func (s *Session) ProtocolVersion() int { return s.client.Version() }

// CanText reports whether the host has said it can type text (Text).
func (s *Session) CanText() bool {
	h, _ := s.client.Host()
	return h.Caps.Has(proto.CapText)
}

// CanWheelH reports whether the host has said it understands the horizontal wheel.
func (s *Session) CanWheelH() bool {
	h, _ := s.client.Host()
	return h.Caps.Has(proto.CapWheelH)
}

// AssumeLegacyExtensions tells the session that the host, though it is of the
// original protocol and so cannot say so, takes text and the horizontal wheel (as
// NightDesk's nx-host does). Call it only on that knowledge: a host that does not
// ends the session at the first text.
func (s *Session) AssumeLegacyExtensions() {
	s.client.AssumeCaps(proto.CapText | proto.CapWheelH)
}

// clamp puts a position inside the picture once its size is known, and always
// inside what the protocol carries.
func (s *Session) clamp(x, y int) (int32, int32) {
	w, h := s.Width(), s.Height()
	maxX, maxY := math.MaxInt32, math.MaxInt32
	if w > 0 {
		maxX = w - 1
	}
	if h > 0 {
		maxY = h - 1
	}
	return int32(min(max(x, 0), maxX)), int32(min(max(y, 0), maxY))
}

// Move sends an absolute pointer position in host pixels, clamped to the picture.
func (s *Session) Move(x, y int) error {
	cx, cy := s.clamp(x, y)
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if err := s.client.SendInput(proto.Input{Kind: proto.KindMouseMove, X: cx, Y: cy}); err != nil {
		return err // a move that did not go is not where the pointer is
	}
	s.mouseX, s.mouseY, s.havePos = cx, cy, true
	return nil
}

// MouseDown and MouseUp use button 0 left, 1 right, 2 middle, at the position of
// the last Move.
func (s *Session) MouseDown(button int) error { return s.mouse(button, true) }
func (s *Session) MouseUp(button int) error   { return s.mouse(button, false) }

func (s *Session) mouse(button int, down bool) error {
	if button < 0 || button > 2 {
		return errors.New("mobile: mouse button must be 0, 1 or 2")
	}
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if !s.havePos {
		return errNoPosition
	}
	kind := proto.KindMouseUp
	if down {
		kind = proto.KindMouseDown
	}
	if err := s.client.SendInput(proto.Input{Kind: kind, X: s.mouseX, Y: s.mouseY, Arg: int32(button)}); err != nil {
		return err
	}
	if down {
		s.buttons = addOnce(s.buttons, int32(button))
	} else {
		s.buttons = slices.DeleteFunc(s.buttons, func(b int32) bool { return b == int32(button) })
	}
	return nil
}

// Wheel sends a vertical wheel delta in Windows 1/120-notch units, at the position
// of the last Move; positive is away from the user.
func (s *Session) Wheel(delta int) error { return s.wheel(proto.KindWheel, delta) }

// WheelH sends a horizontal wheel delta in 1/120-notch units; positive is right.
func (s *Session) WheelH(delta int) error { return s.wheel(proto.KindWheelH, delta) }

func (s *Session) wheel(kind byte, delta int) error {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if !s.havePos {
		return errNoPosition
	}
	d := int32(min(max(delta, math.MinInt32), math.MaxInt32))
	return s.client.SendInput(proto.Input{Kind: kind, X: s.mouseX, Y: s.mouseY, Arg: d})
}

// KeyDown and KeyUp take a Windows virtual-key code; 0x100..0x1ff is the extended
// key with that code (0x100|0x2d is Insert).
func (s *Session) KeyDown(vk int) error { return s.key(vk, true) }
func (s *Session) KeyUp(vk int) error   { return s.key(vk, false) }

func (s *Session) key(vk int, down bool) error {
	if vk < 1 || vk > 0x1ff {
		return errors.New("mobile: virtual-key code out of range")
	}
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	kind := proto.KindKeyUp
	if down {
		kind = proto.KindKeyDown
	}
	if err := s.client.SendInput(proto.Input{Kind: kind, Arg: int32(vk)}); err != nil {
		return err
	}
	if down {
		s.keys = addOnce(s.keys, int32(vk))
	} else {
		s.keys = slices.DeleteFunc(s.keys, func(k int32) bool { return k == int32(vk) })
	}
	return nil
}

// addOnce appends v unless it is there: a key's auto-repeat is more KeyDowns of a
// key already held, not more keys.
func addOnce(list []int32, v int32) []int32 {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// Text types literal UTF-8 text on the host, whatever its keyboard layout. The
// host reports delivery through OnReceipt, with operation numbers 1, 2, 3...
func (s *Session) Text(text string) error {
	if len(text) == 0 {
		return nil
	}
	if len(text) > proto.MaxTextLen {
		return fmt.Errorf("mobile: text exceeds %d UTF-8 bytes", proto.MaxTextLen)
	}
	return s.client.SendText(s.nextOp.Add(1), text)
}

func (s *Session) deliverFrame(frame *tile.Frame, rects []tile.Rect) {
	s.sizeMu.Lock()
	s.width, s.height = frame.W, frame.H
	s.sizeMu.Unlock()
	safeCall(func() { s.cb.OnFrameBegin(frame.W, frame.H) })
	for _, r := range rects {
		s.pixelBuf = s.pixelBuf[:0]
		for y := 0; y < r.H; y++ {
			off := ((r.Y+y)*frame.W + r.X) * 4
			s.pixelBuf = append(s.pixelBuf, frame.Pix[off:off+r.W*4]...)
		}
		pixels := s.pixelBuf
		safeCall(func() { s.cb.OnTile(r.X, r.Y, r.W, r.H, pixels) })
	}
	if s.streamed.CompareAndSwap(false, true) {
		safeState(s.cb, StateStreaming)
	}
}

func safeState(cb Callback, state string) { safeCall(func() { cb.OnState(state) }) }

func safeError(cb Callback, code, message string) { safeCall(func() { cb.OnError(code, message) }) }

func safeCall(fn func()) {
	defer func() { _ = recover() }()
	fn()
}
