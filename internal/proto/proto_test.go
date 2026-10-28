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
