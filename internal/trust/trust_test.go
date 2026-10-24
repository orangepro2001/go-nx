package trust

import (
	"path/filepath"
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
