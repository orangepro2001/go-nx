//go:build windows

package text

import (
	"os"
	"path/filepath"
)

// SystemLocator finds the fonts Windows ships: Segoe UI for Latin, Microsoft YaHei UI for
// Chinese, Malgun Gothic for Korean, in the system fonts directory and the current user's.
func SystemLocator() Locator { return winLocator{} }

type winLocator struct{}

func (winLocator) Fonts(s Script, w Weight) []FontFile {
	var names []FontFile
	switch s {
	case Latin:
		if w == Semibold {
			names = []FontFile{{Path: "seguisb.ttf"}}
		} else {
			names = []FontFile{{Path: "segoeui.ttf"}}
		}
	case Han:
		names = []FontFile{{Path: "msyh.ttc", Family: "Microsoft YaHei UI"}, {Path: "msyh.ttf"}, {Path: "simsun.ttc", Family: "SimSun"}}
	case Hangul:
		names = []FontFile{{Path: "malgun.ttf"}, {Path: "gulim.ttc", Family: "Gulim"}}
	}
	var dirs []string
	if win := os.Getenv("WINDIR"); win != "" {
		dirs = append(dirs, filepath.Join(win, "Fonts"))
	} else {
		dirs = append(dirs, `C:\Windows\Fonts`)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, "Microsoft", "Windows", "Fonts"))
	}
	var out []FontFile
	for _, n := range names {
		for _, d := range dirs {
			out = append(out, FontFile{Path: filepath.Join(d, n.Path), Family: n.Family})
		}
	}
	return out
}
