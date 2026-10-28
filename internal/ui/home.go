package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"go-nx/internal/profiles"
)

// Home tab geometry, relative to the area below the tab bar.
const (
	HomeW = 1180
	HomeH = 700

	leftX    = 30
	fieldX   = 200
	fieldW   = 420
	fieldH   = 30
	chkY     = 168
	btnH     = 36
	btnW     = 260
	quitX    = 310
	quitW    = 300
	actionY  = 262
	forgetY  = 304
	forgetH  = 30
	connY    = 520
	saveX    = 210
	smallBtn = 160

	listX     = 680
	listW     = 470
	listTop   = 60
	rowH      = 42
	delW      = 72
	rowsShown = (HomeH - listTop - 30) / rowH
)

// SpacedCode writes a six-digit code as "123 456" so it is easy to read out and
// type; any other string is returned as it is.
func SpacedCode(c string) string {
	if len(c) == 6 {
		return c[:3] + " " + c[3:]
	}
	return c
}

// Fields on the home tab, in tab order.
const (
	FHostPw = iota // this PC's optional password
	FName          // name for the connection to open or save
	FAddr          // address to connect to
	FPeerPw        // password for that connection
	NumFields
)

var fieldY = [NumFields]int{214, 376, 420, 464}

// FieldRect is the input box of field i.
func FieldRect(i int) Rect { return Rect{fieldX, fieldY[i], fieldW, fieldH} }

// RowRect is the clickable area of saved connection row (0 = first visible).
func RowRect(row int) Rect { return Rect{listX, listTop + row*rowH, listW, rowH - 4} }

// Text of the mode button in each state, kept here so a test can check that they fit.
const (
	ModeLabelStrict = "Strict mode: verification required (click for Open)"
	ModeLabelOpen   = "OPEN mode: no verification! (click for Strict)"
	ModeLabelArmed  = "Click again: anyone will be able to control this PC"
	modePad         = 14
	modeTextOffset  = modePad + 8 // the text starts after the status dot
)

// ModeRect is the strict / open mode button under the host status.
func ModeRect() Rect { return Rect{leftX, chkY, 640, fieldH} }

// modeTextWidth is the room for the button's text.
func modeTextWidth() int { return ModeRect().W - modeTextOffset - modePad }

// updateW is the width of the update button, wide enough for "Restart to update to v0.10.12 (required)".
const updateW = 300

// UpdateRect is the home tab's update button, top right above the saved connections.
func UpdateRect() Rect { return Rect{HomeW - 30 - updateW, 10, updateW, 34} }

// LogsRect is the home tab's log-viewer button.
func LogsRect() Rect { return Rect{HomeW - 150, 650, 120, 36} }

// DeleteRect is the delete button inside a row.
func DeleteRect(row int) Rect {
	r := RowRect(row)
	return Rect{r.X + r.W - delW, r.Y, delW, r.H}
}

// VisibleRows is how many saved connections fit on screen.
func VisibleRows() int { return rowsShown }

// HitKind says what a click on the home tab landed on.
type HitKind int

const (
	HitNone HitKind = iota
	HitMode         // strict / open mode button
	HitPair
	HitQuit
	HitForget // forget every paired device
	HitField  // Index: field
	HitConnect
	HitSave
	HitProfile // Index: profile index (scroll already applied)
	HitDelete  // Index: profile index
	HitLogs
	HitUpdate // check for an update, or restart into one that is ready
)

type Hit struct {
	Kind  HitKind
	Index int
}

// HomeHit finds what is at (x, y), relative to the area below the tab bar.
func HomeHit(x, y, nProfiles, scroll int) Hit {
	if LogsRect().Contains(x, y) {
		return Hit{Kind: HitLogs}
	}
	if UpdateRect().Contains(x, y) {
		return Hit{Kind: HitUpdate}
	}
	switch {
	case ModeRect().Contains(x, y):
		return Hit{Kind: HitMode}
	case Rect{leftX, actionY, btnW, btnH}.Contains(x, y):
		return Hit{Kind: HitPair}
	case Rect{quitX, actionY, quitW, btnH}.Contains(x, y):
		return Hit{Kind: HitQuit}
	case Rect{leftX, forgetY, btnW, forgetH}.Contains(x, y):
		return Hit{Kind: HitForget}
	case Rect{leftX, connY, smallBtn, btnH}.Contains(x, y):
		return Hit{Kind: HitConnect}
	case Rect{saveX, connY, smallBtn, btnH}.Contains(x, y):
		return Hit{Kind: HitSave}
	}
	for i := 0; i < NumFields; i++ {
		if FieldRect(i).Contains(x, y) {
			return Hit{HitField, i}
		}
	}
	for row := 0; row < rowsShown && scroll+row < nProfiles; row++ {
		if DeleteRect(row).Contains(x, y) {
			return Hit{HitDelete, scroll + row}
		}
		if RowRect(row).Contains(x, y) {
			return Hit{HitProfile, scroll + row}
		}
	}
	return Hit{}
}

// HostInfo is what the home tab shows about the hosting side.
type HostInfo struct {
	Err         string
	Addrs       []string
	Port        int
	Paired      int
	ID          string
	Controller  string
	Rejected    string
	PairingLeft time.Duration
	PairingCode string // the code a new device enters as its password; "" when pairing is closed
	RequireAuth bool
}

// Field is one text input.
type Field struct {
	Label string
	Value string
	Mask  bool
}

// HomeData is everything DrawHome needs.
type HomeData struct {
	Host        HostInfo
	Fields      [NumFields]Field
	Focus       int
	Profiles    []profiles.Profile
	Scroll      int
	DelArmed    int    // profile index awaiting a second click to delete, or -1
	ForgetArmed bool   // "forget paired devices" awaiting a second click
	ModeArmed   bool   // switching to open mode is awaiting a second click
	Version     string // this build, e.g. "v0.2.0"
	UpdateLabel string // text of the update button; "" when this build has no updater
	UpdateReady bool   // a verified update is waiting, so the button is highlighted
	Message     string
	Caret       bool
}

// DrawHome draws the home tab with its top edge at y = oy.
func DrawHome(dst *ebiten.Image, face *text.GoTextFace, d *HomeData, oy int) {
	t := func(s string, x, y int, c color.Color) { Text(dst, face, s, x, oy+y, c) }
	h := d.Host
	t("This PC (others can control it)", leftX, 20, ColText)
	if d.Version != "" {
		t(d.Version, leftX+int(Width(face, "This PC (others can control it)"))+24, 20, ColDim)
	}
	if d.UpdateLabel != "" {
		u, bg := UpdateRect(), ColBox
		if d.UpdateReady {
			bg = ColHot
		}
		Fill(dst, u.X, oy+u.Y, u.W, u.H, bg)
		t(Truncate(face, d.UpdateLabel, float64(u.W-24)), u.X+12, u.Y+3, ColText)
	}
	if h.Err != "" {
		for i, line := range Wrap(face, "Hosting unavailable: "+h.Err, 630) {
			if i == 3 {
				break
			}
			t(line, leftX, 54+i*34, ColWarn)
		}
	} else {
		list := "unknown"
		if len(h.Addrs) > 0 {
			list = strings.Join(h.Addrs, ", ")
		}
		t(Truncate(face, fmt.Sprintf("Address: %s   (UDP %d)", list, h.Port), 630), leftX, 54, ColDim)
		t(fmt.Sprintf("Paired devices: %d      ID: %s", h.Paired, h.ID), leftX, 88, ColDim)
		switch {
		case h.Controller != "":
			t("Being controlled by "+h.Controller, leftX, 122, ColWarn)
		case h.PairingLeft > 0:
			t(Truncate(face, fmt.Sprintf("Pairing open %d:%02d - code %s: enter it as the Password on the new device",
				int(h.PairingLeft.Minutes()), int(h.PairingLeft.Seconds())%60, SpacedCode(h.PairingCode)), 630), leftX, 122, ColWarn)
		case h.Rejected != "":
			t(Truncate(face, "Refused "+h.Rejected, 630), leftX, 122, ColWarn)
		default:
			t("Not being controlled", leftX, 122, ColDim)
		}
	}

	// Strict / open mode button. Strict is the safe state; opening up needs a second click.
	m, bg, label := ModeRect(), ColBox, ModeLabelStrict
	switch {
	case d.ModeArmed:
		bg, label = ColDanger, ModeLabelArmed
	case !h.RequireAuth:
		bg, label = ColDanger, ModeLabelOpen
	}
	Fill(dst, m.X, oy+m.Y, m.W, m.H, bg)
	if h.RequireAuth && !d.ModeArmed {
		Fill(dst, m.X+modePad-4, oy+m.Y+m.H/2-4, 8, 8, ColGood) // green dot: verifying
	}
	t(Truncate(face, label, float64(modeTextWidth())), m.X+modeTextOffset, m.Y+1, ColText)

	drawField(dst, face, d, FHostPw, oy)
	Fill(dst, leftX, oy+actionY, btnW, btnH, ColBox)
	t("Pair new device (2 min)", leftX+14, actionY+4, ColText)
	Fill(dst, quitX, oy+actionY, quitW, btnH, ColDanger)
	t("Quit go-nx (stop sharing)", quitX+14, actionY+4, ColText)
	if d.ForgetArmed {
		Fill(dst, leftX, oy+forgetY, btnW, forgetH, ColDanger)
		t(Truncate(face, fmt.Sprintf("Sure? Forget %d device(s)", h.Paired), btnW-20), leftX+10, forgetY+1, ColText)
	} else {
		Fill(dst, leftX, oy+forgetY, btnW, forgetH, ColBox)
		t("Forget paired devices", leftX+10, forgetY+1, ColDim)
	}

	t("Control another PC", leftX, 340, ColText)
	for i := FName; i < NumFields; i++ {
		drawField(dst, face, d, i, oy)
	}
	Fill(dst, leftX, oy+connY, smallBtn, btnH, ColHot)
	t("Connect", leftX+38, connY+4, ColText)
	Fill(dst, saveX, oy+connY, smallBtn, btnH, ColBox)
	t("Save", saveX+52, connY+4, ColText)
	t("Enter: next field, connects on the last one.   Tab: next field   F3: view logs", leftX, 572, ColDim)
	t("Closing the window keeps go-nx running in the tray; Quit stops it.", leftX, 602, ColDim)
	Fill(dst, LogsRect().X, oy+LogsRect().Y, LogsRect().W, LogsRect().H, ColBox)
	t("View logs", LogsRect().X+21, LogsRect().Y+5, ColText)
	for i, line := range Wrap(face, d.Message, 620) {
		if i == 2 {
			break
		}
		t(line, leftX, 640+i*28, ColWarn)
	}

	// Saved connections.
	t("Saved connections", listX, 20, ColText)
	if len(d.Profiles) == 0 {
		for i, line := range Wrap(face, "None yet. Fill in the form and click Save; saved connections open with one click.", listW) {
			t(line, listX, listTop+i*28, ColDim)
		}
	}
	for row := 0; row < rowsShown && d.Scroll+row < len(d.Profiles); row++ {
		p := d.Profiles[d.Scroll+row]
		r := RowRect(row)
		Fill(dst, r.X, oy+r.Y, r.W, r.H, ColBox)
		t(Truncate(face, p.Name, 200), r.X+12, r.Y+6, ColText)
		t(Truncate(face, p.Addr, 170), r.X+226, r.Y+6, ColDim)
		dr := DeleteRect(row)
		if d.DelArmed == d.Scroll+row {
			Fill(dst, dr.X, oy+dr.Y, dr.W, dr.H, ColDanger)
			t("Sure?", dr.X+8, dr.Y+6, ColText)
		} else {
			t("×", dr.X+delW/2-6, dr.Y+5, ColDim)
		}
	}
	if len(d.Profiles) > rowsShown {
		t(fmt.Sprintf("%d-%d of %d (mouse wheel to scroll)", d.Scroll+1,
			min(d.Scroll+rowsShown, len(d.Profiles)), len(d.Profiles)), listX, HomeH-26, ColDim)
	}
}

func drawField(dst *ebiten.Image, face *text.GoTextFace, d *HomeData, i, oy int) {
	f := d.Fields[i]
	r := FieldRect(i)
	Text(dst, face, f.Label, leftX, oy+r.Y+2, ColDim)
	border := ColBox
	if i == d.Focus {
		border = ColHot
	}
	Fill(dst, r.X-2, oy+r.Y-2, r.W+4, r.H+4, border)
	Fill(dst, r.X, oy+r.Y, r.W, r.H, ColInput)
	s := f.Value
	if f.Mask {
		s = strings.Repeat("*", utf8.RuneCountInString(s))
	}
	if i == d.Focus && d.Caret {
		s += "|"
	}
	// Show the end of long values so the caret stays visible.
	for Width(face, s) > float64(r.W-16) && len(s) > 1 {
		_, n := utf8.DecodeRuneInString(s)
		s = s[n:]
	}
	Text(dst, face, s, r.X+8, oy+r.Y+2, ColText)
}
