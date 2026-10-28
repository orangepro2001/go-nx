package inject

// extendedKeys are the virtual keys that Windows reports with the extended-key
// flag, which a synthetic key event must set for the system to tell them from
// their numpad twins. They are the right-hand Ctrl and Alt, Insert, Delete, Home,
// End, Page Up/Down and the arrows, Num Lock, Print Screen, the numpad divide,
// the Windows keys and the application (menu) key.
var extendedKeys = map[uint16]struct{}{
	0x21: {}, 0x22: {}, 0x23: {}, 0x24: {}, // Page Up, Page Down, End, Home
	0x25: {}, 0x26: {}, 0x27: {}, 0x28: {}, // arrows
	0x2C: {},           // Print Screen
	0x2D: {}, 0x2E: {}, // Insert, Delete
	0x5B: {}, 0x5C: {}, 0x5D: {}, // left and right Windows, application
	0x6F: {},           // numpad divide
	0x90: {},           // Num Lock
	0xA3: {}, 0xA5: {}, // right Ctrl, right Alt
}

// Extended reports whether vk is reported with the extended-key flag.
func Extended(vk uint16) bool {
	_, ok := extendedKeys[vk]
	return ok
}
