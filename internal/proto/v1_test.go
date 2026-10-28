package proto

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// Frames are written out byte by byte from docs/protocol.md, not produced by the
// writers under test, so the document and the code cannot drift apart unnoticed.
func TestV1WireVectors(t *testing.T) {
	cases := []struct {
		name  string
		write func(*bytes.Buffer) error
		want  []byte
	}{
		{"hello", func(b *bytes.Buffer) error {
			return WriteHelloV1(b, HelloV1{Version: 1, Caps: 0, Password: "pw", Name: "PC-1"})
		}, []byte{
			0x01, 0x12, 0, 0, 0, // HELLO, 18 payload bytes: 2 + 8 + 1 + 2 + 1 + 4
			0x01, 0x00, // version 1
			0, 0, 0, 0, 0, 0, 0, 0, // caps 0
			0x02, 'p', 'w', // password
			0x04, 'P', 'C', '-', '1', // name
		}},
		{"ack", func(b *bytes.Buffer) error { return WriteAckV1(b, 7) },
			[]byte{0x02, 0x04, 0, 0, 0, 0x07, 0, 0, 0}},
		{"input", func(b *bytes.Buffer) error {
			return WriteInputV1(b, Input{OpID: 5, Kind: KindMouseDown, X: -1, Y: 2, Arg: 1})
		}, []byte{
			0x03, 0x15, 0, 0, 0, // INPUT, 21 payload bytes
			5, 0, 0, 0, 0, 0, 0, 0, // opID
			0x02,                   // kind
			0xFF, 0xFF, 0xFF, 0xFF, // x = -1
			2, 0, 0, 0, // y
			1, 0, 0, 0, // arg
		}},
		{"text", func(b *bytes.Buffer) error { return WriteTextV1(b, 9, "한") },
			[]byte{0x04, 0x0B, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0, 0xED, 0x95, 0x9C}},
		{"receipt", func(b *bytes.Buffer) error { return WriteReceiptV1(b, 9, StatusFailed) },
			[]byte{0x81, 0x09, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0, 0x02}},
		{"welcome", func(b *bytes.Buffer) error {
			return WriteWelcomeV1(b, WelcomeV1{Version: 1, Caps: CapText | CapWheelH, W: 1920, H: 1080, Name: "H"})
		}, []byte{
			0x82, 0x10, 0, 0, 0, // WELCOME, 16 payload bytes
			0x01, 0x00, // version
			0x03, 0, 0, 0, 0, 0, 0, 0, // caps: text | wheelH
			0x80, 0x07, // 1920
			0x38, 0x04, // 1080
			0x01, 'H', // name
		}},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := c.write(&b); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !bytes.Equal(b.Bytes(), c.want) {
			t.Errorf("%s bytes\n got %x\nwant %x", c.name, b.Bytes(), c.want)
		}
	}
}

func TestV1ClientMessagesRoundTrip(t *testing.T) {
	var b bytes.Buffer
	WriteHelloV1(&b, HelloV1{Version: 1, Caps: CapText, Password: "s3cret", Name: "Réception 한국"})
	WriteAckV1(&b, 42)
	WriteInputV1(&b, Input{OpID: 1 << 40, Kind: KindWheelH, X: 3, Y: 4, Arg: -120})
	WriteTextV1(&b, 77, strings.Repeat("字", MaxTextLenV1/3))

	m, err := ReadMsgV1(&b)
	if err != nil || m.Type != MsgHello || m.Version != 1 || m.Caps != CapText || m.Password != "s3cret" || m.Name != "Réception 한국" {
		t.Fatalf("hello = %+v, %v", m, err)
	}
	if m, err = ReadMsgV1(&b); err != nil || m.Type != MsgAck || m.Seq != 42 {
		t.Fatalf("ack = %+v, %v", m, err)
	}
	if m, err = ReadMsgV1(&b); err != nil || m.Type != MsgInput || m.Input != (Input{OpID: 1 << 40, Kind: KindWheelH, X: 3, Y: 4, Arg: -120}) {
		t.Fatalf("input = %+v, %v", m, err)
	}
	if m, err = ReadMsgV1(&b); err != nil || m.Type != MsgText || m.OpID != 77 || m.Text != strings.Repeat("字", MaxTextLenV1/3) {
		t.Fatalf("text = %d bytes, op %d, %v", len(m.Text), m.OpID, err)
	}
	if _, err = ReadMsgV1(&b); err == nil {
		t.Fatal("expected EOF at the end")
	}
}

func TestV1HostMessagesRoundTrip(t *testing.T) {
	var b bytes.Buffer
	WriteWelcomeV1(&b, WelcomeV1{Version: 1, Caps: CapText | CapResize, W: 3840, H: 2160, Name: "车间 1 号机"})
	WriteReceiptV1(&b, 5, StatusExecuted)

	m, err := ReadHostMsgV1(&b)
	if err != nil || m.Type != V1Welcome || m.Welcome.Version != 1 || m.Welcome.Caps != CapText|CapResize ||
		m.Welcome.W != 3840 || m.Welcome.H != 2160 || m.Welcome.Name != "车间 1 号机" {
		t.Fatalf("welcome = %+v, %v", m, err)
	}
	if m, err = ReadHostMsgV1(&b); err != nil || m.Type != V1Receipt || m.OpID != 5 || m.Status != StatusExecuted {
		t.Fatalf("receipt = %+v, %v", m, err)
	}
}

func TestV1VirtualDisplayMessagesRoundTrip(t *testing.T) {
	var c bytes.Buffer
	WriteViewSizeV1(&c, 2560, 1440)
	WriteViewSizeV1(&c, 0, 0)
	if m, err := ReadMsgV1(&c); err != nil || m.Type != V1ClientViewSize || m.W != 2560 || m.H != 1440 {
		t.Fatalf("view size = %+v, %v", m, err)
	}
	if m, err := ReadMsgV1(&c); err != nil || m.Type != V1ClientViewSize || m.W != 0 || m.H != 0 {
		t.Fatalf("no virtual display = %+v, %v", m, err)
	}

	var h bytes.Buffer
	WriteDisplayStateV1(&h, DisplayState{Virtual: true, W: 2560, H: 1440})
	WriteDisplayStateV1(&h, DisplayState{Message: strings.Repeat("驱动", 100)}) // longer than allowed: cut, still valid UTF-8
	if m, err := ReadHostMsgV1(&h); err != nil || m.Type != V1DisplayState || !m.Display.Virtual || m.Display.W != 2560 || m.Display.H != 1440 || m.Display.Message != "" {
		t.Fatalf("state = %+v, %v", m, err)
	}
	m, err := ReadHostMsgV1(&h)
	if err != nil || m.Display.Virtual || len(m.Display.Message) > MaxStateMsgLen || !utf8.ValidString(m.Display.Message) || !strings.HasPrefix(m.Display.Message, "驱动驱动") {
		t.Fatalf("long message = %q, %v", m.Display.Message, err)
	}
	if got := (CapVirtualDisplay | CapText).Names(); !slices.Equal(got, []string{"text", "vdisplay"}) {
		t.Errorf("names = %v", got)
	}
}

// What makes v1 worth having: a peer that is newer than we are can send things we
// do not know and we carry on.
func TestV1SkipsUnknownFramesAndIgnoresExtraBytes(t *testing.T) {
	var b bytes.Buffer
	WriteFrame(&b, 0x7E, []byte("a message type from the future"))
	// A known type with a field appended by a later version.
	ack := make([]byte, 4+3)
	binary.LittleEndian.PutUint32(ack, 99)
	WriteFrame(&b, V1ClientAck, ack)
	WriteFrame(&b, 0x6F, nil)
	WriteAckV1(&b, 100)

	m, err := ReadMsgV1(&b)
	if err != nil || m.Type != MsgAck || m.Seq != 99 {
		t.Fatalf("first message = %+v, %v; want the ack with the extra bytes ignored", m, err)
	}
	if m, err = ReadMsgV1(&b); err != nil || m.Type != MsgAck || m.Seq != 100 {
		t.Fatalf("second message = %+v, %v; the empty unknown frame should have been skipped", m, err)
	}

	var h bytes.Buffer
	WriteFrame(&h, 0x9A, []byte{1, 2, 3})
	WriteReceiptV1(&h, 8, StatusExecuted)
	if hm, err := ReadHostMsgV1(&h); err != nil || hm.Type != V1Receipt || hm.OpID != 8 {
		t.Fatalf("host message = %+v, %v; the unknown frame should have been skipped", hm, err)
	}
}

func TestV1RefusesWhatItShouldNot(t *testing.T) {
	huge := []byte{V1ClientInput, 0xFF, 0xFF, 0xFF, 0xFF} // announces 4 GiB
	if _, err := ReadMsgV1(bytes.NewReader(huge)); err == nil {
		t.Error("a 4 GiB frame was accepted")
	}
	if _, _, err := ReadFrame(bytes.NewReader(huge)); err == nil {
		t.Error("ReadFrame accepted a 4 GiB frame")
	}
	justOver := make([]byte, 5)
	justOver[0] = 0x7E
	binary.LittleEndian.PutUint32(justOver[1:], MaxCtrlPayload+1)
	if _, _, err := ReadFrame(bytes.NewReader(justOver)); err == nil {
		t.Error("a frame one byte over the limit was accepted")
	}

	// Truncation, at every point of every kind of message.
	var full bytes.Buffer
	WriteHelloV1(&full, HelloV1{Version: 1, Password: "x", Name: "n"})
	WriteInputV1(&full, Input{OpID: 1, Kind: KindKeyDown, Arg: 65})
	WriteTextV1(&full, 2, "abc")
	for cut := 1; cut < full.Len(); cut++ {
		r := bytes.NewReader(full.Bytes()[:cut])
		var err error
		for err == nil {
			_, err = ReadMsgV1(r)
		}
	}

	// A known type whose payload is too short for its fixed fields.
	var short bytes.Buffer
	WriteFrame(&short, V1ClientInput, []byte{1, 2, 3})
	if _, err := ReadMsgV1(&short); err == nil {
		t.Error("an INPUT frame with a 3-byte payload was accepted")
	}

	// Writers refuse values that do not fit their field.
	var w bytes.Buffer
	if err := WriteHelloV1(&w, HelloV1{Version: 1, Password: strings.Repeat("p", 256)}); err == nil {
		t.Error("a 256-byte password was written")
	}
	if err := WriteHelloV1(&w, HelloV1{Version: 1, Name: strings.Repeat("n", MaxNameLen+1)}); err == nil {
		t.Error("an over-long device name was written")
	}
	if err := WriteTextV1(&w, 1, strings.Repeat("a", MaxTextLenV1+1)); err == nil {
		t.Error("over-long text was written")
	}
	if err := WriteWelcomeV1(&w, WelcomeV1{Name: strings.Repeat("n", MaxNameLen+1)}); err == nil {
		t.Error("an over-long host name was written")
	}
}

func TestV1TextIsSanitisedAndBounded(t *testing.T) {
	var b bytes.Buffer
	payload := append(make([]byte, 8), 'a', 0xFF, 'b')
	payload[0] = 3
	WriteFrame(&b, V1ClientText, payload)
	m, err := ReadMsgV1(&b)
	if err != nil || m.OpID != 3 || m.Text != "a�b" {
		t.Fatalf("text = %+v, %v", m, err)
	}
	// Text longer than the v1 limit, sent by a peer that ignores it, is refused.
	b.Reset()
	big := append(make([]byte, 8), bytes.Repeat([]byte("a"), MaxTextLenV1+1)...)
	WriteFrame(&b, V1ClientText, big)
	if _, err := ReadMsgV1(&b); err == nil {
		t.Error("text over MaxTextLenV1 was accepted")
	}
}

func TestCapsHas(t *testing.T) {
	c := CapText | CapCursor
	if !c.Has(CapText) || !c.Has(CapCursor) || c.Has(CapWheelH) || c.Has(CapText|CapWheelH) {
		t.Errorf("Has is wrong for %b", c)
	}
}

func FuzzReadMsgV1(f *testing.F) {
	var b bytes.Buffer
	WriteHelloV1(&b, HelloV1{Version: 1, Password: "pw", Name: "n"})
	WriteAckV1(&b, 1)
	WriteInputV1(&b, Input{OpID: 1, Kind: KindMouseMove, X: 1, Y: 2})
	WriteTextV1(&b, 2, "hi")
	f.Add(b.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0x7E, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{V1ClientHello, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		for i := 0; i < 64; i++ {
			if _, err := ReadMsgV1(r); err != nil {
				return
			}
		}
	})
}

func FuzzReadHostMsgV1(f *testing.F) {
	var b bytes.Buffer
	WriteWelcomeV1(&b, WelcomeV1{Version: 1, Caps: CapText, W: 100, H: 100, Name: "h"})
	WriteReceiptV1(&b, 1, StatusExecuted)
	f.Add(b.Bytes())
	f.Add([]byte{})
	f.Add([]byte{V1Welcome, 2, 0, 0, 0, 1, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		for i := 0; i < 64; i++ {
			if _, err := ReadHostMsgV1(r); err != nil {
				return
			}
		}
	})
}

func TestCapsNames(t *testing.T) {
	if got := Caps(0).Names(); len(got) != 0 {
		t.Errorf("no capabilities named %v", got)
	}
	all := CapText | CapWheelH | CapResize | CapCursor
	got := all.Names()
	want := []string{"text", "wheelh", "resize", "cursor"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
	// A capability this build has never heard of is still reported, so that a log or
	// a probe result never silently drops what a newer peer advertises.
	if got := (CapText | Caps(1)<<40).Names(); len(got) != 2 || got[0] != "text" || got[1] != "bit40" {
		t.Errorf("Names() with an unknown bit = %v, want [text bit40]", got)
	}
}
