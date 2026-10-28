package app

import (
	"context"

	"github.com/orangepro2001/go-nx/internal/remote"
)

func (c realConnector) Pinned(addr string) (string, bool) {
	return remote.PinnedFingerprint(c.hosts, addr)
}

func (c realConnector) Probe(ctx context.Context, addr string) (string, error) {
	return remote.Probe(ctx, addr, c.id)
}

func (c realConnector) Pin(addr, fp string) error { return remote.PinHost(c.hosts, addr, fp) }
func (c realConnector) Forget(addr string) error  { return remote.ForgetHost(c.hosts, addr) }

func (c realConnector) Dial(ctx context.Context, addr, password string) (Conn, error) {
	cl, err := remote.Dial(ctx, addr, c.id, c.hosts, password)
	if err != nil {
		return nil, err
	}
	return cl, nil
}
