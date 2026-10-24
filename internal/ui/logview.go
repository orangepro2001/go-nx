package ui

import (
	"fmt"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
)

const (
	logBackX       = HomeW - 150
	logBackY       = 12
	logBackW       = 120
	logBackH       = 36
	logFirstLineY  = 132
	logLineHeight  = 23
	logLinesOnPage = (HomeH - logFirstLineY - 24) / logLineHeight
)

// LogsBackRect is the log viewer's return button, relative to the area below the tab bar.
func LogsBackRect() Rect { return Rect{logBackX, logBackY, logBackW, logBackH} }

// LogsVisibleLines is the number of log rows shown at once.
func LogsVisibleLines() int { return logLinesOnPage }

// DrawLogs renders a scrollable snapshot of recent application logs. scroll is
// measured in lines back from the newest entry (zero shows the latest lines).
func DrawLogs(dst *ebiten.Image, face *text.GoTextFace, lines []string, scroll int, path string, fileErr error, oy int) {
	t := func(s string, x, y int, c color.Color) { Text(dst, face, s, x, oy+y, c) }
	t("Application logs", 30, 20, ColText)
	Fill(dst, logBackX, oy+logBackY, logBackW, logBackH, ColBox)
	t("Back (Esc)", logBackX+9, logBackY+5, ColText)
	t("F3: open/close logs · Mouse wheel: scroll", 30, 58, ColDim)
	t(Truncate(face, "File: "+path, HomeW-60), 30, 88, ColDim)
	if fileErr != nil {
		t(Truncate(face, "File logging unavailable: "+fileErr.Error()+" (recent logs are still shown here)", HomeW-60), 30, 111, ColWarn)
	}
	if len(lines) == 0 {
		t("No log entries yet.", 30, logFirstLineY, ColDim)
		return
	}
	maxScroll := max(len(lines)-logLinesOnPage, 0)
	scroll = min(max(scroll, 0), maxScroll)
	end := len(lines) - scroll
	start := max(end-logLinesOnPage, 0)
	for i, line := range lines[start:end] {
		t(Truncate(face, line, HomeW-60), 30, logFirstLineY+i*logLineHeight, ColText)
	}
	if len(lines) > logLinesOnPage {
		first := max(start+1, 1)
		t(fmt.Sprintf("Showing %d-%d of %d lines", first, end, len(lines)), 30, HomeH-26, ColDim)
	}
}
