// Command uipreview renders the home tab and tab bar to a PNG so the layout can
// be checked without Windows. Run it under a virtual display:
//
//	xvfb-run -a go run ./tools/uipreview /tmp/ui.png
package main

import (
	"image/png"
	"log"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"go-nx/internal/profiles"
	"go-nx/internal/ui"
)

type preview struct {
	face *text.GoTextFace
	out  string
	done bool
}

func (p *preview) Update() error {
	if p.done {
		return ebiten.Termination
	}
	return nil
}

func (p *preview) Draw(screen *ebiten.Image) {
	screen.Fill(ui.ColBG)
	tabs := []ui.TabInfo{
		{Title: "Home"},
		{Title: "Line 1 - Pixel Inspector", State: ui.TabLive},
		{Title: "Press 3 (connecting)", State: ui.TabConnecting},
		{Title: "A very long name for the final inspection station", State: ui.TabError},
	}
	ui.DrawBar(screen, p.face, tabs, 0, 2, "Press 3", true, ui.HomeW)
	d := &ui.HomeData{
		Host: ui.HostInfo{
			Addrs: []string{"192.168.1.20", "10.0.0.7"}, Port: 7000, Paired: 2, ID: "9f3a1c77d0e24b58",
			Rejected: "192.168.1.55:50122 (not paired)",
		},
		Fields: [ui.NumFields]ui.Field{
			{Label: "My password", Value: "s3cret"},
			{Label: "Name (to save)", Value: "Line 4"},
			{Label: "Connect to", Value: "192.168.1.31"},
			{Label: "Password", Value: "hunter2", Mask: true},
		},
		Focus: ui.FAddr,
		Profiles: []profiles.Profile{
			{Name: "Line 1 - Pixel Inspector", Addr: "192.168.1.31:7000"},
			{Name: "Press 3", Addr: "192.168.1.40"},
			{Name: "Final inspection station with a long name", Addr: "factory-pc-17.local:7000"},
			{Name: "Office", Addr: "10.0.0.12"},
		},
		DelArmed: 1,
		Message:  "Disconnected: Another client took over this host",
		Caret:    true,
	}
	ui.DrawHome(screen, p.face, d, ui.BarHeight)
	pix := screen.Bounds()
	img := ebiten.NewImage(pix.Dx(), pix.Dy())
	img.DrawImage(screen, nil)
	f, err := os.Create(p.out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	rgba := make([]byte, 4*pix.Dx()*pix.Dy())
	screen.ReadPixels(rgba)
	out := newRGBA(pix.Dx(), pix.Dy(), rgba)
	if err := png.Encode(f, out); err != nil {
		log.Fatal(err)
	}
	p.done = true
}

func (p *preview) Layout(int, int) (int, int) { return ui.HomeW, ui.HomeH + ui.BarHeight }

func main() {
	out := "/tmp/ui.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	face, err := ui.NewFace()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(ui.HomeW, ui.HomeH+ui.BarHeight)
	go func() { time.Sleep(20 * time.Second); log.Fatal("preview timed out") }()
	if err := ebiten.RunGame(&preview{face: face, out: out}); err != nil {
		log.Fatal(err)
	}
}
