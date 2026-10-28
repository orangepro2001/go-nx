//go:build windows

package main

import (
	"github.com/orangepro2001/go-nx/internal/capture"
	"github.com/orangepro2001/go-nx/internal/hostd"
	"github.com/orangepro2001/go-nx/internal/inject"
)

// openReal opens the monitor named by display (see capture.Open) and an injector
// that follows it. The injector is pointed at the monitor again whenever the
// screen's size or place changes.
func openReal(display string, layered bool) (hostd.Backend, error) {
	capture.EnableDPIAwareness()
	scr, err := capture.Open(display)
	if err != nil {
		return hostd.Backend{}, err
	}
	scr.SetLayered(layered)
	w, h := scr.Size()
	inj := inject.New(w, h)
	retarget(inj, scr.Display())
	d := scr.Display()
	return hostd.Backend{
		Source: scr, Injector: inj, Name: d.Device, W: int(d.W), H: int(d.H), DPI: d.DPI,
		SourceKind: "gdi", InputKind: "sendinput",
		Retarget: func(int, int) { retarget(inj, scr.Display()) },
		Close:    scr.Close,
	}, nil
}

// retarget points the injector at monitor d. The primary monitor keeps the plain
// mapping; any other needs the virtual-desktop one, with its position in it.
func retarget(inj *inject.Injector, d capture.Display) {
	if d.Primary {
		inj.SetPrimary(int(d.W), int(d.H))
		return
	}
	vx, vy, vw, vh := capture.VirtualScreen()
	inj.SetDisplay(int(d.W), int(d.H), int(d.X), int(d.Y), vx, vy, vw, vh)
}
