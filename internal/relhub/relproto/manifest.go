package relproto

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// APIVersion is bumped only for incompatible changes of this package.
const APIVersion = 1

const (
	signaturePrefix = "relhub-release-v1"

	// MaxPackageBytes bounds what a manifest may announce; a signed manifest is
	// trusted, but a typo must not make a client plan for a 10 TB download.
	MaxPackageBytes = int64(16) << 30
	// MaxNotesBytes bounds release notes.
	MaxNotesBytes = 8192

	DefaultChannel = "stable"
)

var (
	appIDRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	platformRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,23}$`)
	channelRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)
	fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	sha256Re   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	semverRe   = regexp.MustCompile(`^(\d{1,3})\.(\d{1,2})\.(\d{1,2})$`)
)

// ValidAppID reports whether s is a legal application id.
func ValidAppID(s string) bool { return appIDRe.MatchString(s) }

// ValidPlatform reports whether s is a legal platform name (android, win-x64, ...).
func ValidPlatform(s string) bool { return platformRe.MatchString(s) }

// ValidChannel reports whether s is a legal channel name (stable, beta, ...).
func ValidChannel(s string) bool { return channelRe.MatchString(s) }

// ValidFileName reports whether s is safe to use as a bare file name.
func ValidFileName(s string) bool { return fileNameRe.MatchString(s) && !strings.Contains(s, "..") }

// ValidSHA256 reports whether s is 64 lowercase hex characters.
func ValidSHA256(s string) bool { return sha256Re.MatchString(s) }

// ValidVersionName reports whether s can be shown as a version.
func ValidVersionName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// VersionCodeFromName derives a versionCode from "MAJOR.MINOR.PATCH" as
// major*10000 + minor*100 + patch (1.6.1 -> 10601). Minor and patch must be < 100.
func VersionCodeFromName(name string) (int64, error) {
	m := semverRe.FindStringSubmatch(name)
	if m == nil {
		return 0, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", name)
	}
	major, _ := strconv.ParseInt(m[1], 10, 64)
	minor, _ := strconv.ParseInt(m[2], 10, 64)
	patch, _ := strconv.ParseInt(m[3], 10, 64)
	code := major*10000 + minor*100 + patch
	if code < 1 {
		return 0, fmt.Errorf("%q gives versionCode 0", name)
	}
	return code, nil
}

// Release is one published package of one app on one platform. Its file never
// changes once published; only the yank flag can.
type Release struct {
	App         string `json:"app"`
	Platform    string `json:"platform"`
	VersionCode int64  `json:"versionCode"`
	VersionName string `json:"versionName"`
	File        string `json:"file"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"publishedAt"`
	Yanked      bool   `json:"yanked,omitempty"`
	YankReason  string `json:"yankReason,omitempty"`
}

// Validate checks the shape of a release record.
func (r *Release) Validate() error {
	return validateFields(r.App, r.Platform, "", r.VersionCode, r.VersionName, r.File, r.Size, r.SHA256, 0, r.Notes, r.PublishedAt)
}

// Manifest is what a client receives and verifies: one release as it is
// offered on one channel, plus that channel's minimum version, signed by the
// server. Everything except KeyID and Signature is covered by the signature
// (Notes through its hash).
type Manifest struct {
	App            string `json:"app"`
	Platform       string `json:"platform"`
	Channel        string `json:"channel"`
	VersionCode    int64  `json:"versionCode"`
	VersionName    string `json:"versionName"`
	File           string `json:"file"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	MinVersionCode int64  `json:"minVersionCode"`
	Notes          string `json:"notes,omitempty"`
	PublishedAt    string `json:"publishedAt"`
	KeyID          string `json:"keyId"`
	Signature      string `json:"signature"`
}

func validateFields(app, platform, channel string, code int64, name, file string, size int64, sum string, minCode int64, notes, publishedAt string) error {
	switch {
	case !ValidAppID(app):
		return Errorf(CodeInvalid, "bad app id %q", app)
	case !ValidPlatform(platform):
		return Errorf(CodeInvalid, "bad platform %q", platform)
	case channel != "" && !ValidChannel(channel):
		return Errorf(CodeInvalid, "bad channel %q", channel)
	case code < 1:
		return Errorf(CodeInvalid, "versionCode must be positive")
	case !ValidVersionName(name):
		return Errorf(CodeInvalid, "bad versionName")
	case !ValidFileName(file):
		return Errorf(CodeInvalid, "bad file name %q", file)
	case size < 1 || size > MaxPackageBytes:
		return Errorf(CodeInvalid, "size %d out of range", size)
	case !ValidSHA256(sum):
		return Errorf(CodeInvalid, "sha256 must be 64 lowercase hex characters")
	case minCode < 0 || minCode > code:
		return Errorf(CodeInvalid, "minVersionCode must be between 0 and versionCode")
	case len(notes) > MaxNotesBytes:
		return Errorf(CodeInvalid, "notes longer than %d bytes", MaxNotesBytes)
	}
	if _, err := time.Parse(time.RFC3339, publishedAt); err != nil || len(publishedAt) > 64 {
		return Errorf(CodeInvalid, "publishedAt is not RFC 3339")
	}
	return nil
}

// Validate checks the shape of a manifest (not its signature).
func (m *Manifest) Validate() error {
	if m.Channel == "" {
		return Errorf(CodeInvalid, "manifest has no channel")
	}
	return validateFields(m.App, m.Platform, m.Channel, m.VersionCode, m.VersionName, m.File, m.Size, m.SHA256, m.MinVersionCode, m.Notes, m.PublishedAt)
}

// signingPayload is the exact byte string that is signed: one key=value line
// per field. Validate rejects control characters in every single-line field
// and notes enter as a hash, so no field can bleed into another.
func (m *Manifest) signingPayload() []byte {
	notes := sha256.Sum256([]byte(m.Notes))
	var b strings.Builder
	b.WriteString(signaturePrefix + "\n")
	fmt.Fprintf(&b, "app=%s\n", m.App)
	fmt.Fprintf(&b, "platform=%s\n", m.Platform)
	fmt.Fprintf(&b, "channel=%s\n", m.Channel)
	fmt.Fprintf(&b, "versionCode=%d\n", m.VersionCode)
	fmt.Fprintf(&b, "versionName=%s\n", m.VersionName)
	fmt.Fprintf(&b, "file=%s\n", m.File)
	fmt.Fprintf(&b, "size=%d\n", m.Size)
	fmt.Fprintf(&b, "sha256=%s\n", m.SHA256)
	fmt.Fprintf(&b, "minVersionCode=%d\n", m.MinVersionCode)
	fmt.Fprintf(&b, "publishedAt=%s\n", m.PublishedAt)
	fmt.Fprintf(&b, "notesSha256=%s\n", hex.EncodeToString(notes[:]))
	return []byte(b.String())
}

// Sign validates the manifest and stores KeyID and the Ed25519 signature.
func (m *Manifest) Sign(priv ed25519.PrivateKey) error {
	if err := m.Validate(); err != nil {
		return err
	}
	m.KeyID = KeyID(priv.Public().(ed25519.PublicKey))
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m.signingPayload()))
	return nil
}

// Verify checks shape and requires a valid signature from any of keys.
func (m *Manifest) Verify(keys []ed25519.PublicKey) error {
	if err := m.Validate(); err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Errorf(CodeInvalid, "malformed signature")
	}
	payload := m.signingPayload()
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, payload, sig) {
			return nil
		}
	}
	return Errorf(CodeInvalid, "signature is not from a trusted release key")
}

// Mandatory reports whether a client at currentCode has to update.
func (m *Manifest) Mandatory(currentCode int64) bool { return m.MinVersionCode > currentCode }

// DownloadPath is the version-pinned download path of the manifest's package,
// relative to the API root (no leading slash).
func (m *Manifest) DownloadPath() string {
	return fmt.Sprintf("v1/apps/%s/%s/releases/%d/download", m.App, m.Platform, m.VersionCode)
}

// KeyID is a short stable identifier of a public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// ParsePublicKey decodes a base64 Ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("not a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}

// EncodePublicKey is the inverse of ParsePublicKey.
func EncodePublicKey(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

// GenerateKeyFile creates a new signing key. The base64 seed is written with
// mode 0600 to path, never overwriting an existing file.
func GenerateKeyFile(path string) (ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return nil, werr
	}
	return pub, nil
}

// LoadPrivateKey reads a key file written by GenerateKeyFile.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s is not a base64 Ed25519 seed", path)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
