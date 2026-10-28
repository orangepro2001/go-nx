// Package update keeps go-nx current from the release-publish server: it asks
// for a newer build now and then, downloads and verifies it next to the running
// exe, and swaps it in when the user asks. Nothing here is Windows-specific, so
// it is tested on any platform.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/orangepro2001/go-nx/internal/relhub/relclient"
	"github.com/orangepro2001/go-nx/internal/relhub/relproto"
)

const (
	// BaseURL is where the release server is reachable inside the tailnet,
	// including the path prefix tailscale serve adds.
	BaseURL  = "https://tony-ai-pc-1.tail9f7f1f.ts.net/release"
	AppID    = "go-nx"
	Platform = "win-x64"

	// FirstCheckDelay keeps the update check out of the way of startup.
	FirstCheckDelay = 30 * time.Second
	// CheckInterval is the time between checks while the app keeps running.
	CheckInterval = 6 * time.Hour

	checkTimeout    = 30 * time.Second
	downloadTimeout = 30 * time.Minute
)

// PinnedKeys are the release server's Ed25519 public keys this build trusts.
// They are compiled in on purpose: a key fetched from the same server would let
// a hostile server vouch for itself. Before running `relhub keys rotate`, ship a
// build that lists the new key as well as the old one (docs/release.md); a build
// that lacks the new key stops accepting updates once the server signs with it.
var PinnedKeys = []string{
	"k4thZR+OVYSGAT/9j5F39csgGqTL0t3X6GYdNCmuRI8=", // keyId f5faa9f9858b789f
}

// State is where the updater is in its cycle.
type State int

const (
	Idle        State = iota // nothing checked yet
	Checking                 // asking the server
	Current                  // the last check found nothing newer
	Downloading              // a newer build is being fetched
	Ready                    // a verified newer build waits next to the exe
	Failed                   // the last check or download failed
	Installed                // the new build replaced the exe; a restart starts it
)

// Status is a snapshot for the tray.
type Status struct {
	State     State
	Version   string // version name of the offered build (Downloading, Ready, Installed)
	Percent   int    // download progress (Downloading)
	Mandatory bool   // the server's signed minimum version is above this build
	Err       error  // why it failed (Failed)
}

// Label is the text of the tray's update entry.
func (s Status) Label() string {
	switch s.State {
	case Checking:
		return "Checking for updates..."
	case Current:
		return "Up to date - check again"
	case Downloading:
		return fmt.Sprintf("Downloading v%s (%d%%)...", s.Version, s.Percent)
	case Ready:
		if s.Mandatory {
			return "Restart to update to v" + s.Version + " (required)"
		}
		return "Restart to update to v" + s.Version
	case Failed:
		return "Update check failed - retry"
	case Installed:
		return "Updated to v" + s.Version + " - restart go-nx to use it"
	}
	return "Check for updates"
}

// Config describes the running build and where to look for a newer one.
type Config struct {
	BaseURL    string   // default BaseURL
	Version    string   // MAJOR.MINOR.PATCH of this build
	Exe        string   // path of the running exe, which is also where the new build lands
	Keys       []string // base64 public keys to trust; default PinnedKeys
	FirstDelay time.Duration
	Interval   time.Duration
	RetryWait  time.Duration // pause before the first download retry; default 1 s
}

// Updater checks, downloads and installs. All methods are safe for concurrent use.
type Updater struct {
	client  *relclient.Client
	target  relclient.Target
	have    int64
	version string
	exe     string
	first   time.Duration
	every   time.Duration
	retry   time.Duration
	kick    chan struct{}

	// installMu makes Install one at a time. It is separate from mu because an
	// install hashes and moves a whole executable, and the interface asks Status
	// for the state on every frame.
	installMu sync.Mutex

	mu    sync.Mutex
	st    Status
	offer *relproto.Manifest // the verified manifest of the build that is Ready
}

// New builds an Updater for cfg.Version. A version that is not MAJOR.MINOR.PATCH
// (a "dev" build) is an error: such a build has no versionCode to compare. So is
// an exe that is the backup or staging copy of another one, which an update
// would swap into the wrong place.
func New(cfg Config) (*Updater, error) {
	have, err := relproto.VersionCodeFromName(cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	if cfg.Exe == "" {
		return nil, errors.New("update: no exe path")
	}
	if strings.HasSuffix(cfg.Exe, oldSuffix) || strings.HasSuffix(cfg.Exe, newSuffix) {
		return nil, fmt.Errorf("update: %s is a backup or staging copy, not the installed exe", cfg.Exe)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = BaseURL
	}
	if len(cfg.Keys) == 0 {
		cfg.Keys = PinnedKeys
	}
	if cfg.FirstDelay <= 0 {
		cfg.FirstDelay = FirstCheckDelay
	}
	if cfg.Interval <= 0 {
		cfg.Interval = CheckInterval
	}
	keys, err := relclient.ParseTrustedKeys(cfg.Keys...)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	c, err := relclient.New(cfg.BaseURL, relclient.WithTrustedKeys(keys...), relclient.WithUserAgent("github.com/orangepro2001/go-nx/"+cfg.Version))
	if err != nil {
		return nil, err
	}
	return &Updater{
		client:  c,
		target:  relclient.Target{App: AppID, Platform: Platform},
		have:    have,
		version: cfg.Version,
		exe:     cfg.Exe,
		first:   cfg.FirstDelay,
		every:   cfg.Interval,
		retry:   cfg.RetryWait,
		kick:    make(chan struct{}, 1),
	}, nil
}

// Status returns the current state.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *Updater) set(s Status) {
	u.mu.Lock()
	u.st = s
	u.mu.Unlock()
}

// setReady records the verified build that waits to be installed.
func (u *Updater) setReady(s Status, m relproto.Manifest) {
	u.mu.Lock()
	u.st, u.offer = s, &m
	u.mu.Unlock()
}

// CheckNow asks Run to check right away. It does nothing while a check or
// download is running or an update is already waiting or installed.
func (u *Updater) CheckNow() {
	switch u.Status().State {
	case Checking, Downloading, Ready, Installed:
		return
	}
	select {
	case u.kick <- struct{}{}:
	default:
	}
}

// Run checks after the first delay and then every interval, or when CheckNow
// is called, until ctx ends. It also removes the backup of the previous build once
// the first delay has passed: until a build has run that long, the backup is the
// way back from one that does not work.
func (u *Updater) Run(ctx context.Context) {
	go func() {
		t := time.NewTimer(u.first)
		defer t.Stop()
		select {
		case <-t.C:
			CleanupOld(u.exe)
		case <-ctx.Done():
		}
	}()
	wait := u.first
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-u.kick:
			t.Stop()
		}
		u.checkOnce(ctx)
		wait = u.every
	}
}

// Exe is the path of the running exe as given to New.
func (u *Updater) Exe() string { return u.exe }

// NewPath is where the downloaded build waits: next to the exe, so that the
// final rename stays on one volume.
func (u *Updater) NewPath() string { return u.exe + newSuffix }

func (u *Updater) checkOnce(ctx context.Context) {
	switch u.Status().State {
	case Ready, Installed:
		return
	}
	u.set(Status{State: Checking})
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	res, err := u.client.Check(cctx, u.target, u.have)
	cancel()
	switch {
	case err != nil && relproto.AsError(err).Code == relproto.CodeNotFound:
		u.set(Status{State: Current}) // nothing published yet
		return
	case err != nil:
		log.Printf("update: check failed: %v", err)
		u.set(Status{State: Failed, Err: err})
		return
	case !res.UpdateAvailable:
		log.Printf("update: v%s is current (server offers v%s)", u.version, res.Manifest.VersionName)
		u.set(Status{State: Current})
		return
	}

	m := res.Manifest
	log.Printf("update: v%s offered (versionCode %d, %d bytes, mandatory=%v)", m.VersionName, m.VersionCode, m.Size, res.Mandatory)
	st := Status{State: Downloading, Version: m.VersionName, Mandatory: res.Mandatory}
	u.set(st)
	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	err = u.client.Download(dctx, *m, u.NewPath(), &relclient.DownloadOptions{
		RetryWait: u.retry,
		Progress: func(done, total int64) {
			if total <= 0 {
				return
			}
			if p := int(done * 100 / total); p != st.Percent {
				st.Percent = p
				u.set(st)
			}
		},
	})
	if err != nil {
		log.Printf("update: download of v%s failed: %v", m.VersionName, err)
		u.set(Status{State: Failed, Err: err})
		return
	}
	log.Printf("update: v%s downloaded and verified at %s", m.VersionName, u.NewPath())
	u.setReady(Status{State: Ready, Version: m.VersionName, Mandatory: res.Mandatory}, *m)
}

// Install swaps the downloaded build in. The caller restarts afterwards.
//
// What is installed is what was verified: the file is checked against the signed
// manifest again here, because it has sat on disk since the download and may have
// been damaged (a full disk, a scanner, a person). If it no longer matches it is
// discarded and the next check fetches it again. Only one install runs at a time,
// and a second one is refused: it would move the new build over the backup of the
// old one.
func (u *Updater) Install() error {
	u.installMu.Lock()
	defer u.installMu.Unlock()

	u.mu.Lock()
	st, m := u.st, u.offer
	u.mu.Unlock()
	if st.State != Ready || m == nil {
		return errors.New("update: no verified update is waiting")
	}
	if err := checkBuild(u.NewPath(), m); err != nil {
		_ = os.Remove(u.NewPath())
		u.set(Status{State: Failed, Err: err})
		return fmt.Errorf("update: the downloaded v%s is not intact any more, so it was discarded (check for updates to fetch it again): %w", m.VersionName, err)
	}
	if err := Replace(u.exe, u.NewPath()); err != nil {
		return err
	}
	u.set(Status{State: Installed, Version: st.Version, Mandatory: st.Mandatory})
	return nil
}

// checkBuild verifies the file at path against a manifest's size and SHA-256.
func checkBuild(path string, m *relproto.Manifest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != m.Size {
		return fmt.Errorf("%w: size is %d, expected %d", relclient.ErrChecksum, n, m.Size)
	}
	if hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return fmt.Errorf("%w: SHA-256 differs", relclient.ErrChecksum)
	}
	return nil
}

const (
	oldSuffix = ".old" // where Replace moves the running exe
	newSuffix = ".new" // where the downloaded build waits
)

var (
	// renameFile and retryPause are variables so that tests can make a rename fail.
	renameFile = os.Rename
	retryPause = 150 * time.Millisecond
)

// swapAttempts is how often a rename that is refused is tried before giving up.
// Right after a download a virus scanner may hold the new file for a moment.
const swapAttempts = 5

// tryRename renames, and tries again a few times if the file is held, which is
// not worth doing when it is simply not there.
func tryRename(from, to string) error {
	var err error
	for i := 0; i < swapAttempts; i++ {
		if i > 0 {
			time.Sleep(retryPause)
		}
		if err = renameFile(from, to); err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return err
}

// Replace puts next in place of exe. A running exe cannot be overwritten on
// Windows but it can be renamed, so the old build moves aside to exe+".old"
// first. Nothing is touched unless both files are there. If the second rename
// is refused it is retried, and if it still fails the first is undone; if even
// that fails, the error says where the old build is, so that nobody is left
// searching for it.
//
// The moment between the two renames is the one place an update can be cut short
// (a crash, a power failure) and leave exe missing. The old build is then at
// exe+".old", and starting that copy puts it back (see Recover).
func Replace(exe, next string) error {
	if fi, err := os.Stat(next); err != nil {
		return fmt.Errorf("update: the new build is not there: %w", err)
	} else if !fi.Mode().IsRegular() {
		return fmt.Errorf("update: %s is not a file", next)
	}
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("update: the exe to replace is not there: %w", err)
	}
	old := exe + oldSuffix
	// A backup of an earlier update is replaced by this rename, not removed
	// beforehand: if the exe cannot be moved, the earlier backup is still there.
	// A backup that cannot be replaced (read-only, or still running from an earlier
	// update) is removed and the rename tried once more.
	if err := renameFile(exe, old); err != nil {
		if rmErr := os.Remove(old); rmErr != nil || renameFile(exe, old) != nil {
			return fmt.Errorf("update: move the running exe aside: %w", err)
		}
	}
	err := tryRename(next, exe)
	if err == nil {
		return nil
	}
	if rerr := tryRename(old, exe); rerr != nil {
		return fmt.Errorf("update: install the new build: %w; putting the old one back also failed (%v). The previous build is %s: rename it to %s to get it back", err, rerr, old, exe)
	}
	return fmt.Errorf("update: install the new build: %w", err)
}

// Recover repairs the one state an interrupted Replace can leave behind: the build
// that is running is the backup copy exe+".old" and the real name is gone, so
// there is nothing to start by its usual name. Started by its backup name, the
// copy takes its real name back and the new path is returned. In any other case
// (a normal start, or the old process of an update that is still running from its
// backup name while the new build holds the real name) nothing is renamed and exe
// is returned unchanged.
func Recover(exe string) (string, error) {
	real, ok := strings.CutSuffix(exe, oldSuffix)
	if !ok {
		return exe, nil
	}
	if _, err := os.Stat(real); err == nil {
		return exe, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return exe, fmt.Errorf("update: cannot tell whether %s exists: %w", real, err)
	}
	if err := renameFile(exe, real); err != nil {
		return exe, fmt.Errorf("update: take the name %s back: %w", real, err)
	}
	return real, nil
}

// CleanupOld removes the build that Replace moved aside. It is fine if the
// file is still in use: the next start tries again.
func CleanupOld(exe string) { _ = os.Remove(exe + oldSuffix) }
