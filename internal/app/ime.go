package app

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2/exp/textinput"
)

// ime connects the input method to the focused text field. Ebiten's textinput package is
// experimental, so everything that touches it is here, in one file (docs/ui-redesign.md,
// section 8.4): if it changes, this is the only place to follow.
//
// While a text field that accepts the input method has the keyboard, a session is open: the
// unfinished text of an input method (a Pinyin or Hangul syllable being composed) is
// reported as composing, and text the input method commits (and ordinary typed characters,
// which on Windows arrive the same way) as committed. The caret rectangle tells the
// candidate window where to appear.
type ime struct {
	comp   textinput.Composer
	caret  image.Rectangle // in the game's coordinates, device pixels
	active bool
	broken bool // the platform refused: typed text comes from Ebiten's plain characters instead

	composing string
	committed []string
}

func newIME() *ime {
	i := &ime{}
	i.comp.OnNewSession = func() *textinput.SessionOptions {
		return &textinput.SessionOptions{CaretBounds: i.caret}
	}
	i.comp.OnComposition = func(c *textinput.Composition) { i.composing = c.Text() }
	i.comp.OnCommit = func(c *textinput.Commit) { i.committed = append(i.committed, c.Text()) }
	return i
}

// update runs one frame. wanted says a field that takes the input method has the keyboard,
// and caret where its caret is in device pixels. It returns the unfinished text, the text
// committed this frame, and whether the input method used the keys of this frame (so the
// interface must not).
func (i *ime) update(wanted bool, caret image.Rectangle) (composing, committed string, handled bool) {
	if !wanted || i.broken {
		if i.active {
			i.comp.Cancel()
			i.active, i.composing, i.committed = false, "", nil
		}
		return "", "", false
	}
	i.caret = caret
	i.active = true
	h, err := i.comp.Update()
	if err != nil {
		i.broken, i.active = true, false
		i.composing, i.committed = "", nil
		return "", "", false
	}
	committed = ""
	for _, c := range i.committed {
		committed += c
	}
	i.committed = i.committed[:0]
	return i.composing, committed, h
}
