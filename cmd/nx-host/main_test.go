package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/hostd"
	"github.com/orangepro2001/go-nx/internal/probe"
)

// process is realMain running in this process with pipes for stdin and stdout, as
// an application that starts nx-host as a sidecar would see it.
type process struct {
	t      *testing.T
	stdin  *io.PipeWriter
	events chan hostd.Event
	stderr *syncBuffer
	exit   chan int
	cancel context.CancelFunc

	mu  sync.Mutex
	log []hostd.Event
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func start(t *testing.T, args ...string) *process {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &process{t: t, stdin: inW, events: make(chan hostd.Event, 256), stderr: &syncBuffer{}, exit: make(chan int, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() {
		p.exit <- realMain(ctx, args, inR, outW, p.stderr)
		outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 4096), 1<<20)
		for sc.Scan() {
			var ev hostd.Event
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
				t.Errorf("stdout carries a line that is not an event: %q (%v)", sc.Text(), err)
				continue
			}
			p.mu.Lock()
			p.log = append(p.log, ev)
			p.mu.Unlock()
			p.events <- ev
		}
		close(p.events)
	}()
	t.Cleanup(func() { cancel(); inW.Close() })
	return p
}

func testArgs(extra ...string) []string {
	return append([]string{"-test-mode", "-listen", "127.0.0.1:0", "-size", "640x480", "-fps", "20", "-state-dir", ""}, extra...)
}

func (p *process) next(what string, match func(hostd.Event) bool) hostd.Event {
	p.t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-p.events:
			if !ok {
				p.t.Fatalf("stdout ended while waiting for %s", what)
			}
			if match(ev) {
				return ev
			}
		case <-deadline:
			p.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func named(n string) func(hostd.Event) bool { return func(e hostd.Event) bool { return e.Event == n } }

func (p *process) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		p.t.Fatal(err)
	}
}

func (p *process) waitExit() int {
	p.t.Helper()
	select {
	case code := <-p.exit:
		return code
	case <-time.After(20 * time.Second):
		p.t.Fatal("the process did not exit")
		return -1
	}
}

func argsWithState(t *testing.T, extra ...string) []string {
	a := testArgs(extra...)
	// Replace the empty -state-dir placeholder with a real directory.
	for i := range a {
		if a[i] == "-state-dir" {
			a[i+1] = t.TempDir()
		}
	}
	return a
}

func TestAWholeSessionOverStdio(t *testing.T) {
	p := start(t, argsWithState(t)...)
	ready := p.next("ready", named("ready"))
	if ready.Schema != hostd.SchemaVersion || !ready.TestMode || ready.Source != "pattern" || ready.InputMode != "log" {
		t.Fatalf("ready = %+v", ready)
	}
	if !strings.HasPrefix(ready.Listen, "127.0.0.1:") || strings.HasSuffix(ready.Listen, ":0") {
		t.Fatalf("listen = %q", ready.Listen)
	}
	if ready.Width != 640 || ready.Height != 480 {
		t.Errorf("size = %dx%d", ready.Width, ready.Height)
	}

	p.send(`{"id":"1","cmd":"pair","seconds":30}`)
	pairing := p.next("pairing", named("pairing"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := probe.Run(ctx, probe.Config{Addr: ready.Listen, Fingerprint: ready.Identity, Code: pairing.Code, Frames: 3, SendInput: true, CheckMarkers: true})
	if err != nil {
		t.Fatalf("probe: %v (%+v)", err, res)
	}
	p.next("connected", named("connected"))
	p.next("disconnected", named("disconnected"))

	p.send(`{"id":"2","cmd":"status"}`)
	st := p.next("status", named("status"))
	if st.Frames == nil || *st.Frames == 0 || st.Inputs == nil || *st.Inputs != uint64(res.InputsSent) {
		t.Errorf("status = %+v: frames and inputs should be counted (%d inputs were sent)", st, res.InputsSent)
	}
	p.send(`{"id":"3","cmd":"stop"}`)
	p.next("ack", named("ack"))
	if stopped := p.next("stopped", named("stopped")); stopped.Reason != "stop" {
		t.Errorf("stopped = %+v", stopped)
	}
	if code := p.waitExit(); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

func TestExitsWhenItsControllerCloses(t *testing.T) {
	p := start(t, argsWithState(t)...)
	p.next("ready", named("ready"))
	p.stdin.Close()
	if stopped := p.next("stopped", named("stopped")); stopped.Reason != "controller-closed" {
		t.Errorf("stopped = %+v", stopped)
	}
	if code := p.waitExit(); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

func TestPairAtStartupOpensTheWindowRightAfterReady(t *testing.T) {
	p := start(t, argsWithState(t, "-pair")...)
	p.next("ready", named("ready"))
	pairing := p.next("pairing", named("pairing"))
	if len(pairing.Code) != 6 || pairing.ExpiresIn == nil || *pairing.ExpiresIn <= 0 {
		t.Errorf("pairing = %+v", pairing)
	}
}

func TestNoAuthStartsInOpenModeAndSaysSo(t *testing.T) {
	p := start(t, argsWithState(t, "-no-auth")...)
	ready := p.next("ready", named("ready"))
	if ready.RequireAuth == nil || *ready.RequireAuth {
		t.Errorf("ready = %+v, want open mode", ready)
	}
	p.next("the warning", func(e hostd.Event) bool { return e.Event == "warning" && strings.Contains(e.Message, "open mode") })
}

// Without a controller to read JSON, a person reads the log: nothing on stdout.
func TestConsoleModeSpeaksToPeopleOnStderr(t *testing.T) {
	inR, _ := io.Pipe()
	var out, errb syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- realMain(ctx, argsWithState(t, "-control", "none", "-pair"), inR, &out, &errb)
	}()
	waitUntil(t, func() bool { return strings.Contains(errb.String(), "pairing code") })
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the host did not stop")
	}
	if out.String() != "" {
		t.Errorf("stdout carries %q in console mode", out.String())
	}
	for _, want := range []string{"listening on", "pairing code", "stopped"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errb.String())
		}
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A controller that stays alive but stops reading would leave a host that nobody
// can stop. The host takes it for gone, and exits with a status that says so.
func TestExitsWhenItsControllerStopsReading(t *testing.T) {
	inR, inW := io.Pipe()
	block := make(chan struct{})
	defer close(block)
	var errb syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- realMain(context.Background(), argsWithState(t, "-write-timeout", "300ms"), inR, blockedWriter{block}, &errb)
	}()
	go func() { // keep asking, so that there is always something to say
		for i := 0; i < 2000; i++ {
			if _, err := io.WriteString(inW, `{"cmd":"status"}`+"\n"); err != nil {
				return
			}
		}
	}()
	select {
	case code := <-done:
		if code != exitControllerGone {
			t.Errorf("exit code %d, want %d", code, exitControllerGone)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a host whose controller stopped reading did not exit")
	}
	if !strings.Contains(errb.String(), "controller") {
		t.Errorf("stderr does not say why: %q", errb.String())
	}
}

type blockedWriter struct{ release chan struct{} }

func (b blockedWriter) Write(p []byte) (int, error) { <-b.release; return len(p), nil }

func TestStartupProblemsAreReportedAsEventsAndExitTwo(t *testing.T) {
	cases := map[string][]string{
		"test mode wants the pattern":      {"-test-mode", "-source", "gdi"},
		"test mode wants the log injector": {"-test-mode", "-inject", "sendinput"},
		"pattern outside test mode":        {"-source", "pattern"},
		"log injector outside test mode":   {"-inject", "log"},
		"unknown source":                   {"-test-mode", "-source", "webcam"},
		"unknown scene":                    {"-test-mode", "-scene", "screensaver"},
		"bad size":                         {"-test-mode", "-size", "wide"},
		"size too small":                   {"-test-mode", "-size", "64x64"},
		"unknown control":                  {"-test-mode", "-control", "telepathy"},
		"address not an address":           {"-test-mode", "-listen", "not an address"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"-listen", "127.0.0.1:0", "-state-dir", t.TempDir()}, extra...)
			p := start(t, args...)
			e := p.next("an error", named("error"))
			if e.Message == "" || e.ErrorCode != "startup" {
				t.Errorf("error = %+v", e)
			}
			if code := p.waitExit(); code != 2 {
				t.Errorf("exit code %d, want 2", code)
			}
		})
	}
}

func TestBadFlagsExitTwo(t *testing.T) {
	var out, errb syncBuffer
	if code := realMain(context.Background(), []string{"-nonsense"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("exit code %d, want 2", code)
	}
	if errb.String() == "" {
		t.Error("nothing was said about the bad flag")
	}
}

func TestVersionIsPrintedWithoutStartingAnything(t *testing.T) {
	var out, errb syncBuffer
	if code := realMain(context.Background(), []string{"-version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.HasPrefix(out.String(), "nx-host ") {
		t.Errorf("stdout = %q", out.String())
	}
}

// Real capture and input exist only on Windows; elsewhere asking for them is an
// error that says what to do instead, not a host that pretends.
func TestRealCaptureIsRefusedWhereItDoesNotExist(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real capture exists here")
	}
	p := start(t, "-listen", "127.0.0.1:0", "-state-dir", t.TempDir())
	e := p.next("an error", named("error"))
	if !strings.Contains(e.Message, "-test-mode") {
		t.Errorf("error = %+v: it should point at -test-mode", e)
	}
	if code := p.waitExit(); code != 2 {
		t.Errorf("exit code %d", code)
	}
}

// A test-mode host that is given no state directory makes its own, so that it
// cannot touch the identity or the paired devices of a real host, and removes it.
func TestTestModeUsesAThrowawayStateDirectory(t *testing.T) {
	before := tempDirsNamed("nx-host-test-")
	p := start(t, "-test-mode", "-listen", "127.0.0.1:0", "-size", "640x480")
	p.next("ready", named("ready"))
	during := tempDirsNamed("nx-host-test-")
	if len(during) != len(before)+1 {
		t.Fatalf("temporary state directories: %d before, %d while running", len(before), len(during))
	}
	p.send(`{"cmd":"stop"}`)
	p.waitExit()
	if after := tempDirsNamed("nx-host-test-"); len(after) != len(before) {
		t.Errorf("the throwaway state directory was left behind: %d before, %d after", len(before), len(after))
	}
}

func tempDirsNamed(prefix string) []string {
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), prefix+"*"))
	return m
}
