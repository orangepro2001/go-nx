package remote

import (
	"fmt"
	"io"

	"github.com/orangepro2001/go-nx/internal/proto"
)

// ALPN protocol identifiers. The TLS handshake picks the control-protocol version:
// a host offers both and prefers v1, a viewer offers both, and either side that
// only knows v0 gets v0 with no further negotiation.
const (
	alpnV0 = "go-nx"
	alpnV1 = "go-nx/1"
)

// protos returns the ALPN list to offer for a highest version of maxVersion.
func protos(maxVersion int) []string {
	if maxVersion >= 1 {
		return []string{alpnV1, alpnV0}
	}
	return []string{alpnV0}
}

// codec is the dialect of the control stream agreed for one connection. Screen
// updates are the same in every version; only the control messages differ.
type codec interface {
	version() int
	// maxText is the longest text one message carries, in bytes.
	maxText() int

	// Written by the viewer.
	writeHello(w io.Writer, h proto.HelloV1) error
	writeAck(w io.Writer, seq uint32) error
	writeInput(w io.Writer, in proto.Input) error
	writeText(w io.Writer, opID uint64, text string) error
	writeViewSize(w io.Writer, width, height uint16) error

	// Read by the host.
	readClientMsg(r io.Reader) (proto.Msg, error)

	// Written by the host.
	writeReceipt(w io.Writer, opID uint64, status byte) error
	writeWelcome(w io.Writer, h proto.WelcomeV1) error
	writeDisplayState(w io.Writer, st proto.DisplayState) error

	// Read by the viewer.
	readHostMsg(r io.Reader) (proto.HostMsg, error)
}

func codecFor(negotiated string) (codec, error) {
	switch negotiated {
	case alpnV1:
		return codecV1{}, nil
	case alpnV0:
		return codecV0{}, nil
	}
	return nil, fmt.Errorf("remote: peer negotiated the unknown protocol %q", negotiated)
}

// codecV0 is the original fixed-size message format. It has no welcome: a v0 host
// says nothing about itself, so the viewer knows no capabilities for it.
type codecV0 struct{}

func (codecV0) version() int { return 0 }
func (codecV0) maxText() int { return proto.MaxTextLen }

func (codecV0) writeHello(w io.Writer, h proto.HelloV1) error { return proto.WriteHello(w, h.Password) }
func (codecV0) writeAck(w io.Writer, seq uint32) error        { return proto.WriteAck(w, seq) }
func (codecV0) writeInput(w io.Writer, in proto.Input) error  { return proto.WriteInput(w, in) }
func (codecV0) writeText(w io.Writer, opID uint64, text string) error {
	return proto.WriteText(w, opID, text)
}
func (codecV0) readClientMsg(r io.Reader) (proto.Msg, error) { return proto.ReadMsg(r) }
func (codecV0) writeReceipt(w io.Writer, opID uint64, status byte) error {
	return proto.WriteReceipt(w, opID, status)
}
func (codecV0) writeWelcome(io.Writer, proto.WelcomeV1) error  { return nil }
func (codecV0) writeViewSize(io.Writer, uint16, uint16) error  { return ErrUnsupported }
func (codecV0) writeDisplayState(io.Writer, proto.DisplayState) error {
	return nil // a v0 viewer never asks for a virtual display
}
func (codecV0) readHostMsg(r io.Reader) (proto.HostMsg, error) { return proto.ReadHostMsgV0(r) }

// codecV1 frames every control message and lets either side be newer than the
// other (docs/protocol.md).
type codecV1 struct{}

func (codecV1) version() int { return proto.Version1 }
func (codecV1) maxText() int { return proto.MaxTextLenV1 }

func (codecV1) writeHello(w io.Writer, h proto.HelloV1) error { return proto.WriteHelloV1(w, h) }
func (codecV1) writeAck(w io.Writer, seq uint32) error        { return proto.WriteAckV1(w, seq) }
func (codecV1) writeInput(w io.Writer, in proto.Input) error  { return proto.WriteInputV1(w, in) }
func (codecV1) writeText(w io.Writer, opID uint64, text string) error {
	return proto.WriteTextV1(w, opID, text)
}
func (codecV1) readClientMsg(r io.Reader) (proto.Msg, error) { return proto.ReadMsgV1(r) }
func (codecV1) writeReceipt(w io.Writer, opID uint64, status byte) error {
	return proto.WriteReceiptV1(w, opID, status)
}
func (codecV1) writeWelcome(w io.Writer, h proto.WelcomeV1) error { return proto.WriteWelcomeV1(w, h) }
func (codecV1) writeViewSize(w io.Writer, width, height uint16) error {
	return proto.WriteViewSizeV1(w, width, height)
}
func (codecV1) writeDisplayState(w io.Writer, st proto.DisplayState) error {
	return proto.WriteDisplayStateV1(w, st)
}
func (codecV1) readHostMsg(r io.Reader) (proto.HostMsg, error)    { return proto.ReadHostMsgV1(r) }
