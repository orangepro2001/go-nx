package trust

import (
	"fmt"
	"net"
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
