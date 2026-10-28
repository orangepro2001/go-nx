//go:build !windows

package main

import (
	"errors"

	"github.com/orangepro2001/go-nx/internal/hostd"
)

// openReal is where the real screen and the real input would come from. They
// exist on Windows only.
func openReal(display string, layered bool) (hostd.Backend, error) {
	return hostd.Backend{}, errors.New("screen capture and input injection are only available on Windows; use -test-mode for a synthetic screen")
}
