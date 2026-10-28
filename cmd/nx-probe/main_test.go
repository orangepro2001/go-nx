package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orangepro2001/go-nx/internal/logsink"
	"github.com/orangepro2001/go-nx/internal/probe"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/synth"
	"github.com/orangepro2001/go-nx/internal/trust"
)

const code = "424242"

func startHost(t *testing.T) (addr, fp string) {
	t.Helper()
	id, err := trust.Identity(filepath.Join(t.TempDir(), "host"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := remote.Listen("127.0.0.1:0", id)
	if err != nil {
		t.Fatal(err)
	}
	src, err := synth.New(640, 480)
	if err != nil {
		t.Fatal(err)
	}
	sink := logsink.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); ln.Close() })
	dd := proto.NewDedup()
	go func() {
		for {
			conn, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			go remote.Serve(ctx, conn, src, sink, dd, remote.Options{FPS: 20, Check: func(_, _, pw string) (trust.Verdict, error) {
				if pw == code {
					return trust.Allowed, nil
				}
				return trust.BadPassword, nil
			}})
		}
	}()
	return ln.Addr().String(), trust.Fingerprint(id.Certificate[0])
}

func runCLI(args ...string) (exit int, stdout, stderr string) {
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb)
	return exit, out.String(), errb.String()
}

func TestUsageErrorsExitWithTwo(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-addr", "127.0.0.1:1"},
		{"-fingerprint", "ab"},
		{"-nonsense"},
		{"-frames", "many"},
	} {
		exit, _, stderr := runCLI(args...)
		if exit != 2 {
			t.Errorf("%v: exit %d, want 2", args, exit)
		}
		if stderr == "" {
			t.Errorf("%v: nothing was said on stderr", args)
		}
	}
}

func TestJSONResultOnSuccess(t *testing.T) {
	addr, fp := startHost(t)
	exit, stdout, stderr := runCLI("-addr", addr, "-fingerprint", fp, "-code", code, "-input", "-markers", "-json", "-timeout", "20s")
	if exit != 0 {
		t.Fatalf("exit %d, stderr %q, stdout %q", exit, stderr, stdout)
	}
	var res probe.Result
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("stdout is not one JSON result: %v\n%s", err, stdout)
	}
	if res.Schema != probe.SchemaVersion || !res.Connected || res.Frames < 3 || res.Receipts != res.InputsSent || res.InputsSent == 0 {
		t.Errorf("unexpected result %+v", res)
	}
	if res.MarkersOK == nil || !*res.MarkersOK {
		t.Errorf("markers = %v", res.MarkersOK)
	}
	if strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
		t.Errorf("-json should print exactly one line: %q", stdout)
	}
}

func TestTextOutputOnSuccess(t *testing.T) {
	addr, fp := startHost(t)
	exit, stdout, stderr := runCLI("-addr", addr, "-fingerprint", fp, "-code", code, "-frames", "2", "-v0")
	if exit != 0 {
		t.Fatalf("exit %d, stderr %q", exit, stderr)
	}
	for _, want := range []string{"connected: true", "protocol: 0", "screen: 640x480", "frames:", "OK"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestFailureExitsWithOneAndExplains(t *testing.T) {
	addr, fp := startHost(t)
	wrong := strings.Repeat("0", len(fp))
	exit, stdout, stderr := runCLI("-addr", addr, "-fingerprint", wrong, "-code", code, "-timeout", "5s")
	if exit != 1 {
		t.Fatalf("exit %d, want 1", exit)
	}
	if !strings.Contains(stderr, "FAIL:") || strings.Contains(stdout, "OK") {
		t.Errorf("stderr %q, stdout %q", stderr, stdout)
	}
	// The same, for a script that reads JSON: still one result, with the reason in it.
	exit, stdout, _ = runCLI("-addr", addr, "-fingerprint", wrong, "-code", code, "-timeout", "5s", "-json")
	if exit != 1 {
		t.Fatalf("-json exit %d, want 1", exit)
	}
	var res probe.Result
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || res.Error == "" || res.Connected {
		t.Errorf("-json failure output: %v, %+v", err, res)
	}
}
