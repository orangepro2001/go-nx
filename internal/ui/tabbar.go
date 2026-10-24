package ui

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
)

const (
	BarHeight = 36
	homeTabW  = 110
	tabGap    = 2
	minTabW   = 90
	maxTabW   = 230
	closeW    = 30
)

// TabState is what a session tab is doing.
type TabState int

const (
	TabLive TabState = iota
	TabConnecting
	TabError
)

// TabInfo describes one tab. Tab 0 is always the home tab.
type TabInfo struct {
	Title string
	State TabState
}

// TabRects lays out n tabs (home plus sessions) in a bar width pixels wide.
func TabRects(n, width int) []Rect {
	rects := make([]Rect, n)
	if n == 0 {
		return rects
	}
	rects[0] = Rect{0, 0, homeTabW, BarHeight}
	if m := n - 1; m > 0 {
		avail := width - homeTabW - tabGap
		w := min(maxTabW, max(minTabW, avail/m-tabGap))
		for i := 1; i < n; i++ {
			rects[i] = Rect{homeTabW + tabGap + (i-1)*(w+tabGap), 0, w, BarHeight}
		}
	}
	return rects
}

// CloseRect is the clickable "x" area at the right end of a session tab.
func CloseRect(t Rect) Rect { return Rect{t.X + t.W - closeW, t.Y, closeW, t.H} }

// TabAt returns the tab under (x, y), and whether the point is on its close
// button. idx is -1 when no tab is hit. The home tab has no close button.
func TabAt(n, width, x, y int) (idx int, onClose bool) {
	for i, r := range TabRects(n, width) {
		if r.Contains(x, y) {
			return i, i > 0 && CloseRect(r).Contains(x, y)
		}
	}
	return -1, false
}

// DrawBar draws the tab bar. If renaming >= 1, that tab shows an edit box
// containing renameText instead of its title.
func DrawBar(dst *ebiten.Image, face *text.GoTextFace, tabs []TabInfo, active, renaming int, renameText string, caret bool, width int) {
	Fill(dst, 0, 0, width, BarHeight, ColInput)
	for i, r := range TabRects(len(tabs), width) {
		bg := ColBG
		if i == active {
			bg = ColBox
		}
		Fill(dst, r.X, r.Y+4, r.W, r.H-4, bg)
		if i == active {
			Fill(dst, r.X, r.Y+4, r.W, 3, ColHot)
		}
		tx := r.X + 12
		if i == 0 {
			Text(dst, face, "Home", tx, r.Y+8, ColText)
			continue
		}
		c := ColGood
		switch tabs[i].State {
		case TabConnecting:
			c = ColWarn
		case TabError:
			c = ColBad
		}
		Fill(dst, tx, r.Y+17, 10, 10, c)
		tx += 20
		if i == renaming {
			Fill(dst, tx-4, r.Y+8, r.W-closeW-20, r.H-12, ColInput)
			s := Truncate(face, renameText, float64(r.W-closeW-34))
			if caret {
				s += "|"
			}
			Text(dst, face, s, tx, r.Y+8, ColText)
		} else {
			Text(dst, face, Truncate(face, tabs[i].Title, float64(r.W-closeW-34)), tx, r.Y+8, ColText)
		}
		Text(dst, face, "×", CloseRect(r).X+8, r.Y+7, ColDim)
	}
}
