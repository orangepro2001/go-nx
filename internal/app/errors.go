package app

import (
	"errors"
	"strings"

	"github.com/quic-go/quic-go"

	"github.com/orangepro2001/go-nx/internal/remote"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

// errHostNotConfirmed means the user declined (or never answered) the prompt for a host
// seen for the first time. Nothing was sent to it.
var errHostNotConfirmed = errors.New("host identity not confirmed")

// describe turns a connection error into something a person can act on, in the interface
// language. hostChanged is true when the host presented a different identity than the one
// pinned, which the person may deliberately accept by forgetting the old one.
func describe(tr *i18n.Translator, err error) (text string, hostChanged bool) {
	var ae *quic.ApplicationError
	if errors.As(err, &ae) {
		switch ae.ErrorCode {
		case remote.CloseUntrusted:
			return tr.T(i18n.KeyErrUntrusted), false
		case remote.CloseBadPassword:
			return tr.T(i18n.KeyErrBadPassword), false
		case remote.CloseLocked:
			return tr.T(i18n.KeyErrLocked), false
		case remote.CloseReplaced:
			return tr.T(i18n.KeyErrReplaced), false
		case remote.CloseUnverified:
			return tr.T(i18n.KeyErrUnverified), false
		case remote.CloseBusy:
			return tr.T(i18n.KeyErrBusy), false
		case remote.CloseKicked:
			return tr.T(i18n.KeyErrKicked), false
		case remote.CloseRevoked:
			return tr.T(i18n.KeyErrRevoked), false
		}
	}
	if errors.Is(err, errHostNotConfirmed) {
		return tr.T(i18n.KeyErrNotConfirmed), false
	}
	if strings.Contains(err.Error(), remote.ErrHostChanged.Error()) {
		return tr.T(i18n.KeyErrHostChanged), true
	}
	var idle *quic.IdleTimeoutError
	if errors.As(err, &idle) {
		return tr.T(i18n.KeyErrTimeout), false
	}
	return tr.T(i18n.KeyErrRaw, "error", err.Error()), false
}

// refusedCode is the code the host closed the connection with, or 0.
func refusedCode(err error) quic.ApplicationErrorCode {
	var ae *quic.ApplicationError
	if errors.As(err, &ae) {
		return ae.ErrorCode
	}
	return 0
}

// refusedAuth reports whether the host turned this PC away for want of a valid password or
// pairing code, which only another one can fix.
func refusedAuth(err error) bool {
	switch refusedCode(err) {
	case remote.CloseUntrusted, remote.CloseBadPassword, remote.CloseLocked, remote.CloseRevoked, remote.CloseUnverified:
		return true
	}
	return false
}

// isHostChanged reports whether err is the host presenting an identity other than the
// pinned one, which retrying cannot fix.
func isHostChanged(err error) bool {
	return strings.Contains(err.Error(), remote.ErrHostChanged.Error())
}

// keyErrDisconnected is the "Disconnected: {reason}" sentence.
const keyErrDisconnected = i18n.KeyErrDisconnected
