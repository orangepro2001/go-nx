// Package i18n is the interface's words in Simplified Chinese, English and Korean.
//
// Strings are keyed by typed constants (keys.go) and live in one table (strings.go) with
// the three languages side by side on each row, so that a translator or a reviewer reads
// them against each other and a language cannot fall out of step. Rules the table is
// held to, and the tests enforce:
//
//   - every key has a non-empty string in every language, and the same named
//     placeholders: {name}, {count}. Placeholders are named, never positional,
//     because Korean puts the words of a sentence in a different order from English.
//   - a sentence is one string; sentences are never put together from pieces. Korean
//     particles change with the sound of the word before them, so a template does not
//     write "{name}은(는)": the variable goes where no particle follows it.
//   - only English has a plural. A string that depends on a count has two keys, a
//     "one" and an "other", chosen with Translator.Count; Chinese and Korean give the
//     same text for both.
//   - logs and diagnostics stay English: they are for whoever investigates a problem.
package i18n

import (
	"fmt"
	"sort"
	"strings"
)

// Lang is an interface language.
type Lang uint8

const (
	English Lang = iota
	Chinese      // Simplified Chinese
	Korean
	// Pseudo is not a language: every English string, grown and mixed with Chinese and
	// Korean characters, to show what a layout does with the worst case.
	Pseudo
	numLangs
)

// Langs are the languages a person can choose, in the order the settings list them.
var Langs = []Lang{Chinese, English, Korean}

var tags = [numLangs]string{"en", "zh-Hans", "ko", "xx"}

// names are written in their own language, so a person can find theirs in any.
var names = [numLangs]string{"English", "简体中文", "한국어", "xx (pseudo)"}

// Tag is the BCP 47 tag, and what settings.json stores.
func (l Lang) Tag() string { return tags[l%numLangs] }

// Name is the language's name in itself.
func (l Lang) Name() string { return names[l%numLangs] }

// Parse reads a stored tag. "zh" and "zh-CN" mean Simplified Chinese.
func Parse(s string) (Lang, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "en", "en-us", "en-gb":
		return English, true
	case "zh", "zh-hans", "zh-cn", "zh-sg":
		return Chinese, true
	case "ko", "ko-kr":
		return Korean, true
	case "xx":
		return Pseudo, true
	}
	return English, false
}

// Match picks the interface language for a person whose system prefers prefs, most
// preferred first (as GetUserPreferredUILanguages returns them). Traditional Chinese
// (zh-TW, zh-HK, zh-MO) falls to Simplified until someone writes that table. Nothing
// matching is English.
func Match(prefs []string) Lang {
	for _, p := range prefs {
		p = strings.ToLower(p)
		primary, _, _ := strings.Cut(p, "-")
		switch primary {
		case "en":
			return English
		case "zh":
			return Chinese
		case "ko":
			return Korean
		}
	}
	return English
}

// Key names one string.
type Key uint16

// Translator looks strings up in one language.
type Translator struct{ lang Lang }

// New returns a Translator for l.
func New(l Lang) *Translator {
	if l >= numLangs {
		l = English
	}
	return &Translator{lang: l}
}

// Lang is the language of the Translator.
func (t *Translator) Lang() Lang { return t.lang }

// T is the string of k with its placeholders replaced. kv are name, value pairs:
// T(KeyConnecting, "addr", a). A placeholder with no value is left as it is, so a
// mistake shows on the screen and fails the tests that look for braces.
func (t *Translator) T(k Key, kv ...any) string {
	return format(t.raw(k), kv)
}

// Count is the "one" string for n == 1 in English and the "other" string otherwise
// (the other languages have the same text under both keys). {count} is n; further
// placeholders come from kv.
func (t *Translator) Count(one, other Key, n int, kv ...any) string {
	k := other
	if n == 1 && t.lang == English {
		k = one
	}
	return format(t.raw(k), append([]any{"count", n}, kv...))
}

func (t *Translator) raw(k Key) string {
	if int(k) >= int(numKeys) {
		return fmt.Sprintf("<key %d>", k)
	}
	e := table[k]
	switch t.lang {
	case Chinese:
		return e.zh
	case Korean:
		return e.ko
	case Pseudo:
		return pseudo(e.en)
	}
	return e.en
}

// format replaces {name} by the value after "name" in kv.
func format(s string, kv []any) string {
	if !strings.Contains(s, "{") {
		return s
	}
	vals := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		if name, ok := kv[i].(string); ok {
			vals[name] = fmt.Sprint(kv[i+1])
		}
	}
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			b.WriteString(s)
			break
		}
		end := strings.IndexByte(s[open:], '}')
		if end < 0 {
			b.WriteString(s)
			break
		}
		name := s[open+1 : open+end]
		b.WriteString(s[:open])
		if v, ok := vals[name]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(s[open : open+end+1])
		}
		s = s[open+end+1:]
	}
	return b.String()
}

// Placeholders are the names inside braces in s, sorted, each once.
func Placeholders(s string) []string {
	seen := map[string]bool{}
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(s[open:], '}')
		if end < 0 {
			break
		}
		seen[s[open+1:open+end]] = true
		s = s[open+end+1:]
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// pseudo grows an English string to about 1.6 times its length and mixes in Chinese and
// Korean characters, keeping placeholders intact. If a layout survives it, it survives
// a translation.
func pseudo(s string) string {
	const filler = "字漢語한국어"
	fill := []rune(filler)
	var b strings.Builder
	n := 0
	inBrace := false
	for _, r := range s {
		b.WriteRune(r)
		switch {
		case r == '{':
			inBrace = true
		case r == '}':
			inBrace = false
		case !inBrace && r != ' ':
			n++
			if n%2 == 0 {
				b.WriteRune(fill[(n/2)%len(fill)])
			}
		}
	}
	return b.String()
}
