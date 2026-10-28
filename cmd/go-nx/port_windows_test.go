//go:build windows

package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// holdUDPEnv tells the test binary, started again as a helper, which address to hold.
const holdUDPEnv = "GO_NX_TEST_HOLD_UDP"

// TestHelperHoldUDP is not a test: started by holdUDP with holdUDPEnv set, it binds that
// address, says whether it could, and keeps it until its standard input is closed.
func TestHelperHoldUDP(t *testing.T) {
	addr := os.Getenv(holdUDPEnv)
	if addr == "" {
		t.Skip("a helper process for TestPortSharersNamesAnotherProcess")
	}
	c, err := net.ListenPacket("udp4", addr)
	if err != nil {
		fmt.Println("failed:", err)
		return
	}
	fmt.Println("bound")
	io.Copy(io.Discard, os.Stdin)
	c.Close()
}

// holdUDP has another process bind addr. It returns that process's ID, or 0 if the bind
// failed; stop ends the process.
func holdUDP(t *testing.T, addr string) (pid int, stop func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldUDP$")
	cmd.Env = append(os.Environ(), holdUDPEnv+"="+addr)
	in, err := cmd.StdinPipe()
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
	stop = func() { in.Close(); cmd.Wait() }
	line, _ := bufio.NewReader(out).ReadString('\n')
	if !strings.HasPrefix(line, "bound") {
		t.Logf("the helper could not bind %s: %s", addr, strings.TrimSpace(line))
		return 0, stop
	}
	return cmd.Process.Pid, stop
}

// The situation behind a pairing code that never works: this process listens on every
// address, as the host does, and another one binds a single address on the same port and
// gets the packets sent to it. portSharers must name that process and its address, and never
// this process's own socket. Windows decides which binding order it allows, so both are tried.
func TestPortSharersNamesAnotherProcess(t *testing.T) {
	// The host binds first and the other program later.
	ours, err := net.ListenPacket("udp", ":0") // every address, as remote.Listen binds
	if err != nil {
		t.Fatal(err)
	}
	port := ours.LocalAddr().(*net.UDPAddr).Port
	socks := udpSockets(port)
	if len(socks) == 0 || socks[0].PID != uint32(os.Getpid()) {
		t.Fatalf("udpSockets(%d) = %v, want this process (%d)", port, socks, os.Getpid())
	}
	if got := portSharers(port); len(got) != 0 {
		t.Fatalf("this process's own socket is listed as another program: %q", got)
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	pid, stop := holdUDP(t, addr)
	if pid == 0 {
		stop()
		ours.Close()
		// The other program binds first and the host later.
		probe, err := net.ListenPacket("udp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		port = probe.LocalAddr().(*net.UDPAddr).Port
		probe.Close()
		addr = "127.0.0.1:" + strconv.Itoa(port)
		if pid, stop = holdUDP(t, addr); pid == 0 {
			stop()
			t.Skip("Windows lets no second process share a port with this one in either order")
		}
		if ours, err = net.ListenPacket("udp", ":"+strconv.Itoa(port)); err != nil {
			stop()
			t.Skipf("the host could not bind every address next to %s: %v", addr, err)
		}
	}
	defer ours.Close()
	defer stop()
	got := portSharers(port)
	want := fmt.Sprintf("(process %d) on 127.0.0.1", pid)
	if len(got) != 1 || !strings.Contains(got[0], want) {
		t.Errorf("portSharers(%d) = %q, want one entry with %q", port, got, want)
	}
}
