package app

import (
	"strconv"
	"testing"
	"time"

	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

func TestGroupIDAndCode(t *testing.T) {
	if got := groupID("a1b2c3d4e5f67a8b"); got != "A1B2 C3D4 E5F6 7A8B" {
		t.Errorf("groupID = %q", got)
	}
	if got := groupID(""); got != "" {
		t.Errorf("groupID of nothing = %q", got)
	}
	if got := groupCode("123456"); got != "123 456" {
		t.Errorf("groupCode = %q", got)
	}
	if got := groupCode("12"); got != "12" {
		t.Errorf("a short code changed: %q", got)
	}
}

func TestWhenText(t *testing.T) {
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	en, zh, ko := i18n.New(i18n.English), i18n.New(i18n.Chinese), i18n.New(i18n.Korean)
	for _, c := range []struct {
		t          time.Time
		en, zh, ko string
	}{
		{time.Date(2026, 10, 28, 9, 12, 0, 0, time.UTC), "today 09:12", "今天 09:12", "오늘 09:12"},
		{time.Date(2026, 10, 27, 23, 59, 0, 0, time.UTC), "yesterday 23:59", "昨天 23:59", "어제 23:59"},
		{time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC), "3 days ago", "3 天前", "3일 전"},
		{time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC), "6 days ago", "6 天前", "6일 전"},
		{time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC), "2026-10-21", "2026-10-21", "2026-10-21"},
		{time.Date(2026, 10, 28, 23, 0, 0, 0, time.UTC), "today 23:00", "今天 23:00", "오늘 23:00"}, // later today
	} {
		if got := whenText(en, now, c.t); got != c.en {
			t.Errorf("en %v = %q, want %q", c.t, got, c.en)
		}
		if got := whenText(zh, now, c.t); got != c.zh {
			t.Errorf("zh %v = %q, want %q", c.t, got, c.zh)
		}
		if got := whenText(ko, now, c.t); got != c.ko {
			t.Errorf("ko %v = %q, want %q", c.t, got, c.ko)
		}
	}
	if whenText(en, now, time.Time{}) != "" {
		t.Error("no time should give no text")
	}
	if got := whenText(en, now, now.AddDate(0, 0, -1)); got != "yesterday 15:00" {
		t.Errorf("exactly a day ago = %q", got)
	}
	one := now.AddDate(0, 0, -1).Add(-2 * time.Hour)
	if got := whenText(en, now, one); got != "yesterday 13:00" {
		t.Errorf("= %q", got)
	}
}

func TestVisibleDevicesPinsFirstThenSorts(t *testing.T) {
	items := []profiles.Profile{
		{Name: "Press 3", Addr: "10.0.0.3", LastUsed: 100},
		{Name: "Line 1", Addr: "10.0.0.1", LastUsed: 300},
		{Name: "office", Addr: "10.0.0.9"},
		{Name: "Zebra", Addr: "10.0.0.4", Pinned: true, LastUsed: 5},
		{Name: "Alpha", Addr: "factory-17", LastUsed: 200},
	}
	names := func(idx []int) string {
		s := ""
		for _, i := range idx {
			s += items[i].Name + ","
		}
		return s
	}
	if got := names(visibleDevices(items, SortRecent, "")); got != "Zebra,Line 1,Alpha,Press 3,office," {
		t.Errorf("recent: %s", got)
	}
	if got := names(visibleDevices(items, SortName, "")); got != "Zebra,Alpha,Line 1,office,Press 3," {
		t.Errorf("by name: %s", got)
	}
	if got := names(visibleDevices(items, SortRecent, "  LINE ")); got != "Line 1," {
		t.Errorf("search by name: %s", got)
	}
	if got := names(visibleDevices(items, SortRecent, "factory")); got != "Alpha," {
		t.Errorf("search by address: %s", got)
	}
	if got := visibleDevices(items, SortRecent, "nothing like this"); len(got) != 0 {
		t.Errorf("a query that matches nothing gave %v", got)
	}
	if got := visibleDevices(nil, SortName, ""); len(got) != 0 {
		t.Errorf("no devices gave %v", got)
	}
}

func TestCountdown(t *testing.T) {
	for d, want := range map[time.Duration]string{
		102 * time.Second:                      "1:42",
		119*time.Second + 400*time.Millisecond: "2:00",
		time.Second:                            "0:01",
		0:                                      "0:00",
		-5 * time.Second:                       "0:00",
		500 * time.Millisecond:                 "0:01",
	} {
		m, s := countdown(d)
		if got := strconv.Itoa(m) + ":" + s; got != want {
			t.Errorf("countdown(%v) = %s, want %s", d, got, want)
		}
	}
}

func TestParsePaired(t *testing.T) {
	loc := time.FixedZone("test", 9*3600)
	at := time.Date(2026, 10, 28, 9, 30, 0, 0, loc)
	for _, c := range []struct {
		in   string
		addr string
		when time.Time
	}{
		{"10.0.0.7:5000 2026-10-28 09:30", "10.0.0.7", at},
		{"10.0.0.7 2026-10-28 09:30", "10.0.0.7", at},
		{"[fe80::1]:5000 2026-10-28 09:30", "fe80::1", at},
		{"host.example:7000 2026-10-28 09:30", "host.example", at},
		{"10.0.0.7:5000", "10.0.0.7", time.Time{}},
		{"something unexpected", "something unexpected", time.Time{}},
		{"10.0.0.7:5000 yesterday at lunch", "10.0.0.7:5000 yesterday at lunch", time.Time{}},
		{"", "", time.Time{}},
	} {
		d := ParsePaired("fp", c.in, loc)
		if d.Fingerprint != "fp" || d.Addr != c.addr || !d.When.Equal(c.when) {
			t.Errorf("ParsePaired(%q) = %+v, want %q at %v", c.in, d, c.addr, c.when)
		}
	}
}
