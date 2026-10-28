package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/orangepro2001/go-nx/internal/ui/i18n"
	"github.com/orangepro2001/go-nx/internal/ui/kit"
	"github.com/orangepro2001/go-nx/internal/ui/text"
)

var sizes = [][2]int{{1100, 720}, {900, 600}, {760, 520}, {640, 480}}

// Every screen, in every language including the pseudo-language that makes every string
// longer, at every window size and in both themes, must show no placeholder left over, put
// nothing off the window, and not draw one line of text on top of another.
func TestEveryScreenFitsInEveryLanguageAndSize(t *testing.T) {
	for _, name := range SceneNames() {
		for _, lang := range []i18n.Lang{i18n.English, i18n.Chinese, i18n.Korean, i18n.Pseudo} {
			for _, sz := range sizes {
				for _, dark := range []bool{true, false} {
					id := fmt.Sprintf("%s/%s/%dx%d/dark=%v", name, lang.Tag(), sz[0], sz[1], dark)
					t.Run(id, func(t *testing.T) {
						p, err := NewPreview(FixedText{}, lang, dark, sz[0], sz[1], 1, t.TempDir())
						if err != nil {
							t.Fatal(err)
						}
						if err := p.Show(name); err != nil {
							t.Fatal(err)
						}
						checkLayout(t, p.App, sz[0], sz[1])
					})
				}
			}
		}
	}
}

type placed struct {
	text       string
	x, y, w, h float64
}

func checkLayout(t *testing.T, a *App, w, h int) {
	t.Helper()
	rec := &kit.Recorder{}
	a.ui.Paint(rec)
	var ts []placed
	clips := []kit.Rect{{X: 0, Y: 0, W: float64(w), H: float64(h)}}
	for _, op := range rec.Ops {
		switch op.Kind {
		case "clip":
			clips = append(clips, op.R.Intersect(clips[len(clips)-1]))
		case "unclip":
			clips = clips[:len(clips)-1]
		case "round":
			// An opaque panel drawn after some text covers it (a notice over the page).
			if op.Color.A == 255 {
				kept := ts[:0]
				for _, p := range ts {
					if (kit.Rect{X: p.x, Y: p.y, W: p.w, H: p.h}).Intersect(op.R).Empty() {
						kept = append(kept, p)
					}
				}
				ts = kept
			}
		case "fill":
			// A dimming layer over the whole window (a dialog's): what was drawn before it is
			// behind it and no longer counts.
			if op.Color.A < 255 && op.R.W >= float64(w) && op.R.H >= float64(h) {
				ts = nil
			}
		case "text":
			if strings.ContainsAny(op.Text, "{}") {
				t.Errorf("a placeholder is showing: %q", op.Text)
			}
			tw := (text.Fake{}).Width(op.Text, op.Style)
			lh := (text.Fake{}).LineHeight(op.Style)
			p := placed{op.Text, op.R.X, op.R.Y, tw, lh}
			vis := clips[len(clips)-1]
			// Text that is clipped away (below the fold of a scrolling page) is not a defect;
			// text that is in view must end inside the window.
			if (kit.Rect{X: p.x, Y: p.y, W: p.w, H: p.h}).Intersect(vis).Empty() {
				continue
			}
			if p.x < -0.5 || p.x+p.w > float64(w)+0.5 {
				t.Errorf("%q spans x %.0f..%.0f in a window %d wide", op.Text, p.x, p.x+p.w, w)
			}
			ts = append(ts, p)
		}
	}
	for i := range ts {
		for j := i + 1; j < len(ts); j++ {
			a, b := ts[i], ts[j]
			ox := min(a.x+a.w, b.x+b.w) - max(a.x, b.x)
			oy := min(a.y+a.h, b.y+b.h) - max(a.y, b.y)
			// An overlap of more than a couple of pixels each way is two texts on top of each other.
			if ox > 2 && oy > 3 {
				t.Errorf("%q (%.0f,%.0f %.0fx%.0f) overlaps %q (%.0f,%.0f %.0fx%.0f)", a.text, a.x, a.y, a.w, a.h, b.text, b.x, b.y, b.w, b.h)
			}
		}
	}
}
