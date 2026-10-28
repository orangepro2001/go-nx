//go:build !windows

package main

func runInput() (*InputResult, error) {
	return &InputResult{Skipped: "the input check injects keys with Windows SendInput and runs on Windows only"}, nil
}
