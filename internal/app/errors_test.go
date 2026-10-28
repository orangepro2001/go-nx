package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

func TestDescribeEveryHostRefusalInEveryLanguage(t *testing.T) {
	codes := map[quic.ApplicationErrorCode]i18n.Key{
		remote.CloseUntrusted:   i18n.KeyErrUntrusted,
		remote.CloseBadPassword: i18n.KeyErrBadPassword,
		remote.CloseLocked:      i18n.KeyErrLocked,
		remote.CloseReplaced:    i18n.KeyErrReplaced,
		remote.CloseUnverified:  i18n.KeyErrUnverified,
		remote.CloseBusy:        i18n.KeyErrBusy,
		remote.CloseRevoked:     i18n.KeyErrRevoked,
		remote.CloseKicked:      i18n.KeyErrKicked,
	}
	for _, l := range []i18n.Lang{i18n.English, i18n.Chinese, i18n.Korean} {
		tr := i18n.New(l)
		seen := map[string]bool{}
		for code, key := range codes {
			got, changed := describe(tr, &quic.ApplicationError{ErrorCode: code})
			if got != tr.T(key) || changed {
				t.Errorf("%v code %d: %q (changed %v)", l, code, got, changed)
			}
			if strings.Contains(got, "{") {
				t.Errorf("%v code %d left a placeholder: %q", l, code, got)
			}
			if seen[got] {
				t.Errorf("%v: two refusals read the same: %q", l, got)
			}
			seen[got] = true
		}
	}
}

func TestDescribeOtherErrors(t *testing.T) {
	tr := i18n.New(i18n.English)
	if got, _ := describe(tr, fmt.Errorf("dial: %w", errHostNotConfirmed)); got != tr.T(i18n.KeyErrNotConfirmed) {
		t.Errorf("not confirmed: %q", got)
	}
	got, changed := describe(tr, fmt.Errorf("handshake: %w", remote.ErrHostChanged))
	if !changed || got != tr.T(i18n.KeyErrHostChanged) {
		t.Errorf("host changed: %q, %v", got, changed)
	}
	if got, _ := describe(tr, &quic.IdleTimeoutError{}); got != tr.T(i18n.KeyErrTimeout) {
		t.Errorf("timeout: %q", got)
	}
	if got, changed := describe(tr, errors.New("connection refused")); got != "connection refused" || changed {
		t.Errorf("an unknown error should be shown as it is: %q", got)
	}
}
