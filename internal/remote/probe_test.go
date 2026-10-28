package remote

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// probeHost runs a host that counts how many viewers reached the point of being
// checked, which is when a Hello (and so a password) has been read.
func probeHost(t *testing.T) (addr string, hostFP string, checked *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	hostID := identity(t)
	ln, err := Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	checked = new(atomic.Int32)
	opt := Options{Check: func(_, _, _ string) (trust.Verdict, error) {
		checked.Add(1)
		return trust.Allowed, nil
	}}
	go func() {
		for {
			conn, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			go Serve(ctx, conn, newFakeSource(64, 64), &fakeInjector{}, proto.NewDedup(), opt)
		}
	}()
	return ln.Addr().String(), fp(hostID), checked
}

func TestProbeReturnsTheHostFingerprintAndSendsNoHello(t *testing.T) {
	addr, hostFP, checked := probeHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := Probe(ctx, addr, identity(t))
	if err != nil {
		t.Fatal(err)
	}
	if got != hostFP {
		t.Fatalf("Probe = %q, want the host's fingerprint %q", got, hostFP)
	}
	time.Sleep(300 * time.Millisecond)
	if n := checked.Load(); n != 0 {
		t.Fatalf("host checked %d viewer(s): a probe must not get as far as a Hello", n)
	}
}

func TestProbeDoesNotPinAnything(t *testing.T) {
	addr, _, _ := probeHost(t)
	hosts := hostsStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Probe(ctx, addr, identity(t)); err != nil {
		t.Fatal(err)
	}
	if hosts.Len() != 0 {
		t.Fatal("Probe wrote to the hosts store")
	}
	if _, pinned := PinnedFingerprint(hosts, addr); pinned {
		t.Fatal("host reported as pinned before the user confirmed it")
	}
}

func TestPinHostThenDialSendsThePasswordOnlyToThatIdentity(t *testing.T) {
	addr, hostFP, checked := probeHost(t)
	viewerID := identity(t)
	hosts := hostsStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fpSeen, err := Probe(ctx, addr, viewerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := PinHost(hosts, addr, fpSeen); err != nil {
		t.Fatal(err)
	}
	if got, ok := PinnedFingerprint(hosts, addr); !ok || got != hostFP {
		t.Fatalf("PinnedFingerprint = %q, %v", got, ok)
	}
	c, err := Dial(ctx, addr, viewerID, hosts, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for checked.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if checked.Load() != 1 {
		t.Fatalf("host checked %d viewers after a pinned Dial, want 1", checked.Load())
	}

	// Pinned to the identity the user confirmed: another host answering at the
	// same address (the pin is by IP, and both listen on 127.0.0.1) must be
	// refused before any password is sent.
	otherAddr, _, otherChecked := probeHost(t)
	if _, err := Dial(ctx, otherAddr, viewerID, hosts, "pw"); err == nil {
		t.Fatal("Dial to a different identity succeeded")
	}
	time.Sleep(300 * time.Millisecond)
	if n := otherChecked.Load(); n != 0 {
		t.Fatalf("the impostor host received %d Hello(s), so the password leaked", n)
	}
}
