package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Protocol v1 (ALPN "go-nx/1") frames the control stream so that either side can
// be newer than the other: every message is
//
//	type u8 | length u32 (little endian) | payload [length]
//
// A receiver skips a type it does not know and ignores bytes after the fields it
// knows. Frame streams (screen updates) are unchanged from v0. docs/protocol.md
// is the specification; this file is its implementation.

// Caps is the set of optional abilities a peer advertises in v1.
type Caps uint64

const (
	CapText   Caps = 1 << iota // the host types literal Unicode text (MsgText)
	CapWheelH                  // the host understands the horizontal wheel input kind
	CapResize                  // the frame size may change during a session
	CapCursor                  // the host draws the pointer into the frames
	// CapVirtualDisplay: the host can show the session on a virtual display made at the size
	// the viewer asks for (V1ClientViewSize), and reports how that went (V1DisplayState).
	CapVirtualDisplay
)

// Has reports whether every ability in want is present.
func (c Caps) Has(want Caps) bool { return c&want == want }

var capNames = [...]string{"text", "wheelh", "resize", "cursor", "vdisplay"}

// Names lists the abilities in c for logs and tooling, in bit order. A bit this
// build does not know is named "bitN", so that what a newer peer advertises is
// never silently dropped.
func (c Caps) Names() []string {
	var out []string
	for bit := 0; bit < 64; bit++ {
		if c&(Caps(1)<<bit) == 0 {
			continue
		}
		if bit < len(capNames) {
			out = append(out, capNames[bit])
		} else {
			out = append(out, fmt.Sprintf("bit%d", bit))
		}
	}
	return out
}

// Version1 is the control-protocol version this package speaks.
const Version1 = 1

// v1 control message types. The client->host values equal the v0 Msg* constants,
// so a decoded Msg means the same thing whichever protocol carried it.
const (
	V1ClientHello byte = MsgHello
	V1ClientAck   byte = MsgAck
	V1ClientInput byte = MsgInput
	V1ClientText  byte = MsgText
	// V1ClientViewSize asks a host that has CapVirtualDisplay to show the session on a
	// virtual display of this size (the viewer's window, in device pixels); 0 x 0 asks for
	// the host's own screen again. It has no v0 equivalent.
	V1ClientViewSize byte = 0x05

	V1Receipt      byte = 0x81 // host -> client; same value as v0's MsgReceipt
	V1Welcome      byte = 0x82 // host -> client, once, right after the viewer is admitted
	V1DisplayState byte = 0x83 // host -> client: whether the session is on a virtual display, and why not
)

// Limits. A length prefix is checked against MaxCtrlPayload before anything is
// allocated, so a hostile peer cannot make a receiver reserve memory.
const (
	MaxCtrlPayload = 16 << 10
	MaxTextLenV1   = 4096 // UTF-8 bytes in one text message
	MaxNameLen     = 64   // UTF-8 bytes in a device or host name
)

// ErrFrameTooBig is returned for a frame whose announced payload exceeds MaxCtrlPayload.
var ErrFrameTooBig = errors.New("proto: control frame too large")

var errShort = errors.New("proto: control frame payload too short")

// WriteFrame writes one v1 control frame in a single Write.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxCtrlPayload {
		return ErrFrameTooBig
	}
	b := make([]byte, 5+len(payload))
	b[0] = typ
	binary.LittleEndian.PutUint32(b[1:], uint32(len(payload)))
	copy(b[5:], payload)
	return write(w, b)
}

// ReadFrame reads one v1 control frame.
func ReadFrame(r io.Reader) (typ byte, payload []byte, err error) {
	var h [5]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.LittleEndian.Uint32(h[1:])
	if n > MaxCtrlPayload {
		return h[0], nil, ErrFrameTooBig
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return h[0], nil, err
	}
	return h[0], payload, nil
}

// HelloV1 is the first message from the viewer.
type HelloV1 struct {
	Version  uint16 // the highest control-protocol version the viewer speaks
	Caps     Caps
	Password string // may be empty; also carries a pairing code
	Name     string // the viewer's display name, for the host's lists; may be empty
}

// DisplayState is what a host with CapVirtualDisplay says after a V1ClientViewSize: whether
// the session is on a virtual display now, at what size (which may differ from the one asked
// for: the driver offers a list of sizes), and, if something went wrong, what.
type DisplayState struct {
	Virtual bool
	W, H    uint16
	Message string // empty when all is well; at most MaxStateMsgLen bytes
}

// MaxStateMsgLen is the longest DisplayState message.
const MaxStateMsgLen = 255

// WelcomeV1 is the host's answer once it has admitted the viewer.
type WelcomeV1 struct {
	Version uint16
	Caps    Caps
	W, H    uint16 // the screen being shared; frames carry their own size too
	Name    string // the host's display name; may be empty
}

func WriteHelloV1(w io.Writer, h HelloV1) error {
	if len(h.Password) > 255 {
		return errors.New("proto: password too long")
	}
	if len(h.Name) > MaxNameLen {
		return errors.New("proto: name too long")
	}
	b := make([]byte, 0, 12+len(h.Password)+len(h.Name))
	b = binary.LittleEndian.AppendUint16(b, h.Version)
	b = binary.LittleEndian.AppendUint64(b, uint64(h.Caps))
	b = append(b, byte(len(h.Password)))
	b = append(b, h.Password...)
	b = append(b, byte(len(h.Name)))
	b = append(b, h.Name...)
	return WriteFrame(w, V1ClientHello, b)
}

func WriteAckV1(w io.Writer, seq uint32) error {
	return WriteFrame(w, V1ClientAck, binary.LittleEndian.AppendUint32(nil, seq))
}

func WriteInputV1(w io.Writer, in Input) error {
	b := make([]byte, 0, 21)
	b = binary.LittleEndian.AppendUint64(b, in.OpID)
	b = append(b, in.Kind)
	b = binary.LittleEndian.AppendUint32(b, uint32(in.X))
	b = binary.LittleEndian.AppendUint32(b, uint32(in.Y))
	b = binary.LittleEndian.AppendUint32(b, uint32(in.Arg))
	return WriteFrame(w, V1ClientInput, b)
}

func WriteTextV1(w io.Writer, opID uint64, text string) error {
	if len(text) > MaxTextLenV1 {
		return errors.New("proto: text too long")
	}
	return WriteFrame(w, V1ClientText, append(binary.LittleEndian.AppendUint64(nil, opID), text...))
}

// WriteViewSizeV1 asks for a virtual display of w x h; 0 x 0 asks for none.
func WriteViewSizeV1(w io.Writer, width, height uint16) error {
	b := binary.LittleEndian.AppendUint16(nil, width)
	return WriteFrame(w, V1ClientViewSize, binary.LittleEndian.AppendUint16(b, height))
}

func WriteDisplayStateV1(w io.Writer, st DisplayState) error {
	msg := st.Message
	if len(msg) > MaxStateMsgLen {
		msg = strings.ToValidUTF8(msg[:MaxStateMsgLen], "")
	}
	b := []byte{0}
	if st.Virtual {
		b[0] = 1
	}
	b = binary.LittleEndian.AppendUint16(b, st.W)
	b = binary.LittleEndian.AppendUint16(b, st.H)
	b = append(b, byte(len(msg)))
	return WriteFrame(w, V1DisplayState, append(b, msg...))
}

func WriteReceiptV1(w io.Writer, opID uint64, status byte) error {
	return WriteFrame(w, V1Receipt, append(binary.LittleEndian.AppendUint64(nil, opID), status))
}

func WriteWelcomeV1(w io.Writer, h WelcomeV1) error {
	if len(h.Name) > MaxNameLen {
		return errors.New("proto: name too long")
	}
	b := make([]byte, 0, 15+len(h.Name))
	b = binary.LittleEndian.AppendUint16(b, h.Version)
	b = binary.LittleEndian.AppendUint64(b, uint64(h.Caps))
	b = binary.LittleEndian.AppendUint16(b, h.W)
	b = binary.LittleEndian.AppendUint16(b, h.H)
	b = append(b, byte(len(h.Name)))
	b = append(b, h.Name...)
	return WriteFrame(w, V1Welcome, b)
}

// fields reads the fixed-size fields of a payload from the front.
type fields struct {
	b   []byte
	err error
}

func (f *fields) take(n int) []byte {
	if f.err != nil || len(f.b) < n {
		f.err = errShort
		return nil
	}
	out := f.b[:n]
	f.b = f.b[n:]
	return out
}

func (f *fields) u8() byte {
	if b := f.take(1); b != nil {
		return b[0]
	}
	return 0
}
func (f *fields) u16() uint16 {
	if b := f.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}
func (f *fields) u32() uint32 {
	if b := f.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}
func (f *fields) u64() uint64 {
	if b := f.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}

// str reads a string with a one-byte length prefix; a name is limited to MaxNameLen.
func (f *fields) str(limit int) string {
	n := int(f.u8())
	if n > limit {
		f.err = errors.New("proto: string field too long")
		return ""
	}
	return strings.ToValidUTF8(string(f.take(n)), "�")
}

// ReadMsgV1 reads the next client->host message that this version knows. Frames
// of an unknown type are skipped, and bytes after the known fields are ignored,
// so a newer viewer can talk to this host.
func ReadMsgV1(r io.Reader) (Msg, error) {
	for {
		typ, payload, err := ReadFrame(r)
		if err != nil {
			return Msg{Type: typ}, err
		}
		f := &fields{b: payload}
		m := Msg{Type: typ}
		switch typ {
		case V1ClientHello:
			m.Version = f.u16()
			m.Caps = Caps(f.u64())
			m.Password = f.str(255)
			m.Name = f.str(MaxNameLen)
		case V1ClientAck:
			m.Seq = f.u32()
		case V1ClientInput:
			m.Input = Input{
				OpID: f.u64(), Kind: f.u8(),
				X: int32(f.u32()), Y: int32(f.u32()), Arg: int32(f.u32()),
			}
		case V1ClientViewSize:
			m.W, m.H = f.u16(), f.u16()
		case V1ClientText:
			m.OpID = f.u64()
			if f.err == nil {
				if len(f.b) > MaxTextLenV1 {
					return m, errors.New("proto: text too long")
				}
				m.Text = strings.ToValidUTF8(string(f.b), "�")
			}
		default:
			continue // a message type from a newer peer
		}
		if f.err != nil {
			return m, f.err
		}
		return m, nil
	}
}

// HostMsg is a decoded host->client control message.
type HostMsg struct {
	Type    byte // V1Receipt, V1Welcome or V1DisplayState
	OpID    uint64
	Status  byte
	Welcome WelcomeV1
	Display DisplayState
}

// ReadHostMsgV1 reads the next host->client message that this version knows,
// skipping unknown ones.
func ReadHostMsgV1(r io.Reader) (HostMsg, error) {
	for {
		typ, payload, err := ReadFrame(r)
		if err != nil {
			return HostMsg{Type: typ}, err
		}
		f := &fields{b: payload}
		m := HostMsg{Type: typ}
		switch typ {
		case V1Receipt:
			m.OpID = f.u64()
			m.Status = f.u8()
		case V1Welcome:
			m.Welcome.Version = f.u16()
			m.Welcome.Caps = Caps(f.u64())
			m.Welcome.W = f.u16()
			m.Welcome.H = f.u16()
			m.Welcome.Name = f.str(MaxNameLen)
		case V1DisplayState:
			m.Display.Virtual = f.u8() == 1
			m.Display.W, m.Display.H = f.u16(), f.u16()
			m.Display.Message = f.str(MaxStateMsgLen)
		default:
			continue
		}
		if f.err != nil {
			return m, f.err
		}
		return m, nil
	}
}

// ReadHostMsgV0 reads a v0 host->client message, which is only ever a receipt,
// in the same shape as ReadHostMsgV1 so a caller can treat both alike.
func ReadHostMsgV0(r io.Reader) (HostMsg, error) {
	opID, status, err := ReadReceipt(r)
	if err != nil {
		return HostMsg{}, err
	}
	return HostMsg{Type: V1Receipt, OpID: opID, Status: status}, nil
}
