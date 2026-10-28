package proto

import (
	"bytes"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Input{OpID: 1<<63 + 5, Kind: KindMouseDown, X: -3, Y: 1079, Arg: 2}
	WriteHello(&buf, "pw 1")
	WriteAck(&buf, 77)
	WriteInput(&buf, in)

	m, err := ReadMsg(&buf)
	if err != nil || m.Type != MsgHello || m.Password != "pw 1" {
		t.Fatalf("hello = %+v, %v", m, err)
	}
	m, err = ReadMsg(&buf)
	if err != nil || m.Type != MsgAck || m.Seq != 77 {
		t.Fatalf("ack = %+v, %v", m, err)
	}
	m, err = ReadMsg(&buf)
	if err != nil || m.Type != MsgInput || m.Input != in {
		t.Fatalf("input = %+v, %v", m, err)
	}
	if _, err := ReadMsg(&buf); err == nil {
		t.Fatal("expected EOF")
	}
}

func TestReceiptAndHeader(t *testing.T) {
	var buf bytes.Buffer
	WriteReceipt(&buf, 42, StatusFailed)
	id, st, err := ReadReceipt(&buf)
	if err != nil || id != 42 || st != StatusFailed {
		t.Fatalf("receipt = %d %d %v", id, st, err)
	}
	h, err := ParseFrameHeader(FrameHeader{Seq: 9, W: 1920, H: 1080}.Marshal())
	if err != nil || h != (FrameHeader{9, 1920, 1080}) {
		t.Fatalf("header = %+v %v", h, err)
	}
	if _, err := ParseFrameHeader([]byte{1}); err == nil {
		t.Fatal("short header accepted")
	}
}

func TestUnknownTypeRejected(t *testing.T) {
	if _, err := ReadMsg(bytes.NewReader([]byte{200})); err == nil {
		t.Fatal("unknown type accepted")
	}
}

func TestDedupEvictsOldest(t *testing.T) {
	d := NewDedup()
	for i := uint64(1); i <= dedupSize+10; i++ {
		d.Put(i, StatusExecuted)
	}
	if _, ok := d.Get(1); ok {
		t.Error("oldest entry not evicted")
	}
	if s, ok := d.Get(dedupSize + 10); !ok || s != StatusExecuted {
		t.Error("newest entry missing")
	}
	if len(d.m) != dedupSize {
		t.Errorf("cache size = %d, want %d", len(d.m), dedupSize)
	}
}

func TestParseFrameHeaderRejectsUnreasonableSizes(t *testing.T) {
	cases := []struct {
		name string
		w, h uint16
		ok   bool
	}{
		{"1080p", 1920, 1080, true},
		{"8K", 7680, 4320, true},
		{"widest allowed", MaxFrameDim, 1, true},
		{"zero width", 0, 1080, false},
		{"zero height", 1920, 0, false},
		{"both max uint16", 65535, 65535, false},
		{"one side over the dimension cap", MaxFrameDim + 1, 1, false},
		{"area over the pixel cap", 12000, 6000, false},
	}
	for _, c := range cases {
		_, err := ParseFrameHeader(FrameHeader{Seq: 1, W: c.w, H: c.h}.Marshal())
		if (err == nil) != c.ok {
			t.Errorf("%s (%dx%d): err = %v, want ok=%v", c.name, c.w, c.h, err, c.ok)
		}
		if got := ValidFrameSize(int(c.w), int(c.h)); got != c.ok {
			t.Errorf("%s: ValidFrameSize = %v, want %v", c.name, got, c.ok)
		}
	}
}

// The fuzz targets below read what a peer controls. They must never panic and
// must never accept a value outside the documented limits.
func FuzzReadMsg(f *testing.F) {
	var buf bytes.Buffer
	WriteHello(&buf, "pw")
	WriteAck(&buf, 3)
	WriteInput(&buf, Input{OpID: 7, Kind: KindMouseMove, X: 5, Y: 6})
	f.Add(buf.Bytes())
	f.Add([]byte{})
	f.Add([]byte{MsgHello, 255})
	f.Add([]byte{200})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		for i := 0; i < 64; i++ { // a stream of messages; stop at the first error
			if _, err := ReadMsg(r); err != nil {
				return
			}
		}
	})
}

func FuzzParseFrameHeader(f *testing.F) {
	f.Add(FrameHeader{Seq: 1, W: 1920, H: 1080}.Marshal())
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{255}, FrameHeaderSize))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := ParseFrameHeader(data)
		if err == nil && !ValidFrameSize(int(h.W), int(h.H)) {
			t.Fatalf("accepted header %+v outside the limits", h)
		}
	})
}

// The bytes below are what the NightDesk clients put on the wire for the two v0
// extensions, written out by hand from the format rather than produced by this
// package's own writers, so a change here that breaks them is caught.
func TestV0ExtensionWireVectors(t *testing.T) {
	text := []byte{
		0x04,                      // MsgText
		0x01, 0, 0, 0, 0, 0, 0, 0, // opID = 1
		0x06, 0x00, // 6 bytes follow: h, é (2 bytes), l, l, o
		'h', 0xC3, 0xA9, 'l', 'l', 'o', // "héllo"
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, 1, "héllo"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), text) {
		t.Fatalf("WriteText bytes\n got %x\nwant %x", buf.Bytes(), text)
	}
	m, err := ReadMsg(bytes.NewReader(text))
	if err != nil || m.Type != MsgText || m.OpID != 1 || m.Text != "héllo" {
		t.Fatalf("ReadMsg(text) = %+v, %v", m, err)
	}

	hwheel := []byte{
		0x03,                      // MsgInput
		0x02, 0, 0, 0, 0, 0, 0, 0, // opID = 2
		0x07,          // KindWheelH
		0x0A, 0, 0, 0, // x = 10
		0x14, 0, 0, 0, // y = 20
		0x78, 0, 0, 0, // arg = 120 (one notch)
	}
	buf.Reset()
	if err := WriteInput(&buf, Input{OpID: 2, Kind: KindWheelH, X: 10, Y: 20, Arg: 120}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), hwheel) {
		t.Fatalf("WriteInput(WheelH) bytes\n got %x\nwant %x", buf.Bytes(), hwheel)
	}
}

func TestTextLimitsAndSanitising(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, 1, string(make([]byte, MaxTextLen+1))); err == nil {
		t.Error("WriteText accepted more than MaxTextLen bytes")
	}
	if err := WriteText(&buf, 1, string(make([]byte, MaxTextLen))); err != nil {
		t.Errorf("WriteText refused exactly MaxTextLen bytes: %v", err)
	}

	// A peer that announces more than the limit is refused before the body is read.
	raw := []byte{MsgText, 0, 0, 0, 0, 0, 0, 0, 0, byte((MaxTextLen + 1) & 0xff), byte((MaxTextLen + 1) >> 8)}
	if _, err := ReadMsg(bytes.NewReader(raw)); err == nil {
		t.Error("ReadMsg accepted a text longer than MaxTextLen")
	}
	// A truncated body is an error, not a short string.
	short := []byte{MsgText, 0, 0, 0, 0, 0, 0, 0, 0, 5, 0, 'a', 'b'}
	if _, err := ReadMsg(bytes.NewReader(short)); err == nil {
		t.Error("ReadMsg accepted a truncated text body")
	}
	// Invalid UTF-8 is replaced, not passed on to the injector as garbage.
	bad := []byte{MsgText, 0, 0, 0, 0, 0, 0, 0, 0, 3, 0, 'a', 0xFF, 'b'}
	m, err := ReadMsg(bytes.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "a�b" {
		t.Errorf("invalid UTF-8 became %q, want %q", m.Text, "a�b")
	}
}
