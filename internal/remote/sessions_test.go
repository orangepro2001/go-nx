package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"go-nx/internal/proto"
	"go-nx/internal/tile"
	"go-nx/internal/trust"
)

// Switching a host from open to strict mode must end the sessions that were let
// in only because it was open, and leave verified ones alone.
func TestDropUnverifiedEndsOnlyOpenAdmissions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store, err := trust.OpenStore(filepath.Join(t.TempDir(), "trusted.json"))
	if err != nil {
		t.Fatal(err)
	}
	policy := trust.NewPolicy(store)
	policy.SetRequireAuth(false)
	sessions := NewSessions(policy.RequireAuth)

	pairedID, strangerID := identity(t), identity(t)
	if err := store.Set(fp(pairedID), "paired earlier"); err != nil {
		t.Fatal(err)
	}

	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	opt := Options{Check: policy.Check, Sessions: sessions}
	go func() {
		for {
			conn, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			go Serve(ctx, conn, newFakeSource(64, 64), &fakeInjector{}, proto.NewDedup(), opt)
		}
	}()

	run := func(id tls.Certificate) chan error {
		c, err := Dial(ctx, ln.Addr().String(), id, hostsStore(t), "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx, func(*tile.Frame, []tile.Rect) {}, nil) }()
		return done
	}
	paired := run(pairedID)
	stranger := run(strangerID)

	waitFor(t, "one verified and one unverified session", func() bool {
		v, u := sessions.Count()
		return v == 1 && u == 1
	})

	policy.SetRequireAuth(true)
	if n := sessions.DropUnverified(CloseUnverified, "host now requires verification"); n != 1 {
		t.Fatalf("DropUnverified ended %d sessions, want 1", n)
	}

	select {
	case err := <-stranger:
		var ae *quic.ApplicationError
		if !errors.As(err, &ae) || ae.ErrorCode != CloseUnverified {
			t.Fatalf("the unverified session ended with %v, want application code CloseUnverified", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the unverified session was not ended")
	}
	select {
	case err := <-paired:
		t.Fatalf("the verified session was ended too: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	waitFor(t, "only the verified session left", func() bool {
		v, u := sessions.Count()
		return v == 1 && u == 0
	})
}

// The window between the policy turning strict and DropUnverified running must
// not let an open admission slip in afterwards.
func TestSessionsRefuseOpenAdmissionOnceStrict(t *testing.T) {
	strict := false
	s := NewSessions(func() bool { return strict })
	if _, ok := s.add(nil, false); !ok {
		t.Fatal("a verified session was refused")
	}
	strict = true
	if _, ok := s.add(nil, true); ok {
		t.Fatal("an unverified session was admitted while strict")
	}
	if _, ok := s.add(nil, false); !ok {
		t.Fatal("a verified session was refused while strict")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
