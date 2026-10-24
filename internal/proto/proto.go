// Package proto defines the wire format shared by host and client.
//
// The client opens one bidirectional "control" stream: it carries Hello, Ack and
// Input messages client->host and Receipt messages host->client. Screen updates
// travel on separate host-opened unidirectional streams (one per frame), each
// starting with a FrameHeader followed by tile records until EOF.
package proto

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// Client -> host message types.
const (
	MsgHello byte = 1 // pwLen u8, password (may be empty)
	MsgAck   byte = 2 // seq u32
	MsgInput byte = 3 // opID u64, kind u8, x i32, y i32, arg i32
)

// Host -> client message types.
const MsgReceipt byte = 0x81 // opID u64, status u8

// Input kinds.
const (
	KindMouseMove byte = 1 // x, y in host pixels
	KindMouseDown byte = 2 // arg: 0 left, 1 right, 2 middle
	KindMouseUp   byte = 3
	KindWheel     byte = 4 // arg: delta in 1/120 notches
	KindKeyDown   byte = 5 // arg: Windows virtual-key code
	KindKeyUp     byte = 6
)

// Receipt statuses.
const (
	StatusExecuted byte = 1
	StatusFailed   byte = 2
)

// Input is one user action. OpID 0 means fire-and-forget: no dedup, no receipt.
type Input struct {
	OpID uint64
	Kind byte
	X, Y int32
	Arg  int32
}

// Msg is a decoded client->host message.
type Msg struct {
	Type     byte
	Password string // MsgHello
	Seq      uint32 // MsgAck
	Input    Input  // MsgInput
}

func WriteHello(w io.Writer, password string) error {
	if len(password) > 255 {
		return errors.New("proto: password too long")
	}
	return write(w, append([]byte{MsgHello, byte(len(password))}, password...))
}

func WriteAck(w io.Writer, seq uint32) error {
	b := []byte{MsgAck, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[1:], seq)
	return write(w, b)
}

func WriteInput(w io.Writer, in Input) error {
	b := make([]byte, 22)
	b[0] = MsgInput
	binary.LittleEndian.PutUint64(b[1:], in.OpID)
	b[9] = in.Kind
	binary.LittleEndian.PutUint32(b[10:], uint32(in.X))
	binary.LittleEndian.PutUint32(b[14:], uint32(in.Y))
	binary.LittleEndian.PutUint32(b[18:], uint32(in.Arg))
	return write(w, b)
}

func WriteReceipt(w io.Writer, opID uint64, status byte) error {
	b := make([]byte, 10)
	b[0] = MsgReceipt
	binary.LittleEndian.PutUint64(b[1:], opID)
	b[9] = status
	return write(w, b)
}

// write sends b in a single Write so concurrent writers holding a lock never
// interleave partial messages.
func write(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}

// ReadMsg reads one client->host message.
func ReadMsg(r io.Reader) (Msg, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return Msg{}, err
	}
	m := Msg{Type: t[0]}
	switch t[0] {
	case MsgHello:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return m, err
		}
		pw := make([]byte, n[0])
		if _, err := io.ReadFull(r, pw); err != nil {
			return m, err
		}
		m.Password = string(pw)
	case MsgAck:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return m, err
		}
		m.Seq = binary.LittleEndian.Uint32(b[:])
	case MsgInput:
		var b [21]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return m, err
		}
		m.Input = Input{
			OpID: binary.LittleEndian.Uint64(b[0:]),
			Kind: b[8],
			X:    int32(binary.LittleEndian.Uint32(b[9:])),
			Y:    int32(binary.LittleEndian.Uint32(b[13:])),
			Arg:  int32(binary.LittleEndian.Uint32(b[17:])),
		}
	default:
		return m, errors.New("proto: unknown message type")
	}
	return m, nil
}

// ReadReceipt reads one host->client message.
func ReadReceipt(r io.Reader) (opID uint64, status byte, err error) {
	var b [10]byte
	if _, err = io.ReadFull(r, b[:]); err != nil {
		return
	}
	if b[0] != MsgReceipt {
		return 0, 0, errors.New("proto: unknown message type")
	}
	return binary.LittleEndian.Uint64(b[1:]), b[9], nil
}

// FrameHeader precedes the tile records of one screen update.
type FrameHeader struct {
	Seq  uint32
	W, H uint16
}

const FrameHeaderSize = 8

func (h FrameHeader) Marshal() []byte {
	b := make([]byte, FrameHeaderSize)
	binary.LittleEndian.PutUint32(b[0:], h.Seq)
	binary.LittleEndian.PutUint16(b[4:], h.W)
	binary.LittleEndian.PutUint16(b[6:], h.H)
	return b
}

func ParseFrameHeader(b []byte) (FrameHeader, error) {
	if len(b) < FrameHeaderSize {
		return FrameHeader{}, errors.New("proto: short frame header")
	}
	return FrameHeader{
		Seq: binary.LittleEndian.Uint32(b[0:]),
		W:   binary.LittleEndian.Uint16(b[4:]),
		H:   binary.LittleEndian.Uint16(b[6:]),
	}, nil
}

const dedupSize = 1024

// Dedup remembers the status of the last 1024 operations so a repeated opID is
// answered from the cache instead of being executed again.
type Dedup struct {
	mu   sync.Mutex
	m    map[uint64]byte
	ring [dedupSize]uint64
	n    int
}

func NewDedup() *Dedup { return &Dedup{m: make(map[uint64]byte, dedupSize)} }

func (d *Dedup) Get(id uint64) (byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.m[id]
	return s, ok
}

func (d *Dedup) Put(id uint64, status byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.m[id]; !ok {
		if d.n >= dedupSize {
			delete(d.m, d.ring[d.n%dedupSize])
		}
		d.ring[d.n%dedupSize] = id
		d.n++
	}
	d.m[id] = status
}
