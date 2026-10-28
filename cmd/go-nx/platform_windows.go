//go:build windows

package main

import (
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/orangepro2001/go-nx/internal/ui/kit"
)

var (
	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pEmptyClipboard   = user32.NewProc("EmptyClipboard")
	pGetClipboardData = user32.NewProc("GetClipboardData")
	pSetClipboardData = user32.NewProc("SetClipboardData")
	pGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	pGlobalFree       = kernel32.NewProc("GlobalFree")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// winPlatform is what Windows provides to the application (app.Platform).
type winPlatform struct{}

func (winPlatform) Quit()                    { quitApp() }
func (winPlatform) HideWindow()              { hideMainWindow() }
func (winPlatform) WindowHidden() bool       { return windowHidden.Load() }
func (winPlatform) Clipboard() kit.Clipboard { return winClipboard{} }
func (winPlatform) MessageBox(title, text string) {
	messageBox(title, text)
}

// SystemDark reads the "apps use light theme" setting, which is 0 when apps should be dark.
func (winPlatform) SystemDark() (dark, ok bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false, false
	}
	return v == 0, true
}

// PreferredLanguages are the languages the user has chosen for Windows, most preferred
// first ("zh-CN", "en-US").
func (winPlatform) PreferredLanguages() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return langs
}

// ApplyTheme colours the title bar: Windows draws it, and it follows the theme of the
// application only when asked (DWMWA_USE_IMMERSIVE_DARK_MODE).
func (winPlatform) ApplyTheme(dark bool) {
	h := mainWindow.Load()
	if h == 0 {
		themeWanted.Store(dark)
		themePending.Store(true)
		return
	}
	setTitleBarDark(h, dark)
}

func setTitleBarDark(h uintptr, dark bool) {
	v := int32(0)
	if dark {
		v = 1
	}
	_ = windows.DwmSetWindowAttribute(windows.HWND(h), windows.DWMWA_USE_IMMERSIVE_DARK_MODE, unsafe.Pointer(&v), 4)
}

// winClipboard is the system clipboard, as text.
type winClipboard struct{}

func openClipboard() bool {
	for i := 0; i < 10; i++ { // another program may have it for a moment
		if r, _, _ := pOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (winClipboard) Get() string {
	if !openClipboard() {
		return ""
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return ""
	}
	defer pGlobalUnlock.Call(h)
	return windows.UTF16PtrToString((*uint16)(ptrFrom(p)))
}

func (winClipboard) Set(s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	if !openClipboard() {
		return
	}
	defer pCloseClipboard.Call()
	size := uintptr(len(u) * 2)
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return
	}
	copy(unsafe.Slice((*uint16)(ptrFrom(p)), len(u)), u)
	pGlobalUnlock.Call(h)
	pEmptyClipboard.Call()
	if r, _, _ := pSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		pGlobalFree.Call(h) // the system did not take it
	}
}

// ptrFrom turns an address returned by the system (GlobalLock) into a pointer. The memory
// belongs to Windows, not to Go, so the garbage collector has nothing to do with it.
func ptrFrom(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }
