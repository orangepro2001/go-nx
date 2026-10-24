// Package remote runs the QUIC session between host and client.
//
// Both sides present their persistent self-signed certificate (mutual TLS). No
// certificate authority is involved: the host decides by fingerprint whether a
// viewer may connect, and the viewer pins the fingerprint of each host it has
// connected to before.
package remote

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"go-nx/internal/trust"
)

const alpn = "go-nx"

// Application close codes the host sends, so the viewer can say why it was dropped.
const (
	CloseUntrusted   = 1 // not paired, and no valid password offered
	CloseBadPassword = 2 // the password offered was wrong
	CloseReplaced    = 3 // another viewer took over
	CloseLocked      = 4 // too many wrong passwords; try again later
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
func serverTLS(id tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id},
		ClientAuth:   tls.RequireAnyClientCert,
		NextProtos:   []string{alpn},
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
		NextProtos: []string{alpn},
		MinVersion: tls.VersionTLS13,
	}, pin
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
	return quic.ListenAddr(addr, serverTLS(id), quicConfig())
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
