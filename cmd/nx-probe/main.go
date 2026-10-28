// Command nx-probe connects to a running go-nx host, checks that pictures arrive
// and, with -input, that one of every kind of input is acknowledged. It is an
// acceptance tool: it captures and injects nothing itself, and on its own it
// proves only the protocol path (see internal/probe).
//
// Exit status: 0 the probe passed, 1 it failed, 2 the command line was wrong.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/orangepro2001/go-nx/internal/probe"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nx-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		addr    = fs.String("addr", "", "host address, e.g. 100.64.0.1:7000")
		fp      = fs.String("fingerprint", "", "the host's certificate SHA-256 fingerprint (the ID its main window shows)")
		code    = fs.String("code", "", "pairing code or password")
		state   = fs.String("state-dir", "", "directory for this probe's identity (default: a temporary one)")
		frames  = fs.Int("frames", 3, "number of pictures to receive")
		timeout = fs.Duration("timeout", 30*time.Second, "overall deadline")
		input   = fs.Bool("input", false, "send one of each kind of input and a text, and wait for the receipts")
		text    = fs.String("text", "", "text to send with -input (default: a short test phrase)")
		v0      = fs.Bool("v0", false, "speak the original protocol instead of version 1")
		cursor  = fs.Bool("cursor", false, "ask the host to draw the pointer into the picture")
		markers = fs.Bool("markers", false, "check the orientation markers of a synthetic host (fails against a real desktop)")
		asJSON  = fs.Bool("json", false, "print the result as one line of JSON")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *addr == "" || *fp == "" {
		fmt.Fprintln(stderr, "nx-probe: -addr and -fingerprint are required")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := probe.Run(ctx, probe.Config{
		Addr: *addr, Fingerprint: *fp, Code: *code, StateDir: *state, Frames: *frames, Timeout: *timeout,
		SendInput: *input, Text: *text, ForceV0: *v0, WantCursor: *cursor, CheckMarkers: *markers,
	})
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(res)
	} else {
		printResult(stdout, res)
	}
	if err != nil {
		fmt.Fprintln(stderr, "FAIL:", err)
		return 1
	}
	if !*asJSON {
		fmt.Fprintln(stdout, "OK: the probe passed")
	}
	return 0
}

func printResult(w io.Writer, res probe.Result) {
	fmt.Fprintf(w, "connected: %v\n", res.Connected)
	if !res.Connected {
		return
	}
	fmt.Fprintf(w, "protocol: %d\n", res.Protocol)
	if res.HostName != "" {
		fmt.Fprintf(w, "host: %s %v\n", res.HostName, res.HostCaps)
	}
	fmt.Fprintf(w, "screen: %dx%d\n", res.Width, res.Height)
	fmt.Fprintf(w, "first picture: %d ms\n", res.FirstFrameMS)
	fmt.Fprintf(w, "frames: %d (%d tile updates)\n", res.Frames, res.Tiles)
	fmt.Fprintf(w, "digest first/last: %s / %s\n", short(res.DigestFirst), short(res.DigestLast))
	if res.InputsSent > 0 {
		fmt.Fprintf(w, "inputs: %d sent, %d receipts, %d failed\n", res.InputsSent, res.Receipts, res.ReceiptFailures)
	}
	if res.MarkersOK != nil {
		fmt.Fprintf(w, "markers: %v\n", *res.MarkersOK)
	}
	fmt.Fprintf(w, "duration: %d ms\n", res.DurationMS)
}

func short(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}
