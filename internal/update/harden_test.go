package update

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/relhub/relclient"
	"github.com/orangepro2001/go-nx/internal/relhub/relproto"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A server that offers something older than what runs must not downgrade anyone:
// the verdict comes from the signed versionCode, not from the server's say-so.
func TestAnOlderOfferIsNotAnUpdate(t *testing.T) {
	f := newFixture(t, offer{version: "0.0.9", payload: []byte("older build")})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Current {
		t.Fatalf("status = %+v, want Current", st)
	}
	if exists(u.NewPath()) || len(f.downloaded()) != 0 {
		t.Fatalf("an older build was downloaded (%v requests, new file %v)", f.downloaded(), exists(u.NewPath()))
	}
}

// A manifest that carries the right signature but belongs to another app, platform
// or channel is a mix-up on the server (or a replay) and must not be installed.
func TestAManifestForSomethingElseIsRefused(t *testing.T) {
	cases := map[string]offer{
		"app":      {app: "other-app"},
		"platform": {platform: "linux-x64"},
		"channel":  {channel: "beta"},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			o.version, o.payload = "0.1.1", []byte("somebody else's build")
			f := newFixture(t, o)
			u := f.updater(t, "0.1.0")
			u.checkOnce(context.Background())
			st := u.Status()
			if st.State != Failed || st.Err == nil {
				t.Fatalf("status = %+v, want Failed", st)
			}
			if got := relproto.AsError(st.Err).Code; got != relproto.CodeInvalid {
				t.Errorf("error code = %q (%v), want %q", got, st.Err, relproto.CodeInvalid)
			}
			if exists(u.NewPath()) || len(f.downloaded()) != 0 {
				t.Fatal("something was downloaded for a manifest that is not ours")
			}
		})
	}
}

// A download that ends early is not an update, and nothing of it may linger as a
// candidate.
func TestATruncatedDownloadIsNotInstallable(t *testing.T) {
	payload := []byte("a complete build, twice as long as what arrives")
	f := newFixture(t, offer{version: "0.1.1", payload: payload, served: payload[:20]})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	st := u.Status()
	if st.State != Failed || st.Err == nil {
		t.Fatalf("status = %+v, want Failed", st)
	}
	if exists(u.NewPath()) {
		t.Fatal("a truncated download was left as the update candidate")
	}
	if err := u.Install(); err == nil {
		t.Fatal("Install accepted a truncated download")
	}
	if read(t, f.exe) != "old build" {
		t.Fatal("the exe changed")
	}
}

// A longer body than the manifest announced is wrong in the other direction.
func TestAnOverlongDownloadIsRefused(t *testing.T) {
	payload := []byte("the real build")
	f := newFixture(t, offer{version: "0.1.1", payload: payload, served: append(append([]byte(nil), payload...), "trailing junk"...)})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Failed || !errors.Is(st.Err, relclient.ErrChecksum) {
		t.Fatalf("status = %+v, want Failed with ErrChecksum", st)
	}
	if exists(u.NewPath()) || exists(u.NewPath()+".part") {
		t.Fatal("an overlong download survived")
	}
}

// A partial file from an interrupted attempt is continued, not fetched again.
func TestAnInterruptedDownloadResumes(t *testing.T) {
	payload := []byte("0123456789abcdefghij")
	f := newFixture(t, offer{version: "0.1.1", payload: payload})
	u := f.updater(t, "0.1.0")
	if err := os.WriteFile(u.NewPath()+".part", payload[:8], 0o644); err != nil {
		t.Fatal(err)
	}
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Ready {
		t.Fatalf("status = %+v (%v), want Ready", st, st.Err)
	}
	if got := read(t, u.NewPath()); got != string(payload) {
		t.Fatalf("downloaded %q, want %q", got, payload)
	}
	if reqs := f.downloaded(); len(reqs) != 1 || reqs[0] != "bytes=8-" {
		t.Fatalf("download requests = %q, want one request for bytes=8-", reqs)
	}
	if exists(u.NewPath() + ".part") {
		t.Error("the partial file was left behind")
	}
}

// A leftover partial file that is not a prefix of the build is found out by the
// hash, thrown away, and the next check starts clean and succeeds. One that is
// longer than the build cannot be a prefix of it and is not continued at all.
func TestAStalePartialFileHealsOnTheNextCheck(t *testing.T) {
	payload := []byte("0123456789abcdefghij")
	cases := []struct {
		name       string
		part       string
		firstFails bool
	}{
		{"as long as the build", strings.Repeat("x", len(payload)), true},
		{"a shorter prefix of something else", "garbage!", true},
		{"longer than the build", strings.Repeat("x", len(payload)+7), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, offer{version: "0.1.1", payload: payload})
			u := f.updater(t, "0.1.0")
			if err := os.WriteFile(u.NewPath()+".part", []byte(c.part), 0o644); err != nil {
				t.Fatal(err)
			}
			u.checkOnce(context.Background())
			if c.firstFails {
				if st := u.Status(); st.State != Failed || !errors.Is(st.Err, relclient.ErrChecksum) {
					t.Fatalf("first check: status = %+v, want Failed with ErrChecksum", st)
				}
				if exists(u.NewPath()) || exists(u.NewPath()+".part") {
					t.Fatal("the bad partial file survived")
				}
				u.checkOnce(context.Background())
			}
			if st := u.Status(); st.State != Ready {
				t.Fatalf("status = %+v (%v), want Ready", st, st.Err)
			}
			if got := read(t, u.NewPath()); got != string(payload) {
				t.Fatalf("downloaded %q", got)
			}
		})
	}
}

// ready returns an updater with a verified update waiting.
func ready(t *testing.T, payload string) (*fixture, *Updater) {
	t.Helper()
	f := newFixture(t, offer{version: "0.1.1", payload: []byte(payload)})
	u := f.updater(t, "0.1.0")
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Ready {
		t.Fatalf("status = %+v (%v), want Ready", st, st.Err)
	}
	return f, u
}

// Between the download and the click the file can change: a full disk, a virus
// scanner, a person. What is installed is what was verified, so Install checks the
// file again and, if it differs, discards it and goes back to asking the server.
func TestInstallChecksTheDownloadAgain(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"flipped byte": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("new buile"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"truncated": func(t *testing.T, path string) {
			if err := os.Truncate(path, 3); err != nil {
				t.Fatal(err)
			}
		},
		"appended": func(t *testing.T, path string) {
			fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			fh.WriteString("!")
			fh.Close()
		},
		"deleted": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			f, u := ready(t, "new build")
			damage(t, u.NewPath())

			err := u.Install()
			if err == nil {
				t.Fatal("Install installed a file that is not what was verified")
			}
			if read(t, f.exe) != "old build" || exists(f.exe+".old") {
				t.Fatalf("a refused install touched the files: exe=%q, .old exists=%v", read(t, f.exe), exists(f.exe+".old"))
			}
			if exists(u.NewPath()) {
				t.Error("the damaged candidate was kept")
			}
			if st := u.Status(); st.State != Failed || st.Err == nil {
				t.Fatalf("status = %+v, want Failed so that the next check fetches it again", st)
			}

			u.checkOnce(context.Background())
			if st := u.Status(); st.State != Ready {
				t.Fatalf("after a new check: %+v (%v), want Ready", st, st.Err)
			}
			if err := u.Install(); err != nil {
				t.Fatalf("Install after the re-download: %v", err)
			}
			if read(t, f.exe) != "new build" {
				t.Fatalf("exe = %q", read(t, f.exe))
			}
		})
	}
}

// Clicking twice, or two clicks racing, must not replace the backup of the old
// build with the new build.
func TestInstallingTwiceKeepsTheBackup(t *testing.T) {
	f, u := ready(t, "new build")
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if st := u.Status(); st.State != Installed || st.Version != "0.1.1" {
		t.Fatalf("status after Install = %+v, want Installed 0.1.1", st)
	}
	if err := u.Install(); err == nil {
		t.Fatal("a second Install succeeded")
	}
	if read(t, f.exe) != "new build" || read(t, f.exe+".old") != "old build" {
		t.Fatalf("exe=%q .old=%q: the second Install damaged the files", read(t, f.exe), read(t, f.exe+".old"))
	}
	// An installed update is not offered again, and a click means "restart", not "download".
	u.CheckNow()
	u.checkOnce(context.Background())
	if st := u.Status(); st.State != Installed {
		t.Fatalf("a check after Install changed the state to %+v", st)
	}
	if reqs := f.downloaded(); len(reqs) != 1 {
		t.Fatalf("%d downloads, want only the first", len(reqs))
	}
}

func TestConcurrentInstallsInstallOnce(t *testing.T) {
	f, u := ready(t, "new build")
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if u.Install() == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("%d installs succeeded, want exactly 1", ok.Load())
	}
	if read(t, f.exe) != "new build" || read(t, f.exe+".old") != "old build" {
		t.Fatalf("exe=%q .old=%q", read(t, f.exe), read(t, f.exe+".old"))
	}
}

// Status must stay answerable while an install is running: the interface asks for
// it on every frame.
func TestStatusDoesNotWaitForInstall(t *testing.T) {
	_, u := ready(t, "new build")
	u.installMu.Lock() // as if an install were in progress
	done := make(chan struct{})
	go func() { u.Status(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Status blocked behind an install")
	}
	u.installMu.Unlock()
}

// A refused replacement must change nothing, including the backup of an earlier
// update.
func TestReplaceWithoutANewBuildChangesNothing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("v2"), 0o755)
	os.WriteFile(exe+".old", []byte("v1"), 0o755)

	if err := Replace(exe, exe+".new"); err == nil {
		t.Fatal("Replace without a new build succeeded")
	}
	if read(t, exe) != "v2" || read(t, exe+".old") != "v1" {
		t.Fatalf("exe=%q .old=%q: a refused replacement changed files", read(t, exe), read(t, exe+".old"))
	}
	// A directory is not a build either.
	if err := os.Mkdir(exe+".new", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(exe, exe+".new"); err == nil {
		t.Fatal("Replace accepted a directory as the new build")
	}
	if read(t, exe) != "v2" || read(t, exe+".old") != "v1" {
		t.Fatal("a refused replacement changed files")
	}
}

func TestReplaceWithoutARunningExeChangesNothing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe+".new", []byte("v3"), 0o755)
	os.WriteFile(exe+".old", []byte("v1"), 0o755)
	if err := Replace(exe, exe+".new"); err == nil {
		t.Fatal("Replace of an exe that is not there succeeded")
	}
	if read(t, exe+".new") != "v3" || read(t, exe+".old") != "v1" {
		t.Fatal("a refused replacement changed files")
	}
}

// failRenames makes renames of the named destination fail while *n is positive,
// counting down, and records every rename tried.
func failRenames(t *testing.T, dst string, n *int, log *[]string) {
	t.Helper()
	was, pause := renameFile, retryPause
	renameFile = func(from, to string) error {
		*log = append(*log, filepath.Base(from)+">"+filepath.Base(to))
		if to == dst && *n != 0 {
			if *n > 0 {
				*n--
			}
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("The process cannot access the file because it is being used by another process.")}
		}
		return was(from, to)
	}
	retryPause = time.Millisecond
	t.Cleanup(func() { renameFile, retryPause = was, pause })
}

// Right after the download a virus scanner may still hold the new file for a
// moment. The second rename waits it out instead of undoing the whole update.
func TestReplaceRidesOutATransientLock(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("v2"), 0o755)
	os.WriteFile(exe+".new", []byte("v3"), 0o755)
	n, log := 2, []string{}
	failRenames(t, exe, &n, &log)

	if err := Replace(exe, exe+".new"); err != nil {
		t.Fatalf("Replace: %v (renames tried: %v)", err, log)
	}
	if read(t, exe) != "v3" || read(t, exe+".old") != "v2" {
		t.Fatalf("exe=%q .old=%q", read(t, exe), read(t, exe+".old"))
	}
	if exists(exe + ".new") {
		t.Error("the staged file is still there")
	}
}

// If the new build cannot be moved in, the old one goes back.
func TestReplacePutsTheOldBuildBackWhenInstallingKeepsFailing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("v2"), 0o755)
	os.WriteFile(exe+".new", []byte("v3"), 0o755)
	// Only the install of the new build fails, every time; putting the old one back works.
	was, pause := renameFile, retryPause
	renameFile = func(from, to string) error {
		if from == exe+".new" {
			return errors.New("locked")
		}
		return was(from, to)
	}
	retryPause = time.Millisecond
	t.Cleanup(func() { renameFile, retryPause = was, pause })

	err := Replace(exe, exe+".new")
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("Replace = %v, want the lock reported", err)
	}
	if read(t, exe) != "v2" || exists(exe+".old") || read(t, exe+".new") != "v3" {
		t.Fatalf("exe=%q .old exists=%v .new=%q: the old build was not put back cleanly", read(t, exe), exists(exe+".old"), read(t, exe+".new"))
	}
}

// If even putting the old build back fails, the person is told where it is.
func TestReplaceSaysWhereTheOldBuildIsWhenRestoringFails(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("v2"), 0o755)
	os.WriteFile(exe+".new", []byte("v3"), 0o755)
	was, pause := renameFile, retryPause
	renameFile = func(from, to string) error {
		if to == exe { // neither the new build nor the old one can be moved into place
			return errors.New("denied")
		}
		return was(from, to)
	}
	retryPause = time.Millisecond
	t.Cleanup(func() { renameFile, retryPause = was, pause })

	err := Replace(exe, exe+".new")
	if err == nil {
		t.Fatal("Replace succeeded")
	}
	for _, want := range []string{exe + ".old", exe, "rename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
	if read(t, exe+".old") != "v2" {
		t.Fatalf(".old = %q: the old build must at least survive under its backup name", read(t, exe+".old"))
	}
}

// A directory nobody may write to refuses the very first rename, and then nothing
// has changed. (On Windows a read-only directory does not stop renames, and a
// superuser ignores permissions, so this is a Unix, unprivileged test.)
func TestReplaceInAReadOnlyDirectoryChangesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind here")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("v2"), 0o755)
	os.WriteFile(exe+".new", []byte("v3"), 0o755)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	err := Replace(exe, exe+".new")
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Replace = %v, want a permission error", err)
	}
	if read(t, exe) != "v2" || read(t, exe+".new") != "v3" {
		t.Fatal("files changed although nothing could be done")
	}
}

// Letting an update be installed from a copy that is a backup or a staging file
// would swap the wrong files.
func TestUpdaterRefusesABackupOrStagingCopy(t *testing.T) {
	for _, name := range []string{"go-nx.exe.old", "go-nx.exe.new"} {
		if _, err := New(Config{Version: "0.1.0", Exe: filepath.Join(t.TempDir(), name)}); err == nil {
			t.Errorf("New accepted %s as the exe to update", name)
		}
	}
}

// The one state an interrupted Replace can leave: the build that is running is the
// backup copy, and the real name is gone. Starting that copy puts it back.
func TestRecoverPutsTheBackupBackWhenTheRealNameIsGone(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe+".old", []byte("the good build"), 0o755)
	os.WriteFile(exe+".new", []byte("the update that never got in"), 0o755)

	got, err := Recover(exe + ".old")
	if err != nil {
		t.Fatal(err)
	}
	if got != exe {
		t.Fatalf("Recover returned %q, want %q", got, exe)
	}
	if read(t, exe) != "the good build" || exists(exe+".old") {
		t.Fatalf("exe=%q, .old exists=%v", read(t, exe), exists(exe+".old"))
	}
	if read(t, exe+".new") != "the update that never got in" {
		t.Error("Recover touched the staged update")
	}
}

func TestRecoverLeavesEverythingElseAlone(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe, []byte("current"), 0o755)
	os.WriteFile(exe+".old", []byte("previous"), 0o755)

	// A normal start.
	if got, err := Recover(exe); err != nil || got != exe {
		t.Fatalf("Recover(exe) = %q, %v", got, err)
	}
	// The predecessor, still running from its backup name after an update: the real
	// name exists, so there is nothing to repair and it must not be renamed over it.
	if got, err := Recover(exe + ".old"); err != nil || got != exe+".old" {
		t.Fatalf("Recover(.old) with the real name present = %q, %v", got, err)
	}
	if read(t, exe) != "current" || read(t, exe+".old") != "previous" {
		t.Fatalf("exe=%q .old=%q", read(t, exe), read(t, exe+".old"))
	}
}

// Whatever was started from has to keep its single owner: when the real name cannot
// be restored, the running copy keeps its own path and the problem is reported.
func TestRecoverReportsWhatItCannotRepair(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-nx.exe")
	os.WriteFile(exe+".old", []byte("the good build"), 0o755)
	was := renameFile
	renameFile = func(from, to string) error { return errors.New("denied") }
	t.Cleanup(func() { renameFile = was })

	got, err := Recover(exe + ".old")
	if err == nil {
		t.Fatal("Recover reported success")
	}
	if got != exe+".old" {
		t.Fatalf("Recover returned %q, want the path it was given", got)
	}
}

// The backup of the previous build is the way back from a bad update, so it is
// kept until the new build has been running for a while, not removed on the way in.
func TestTheBackupOutlivesTheStartOfTheNewBuild(t *testing.T) {
	f := newFixture(t, offer{})
	os.WriteFile(f.exe+".old", []byte("previous"), 0o755)

	slow, err := New(Config{BaseURL: f.srv.URL, Version: "0.1.0", Exe: f.exe, Keys: f.keys, FirstDelay: time.Hour, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { slow.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if !exists(f.exe + ".old") {
		t.Fatal("the backup was removed before the new build had run for its first delay")
	}

	quick := f.updater(t, "0.1.0") // first delay 10 ms
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go quick.Run(ctx2)
	waitFor(t, func() bool { return !exists(f.exe + ".old") })
}

func TestInstalledLabel(t *testing.T) {
	if got := (Status{State: Installed, Version: "0.1.1"}).Label(); !strings.Contains(got, "0.1.1") || !strings.Contains(strings.ToLower(got), "restart") {
		t.Fatalf("label = %q", got)
	}
}

// Nothing in the package may hold the status lock across the slow parts.
func TestCheckNowDuringInstallIsHarmless(t *testing.T) {
	_, u := ready(t, "new build")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); u.Install() }()
		go func() { defer wg.Done(); u.CheckNow(); _ = u.Status().Label() }()
	}
	wg.Wait()
	if st := u.Status(); st.State != Installed {
		t.Fatalf("status = %+v (%v)", st, fmt.Sprint(st.Err))
	}
}
