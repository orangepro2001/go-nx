package app

import (
	"sort"

	"github.com/hajimehoshi/ebiten/v2"
)

// vk maps Ebiten keys to Windows virtual-key codes. Bit 0x100 marks an extended
// key where the code alone is ambiguous (numpad Enter is VK_RETURN, extended).
var vk = map[ebiten.Key]uint16{
	ebiten.KeyBackspace: 0x08, ebiten.KeyTab: 0x09, ebiten.KeyEnter: 0x0D,
	ebiten.KeyShiftLeft: 0xA0, ebiten.KeyShiftRight: 0xA1,
	ebiten.KeyControlLeft: 0xA2, ebiten.KeyControlRight: 0xA3,
	ebiten.KeyAltLeft: 0xA4, ebiten.KeyAltRight: 0xA5,
	ebiten.KeyPause: 0x13, ebiten.KeyCapsLock: 0x14, ebiten.KeyEscape: 0x1B, ebiten.KeySpace: 0x20,
	ebiten.KeyPageUp: 0x21, ebiten.KeyPageDown: 0x22, ebiten.KeyEnd: 0x23, ebiten.KeyHome: 0x24,
	ebiten.KeyArrowLeft: 0x25, ebiten.KeyArrowUp: 0x26, ebiten.KeyArrowRight: 0x27, ebiten.KeyArrowDown: 0x28,
	ebiten.KeyInsert: 0x2D, ebiten.KeyDelete: 0x2E,
	ebiten.KeyMetaLeft: 0x5B, ebiten.KeyMetaRight: 0x5C, ebiten.KeyContextMenu: 0x5D,
	ebiten.KeyNumpad0: 0x60, ebiten.KeyNumpad1: 0x61, ebiten.KeyNumpad2: 0x62, ebiten.KeyNumpad3: 0x63,
	ebiten.KeyNumpad4: 0x64, ebiten.KeyNumpad5: 0x65, ebiten.KeyNumpad6: 0x66, ebiten.KeyNumpad7: 0x67,
	ebiten.KeyNumpad8: 0x68, ebiten.KeyNumpad9: 0x69,
	ebiten.KeyNumpadMultiply: 0x6A, ebiten.KeyNumpadAdd: 0x6B, ebiten.KeyNumpadSubtract: 0x6D,
	ebiten.KeyNumpadDecimal: 0x6E, ebiten.KeyNumpadDivide: 0x6F,
	ebiten.KeyNumpadEnter: 0x10D, ebiten.KeyNumLock: 0x90, ebiten.KeyScrollLock: 0x91,
	ebiten.KeySemicolon: 0xBA, ebiten.KeyEqual: 0xBB, ebiten.KeyComma: 0xBC, ebiten.KeyMinus: 0xBD,
	ebiten.KeyPeriod: 0xBE, ebiten.KeySlash: 0xBF, ebiten.KeyBackquote: 0xC0,
	ebiten.KeyBracketLeft: 0xDB, ebiten.KeyBackslash: 0xDC, ebiten.KeyBracketRight: 0xDD, ebiten.KeyQuote: 0xDE,
}

func init() {
	for i := 0; i < 26; i++ {
		vk[ebiten.KeyA+ebiten.Key(i)] = uint16('A' + i)
	}
	for i := 0; i < 10; i++ {
		vk[ebiten.KeyDigit0+ebiten.Key(i)] = uint16('0' + i)
	}
	for i := 0; i < 12; i++ {
		vk[ebiten.KeyF1+ebiten.Key(i)] = uint16(0x70 + i)
	}
	for i := 0; i < 12; i++ {
		vk[ebiten.KeyF13+ebiten.Key(i)] = uint16(0x7C + i) // F13 to F24
	}
	vk[ebiten.KeyPrintScreen] = 0x2C
	vk[ebiten.KeyIntlBackslash] = 0xE2 // the extra key on ISO keyboards

	// Keys are visited in a fixed order: ranging over the map would hand the
	// events of one tick to the host in a different order each time.
	for k := range vk {
		vkKeys = append(vkKeys, k)
	}
	sort.Slice(vkKeys, func(i, j int) bool { return vk[vkKeys[i]] < vk[vkKeys[j]] })
}

// vkKeys is every key in vk, ordered by virtual-key code.
var vkKeys []ebiten.Key
