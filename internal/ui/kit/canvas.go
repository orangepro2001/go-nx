package kit

import (
	"fmt"
	"strings"

	"github.com/orangepro2001/go-nx/internal/ui/text"
	"github.com/orangepro2001/go-nx/internal/ui/theme"
)

// Color is a non-premultiplied colour.
type Color = theme.Color

// Icon names a symbol from the icon set. The set is drawn as vector paths by the
// Canvas of the running program, so it needs no font and no image file.
type Icon uint8

const (
	IconNone Icon = iota
	IconMonitor
	IconPlus
	IconClose
	IconCheck
	IconStar
	IconStarFilled
	IconMore
	IconSettings
	IconLogs
	IconWarning
	IconInfo
	IconCopy
	IconPlug
	IconUnplug
	IconFullscreen
	IconKeyboard
	IconText
	IconRefresh
	IconSearch
	IconShield
	IconShieldOpen
	IconTrash
	IconEdit
	IconChevronDown
	IconChevronRight
	IconDownload
	IconKeyRelease
	IconPower
	IconActualSize
	IconFit
	IconUser
	NumIcons
)

// Canvas is what the tree is painted onto. Coordinates are logical pixels. Shapes are
// drawn in the order they are asked for; PushClip limits everything drawn until the
// matching PopClip.
type Canvas interface {
	FillRect(r Rect, c Color)
	RoundRect(r Rect, radius float64, c Color)
	StrokeRoundRect(r Rect, radius, width float64, c Color)
	// Shadow draws a soft shadow of a rounded rectangle: dy below it, blurred over blur.
	Shadow(r Rect, radius, blur, dy float64, c Color)
	// Text draws one line of text with the top of its line box at y.
	Text(s string, x, y float64, st text.Style, c Color)
	Icon(id Icon, r Rect, c Color)
	PushClip(r Rect)
	PopClip()
}

// Op is one drawing command, as a Recorder keeps it.
type Op struct {
	Kind   string // "fill", "round", "stroke", "shadow", "text", "icon", "clip", "unclip"
	R      Rect
	Radius float64
	Width  float64
	Color  Color
	Text   string
	Style  text.Style
	Icon   Icon
}

// String is the op on one line, in a form that is stable enough to compare against a
// stored golden file.
func (o Op) String() string {
	c := fmt.Sprintf("#%02X%02X%02X", o.Color.R, o.Color.G, o.Color.B)
	if o.Color.A != 255 {
		c += fmt.Sprintf("/%d", o.Color.A)
	}
	r := fmt.Sprintf("%g,%g %gx%g", o.R.X, o.R.Y, o.R.W, o.R.H)
	switch o.Kind {
	case "fill":
		return fmt.Sprintf("fill %s %s", r, c)
	case "round":
		return fmt.Sprintf("round %s r=%g %s", r, o.Radius, c)
	case "stroke":
		return fmt.Sprintf("stroke %s r=%g w=%g %s", r, o.Radius, o.Width, c)
	case "shadow":
		return fmt.Sprintf("shadow %s r=%g blur=%g %s", r, o.Radius, o.Width, c)
	case "text":
		w := ""
		if o.Style.Weight == text.Semibold {
			w = " semibold"
		}
		return fmt.Sprintf("text %g,%g size=%g%s %q %s", o.R.X, o.R.Y, o.Style.Size, w, o.Text, c)
	case "icon":
		return fmt.Sprintf("icon %d %s %s", o.Icon, r, c)
	case "clip":
		return fmt.Sprintf("clip %s", r)
	}
	return o.Kind
}

// Recorder is a Canvas that keeps what it was asked to draw. Tests and the stored golden
// layouts use it.
type Recorder struct{ Ops []Op }

func (c *Recorder) FillRect(r Rect, col Color) {
	c.Ops = append(c.Ops, Op{Kind: "fill", R: r, Color: col})
}
func (c *Recorder) RoundRect(r Rect, radius float64, col Color) {
	c.Ops = append(c.Ops, Op{Kind: "round", R: r, Radius: radius, Color: col})
}
func (c *Recorder) StrokeRoundRect(r Rect, radius, width float64, col Color) {
	c.Ops = append(c.Ops, Op{Kind: "stroke", R: r, Radius: radius, Width: width, Color: col})
}
func (c *Recorder) Shadow(r Rect, radius, blur, dy float64, col Color) {
	r.Y += dy
	c.Ops = append(c.Ops, Op{Kind: "shadow", R: r, Radius: radius, Width: blur, Color: col})
}
func (c *Recorder) Text(s string, x, y float64, st text.Style, col Color) {
	c.Ops = append(c.Ops, Op{Kind: "text", R: Rect{X: x, Y: y}, Text: s, Style: st, Color: col})
}
func (c *Recorder) Icon(id Icon, r Rect, col Color) {
	c.Ops = append(c.Ops, Op{Kind: "icon", R: r, Icon: id, Color: col})
}
func (c *Recorder) PushClip(r Rect) { c.Ops = append(c.Ops, Op{Kind: "clip", R: r}) }
func (c *Recorder) PopClip()        { c.Ops = append(c.Ops, Op{Kind: "unclip"}) }

// Texts are the strings drawn, in order.
func (c *Recorder) Texts() []string {
	var out []string
	for _, o := range c.Ops {
		if o.Kind == "text" {
			out = append(out, o.Text)
		}
	}
	return out
}

// Text finds the first text op drawing exactly s.
func (c *Recorder) Text1(s string) (Op, bool) {
	for _, o := range c.Ops {
		if o.Kind == "text" && o.Text == s {
			return o, true
		}
	}
	return Op{}, false
}

// Dump is every op, one per line: the golden form of a screen.
func (c *Recorder) Dump() string {
	var b strings.Builder
	for _, o := range c.Ops {
		b.WriteString(o.String())
		b.WriteByte('\n')
	}
	return b.String()
}
