package trust

import (
	"path/filepath"
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

func TestOpenByDefaultUntilAuthRequired(t *testing.T) {
	s, _ := OpenStore(filepath.Join(t.TempDir(), "trusted.json"))
	p := NewPolicy(s)
	check(t, p, "anyone", "", Allowed)
	p.SetRequireAuth(true)
	check(t, p, "anyone", "", Untrusted)
	p.SetRequireAuth(false)
	check(t, p, "anyone", "", Allowed)
}
