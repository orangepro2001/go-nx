package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/tile"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// versionRig is one host and one viewer joined over loopback, each limited to a
// control-protocol version, so every combination of old and new can be driven.
type versionRig struct {
	client *Client
	inj    *fakeInjector
	src    *fakeSource
	peer   chan PeerInfo
	served chan error
	recv   chan receiptEvent
}

type receiptEvent struct {
	op     uint64
	status byte
}

func connectVersions(t *testing.T, hostMax int, viewerV0 bool, name string) *versionRig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	ln, err := listen("127.0.0.1:0", identity(t), hostMax)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	r := &versionRig{
		inj: &fakeInjector{}, src: newFakeSource(64, 64),
		peer: make(chan PeerInfo, 1), served: make(chan error, 1), recv: make(chan receiptEvent, 16),
	}
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			r.served <- err
			return
		}
		r.served <- Serve(ctx, conn, r.src, r.inj, proto.NewDedup(), Options{
			Check: allowAll, HostName: "the-host", FPS: 50,
			OnPeer: func(p PeerInfo) { r.peer <- p },
		})
	}()
	r.client, err = DialWith(ctx, ln.Addr().String(), identity(t), hostsStore(t), DialOptions{Name: name, ForceV0: viewerV0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.client.Close)
	go r.client.Run(ctx, func(*tile.Frame, []tile.Rect) {}, func(id uint64, st byte) { r.recv <- receiptEvent{id, st} })
	return r
}

func (r *versionRig) wantReceipt(t *testing.T, op uint64) byte {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-r.recv:
			if e.op == op {
				return e.status
			}
		case <-deadline:
			t.Fatalf("no receipt for op %d", op)
		}
	}
}

// Every pairing of a viewer and a host, old or new, connects, shows the screen,
// executes input and agrees on what each side may do.
func TestEveryVersionPairingWorks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hostMax   int
		viewerV0  bool
		wantVer   int
		wantCaps  proto.Caps
		wantName  string // what the host learned about the viewer
		wantHost  string // what the viewer learned about the host
		textFlows bool   // SendText works without AssumeCaps
	}{
		{"v1 viewer, v1 host", 1, false, 1, proto.CapText | proto.CapWheelH, "viewer-pc", "the-host", true},
		{"v1 viewer, v0 host", 0, false, 0, 0, "", "", false},
		{"v0 viewer, v1 host", 1, true, 0, 0, "", "", false},
		{"v0 viewer, v0 host", 0, true, 0, 0, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := connectVersions(t, tc.hostMax, tc.viewerV0, "viewer-pc")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			info, err := r.client.WaitHost(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if info.Version != tc.wantVer || r.client.Version() != tc.wantVer {
				t.Fatalf("negotiated version %d/%d, want %d", info.Version, r.client.Version(), tc.wantVer)
			}
			if info.Caps != tc.wantCaps || info.Name != tc.wantHost {
				t.Errorf("host info = %+v, want caps %b and name %q", info, tc.wantCaps, tc.wantHost)
			}
			if tc.wantVer == 1 && (info.W != 64 || info.H != 64) {
				t.Errorf("welcome reports a %dx%d screen, want 64x64", info.W, info.H)
			}
			select {
			case p := <-r.peer:
				if p.Version != tc.wantVer || p.Name != tc.wantName {
					t.Errorf("host saw peer %+v, want version %d and name %q", p, tc.wantVer, tc.wantName)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the host never reported the viewer")
			}

			// Input, with a receipt, and the horizontal wheel.
			for i, in := range []proto.Input{
				{OpID: 1, Kind: proto.KindKeyDown, Arg: 65},
				{OpID: 2, Kind: proto.KindWheelH, X: 1, Y: 1, Arg: 120},
			} {
				if err := r.client.SendInput(in); err != nil {
					t.Fatal(err)
				}
				if st := r.wantReceipt(t, in.OpID); st != proto.StatusExecuted {
					t.Errorf("input %d: receipt %d, want Executed", i, st)
				}
			}

			// Text: only offered where the host said it can, but a caller that knows
			// better (a NightDesk host speaking v0) may assume it.
			err = r.client.SendText(3, "한 中 é")
			if tc.textFlows {
				if err != nil {
					t.Fatalf("SendText: %v", err)
				}
			} else {
				if !errors.Is(err, ErrUnsupported) {
					t.Fatalf("SendText = %v, want ErrUnsupported: this host never said it can type", err)
				}
				r.client.AssumeCaps(proto.CapText)
				if err := r.client.SendText(3, "한 中 é"); err != nil {
					t.Fatalf("SendText after AssumeCaps: %v", err)
				}
			}
			if st := r.wantReceipt(t, 3); st != proto.StatusExecuted {
				t.Errorf("text: receipt %d, want Executed", st)
			}
			if got := r.inj.typed(); len(got) != 1 || got[0] != "한 中 é" {
				t.Errorf("typed %q, want the text once", got)
			}
			select {
			case err := <-r.served:
				t.Fatalf("the host ended the session: %v", err)
			default:
			}
		})
	}
}

// A v0 client written from the frozen v0 byte format, not from this package's
// Client, against a host that speaks both. This is what a deployed 0.1-0.3 viewer
// or a NightDesk viewer does.
func TestFrozenV0ViewerAgainstTheCurrentHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	inj := &fakeInjector{}
	checked := make(chan string, 1)
	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			served <- err
			return
		}
		served <- Serve(ctx, conn, newFakeSource(64, 64), inj, proto.NewDedup(), Options{
			FPS: 50, Check: func(_, _, pw string) (trust.Verdict, error) { checked <- pw; return trust.Allowed, nil },
		})
	}()

	conn := rawDial(t, ctx, ln.Addr().String(), []string{"go-nx"})
	if got := conn.ConnectionState().TLS.NegotiatedProtocol; got != "go-nx" {
		t.Fatalf("negotiated %q; an old viewer must get v0", got)
	}
	ctrl, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Hello with password "pw", exactly as v0 writes it: type, length, bytes.
	ctrl.Write([]byte{0x01, 0x02, 'p', 'w'})
	select {
	case pw := <-checked:
		if pw != "pw" {
			t.Fatalf("host read password %q, want pw", pw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host never read the v0 hello")
	}
	// A keydown with op 9, then a text op 10, as raw v0 bytes.
	input := []byte{0x03, 9, 0, 0, 0, 0, 0, 0, 0, 0x05, 65, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	text := []byte{0x04, 10, 0, 0, 0, 0, 0, 0, 0, 0x02, 0x00, 'h', 'i'}
	ctrl.Write(append(input, text...))
	// Two v0 receipts come back: 0x81, opID u64, status u8.
	got := make([]byte, 20)
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := readFull(ctrl, got); err != nil {
		t.Fatalf("reading receipts: %v", err)
	}
	want := append([]byte{0x81, 9, 0, 0, 0, 0, 0, 0, 0, 1}, 0x81, 10, 0, 0, 0, 0, 0, 0, 0, 1)
	if !bytes.Equal(got, want) {
		t.Fatalf("v0 receipts\n got %x\nwant %x", got, want)
	}
	if inj.count(proto.KindKeyDown) != 1 || len(inj.typed()) != 1 || inj.typed()[0] != "hi" {
		t.Errorf("injected %d keydowns and %q", inj.count(proto.KindKeyDown), inj.typed())
	}
}

// A v1 viewer written from the byte format: a message type from the future in the
// middle of the stream is skipped, a malformed oversized frame ends the session.
func TestV1HostSkipsUnknownFramesAndRefusesOversizedOnes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ln, err := Listen("127.0.0.1:0", identity(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	inj := &fakeInjector{}
	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept(ctx)
		if err != nil {
			served <- err
			return
		}
		served <- Serve(ctx, conn, newFakeSource(64, 64), inj, proto.NewDedup(), Options{FPS: 50, Check: allowAll})
	}()

	conn := rawDial(t, ctx, ln.Addr().String(), []string{"go-nx/1", "go-nx"})
	if got := conn.ConnectionState().TLS.NegotiatedProtocol; got != "go-nx/1" {
		t.Fatalf("negotiated %q, want go-nx/1", got)
	}
	ctrl, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	proto.WriteHelloV1(&b, proto.HelloV1{Version: 1})
	proto.WriteFrame(&b, 0x7E, []byte("from the future")) // unknown: skipped
	proto.WriteInputV1(&b, proto.Input{OpID: 5, Kind: proto.KindKeyDown, Arg: 66})
	ctrl.Write(b.Bytes())

	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	var sawReceipt, sawWelcome bool
	for !sawReceipt {
		m, err := proto.ReadHostMsgV1(ctrl)
		if err != nil {
			t.Fatalf("reading from the host: %v", err)
		}
		switch m.Type {
		case proto.V1Welcome:
			sawWelcome = true
			if m.Welcome.Version != 1 || !m.Welcome.Caps.Has(proto.CapText) {
				t.Errorf("welcome = %+v", m.Welcome)
			}
		case proto.V1Receipt:
			sawReceipt = true
			if m.OpID != 5 || m.Status != proto.StatusExecuted {
				t.Errorf("receipt = %+v", m)
			}
		}
	}
	if !sawWelcome {
		t.Error("the host sent no welcome before the first receipt")
	}
	if inj.count(proto.KindKeyDown) != 1 {
		t.Errorf("the key after the unknown frame was injected %d times, want 1", inj.count(proto.KindKeyDown))
	}

	// Now a frame that announces more than the protocol allows.
	ctrl.Write([]byte{proto.V1ClientInput, 0xFF, 0xFF, 0xFF, 0x7F})
	select {
	case err := <-served:
		if err == nil {
			t.Error("Serve returned nil for an oversized frame")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host kept the session after an oversized control frame")
	}
}

// rawDial opens a QUIC connection offering exactly the given ALPN protocols.
func rawDial(t *testing.T, ctx context.Context, addr string, nextProtos []string) *quic.Conn {
	t.Helper()
	cfg := &tls.Config{
		Certificates:       []tls.Certificate{identity(t)},
		InsecureSkipVerify: true,
		NextProtos:         nextProtos,
		MinVersion:         tls.VersionTLS13,
	}
	conn, err := quic.DialAddr(ctx, addr, cfg, quicConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseWithError(0, "") })
	return conn
}

func readFull(s *quic.Stream, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := s.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
