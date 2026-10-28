package ui

import (
	"testing"

	"go-nx/internal/profiles"
)

func TestTabRectsDoNotOverlapAndFit(t *testing.T) {
	for _, n := range []int{1, 2, 5, 12} {
		rects := TabRects(n, 1180)
		if len(rects) != n {
			t.Fatalf("n=%d: got %d rects", n, len(rects))
		}
		for i := 1; i < n; i++ {
			if rects[i].X < rects[i-1].X+rects[i-1].W {
				t.Errorf("n=%d: tab %d overlaps tab %d", n, i, i-1)
			}
		}
		if n <= 5 {
			last := rects[n-1]
			if last.X+last.W > 1180 {
				t.Errorf("n=%d: last tab ends at %d, past the bar", n, last.X+last.W)
			}
		}
	}
}

func TestTabAtAndClose(t *testing.T) {
	rects := TabRects(3, 1180)
	if i, c := TabAt(3, 1180, 20, 10); i != 0 || c {
		t.Fatalf("home tab = %d,%v", i, c)
	}
	s1 := rects[1]
	if i, c := TabAt(3, 1180, s1.X+20, 10); i != 1 || c {
		t.Fatalf("session body = %d,%v", i, c)
	}
	cr := CloseRect(s1)
	if i, c := TabAt(3, 1180, cr.X+5, 10); i != 1 || !c {
		t.Fatalf("close button = %d,%v", i, c)
	}
	if i, _ := TabAt(3, 1180, 1100, 10); i != -1 {
		t.Fatalf("empty bar hit tab %d", i)
	}
	if i, _ := TabAt(3, 1180, 20, BarHeight+5); i != -1 {
		t.Fatalf("below the bar hit tab %d", i)
	}
}

func TestHomeHit(t *testing.T) {
	cases := []struct {
		name string
		x, y int
		want Hit
	}{
		{"checkbox", 40, chkY + 5, Hit{Kind: HitCheckbox}},
		{"pair", 100, actionY + 5, Hit{Kind: HitPair}},
		{"quit", quitX + 10, actionY + 5, Hit{Kind: HitQuit}},
		{"forget devices", leftX + 10, forgetY + 5, Hit{Kind: HitForget}},
		{"connect", 60, connY + 5, Hit{Kind: HitConnect}},
		{"save", saveX + 60, connY + 5, Hit{Kind: HitSave}},
		{"host password", fieldX + 5, fieldY[FHostPw] + 5, Hit{HitField, FHostPw}},
		{"address", fieldX + 5, fieldY[FAddr] + 5, Hit{HitField, FAddr}},
		{"first row", listX + 20, listTop + 5, Hit{HitProfile, 3}},
		{"first row delete", listX + listW - 10, listTop + 5, Hit{HitDelete, 3}},
		{"second row", listX + 20, listTop + rowH + 5, Hit{HitProfile, 4}},
		{"view logs", LogsRect().X + 5, LogsRect().Y + 5, Hit{Kind: HitLogs}},
		{"nothing", 5, 5, Hit{}},
	}
	for _, c := range cases {
		if got := HomeHit(c.x, c.y, 10, 3); got != c.want {
			t.Errorf("%s: HomeHit(%d,%d) = %+v, want %+v", c.name, c.x, c.y, got, c.want)
		}
	}
	if got := HomeHit(listX+20, listTop+5, 0, 0); got.Kind != HitNone {
		t.Errorf("empty list row hit %+v", got)
	}
}

func TestLayoutHasNoOverlaps(t *testing.T) {
	// Interactive areas that share a point would make one of them unreachable.
	var rects []Rect
	for i := 0; i < NumFields; i++ {
		rects = append(rects, FieldRect(i))
	}
	rects = append(rects, Rect{leftX, chkY, 640, fieldH}, Rect{leftX, actionY, btnW, btnH},
		Rect{quitX, actionY, quitW, btnH}, Rect{leftX, forgetY, btnW, forgetH}, Rect{leftX, connY, smallBtn, btnH}, Rect{saveX, connY, smallBtn, btnH})
	for i, a := range rects {
		for j, b := range rects[i+1:] {
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Errorf("areas %d and %d overlap: %+v %+v", i, i+1+j, a, b)
			}
		}
		if a.X+a.W > listX-10 {
			t.Errorf("area %d runs into the saved-connections column: %+v", i, a)
		}
	}
	if last := RowRect(VisibleRows() - 1); last.Y+last.H > HomeH {
		t.Errorf("last visible row ends at %d, past HomeH %d", last.Y+last.H, HomeH)
	}
}

func TestTruncateAndWrap(t *testing.T) {
	face, err := NewFace()
	if err != nil {
		t.Fatal(err)
	}
	long := "A very long saved connection name for a production line"
	got := Truncate(face, long, 200)
	if Width(face, got) > 200 || got == long || got[len(got)-3:] != "…" {
		t.Fatalf("Truncate = %q (%.0fpx)", got, Width(face, got))
	}
	if s := "short"; Truncate(face, s, 200) != s {
		t.Fatal("short text was changed")
	}
	for _, line := range Wrap(face, long+" "+long, 300) {
		if Width(face, line) > 300 {
			t.Errorf("wrapped line too wide: %q", line)
		}
	}
	if Wrap(face, "", 300) != nil {
		t.Fatal("empty text produced lines")
	}
	_ = profiles.Profile{}
}

func TestHostErrorFitsAboveCheckbox(t *testing.T) {
	face, err := NewFace()
	if err != nil {
		t.Fatal(err)
	}
	msg := "Hosting unavailable: UDP port 7000 is in use by loopback.exe (process 12345). " +
		"End the older go-nx / host.exe in Task Manager, or start with -port."
	if n := len(Wrap(face, msg, 630)); n > 3 {
		t.Fatalf("the message needs %d lines; only 3 fit above the checkbox", n)
	}
}
