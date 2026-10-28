package trust

import (
	"crypto/subtle"
	"sync"
	"time"
)

// Verdict is the host's decision about a connecting viewer.
type Verdict int

const (
	Allowed     Verdict = iota
	Untrusted           // not paired, no pairing open, no (valid) password offered
	BadPassword         // a password was offered and it is wrong
	Locked              // too many wrong passwords recently
)

const (
	maxFailures = 5
	lockout     = time.Minute
)

// Policy decides who may control this PC. Verification is optional: until
// SetRequireAuth(true), every viewer is allowed. Once it is on, in order:
//  1. a device whose fingerprint was paired before is allowed;
//  2. while pairing is open, the next new device is paired and allowed;
//  3. otherwise the optional password, if the host has one, must match.
//
// Wrong passwords are counted; after five the host refuses password attempts for
// a minute so a short password cannot be guessed over the network.
type Policy struct {
	mu          sync.Mutex
	trusted     *Store
	requireAuth bool
	password    string
	pairUntil   time.Time
	fails       int
	lockedUntil time.Time
	now         func() time.Time
}

func NewPolicy(trusted *Store) *Policy { return &Policy{trusted: trusted, now: time.Now} }

// SetRequireAuth turns verification on or off.
func (p *Policy) SetRequireAuth(on bool) {
	p.mu.Lock()
	p.requireAuth = on
	p.mu.Unlock()
}

func (p *Policy) SetPassword(pw string) {
	p.mu.Lock()
	p.password = pw
	p.mu.Unlock()
}

func (p *Policy) StartPairing(d time.Duration) {
	p.mu.Lock()
	p.pairUntil = p.now().Add(d)
	p.mu.Unlock()
}

// PairingLeft is how long pairing stays open; zero if closed.
func (p *Policy) PairingLeft() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(p.pairUntil.Sub(p.now()), 0)
}

func (p *Policy) TrustedCount() int { return p.trusted.Len() }

// Revoke forgets the paired device with certificate fingerprint fp. It is a
// stranger again: it must pair or offer the password like any new device.
func (p *Policy) Revoke(fp string) error { return p.trusted.Delete(fp) }

// RevokeAll forgets every paired device and returns how many there were.
func (p *Policy) RevokeAll() (int, error) { return p.trusted.Clear() }

// Check decides about a viewer with certificate fingerprint fp from addr, which
// offered password (possibly empty).
func (p *Policy) Check(fp, addr, password string) (Verdict, error) {
	p.mu.Lock()
	open := !p.requireAuth
	p.mu.Unlock()
	if open {
		return Allowed, nil
	}
	if _, ok := p.trusted.Get(fp); ok {
		return Allowed, nil
	}
	p.mu.Lock()
	if p.now().Before(p.pairUntil) {
		p.pairUntil = time.Time{} // one device per pairing
		p.mu.Unlock()
		// Failing to save only costs the user another pairing next time.
		return Allowed, p.trusted.Set(fp, addr+" "+p.now().Format("2006-01-02 15:04"))
	}
	defer p.mu.Unlock()
	if password == "" || p.password == "" {
		return Untrusted, nil
	}
	if p.now().Before(p.lockedUntil) {
		return Locked, nil
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(p.password)) == 1 {
		p.fails = 0
		return Allowed, nil
	}
	p.fails++
	if p.fails >= maxFailures {
		p.fails = 0
		p.lockedUntil = p.now().Add(lockout)
	}
	return BadPassword, nil
}
