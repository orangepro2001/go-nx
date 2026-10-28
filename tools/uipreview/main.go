// Command uipreview draws every screen of the interface, in each language and theme, to
// PNG files, with the real fonts of the machine it runs on, so the look of the program can
// be checked without Windows and the result of a change can be seen. It runs the same
// application code as the program, with made-up data (app.Preview).
//
//	xvfb-run -a go run ./tools/uipreview -out /tmp/ui                   # Linux
//	go run ./tools/uipreview -out ui                                   # Windows
//	... -scenes this-pc,settings -lang ko -theme dark -scale 1.5 -size 1100x740
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/orangepro2001/go-nx/internal/app"
	"github.com/orangepro2001/go-nx/internal/ui/gfx"
	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/text"
)

type job struct {
	scene string
	lang  i18n.Lang
	dark  bool
	scale float64
}

type previewGame struct {
	out    string
	w, h   int
	jobs   []job
	next   int
	eng    *text.Engine
	cv     *gfx.Canvas
	img    *ebiten.Image
	engFor i18n.Lang
	files  []string
	failed bool
	start  time.Time
}

func (g *previewGame) Update() error {
	if g.next >= len(g.jobs) || g.failed {
		return ebiten.Termination
	}
	if time.Since(g.start) > 5*time.Minute {
		g.failed = true
		return fmt.Errorf("timed out")
	}
	return nil
}

func (g *previewGame) Layout(int, int) (int, int) { return 64, 64 }

func (g *previewGame) Draw(*ebiten.Image) {
	if g.next >= len(g.jobs) {
		return
	}
	j := g.jobs[g.next]
	if g.eng == nil || g.engFor != j.lang {
		if g.eng != nil {
			g.cv.Close()
			g.eng.Close()
		}
		e, err := text.NewEngine(text.SystemLocator(), nil)
		if err != nil {
			log.Print(err)
			g.failed = true
			return
		}
		cv, err := gfx.New(e)
		if err != nil {
			log.Print(err)
			g.failed = true
			return
		}
		e.SetLanguage(j.lang)
		g.eng, g.cv, g.engFor = e, cv, j.lang
	}
	if !g.eng.Ready() {
		return // the fonts are opening; try again next frame
	}
	g.eng.SetScale(j.scale)
	pw, ph := int(float64(g.w)*j.scale+0.5), int(float64(g.h)*j.scale+0.5)
	dir, err := os.MkdirTemp("", "uipreview")
	if err != nil {
		log.Print(err)
		g.failed = true
		return
	}
	defer os.RemoveAll(dir)
	p, err := app.NewPreview(g.eng, j.lang, j.dark, pw, ph, j.scale, dir)
	if err != nil {
		log.Print(err)
		g.failed = true
		return
	}
	if err := p.Show(j.scene); err != nil {
		log.Print(err)
		g.failed = true
		return
	}
	// Fonts that a string asks for as it is drawn (a Korean name in the English interface) are
	// opened in the background; draw once to ask for them, wait, and draw again.
	// One image is kept for every capture of a size: making and freeing a window-sized image
	// for each of ninety captures eventually makes the software GL of a CI runner draw nothing.
	if g.img == nil || g.img.Bounds().Dx() != pw || g.img.Bounds().Dy() != ph {
		g.img = ebiten.NewImage(pw, ph)
	}
	img := g.img
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			for deadline := time.Now().Add(5 * time.Second); g.eng.Busy() && time.Now().Before(deadline); {
				time.Sleep(5 * time.Millisecond)
			}
			p.App.UI().Invalidate()
			p.Step()
		}
		img.Clear()
		p.DrawPicture(img) // the remote picture of a session screen, under the interface
		g.cv.Begin(img, j.scale)
		p.App.UI().Paint(g.cv)
	}
	buf := make([]byte, 4*pw*ph)
	img.ReadPixels(buf)
	rgba := &image.RGBA{Pix: buf, Stride: 4 * pw, Rect: image.Rect(0, 0, pw, ph)}
	theme := "light"
	if j.dark {
		theme = "dark"
	}
	name := fmt.Sprintf("%s-%s-%s-%sx.png", j.scene, j.lang.Tag(), theme, strconv.FormatFloat(j.scale, 'f', -1, 64))
	path := filepath.Join(g.out, name)
	f, err := os.Create(path)
	if err != nil {
		log.Print(err)
		g.failed = true
		return
	}
	if err := png.Encode(f, rgba); err != nil {
		log.Print(err)
		g.failed = true
	}
	f.Close()
	g.files = append(g.files, path)
	g.next++
}

func main() {
	out := flag.String("out", "ui", "directory for the PNG files")
	scenes := flag.String("scenes", "", "comma-separated screens (default: all); -list shows them")
	langs := flag.String("lang", "en,zh-Hans,ko", "comma-separated languages (en, zh-Hans, ko, xx)")
	themes := flag.String("theme", "dark,light", "comma-separated themes")
	scales := flag.String("scale", "1", "comma-separated display scales, e.g. 1,1.5,2")
	size := flag.String("size", "1100x720", "window size in logical pixels")
	list := flag.Bool("list", false, "list the screens and exit")
	flag.Parse()

	if *list {
		fmt.Println(strings.Join(app.SceneNames(), "\n"))
		return
	}
	var w, h int
	if _, err := fmt.Sscanf(*size, "%dx%d", &w, &h); err != nil || w < 200 || h < 200 {
		log.Fatalf("-size %q is not WxH", *size)
	}
	names := app.SceneNames()
	if *scenes != "" {
		names = strings.Split(*scenes, ",")
	}
	var jobs []job
	for _, l := range strings.Split(*langs, ",") {
		lang, ok := i18n.Parse(l)
		if !ok {
			log.Fatalf("unknown language %q", l)
		}
		for _, sc := range strings.Split(*scales, ",") {
			scale, err := strconv.ParseFloat(sc, 64)
			if err != nil || scale < 1 || scale > 4 {
				log.Fatalf("bad scale %q", sc)
			}
			for _, th := range strings.Split(*themes, ",") {
				for _, n := range names {
					jobs = append(jobs, job{n, lang, th == "dark", scale})
				}
			}
		}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	g := &previewGame{out: *out, w: w, h: h, jobs: jobs, start: time.Now()}
	ebiten.SetWindowSize(320, 200)
	ebiten.SetWindowTitle("uipreview")
	if err := ebiten.RunGame(g); err != nil && err != ebiten.Termination {
		log.Fatal(err)
	}
	if g.failed {
		os.Exit(1)
	}
	for _, f := range g.files {
		fmt.Println(f)
	}
}
