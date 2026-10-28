// Package update keeps go-nx current from the release-publish server: it asks
// for a newer build now and then, downloads and verifies it next to the running
// exe, and swaps it in when the user asks. Nothing here is Windows-specific, so
// it is tested on any platform.
package update

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
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
)

// Status is a snapshot for the tray.
type Status struct {
	State     State
	Version   string // version name of the offered build (Downloading, Ready)
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
	kick    chan struct{}

	mu sync.Mutex
	st Status
}

// New builds an Updater for cfg.Version. A version that is not MAJOR.MINOR.PATCH
// (a "dev" build) is an error: such a build has no versionCode to compare.
func New(cfg Config) (*Updater, error) {
	have, err := relproto.VersionCodeFromName(cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	if cfg.Exe == "" {
		return nil, errors.New("update: no exe path")
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

// CheckNow asks Run to check right away. It does nothing while a check or
// download is running or an update is already waiting.
func (u *Updater) CheckNow() {
	switch u.Status().State {
	case Checking, Downloading, Ready:
		return
	}
	select {
	case u.kick <- struct{}{}:
	default:
	}
}

// Run checks after the first delay and then every interval, or when CheckNow
// is called, until ctx ends.
func (u *Updater) Run(ctx context.Context) {
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
func (u *Updater) NewPath() string { return u.exe + ".new" }

func (u *Updater) checkOnce(ctx context.Context) {
	if u.Status().State == Ready {
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
	u.set(Status{State: Ready, Version: m.VersionName, Mandatory: res.Mandatory})
}

// Install swaps the downloaded build in. The caller restarts afterwards.
func (u *Updater) Install() error {
	if u.Status().State != Ready {
		return errors.New("update: no verified update is waiting")
	}
	return Replace(u.exe, u.NewPath())
}

// Replace puts next in place of exe. A running exe cannot be overwritten on
// Windows but it can be renamed, so the old build moves aside to exe+".old"
// first. If the second rename fails the first is undone.
func Replace(exe, next string) error {
	old := exe + ".old"
	_ = os.Remove(old) // left by an earlier update; fails harmlessly while that process still runs
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("update: move the running exe aside: %w", err)
	}
	if err := os.Rename(next, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return fmt.Errorf("update: install the new build: %w (restoring the old one also failed: %v)", err, rerr)
		}
		return fmt.Errorf("update: install the new build: %w", err)
	}
	return nil
}

// CleanupOld removes the build that Replace moved aside. It is fine if the
// file is still in use: the next start tries again.
func CleanupOld(exe string) { _ = os.Remove(exe + ".old") }
