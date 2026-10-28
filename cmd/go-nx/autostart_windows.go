//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// Starting with Windows goes through the user's Run key: Windows starts go-nx, in the tray,
// when that user signs in. It needs no administrator rights and no service.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// runValue names go-nx's entry under the Run key; tests use another name.
var runValue = "go-nx"

// runCommand is what the Run key holds for go-nx, if anything.
func runCommand() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValue)
	return v, err == nil
}

// startCommand is the command line that starts this go-nx in the tray.
func startCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return syscall.EscapeArg(exe) + " -hidden", nil
}

// setAutostart adds go-nx to the Run key, pointing at this executable, or takes it out.
func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	cmd, err := startCommand()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, cmd)
}
