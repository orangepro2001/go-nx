package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"go-nx/internal/relhub/relproto"
)

// TestE2E runs the updater against a real relhub server. It is skipped unless
// scripts/e2e-update.sh (which starts a throwaway server and publishes 0.1.1)
// provides the environment.
func TestE2E(t *testing.T) {
	url, key, payload := os.Getenv("RELHUB_E2E_URL"), os.Getenv("RELHUB_E2E_KEY"), os.Getenv("RELHUB_E2E_PAYLOAD")
	if url == "" || key == "" || payload == "" {
		t.Skip("set by scripts/e2e-update.sh")
	}
	want, err := os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	if err := os.WriteFile(exe, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("update from 0.1.0 to 0.1.1", func(t *testing.T) {
		u, err := New(Config{BaseURL: url, Version: "0.1.0", Exe: exe, Keys: []string{key}})
		if err != nil {
			t.Fatal(err)
		}
		u.checkOnce(context.Background())
		if st := u.Status(); st.State != Ready || st.Version != "0.1.1" {
			t.Fatalf("status = %+v (err %v), want Ready 0.1.1", st, st.Err)
		}
		if err := u.Install(); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(exe); string(got) != string(want) {
			t.Fatal("the installed exe is not the published package")
		}
		if got, _ := os.ReadFile(exe + ".old"); string(got) != "old build" {
			t.Fatalf(".old = %q", got)
		}
	})

	t.Run("already on 0.1.1", func(t *testing.T) {
		u, _ := New(Config{BaseURL: url, Version: "0.1.1", Exe: exe, Keys: []string{key}})
		u.checkOnce(context.Background())
		if st := u.Status(); st.State != Current {
			t.Fatalf("status = %+v, want Current", st)
		}
	})

	t.Run("a different pinned key refuses the real server", func(t *testing.T) {
		other, _, _ := ed25519.GenerateKey(rand.Reader)
		u, _ := New(Config{BaseURL: url, Version: "0.1.0", Exe: exe, Keys: []string{relproto.EncodePublicKey(other)}})
		u.checkOnce(context.Background())
		if st := u.Status(); st.State != Failed {
			t.Fatalf("status = %+v, want Failed: the manifest is not signed by the pinned key", st)
		}
	})
}
