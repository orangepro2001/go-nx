//go:build windows

package main

import (
	"strings"
	"testing"
)

// These run on Windows only (CI's runner): the system calls behind the clipboard, the theme
// and the language have nothing to be tested against anywhere else.

func TestClipboardRoundTrip(t *testing.T) {
	c := winClipboard{}
	if !openClipboard() {
		t.Skip("the clipboard cannot be opened in this session")
	}
	pCloseClipboard.Call()
	for _, s := range []string{"hello", "你好，世界", "한국어 텍스트", "emoji 😀 and a surrogate pair", "line one\r\nline two", strings.Repeat("long ", 5000)} {
		c.Set(s)
		if got := c.Get(); got != s {
			t.Errorf("set %q, got %q", s[:min(len(s), 30)], got[:min(len(got), 30)])
		}
	}
	c.Set("")
	if got := c.Get(); got != "" {
		t.Errorf("an empty string came back as %q", got)
	}
}

func TestSystemQueriesAnswerSomething(t *testing.T) {
	p := winPlatform{}
	langs := p.PreferredLanguages()
	if len(langs) == 0 {
		t.Error("no preferred languages: the interface language could not follow the system")
	}
	for _, l := range langs {
		if l == "" || !strings.ContainsAny(l, "abcdefghijklmnopqrstuvwxyz") {
			t.Errorf("a preferred language is %q", l)
		}
	}
	dark, ok := p.SystemDark()
	t.Logf("system theme: dark=%v known=%v; languages %v", dark, ok, langs)
}
