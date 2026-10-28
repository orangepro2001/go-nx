//go:build windows

package update

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/wintest"
)

// These tests run the real thing the updater depends on: renaming an executable
// while it is running, which is allowed on Windows (deleting or overwriting it is
// not). The test binary copies itself into a temporary directory, starts as a
// long-lived helper process, and is then replaced underneath. The helper does
// what go-nx does at startup, and prints what it saw on its first line.

const helperEnv = "GO_NX_UPDATE_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "recover":
		exe, err := os.Executable()
		got, rerr := Recover(exe)
		os.Stdout.WriteString("recovered " + got + " " + errText(err) + errText(rerr) + "\n")
	default:
		exe, _ := os.Executable()
		os.Stdout.WriteString("running " + exe + "\n")
	}
	io.Copy(io.Discard, os.Stdin) // stay alive until the test closes stdin
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type helper struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	first string // the first line it printed
}

// startHelper copies this test binary to path and runs it in the given mode.
func startHelper(t *testing.T, path, mode string) *helper {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &helper{cmd: cmd, stdin: stdin}
	t.Cleanup(h.stop)
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		lines <- strings.TrimSpace(line)
	}()
	select {
	case h.first = <-lines:
	case <-time.After(30 * time.Second):
		t.Fatal("the helper process did not start")
	}
	return h
}

func (h *helper) stop() {
	h.stdin.Close()
	done := make(chan struct{})
	go func() { h.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		h.cmd.Process.Kill()
		<-done
	}
}

func TestReplaceOfARunningExe(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	h := startHelper(t, exe, "run")
	self, _ := os.Stat(exe)

	if err := os.WriteFile(exe+".new", []byte("the new build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(exe, exe+".new"); err != nil {
		t.Fatalf("Replace of a running exe: %v", err)
	}
	if got := read(t, exe); got != "the new build" {
		t.Fatalf("exe = %q after Replace", got)
	}
	if fi, err := os.Stat(exe + ".old"); err != nil || fi.Size() != self.Size() {
		t.Fatalf("the running build was not kept as .old: %v", err)
	}

	// The backup is the image of a live process, so Windows will not delete it yet
	// (a Wine desktop is more lenient, which proves nothing either way).
	CleanupOld(exe)
	if !wintest.IsWine() && !exists(exe+".old") {
		t.Error("the image of a running process was deleted")
	}
	h.stop()
	for i := 0; i < 50 && exists(exe+".old"); i++ {
		CleanupOld(exe) // the file stays locked for a moment after the process ends
		time.Sleep(100 * time.Millisecond)
	}
	if exists(exe + ".old") {
		t.Error("CleanupOld could not remove the backup after the process ended")
	}
}

// The state an interrupted update leaves: only the backup copy is there, and it is
// what gets started. It takes the real name back while it runs.
func TestARunningBackupCopyTakesTheRealNameBack(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	h := startHelper(t, exe+".old", "recover")
	// The path may be spelled differently (8.3 short names, case) from the one used
	// to start the process, so compare what it names rather than the text.
	got, ok := strings.CutPrefix(h.first, "recovered ")
	if !ok {
		t.Fatalf("the helper reported %q", h.first)
	}
	want, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("the real name was not restored: %v", err)
	}
	if reported, err := os.Stat(got); err != nil || !os.SameFile(reported, want) {
		t.Fatalf("the helper says it is now %q, which is not %s (%v)", got, exe, err)
	}
	if !exists(exe) || exists(exe+".old") {
		t.Fatalf("real name present=%v, backup name present=%v", exists(exe), exists(exe+".old"))
	}
}
