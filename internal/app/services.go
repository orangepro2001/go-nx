// Package app is go-nx's interface and the logic behind it, independent of Windows: the
// pages, the dialogs, the session tabs, the connection flow and the viewer. Everything it
// needs from the machine — the hosting side, the stores on disk, the updater, the
// clipboard, the tray — comes in through the interfaces in this file, so the whole
// application can be driven by tests and drawn on any development machine, and the
// Windows program (cmd/go-nx) only supplies the real ones.
package app

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/orangepro2001/go-nx/internal/logging"
	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/update"
)

// Reason a device was turned away, as the host reports it.
type Reason string

const (
	ReasonNone          Reason = ""
	ReasonNotPaired     Reason = "not-paired"
	ReasonWrongPassword Reason = "wrong-password"
	ReasonLockedOut     Reason = "locked-out"
)

// HostInfo is a snapshot of the sharing side for the interface.
type HostInfo struct {
	Err                string // non-empty if sharing could not start
	Addrs              []string
	Port               int
	ID                 string // this PC's short ID, which a first-time viewer is asked to compare
	Paired             int
	Controller         string // address of the device controlling this PC, "" for none
	ControllerVerified bool
	Rejected           string // address of the last device turned away
	RejectedWhy        Reason
	PairingLeft        time.Duration
	PairingCode        string // the code a new device enters as its password; "" when pairing is closed
	RequireAuth        bool   // strict mode
	Lockout            bool   // a device that keeps entering wrong passwords is locked out for a while
	// PortShared names the other programs bound to the hosting port and the addresses they
	// hold ("nx-host.exe (process 4120) on 100.109.77.29"): connections to those addresses
	// reach them, not this PC's host.
	PortShared []string
}

// PairedDevice is a device that is paired with this PC.
type PairedDevice struct {
	Fingerprint string
	Addr        string
	When        time.Time // zero when unknown
}

// HostService is the sharing side of go-nx.
type HostService interface {
	Info() HostInfo
	StartPairing()
	ClosePairing()
	// SetRequireAuth switches between strict (true) and open mode and returns how many
	// sessions that were let in without verification were ended by going strict.
	SetRequireAuth(on bool) (ended int)
	// SetLockout turns the lockout after wrong passwords on or off.
	SetLockout(on bool)
	Password() string
	SetPassword(string)
	Paired() []PairedDevice
	// Revoke forgets one paired device, ending its session if it has one.
	Revoke(fingerprint string) error
	// RevokeAll forgets every paired device and returns how many there were.
	RevokeAll() (int, error)
	// DisconnectController ends the session of the device controlling this PC.
	DisconnectController()
}

// Settings is the small persistent key-value store of preferences.
type Settings interface {
	Get(key string) (string, bool)
	Set(key, val string) error
}

// ProfileStore is the saved connections.
type ProfileStore interface {
	Find(name string) (profiles.Profile, bool)
	Upsert(profiles.Profile) error
	Replace(old string, p profiles.Profile) (string, error)
	Delete(i int) error
	Touch(name string, now time.Time) error
	SetPinned(name string, pinned bool) error
	Rename(old, name string) (string, error)
}

// UpdateService is the self-updater. It may be nil in a build that does not update.
type UpdateService interface {
	Status() update.Status
	// Click does what the update button does: check, or install and restart.
	Click()
	// RestartPending reports whether clicking restarts go-nx (so a second click confirms).
	RestartPending() bool
}

// Platform is what the operating system provides.
type Platform interface {
	Quit()
	HideWindow()
	WindowHidden() bool
	Clipboard() kit.Clipboard
	// SystemDark reports whether Windows is set to dark apps; ok is false when unknown.
	SystemDark() (dark, ok bool)
	// PreferredLanguages are the user's interface languages, most preferred first.
	PreferredLanguages() []string
	// ApplyTheme is told the theme in force, to colour the title bar.
	ApplyTheme(dark bool)
	MessageBox(title, text string)
}

// Conn is a connection to a host, as the viewer uses it. *remote.Client is one.
type Conn interface {
	Run(ctx context.Context, onFrame func(*tile.Frame, []tile.Rect), onReceipt func(opID uint64, status byte)) error
	SendInput(proto.Input) error
	Close()
}

// Connector makes connections and keeps the identity of hosts it has met.
type Connector interface {
	Pinned(addr string) (fingerprint string, ok bool)
	Probe(ctx context.Context, addr string) (fingerprint string, err error)
	Pin(addr, fingerprint string) error
	Forget(addr string) error
	Dial(ctx context.Context, addr, password string) (Conn, error)
}

// Config is what the program running the application gives it.
type Config struct {
	Version   string // "0.7.0"; "dev" for a build that was not released
	Host      HostService
	Profiles  ProfileStore
	Settings  Settings
	Connector Connector
	Updater   UpdateService
	Platform  Platform
	Logs      *logging.Logger
	LogPath   string
	Connect   string           // address to prefill, from -connect
	Now       func() time.Time // nil: time.Now
	// DefaultCursor etc. are decided by the shell, not here.
}

// realConnector connects with the remote package.
type realConnector struct {
	id    tls.Certificate
	hosts *trust.Store
}

// NewConnector is the Connector that speaks the real protocol, with this PC's identity
// and the store of host identities pinned so far.
func NewConnector(id tls.Certificate, hosts *trust.Store) Connector {
	return realConnector{id: id, hosts: hosts}
}
