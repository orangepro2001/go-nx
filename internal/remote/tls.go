// Package remote runs the QUIC session between host and client.
//
// Both sides present their persistent self-signed certificate (mutual TLS). No
// certificate authority is involved: the host decides by fingerprint whether a
// viewer may connect, and the viewer pins the fingerprint of each host it has
// connected to before.
package remote

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/proto"
	"github.com/orangepro2001/go-nx/internal/trust"
)

// Application close codes the host sends, so the viewer can say why it was dropped.
const (
	CloseUntrusted   = 1 // not paired, and no valid password offered
	CloseBadPassword = 2 // the password offered was wrong
	CloseReplaced    = 3 // another viewer took over
	CloseLocked      = 4 // too many wrong passwords; try again later
	CloseRevoked     = 5 // this device was removed from the host's paired devices
	CloseBusy        = 6 // too many connections are waiting to be authenticated; try again shortly
	CloseUnverified  = 7 // the host went back to strict mode and this session had not been verified
)

// ErrHostChanged means a host's certificate differs from the one remembered for its address.
var ErrHostChanged = errors.New("host identity changed")

// quicConfig is tuned for links that cross a tunnel such as a Tailscale subnet
// route, where the first packets go through a relay and paths can switch.
func quicConfig() *quic.Config {
	return &quic.Config{
		// Survive a path switch or a stall of tens of seconds without dropping the
		// session. Keep-alives every 5 s still detect a dead peer within this time.
		MaxIdleTimeout:  45 * time.Second,
		KeepAlivePeriod: 5 * time.Second,
		// Tailscale's MTU is 1280, so a packet of 1200 bytes of payload is safe. The
		// library default (1280 payload, 1308 on the wire) relies on ICMP "packet too
		// big" messages to shrink, and a lost one black-holes the connection. Probing
		// for larger packets is off for the same reason; our updates are small.
		InitialPacketSize:       1200,
		DisablePathMTUDiscovery: true,
	}
}

// serverTLS asks every viewer for a certificate. Trust is decided by fingerprint
// in Serve, so any certificate is accepted at the TLS layer.
func serverTLS(id tls.Certificate, maxVersion int) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id},
		ClientAuth:   tls.RequireAnyClientCert,
		NextProtos:   protos(maxVersion),
		MinVersion:   tls.VersionTLS13,
	}
}

// clientTLS presents id and pins the host's fingerprint under key host: the
// first connection remembers it, later ones must match.
//
// Recording a new pin is deliberately not done inside VerifyPeerCertificate: that
// callback runs on the handshake's critical path, so a slow disk (antivirus,
// roaming profile) would delay or even abort the very first connection. The
// returned *pinRecorder is committed by Dial once the handshake has finished.
func clientTLS(id tls.Certificate, host string, hosts *trust.Store) (*tls.Config, *pinRecorder) {
	return clientTLSVersions(id, host, hosts, proto.Version1)
}

// clientTLSVersions is clientTLS offering control protocols up to maxVersion.
func clientTLSVersions(id tls.Certificate, host string, hosts *trust.Store, maxVersion int) (*tls.Config, *pinRecorder) {
	pin := &pinRecorder{host: host}
	return &tls.Config{
		Certificates:       []tls.Certificate{id},
		InsecureSkipVerify: true, // replaced by the fingerprint pin below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("host sent no certificate")
			}
			fp := trust.Fingerprint(raw[0])
			if pinned, ok := hosts.Get(host); ok {
				if pinned != fp {
					return ErrHostChanged
				}
				return nil
			}
			pin.remember(fp)
			return nil
		},
		NextProtos: protos(maxVersion),
		MinVersion: tls.VersionTLS13,
	}, pin
}

// pinnedClientTLS requires the host's certificate to be exactly the one with the
// fingerprint expected (already in normalised form). It never reads or records a
// pin: the fingerprint comes with the pairing code, so a host that merely sits at
// the same address is refused during the handshake, before any password is sent.
func pinnedClientTLS(id tls.Certificate, expected string, maxVersion int) *tls.Config {
	return &tls.Config{
		Certificates:       []tls.Certificate{id},
		InsecureSkipVerify: true, // replaced by the exact fingerprint comparison below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("host sent no certificate")
			}
			if trust.Fingerprint(raw[0]) != expected {
				return ErrHostChanged
			}
			return nil
		},
		NextProtos: protos(maxVersion),
		MinVersion: tls.VersionTLS13,
	}
}

// normalizeFingerprint reads a fingerprint as people and QR codes write it: any
// case, with or without colons or spaces between bytes. It reports false for
// anything that is not 32 bytes of hex.
func normalizeFingerprint(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ':' || unicode.IsSpace(r):
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r >= 'A' && r <= 'F':
			b.WriteRune(r + 'a' - 'A')
		default:
			return "", false
		}
	}
	if b.Len() != 2*sha256.Size {
		return "", false
	}
	return b.String(), true
}

// pinRecorder holds the fingerprint seen for a not-yet-pinned host so it can be
// saved after the handshake, keeping disk I/O off the connection's fast path.
type pinRecorder struct {
	mu   sync.Mutex
	host string
	fp   string
}

func (p *pinRecorder) remember(fp string) {
	p.mu.Lock()
	p.fp = fp
	p.mu.Unlock()
}

func (p *pinRecorder) commit(hosts *trust.Store) error {
	p.mu.Lock()
	fp := p.fp
	p.mu.Unlock()
	if fp == "" {
		return nil
	}
	return hosts.Set(p.host, fp)
}

// Listen starts a QUIC listener for hosts.
func Listen(addr string, id tls.Certificate) (*quic.Listener, error) {
	return listen(addr, id, proto.Version1)
}

// listen is Listen offering control protocols up to maxVersion; 0 behaves like a
// host from before version negotiation existed.
func listen(addr string, id tls.Certificate, maxVersion int) (*quic.Listener, error) {
	return quic.ListenAddr(addr, serverTLS(id, maxVersion), quicConfig())
}

// Probe connects to addr only far enough to learn the fingerprint of the
// certificate the host presents, then hangs up. Nothing is sent over the session
// and nothing is stored: no Hello, so no password, and no pin. An interactive
// caller uses it for a host that is not pinned yet, lets the user compare the
// fingerprint with the one the host displays, and only then calls PinHost and
// Dial, so a password only ever goes to an identity the user has confirmed.
func Probe(ctx context.Context, addr string, id tls.Certificate) (string, error) {
	var seen atomic.Value // string
	cfg := &tls.Config{
		Certificates:       []tls.Certificate{id},
		InsecureSkipVerify: true, // nothing is trusted or sent; the caller verifies the fingerprint
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("host sent no certificate")
			}
			seen.Store(trust.Fingerprint(raw[0]))
			return nil
		},
		NextProtos: protos(proto.Version1),
		MinVersion: tls.VersionTLS13,
	}
	conn, err := quic.DialAddr(ctx, addr, cfg, quicConfig())
	if err != nil {
		return "", err
	}
	conn.CloseWithError(0, "")
	fp, _ := seen.Load().(string)
	if fp == "" {
		return "", errors.New("host sent no certificate")
	}
	return fp, nil
}

// PinnedFingerprint is the fingerprint remembered for the host at addr.
func PinnedFingerprint(hosts *trust.Store, addr string) (string, bool) {
	return hosts.Get(hostOf(addr))
}

// PinHost remembers fp as the identity of the host at addr; later connections
// must present it. Call it after the user has confirmed the fingerprint.
func PinHost(hosts *trust.Store, addr, fp string) error { return hosts.Set(hostOf(addr), fp) }

// ForgetHost drops the fingerprint remembered for the host at addr, so the next
// connection to it pins whatever certificate it presents. Use it only after the
// user has been told why the identity changed and chose to trust the new one.
func ForgetHost(hosts *trust.Store, addr string) error { return hosts.Delete(hostOf(addr)) }

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// sourceHost is the part of a remote address that identifies a PC: its IP.
func sourceHost(addr string) string { return hostOf(addr) }

// ParseListen turns the -listen and -port options into the UDP address to bind,
// the port to show, and the IP the host is restricted to ("" for every
// interface). listen may be empty (all interfaces on port), an IP (that interface
// on port), an IP with a port, or just ":port". Names are refused: which address
// a name resolves to is not something to decide at start-up.
func ParseListen(listen string, port int) (bind string, outPort int, ip string, err error) {
	if listen == "" {
		if port < 1 || port > 65535 {
			return "", 0, "", fmt.Errorf("port %d is out of range", port)
		}
		return fmt.Sprintf(":%d", port), port, "", nil
	}
	host, portStr, splitErr := net.SplitHostPort(listen)
	if splitErr != nil {
		host, portStr = strings.Trim(listen, "[]"), strconv.Itoa(port) // no port given: an address alone
	}
	p, convErr := strconv.Atoi(portStr)
	if convErr != nil || p < 1 || p > 65535 {
		return "", 0, "", fmt.Errorf("listen address %q has a bad port", listen)
	}
	if host == "" {
		return fmt.Sprintf(":%d", p), p, "", nil
	}
	addr, parseErr := netip.ParseAddr(host)
	if parseErr != nil {
		return "", 0, "", fmt.Errorf("listen address %q: %q is not an IP address", listen, host)
	}
	return net.JoinHostPort(addr.String(), strconv.Itoa(p)), p, addr.String(), nil
}
