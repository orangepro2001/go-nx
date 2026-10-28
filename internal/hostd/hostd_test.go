package hostd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/logsink"
	"github.com/orangepro2001/go-nx/internal/probe"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// memEmitter keeps every event, in order, and lets a test wait for the next one it
// has not looked at yet that matches.
type memEmitter struct {
	mu     sync.Mutex
	events []Event
	next   int
	wake   chan struct{}
}

func newMemEmitter() *memEmitter { return &memEmitter{wake: make(chan struct{}, 1)} }

func (m *memEmitter) Emit(ev Event) {
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *memEmitter) all() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.events...)
}

// wait returns the first event after the ones already consumed that matches, and
// consumes everything up to and including it.
func (m *memEmitter) wait(t *testing.T, what string, match func(Event) bool) Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		m.mu.Lock()
		for i := m.next; i < len(m.events); i++ {
			if match(m.events[i]) {
				m.next = i + 1
				ev := m.events[i]
				m.mu.Unlock()
				return ev
			}
		}
		m.mu.Unlock()
		select {
		case <-m.wake:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; events so far: %s", what, dump(m.all()))
			return Event{}
		}
	}
}

// waitAnywhere finds a matching event among all of them, those already looked at
// included, and consumes none. It is for events whose order against an answer is
// not fixed: an answer is sent from the goroutine that handled the command, a
// session ends on its own.
func (m *memEmitter) waitAnywhere(t *testing.T, what string, match func(Event) bool) Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		for _, ev := range m.all() {
			if match(ev) {
				return ev
			}
		}
		select {
		case <-m.wake:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s; events so far: %s", what, dump(m.all()))
			return Event{}
		}
	}
}

func disconnectedFor(reason string) func(Event) bool {
	return func(e Event) bool { return e.Event == "disconnected" && e.Reason == reason }
}

func dump(evs []Event) string {
	var b strings.Builder
	for _, e := range evs {
		j, _ := json.Marshal(e)
		b.Write(j)
		b.WriteByte(' ')
	}
	return b.String()
}

func named(name string) func(Event) bool { return func(e Event) bool { return e.Event == name } }

type rig struct {
	t    *testing.T
	h    *Host
	em   *memEmitter
	sink *logsink.Sink
	src  *synth.Source
	cmd  *io.PipeWriter
	dir  string
}

type rigOpt func(*Config)

func withNoAuth(c *Config)     { c.NoAuth = true }
func withFPS(fps int) rigOpt   { return func(c *Config) { c.FPS = fps } }
func withBoost(fps int) rigOpt { return func(c *Config) { c.Boost = fps } }
func inDir(dir string) rigOpt  { return func(c *Config) { c.StateDir = dir } }

// startHost runs a host on loopback with a synthetic screen and a log-only injector,
// controlled through a pipe.
func startHost(t *testing.T, opts ...rigOpt) *rig {
	t.Helper()
	be, sink, err := PatternBackend(synth.SceneBlocks, 640, 480, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Listen: "127.0.0.1:0", StateDir: t.TempDir(), FPS: 20, Version: "test", Backend: be}
	for _, o := range opts {
		o(&cfg)
	}
	em := newMemEmitter()
	h, err := New(cfg, em)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	r := &rig{t: t, h: h, em: em, sink: sink, src: be.Source.(*synth.Source), cmd: pw, dir: cfg.StateDir}
	h.Start(context.Background())
	go ReadCommands(pr, h)
	t.Cleanup(func() {
		pw.Close()
		h.Stop("test over")
		h.Wait()
	})
	return r
}

func (r *rig) send(format string, args ...any) {
	r.t.Helper()
	if _, err := fmt.Fprintf(r.cmd, format+"\n", args...); err != nil {
		r.t.Fatalf("writing a command: %v", err)
	}
}

// pair opens a pairing window and returns the code.
func (r *rig) pair() string {
	r.t.Helper()
	r.send(`{"cmd":"pair","seconds":60}`)
	return r.em.wait(r.t, "the pairing code", func(e Event) bool { return e.Event == "pairing" && e.Code != "" }).Code
}

func (r *rig) probeCfg(code, stateDir string) probe.Config {
	return probe.Config{Addr: r.h.Addr(), Fingerprint: r.h.Identity(), Code: code, StateDir: stateDir, Frames: 2, SendInput: true}
}

func probeOnce(t *testing.T, cfg probe.Config) (probe.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return probe.Run(ctx, cfg)
}

func TestReadyDescribesTheHost(t *testing.T) {
	r := startHost(t)
	ev := r.em.wait(t, "ready", named("ready"))
	if ev.Schema != SchemaVersion || ev.Version != "test" || ev.PID == 0 {
		t.Errorf("ready = %+v", ev)
	}
	if !strings.HasPrefix(ev.Listen, "127.0.0.1:") || strings.HasSuffix(ev.Listen, ":0") {
		t.Errorf("listen = %q: it must be the address actually bound, with the port the system chose", ev.Listen)
	}
	if len(ev.Identity) != 64 || ev.Identity != r.h.Identity() {
		t.Errorf("identity = %q", ev.Identity)
	}
	if ev.RequireAuth == nil || !*ev.RequireAuth {
		t.Errorf("a host starts in strict mode: requireAuth = %v", ev.RequireAuth)
	}
	if !ev.TestMode || ev.Source != "pattern" || ev.InputMode != "log" || ev.Width != 640 || ev.Height != 480 || ev.FPS != 20 {
		t.Errorf("ready = %+v", ev)
	}
	if ev.Paired == nil || *ev.Paired != 0 {
		t.Errorf("paired = %v, want 0", ev.Paired)
	}
	r.em.wait(t, "the test-mode warning", func(e Event) bool { return e.Event == "warning" && strings.Contains(e.Message, "TEST MODE") })
	if evs := r.em.all(); evs[0].Event != "ready" {
		t.Errorf("the first event is %q, want ready", evs[0].Event)
	}
}

func TestPairingLetsADeviceInAndItStaysPaired(t *testing.T) {
	r := startHost(t)
	r.send(`{"id":"p1","cmd":"pair","seconds":60}`)
	pairing := r.em.wait(t, "pairing", named("pairing"))
	if pairing.ID != "p1" || len(pairing.Code) != 6 || pairing.ExpiresIn == nil || *pairing.ExpiresIn != 60 {
		t.Fatalf("pairing = %+v", pairing)
	}
	viewerDir := t.TempDir()
	res, err := probeOnce(t, r.probeCfg(pairing.Code, viewerDir))
	if err != nil {
		t.Fatalf("probe with the pairing code: %v (%+v)", err, res)
	}
	connected := r.em.wait(t, "connected", named("connected"))
	if connected.Addr == "" || len(connected.Identity) != 64 || connected.Protocol == nil || *connected.Protocol != 1 {
		t.Errorf("connected = %+v", connected)
	}
	r.em.waitAnywhere(t, "disconnected (closed)", disconnectedFor("closed"))

	// The same device again, with no code: it is paired now.
	if res, err := probeOnce(t, r.probeCfg("", viewerDir)); err != nil {
		t.Fatalf("a paired device was refused: %v (%+v)", err, res)
	}
	r.send(`{"id":2,"cmd":"status"}`)
	st := r.em.wait(t, "status", named("status"))
	if st.ID != "2" || len(st.Trusted) != 1 || st.Trusted[0] != connected.Identity {
		t.Errorf("status = %+v, want the one paired device %s", st, connected.Identity)
	}
	// And a different device with no code is not.
	if _, err := probeOnce(t, r.probeCfg("", t.TempDir())); err == nil {
		t.Fatal("an unpaired device got in without a code")
	}
	denied := r.em.wait(t, "denied", named("denied"))
	if denied.Reason != "untrusted" || denied.Addr == "" {
		t.Errorf("denied = %+v", denied)
	}
}

func TestPairCancelClosesTheWindow(t *testing.T) {
	r := startHost(t)
	code := r.pair()
	r.send(`{"id":"c","cmd":"pair-cancel"}`)
	ev := r.em.wait(t, "the window closing", func(e Event) bool { return e.Event == "pairing" && e.Code == "" })
	if ev.ID != "c" || ev.ExpiresIn == nil || *ev.ExpiresIn != 0 {
		t.Errorf("pairing = %+v", ev)
	}
	if _, err := probeOnce(t, r.probeCfg(code, t.TempDir())); err == nil {
		t.Fatal("a code was accepted after the window was cancelled")
	}
	if d := r.em.wait(t, "denied", named("denied")); d.Reason != "bad-code" && d.Reason != "untrusted" {
		t.Errorf("denied = %+v", d)
	}
}

func TestWrongCodeIsReportedAsBadCode(t *testing.T) {
	r := startHost(t)
	r.pair()
	if _, err := probeOnce(t, r.probeCfg("000000", t.TempDir())); err == nil {
		t.Fatal("a wrong code was accepted")
	}
	if d := r.em.wait(t, "denied", named("denied")); d.Reason != "bad-code" {
		t.Errorf("denied = %+v, want bad-code", d)
	}
}

// liveViewer connects a viewer and keeps it connected until it ends; ended receives
// the error it ended with.
func liveViewer(t *testing.T, r *rig, id tls.Certificate, code string) (ended chan error, c *remote.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	c, err := remote.DialWith(ctx, r.h.Addr(), id, nil, remote.DialOptions{Password: code, ExpectFingerprint: r.h.Identity()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(c.Close)
	ended = make(chan error, 1)
	go func() { ended <- c.Run(ctx, func(*tile.Frame, []tile.Rect) {}, func(uint64, byte) {}) }()
	return ended, c
}

func identityIn(t *testing.T) tls.Certificate {
	t.Helper()
	id, err := trust.Identity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitEnded(t *testing.T, ended chan error, what string) error {
	t.Helper()
	select {
	case err := <-ended:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: the viewer's session did not end", what)
		return nil
	}
}

func TestUntrustEndsTheDevicesSessionAndItsPairing(t *testing.T) {
	r := startHost(t)
	id := identityIn(t)
	ended, _ := liveViewer(t, r, id, r.pair())
	connected := r.em.wait(t, "connected", named("connected"))

	r.send(`{"id":"u","cmd":"untrust","fingerprint":"%s"}`, strings.ToUpper(connected.Identity))
	st := r.em.wait(t, "status", named("status"))
	if st.ID != "u" || len(st.Trusted) != 0 {
		t.Errorf("status = %+v, want no paired devices", st)
	}
	if err := waitEnded(t, ended, "after untrust"); err == nil || !strings.Contains(err.Error(), "5") && !strings.Contains(err.Error(), "remov") {
		t.Logf("viewer ended with: %v", err)
	}
	r.em.waitAnywhere(t, "disconnected (revoked)", disconnectedFor("revoked"))
	// It cannot come back without pairing again.
	if _, err := remote.DialWith(context.Background(), r.h.Addr(), id, nil, remote.DialOptions{ExpectFingerprint: r.h.Identity()}); err == nil {
		// The handshake succeeds; the host refuses after the hello.
		r.em.wait(t, "denied", named("denied"))
	}
}

func TestUntrustOfAnUnknownDeviceIsAnError(t *testing.T) {
	r := startHost(t)
	r.send(`{"id":"x","cmd":"untrust","fingerprint":"%s"}`, strings.Repeat("a", 64))
	e := r.em.wait(t, "error", named("error"))
	if e.ID != "x" || e.ErrorCode != "not-found" {
		t.Errorf("error = %+v", e)
	}
}

func TestUntrustAllForgetsEveryDevice(t *testing.T) {
	r := startHost(t)
	ended, _ := liveViewer(t, r, identityIn(t), r.pair())
	r.em.wait(t, "connected", named("connected"))
	r.send(`{"cmd":"untrust-all"}`)
	st := r.em.wait(t, "status", named("status"))
	if len(st.Trusted) != 0 {
		t.Errorf("trusted = %v", st.Trusted)
	}
	waitEnded(t, ended, "after untrust-all")
	r.em.waitAnywhere(t, "disconnected (revoked)", disconnectedFor("revoked"))
}

// Open mode lets anyone in; switching to strict ends the sessions that were let in
// only because of it, and leaves a paired device alone.
func TestSwitchingToStrictEndsUnverifiedSessionsOnly(t *testing.T) {
	r := startHost(t, withNoAuth)
	r.em.wait(t, "the open-mode warning", func(e Event) bool { return e.Event == "warning" && strings.Contains(e.Message, "open mode") })

	// A stranger, let in because the host is open.
	ended, _ := liveViewer(t, r, identityIn(t), "")
	r.em.wait(t, "connected", named("connected"))
	r.send(`{"id":"m","cmd":"mode","mode":"strict"}`)
	st := r.em.wait(t, "status", named("status"))
	if st.ID != "m" || st.RequireAuth == nil || !*st.RequireAuth || st.Dropped == nil || *st.Dropped != 1 {
		t.Errorf("status = %+v, want strict and 1 session dropped", st)
	}
	waitEnded(t, ended, "after switching to strict")
	r.em.waitAnywhere(t, "disconnected (unverified)", disconnectedFor("unverified"))

	// A paired device, in open mode, is verified: strict mode does not touch it.
	r.send(`{"cmd":"mode","mode":"open"}`)
	if st := r.em.wait(t, "status", named("status")); st.RequireAuth == nil || *st.RequireAuth {
		t.Fatalf("status = %+v, want open", st)
	}
	paired := identityIn(t)
	pairedEnded, _ := liveViewer(t, r, paired, r.pair())
	r.em.wait(t, "connected", named("connected"))
	r.send(`{"cmd":"mode","mode":"strict"}`)
	if st := r.em.wait(t, "status", named("status")); st.Dropped == nil || *st.Dropped != 0 {
		t.Errorf("status = %+v: the paired device should not have been dropped", st)
	}
	select {
	case err := <-pairedEnded:
		t.Fatalf("the paired device's session ended: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestRequestIDsAreEchoedAsTheyWereSent(t *testing.T) {
	r := startHost(t)
	for _, c := range []struct{ sent, want string }{
		{`"abc"`, "abc"}, {`7`, "7"}, {`"7"`, "7"}, {`12345678901234`, "12345678901234"}, {`1.5`, "1.5"},
	} {
		r.send(`{"id":%s,"cmd":"status"}`, c.sent)
		if st := r.em.wait(t, "status", named("status")); st.ID != c.want {
			t.Errorf("id %s was answered with %q, want %q", c.sent, st.ID, c.want)
		}
	}
	r.send(`{"cmd":"status"}`)
	if st := r.em.wait(t, "status", named("status")); st.ID != "" {
		t.Errorf("a command without an id was answered with id %q", st.ID)
	}
	// An id that is not a string or a number is refused, and the refusal still
	// names the id if it can.
	r.send(`{"id":{"a":1},"cmd":"status"}`)
	if e := r.em.wait(t, "error", named("error")); e.ErrorCode != "bad-json" {
		t.Errorf("error = %+v", e)
	}
	r.send(`{"id":"%s","cmd":"status"}`, strings.Repeat("x", 65))
	if e := r.em.wait(t, "error", named("error")); e.ErrorCode != "bad-json" {
		t.Errorf("an id of 65 bytes: %+v", e)
	}
}

func TestBadCommandsAreAnsweredAndNeverStopTheHost(t *testing.T) {
	r := startHost(t)
	cases := []struct{ line, code, id string }{
		{`not json at all`, "bad-json", ""},
		{`{"cmd":`, "bad-json", ""},
		{`[1,2,3]`, "bad-json", ""},
		{`{"id":"1"}`, "bad-command", "1"},
		{`{"id":"2","cmd":"dance"}`, "unknown-command", "2"},
		{`{"id":"3","cmd":"fps","value":0}`, "bad-argument", "3"},
		{`{"id":"4","cmd":"fps","value":61}`, "bad-argument", "4"},
		{`{"id":"5","cmd":"fps","value":"fast"}`, "bad-json", "5"},
		{`{"id":"6","cmd":"pair","seconds":-1}`, "bad-argument", "6"},
		{`{"id":"7","cmd":"pair","seconds":3601}`, "bad-argument", "7"},
		{`{"id":"8","cmd":"untrust"}`, "bad-argument", "8"},
		{`{"id":"9","cmd":"mode","mode":"half"}`, "bad-argument", "9"},
		{`{"id":"10","cmd":"status","schema":99}`, "unsupported-schema", "10"},
	}
	for _, c := range cases {
		r.send("%s", c.line)
		e := r.em.wait(t, "the answer to "+c.line, named("error"))
		if e.ErrorCode != c.code || e.ID != c.id || e.Message == "" {
			t.Errorf("%s: error = %+v, want code %q id %q and a message", c.line, e, c.code, c.id)
		}
	}
	// Still alive and well.
	r.send(`{"id":"last","cmd":"status"}`)
	if st := r.em.wait(t, "status", named("status")); st.ID != "last" || st.FPS != 20 {
		t.Errorf("status = %+v", st)
	}
	r.send("")                                                 // an empty line is nothing
	r.send("   ")                                              // and so is a blank one
	r.send(`{"cmd":"status","somethingNew":{"nested":[1,2]}}`) // fields it does not know are ignored
	r.em.wait(t, "status", named("status"))
}

func TestAnOverlongLineIsSkippedAndTheNextOneRead(t *testing.T) {
	r := startHost(t)
	r.send(`{"id":"big","cmd":"status","pad":"%s"}`, strings.Repeat("x", 3*MaxLineBytes))
	if e := r.em.wait(t, "the error", named("error")); e.ErrorCode != "line-too-long" {
		t.Fatalf("error = %+v", e)
	}
	r.send(`{"id":"after","cmd":"status"}`)
	if st := r.em.wait(t, "status", named("status")); st.ID != "after" {
		t.Fatalf("status = %+v", st)
	}
	// A line right at the limit is fine.
	pad := MaxLineBytes - len(`{"cmd":"status","p":""}`)
	r.send(`{"cmd":"status","p":"%s"}`, strings.Repeat("y", pad))
	r.em.wait(t, "status for a line at the limit", named("status"))
}

func TestStopAnswersAndStopsTheHost(t *testing.T) {
	r := startHost(t)
	r.send(`{"id":"bye","cmd":"stop"}`)
	if ack := r.em.wait(t, "ack", named("ack")); ack.ID != "bye" {
		t.Errorf("ack = %+v", ack)
	}
	done := make(chan struct{})
	go func() { r.h.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the host did not stop")
	}
	evs := r.em.all()
	if last := evs[len(evs)-1]; last.Event != "stopped" || last.Reason != "stop" {
		t.Errorf("the last event is %+v, want stopped / stop", last)
	}
}

// The controller owns the host: when it goes away, so does the host.
func TestTheControllerClosingItsEndStopsTheHost(t *testing.T) {
	r := startHost(t)
	r.cmd.Close()
	done := make(chan struct{})
	go func() { r.h.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the host did not stop when its controller closed the pipe")
	}
	if last := r.em.all(); last[len(last)-1].Reason != "controller-closed" {
		t.Errorf("the last event is %+v", last[len(last)-1])
	}
}

func TestStopReasonIsTheFirstGiven(t *testing.T) {
	r := startHost(t)
	r.h.Stop("first")
	r.h.Stop("second")
	r.h.Wait()
	evs := r.em.all()
	if last := evs[len(evs)-1]; last.Reason != "first" {
		t.Errorf("reason = %q, want first", last.Reason)
	}
}

// grabsOver is how many pictures the synthetic screen produced in d.
func grabsOver(r *rig, d time.Duration) uint64 {
	before := r.src.Frames()
	time.Sleep(d)
	return r.src.Frames() - before
}

func TestFPSCommandChangesTheCaptureRate(t *testing.T) {
	r := startHost(t, withFPS(5))
	_, _ = liveViewer(t, r, identityIn(t), r.pair())
	r.em.wait(t, "connected", named("connected"))
	slow := grabsOver(r, 800*time.Millisecond)
	r.send(`{"cmd":"fps","value":50}`)
	if st := r.em.wait(t, "status", named("status")); st.FPS != 50 {
		t.Fatalf("status = %+v", st)
	}
	time.Sleep(300 * time.Millisecond) // a new rate is noticed at the next tick of the old one
	fast := grabsOver(r, 800*time.Millisecond)
	if slow > 8 || fast < 20 || fast < 3*slow {
		t.Errorf("%d pictures in 0.8 s at 5 per second, %d at 50", slow, fast)
	}
}

// While the viewer is working the host speeds up by itself, and falls back.
func TestTheCaptureRateRisesWhileTheViewerWorks(t *testing.T) {
	r := startHost(t, withFPS(4), withBoost(40))
	_, c := liveViewer(t, r, identityIn(t), r.pair())
	r.em.wait(t, "connected", named("connected"))
	idle := grabsOver(r, 1000*time.Millisecond)

	stop := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
				c.SendInput(moveTo(int32(i%600), 100))
			}
		}
	}()
	time.Sleep(400 * time.Millisecond)
	busy := grabsOver(r, 1000*time.Millisecond)
	close(stop)
	if idle > 8 || busy < 15 {
		t.Errorf("%d pictures in a second while idle, %d while the viewer was moving the pointer", idle, busy)
	}
	time.Sleep(3 * time.Second) // the boost holds for a couple of seconds, then lets go
	if again := grabsOver(r, 1000*time.Millisecond); again > 8 {
		t.Errorf("%d pictures in a second after the viewer stopped: the rate did not fall back", again)
	}
	if _, inputs := r.h.Counts(); inputs == 0 {
		t.Error("no input was counted")
	}
}

// Pairing survives a restart: the identity and the paired devices are the state.
func TestStateSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := startHost(t, inDir(dir))
	viewerDir := t.TempDir()
	if _, err := probeOnce(t, first.probeCfg(first.pair(), viewerDir)); err != nil {
		t.Fatal(err)
	}
	identity := first.h.Identity()
	first.h.Stop("restart")
	first.h.Wait()

	second := startHost(t, inDir(dir))
	if second.h.Identity() != identity {
		t.Fatal("the host's identity changed across a restart: every viewer's pin would break")
	}
	ev := second.em.wait(t, "ready", named("ready"))
	if ev.Paired == nil || *ev.Paired != 1 {
		t.Errorf("paired = %v after a restart, want 1", ev.Paired)
	}
	if res, err := probeOnce(t, second.probeCfg("", viewerDir)); err != nil {
		t.Fatalf("the paired device was refused after a restart: %v (%+v)", err, res)
	}
}

func TestACorruptPairedDevicesFileIsSetAsideNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/trusted.json", "{this is not json"); err != nil {
		t.Fatal(err)
	}
	r := startHost(t, inDir(dir))
	w := r.em.wait(t, "the warning", func(e Event) bool { return e.Event == "warning" && strings.Contains(e.Message, "set aside") })
	if !strings.Contains(w.Message, "trusted.json") {
		t.Errorf("warning = %q", w.Message)
	}
	if ev := r.em.wait(t, "ready", named("ready")); ev.Paired == nil || *ev.Paired != 0 {
		t.Errorf("ready = %+v", ev)
	}
}

func TestANewHostNeedsABackendAndAStateDir(t *testing.T) {
	be, _, _ := PatternBackend(synth.SceneStatic, 640, 480, nil)
	if _, err := New(Config{Listen: "127.0.0.1:0", Backend: be}, newMemEmitter()); err == nil {
		t.Error("a host without a state directory was created")
	}
	if _, err := New(Config{Listen: "127.0.0.1:0", StateDir: t.TempDir()}, newMemEmitter()); err == nil {
		t.Error("a host without a backend was created")
	}
	// A port that is taken is reported, with a hint.
	r := startHost(t)
	_, err := New(Config{Listen: r.h.Addr(), StateDir: t.TempDir(), Backend: be}, newMemEmitter())
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("listening on a port in use: %v", err)
	}
}

// Every line a LineEmitter writes is one JSON object with an event name.
func TestLineEmitterWritesOneObjectPerLineInOrder(t *testing.T) {
	var buf lockedBuffer
	e := NewLineEmitter(&buf, time.Second, nil)
	for i := 0; i < 200; i++ {
		e.Emit(Event{Event: "status", FPS: i + 1})
	}
	e.Close()
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 200 {
		t.Fatalf("%d lines, want 200", len(lines))
	}
	for i, l := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(l), &ev); err != nil || ev.Event != "status" || ev.FPS != i+1 {
			t.Fatalf("line %d = %q (%v)", i, l, err)
		}
	}
	e.Emit(Event{Event: "late"}) // after Close: ignored, not a panic
	e.Close()
}

// A controller that stops reading is gone: the writer is stuck, and the owner told.
func TestLineEmitterNoticesAControllerThatStoppedReading(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	gone := make(chan string, 4)
	e := NewLineEmitter(blockedWriter{block}, 200*time.Millisecond, func(reason string) { gone <- reason })
	start := time.Now()
	for i := 0; i < 10; i++ {
		e.Emit(Event{Event: "status"}) // queues; never waits for the writer
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("queuing ten events took %v", time.Since(start))
	}
	select {
	case reason := <-gone:
		if reason != "write-timeout" {
			t.Errorf("reason = %q", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a writer that never returns was not noticed")
	}
	select {
	case reason := <-gone:
		t.Errorf("the owner was told twice (%q)", reason)
	case <-time.After(400 * time.Millisecond):
	}
}

func TestLineEmitterNoticesABrokenPipe(t *testing.T) {
	gone := make(chan string, 4)
	e := NewLineEmitter(failingWriter{}, time.Second, func(reason string) { gone <- reason })
	e.Emit(Event{Event: "status"})
	select {
	case reason := <-gone:
		if reason != "write-failed" {
			t.Errorf("reason = %q", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a failing writer was not noticed")
	}
	// Emitting goes on without blocking even though nothing is being written.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 3*emitQueue; i++ {
			e.Emit(Event{Event: "status"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked on a dead pipe")
	}
}

// The queue being full is not a failure for as long as the controller is reading.
func TestLineEmitterBackpressureWaitsForASlowReader(t *testing.T) {
	var buf lockedBuffer
	w := &slowWriter{w: &buf, delay: 2 * time.Millisecond}
	gone := make(chan string, 1)
	e := NewLineEmitter(w, 5*time.Second, func(r string) { gone <- r })
	for i := 0; i < emitQueue+100; i++ {
		e.Emit(Event{Event: "status", FPS: i + 1})
	}
	e.Close()
	select {
	case r := <-gone:
		t.Fatalf("a slow but reading controller was declared gone (%s)", r)
	default:
	}
	if n := strings.Count(buf.String(), "\n"); n != emitQueue+100 {
		t.Fatalf("%d lines written, want %d", n, emitQueue+100)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type blockedWriter struct{ release chan struct{} }

func (b blockedWriter) Write(p []byte) (int, error) { <-b.release; return len(p), nil }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type slowWriter struct {
	w     io.Writer
	delay time.Duration
}

func (s *slowWriter) Write(p []byte) (int, error) { time.Sleep(s.delay); return s.w.Write(p) }

func TestDisconnectReasonsAreStableWords(t *testing.T) {
	appErr := func(code uint64, remote bool) error {
		return &quic.ApplicationError{ErrorCode: quic.ApplicationErrorCode(code), Remote: remote}
	}
	cases := []struct {
		name       string
		cause, err error
		want       string
	}{
		{"replaced by a newer viewer", appErr(uint64(remote.CloseReplaced), false), nil, "replaced"},
		{"revoked", appErr(uint64(remote.CloseRevoked), false), nil, "revoked"},
		{"switched to strict", appErr(uint64(remote.CloseUnverified), false), nil, "unverified"},
		{"the viewer hung up, seen as an error", nil, appErr(0, true), "closed"},
		{"the viewer hung up, seen as a closed connection", nil, nil, "closed"},
		{"the viewer hung up, wrapped", nil, fmt.Errorf("remote: %w", appErr(0, true)), "closed"},
		{"the viewer ended its control stream", nil, io.EOF, "closed"},
		{"the viewer went silent", &quic.IdleTimeoutError{}, nil, "timeout"},
		{"the host stopped", nil, context.Canceled, "closed"},
		{"something else", nil, io.ErrUnexpectedEOF, "error"},
		// A code that only looks like ours, sent by the viewer, is not our doing.
		{"a viewer that closes with our code", appErr(uint64(remote.CloseRevoked), true), nil, "closed"},
	}
	for _, c := range cases {
		if got, _ := disconnectReason(c.cause, c.err); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
