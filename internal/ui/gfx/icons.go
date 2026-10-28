package gfx

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/orangepro2001/go-nx/internal/ui/kit"
)

// The icons are drawn from a few lines and curves on a 24 by 24 grid, rasterised at the
// size they are used at (and the screen's scale) the first time they are asked for, and
// kept. They are white on transparent, so any colour can be tinted onto them. Nothing
// depends on a font having a glyph for a symbol.

const iconGrid = 24.0

type iconSpec struct {
	stroke func(p *vector.Path, f float32) // outlines, stroked 2 units wide with round ends
	fill   func(p *vector.Path, f float32) // solid shapes
}

func poly(p *vector.Path, f float32, closed bool, pts ...float32) {
	for i := 0; i+1 < len(pts); i += 2 {
		if i == 0 {
			p.MoveTo(pts[0]*f, pts[1]*f)
		} else {
			p.LineTo(pts[i]*f, pts[i+1]*f)
		}
	}
	if closed {
		p.Close()
	}
}

func line(p *vector.Path, f float32, x0, y0, x1, y1 float32) {
	p.MoveTo(x0*f, y0*f)
	p.LineTo(x1*f, y1*f)
}

func circle(p *vector.Path, f float32, cx, cy, r float32) {
	p.MoveTo((cx+r)*f, cy*f)
	p.Arc(cx*f, cy*f, r*f, 0, 2*math.Pi, vector.Clockwise)
	p.Close()
}

func rrect(p *vector.Path, f float32, x, y, w, h, r float32) {
	p.MoveTo((x+r)*f, y*f)
	p.LineTo((x+w-r)*f, y*f)
	p.Arc((x+w-r)*f, (y+r)*f, r*f, -math.Pi/2, 0, vector.Clockwise)
	p.LineTo((x+w)*f, (y+h-r)*f)
	p.Arc((x+w-r)*f, (y+h-r)*f, r*f, 0, math.Pi/2, vector.Clockwise)
	p.LineTo((x+r)*f, (y+h)*f)
	p.Arc((x+r)*f, (y+h-r)*f, r*f, math.Pi/2, math.Pi, vector.Clockwise)
	p.LineTo(x*f, (y+r)*f)
	p.Arc((x+r)*f, (y+r)*f, r*f, math.Pi, 3*math.Pi/2, vector.Clockwise)
	p.Close()
}

func star(p *vector.Path, f float32) {
	const cx, cy, ro, ri = 12.0, 12.5, 9.0, 3.9
	for i := 0; i < 10; i++ {
		r := float32(ro)
		if i%2 == 1 {
			r = ri
		}
		a := -math.Pi/2 + float64(i)*math.Pi/5
		x, y := cx+r*float32(math.Cos(a)), cy+r*float32(math.Sin(a))
		if i == 0 {
			p.MoveTo(x*f, y*f)
		} else {
			p.LineTo(x*f, y*f)
		}
	}
	p.Close()
}

func shield(p *vector.Path, f float32) {
	p.MoveTo(12*f, 3*f)
	p.LineTo(20*f, 6*f)
	p.LineTo(20*f, 12*f)
	p.QuadTo(20*f, 18.5*f, 12*f, 21.5*f)
	p.QuadTo(4*f, 18.5*f, 4*f, 12*f)
	p.LineTo(4*f, 6*f)
	p.Close()
}

func plug(p *vector.Path, f float32) {
	line(p, f, 9, 3, 9, 8)
	line(p, f, 15, 3, 15, 8)
	rrect(p, f, 6, 8, 12, 6, 2)
	line(p, f, 12, 14, 12, 21)
}

var icons = map[kit.Icon]iconSpec{
	kit.IconMonitor: {stroke: func(p *vector.Path, f float32) {
		rrect(p, f, 3, 4, 18, 12, 2)
		line(p, f, 8, 20, 16, 20)
		line(p, f, 12, 16, 12, 20)
	}},
	kit.IconPlus:       {stroke: func(p *vector.Path, f float32) { line(p, f, 12, 5, 12, 19); line(p, f, 5, 12, 19, 12) }},
	kit.IconClose:      {stroke: func(p *vector.Path, f float32) { line(p, f, 6, 6, 18, 18); line(p, f, 18, 6, 6, 18) }},
	kit.IconCheck:      {stroke: func(p *vector.Path, f float32) { poly(p, f, false, 5, 12.5, 10, 17.5, 19, 7) }},
	kit.IconStar:       {stroke: star},
	kit.IconStarFilled: {fill: star, stroke: star},
	kit.IconMore: {fill: func(p *vector.Path, f float32) {
		circle(p, f, 5, 12, 1.9)
		circle(p, f, 12, 12, 1.9)
		circle(p, f, 19, 12, 1.9)
	}},
	kit.IconSettings: {stroke: func(p *vector.Path, f float32) {
		circle(p, f, 12, 12, 3.2)
		circle(p, f, 12, 12, 7)
		for i := 0; i < 8; i++ {
			a := float64(i) * math.Pi / 4
			c, s := float32(math.Cos(a)), float32(math.Sin(a))
			line(p, f, 12+7*c, 12+7*s, 12+9.8*c, 12+9.8*s)
		}
	}},
	kit.IconLogs: {stroke: func(p *vector.Path, f float32) {
		line(p, f, 9, 7, 20, 7)
		line(p, f, 9, 12, 20, 12)
		line(p, f, 9, 17, 20, 17)
		line(p, f, 4, 7, 4.1, 7)
		line(p, f, 4, 12, 4.1, 12)
		line(p, f, 4, 17, 4.1, 17)
	}},
	kit.IconWarning: {stroke: func(p *vector.Path, f float32) {
		poly(p, f, true, 12, 4, 21.5, 20, 2.5, 20)
		line(p, f, 12, 10, 12, 14)
		line(p, f, 12, 17, 12, 17.1)
	}},
	kit.IconInfo: {stroke: func(p *vector.Path, f float32) {
		circle(p, f, 12, 12, 9)
		line(p, f, 12, 11, 12, 16.5)
		line(p, f, 12, 7.5, 12, 7.6)
	}},
	kit.IconCopy: {stroke: func(p *vector.Path, f float32) {
		rrect(p, f, 9, 9, 11, 11, 2)
		p.MoveTo(15*f, 9*f)
		p.LineTo(15*f, 6*f)
		p.Arc(13*f, 6*f, 2*f, 0, -math.Pi/2, vector.CounterClockwise)
		p.LineTo(6*f, 4*f)
		p.Arc(6*f, 6*f, 2*f, -math.Pi/2, -math.Pi, vector.CounterClockwise)
		p.LineTo(4*f, 13*f)
		p.Arc(6*f, 13*f, 2*f, math.Pi, math.Pi/2, vector.CounterClockwise)
		p.LineTo(9*f, 15*f)
	}},
	kit.IconPlug:   {stroke: plug},
	kit.IconUnplug: {stroke: func(p *vector.Path, f float32) { plug(p, f); line(p, f, 4, 4, 20, 20) }},
	kit.IconFullscreen: {stroke: func(p *vector.Path, f float32) {
		poly(p, f, false, 4, 9, 4, 4, 9, 4)
		poly(p, f, false, 15, 4, 20, 4, 20, 9)
		poly(p, f, false, 20, 15, 20, 20, 15, 20)
		poly(p, f, false, 9, 20, 4, 20, 4, 15)
	}},
	kit.IconKeyboard: {stroke: func(p *vector.Path, f float32) {
		rrect(p, f, 2.5, 6, 19, 12, 2)
		for _, x := range []float32{6.5, 10, 13.5, 17} {
			line(p, f, x, 10, x+0.1, 10)
		}
		line(p, f, 7, 14, 17, 14)
	}},
	kit.IconText: {stroke: func(p *vector.Path, f float32) {
		line(p, f, 5, 6, 19, 6)
		line(p, f, 12, 6, 12, 19)
		line(p, f, 9, 19, 15, 19)
	}},
	kit.IconRefresh: {stroke: func(p *vector.Path, f float32) {
		p.MoveTo(19.5*f, 12*f)
		p.Arc(12*f, 12*f, 7.5*f, 0, 1.5*math.Pi, vector.CounterClockwise)
		poly(p, f, false, 12, 1.7, 16, 4.5, 12, 7.3)
	}},
	kit.IconSearch: {stroke: func(p *vector.Path, f float32) {
		circle(p, f, 10.5, 10.5, 6)
		line(p, f, 15, 15, 20, 20)
	}},
	kit.IconShield: {stroke: func(p *vector.Path, f float32) {
		shield(p, f)
		poly(p, f, false, 8.5, 12.3, 11, 14.8, 15.8, 9.5)
	}},
	kit.IconShieldOpen: {stroke: func(p *vector.Path, f float32) {
		shield(p, f)
		line(p, f, 12, 8.5, 12, 13)
		line(p, f, 12, 16, 12, 16.1)
	}},
	kit.IconTrash: {stroke: func(p *vector.Path, f float32) {
		line(p, f, 4, 7, 20, 7)
		poly(p, f, false, 9, 7, 9, 4, 15, 4, 15, 7)
		poly(p, f, false, 6, 7, 7, 20, 17, 20, 18, 7)
		line(p, f, 10, 11, 10, 16)
		line(p, f, 14, 11, 14, 16)
	}},
	kit.IconEdit: {stroke: func(p *vector.Path, f float32) {
		poly(p, f, true, 4, 20, 4.8, 15.5, 16, 4.3, 19.7, 8, 8.5, 19.2)
		line(p, f, 14, 6.3, 17.7, 10)
	}},
	kit.IconChevronDown:  {stroke: func(p *vector.Path, f float32) { poly(p, f, false, 6, 9, 12, 15, 18, 9) }},
	kit.IconChevronRight: {stroke: func(p *vector.Path, f float32) { poly(p, f, false, 9, 6, 15, 12, 9, 18) }},
	kit.IconDownload: {stroke: func(p *vector.Path, f float32) {
		line(p, f, 12, 4, 12, 15)
		poly(p, f, false, 7, 10.5, 12, 15.5, 17, 10.5)
		line(p, f, 5, 20, 19, 20)
	}},
	kit.IconKeyRelease: {stroke: func(p *vector.Path, f float32) {
		rrect(p, f, 2.5, 6, 19, 13, 2)
		line(p, f, 12, 16, 12, 10)
		poly(p, f, false, 9, 12.5, 12, 9.5, 15, 12.5)
	}},
	kit.IconPower: {stroke: func(p *vector.Path, f float32) {
		// A circle open at the top, and the bar through the gap.
		p.MoveTo(7.4*f, 6.4*f)
		p.Arc(12*f, 12.5*f, 8*f, -2.2, -0.94, vector.CounterClockwise)
		line(p, f, 12, 3, 12, 11)
	}},
	kit.IconActualSize: {stroke: func(p *vector.Path, f float32) {
		rrect(p, f, 4, 4, 16, 16, 2)
		rrect(p, f, 9, 9, 6, 6, 1)
	}},
	kit.IconFit: {stroke: func(p *vector.Path, f float32) {
		rrect(p, f, 3, 5, 18, 14, 2)
		poly(p, f, false, 8, 10, 8, 8, 10, 8)
		poly(p, f, false, 14, 8, 16, 8, 16, 10)
		poly(p, f, false, 16, 14, 16, 16, 14, 16)
		poly(p, f, false, 10, 16, 8, 16, 8, 14)
	}},
	kit.IconUser: {stroke: func(p *vector.Path, f float32) {
		circle(p, f, 12, 8, 4)
		p.MoveTo(4*f, 20*f)
		p.Arc(12*f, 20*f, 8*f, math.Pi, 2*math.Pi, vector.Clockwise)
	}},
}

type iconKey struct {
	id kit.Icon
	px int
}

// iconImage returns the icon rasterised for a square of px device pixels.
func (c *Canvas) iconImage(id kit.Icon, px int) *ebiten.Image {
	k := iconKey{id, px}
	if img := c.icons[k]; img != nil {
		return img
	}
	spec, ok := icons[id]
	if !ok || px < 2 {
		return nil
	}
	img := ebiten.NewImage(px, px)
	f := float32(px) / iconGrid
	if spec.fill != nil {
		var p vector.Path
		spec.fill(&p, f)
		vector.FillPath(img, &p, nil, &vector.DrawPathOptions{AntiAlias: true})
	}
	if spec.stroke != nil {
		var p vector.Path
		spec.stroke(&p, f)
		w := 2 * f
		if w < 1.4 {
			w = 1.4
		}
		vector.StrokePath(img, &p, &vector.StrokeOptions{Width: w, LineCap: vector.LineCapRound, LineJoin: vector.LineJoinRound}, &vector.DrawPathOptions{AntiAlias: true})
	}
	c.icons[k] = img
	return img
}
