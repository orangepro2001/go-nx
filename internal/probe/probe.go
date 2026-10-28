// Package probe connects to a go-nx host the way a phone or a script would: with
// the host's fingerprint pinned from the start, the pairing code as the password,
// and the same decoding path as the real viewer. It then checks that pictures
// arrive and that every input it sends is acknowledged. It is an acceptance tool
// for a running host; it captures and injects nothing itself.
//
// What it can prove is the protocol path: handshake, pinning, pairing, tile
// decoding, receipts. It cannot prove that the host's real screen capture or input
// injection is right; that needs a real desktop (docs/ACCEPTANCE.md).
package probe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// SchemaVersion is the version of Result's JSON; it changes only when a field is
// renamed or its meaning changes, never when one is added.
const SchemaVersion = 1

// Config describes one run.
type Config struct {
	Addr        string        // host address, host:port
	Fingerprint string        // SHA-256 fingerprint the host must present
	Code        string        // pairing code or password
	StateDir    string        // where this probe keeps its identity; a fresh temporary one if empty
	Frames      int           // pictures to receive; default 3
	Timeout     time.Duration // overall deadline; default 30 s
	DialTimeout time.Duration // handshake deadline; default 15 s
	SendInput   bool          // send one of each kind of input and a text, and wait for receipts
	Text        string        // the text; default "go-nx probe 你好"
	ForceV0     bool          // speak the original protocol; text and horizontal wheel are then taken for granted
	WantCursor  bool          // ask the host to draw the pointer into the picture
	// CheckMarkers verifies the orientation markers of a synthetic host (package
	// synth) on every picture. It fails against a real desktop, which has none.
	CheckMarkers bool
}

// Result is what a run found, as JSON for scripts. Fields are added over time but
// never renamed; Schema says which names these are.
type Result struct {
	Schema          int      `json:"schema"`
	Addr            string   `json:"addr"`
	Connected       bool     `json:"connected"`
	Protocol        int      `json:"protocol"`
	HostName        string   `json:"hostName,omitempty"`
	HostCaps        []string `json:"hostCaps,omitempty"`
	Width           int      `json:"width"`
	Height          int      `json:"height"`
	FirstFrameMS    int64    `json:"firstFrameMs"`
	Frames          int      `json:"frames"`
	Tiles           int      `json:"tiles"`
	DigestFirst     string   `json:"digestFirst"`
	DigestLast      string   `json:"digestLast"`
	InputsSent      int      `json:"inputsSent"`
	Receipts        int      `json:"receipts"`
	ReceiptFailures int      `json:"receiptFailures"`
	MarkersOK       *bool    `json:"markersOk,omitempty"` // set only when CheckMarkers was on
	DurationMS      int64    `json:"durationMs"`
	Error           string   `json:"error,omitempty"`
}

func (c Config) withDefaults() Config {
	if c.Frames <= 0 {
		c.Frames = 3
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 15 * time.Second
	}
	if c.Text == "" {
		c.Text = "go-nx probe 你好"
	}
	return c
}

type state struct {
	mu              sync.Mutex
	frames, tiles   int
	width, height   int
	firstFrameMS    int64
	digests         []string
	receipts        map[uint64]byte
	receiptFailures int
	markersErr      error
}

type snapshot struct {
	frames, tiles, receipts, failures int
	width, height                     int
	firstMS                           int64
	first, last                       string
	markersErr                        error
}

func (s *state) snap() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := snapshot{
		frames: s.frames, tiles: s.tiles, receipts: len(s.receipts), failures: s.receiptFailures,
		width: s.width, height: s.height, firstMS: s.firstFrameMS, markersErr: s.markersErr,
	}
	if n := len(s.digests); n > 0 {
		sn.first, sn.last = s.digests[0], s.digests[n-1]
	}
	return sn
}

// Run connects, receives pictures and, if asked, exercises the input kinds.
func Run(ctx context.Context, cfg Config) (Result, error) {
	cfg = cfg.withDefaults()
	start := time.Now()
	res := Result{Schema: SchemaVersion, Addr: cfg.Addr}
	fail := func(err error) (Result, error) {
		res.Error = err.Error()
		res.DurationMS = time.Since(start).Milliseconds()
		return res, err
	}
	if cfg.Addr == "" || cfg.Fingerprint == "" {
		return fail(errors.New("probe: an address and the host's fingerprint are both required"))
	}

	dir := cfg.StateDir
	if dir == "" {
		tmp, err := os.MkdirTemp("", "nx-probe-")
		if err != nil {
			return fail(err)
		}
		defer os.RemoveAll(tmp)
		dir = tmp
	}
	id, err := trust.Identity(dir)
	if err != nil {
		return fail(fmt.Errorf("probe: identity: %w", err))
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, cfg.DialTimeout)
	client, err := remote.DialWith(dialCtx, cfg.Addr, id, nil, remote.DialOptions{
		Password: cfg.Code, ExpectFingerprint: cfg.Fingerprint, ForceV0: cfg.ForceV0, WantCursor: cfg.WantCursor,
	})
	cancelDial()
	if err != nil {
		return fail(fmt.Errorf("probe: connect to %s: %w", cfg.Addr, err))
	}
	defer client.Close()
	res.Connected = true
	res.Protocol = client.Version()
	if cfg.ForceV0 {
		client.AssumeCaps(proto.CapText | proto.CapWheelH)
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	st := &state{receipts: map[uint64]byte{}}
	first := make(chan struct{}, 1)
	progress := make(chan struct{}, 1)
	done := make(chan error, 1)
	ping := func(c chan struct{}) {
		select {
		case c <- struct{}{}:
		default:
		}
	}
	go func() {
		done <- client.Run(runCtx, func(f *tile.Frame, rects []tile.Rect) {
			sum := sha256.Sum256(f.Pix)
			st.mu.Lock()
			st.frames++
			st.tiles += len(rects)
			if st.frames == 1 {
				st.width, st.height = f.W, f.H
				st.firstFrameMS = time.Since(start).Milliseconds()
			}
			st.digests = append(st.digests, hex.EncodeToString(sum[:]))
			if cfg.CheckMarkers && st.markersErr == nil {
				st.markersErr = synth.VerifyMarkers(f)
			}
			st.mu.Unlock()
			ping(first)
			ping(progress)
		}, func(opID uint64, status byte) {
			st.mu.Lock()
			st.receipts[opID] = status
			if status != proto.StatusExecuted {
				st.receiptFailures++
			}
			st.mu.Unlock()
			ping(progress)
		})
	}()

	deadline := time.NewTimer(cfg.Timeout)
	defer deadline.Stop()

	expected := 0
	var sendErr error
	if cfg.SendInput {
		select {
		case <-first:
		case err := <-done:
			return fail(fmt.Errorf("probe: the session ended before the first picture: %w", errOrClosed(err)))
		case <-deadline.C:
			return fail(errors.New("probe: no picture arrived before the deadline"))
		}
		sn := st.snap()
		res.InputsSent, sendErr = sendInputs(client, cfg, sn.width, sn.height)
		expected = res.InputsSent
	}

	var sessionErr error
	timedOut := false
loop:
	for {
		sn := st.snap()
		if sn.frames >= cfg.Frames && sn.receipts >= expected {
			break
		}
		select {
		case <-progress:
		case <-deadline.C:
			timedOut = true
			break loop
		case err := <-done:
			sessionErr = errOrClosed(err)
			break loop
		}
	}

	sn := st.snap()
	res.Frames, res.Tiles = sn.frames, sn.tiles
	res.Width, res.Height, res.FirstFrameMS = sn.width, sn.height, sn.firstMS
	res.Receipts, res.ReceiptFailures = sn.receipts, sn.failures
	res.DigestFirst, res.DigestLast = sn.first, sn.last
	if h, ok := client.Host(); ok {
		res.HostName = h.Name
		res.HostCaps = h.Caps.Names()
	}
	if cfg.CheckMarkers {
		ok := sn.markersErr == nil && sn.frames > 0
		res.MarkersOK = &ok
	}

	cancelRun()
	client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	var err2 error
	switch {
	case sendErr != nil:
		err2 = fmt.Errorf("probe: send input: %w", sendErr)
	case timedOut:
		err2 = fmt.Errorf("probe: timeout after %v: %d of %d pictures, %d of %d receipts, %d failed%s",
			cfg.Timeout, sn.frames, cfg.Frames, sn.receipts, expected, sn.failures, stillHint(sn.frames))
	case sessionErr != nil:
		err2 = fmt.Errorf("probe: the session ended after %d of %d pictures and %d of %d receipts: %w",
			sn.frames, cfg.Frames, sn.receipts, expected, sessionErr)
	case sn.markersErr != nil:
		err2 = fmt.Errorf("probe: %w", sn.markersErr)
	case sn.failures > 0:
		err2 = fmt.Errorf("probe: %d input receipts reported failure", sn.failures)
	case cfg.Frames > 1 && sn.first == sn.last:
		err2 = errors.New("probe: the first and the last picture are identical; the source did not animate")
	}
	if err2 != nil {
		return fail(err2)
	}
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

func stillHint(frames int) string {
	if frames == 1 {
		return " (a host showing a still screen sends one picture and no more)"
	}
	return ""
}

func errOrClosed(err error) error {
	if err == nil {
		return errors.New("the host closed the connection")
	}
	return err
}

// sendInputs sends one of each kind of input the host can take, every one with an
// operation number so that the host must answer each with a receipt, and returns
// how many it sent. Pointer positions are in the middle of the screen.
func sendInputs(c *remote.Client, cfg Config, w, h int) (int, error) {
	caps := proto.CapText | proto.CapWheelH // a v0 host cannot say; ForceV0 assumed these
	if info, ok := c.Host(); ok && c.Version() > 0 {
		caps = info.Caps
	}
	x, y := int32(w/2), int32(h/2)
	ops := []proto.Input{
		{Kind: proto.KindMouseMove, X: x, Y: y},
		{Kind: proto.KindMouseDown, X: x, Y: y, Arg: 0},
		{Kind: proto.KindMouseUp, X: x, Y: y, Arg: 0},
		{Kind: proto.KindWheel, X: x, Y: y, Arg: 120},
		{Kind: proto.KindKeyDown, Arg: 0x41},
		{Kind: proto.KindKeyUp, Arg: 0x41},
	}
	if caps.Has(proto.CapWheelH) {
		ops = append(ops, proto.Input{Kind: proto.KindWheelH, X: x, Y: y, Arg: 120})
	}
	sent := 0
	for i, in := range ops {
		in.OpID = uint64(i + 1)
		if err := c.SendInput(in); err != nil {
			return sent, err
		}
		sent++
	}
	if caps.Has(proto.CapText) {
		if err := c.SendText(uint64(sent+1), cfg.Text); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}
