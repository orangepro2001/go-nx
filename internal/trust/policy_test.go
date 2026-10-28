package trust

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newPolicy(t *testing.T) (*Policy, *clock) {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "trusted.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	p := NewPolicy(s)
	p.SetRequireAuth(true)
	p.now = c.now
	return p, c
}

func check(t *testing.T, p *Policy, fp, pw string, want Verdict) {
	t.Helper()
	got, err := p.Check(fp, "10.0.0.2:5000", pw)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Check(%q, %q) = %v, want %v", fp, pw, got, want)
	}
}

func TestUnknownDeviceRejectedByDefault(t *testing.T) {
	p, _ := newPolicy(t)
	check(t, p, "aa", "", Untrusted)
	check(t, p, "aa", "guess", Untrusted) // host has no password
}

func TestPairingTrustsOneDeviceThenCloses(t *testing.T) {
	p, c := newPolicy(t)
	p.StartPairing(2 * time.Minute)
	if p.PairingLeft() != 2*time.Minute {
		t.Fatalf("left = %v", p.PairingLeft())
	}
	check(t, p, "aa", "", Allowed)
	check(t, p, "bb", "", Untrusted) // window closed after the first device
	check(t, p, "aa", "", Allowed)   // remembered
	if p.TrustedCount() != 1 {
		t.Fatalf("trusted = %d", p.TrustedCount())
	}
	p.StartPairing(time.Minute)
	c.t = c.t.Add(2 * time.Minute)
	check(t, p, "cc", "", Untrusted) // window expired
}

func TestPassword(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetPassword("s3cret")
	check(t, p, "aa", "s3cret", Allowed)
	check(t, p, "aa", "s3cret", Allowed) // not remembered as paired, but still works
	check(t, p, "aa", "wrong", BadPassword)
	check(t, p, "aa", "", Untrusted) // no password offered: ask to pair or enter one
	if p.TrustedCount() != 0 {
		t.Fatal("password login must not pair the device")
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	p, c := newPolicy(t)
	p.SetPassword("s3cret")
	for i := 0; i < maxFailures; i++ {
		check(t, p, "aa", "nope", BadPassword)
	}
	check(t, p, "aa", "s3cret", Locked) // even the right password waits
	c.t = c.t.Add(lockout + time.Second)
	check(t, p, "aa", "s3cret", Allowed)
}

func TestLockoutCanBeTurnedOff(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetPassword("s3cret")
	if !p.Lockout() {
		t.Fatal("a new policy must lock out a source that keeps guessing")
	}
	for i := 0; i < maxFailures; i++ {
		check(t, p, "aa", "nope", BadPassword)
	}
	check(t, p, "aa", "s3cret", Locked)
	p.SetLockout(false)
	check(t, p, "aa", "s3cret", Allowed) // turning it off frees a source that is locked out now
	for i := 0; i < 3*maxFailures; i++ {
		check(t, p, "aa", "nope", BadPassword) // and nobody is locked out any more
	}
	check(t, p, "aa", "s3cret", Allowed)
	p.SetLockout(true)
	for i := 0; i < maxFailures; i++ {
		check(t, p, "aa", "nope", BadPassword) // failures while it was off do not count
	}
	check(t, p, "aa", "s3cret", Locked)
}

func TestWrongCodesStillCloseThePairingWithoutLockout(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetLockout(false)
	p.OpenPairing("123456", time.Minute)
	for i := 0; i < maxPairFails; i++ {
		checkFrom(t, p, "10.0.0.66:1", "000000", BadPassword) // one source, never locked out
	}
	if p.PairingLeft() != 0 {
		t.Fatal("guessing the code must still close the pairing window")
	}
	checkFrom(t, p, "10.0.0.66:1", "123456", Untrusted)
}

func TestPairedDeviceIgnoresLockout(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetPassword("s3cret")
	p.StartPairing(time.Minute)
	check(t, p, "aa", "", Allowed)
	for i := 0; i < maxFailures; i++ {
		check(t, p, "zz", "nope", BadPassword)
	}
	check(t, p, "aa", "", Allowed)
}

func TestOpenModeAdmitsStrangersAsUnverified(t *testing.T) {
	s, _ := OpenStore(filepath.Join(t.TempDir(), "trusted.json"))
	p := NewPolicy(s)
	check(t, p, "anyone", "", Untrusted) // a fresh policy is strict: it fails closed
	p.SetRequireAuth(false)
	check(t, p, "anyone", "", AllowedOpen)
	p.SetRequireAuth(true)
	check(t, p, "anyone", "", Untrusted)
	p.SetRequireAuth(false)
	check(t, p, "anyone", "", AllowedOpen)
	if !AllowedOpen.Admitted() || !Allowed.Admitted() || Untrusted.Admitted() || BadPassword.Admitted() || Locked.Admitted() {
		t.Fatal("Admitted must be true for exactly Allowed and AllowedOpen")
	}
}

// A device that was paired under strict mode stays verified when the host is
// opened up, so switching modes never demotes it.
func TestPairedDeviceStaysVerifiedInOpenMode(t *testing.T) {
	p, _ := newPolicy(t)
	p.StartPairing(time.Minute)
	check(t, p, "aa", "", Allowed) // paired in strict mode
	p.SetRequireAuth(false)
	check(t, p, "aa", "", Allowed)           // still verified
	check(t, p, "stranger", "", AllowedOpen) // a stranger is merely let in
	p.SetRequireAuth(true)
	check(t, p, "aa", "", Allowed)
	check(t, p, "stranger", "", Untrusted)
}

// Pairing keeps working while the host is open, so a device can be paired first
// and the host switched to strict mode afterwards.
func TestPairingCodeWorksInOpenMode(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetRequireAuth(false)
	p.OpenPairing("123456", time.Minute)
	checkFrom(t, p, "10.0.0.1:1", "000000", AllowedOpen) // a wrong code is not an error when nothing is checked
	if p.TrustedCount() != 0 {
		t.Fatal("a wrong code paired a device")
	}
	checkFrom(t, p, "10.0.0.2:1", "123456", Allowed) // the right one pairs it, so it is verified
	if p.TrustedCount() != 1 {
		t.Fatalf("TrustedCount = %d, want 1", p.TrustedCount())
	}
	checkFrom(t, p, "10.0.0.3:1", "123456", AllowedOpen) // and the window closed
}

func TestRequireAuthGetter(t *testing.T) {
	p, _ := newPolicy(t)
	if !p.RequireAuth() {
		t.Fatal("newPolicy sets strict mode")
	}
	p.SetRequireAuth(false)
	if p.RequireAuth() {
		t.Fatal("RequireAuth still true after switching to open")
	}
}

func TestRevokeTurnsAPairedDeviceBackIntoAStranger(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetRequireAuth(true)
	p.StartPairing(time.Minute)
	check(t, p, "aa", "", Allowed) // pairs aa
	check(t, p, "aa", "", Allowed) // and it stays paired
	if err := p.Revoke("aa"); err != nil {
		t.Fatal(err)
	}
	check(t, p, "aa", "", Untrusted)
	if p.TrustedCount() != 0 {
		t.Fatalf("TrustedCount = %d after revoke", p.TrustedCount())
	}
}

func TestRevokeAllForgetsEveryDevice(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetRequireAuth(true)
	for _, fp := range []string{"aa", "bb", "cc"} {
		p.StartPairing(time.Minute)
		check(t, p, fp, "", Allowed)
	}
	n, err := p.RevokeAll()
	if err != nil || n != 3 {
		t.Fatalf("RevokeAll = %d, %v; want 3, nil", n, err)
	}
	for _, fp := range []string{"aa", "bb", "cc"} {
		check(t, p, fp, "", Untrusted)
	}
}

func checkFrom(t *testing.T, p *Policy, addr, pw string, want Verdict) {
	t.Helper()
	got, err := p.Check("fp-"+addr, addr, pw)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Check from %s (%q) = %v, want %v", addr, pw, got, want)
	}
}

func TestLockoutIsPerSource(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetPassword("s3cret")
	for i := 0; i < maxFailures; i++ {
		checkFrom(t, p, "10.0.0.66:4000", "nope", BadPassword)
	}
	checkFrom(t, p, "10.0.0.66:4001", "s3cret", Locked) // same address, other port: still the same source
	checkFrom(t, p, "10.0.0.7:4000", "s3cret", Allowed) // a different PC is not affected
}

func TestFailuresDecayWhenTheSourceGoesQuiet(t *testing.T) {
	p, c := newPolicy(t)
	p.SetPassword("s3cret")
	for i := 0; i < maxFailures-1; i++ {
		checkFrom(t, p, "10.0.0.66:1", "nope", BadPassword)
	}
	c.t = c.t.Add(failWindow + time.Second) // quiet for longer than the window
	for i := 0; i < maxFailures-1; i++ {
		checkFrom(t, p, "10.0.0.66:1", "nope", BadPassword) // still below the limit
	}
	checkFrom(t, p, "10.0.0.66:1", "s3cret", Allowed)
}

func TestRepeatedLockoutsGetLonger(t *testing.T) {
	p, c := newPolicy(t)
	p.SetPassword("s3cret")
	src := "10.0.0.66:1"
	trip := func() {
		for i := 0; i < maxFailures; i++ {
			checkFrom(t, p, src, "nope", BadPassword)
		}
	}
	trip()
	c.t = c.t.Add(lockout - time.Second)
	checkFrom(t, p, src, "s3cret", Locked)
	c.t = c.t.Add(2 * time.Second) // first lockout (1 min) is over
	trip()
	c.t = c.t.Add(lockout + time.Second) // 1 min later: the second lockout is 2 min
	checkFrom(t, p, src, "s3cret", Locked)
	c.t = c.t.Add(lockout) // 2 min in total
	checkFrom(t, p, src, "s3cret", Allowed)
}

func TestLockoutNeverExceedsTheCap(t *testing.T) {
	p, c := newPolicy(t)
	p.SetPassword("s3cret")
	src := "10.0.0.66:1"
	for round := 0; round < 12; round++ {
		for i := 0; i < maxFailures; i++ {
			checkFrom(t, p, src, "nope", BadPassword)
		}
		c.t = c.t.Add(maxLockout + time.Second)
	}
	checkFrom(t, p, src, "s3cret", Allowed)
}

func TestSourceTableStaysBounded(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetPassword("s3cret")
	for i := 0; i < 5*maxSources; i++ {
		addr := net.JoinHostPort(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255), "1")
		checkFrom(t, p, addr, "nope", BadPassword)
	}
	p.mu.Lock()
	n := len(p.sources)
	p.mu.Unlock()
	if n > maxSources {
		t.Fatalf("tracking %d sources, cap is %d", n, maxSources)
	}
}

func TestCorrectPasswordClearsThatSourcesFailures(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetPassword("s3cret")
	for i := 0; i < maxFailures-1; i++ {
		checkFrom(t, p, "10.0.0.66:1", "nope", BadPassword)
	}
	checkFrom(t, p, "10.0.0.66:1", "s3cret", Allowed)
	for i := 0; i < maxFailures-1; i++ { // the count started over
		checkFrom(t, p, "10.0.0.66:1", "nope", BadPassword)
	}
	checkFrom(t, p, "10.0.0.66:1", "s3cret", Allowed)
}

func TestPairingCodePairsOnlyTheDeviceThatEntersIt(t *testing.T) {
	p, _ := newPolicy(t)
	p.OpenPairing("123456", time.Minute)
	check(t, p, "aa", "", Untrusted)         // no code: not paired, and does not use up the window
	check(t, p, "aa", "654321", BadPassword) // wrong code
	if p.TrustedCount() != 0 {
		t.Fatal("a device was paired without the right code")
	}
	check(t, p, "aa", "123456", Allowed)
	if p.TrustedCount() != 1 {
		t.Fatalf("TrustedCount = %d after pairing, want 1", p.TrustedCount())
	}
	check(t, p, "aa", "", Allowed) // paired devices need nothing from now on
	// The code works once: the window closed when it was used.
	check(t, p, "bb", "123456", Untrusted)
	if p.PairingLeft() != 0 {
		t.Errorf("PairingLeft = %v after a successful pairing", p.PairingLeft())
	}
}

// The host shows the code as "123 456". Typed the way it is shown, with the gap, or with
// an input method's full-width digits, it is still the code.
func TestPairingCodeIsReadTheWayItIsShown(t *testing.T) {
	for _, typed := range []string{"123 456", " 123456 ", "123-456", "１２３４５６", "１２３ ４５６"} {
		for _, strict := range []bool{true, false} {
			p, _ := newPolicy(t)
			p.SetRequireAuth(strict)
			p.OpenPairing("123456", time.Minute)
			check(t, p, "aa", typed, Allowed)
			if p.TrustedCount() != 1 {
				t.Errorf("%q (strict %v): not paired", typed, strict)
			}
		}
	}
	for _, typed := range []string{"12345", "1234567", "123 4567", "123x456", "654 321"} {
		p, _ := newPolicy(t)
		p.OpenPairing("123456", time.Minute)
		check(t, p, "aa", typed, BadPassword)
	}
	// The host password is compared exactly: a space in it is part of it.
	p, _ := newPolicy(t)
	p.SetPassword("ab cd")
	check(t, p, "aa", "abcd", BadPassword)
	check(t, p, "aa", "ab cd", Allowed)
}

// The log says what was offered and what the host would have taken, never either secret.
func TestExplainTellsAnExpiredCodeFromAWrongOne(t *testing.T) {
	p, c := newPolicy(t)
	if got := p.Explain(""); got != "nothing offered; pairing not open; no host password" {
		t.Errorf("idle host: %q", got)
	}
	p.SetPassword("s3cret-pw")
	p.OpenPairing("123456", 2*time.Minute)
	c.t = c.t.Add(30 * time.Second)
	if got := p.Explain("654 321"); got != "offered a 6-digit code; pairing open, 1m30s left; host password set" {
		t.Errorf("wrong code while open: %q", got)
	}
	c.t = c.t.Add(2 * time.Minute)
	if got := p.Explain("123456"); got != "offered a 6-digit code; pairing ran out 30s ago; host password set" {
		t.Errorf("code after the window: %q", got)
	}
	got := p.Explain("hunter22")
	if got != "offered a password, length 8; pairing ran out 30s ago; host password set" {
		t.Errorf("a password: %q", got)
	}
	for _, secret := range []string{"hunter22", "s3cret-pw", "123456"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q gives away %q", got, secret)
		}
	}
	p.ClosePairing()
	if got := p.Explain("1"); got != "offered a password, length 1; pairing not open; host password set" {
		t.Errorf("after closing: %q", got)
	}
}

func TestPairingWithoutCodeStillAcceptsTheNextDevice(t *testing.T) {
	p, _ := newPolicy(t)
	p.StartPairing(time.Minute)
	check(t, p, "aa", "", Allowed)
	check(t, p, "bb", "", Untrusted)
}

func TestPairingClosesAfterTooManyWrongCodes(t *testing.T) {
	p, _ := newPolicy(t)
	p.OpenPairing("123456", time.Minute)
	// Different sources, so the per-source lockout is not what stops this.
	for i := 0; i < maxPairFails; i++ {
		checkFrom(t, p, net.JoinHostPort(fmt.Sprintf("10.0.1.%d", i+1), "1"), "000000", BadPassword)
	}
	if p.PairingLeft() != 0 {
		t.Fatal("pairing is still open after the wrong-code limit")
	}
	checkFrom(t, p, "10.0.2.1:1", "123456", Untrusted) // the right code is too late
	if p.TrustedCount() != 0 {
		t.Fatal("a device was paired after pairing closed")
	}
}

func TestWrongPairingCodesFromOneSourceAreLockedOut(t *testing.T) {
	p, _ := newPolicy(t)
	p.OpenPairing("123456", time.Minute)
	for i := 0; i < maxFailures-1; i++ {
		checkFrom(t, p, "10.0.0.66:1", "000000", BadPassword)
	}
	// Still open (fewer wrong codes than the window allows), but this source is on its last try.
	checkFrom(t, p, "10.0.0.66:1", "000000", BadPassword)
	checkFrom(t, p, "10.0.0.66:1", "123456", Locked)
}

func TestClosePairingEndsTheWindow(t *testing.T) {
	p, _ := newPolicy(t)
	p.OpenPairing("123456", time.Minute)
	p.ClosePairing()
	checkFrom(t, p, "10.0.0.1:1", "123456", Untrusted)
	if p.PairingCode() != "" || p.PairingLeft() != 0 {
		t.Errorf("code %q / left %v after ClosePairing", p.PairingCode(), p.PairingLeft())
	}
}

func TestHostPasswordStillWorksWhilePairingIsOpen(t *testing.T) {
	p, _ := newPolicy(t)
	p.SetPassword("s3cret")
	p.OpenPairing("123456", time.Minute)
	checkFrom(t, p, "10.0.0.1:1", "s3cret", Allowed)
	if p.TrustedCount() != 0 {
		t.Fatal("logging in with the host password must not pair the device")
	}
}

func TestNewPairingCodeIsSixDigits(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c := NewPairingCode()
		if len(c) != 6 {
			t.Fatalf("code %q is not 6 characters", c)
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				t.Fatalf("code %q has a non-digit", c)
			}
		}
		seen[c] = true
	}
	if len(seen) < 40 {
		t.Errorf("only %d distinct codes in 50 draws", len(seen))
	}
}
