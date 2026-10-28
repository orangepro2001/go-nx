package probe

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/logsink"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

const (
	testCode      = "123456"
	screenW       = 640
	screenH       = 480
	testHostName  = "probe-test-host"
	probeDeadline = 25 * time.Second
)

// textlessSink is an injector that cannot type text.
type textlessSink struct{ s *logsink.Sink }

func (t textlessSink) Inject(in proto.Input) error { return t.s.Inject(in) }

type testHost struct {
	addr string
	fp   string
	sink *logsink.Sink

	mu       sync.Mutex
	seenFPs  []string // fingerprints of the viewers that were checked
	attempts int      // how many viewers were checked at all
}

func (h *testHost) viewers() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.seenFPs)
}

// startHost runs a real QUIC host on loopback over a synthetic screen and a sink
// that only logs, as nx-host does.
func startHost(t *testing.T, scene synth.Scene, typing bool) *testHost {
	t.Helper()
	return startHostWith(t, scene, typing, func(s remote.Source) remote.Source { return s })
}

// startHostWith is startHost with the source wrapped, to give it a fault.
func startHostWith(t *testing.T, scene synth.Scene, typing bool, wrap func(remote.Source) remote.Source) *testHost {
	t.Helper()
	id, err := trust.Identity(filepath.Join(t.TempDir(), "host"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := remote.Listen("127.0.0.1:0", id)
	if err != nil {
		t.Fatal(err)
	}
	src, err := synth.NewScene(scene, screenW, screenH)
	if err != nil {
		t.Fatal(err)
	}
	th := &testHost{addr: ln.Addr().String(), fp: trust.Fingerprint(id.Certificate[0]), sink: logsink.New(nil)}
	th.sink.SetScreen(screenW, screenH)
	var inj remote.Injector = th.sink
	if !typing {
		inj = textlessSink{th.sink}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); ln.Close() })
	opt := remote.Options{
		FPS: 20, HostName: testHostName,
		Check: func(f, _, password string) (trust.Verdict, error) {
			th.mu.Lock()
			th.seenFPs = append(th.seenFPs, f)
			th.attempts++
			th.mu.Unlock()
			if password == testCode {
				return trust.Allowed, nil
			}
			return trust.BadPassword, nil
		},
	}
	dd := proto.NewDedup()
	go func() {
		for {
			conn, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			go remote.Serve(ctx, conn, wrap(src), inj, dd, opt)
		}
	}()
	return th
}

func (h *testHost) config() Config {
	return Config{Addr: h.addr, Fingerprint: h.fp, Code: testCode, Frames: 3, SendInput: true, CheckMarkers: true}
}

func run(t *testing.T, cfg Config) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), probeDeadline)
	defer cancel()
	return Run(ctx, cfg)
}

func TestProbeAgainstAV1Host(t *testing.T) {
	h := startHost(t, synth.SceneBlocks, true)
	res, err := run(t, h.config())
	if err != nil {
		t.Fatalf("probe failed: %v (%+v)", err, res)
	}
	if !res.Connected || res.Width != screenW || res.Height != screenH || res.Frames < 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Protocol != 1 || res.HostName != testHostName {
		t.Errorf("protocol %d host %q, want 1 and %q", res.Protocol, res.HostName, testHostName)
	}
	for _, c := range []string{"text", "wheelh"} {
		if !slices.Contains(res.HostCaps, c) {
			t.Errorf("host capabilities %v lack %q", res.HostCaps, c)
		}
	}
	if res.InputsSent != 8 || res.Receipts != 8 || res.ReceiptFailures != 0 {
		t.Errorf("inputs %d, receipts %d, failures %d, want 8, 8, 0", res.InputsSent, res.Receipts, res.ReceiptFailures)
	}
	if res.MarkersOK == nil || !*res.MarkersOK {
		t.Errorf("markers = %v, want checked and fine", res.MarkersOK)
	}
	if res.DigestFirst == "" || res.DigestFirst == res.DigestLast {
		t.Errorf("digests %q / %q: the source should have animated", res.DigestFirst, res.DigestLast)
	}
	if n, _ := h.sink.Snapshot(); n != 8 {
		t.Errorf("the host accepted %d actions, want 8", n)
	}
	if typed := h.sink.Typed(); len(typed) != 1 || !strings.Contains(typed[0], "go-nx probe") {
		t.Errorf("the host typed %q", typed)
	}
	kinds := map[byte]bool{}
	for _, in := range h.sink.Inputs() {
		kinds[in.Kind] = true
	}
	for _, k := range []byte{proto.KindMouseMove, proto.KindMouseDown, proto.KindMouseUp, proto.KindWheel, proto.KindKeyDown, proto.KindKeyUp, proto.KindWheelH} {
		if !kinds[k] {
			t.Errorf("input kind %d was never sent", k)
		}
	}
}

// A viewer that speaks the original protocol gets the same service; the abilities
// the original protocol cannot announce are taken for granted.
func TestProbeSpeakingTheOriginalProtocol(t *testing.T) {
	h := startHost(t, synth.SceneBlocks, true)
	cfg := h.config()
	cfg.ForceV0 = true
	res, err := run(t, cfg)
	if err != nil {
		t.Fatalf("probe failed: %v (%+v)", err, res)
	}
	if res.Protocol != 0 || res.HostName != "" || len(res.HostCaps) != 0 {
		t.Errorf("protocol %d host %q caps %v: a v0 host says nothing about itself", res.Protocol, res.HostName, res.HostCaps)
	}
	if res.Receipts != 8 || res.ReceiptFailures != 0 {
		t.Errorf("receipts %d failures %d, want 8 and 0", res.Receipts, res.ReceiptFailures)
	}
}

// What the host has not said it can do is not tried, and is not a failure.
func TestProbeSkipsWhatTheHostCannotDo(t *testing.T) {
	h := startHost(t, synth.SceneBlocks, false)
	res, err := run(t, h.config())
	if err != nil {
		t.Fatalf("probe failed: %v (%+v)", err, res)
	}
	if res.InputsSent != 7 || res.Receipts != 7 {
		t.Errorf("sent %d, receipts %d: want 7 and 7 (no text)", res.InputsSent, res.Receipts)
	}
	if slices.Contains(res.HostCaps, "text") {
		t.Errorf("a host without a text injector advertises %v", res.HostCaps)
	}
	if len(h.sink.Typed()) != 0 {
		t.Error("text was sent to a host that cannot type")
	}
}

// A different machine at the address is refused during the handshake, before the
// code is sent and before anything is sent at all.
func TestProbeRefusesAWrongFingerprint(t *testing.T) {
	h := startHost(t, synth.SceneBlocks, true)
	cfg := h.config()
	cfg.Fingerprint = strings.Repeat("ab", 32)
	res, err := run(t, cfg)
	if err == nil || res.Connected {
		t.Fatalf("probe = %v, %+v: it should not have connected", err, res)
	}
	time.Sleep(200 * time.Millisecond)
	h.mu.Lock()
	attempts := h.attempts
	h.mu.Unlock()
	if attempts != 0 {
		t.Errorf("the host checked %d viewer(s): the code was sent to the wrong host", attempts)
	}
	if res.Error == "" {
		t.Error("the result does not say what went wrong")
	}
}

func TestProbeRefusesAWrongCode(t *testing.T) {
	h := startHost(t, synth.SceneBlocks, true)
	cfg := h.config()
	cfg.Code = "000000"
	start := time.Now()
	res, err := run(t, cfg)
	if err == nil {
		t.Fatalf("probe succeeded with a wrong code: %+v", res)
	}
	if res.Frames != 0 {
		t.Errorf("frames = %d from a host that refused the code", res.Frames)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("the probe took %v to notice that the host had hung up", time.Since(start))
	}
	if n, _ := h.sink.Snapshot(); n != 0 {
		t.Errorf("the host accepted %d actions from a refused viewer", n)
	}
}

// A still screen is sent once and then never again; asking for more frames than
// that times out, and says why.
func TestProbeOnAStillScreenTimesOutAndSaysSo(t *testing.T) {
	h := startHost(t, synth.SceneStatic, true)
	cfg := h.config()
	cfg.SendInput = false
	cfg.Frames = 3
	cfg.Timeout = 1500 * time.Millisecond
	res, err := run(t, cfg)
	if err == nil {
		t.Fatalf("probe succeeded: %+v", res)
	}
	if res.Frames != 1 {
		t.Errorf("frames = %d, want 1", res.Frames)
	}
	if !strings.Contains(err.Error(), "still") {
		t.Errorf("the error does not mention a still screen: %v", err)
	}
	cfg.Frames = 1 // one frame is all a still screen has
	if res, err := run(t, cfg); err != nil || res.Frames != 1 {
		t.Errorf("probe of a still screen for 1 frame = %v, %+v", err, res)
	}
}

func TestProbeKeepsItsIdentityInTheStateDir(t *testing.T) {
	h := startHost(t, synth.SceneBlocks, true)
	cfg := h.config()
	cfg.SendInput = false
	cfg.Frames = 1
	cfg.StateDir = t.TempDir()
	for i := 0; i < 2; i++ {
		if res, err := run(t, cfg); err != nil {
			t.Fatalf("run %d: %v (%+v)", i, err, res)
		}
	}
	fps := h.viewers()
	if len(fps) != 2 || fps[0] != fps[1] {
		t.Fatalf("the host saw viewers %v, want the same identity twice", fps)
	}
	cfg.StateDir = "" // a temporary identity, new every time
	run(t, cfg)
	if fps := h.viewers(); len(fps) != 3 || fps[2] == fps[0] {
		t.Fatalf("an unnamed state dir should give a fresh identity: %v", fps)
	}
}

func TestProbeNeedsAnAddressAndAFingerprint(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no address":     {Fingerprint: strings.Repeat("ab", 32)},
		"no fingerprint": {Addr: "127.0.0.1:1"},
	} {
		start := time.Now()
		if _, err := run(t, cfg); err == nil {
			t.Errorf("%s: probe succeeded", name)
		}
		if time.Since(start) > time.Second {
			t.Errorf("%s: took %v to say so", name, time.Since(start))
		}
	}
}

// The JSON is read by scripts: the names are a contract.
func TestResultJSONNames(t *testing.T) {
	ok := true
	b, err := json.Marshal(Result{
		Schema: SchemaVersion, Addr: "a", Connected: true, Protocol: 1, HostName: "h", HostCaps: []string{"text"},
		Width: 1, Height: 2, FirstFrameMS: 3, Frames: 4, Tiles: 5, DigestFirst: "d1", DigestLast: "d2",
		InputsSent: 6, Receipts: 7, ReceiptFailures: 8, MarkersOK: &ok, DurationMS: 9, Error: "e",
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"schema", "addr", "connected", "protocol", "hostName", "hostCaps", "width", "height",
		"firstFrameMs", "frames", "tiles", "digestFirst", "digestLast", "inputsSent", "receipts", "receiptFailures",
		"markersOk", "durationMs", "error"} {
		if _, found := m[k]; !found {
			t.Errorf("the result has no %q field: %s", k, b)
		}
	}
	if m["schema"] != float64(1) {
		t.Errorf("schema = %v, want 1", m["schema"])
	}
	// Optional things are left out rather than reported as zero.
	b, _ = json.Marshal(Result{Schema: SchemaVersion})
	for _, k := range []string{"markersOk", "error", "hostName"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("an empty result carries %q: %s", k, b)
		}
	}
}

// swapRB is a host whose capture gets the colour order wrong.
type swapRB struct{ remote.Source }

func (s swapRB) Grab(dst *tile.Frame) error {
	if err := s.Source.Grab(dst); err != nil {
		return err
	}
	for i := 0; i < len(dst.Pix); i += 4 {
		dst.Pix[i], dst.Pix[i+2] = dst.Pix[i+2], dst.Pix[i]
	}
	return nil
}

// upsideDown is a host whose capture is flipped.
type upsideDown struct{ remote.Source }

func (s upsideDown) Grab(dst *tile.Frame) error {
	if err := s.Source.Grab(dst); err != nil {
		return err
	}
	stride := dst.W * 4
	row := make([]byte, stride)
	for y := 0; y < dst.H/2; y++ {
		a, b := dst.Pix[y*stride:(y+1)*stride], dst.Pix[(dst.H-1-y)*stride:(dst.H-y)*stride]
		copy(row, a)
		copy(a, b)
		copy(b, row)
	}
	return nil
}

// The pixel path is checked end to end against a synthetic host: a wrong colour
// order or a flipped picture anywhere between the source and the decoded frame is
// reported as such, not as a vague failure.
func TestProbeNoticesWrongColoursAndOrientation(t *testing.T) {
	for name, wrap := range map[string]func(remote.Source) remote.Source{
		"red and blue swapped": func(s remote.Source) remote.Source { return swapRB{s} },
		"upside down":          func(s remote.Source) remote.Source { return upsideDown{s} },
	} {
		t.Run(name, func(t *testing.T) {
			h := startHostWith(t, synth.SceneBlocks, true, wrap)
			cfg := h.config()
			cfg.SendInput = false
			res, err := run(t, cfg)
			if err == nil || !strings.Contains(err.Error(), "marker") {
				t.Fatalf("probe = %v, want a complaint about the markers", err)
			}
			if res.MarkersOK == nil || *res.MarkersOK {
				t.Errorf("markersOk = %v, want false", res.MarkersOK)
			}
		})
	}
}

// A host that could not carry out an input says so in its receipt, and the probe
// does not call that a pass. Here the host's screen is smaller than the one it
// announces, so the pointer inputs are outside it.
func TestProbeReportsInputsTheHostFailed(t *testing.T) {
	h := startHost(t, synth.SceneBlocks, true)
	h.sink.SetScreen(100, 100)
	res, err := run(t, h.config())
	if err == nil || !strings.Contains(err.Error(), "failure") {
		t.Fatalf("probe = %v, want it to report failed receipts", err)
	}
	if res.ReceiptFailures == 0 || res.Receipts != res.InputsSent {
		t.Errorf("failures %d, receipts %d of %d sent", res.ReceiptFailures, res.Receipts, res.InputsSent)
	}
}
