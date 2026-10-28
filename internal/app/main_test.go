package app

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// A few tests draw for real (the remote picture is an Ebiten image), which needs a window
// and a GPU or Mesa's software one. Ebiten wants its loop on the main thread, so with a
// display the tests run on another goroutine and hand the drawing ones a way into the loop,
// as in package gfx. Without a display (a bare shell on Linux) those tests skip.

var (
	gpuJobs = make(chan func(*ebiten.Image))
	haveGPU = true
)

type gpuLoop struct{}

func (gpuLoop) Update() error { return nil }
func (gpuLoop) Draw(screen *ebiten.Image) {
	for {
		select {
		case f := <-gpuJobs:
			f(screen)
		default:
			return
		}
	}
}
func (gpuLoop) Layout(int, int) (int, int) { return 320, 240 }

func TestMain(m *testing.M) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		haveGPU = false
		os.Exit(m.Run())
	}
	go func() { os.Exit(m.Run()) }()
	ebiten.SetWindowSize(320, 240)
	if err := ebiten.RunGame(gpuLoop{}); err != nil {
		os.Stderr.WriteString("ebiten: " + err.Error() + "\n")
		os.Exit(3)
	}
}

// onGPU runs f in the game loop and waits for it.
func onGPU(t *testing.T, f func(screen *ebiten.Image)) {
	t.Helper()
	if !haveGPU {
		t.Skip("no display: run under xvfb-run")
	}
	done := make(chan struct{})
	select {
	case gpuJobs <- func(s *ebiten.Image) { defer close(done); f(s) }:
	case <-time.After(20 * time.Second):
		t.Fatal("the game loop did not take the job")
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the job did not finish")
	}
}
