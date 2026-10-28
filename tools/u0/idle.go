package main

import (
	"fmt"
	"image/color"
	"slices"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// IdleResult is one way of drawing, left alone for a few seconds.
type IdleResult struct {
	Name          string  `json:"name"`
	Seconds       float64 `json:"seconds"`
	CPUPercent    float64 `json:"cpuPercentOfOneCore"` // process CPU time over wall time
	UpdatesPerSec float64 `json:"updatesPerSec"`
	DrawsPerSec   float64 `json:"drawsPerSec"`
	Note          string  `json:"note,omitempty"`
}

// ScheduleResult is what ScheduleFrame does in FPSModeVsyncOffMinimum.
type ScheduleResult struct {
	Scheduled       int     `json:"scheduled"`
	DrawsAfterEach  float64 `json:"meanDrawsAfterEach"` // 1 is the hoped-for answer
	MedianLatencyMS float64 `json:"medianLatencyMs"`
	MaxLatencyMS    float64 `json:"maxLatencyMs"`
	IdleDrawsPerSec float64 `json:"idleDrawsPerSec"`         // between schedules, nothing was asked for
	IdleCPUPercent  float64 `json:"idleCpuPercentOfOneCore"` // and what the process cost while it waited
	Works           bool    `json:"works"`
	Note            string  `json:"note,omitempty"`
}

// IdleReport is the whole check.
type IdleReport struct {
	GraphicsLibrary string         `json:"graphicsLibrary"`
	Strategies      []IdleResult   `json:"strategies"`
	Schedule        ScheduleResult `json:"scheduleFrame"`
	Err             string         `json:"error,omitempty"`
}

// idleGame runs the strategies one after the other in a single window.
type idleGame struct {
	seconds time.Duration
	rep     *IdleReport

	updates, draws atomic.Int64
	changed        bool // the picture needs drawing (for the skip-draw strategies)
	skip           bool // draw only when changed

	phase   int
	phaseAt time.Time
	cpuAt   time.Duration
	u0, d0  int64
	warm    bool
	names   []string
	setup   []func()
	started bool

	scheduling atomic.Bool // the ScheduleFrame check is running in its own goroutine
	finished   atomic.Bool // ... and has finished: end the game at the next Update
}

func (g *idleGame) Layout(w, h int) (int, int) { return w, h }

func (g *idleGame) Draw(screen *ebiten.Image) {
	g.draws.Add(1)
	if g.skip && !g.changed {
		return // the screen keeps what it had: SetScreenClearedEveryFrame(false)
	}
	g.changed = false
	screen.Fill(color.RGBA{24, 28, 36, 255})
	vector.DrawFilledRect(screen, 20, 20, 120, 60, color.RGBA{50, 128, 255, 255}, true)
}

func (g *idleGame) Update() error {
	g.updates.Add(1)
	if g.finished.Load() {
		return ebiten.Termination
	}
	if g.scheduling.Load() {
		return nil
	}
	if !g.started {
		g.started = true
		g.enter(0)
	}
	now := time.Now()
	// A second of settling after each switch, then the measured interval.
	if !g.warm {
		if now.Sub(g.phaseAt) >= time.Second {
			g.warm, g.phaseAt = true, now
			g.cpuAt, g.u0, g.d0 = cpuTime(), g.updates.Load(), g.draws.Load()
		}
		return nil
	}
	if elapsed := now.Sub(g.phaseAt); elapsed >= g.seconds {
		sec := elapsed.Seconds()
		g.rep.Strategies = append(g.rep.Strategies, IdleResult{
			Name: g.names[g.phase], Seconds: sec,
			CPUPercent:    float64(cpuTime()-g.cpuAt) / float64(elapsed) * 100,
			UpdatesPerSec: float64(g.updates.Load()-g.u0) / sec,
			DrawsPerSec:   float64(g.draws.Load()-g.d0) / sec,
		})
		g.enter(g.phase + 1)
	}
	return nil
}

// enter switches to strategy i, or starts the ScheduleFrame check after the last.
func (g *idleGame) enter(i int) {
	g.phase, g.warm, g.phaseAt = i, false, time.Now()
	g.changed = true
	if i < len(g.setup) {
		g.setup[i]()
		return
	}
	// In the minimum mode nothing runs unless something is asked for, so the check
	// cannot live in Update: it runs on its own goroutine and Update only ends the game.
	ebiten.SetScreenClearedEveryFrame(true)
	g.skip = false
	ebiten.SetTPS(ebiten.SyncWithFPS)
	ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum)
	g.scheduling.Store(true)
	go g.checkSchedule()
}

// checkSchedule asks ScheduleFrame for a frame eight times, and watches what the
// minimum mode does in between.
func (g *idleGame) checkSchedule() {
	sr := &g.rep.Schedule
	defer func() {
		g.finished.Store(true)
		ebiten.ScheduleFrame() // one more frame, so that Update runs and ends the game
	}()

	// Nothing has been asked for: how many frames does the mode run?
	time.Sleep(500 * time.Millisecond) // let the frames of the switch itself pass
	// Long enough for the CPU clock's granularity (Windows counts in 15.6 ms steps).
	wait := max(2*g.seconds, 4*time.Second)
	t0, d0, c0 := time.Now(), g.draws.Load(), cpuTime()
	time.Sleep(wait)
	elapsed := time.Since(t0)
	sr.IdleDrawsPerSec = float64(g.draws.Load()-d0) / elapsed.Seconds()
	sr.IdleCPUPercent = float64(cpuTime()-c0) / float64(elapsed) * 100

	var lat []time.Duration
	var drawsTotal int64
	for i := 0; i < 8; i++ {
		d0 := g.draws.Load()
		t := time.Now()
		ebiten.ScheduleFrame()
		deadline := time.Now().Add(3 * time.Second)
		for g.draws.Load() == d0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if g.draws.Load() == d0 {
			sr.Note = "ScheduleFrame did not produce a frame within 3 s"
			return
		}
		lat = append(lat, time.Since(t))
		time.Sleep(300 * time.Millisecond) // anything extra the request caused shows up now
		drawsTotal += g.draws.Load() - d0
	}
	sr.Scheduled = len(lat)
	sr.DrawsAfterEach = float64(drawsTotal) / float64(len(lat))
	sr.MedianLatencyMS = ms(median(lat))
	sr.MaxLatencyMS = ms(slices.Max(lat))
	sr.Works = sr.DrawsAfterEach >= 1 && sr.DrawsAfterEach <= 3 && sr.IdleDrawsPerSec < 5
}

func median(d []time.Duration) time.Duration {
	c := slices.Clone(d)
	slices.Sort(c)
	return c[len(c)/2]
}

func runIdle(seconds float64) (*IdleReport, error) {
	rep := &IdleReport{}
	g := &idleGame{seconds: time.Duration(seconds * float64(time.Second)), rep: rep}
	g.names = []string{
		"default: vsync, 60 TPS, redraw every frame",
		"skip draw: screen not cleared, Draw returns early, 60 TPS",
		"skip draw with 10 TPS",
	}
	g.setup = []func(){
		func() { ebiten.SetScreenClearedEveryFrame(true); g.skip = false; ebiten.SetTPS(60) },
		func() { ebiten.SetScreenClearedEveryFrame(false); g.skip = true; ebiten.SetTPS(60) },
		func() { ebiten.SetScreenClearedEveryFrame(false); g.skip = true; ebiten.SetTPS(10) },
	}
	if err := runWindow("go-nx u0 idle", 320, 200, g); err != nil {
		rep.Err = err.Error()
		return rep, err
	}
	rep.GraphicsLibrary = graphicsLibrary()
	return rep, nil
}

func (r IdleResult) String() string {
	return fmt.Sprintf("%-60s %5.1f%% of a core, %5.1f updates/s, %5.1f draws/s", r.Name, r.CPUPercent, r.UpdatesPerSec, r.DrawsPerSec)
}
