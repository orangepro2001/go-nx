// Package trust holds this PC's identity and the sets of fingerprints it trusts.
package trust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"go-nx/internal/fsutil"
)

// Fingerprint is the hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Identity loads the self-signed certificate stored in dir/identity.pem,
// creating it on first use. The same identity is used whether this PC acts as
// host or as viewer.
func Identity(dir string) (tls.Certificate, error) {
	path := filepath.Join(dir, "identity.pem")
	if pemData, err := os.ReadFile(path); err == nil {
		return tls.X509KeyPair(pemData, pemData)
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	name, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "go-nx " + name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	pemData := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	if err := fsutil.WriteFileAtomic(path, pemData, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(pemData, pemData)
}

// Store is a small persistent string map kept as a JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	m    map[string]string
}

// OpenStore loads path, or starts empty if it does not exist.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, m: map[string]string{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.m); err != nil {
		return nil, err
	}
	return s, nil
}

// OpenStoreRecovering is OpenStore for start-up: a file that exists but cannot
// be parsed is moved aside (its new name is returned as backup) and the store
// starts empty, so one damaged file cannot keep the program from starting. The
// caller should tell the user, because whatever was in the file is gone from the
// program's point of view (a trusted store loses its pairings).
func OpenStoreRecovering(path string) (s *Store, backup string, err error) {
	s, err = OpenStore(path)
	if err == nil {
		return s, "", nil
	}
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	if !errors.As(err, &syn) && !errors.As(err, &typ) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, "", err // unreadable (permissions, I/O): not ours to paper over
	}
	if backup, err = fsutil.Quarantine(path); err != nil {
		return nil, "", err
	}
	return &Store{path: path, m: map[string]string{}}, backup, nil
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}

// Bool reads a flag stored as "1" or "0". A key that was never set gives def,
// so a new default reaches installs that never touched the setting while an
// explicit choice is kept; any other stored value counts as false.
func (s *Store) Bool(key string, def bool) bool {
	v, ok := s.Get(key)
	if !ok {
		return def
	}
	return v == "1"
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// Set stores key=val and writes the file.
func (s *Store) Set(key, val string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = val
	return s.saveLocked()
}

// Delete removes key and writes the file. A key that is not there is not an error.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok {
		return nil
	}
	delete(s.m, key)
	return s.saveLocked()
}

// Clear removes every entry, writes the file, and returns how many there were.
func (s *Store) Clear() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.m)
	if n == 0 {
		return 0, nil
	}
	clear(s.m)
	return n, s.saveLocked()
}

// Keys returns the stored keys in sorted order.
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.m))
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.path, b, 0o600)
}
