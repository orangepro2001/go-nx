package vdisplay

import (
	"encoding/binary"
	"errors"
)

// A Topology is QueryDisplayConfig's encoding: DISPLAYCONFIG_PATH_INFO records of 72 bytes,
// whose source mode index is at byte 12 and target mode index at byte 32, and
// DISPLAYCONFIG_MODE_INFO records of 64 bytes that start with their kind (1 source,
// 2 target). A source mode keeps the source's position at bytes 28 to 36.
const (
	pathSize = 72
	modeSize = 64
)

// validTopology checks a topology before it goes to Windows: whole records, not too many,
// and every mode index pointing at a mode of the right kind. A layout read back from disk
// after a crash is checked like any other.
func validTopology(t Topology) error {
	if len(t.Paths) == 0 || len(t.Paths)%pathSize != 0 || len(t.Paths) > 128*pathSize ||
		len(t.Modes) == 0 || len(t.Modes)%modeSize != 0 || len(t.Modes) > 512*modeSize {
		return errors.New("the display layout has the wrong length")
	}
	n := len(t.Modes) / modeSize
	for off := 0; off < len(t.Paths); off += pathSize {
		for _, f := range []struct {
			at   int
			kind uint32
		}{{12, 1}, {32, 2}} {
			i := binary.LittleEndian.Uint32(t.Paths[off+f.at:])
			if i == 0xFFFFFFFF {
				continue
			}
			if int(i) >= n || binary.LittleEndian.Uint32(t.Modes[int(i)*modeSize:]) != f.kind {
				return errors.New("the display layout points at a mode that is not there")
			}
		}
	}
	return nil
}
