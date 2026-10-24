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
