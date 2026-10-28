//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The Run key of the user running the tests is changed, under a name of its own, and put
// back.
func TestAutostartAddsAndRemovesTheRunEntry(t *testing.T) {
	runValue = fmt.Sprintf("go-nx-test-%d", os.Getpid())
	t.Cleanup(func() { setAutostart(false); runValue = "go-nx" })
	p := winPlatform{}
	if p.Autostart() {
		t.Fatal("on before it was turned on")
	}
	if err := p.SetAutostart(true); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	cmd, ok := runCommand()
	if !ok || !strings.Contains(cmd, exe) || !strings.HasSuffix(cmd, " -hidden") || !p.Autostart() {
		t.Fatalf("Run entry %q (%v), want this executable started in the tray", cmd, ok)
	}
	if err := p.SetAutostart(false); err != nil {
		t.Fatal(err)
	}
	if p.Autostart() {
		t.Error("still on after it was turned off")
	}
	if err := p.SetAutostart(false); err != nil {
		t.Errorf("turning it off when it is off: %v", err)
	}
}
