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

func TestStoreDeleteAndClearPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s, _ := OpenStore(p)
	for _, k := range []string{"b", "a", "c"} {
		if err := s.Set(k, "v"); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(s.Keys(), ","); got != "a,b,c" {
		t.Fatalf("Keys = %s, want sorted a,b,c", got)
	}
	if err := s.Delete("b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("missing"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
	re, _ := OpenStore(p)
	if _, ok := re.Get("b"); ok || re.Len() != 2 {
		t.Fatalf("after Delete reopen has len %d, b present = %v", re.Len(), ok)
	}
	n, err := s.Clear()
	if err != nil || n != 2 {
		t.Fatalf("Clear = %d, %v; want 2, nil", n, err)
	}
	if re, _ = OpenStore(p); re.Len() != 0 {
		t.Fatalf("after Clear reopen has %d entries", re.Len())
	}
	if n, err := s.Clear(); err != nil || n != 0 {
		t.Fatalf("Clear on empty = %d, %v", n, err)
	}
}

func TestStoreBoolUsesDefaultOnlyWhenUnset(t *testing.T) {
	s, _ := OpenStore(filepath.Join(t.TempDir(), "s.json"))
	if !s.Bool("flag", true) || s.Bool("flag", false) {
		t.Fatal("an unset key must give the default")
	}
	s.Set("flag", "0")
	if s.Bool("flag", true) {
		t.Fatal("an explicit 0 must win over a true default")
	}
	s.Set("flag", "1")
	if !s.Bool("flag", false) {
		t.Fatal("an explicit 1 must win over a false default")
	}
	s.Set("flag", "garbage")
	if s.Bool("flag", true) {
		t.Fatal("an unrecognised value is not 1, so it is false")
	}
}
