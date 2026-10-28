package trust

import (
	"crypto/subtle"
	"net"
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

// Limits on guessing the host password. They apply per source address, so one
// PC hammering the host cannot lock anyone else out.
const (
	maxFailures = 5                // wrong passwords from one source before it is locked out
	lockout     = time.Minute      // the first lockout; each one in a row doubles it
	maxLockout  = 15 * time.Minute // ...up to this
	failWindow  = 10 * time.Minute // failures older than this are forgotten
	maxSources  = 1024             // sources tracked at once; the table never grows past this
)

// source is what the policy remembers about one address that guessed wrong.
type source struct {
	fails       int       // wrong passwords inside failWindow
	lastFail    time.Time // when the latest one happened
	lockedUntil time.Time
	strikes     int // lockouts in a row; each one lasts twice as long as the last
}

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
	sources     map[string]*source // by host part of the address, for password attempts
	now         func() time.Time
}

func NewPolicy(trusted *Store) *Policy {
	return &Policy{trusted: trusted, now: time.Now, sources: map[string]*source{}}
}

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
	now, key := p.now(), sourceKey(addr)
	src := p.sources[key]
	if src != nil && now.Before(src.lockedUntil) {
		return Locked, nil
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(p.password)) == 1 {
		delete(p.sources, key)
		return Allowed, nil
	}
	if src == nil {
		p.makeRoom(now)
		src = &source{}
		p.sources[key] = src
	}
	if now.Sub(src.lastFail) > failWindow {
		src.fails = 0 // quiet for a while: earlier mistakes no longer count
	}
	if now.Sub(src.lastFail) > 2*maxLockout {
		src.strikes = 0 // and long enough that the escalation starts over too
	}
	src.fails++
	src.lastFail = now
	if src.fails >= maxFailures {
		src.fails = 0
		src.strikes++
		d := lockout << min(src.strikes-1, 10)
		src.lockedUntil = now.Add(min(d, maxLockout))
	}
	return BadPassword, nil
}

// sourceKey is the part of addr that identifies a PC: its IP, not the port,
// which changes with every connection.
func sourceKey(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// makeRoom keeps the table below maxSources before a new source is added: it
// drops sources whose lockout and failures have run out, and if that is not
// enough, the one that failed longest ago. Callers hold p.mu.
func (p *Policy) makeRoom(now time.Time) {
	if len(p.sources) < maxSources {
		return
	}
	for k, s := range p.sources {
		if !now.Before(s.lockedUntil) && now.Sub(s.lastFail) > failWindow {
			delete(p.sources, k)
		}
	}
	for len(p.sources) >= maxSources {
		var oldest string
		var at time.Time
		for k, s := range p.sources {
			if oldest == "" || s.lastFail.Before(at) {
				oldest, at = k, s.lastFail
			}
		}
		delete(p.sources, oldest)
	}
}
