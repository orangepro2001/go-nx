package trust

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Verdict is the host's decision about a connecting viewer.
type Verdict int

const (
	Allowed     Verdict = iota // verified: a paired device, or one that offered the right password or pairing code
	Untrusted                  // not paired, no pairing open, no (valid) password offered
	BadPassword                // a password was offered and it is wrong
	Locked                     // too many wrong passwords recently
	AllowedOpen                // let in only because the host is in open mode: not verified
)

// Limits on guessing the host password. They apply per source address, so one
// PC hammering the host cannot lock anyone else out.
const (
	maxFailures = 5                // wrong passwords from one source before it is locked out
	lockout     = time.Minute      // the first lockout; each one in a row doubles it
	maxLockout  = 15 * time.Minute // ...up to this
	failWindow  = 10 * time.Minute // failures older than this are forgotten
	maxSources  = 1024             // sources tracked at once; the table never grows past this

	// maxPairFails is how many wrong pairing codes, from anyone, close an open
	// pairing window. A 6-digit code survives that many guesses with odds of
	// better than 99.998%.
	maxPairFails = 10
)

// source is what the policy remembers about one address that guessed wrong.
type source struct {
	fails       int       // wrong passwords inside failWindow
	lastFail    time.Time // when the latest one happened
	lockedUntil time.Time
	strikes     int // lockouts in a row; each one lasts twice as long as the last
}

// Admitted reports whether the viewer may control the PC: Allowed or AllowedOpen.
func (v Verdict) Admitted() bool { return v == Allowed || v == AllowedOpen }

// Policy decides who may control this PC. Verification is optional: until
// SetRequireAuth(true), every viewer is allowed. Once it is on, in order:
//  1. a device whose fingerprint was paired before is allowed;
//  2. while pairing is open, the next new device is paired and allowed;
//  3. otherwise the optional password, if the host has one, must match.
//
// Wrong passwords are counted; after five the host refuses password attempts for
// a minute so a short password cannot be guessed over the network, unless
// SetLockout(false) turns that off.
type Policy struct {
	mu          sync.Mutex
	trusted     *Store
	requireAuth bool
	lockout     bool // a source that keeps guessing wrong is locked out for a while
	password    string
	pairUntil   time.Time
	pairCode    string             // if set, the code a new device must enter to pair
	pairFails   int                // wrong codes since the window opened
	sources     map[string]*source // by host part of the address, for password attempts
	now         func() time.Time
}

// NewPolicy starts strict: until SetRequireAuth(false) is called, a viewer must
// be paired or offer the right password or pairing code.
func NewPolicy(trusted *Store) *Policy {
	return &Policy{trusted: trusted, requireAuth: true, lockout: true, now: time.Now, sources: map[string]*source{}}
}

// Lockout reports whether a source that guesses wrong too often is locked out.
func (p *Policy) Lockout() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lockout
}

// SetLockout turns the lockout after wrong passwords on or off. Either way the
// sources counted so far are forgotten, so turning it off lets in at once a device
// that is locked out now. Wrong pairing codes still close the pairing window
// (maxPairFails): that is what keeps a code from being guessed.
func (p *Policy) SetLockout(on bool) {
	p.mu.Lock()
	p.lockout = on
	clear(p.sources)
	p.mu.Unlock()
}

// RequireAuth reports whether strict mode is on.
func (p *Policy) RequireAuth() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requireAuth
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

// StartPairing opens a pairing window in which the next new device is paired
// and allowed, with no code to enter.
func (p *Policy) StartPairing(d time.Duration) { p.OpenPairing("", d) }

// OpenPairing opens pairing for d. With a non-empty code, a new device is paired
// only if it offers that code as its password, once: the window closes when it
// is used, and after maxPairFails wrong codes. With an empty code the next new
// device is paired.
func (p *Policy) OpenPairing(code string, d time.Duration) {
	p.mu.Lock()
	p.pairUntil = p.now().Add(d)
	p.pairCode = code
	p.pairFails = 0
	p.mu.Unlock()
}

// ClosePairing ends the pairing window at once.
func (p *Policy) ClosePairing() {
	p.mu.Lock()
	p.closePairingLocked()
	p.mu.Unlock()
}

func (p *Policy) closePairingLocked() {
	p.pairUntil = time.Time{}
	p.pairCode = ""
	p.pairFails = 0
}

// PairingCode is the code of the open pairing window, or "" if there is none.
func (p *Policy) PairingCode() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.now().Before(p.pairUntil) {
		return p.pairCode
	}
	return ""
}

// NewPairingCode returns a random six-digit code.
func NewPairingCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic("trust: no randomness: " + err.Error()) // the OS random source failing is not recoverable
	}
	return fmt.Sprintf("%06d", n.Int64())
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
// offered password (possibly empty). While a coded pairing window is open the
// password is also how a new device offers the pairing code.
//
// A paired device is always Allowed, in either mode. In open mode everyone else is
// AllowedOpen (let in, not verified) unless they complete a pairing, which still
// works so a device can be paired before the host goes back to strict mode.
func (p *Policy) Check(fp, addr, password string) (Verdict, error) {
	if _, ok := p.trusted.Get(fp); ok {
		return Allowed, nil
	}
	p.mu.Lock()
	var verdict Verdict
	var pair bool
	if p.requireAuth {
		verdict, pair = p.decideLocked(addr, password)
	} else {
		verdict, pair = p.openLocked(password)
	}
	p.mu.Unlock()
	if pair {
		// Failing to save only costs the user another pairing next time.
		return verdict, p.trusted.Set(fp, addr+" "+p.now().Format("2006-01-02 15:04"))
	}
	return verdict, nil
}

// openLocked handles a device that is not paired when the host is in open mode.
// It is let in either way; the only question is whether it just paired. Wrong
// codes are not penalised and nothing is rate-limited, because nothing is being
// checked. Callers hold p.mu.
func (p *Policy) openLocked(password string) (v Verdict, pair bool) {
	pairing := p.now().Before(p.pairUntil)
	switch {
	case pairing && p.pairCode == "":
		p.closePairingLocked() // one device per pairing
		return Allowed, true
	case pairing && password != "" && CodeMatches(password, p.pairCode):
		p.closePairingLocked()
		return Allowed, true
	}
	return AllowedOpen, false
}

// CodeMatches reports whether offered is the pairing code as a person types it.
func CodeMatches(offered, code string) bool {
	return subtle.ConstantTimeCompare([]byte(asCode(offered)), []byte(code)) == 1
}

// asCode reads s as a pairing code is typed: the host shows "123 456", so spaces and
// dashes between the digits do not count, and full-width digits from an input method are
// the same digits.
func asCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsSpace(r) || r == '-':
		case r >= '０' && r <= '９':
			b.WriteRune('0' + r - '０')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Explain says, for the log, what a refused viewer offered and what the host would have
// taken, without either secret: enough to tell a code that had expired from a mistyped
// one, or a code from a password.
func (p *Policy) Explain(password string) string {
	offered := "nothing offered"
	if c := asCode(password); len(c) == 6 && strings.Trim(c, "0123456789") == "" {
		offered = "offered a 6-digit code"
	} else if n := utf8.RuneCountInString(password); n > 0 {
		offered = fmt.Sprintf("offered a password, length %d", n)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	pairing := "pairing not open"
	switch {
	case now.Before(p.pairUntil) && p.pairCode != "":
		pairing = fmt.Sprintf("pairing open, %s left", p.pairUntil.Sub(now).Round(time.Second))
	case now.Before(p.pairUntil):
		pairing = "pairing open without a code"
	case !p.pairUntil.IsZero():
		pairing = fmt.Sprintf("pairing ran out %s ago", now.Sub(p.pairUntil).Round(time.Second))
	}
	host := "no host password"
	if p.password != "" {
		host = "host password set"
	}
	return offered + "; " + pairing + "; " + host
}

// decideLocked handles a device that is not paired. pair is true when the device
// should now be added to the paired set. Callers hold p.mu.
func (p *Policy) decideLocked(addr, password string) (v Verdict, pair bool) {
	now := p.now()
	pairing := now.Before(p.pairUntil)
	coded := pairing && p.pairCode != ""
	if pairing && !coded {
		p.closePairingLocked() // one device per pairing
		return Allowed, true
	}
	if password == "" || (p.password == "" && !coded) {
		return Untrusted, false // nothing offered, or nothing it could match
	}
	key := sourceKey(addr)
	src := p.sources[key]
	if src != nil && now.Before(src.lockedUntil) {
		return Locked, false
	}
	if coded && CodeMatches(password, p.pairCode) {
		p.closePairingLocked()
		delete(p.sources, key)
		return Allowed, true
	}
	if p.password != "" && subtle.ConstantTimeCompare([]byte(password), []byte(p.password)) == 1 {
		delete(p.sources, key)
		return Allowed, false
	}
	if coded {
		if p.pairFails++; p.pairFails >= maxPairFails {
			p.closePairingLocked()
		}
	}
	if !p.lockout {
		return BadPassword, false
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
	return BadPassword, false
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
