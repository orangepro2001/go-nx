package inject

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestTextUnitsRoundTripAcrossScripts(t *testing.T) {
	for _, s := range []string{"", "plain ASCII", "héllo", "한국어", "中文输入", "mixed 한/中/é", "emoji 😀 and 𝒳", "tab\tand\nnewline"} {
		got := string(utf16.Decode(textUnits(s)))
		if got != s {
			t.Errorf("round trip of %q gave %q", s, got)
		}
	}
	if n := len(textUnits("😀")); n != 2 {
		t.Errorf("an astral character is %d units, want 2 (a surrogate pair)", n)
	}
}

func TestSplitUnitsKeepsEverythingInOrderAndNeverSplitsAPair(t *testing.T) {
	for _, tc := range []struct {
		text string
		n    int
	}{
		{strings.Repeat("a", 200), 64},
		{strings.Repeat("😀", 100), 64}, // 200 units: 64 would fall mid-pair at some boundaries
		{"a" + strings.Repeat("😀", 70), 64},
		{strings.Repeat("한", 130), 64},
		{"😀", 1}, // a piece smaller than a pair still keeps the pair whole
		{"x", 64},
	} {
		units := textUnits(tc.text)
		pieces := splitUnits(units, tc.n)
		var rejoined []uint16
		for i, p := range pieces {
			if len(p) == 0 {
				t.Fatalf("%q/%d: piece %d is empty", tc.text, tc.n, i)
			}
			if len(p) > tc.n && !(tc.n == 1 && len(p) == 2) {
				t.Errorf("%q/%d: piece %d has %d units", tc.text, tc.n, i, len(p))
			}
			if isHighSurrogate(p[len(p)-1]) {
				t.Errorf("%q/%d: piece %d ends between the halves of a pair", tc.text, tc.n, i)
			}
			rejoined = append(rejoined, p...)
		}
		if string(utf16.Decode(rejoined)) != tc.text {
			t.Errorf("%q/%d: pieces do not rejoin to the original", tc.text, tc.n)
		}
	}
	if got := splitUnits(nil, 64); len(got) != 0 {
		t.Errorf("splitUnits(nil) = %v, want nothing", got)
	}
}
