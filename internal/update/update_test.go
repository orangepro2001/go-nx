package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/relhub/relclient"
	"github.com/orangepro2001/go-nx/internal/relhub/relproto"
)

// offer is what the fake release server publishes.
type offer struct {
	version  string // version name of the offered build; "" publishes nothing
	min      int64  // channel minimum versionCode
	payload  []byte // bytes the manifest describes
	served   []byte // bytes the download endpoint sends; payload when nil
	signedBy ed25519.PrivateKey
}

type fixture struct {
	srv  *httptest.Server
	pub  ed25519.PublicKey
	exe  string
	dir  string
	keys []string
}

func newFixture(t *testing.T, o offer) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if o.signedBy == nil {
		o.signedBy = priv
	}
	served := o.served
	if served == nil {
		served = o.payload
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/apps/go-nx/check", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if o.version == "" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(relproto.ErrorBody{Error: relproto.Errorf(relproto.CodeNotFound, "no release")})
			return
		}
		code, err := relproto.VersionCodeFromName(o.version)
		if err != nil {
			t.Error(err)
		}
		sum := sha256.Sum256(o.payload)
		m := &relproto.Manifest{
			App: AppID, Platform: Platform, Channel: relproto.DefaultChannel,
			VersionCode: code, VersionName: o.version, File: "go-nx.exe",
			Size: int64(len(o.payload)), SHA256: hex.EncodeToString(sum[:]),
			MinVersionCode: o.min, Notes: "test", PublishedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := m.Sign(o.signedBy); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(relproto.CheckResult{App: AppID, Platform: Platform, Channel: m.Channel, Manifest: m})
	})
	mux.HandleFunc("/v1/apps/go-nx/win-x64/releases/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "go-nx.exe", time.Time{}, strings.NewReader(string(served)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	if err := os.WriteFile(exe, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fixture{srv: srv, pub: pub, exe: exe, dir: dir, keys: []string{relproto.EncodePublicKey(pub)}}
}

func (f *fixture) updater(t *testing.T, version string) *Updater {
	t.Helper()
	u, err := New(Config{BaseURL: f.srv.URL, Version: version, Exe: f.exe, Keys: f.keys, FirstDelay: 10 * time.Millisecond, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNewRejectsDevBuild(t *testing.T) {
	if _, err := New(Config{Version: "dev", Exe: "go-nx.exe"}); err == nil {
		t.Fatal("a dev build has no versionCode and must not get an updater")
	}
	if _, err := New(Config{Version: "0.1.0", Exe: "go-nx.exe"}); err != nil {
		t.Fatalf("pinned key and default URL must be valid: %v", err)
	}
}

func TestPinnedKeysAreValid(t *testing.T) {
	keys, err := relclient.ParseTrustedKeys(PinnedKeys...)
	if err != nil || len(keys) == 0 {
		t.Fatalf("PinnedKeys do not parse: %v", err)
	}
	if got := relproto.KeyID(keys[0]); got != "f5faa9f9858b789f" {
		t.Fatalf("first pinned key has keyId %s, the release server's is f5faa9f9858b789f", got)
	}
}

func TestUpdateDownloadedAndInstalled(t *testing.T) {
	f := newFixture(t, offer{version: "0.1.1", payload: []byte("new build")})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())

	st := u.Status()
	if st.State != Ready || st.Version != "0.1.1" || st.Mandatory {
		t.Fatalf("status = %+v, want Ready 0.1.1 optional", st)
	}
	if got := st.Label(); got != "Restart to update to v0.1.1" {
		t.Fatalf("label = %q", got)
	}
	if b, err := os.ReadFile(u.NewPath()); err != nil || string(b) != "new build" {
		t.Fatalf("downloaded file = %q, %v", b, err)
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "old build" {
		t.Fatal("the running exe must not change before Install")
	}

	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "new build" {
		t.Fatalf("exe = %q after Install", b)
	}
	if b, _ := os.ReadFile(f.exe + ".old"); string(b) != "old build" {
		t.Fatalf(".old = %q after Install", b)
	}
	CleanupOld(f.exe)
	if _, err := os.Stat(f.exe + ".old"); !os.IsNotExist(err) {
		t.Fatal("CleanupOld left the old build behind")
	}
}

func TestMandatoryUpdateIsSignedAndLabelled(t *testing.T) {
	f := newFixture(t, offer{version: "0.2.0", min: 101, payload: []byte("new build")})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	st := u.Status()
	if st.State != Ready || !st.Mandatory {
		t.Fatalf("status = %+v, want Ready mandatory", st)
	}
	if !strings.HasSuffix(st.Label(), "(required)") {
		t.Fatalf("label = %q", st.Label())
	}
}

func TestAlreadyCurrent(t *testing.T) {
	f := newFixture(t, offer{version: "0.1.0", payload: []byte("same")})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Current {
		t.Fatalf("status = %+v, want Current", st)
	}
	if _, err := os.Stat(u.NewPath()); !os.IsNotExist(err) {
		t.Fatal("nothing should be downloaded when the server has nothing newer")
	}
}

func TestNothingPublishedIsNotAnError(t *testing.T) {
	f := newFixture(t, offer{})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Current {
		t.Fatalf("status = %+v, want Current", st)
	}
}

func TestManifestFromAnotherKeyIsRefused(t *testing.T) {
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	f := newFixture(t, offer{version: "0.1.1", payload: []byte("evil"), signedBy: other})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	st := u.Status()
	if st.State != Failed || st.Err == nil {
		t.Fatalf("status = %+v, want Failed", st)
	}
	if _, err := os.Stat(u.NewPath()); !os.IsNotExist(err) {
		t.Fatal("an unverified build must never reach disk as a candidate")
	}
}

func TestCorruptDownloadIsDiscarded(t *testing.T) {
	f := newFixture(t, offer{version: "0.1.1", payload: []byte("new build"), served: []byte("NEW BUILD")})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	st := u.Status()
	if st.State != Failed || !errors.Is(st.Err, relclient.ErrChecksum) {
		t.Fatalf("status = %+v, want Failed with ErrChecksum", st)
	}
	for _, p := range []string{u.NewPath(), u.NewPath() + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s survived a checksum failure", p)
		}
	}
	if err := u.Install(); err == nil {
		t.Fatal("Install must refuse when no verified update is waiting")
	}
}

func TestServerUnreachable(t *testing.T) {
	f := newFixture(t, offer{version: "0.1.1", payload: []byte("x")})
	f.srv.Close()
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Failed {
		t.Fatalf("status = %+v, want Failed", st)
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "old build" {
		t.Fatal("an unreachable server must leave the exe alone")
	}
}

func TestRunChecksAfterDelayAndCheckNowIsNoOpWhenReady(t *testing.T) {
	f := newFixture(t, offer{version: "0.1.1", payload: []byte("new build")})
	u := f.updater(t, "0.1.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)

	waitFor(t, func() bool { return u.Status().State == Ready })
	u.CheckNow() // an update is waiting: must be a no-op, not a second download
	time.Sleep(50 * time.Millisecond)
	if u.Status().State != Ready {
		t.Fatalf("CheckNow disturbed a ready update: %+v", u.Status())
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReplaceRollsBackWhenNewBuildIsMissing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("old build"), 0o755)

	if err := Replace(exe, filepath.Join(dir, "go-nx.exe.new")); err == nil {
		t.Fatal("Replace with a missing new build must fail")
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != "old build" {
		t.Fatalf("exe = %q, %v: the old build must be back in place", b, err)
	}
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatal("rollback left a .old behind")
	}
}

func TestReplaceOverwritesStaleOld(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("v2"), 0o755)
	os.WriteFile(exe+".old", []byte("v1"), 0o755)
	os.WriteFile(exe+".new", []byte("v3"), 0o755)

	if err := Replace(exe, exe+".new"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "v3" {
		t.Fatalf("exe = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "v2" {
		t.Fatalf(".old = %q", b)
	}
}

func TestLabels(t *testing.T) {
	cases := map[Status]string{
		{}:                "Check for updates",
		{State: Checking}: "Checking for updates...",
		{State: Current}:  "Up to date - check again",
		{State: Downloading, Version: "0.1.1", Percent: 42}: "Downloading v0.1.1 (42%)...",
		{State: Failed}: "Update check failed - retry",
	}
	for st, want := range cases {
		if got := st.Label(); got != want {
			t.Errorf("Label(%+v) = %q, want %q", st, got, want)
		}
	}
}
