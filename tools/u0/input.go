package main

// InputResult is what an Ebiten window sees of keys that are injected with
// SendInput, which is what a physical keyboard looks like to it. The check runs on
// Windows only.
type InputResult struct {
	Skipped        string   `json:"skipped,omitempty"`
	Focused        bool     `json:"focused"`                 // the window reached the foreground and Ebiten knows it
	Keys           []string `json:"keysSeen,omitempty"`      // keys Ebiten reported as pressed, in order
	Chars          string   `json:"charsSeen,omitempty"`     // characters from ebiten.AppendInputChars
	FieldText      string   `json:"textFieldText,omitempty"` // what an exp/textinput Field holds
	NoKeyForHangul bool     `json:"vkHangulAndHanjaHaveNoEbitenKey"`
	HangulChars    string   `json:"unicodeCharsSeen,omitempty"` // text injected as Unicode that arrived as characters
	Err            string   `json:"error,omitempty"`
}
