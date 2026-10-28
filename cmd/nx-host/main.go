// Command nx-host shares one monitor over the go-nx protocol without a window. An
// application starts it as a sidecar and talks to it in JSON Lines on stdin and
// stdout (docs/nx-host.md): commands in, events out. It never edits the display
// topology; pairing is opened on request and bound to one code at a time.
//
// With -test-mode the screen is a synthetic picture and input is only logged, so
// the whole program can be run, and probed with nx-probe, on any machine without
// touching its desktop. Real capture and input exist on Windows only.
//
// Exit status: 0 stopped normally, 2 could not start, 3 the controller stopped
// reading its events (nothing could be reported to it, so the host stops).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/orangepro2001/go-nx/internal/hostd"
	"github.com/orangepro2001/go-nx/internal/synth"
)

// version is set by build.sh with -ldflags -X.
var version = "dev"

const (
	exitStartup        = 2
	exitControllerGone = 3
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(realMain(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type options struct {
	listen       string
	display      string
	layered      bool
	fps, boost   int
	idle         int
	stateDir     string
	pair, noAuth bool
	allowWinL    bool
	control      string
	testMode     bool
	source       string
	inject       string
	scene        string
	size         string
	writeTimeout time.Duration
}

func realMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var o options
	fs := flag.NewFlagSet("nx-host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.listen, "listen", "127.0.0.1:7000", "UDP address to listen on; use 0.0.0.0:7000 to be reachable from the network")
	fs.StringVar(&o.display, "display", "primary", `monitor to share: "primary", an index, or a device name such as \\.\DISPLAY2`)
	fs.BoolVar(&o.layered, "layered", true, "include layered windows (tooltips, menus) in the capture")
	fs.IntVar(&o.fps, "fps", 10, "capture rate while the viewer is watching")
	fs.IntVar(&o.boost, "boost", 30, "capture rate while the viewer is working; 0 for none")
	fs.IntVar(&o.idle, "idle", 2, "capture rate once the screen has not changed and the viewer has done nothing for 3 s; 0 for none")
	fs.StringVar(&o.stateDir, "state-dir", "", "where the identity and the paired devices are kept (default: a per-user directory; in test mode a throwaway one)")
	fs.BoolVar(&o.pair, "pair", false, "open pairing at startup")
	fs.BoolVar(&o.noAuth, "no-auth", false, "open mode: let anyone in without verification (for development)")
	fs.BoolVar(&o.allowWinL, "allow-win-l", false, "let a viewer press Win+L, which locks the host")
	fs.StringVar(&o.control, "control", "stdio", `"stdio" for JSON Lines on stdin and stdout, "none" for a console host that writes to stderr`)
	fs.BoolVar(&o.testMode, "test-mode", false, "synthetic screen and logged input only; never touches the desktop")
	fs.StringVar(&o.source, "source", "", `"gdi" for the real screen or "pattern" for a synthetic one (default gdi, or pattern in test mode)`)
	fs.StringVar(&o.inject, "inject", "", `"sendinput" for real input or "log" to only log it (default sendinput, or log in test mode)`)
	fs.StringVar(&o.scene, "scene", "blocks", "synthetic scene: blocks, static, docscroll, terminal or video")
	fs.StringVar(&o.size, "size", "1920x1080", "synthetic screen size")
	fs.DurationVar(&o.writeTimeout, "write-timeout", 30*time.Second, "how long the controller may leave events unread before the host takes it for gone")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return exitStartup
	}
	if *showVersion {
		fmt.Fprintln(stdout, "nx-host "+version)
		return 0
	}

	gone := make(chan string, 1)
	var em hostd.Emitter
	var closeEm func()
	if o.control == "none" {
		em, closeEm = humanEmitter{w: stderr}, func() {}
	} else {
		le := hostd.NewLineEmitter(stdout, o.writeTimeout, func(reason string) {
			select {
			case gone <- reason:
			default:
			}
		})
		em, closeEm = le, le.Close
	}
	defer closeEm()
	fail := func(format string, a ...any) int {
		em.Emit(hostd.Event{Event: "error", ErrorCode: "startup", Message: fmt.Sprintf(format, a...)})
		return exitStartup
	}

	if o.control != "stdio" && o.control != "none" {
		return fail(`unknown -control %q (want "stdio" or "none")`, o.control)
	}
	source, _, err := resolveModes(o)
	if err != nil {
		return fail("%v", err)
	}
	logger := log.New(stderr, "nx-host: ", log.Ltime|log.Lmicroseconds)

	var backend hostd.Backend
	if source == "pattern" {
		w, h, err := parseSize(o.size)
		if err != nil {
			return fail("%v", err)
		}
		scene, err := synth.ParseScene(o.scene)
		if err != nil {
			return fail("%v", err)
		}
		backend, _, err = hostd.PatternBackend(scene, w, h, logger)
		if err != nil {
			return fail("%v", err)
		}
		logger.Printf("screen: synthetic %s %dx%d (TEST MODE: input is logged, not injected)", scene, w, h)
	} else {
		backend, err = openReal(o.display, o.layered)
		if err != nil {
			return fail("%v", err)
		}
		logger.Printf("screen: %s %dx%d", backend.Name, backend.W, backend.H)
	}
	backend.TestMode = o.testMode

	dir, cleanup, err := stateDir(o)
	if err != nil {
		if backend.Close != nil {
			backend.Close()
		}
		return fail("%v", err)
	}
	defer cleanup()

	h, err := hostd.New(hostd.Config{
		Listen: o.listen, StateDir: dir, FPS: o.fps, Boost: o.boost, Idle: o.idle, NoAuth: o.noAuth, AllowWinL: o.allowWinL,
		Version: version, Backend: backend, Log: logger,
	}, em)
	if err != nil {
		if backend.Close != nil {
			backend.Close()
		}
		return fail("%v", err)
	}
	if o.noAuth {
		logger.Printf("WARNING: open mode: anyone who can reach %s is let in", h.Addr())
	}

	h.Start(ctx)
	if o.pair {
		h.Handle([]byte(`{"cmd":"pair"}`))
	}
	if o.control == "stdio" {
		go hostd.ReadCommands(stdin, h)
	}
	go func() { <-ctx.Done(); h.Stop("interrupted") }()

	stopped := make(chan struct{})
	go func() { h.Wait(); close(stopped) }()
	select {
	case <-stopped:
		return 0
	case reason := <-gone:
		fmt.Fprintf(stderr, "nx-host: the controller stopped reading its events (%s); stopping\n", reason)
		h.Stop("controller-gone")
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
		}
		return exitControllerGone
	}
}

// resolveModes settles -source and -inject, which default to the real thing, or to
// the synthetic one in test mode. Test mode must never be able to touch the
// desktop even when a flag is passed by accident, and a synthetic source outside
// test mode would be a host that looks real and is not.
func resolveModes(o options) (source, inject string, err error) {
	source, inject = o.source, o.inject
	if o.testMode {
		if source == "" {
			source = "pattern"
		}
		if inject == "" {
			inject = "log"
		}
		if source != "pattern" || inject != "log" {
			return "", "", errors.New("-test-mode means exactly -source pattern -inject log")
		}
		return source, inject, nil
	}
	if source == "" {
		source = "gdi"
	}
	if inject == "" {
		inject = "sendinput"
	}
	switch {
	case source != "gdi" && source != "pattern":
		return "", "", fmt.Errorf("unknown -source %q (want gdi or pattern)", source)
	case inject != "sendinput" && inject != "log":
		return "", "", fmt.Errorf("unknown -inject %q (want sendinput or log)", inject)
	case source != "gdi" || inject != "sendinput":
		return "", "", errors.New("-source pattern and -inject log are only for -test-mode")
	}
	return source, inject, nil
}

func parseSize(s string) (w, h int, err error) {
	a, b, ok := strings.Cut(strings.ToLower(s), "x")
	if ok {
		w, err = strconv.Atoi(a)
		if err == nil {
			h, err = strconv.Atoi(b)
		}
	}
	if !ok || err != nil {
		return 0, 0, fmt.Errorf("-size %q is not WIDTHxHEIGHT", s)
	}
	return w, h, nil
}

// stateDir is where the identity and the paired devices live. A test-mode host
// with none given makes a throwaway one, so that it cannot touch the identity or
// the paired devices of a real host, and removes it when it stops.
func stateDir(o options) (dir string, cleanup func(), err error) {
	switch {
	case o.stateDir != "":
		return o.stateDir, func() {}, nil
	case o.testMode:
		tmp, err := os.MkdirTemp("", "nx-host-test-")
		if err != nil {
			return "", nil, fmt.Errorf("cannot create a state directory for test mode: %w", err)
		}
		return tmp, func() { os.RemoveAll(tmp) }, nil
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return "", nil, fmt.Errorf("cannot find a place for the state directory (use -state-dir): %w", err)
		}
	}
	return filepath.Join(base, "go-nx", "nx-host"), func() {}, nil
}

// humanEmitter writes events as sentences, for a host that is run from a console.
type humanEmitter struct{ w io.Writer }

func (h humanEmitter) Emit(ev hostd.Event) {
	switch ev.Event {
	case "ready":
		fmt.Fprintf(h.w, "listening on %s, sharing %s %dx%d, identity %s\n", ev.Listen, ev.Display, ev.Width, ev.Height, ev.Identity)
	case "pairing":
		if ev.Code == "" {
			fmt.Fprintln(h.w, "pairing closed")
		} else {
			fmt.Fprintf(h.w, "pairing code %s (valid %ds)\n", ev.Code, *ev.ExpiresIn)
		}
	case "connected":
		fmt.Fprintf(h.w, "connected: %s %q\n", ev.Addr, ev.Name)
	case "disconnected":
		fmt.Fprintf(h.w, "disconnected: %s (%s) %s\n", ev.Addr, ev.Reason, ev.Message)
	case "denied":
		fmt.Fprintf(h.w, "refused %s: %s\n", ev.Addr, ev.Reason)
	case "error":
		fmt.Fprintf(h.w, "error: %s\n", ev.Message)
	case "warning":
		fmt.Fprintf(h.w, "warning: %s\n", ev.Message)
	case "stopped":
		fmt.Fprintf(h.w, "stopped (%s)\n", ev.Reason)
	}
}
