package vdisplay

import (
	"os"
	"path/filepath"
	"testing"
)

// The installer in the tree is the one whose size and hash the code expects, and a changed
// one is refused.
func TestTheCarriedInstallerIsParsecs(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("driver", InstallerName))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkInstaller(b); err != nil {
		t.Fatal(err)
	}
	b[len(b)/2]++
	if checkInstaller(b) == nil {
		t.Error("a changed installer passed")
	}
	if checkInstaller(b[:100]) == nil {
		t.Error("a short installer passed")
	}
}
