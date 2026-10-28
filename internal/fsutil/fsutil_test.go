package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestWriteFileAtomicCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "a.json") // parent is created
	if err := WriteFileAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "two" {
		t.Fatalf("content = %q, %v", b, err)
	}
	if got := names(t, filepath.Dir(p)); len(got) != 1 {
		t.Errorf("leftover files after two writes: %v", got)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
}

// If the replacement cannot happen the old file must be untouched and no
// temporary file may be left behind.
func TestWriteFileAtomicFailureKeepsOldFileAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.json")
	if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blocker, "inner"), 0o700); err != nil { // a non-empty directory cannot be replaced by a file
		t.Fatal(err)
	}
	if err := WriteFileAtomic(blocker, []byte("x"), 0o600); err == nil {
		t.Fatal("replacing a directory with a file succeeded")
	}
	if b, _ := os.ReadFile(p); string(b) != "keep" {
		t.Errorf("unrelated file changed: %q", b)
	}
	for _, n := range names(t, dir) {
		if strings.Contains(n, ".tmp") {
			t.Errorf("temporary file %q left behind", n)
		}
	}
}

func TestQuarantineMovesFileAside(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "trusted.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := Quarantine(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("original file still in place")
	}
	if b, err := os.ReadFile(backup); err != nil || string(b) != "{not json" {
		t.Fatalf("backup = %q, %v", b, err)
	}
	if !strings.HasPrefix(filepath.Base(backup), "trusted.json.corrupt-") {
		t.Errorf("backup name %q", filepath.Base(backup))
	}
	// A second corrupt file must not overwrite the first backup.
	os.WriteFile(p, []byte("also bad"), 0o600)
	backup2, err := Quarantine(p)
	if err != nil || backup2 == backup {
		t.Fatalf("second backup = %q (first %q), %v", backup2, backup, err)
	}
	if b, _ := os.ReadFile(backup); string(b) != "{not json" {
		t.Error("first backup was overwritten")
	}
}
