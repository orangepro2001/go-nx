package trust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := Identity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Identity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(a.Certificate[0]) != Fingerprint(b.Certificate[0]) {
		t.Fatal("identity changed between loads")
	}
	c, _ := Identity(t.TempDir())
	if Fingerprint(a.Certificate[0]) == Fingerprint(c.Certificate[0]) {
		t.Fatal("two directories share an identity")
	}
}

func TestStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "s.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("empty store has a key")
	}
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s2.Get("k"); !ok || v != "v" || s2.Len() != 1 {
		t.Fatalf("reloaded store = %q %v len %d", v, ok, s2.Len())
	}
}

func TestOpenStoreRecoveringSetsCorruptFileAside(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "trusted.json")
	if err := os.WriteFile(p, []byte(`{"aa": "x"`), 0o600); err != nil { // truncated JSON
		t.Fatal(err)
	}
	if _, err := OpenStore(p); err == nil {
		t.Fatal("strict OpenStore accepted a corrupt file")
	}
	s, backup, err := OpenStoreRecovering(p)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" || s.Len() != 0 {
		t.Fatalf("backup = %q, len = %d; want a backup and an empty store", backup, s.Len())
	}
	if b, _ := os.ReadFile(backup); string(b) != `{"aa": "x"` {
		t.Errorf("backup content = %q", b)
	}
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	again, backup2, err := OpenStoreRecovering(p)
	if err != nil || backup2 != "" {
		t.Fatalf("reopen = %q, %v", backup2, err)
	}
	if v, _ := again.Get("k"); v != "v" {
		t.Errorf("value after reopen = %q", v)
	}
}

func TestStoreSetLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "s.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.Set("k", strings.Repeat("v", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	es, _ := os.ReadDir(dir)
	if len(es) != 1 {
		t.Errorf("directory has %d entries, want just the store file", len(es))
	}
}
