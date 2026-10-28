package remote

import (
	"context"
	"crypto/tls"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// pinHost starts a host that trusts viewer and counts how many viewers reached
// the point of being checked (a viewer is checked only after its Hello, and the
// Hello carries the password).
func pinHost(t *testing.T, hostID, viewer tls.Certificate) (addr string, checked *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ln, err := Listen("127.0.0.1:0", hostID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	checked = new(atomic.Int32)
	opt := Options{FPS: 20, Check: func(f, _, _ string) (trust.Verdict, error) {
		checked.Add(1)
		if f == fp(viewer) {
			return trust.Allowed, nil
		}
		return trust.Untrusted, nil
	}}
	dd := proto.NewDedup()
	go func() {
		for {
			conn, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			go Serve(ctx, conn, newFakeSource(64, 48), &fakeInjector{}, dd, opt)
		}
	}()
	return ln.Addr().String(), checked
}

func dialExpecting(t *testing.T, addr string, viewer tls.Certificate, hosts *trust.Store, expect string) (*Client, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, err := DialWith(ctx, addr, viewer, hosts, DialOptions{Password: "secret", ExpectFingerprint: expect})
	if err == nil {
		t.Cleanup(c.Close)
	}
	return c, err
}

// A pairing code carries the fingerprint of the host it was made on. A viewer that
// has it connects to exactly that certificate, and neither consults nor changes
// what it remembered about the address (the address may belong to anyone by now).
func TestAnExpectedFingerprintConnectsAndStoresNothing(t *testing.T) {
	host, viewer := identity(t), identity(t)
	addr, _ := pinHost(t, host, viewer)

	hosts := hostsStore(t)
	if err := hosts.Set("127.0.0.1", "00ff"+strings.Repeat("0", 60)); err != nil { // a stale pin for the address
		t.Fatal(err)
	}
	c, err := dialExpecting(t, addr, viewer, hosts, fp(host))
	if err != nil {
		t.Fatalf("Dial with the right fingerprint: %v", err)
	}
	c.Close()
	if got, _ := hosts.Get("127.0.0.1"); got != "00ff"+strings.Repeat("0", 60) {
		t.Errorf("the remembered pin was changed to %q", got)
	}

	// And no store at all is needed.
	c, err = dialExpecting(t, addr, viewer, nil, fp(host))
	if err != nil {
		t.Fatalf("Dial without a store: %v", err)
	}
	c.Close()
}

// A different machine at the address must be refused during the handshake, before
// the password is sent: the host never sees a Hello.
func TestAWrongExpectedFingerprintNeverSendsThePassword(t *testing.T) {
	host, impostorsExpectation, viewer := identity(t), identity(t), identity(t)
	addr, checked := pinHost(t, host, viewer)

	_, err := dialExpecting(t, addr, viewer, hostsStore(t), fp(impostorsExpectation))
	if err == nil || !strings.Contains(err.Error(), ErrHostChanged.Error()) {
		t.Fatalf("Dial = %v, want %q", err, ErrHostChanged)
	}
	time.Sleep(300 * time.Millisecond) // a Hello that was sent anyway would have arrived by now
	if n := checked.Load(); n != 0 {
		t.Fatalf("the host checked %d viewer(s): the password was sent to a host that was not the expected one", n)
	}
}

func TestAnExpectedFingerprintIsReadLeniently(t *testing.T) {
	host, viewer := identity(t), identity(t)
	addr, _ := pinHost(t, host, viewer)
	good := fp(host)
	var colons []string
	for i := 0; i < len(good); i += 2 {
		colons = append(colons, good[i:i+2])
	}
	for name, form := range map[string]string{
		"upper case":   strings.ToUpper(good),
		"with colons":  strings.Join(colons, ":"),
		"with spaces":  " " + strings.Join(colons, " ") + " ",
		"upper+colons": strings.ToUpper(strings.Join(colons, ":")),
	} {
		c, err := dialExpecting(t, addr, viewer, nil, form)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		c.Close()
	}
}

// Something that cannot be a fingerprint is a mistake in the caller, reported at
// once and not as a host that "changed".
func TestAMalformedExpectedFingerprintIsRefusedBeforeDialling(t *testing.T) {
	host, viewer := identity(t), identity(t)
	addr, _ := pinHost(t, host, viewer)
	for _, bad := range []string{"abc", strings.Repeat("g", 64), fp(host) + "00", fp(host)[:62]} {
		_, err := dialExpecting(t, addr, viewer, nil, bad)
		if err == nil {
			t.Errorf("%q was accepted as a fingerprint", bad)
			continue
		}
		if strings.Contains(err.Error(), ErrHostChanged.Error()) {
			t.Errorf("%q: %v: a malformed fingerprint is not a changed host", bad, err)
		}
	}
}

func TestDialNeedsAStoreOrAFingerprint(t *testing.T) {
	host, viewer := identity(t), identity(t)
	addr, _ := pinHost(t, host, viewer)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Dial(ctx, addr, viewer, nil, ""); err == nil {
		t.Fatal("Dial with nothing to check the host against succeeded")
	}
}
