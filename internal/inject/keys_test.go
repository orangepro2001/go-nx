package inject

import "testing"

func TestExtendedKeys(t *testing.T) {
	for vk, name := range map[uint16]string{
		0x21: "Page Up", 0x22: "Page Down", 0x23: "End", 0x24: "Home",
		0x25: "Left", 0x26: "Up", 0x27: "Right", 0x28: "Down",
		0x2C: "Print Screen", 0x2D: "Insert", 0x2E: "Delete",
		0x5B: "Left Win", 0x5C: "Right Win", 0x5D: "Apps",
		0x6F: "Numpad /", 0x90: "Num Lock", 0xA3: "Right Ctrl", 0xA5: "Right Alt",
	} {
		if !Extended(vk) {
			t.Errorf("%s (%#x) must be extended", name, vk)
		}
	}
	for vk, name := range map[uint16]string{
		'A': "A", 0x0D: "Enter (the numpad one comes in through the 0x100 bit)", 0x10: "Shift", 0xA0: "Left Shift", 0xA1: "Right Shift",
		0xA2: "Left Ctrl", 0xA4: "Left Alt", 0x70: "F1", 0x87: "F24", 0x13: "Pause", 0x6A: "Numpad *", 0x60: "Numpad 0",
	} {
		if Extended(vk) {
			t.Errorf("%s (%#x) must not be extended", name, vk)
		}
	}
}
