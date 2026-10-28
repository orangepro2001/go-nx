package i18n

import (
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// printfVerb is %s, %d, %v and the like: positional placeholders, which the tables do not use.
var printfVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

// literal is s without its {placeholders}.
func literal(s string) string { return regexp.MustCompile(`\{[^}]*\}`).ReplaceAllString(s, "") }

func eachLang(f func(l Lang, name string, get func(k Key) string)) {
	for _, l := range []Lang{English, Chinese, Korean} {
		l := l
		tr := New(l)
		f(l, l.Tag(), func(k Key) string { return tr.raw(k) })
	}
}

func TestEveryKeyHasAStringInEveryLanguage(t *testing.T) {
	for k := Key(0); k < numKeys; k++ {
		e := table[k]
		for lang, s := range map[string]string{"en": e.en, "zh": e.zh, "ko": e.ko} {
			if strings.TrimSpace(s) == "" {
				t.Errorf("key %d (%q): the %s string is empty", k, e.en, lang)
			}
		}
	}
}

// Korean puts the verb last and attaches particles to the word before them, so
// a sentence has to be one string with the same named placeholders as the others.
func TestPlaceholdersAreTheSameInEveryLanguage(t *testing.T) {
	for k := Key(0); k < numKeys; k++ {
		e := table[k]
		en := strings.Join(Placeholders(e.en), ",")
		for lang, s := range map[string]string{"zh": e.zh, "ko": e.ko} {
			if got := strings.Join(Placeholders(s), ","); got != en {
				t.Errorf("key %d (%q): {%s} in en but {%s} in %s", k, e.en, en, got, lang)
			}
		}
		for _, s := range []string{e.en, e.zh, e.ko} {
			if printfVerb.MatchString(s) || strings.Contains(s, "{}") {
				t.Errorf("key %d: %q uses a positional placeholder", k, s)
			}
			if strings.Count(s, "{") != strings.Count(s, "}") {
				t.Errorf("key %d: %q has unbalanced braces", k, s)
			}
		}
	}
}

// The particles of Korean change with the last sound of the word before them, so a
// template must not write them as "name은(는)"; the variable goes where no particle follows.
func TestKoreanDoesNotGuessParticles(t *testing.T) {
	for k := Key(0); k < numKeys; k++ {
		for _, bad := range []string{"(이)", "(가)", "(은)", "(는)", "(을)", "(를)", "(과)", "(와)", "(으)", "(이)가", "이(가)", "은(는)", "을(를)", "으로(로)", "과(와)"} {
			if strings.Contains(table[k].ko, bad) {
				t.Errorf("key %d: %q guesses a particle with %q", k, table[k].ko, bad)
			}
		}
	}
}

// A sentence that depends on a count has a One and an Other key. Only English tells
// them apart; the other languages must not (a stray difference would be a bug).
func TestPluralPairs(t *testing.T) {
	pairs := 0
	for k := Key(0); k < numKeys; k++ {
		one := table[k]
		if !strings.Contains(one.en, "{count}") {
			continue
		}
		// find the key whose English text is the "other" form: same prefix of the identifier is not
		// available at run time, so pairs are adjacent rows: ...One then ...Other.
		if k+1 < numKeys && strings.Contains(table[k+1].en, "{count}") && pairName(k) {
			other := table[k+1]
			pairs++
			if one.zh != other.zh || one.ko != other.ko {
				t.Errorf("keys %d/%d: zh/ko differ between one and other (%q vs %q)", k, k+1, one.zh, other.zh)
			}
			if one.en == other.en {
				t.Errorf("keys %d/%d: the English one and other are the same (%q)", k, k+1, one.en)
			}
		}
	}
	if pairs < 3 {
		t.Errorf("found only %d plural pairs", pairs)
	}
}

// pairName is true for the "One" key of a One/Other pair, which this test knows by
// the constants it checks explicitly.
func pairName(k Key) bool {
	switch k {
	case KeyTimeDaysAgoOne, KeyToastStrictDroppedOne, KeyForgetAskOne, KeyToastForgotOne:
		return true
	}
	return false
}

func TestCountChoosesTheEnglishFormByNumber(t *testing.T) {
	en, zh := New(English), New(Chinese)
	if got := en.Count(KeyToastForgotOne, KeyToastForgotOther, 1); got != "Forgot 1 paired device" {
		t.Errorf("one = %q", got)
	}
	if got := en.Count(KeyToastForgotOne, KeyToastForgotOther, 3); got != "Forgot 3 paired devices" {
		t.Errorf("three = %q", got)
	}
	if got := en.Count(KeyToastForgotOne, KeyToastForgotOther, 0); got != "Forgot 0 paired devices" {
		t.Errorf("zero = %q", got)
	}
	if got := zh.Count(KeyToastForgotOne, KeyToastForgotOther, 1); got != "已忘记 1 台已配对的设备" {
		t.Errorf("zh one = %q", got)
	}
}

func TestFormatReplacesNamedPlaceholders(t *testing.T) {
	en := New(English)
	if got := en.T(KeySessConnecting, "addr", "10.0.0.5:7000"); got != "Connecting to 10.0.0.5:7000…" {
		t.Errorf("got %q", got)
	}
	// Placeholders may be given in any order and numbers are formatted.
	if got := en.T(KeyPairLeft, "seconds", "05", "minutes", 1); got != "1:05 left" {
		t.Errorf("got %q", got)
	}
	// A placeholder with no value stays visible, so that a mistake can be seen and tested for.
	if got := en.T(KeySessConnecting); got != "Connecting to {addr}…" {
		t.Errorf("got %q", got)
	}
	// Extra values are ignored.
	if got := en.T(KeyCancel, "unused", 1); got != "Cancel" {
		t.Errorf("got %q", got)
	}
	if got := New(Korean).T(KeySessConnecting, "addr", "a"); got != "a에 연결하는 중…" {
		t.Errorf("ko = %q", got)
	}
	if got := en.T(Key(60000)); !strings.HasPrefix(got, "<key ") {
		t.Errorf("an unknown key gave %q", got)
	}
}

func TestMatchPicksTheFirstSupportedPreference(t *testing.T) {
	for _, c := range []struct {
		prefs []string
		want  Lang
	}{
		{[]string{"ko-KR", "en-US"}, Korean},
		{[]string{"zh-CN"}, Chinese},
		{[]string{"zh-Hans-CN", "en"}, Chinese},
		{[]string{"zh-TW"}, Chinese}, // until there is a Traditional table
		{[]string{"zh-HK", "en"}, Chinese},
		{[]string{"fr-FR", "ko"}, Korean},
		{[]string{"fr-FR", "de"}, English},
		{[]string{"en-GB", "ko"}, English},
		{nil, English},
		{[]string{""}, English},
	} {
		if got := Match(c.prefs); got != c.want {
			t.Errorf("Match(%v) = %v, want %v", c.prefs, got, c.want)
		}
	}
}

func TestParseAndTag(t *testing.T) {
	for _, l := range []Lang{English, Chinese, Korean, Pseudo} {
		got, ok := Parse(l.Tag())
		if !ok || got != l {
			t.Errorf("Parse(%q) = %v, %v", l.Tag(), got, ok)
		}
		if l.Name() == "" {
			t.Errorf("%v has no name", l)
		}
	}
	for in, want := range map[string]Lang{"ZH": Chinese, " zh-cn ": Chinese, "KO-kr": Korean, "en-us": English} {
		if got, ok := Parse(in); !ok || got != want {
			t.Errorf("Parse(%q) = %v, %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "auto", "fr", "zh-TW"} {
		if _, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) was accepted", bad)
		}
	}
}

// The pseudo-language grows every string and keeps its placeholders, so a layout that
// copes with it copes with a translation.
func TestPseudoLanguageGrowsStringsAndKeepsPlaceholders(t *testing.T) {
	p := New(Pseudo)
	for k := Key(0); k < numKeys; k++ {
		src := table[k].en
		got := p.raw(k)
		if strings.Join(Placeholders(got), ",") != strings.Join(Placeholders(src), ",") {
			t.Errorf("key %d: pseudo %q lost placeholders of %q", k, got, src)
		}
		letters := 0
		for _, r := range literal(src) {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if letters >= 8 && len([]rune(literal(got))) < len([]rune(literal(src)))*13/10 {
			t.Errorf("key %d: pseudo %q is hardly longer than %q", k, got, src)
		}
	}
}

func TestPlaceholdersListsNamesOnce(t *testing.T) {
	if got := strings.Join(Placeholders("{b} and {a} and {b} {"), ","); got != "a,b" {
		t.Errorf("got %q", got)
	}
	if len(Placeholders("no braces")) != 0 {
		t.Error("found placeholders in plain text")
	}
}

func TestTranslatorsOfOutOfRangeLanguageAreEnglish(t *testing.T) {
	if New(Lang(99)).Lang() != English {
		t.Error("an unknown language is not English")
	}
}

var _ = eachLang
