package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// ErrDenied is returned by Serve when the viewer was refused.
var ErrDenied = errors.New("remote: viewer refused")

// Source supplies screen frames.
type Source interface {
	Size() (w, h int)
	Grab(dst *tile.Frame) error
}

// Refresher is implemented by a Source whose surface can go stale or change size:
// a monitor whose resolution changes, or a desktop that is briefly unavailable
// (lock screen, UAC prompt, a display being reconfigured). Refresh rebuilds what
// it needs and reports whether the size or position changed.
type Refresher interface {
	Refresh() (changed bool, err error)
}

// CursorSetter is implemented by a Source that can draw the system pointer into
// the pictures it grabs. Serve turns it on for a viewer that asks for it and off
// otherwise, because a viewer that shows its own pointer would show two.
type CursorSetter interface {
	SetCursorVisible(on bool)
}

// Injector performs user input on the host.
type Injector interface {
	Inject(in proto.Input) error
}

// TextInjector is implemented by an Injector that can also type literal Unicode
// text, independent of the keyboard layout and of any input method that is
// active. A host whose Injector lacks it answers text with a failed receipt.
type TextInjector interface {
	InjectText(text string) error
}

// errTextUnsupported is the failure behind a Failed receipt for text sent to a
// host that cannot type it.
var errTextUnsupported = errors.New("remote: this host cannot type text")

// inputSink is what the control loop feeds: key, mouse and wheel input, and text.
type inputSink interface {
	Inject(in proto.Input) error
	InjectText(text string) error
}

// plainSink adapts an Injector that is not behind an InputController.
type plainSink struct{ inj Injector }

func (p plainSink) Inject(in proto.Input) error { return p.inj.Inject(in) }

func (p plainSink) InjectText(text string) error {
	if t, ok := p.inj.(TextInjector); ok {
		return t.InjectText(text)
	}
	return errTextUnsupported
}

type Options struct {
	FPS int // capture rate; default DefaultFPS
	// FPSFn, if set, is asked for the capture rate before every frame, so the rate
	// can follow what the viewer is doing (see Boost). A value of zero or less keeps
	// the current rate; one above MaxFPS is lowered to it. A change is noticed at
	// the next tick, so from a slow rate it can take up to one slow interval.
	FPSFn func() int
	// IdleFPS, if above zero and below the rate otherwise in force, is the capture
	// rate while the session is idle: no change to the picture has been sent and the
	// viewer has sent no input for IdleAfter. Looking at an unchanged screen is not
	// free (a full comparison of every tile, as well as the grab), so a viewer that is
	// only watching a still desktop costs the host little. The first input ends the
	// idleness at once; a change on the screen is noticed at the next idle tick.
	IdleFPS int
	// IdleAfter is how long nothing must happen before IdleFPS applies; default 3 s.
	IdleAfter  time.Duration
	AckTimeout time.Duration // base time to wait for a frame's ack; default 1s (see frameTimeout)
	// Check decides whether the viewer (certificate fingerprint, address, offered
	// password) may connect.
	Check    func(fingerprint, addr, password string) (trust.Verdict, error)
	OnAuthed func()      // called once the viewer is accepted
	OnError  func(error) // non-fatal session problems
	// OnInput is called for every input or text message an admitted viewer sends
	// (including repeats of one already run), not for acknowledgements. It runs on
	// the goroutine that reads the viewer and must not block.
	OnInput func()
	// OnFrame is called after each screen update has been written to the wire, with
	// its sequence number and size in bytes. It runs on the goroutine that serves the
	// session and must not block.
	OnFrame         func(seq uint32, bytes int)
	InputController *InputController // shared controller for releasing inputs across sessions
	Gate            *Gate            // optional cap on connections awaiting authentication
	Sessions        *Sessions        // optional: lets the host end sessions that were let in without verification

	// HostName is this host's display name, sent to a v1 viewer in the welcome.
	HostName string
	// Caps are abilities to advertise in addition to the ones Serve works out from
	// the injector (text, horizontal wheel); the caller adds what only it knows,
	// such as a source that draws the pointer into the frames.
	Caps proto.Caps
	// OnPeer, if set, is called once the viewer has been admitted, with what it
	// introduced itself as (the name is empty for a v0 viewer).
	OnPeer func(PeerInfo)

	// RefreshEvery is how often a Refresher source is asked whether the screen
	// changed; default 1 s.
	RefreshEvery time.Duration
	// GrabRetryFor is how long a Refresher source may keep failing to grab before
	// the session is ended; default 60 s. A source without Refresh ends the
	// session at the first failure.
	GrabRetryFor time.Duration
	// HeldInputIdle is how long a viewer may be silent while holding a key or mouse
	// button before the host lets go of them; default 20 s. A live viewer holding a
	// key keeps sending its auto-repeat, so only one that has gone is released early.
	HeldInputIdle time.Duration
	// OnSourceChanged, if set, is called with the new size when a Refresher source
	// reports that its geometry changed, so the caller can retarget the injector.
	OnSourceChanged func(w, h int)
	// OnViewSize, if set, is called when a v1 viewer asks for the session to be shown on a
	// virtual display of w x h (0 x 0: on the host's own screen again). report sends the
	// viewer what came of it, and may be called later from any goroutine. OnViewSize runs
	// on the goroutine that reads the viewer and must not block. Setting it advertises
	// CapVirtualDisplay.
	OnViewSize func(w, h int, report func(proto.DisplayState))
}

// PeerInfo is what a viewer told the host about itself.
type PeerInfo struct {
	Version int    // control-protocol version in use: 0 or 1
	Name    string // the viewer's display name; empty for v0
	Caps    proto.Caps
}

// minRate is the slowest link we still expect to deliver a frame on. A frame's
// timeout is AckTimeout plus the time it takes at this rate, so a full-screen
// update (several MB) gets seconds while a few-tile update keeps the short timeout.
// A fixed timeout would cancel and resend a large frame forever on a slow link.
const minRate = 250_000 // bytes per second (2 Mbit/s)

// defaultIdleAfter is how long a session must see no change and no input before
// Options.IdleFPS applies.
const defaultIdleAfter = 3 * time.Second

// captureRate is the rate to capture at: want, unless a slower idle rate is set and
// the session has been idle for at least idleAfter. An idle rate that is not slower
// than want is ignored.
func captureRate(want, idleFPS int, idleFor, idleAfter time.Duration) int {
	if idleFPS > 0 && idleFPS < want && idleFor >= idleAfter {
		return idleFPS
	}
	return want
}

func frameTimeout(base time.Duration, size int) time.Duration {
	return base + time.Duration(size)*time.Second/minRate
}

type flight struct {
	timeout time.Duration
	seq     uint32
	idxs    []int
	st      *quic.SendStream
	sentAt  time.Time
}

// Serve runs one client session until the connection ends.
//
// Screen updates are sent one at a time: a frame is only followed by the next
// after the client acks it. If the ack does not arrive within AckTimeout the
// frame's stream is cancelled and its tiles are folded into the next update,
// which is encoded from the newest screen contents. A slow network therefore
// lowers the frame rate instead of building up a backlog.
func Serve(ctx context.Context, conn *quic.Conn, src Source, inj Injector, dd *proto.Dedup, opt Options) error {
	closeWithDefault := true
	defer func() {
		if closeWithDefault {
			conn.CloseWithError(0, "session ended")
		}
	}()
	if opt.FPS <= 0 {
		opt.FPS = DefaultFPS
	}
	opt.FPS = min(opt.FPS, MaxFPS)
	if opt.AckTimeout <= 0 {
		opt.AckTimeout = time.Second
	}
	if opt.IdleAfter <= 0 {
		opt.IdleAfter = defaultIdleAfter
	}
	if opt.RefreshEvery <= 0 {
		opt.RefreshEvery = time.Second
	}
	if opt.GrabRetryFor <= 0 {
		opt.GrabRetryFor = time.Minute
	}
	if opt.HeldInputIdle <= 0 {
		opt.HeldInputIdle = defaultIdle
	}
	// Hold a pre-authentication slot until the host has decided about this viewer.
	leaveGate := func() {}
	if opt.Gate != nil {
		leave, ok := opt.Gate.Enter(conn.RemoteAddr().String())
		if !ok {
			closeWithDefault = false
			conn.CloseWithError(CloseBusy, "too many connections waiting")
			return ErrDenied
		}
		leaveGate = leave
	}
	defer leaveGate()
	var peer string
	if certs := conn.ConnectionState().TLS.PeerCertificates; len(certs) > 0 {
		peer = trust.Fingerprint(certs[0].Raw)
	}
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ctrl, err := conn.AcceptStream(actx)
	cancel()
	if err != nil {
		return err
	}
	cd, err := codecFor(conn.ConnectionState().TLS.NegotiatedProtocol)
	if err != nil {
		return err
	}
	ctrl.SetReadDeadline(time.Now().Add(10 * time.Second))
	hello, err := cd.readClientMsg(ctrl)
	if err != nil {
		return err
	}
	if hello.Type != proto.MsgHello {
		return errors.New("remote: expected hello")
	}
	ctrl.SetReadDeadline(time.Time{})
	verdict := trust.Untrusted
	if peer != "" && opt.Check != nil {
		var err error
		if verdict, err = opt.Check(peer, conn.RemoteAddr().String(), hello.Password); err != nil && opt.OnError != nil {
			opt.OnError(err)
		}
	}
	leaveGate() // decided: no longer waiting to be authenticated
	if !verdict.Admitted() {
		code := map[trust.Verdict]quic.ApplicationErrorCode{
			trust.BadPassword: CloseBadPassword, trust.Locked: CloseLocked,
		}[verdict]
		if code == 0 {
			code = CloseUntrusted
		}
		closeWithDefault = false
		conn.CloseWithError(code, "refused")
		return ErrDenied
	}

	if opt.Sessions != nil {
		remove, ok := opt.Sessions.add(conn, verdict == trust.AllowedOpen)
		if !ok { // the host went strict between the policy's decision and now
			closeWithDefault = false
			conn.CloseWithError(CloseUnverified, "host requires verification")
			return ErrDenied
		}
		defer remove()
	}

	var input inputSink = plainSink{inj}
	var inputSession *sessionInputState
	if opt.InputController != nil {
		inputSession = opt.InputController.newSession()
		if err := inputSession.Activate(opt.OnAuthed); err != nil && opt.OnError != nil {
			opt.OnError(err)
		}
	} else if opt.OnAuthed != nil {
		opt.OnAuthed()
	}
	if inputSession != nil {
		input = inputSession
		defer func() {
			if err := inputSession.Release(); err != nil && opt.OnError != nil {
				opt.OnError(err)
			}
		}()
	}

	refresher, _ := src.(Refresher)
	if refresher != nil {
		refresher.Refresh() // the screen may have changed since the host started
	}
	if cs, ok := src.(CursorSetter); ok {
		cs.SetCursorVisible(cd.version() >= proto.Version1 && hello.Caps.Has(proto.CapCursor))
	}
	w, h := src.Size()
	if !proto.ValidFrameSize(w, h) {
		// A viewer would refuse the frames, so say so here instead of streaming them.
		return fmt.Errorf("screen %dx%d is outside what the protocol carries (at most %d px a side, %d px in all)",
			w, h, proto.MaxFrameDim, proto.MaxFramePixels)
	}
	if opt.OnPeer != nil {
		opt.OnPeer(PeerInfo{Version: cd.version(), Name: hello.Name, Caps: hello.Caps})
	}
	if err := cd.writeWelcome(ctrl, proto.WelcomeV1{
		Version: proto.Version1, Caps: hostCaps(inj, src, opt), W: uint16(w), H: uint16(h), Name: opt.HostName,
	}); err != nil {
		return err
	}

	// Input from the viewer is activity: it ends idleness and, when it asks for a
	// faster rate than the one in force, wakes the capture loop at once instead of
	// leaving it to find out at its next (possibly slow) tick.
	var lastAck atomic.Uint32
	var lastInput atomic.Int64 // unix nanoseconds of the viewer's last input; 0 for none
	kick := make(chan struct{}, 1)
	onInput := func() {
		lastInput.Store(time.Now().UnixNano())
		if opt.OnInput != nil {
			opt.OnInput()
		}
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	// Receipts and display states are written from different goroutines.
	out := &syncWriter{w: ctrl}
	var onViewSize func(w, h int)
	if opt.OnViewSize != nil {
		report := func(st proto.DisplayState) {
			if err := cd.writeDisplayState(out, st); err != nil && opt.OnError != nil {
				opt.OnError(fmt.Errorf("display state not sent: %w", err))
			}
		}
		onViewSize = func(w, h int) { opt.OnViewSize(w, h, report) }
	}
	readErr := make(chan error, 1)
	go func() { readErr <- readControl(ctrl, out, cd, input, dd, &lastAck, onInput, onViewSize) }()

	cur := tile.NewFrame(w, h)
	var diff tile.Differ
	pending := map[int]struct{}{}
	var seq uint32
	var fl *flight

	// retarget follows a change in the source: a new frame size starts a new
	// picture (the first update after it is complete), and the caller learns of it.
	retarget := func() error {
		nw, nh := src.Size()
		if !proto.ValidFrameSize(nw, nh) {
			return fmt.Errorf("screen %dx%d is outside what the protocol carries", nw, nh)
		}
		if nw != w || nh != h {
			w, h = nw, nh
			cur = tile.NewFrame(w, h)
			diff = tile.Differ{}
			clear(pending)
			if fl != nil {
				fl.st.CancelWrite(0) // a picture of the old size is no use
				fl = nil
			}
		}
		if opt.OnSourceChanged != nil {
			opt.OnSourceChanged(w, h)
		}
		return nil
	}

	var lastRefresh, grabFailedSince time.Time
	lastChange := time.Now() // a session that has just begun is not idle
	want := opt.FPS          // the rate the caller asks for, before any slowing down for idleness
	rate := func() int {
		if opt.FPSFn != nil {
			if n := min(opt.FPSFn(), MaxFPS); n > 0 {
				want = n
			}
		}
		last := lastChange
		if in := time.Unix(0, lastInput.Load()); in.After(last) {
			last = in
		}
		return captureRate(want, opt.IdleFPS, time.Since(last), opt.IdleAfter)
	}
	fps := rate()
	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()
	for {
		select {
		case <-conn.Context().Done():
			return nil
		case err := <-readErr:
			if errors.Is(err, io.EOF) {
				return nil // the viewer ended its control stream on purpose (Client.Close): not a failure
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if next := rate(); next != fps {
				fps = next
				tick.Reset(time.Second / time.Duration(fps))
			}
		case <-kick:
			next := rate()
			if next <= fps {
				continue // already fast enough: the ticker will come by on time
			}
			fps = next
			tick.Reset(time.Second / time.Duration(fps))
		}
		if inputSession != nil {
			if released, err := inputSession.ReleaseIfIdle(opt.HeldInputIdle); released && opt.OnError != nil {
				opt.OnError(fmt.Errorf("let go of held input after %v without word from the viewer (release error: %v)", opt.HeldInputIdle, err))
			}
		}
		if refresher != nil && time.Since(lastRefresh) >= opt.RefreshEvery {
			lastRefresh = time.Now()
			if changed, err := refresher.Refresh(); err == nil && changed {
				if err := retarget(); err != nil {
					return err
				}
			}
		}
		if err := src.Grab(cur); err != nil {
			if refresher == nil {
				return err
			}
			// The desktop is unavailable for now. Keep the session, tell the caller
			// once, and keep trying to get it back for a while: the viewer keeps its
			// last picture meanwhile.
			if grabFailedSince.IsZero() {
				grabFailedSince = time.Now()
				if opt.OnError != nil {
					opt.OnError(fmt.Errorf("capture paused: %w", err))
				}
			}
			if time.Since(grabFailedSince) > opt.GrabRetryFor {
				return fmt.Errorf("capture did not recover within %v: %w", opt.GrabRetryFor, err)
			}
			if changed, rerr := refresher.Refresh(); rerr == nil && changed {
				if err := retarget(); err != nil {
					return err
				}
			}
			continue
		}
		if !grabFailedSince.IsZero() {
			grabFailedSince = time.Time{}
			if opt.OnError != nil {
				opt.OnError(errors.New("capture resumed"))
			}
		}
		for _, i := range diff.Diff(cur) {
			pending[i] = struct{}{}
		}
		if fl != nil {
			switch {
			case lastAck.Load() >= fl.seq:
				fl = nil
			case time.Since(fl.sentAt) > fl.timeout:
				fl.st.CancelWrite(0)
				for _, i := range fl.idxs {
					pending[i] = struct{}{}
				}
				fl = nil
			default:
				continue
			}
		}
		if len(pending) == 0 {
			continue
		}
		idxs := make([]int, 0, len(pending))
		for i := range pending {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		data, err := tile.Encode(cur, idxs)
		if err != nil {
			return err
		}
		st, err := conn.OpenUniStream()
		if err != nil {
			continue // stream limit reached; retry next tick, pending is kept
		}
		seq++
		hdr := proto.FrameHeader{Seq: seq, W: uint16(w), H: uint16(h)}.Marshal()
		timeout := frameTimeout(opt.AckTimeout, len(data))
		st.SetWriteDeadline(time.Now().Add(timeout))
		if _, err := st.Write(append(hdr, data...)); err != nil {
			st.CancelWrite(0)
			continue
		}
		st.Close()
		lastChange = time.Now()
		if next := rate(); next > fps {
			// A change found at an idle look ends the idleness now, not at the next idle tick.
			fps = next
			tick.Reset(time.Second / time.Duration(fps))
		}
		if opt.OnFrame != nil {
			opt.OnFrame(seq, len(data))
		}
		fl = &flight{seq: seq, idxs: idxs, st: st, sentAt: time.Now(), timeout: timeout}
		clear(pending)
	}
}

func readControl(ctrl io.Reader, out io.Writer, cd codec, input inputSink, dd *proto.Dedup, lastAck *atomic.Uint32, onInput func(), onViewSize func(w, h int)) error {
	for {
		m, err := cd.readClientMsg(ctrl)
		if err != nil {
			return err
		}
		if onInput != nil && (m.Type == proto.MsgInput || m.Type == proto.MsgText) {
			onInput()
		}
		switch m.Type {
		case proto.MsgAck:
			lastAck.Store(m.Seq)
		case proto.MsgInput:
			if err := runOp(out, cd, dd, m.Input.OpID, func() error { return input.Inject(m.Input) }); err != nil {
				return err
			}
		case proto.MsgText:
			if err := runOp(out, cd, dd, m.OpID, func() error { return input.InjectText(m.Text) }); err != nil {
				return err
			}
		case proto.V1ClientViewSize:
			if onViewSize != nil {
				onViewSize(int(m.W), int(m.H))
			}
		}
	}
}

// runOp runs one input operation exactly once per opID and reports the result.
// opID 0 is fire-and-forget: it runs every time and gets no receipt. A repeated
// opID (a retry after a lost receipt) is not run again; its first receipt is
// sent again.
func runOp(out io.Writer, cd codec, dd *proto.Dedup, opID uint64, run func() error) error {
	if opID == 0 {
		_ = run()
		return nil
	}
	status, seen := dd.Get(opID)
	if !seen {
		status = proto.StatusExecuted
		if run() != nil {
			status = proto.StatusFailed
		}
		dd.Put(opID, status)
	}
	return cd.writeReceipt(out, opID, status)
}

// syncWriter lets several goroutines write whole messages to one stream.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// hostCaps is what this host tells a v1 viewer it can do: what the injector is
// able to do, plus whatever the caller adds.
func hostCaps(inj Injector, src Source, opt Options) proto.Caps {
	caps := proto.CapWheelH | opt.Caps
	if opt.OnViewSize != nil {
		caps |= proto.CapVirtualDisplay
	}
	if _, ok := inj.(TextInjector); ok {
		caps |= proto.CapText
	}
	if _, ok := src.(Refresher); ok {
		caps |= proto.CapResize
	}
	if _, ok := src.(CursorSetter); ok {
		caps |= proto.CapCursor
	}
	return caps
}
