package remote

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// Injector performs user input on the host.
type Injector interface {
	Inject(in proto.Input) error
}

type Options struct {
	FPS        int           // capture rate; default 10
	AckTimeout time.Duration // base time to wait for a frame's ack; default 1s (see frameTimeout)
	// Check decides whether the viewer (certificate fingerprint, address, offered
	// password) may connect.
	Check           func(fingerprint, addr, password string) (trust.Verdict, error)
	OnAuthed        func()           // called once the viewer is accepted
	OnError         func(error)      // non-fatal session problems
	InputController *InputController // shared controller for releasing inputs across sessions
	Gate            *Gate            // optional cap on connections awaiting authentication
	Sessions        *Sessions        // optional: lets the host end sessions that were let in without verification
}

// minRate is the slowest link we still expect to deliver a frame on. A frame's
// timeout is AckTimeout plus the time it takes at this rate, so a full-screen
// update (several MB) gets seconds while a few-tile update keeps the short timeout.
// A fixed timeout would cancel and resend a large frame forever on a slow link.
const minRate = 250_000 // bytes per second (2 Mbit/s)

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
		opt.FPS = 10
	}
	if opt.AckTimeout <= 0 {
		opt.AckTimeout = time.Second
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
	ctrl.SetReadDeadline(time.Now().Add(10 * time.Second))
	hello, err := proto.ReadMsg(ctrl)
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

	input := inj
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

	var lastAck atomic.Uint32
	readErr := make(chan error, 1)
	go func() { readErr <- readControl(ctrl, input, dd, &lastAck) }()

	w, h := src.Size()
	if !proto.ValidFrameSize(w, h) {
		// A viewer would refuse the frames, so say so here instead of streaming them.
		return fmt.Errorf("screen %dx%d is outside what the protocol carries (at most %d px a side, %d px in all)",
			w, h, proto.MaxFrameDim, proto.MaxFramePixels)
	}
	cur := tile.NewFrame(w, h)
	var diff tile.Differ
	pending := map[int]struct{}{}
	var seq uint32
	var fl *flight

	tick := time.NewTicker(time.Second / time.Duration(opt.FPS))
	defer tick.Stop()
	for {
		select {
		case <-conn.Context().Done():
			return nil
		case err := <-readErr:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		if err := src.Grab(cur); err != nil {
			return err
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
		fl = &flight{seq: seq, idxs: idxs, st: st, sentAt: time.Now(), timeout: timeout}
		clear(pending)
	}
}

func readControl(ctrl *quic.Stream, inj Injector, dd *proto.Dedup, lastAck *atomic.Uint32) error {
	for {
		m, err := proto.ReadMsg(ctrl)
		if err != nil {
			return err
		}
		switch m.Type {
		case proto.MsgAck:
			lastAck.Store(m.Seq)
		case proto.MsgInput:
			in := m.Input
			if in.OpID == 0 {
				inj.Inject(in)
				continue
			}
			status, seen := dd.Get(in.OpID)
			if !seen {
				status = proto.StatusExecuted
				if inj.Inject(in) != nil {
					status = proto.StatusFailed
				}
				dd.Put(in.OpID, status)
			}
			if err := proto.WriteReceipt(ctrl, in.OpID, status); err != nil {
				return err
			}
		}
	}
}
