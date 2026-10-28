// Package inject performs mouse and keyboard input on the local desktop. The
// Windows implementation is in inject_windows.go; the parts that are plain
// arithmetic live in files without a build tag so they can be tested anywhere.
package inject

import "unicode/utf16"

// textChunk is how many UTF-16 code units one SendInput call types. Each unit is
// a key down and a key up, so a chunk is at most twice this many INPUT records;
// the limit keeps the call's buffer small however much text arrives.
const textChunk = 64

// textUnits is the UTF-16 form of text: what a KEYEVENTF_UNICODE injection types,
// one code unit per key. A character outside the Basic Multilingual Plane is two
// units (a surrogate pair), which the receiving application puts back together.
func textUnits(text string) []uint16 { return utf16.Encode([]rune(text)) }

// splitUnits cuts units into pieces of at most n, never between the two halves of
// a surrogate pair: a piece is typed in one call, and other input can slip in
// between calls, which would otherwise land inside a character.
func splitUnits(units []uint16, n int) [][]uint16 {
	var out [][]uint16
	for len(units) > 0 {
		end := min(n, len(units))
		if end < len(units) && isHighSurrogate(units[end-1]) {
			end-- // keep the pair together
		}
		if end == 0 { // n == 1 and the first unit is a high surrogate: take the whole pair
			end = min(2, len(units))
		}
		out = append(out, units[:end])
		units = units[end:]
	}
	return out
}

func isHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }
