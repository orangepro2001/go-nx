package text

import (
	"strings"
	"testing"
	"unicode/utf8"
)

var body = Style{Size: 10} // Fake: a narrow rune is 5 wide, a wide one 10

func TestWrapBreaksAtSpacesAndKeepsNewlines(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		maxW float64
		want []string
	}{
		{"fits", "hello world", 200, []string{"hello world"}},
		{"breaks at the space", "hello world", 50, []string{"hello", "world"}},
		{"several words per line", "aa bb cc dd", 30, []string{"aa bb", "cc dd"}},
		{"a newline always breaks", "one\ntwo", 200, []string{"one", "two"}},
		{"an empty line is kept", "one\n\ntwo", 200, []string{"one", "", "two"}},
		{"no text, no lines", "", 100, []string{""}},
		{"trailing spaces are not kept", "ab   ", 100, []string{"ab"}},
		{"trailing newline adds nothing", "one\n", 200, []string{"one"}},
		{"carriage returns are dropped", "one\r\ntwo", 200, []string{"one", "two"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Wrap(Fake{}, c.in, body, c.maxW)
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("Wrap(%q, %v) = %q, want %q", c.in, c.maxW, got, c.want)
			}
		})
	}
}

// A sentence in Chinese has no spaces; it has to wrap anyway, and punctuation must not
// start a line.
func TestWrapChineseWithoutSpaces(t *testing.T) {
	in := "无法连接到主机，请检查地址是否正确。"
	lines := Wrap(Fake{}, in, body, 80) // 8 wide characters per line
	if len(lines) < 2 {
		t.Fatalf("a 17-character sentence in an 8-character box gave %q", lines)
	}
	for _, l := range lines {
		if w := (Fake{}).Width(l, body); w > 80 {
			t.Errorf("line %q is %v wide, over 80", l, w)
		}
		if r, _ := utf8.DecodeRuneInString(l); r == '，' || r == '。' {
			t.Errorf("line %q starts with punctuation that must stay with the character before it", l)
		}
	}
	if strings.Join(lines, "") != in {
		t.Errorf("text was lost or changed: %q", lines)
	}
}

func TestWrapKorean(t *testing.T) {
	in := "호스트에 연결할 수 없습니다. 주소를 확인하세요."
	lines := Wrap(Fake{}, in, body, 100)
	if len(lines) < 2 {
		t.Fatalf("gave %q", lines)
	}
	for _, l := range lines {
		if w := (Fake{}).Width(l, body); w > 100 {
			t.Errorf("line %q is %v wide", l, w)
		}
	}
	if strings.Join(lines, " ") != in {
		t.Errorf("text changed: %q", lines)
	}
	// Korean is broken at spaces, not inside a word.
	for _, l := range lines {
		for _, w := range strings.Fields(l) {
			if !strings.Contains(in, w) {
				t.Errorf("line %q holds %q, which is not a whole word of the sentence", l, w)
			}
		}
	}
}

func TestKoreanWordsStayWholeNextToDigitsAndLatin(t *testing.T) {
	// "7000을" and "go-nx를" are one word each for line breaking.
	for _, l := range Wrap(Fake{}, "주소 192.168.0.1:7000을 확인하고 go-nx를 다시 시작하세요", body, 120) {
		if strings.HasPrefix(l, "을") || strings.HasPrefix(l, "를") {
			t.Errorf("line %q starts with a particle cut off its word", l)
		}
	}
}

func TestWrapBreaksAWordThatIsWiderThanTheLine(t *testing.T) {
	long := "192.168.100.200:7000-and-more-and-more"
	lines := Wrap(Fake{}, long, body, 60) // 12 narrow characters
	for _, l := range lines {
		if w := (Fake{}).Width(l, body); w > 60 {
			t.Errorf("line %q is %v wide", l, w)
		}
	}
	if strings.Join(lines, "") != long {
		t.Errorf("text lost: %q", lines)
	}
	// A single cluster wider than the box still gets a line of its own.
	if got := Wrap(Fake{}, "中", body, 3); len(got) != 1 || got[0] != "中" {
		t.Errorf("Wrap of one wide rune in a narrow box = %q", got)
	}
}

func TestWrapDoesNotSplitAGraphemeCluster(t *testing.T) {
	// "e" + combining acute, and a family emoji joined with ZWJ, are one cluster each.
	in := "éééééé"
	for _, l := range Wrap(Fake{}, in, body, 12) {
		if strings.HasPrefix(l, "́") {
			t.Errorf("line %q starts with a combining mark", l)
		}
	}
}

func TestTruncate(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		maxW float64
		want string
	}{
		{"fits", "hello", 25, "hello"},
		{"cut", "hello world", 40, "hello w…"},
		{"cut chinese", "远程控制主机", 45, "远程控制…"},
		{"cut korean", "원격 제어 호스트", 55, "원격 제어…"},
		{"only the ellipsis fits", "hello", 5, "…"},
		{"one letter and the ellipsis", "hello", 10, "h…"},
		{"not even that", "hello", 4, ""},
		{"empty", "", 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Truncate(Fake{}, c.in, body, c.maxW)
			if got != c.want {
				t.Errorf("Truncate(%q, %v) = %q, want %q", c.in, c.maxW, got, c.want)
			}
			if w := (Fake{}).Width(got, body); w > c.maxW {
				t.Errorf("result %q is %v wide, over %v", got, w, c.maxW)
			}
		})
	}
}

func TestTruncateDoesNotCutAClusterInHalf(t *testing.T) {
	in := "aéééééé" // each é is e plus a combining mark
	got := Truncate(Fake{}, in, body, 30)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	trimmed := strings.TrimSuffix(got, Ellipsis)
	if strings.HasSuffix(trimmed, "e") && !strings.HasSuffix(trimmed, "é") && strings.Count(in, "é") > 0 && len(trimmed) > 1 {
		// The kept text ends in a bare e: the mark that belongs to it was cut off.
		if strings.HasPrefix(in[len(trimmed):], "́") {
			t.Errorf("%q cuts the accent off its letter", got)
		}
	}
}

func TestWiderStyleBreaksSooner(t *testing.T) {
	small, big := Style{Size: 10}, Style{Size: 20}
	a := Wrap(Fake{}, "aaaa bbbb cccc", small, 100)
	b := Wrap(Fake{}, "aaaa bbbb cccc", big, 100)
	if len(b) <= len(a) {
		t.Errorf("bigger type made %d lines, smaller %d", len(b), len(a))
	}
}
