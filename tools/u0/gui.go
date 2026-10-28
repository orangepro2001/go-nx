package main

import (
	"errors"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// processStart is when the spike began, for the time to the first picture.
var processStart = time.Now()

// runWindow shows a small window running g until g ends the loop with
// ebiten.Termination. The window is created unfocused so that it does not steal the
// keyboard from whoever started the spike.
func runWindow(title string, w, h int, g ebiten.Game) error {
	ebiten.SetWindowSize(w, h)
	ebiten.SetWindowTitle(title)
	err := ebiten.RunGameWithOptions(g, &ebiten.RunGameOptions{InitUnfocused: true})
	if errors.Is(err, ebiten.Termination) {
		return nil
	}
	return err
}

func graphicsLibrary() string {
	var d ebiten.DebugInfo
	ebiten.ReadDebugInfo(&d)
	return d.GraphicsLibrary.String()
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
