package main

import (
	"image"
	"slices"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// Dist is a distribution of durations in microseconds.
type Dist struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50us"`
	P99 float64 `json:"p99us"`
	Max float64 `json:"maxUs"`
}

func dist(d []time.Duration) Dist {
	if len(d) == 0 {
		return Dist{}
	}
	c := slices.Clone(d)
	slices.Sort(c)
	us := func(x time.Duration) float64 { return float64(x.Nanoseconds()) / 1000 }
	return Dist{N: len(c), P50: us(c[len(c)/2]), P99: us(c[min(len(c)-1, len(c)*99/100)]), Max: us(c[len(c)-1])}
}

// TileResult is what a tile and a picture cost to get into an image and onto the
// screen. "Submit" is how long the call takes; "synced" is the time until the GPU
// has really taken the pixels (forced by reading one back), per tile or per picture.
type TileResult struct {
	GraphicsLibrary string `json:"graphicsLibrary"`
	// ClockResolutionUS is the smallest step the timer showed. Where it is coarse
	// (Go on Windows reads a clock that moves every half a millisecond or more) the
	// per-call distributions below are quantised; the means, which time whole loops,
	// are not.
	ClockResolutionUS float64 `json:"clockResolutionUs"`
	TileSubmitMeanUS  float64 `json:"tileSubmitMeanUs"` // 2000 WritePixels of a 64x64 tile, call only, timed as one loop
	DrawImageMeanUS   float64 `json:"drawImageMeanUs"`  // 1000 DrawImage of the 1080p picture onto an image, call only
	TileSubmit        Dist    `json:"tileSubmit"`       // WritePixels of one 64x64 tile, call only
	TileSyncedUS      float64 `json:"tileSyncedUs"`     // mean per tile over batches of 100 that end in a readback
	Full1080Submit    Dist    `json:"full1080Submit"`   // WritePixels of a whole 1920x1080 picture
	Full1080Synced    float64 `json:"full1080SyncedMs"`
	Full2160Submit    Dist    `json:"full2160Submit"`
	Full2160Synced    float64 `json:"full2160SyncedMs"`
	DrawImage         Dist    `json:"drawImage1080"` // screen.DrawImage of the 1080p picture, call only
	Err               string  `json:"error,omitempty"`
}

type tilesGame struct {
	res   *TileResult
	tick  int
	draws []time.Duration
	big   *ebiten.Image
}

func (g *tilesGame) Layout(w, h int) (int, int) { return w, h }

func (g *tilesGame) Update() error {
	g.tick++
	switch {
	case g.tick == 2:
		g.measureUploads()
	case len(g.draws) >= 300:
		g.res.DrawImage = dist(g.draws)
		g.res.GraphicsLibrary = graphicsLibrary()
		return ebiten.Termination
	}
	return nil
}

func (g *tilesGame) Draw(screen *ebiten.Image) {
	if g.big == nil {
		return
	}
	t := time.Now()
	screen.DrawImage(g.big, nil)
	if g.tick > 3 {
		g.draws = append(g.draws, time.Since(t))
	}
}

// sync forces the GPU to take what was written, by reading one pixel back.
func sync(img *ebiten.Image) {
	var px [4]byte
	img.SubImage(image.Rect(0, 0, 1, 1)).(*ebiten.Image).ReadPixels(px[:])
}

func (g *tilesGame) measureUploads() {
	r := g.res
	big := ebiten.NewImage(1920, 1080)
	tile := make([]byte, 64*64*4)
	for i := range tile {
		tile[i] = byte(i * 7)
	}
	cols, rows := 1920/64, 1080/64
	at := func(i int) *ebiten.Image {
		x, y := (i%cols)*64, ((i/cols)%rows)*64
		return big.SubImage(image.Rect(x, y, x+64, y+64)).(*ebiten.Image)
	}
	for i := 0; i < 200; i++ { // warm up
		at(i).WritePixels(tile)
	}
	sync(big)

	r.ClockResolutionUS = clockResolution()
	var d []time.Duration
	loop := time.Now()
	for i := 0; i < 2000; i++ {
		t := time.Now()
		at(i).WritePixels(tile)
		d = append(d, time.Since(t))
	}
	r.TileSubmitMeanUS = float64(time.Since(loop).Nanoseconds()) / 1000 / 2000
	r.TileSubmit = dist(d)

	const batches, per = 20, 100
	t := time.Now()
	for b := 0; b < batches; b++ {
		for i := 0; i < per; i++ {
			at(b*per + i).WritePixels(tile)
		}
		sync(big)
	}
	r.TileSyncedUS = float64(time.Since(t).Nanoseconds()) / 1000 / float64(batches*per)

	full := func(w, h int) (Dist, float64) {
		img := ebiten.NewImage(w, h)
		px := make([]byte, 4*w*h)
		for i := range px {
			px[i] = byte(i)
		}
		img.WritePixels(px)
		sync(img)
		var ds []time.Duration
		for i := 0; i < 20; i++ {
			t := time.Now()
			img.WritePixels(px)
			ds = append(ds, time.Since(t))
		}
		t := time.Now()
		for i := 0; i < 10; i++ {
			img.WritePixels(px)
			sync(img)
		}
		return dist(ds), float64(time.Since(t).Microseconds()) / 1000 / 10
	}
	target := ebiten.NewImage(1920, 1080)
	t = time.Now()
	for i := 0; i < 1000; i++ {
		target.DrawImage(big, nil)
	}
	r.DrawImageMeanUS = float64(time.Since(t).Nanoseconds()) / 1000 / 1000
	sync(target)
	r.Full1080Submit, r.Full1080Synced = full(1920, 1080)
	r.Full2160Submit, r.Full2160Synced = full(3840, 2160)
	g.big = big
}

func runTiles() (*TileResult, error) {
	res := &TileResult{}
	if err := runWindow("go-nx u0 tiles", 640, 360, &tilesGame{res: res}); err != nil {
		res.Err = err.Error()
		return res, err
	}
	return res, nil
}

// clockResolution is the smallest nonzero step between two readings of the clock, in
// microseconds.
func clockResolution() float64 {
	best := time.Hour
	for i := 0; i < 20000; i++ {
		a := time.Now()
		b := time.Now()
		for b.Equal(a) {
			b = time.Now()
		}
		best = min(best, b.Sub(a))
	}
	return float64(best.Nanoseconds()) / 1000
}
