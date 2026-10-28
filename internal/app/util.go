package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/profiles"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
)

// groupID writes a host ID in groups of four, "A1B2 C3D4 E5F6 7A8B", which is how it is
// read out and compared.
func groupID(id string) string {
	id = strings.ToUpper(id)
	var b strings.Builder
	for i, r := range id {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// groupCode writes a six-digit pairing code as "123 456"; any other string is unchanged.
func groupCode(c string) string {
	if len(c) == 6 {
		return c[:3] + " " + c[3:]
	}
	return c
}

// whenText says when t was, in the words of the interface language: "today 09:12",
// "yesterday 18:40", "3 days ago", or the date for anything older than a week.
func whenText(tr *i18n.Translator, now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	n, y := now, t.In(now.Location())
	t = y
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location()) }
	days := int(day(n).Sub(day(y)).Hours()/24 + 0.5)
	switch {
	case days <= 0:
		return tr.T(i18n.KeyTimeToday, "time", t.Format("15:04"))
	case days == 1:
		return tr.T(i18n.KeyTimeYesterday, "time", t.Format("15:04"))
	case days < 7:
		return tr.Count(i18n.KeyTimeDaysAgoOne, i18n.KeyTimeDaysAgoOther, days)
	}
	return t.Format("2006-01-02")
}

// SortMode orders the saved devices.
type SortMode string

const (
	SortRecent SortMode = "recent"
	SortName   SortMode = "name"
)

// visibleDevices is the indices into items of the devices to show: those matching query
// (a case-insensitive part of the name or the address), pinned ones first, then by most
// recently used or by name.
func visibleDevices(items []profiles.Profile, mode SortMode, query string) []int {
	query = strings.ToLower(strings.TrimSpace(query))
	var idx []int
	for i, p := range items {
		if query == "" || strings.Contains(strings.ToLower(p.Name), query) || strings.Contains(strings.ToLower(p.Addr), query) {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		pa, pb := items[idx[a]], items[idx[b]]
		if pa.Pinned != pb.Pinned {
			return pa.Pinned
		}
		if mode == SortRecent && pa.LastUsed != pb.LastUsed {
			return pa.LastUsed > pb.LastUsed
		}
		return strings.ToLower(pa.Name) < strings.ToLower(pb.Name)
	})
	return idx
}

// countdown is "1:42".
func countdown(d time.Duration) (minutes int, seconds string) {
	if d < 0 {
		d = 0
	}
	s := int((d + 999*time.Millisecond) / time.Second) // round up: 0:00 means it is over
	return s / 60, fmt.Sprintf("%02d", s%60)
}
