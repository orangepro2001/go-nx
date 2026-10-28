//go:build !windows

package text

import "path/filepath"

// SystemLocator finds fonts on a development machine or a CI runner, where the Windows
// fonts are not: Noto, DejaVu and Liberation when they are installed. The program only
// ships for Windows; this is so that the interface can be drawn and tested elsewhere.
func SystemLocator() Locator { return unixLocator{} }

type unixLocator struct{}

var unixDirs = []string{
	"/usr/share/fonts/truetype/noto", "/usr/share/fonts/opentype/noto", "/usr/share/fonts/truetype/dejavu",
	"/usr/share/fonts/truetype/liberation", "/usr/share/fonts/noto-cjk", "/usr/share/fonts/google-noto-cjk",
	"/usr/share/fonts/truetype/noto-cjk", "/usr/share/fonts/OTF", "/usr/share/fonts/TTF",
	"/System/Library/Fonts", "/Library/Fonts",
}

func (unixLocator) Fonts(s Script, w Weight) []FontFile {
	var names []FontFile
	switch s {
	case Latin:
		if w == Semibold {
			names = []FontFile{{Path: "NotoSans-SemiBold.ttf"}, {Path: "NotoSans-Bold.ttf"}, {Path: "DejaVuSans-Bold.ttf"}}
		} else {
			names = []FontFile{{Path: "NotoSans-Regular.ttf"}, {Path: "DejaVuSans.ttf"}, {Path: "LiberationSans-Regular.ttf"}}
		}
	case Han:
		names = []FontFile{{Path: "NotoSansCJK-Regular.ttc", Family: "Noto Sans CJK SC"}, {Path: "NotoSansCJKsc-Regular.otf"}}
	case Hangul:
		names = []FontFile{{Path: "NotoSansCJK-Regular.ttc", Family: "Noto Sans CJK KR"}, {Path: "NotoSansCJKkr-Regular.otf"}}
	}
	var out []FontFile
	for _, n := range names {
		for _, d := range unixDirs {
			out = append(out, FontFile{Path: filepath.Join(d, n.Path), Family: n.Family})
		}
	}
	return out
}
