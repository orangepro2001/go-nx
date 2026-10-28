package vdisplay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// The Parsec Virtual Display Driver installer that go-nx carries (Parsec's own signed
// build, the one NightDesk carries too: https://builds.parsec.app/vdd/parsec-vdd-0.45.0.0.exe).
const (
	InstallerName   = "parsec-vdd-0.45.0.0.exe"
	installerSize   = 517256
	installerSHA256 = "e23332448fdaf5aa017cb308db5ef6855fac526a7ded05d80c039404126d5362"
	// InstallerURL is where to get it by hand.
	InstallerURL = "https://builds.parsec.app/vdd/parsec-vdd-0.45.0.0.exe"
)

// checkInstaller makes sure b is that installer, byte for byte.
func checkInstaller(b []byte) error {
	if len(b) != installerSize {
		return fmt.Errorf("the driver installer is %d bytes, not %d", len(b), installerSize)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != installerSHA256 {
		return fmt.Errorf("the driver installer's SHA-256 is %s, not %s", got, installerSHA256)
	}
	return nil
}
