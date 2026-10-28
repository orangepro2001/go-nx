//go:build windows

package vdisplay

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed driver/parsec-vdd-0.45.0.0.exe
var installer []byte

var (
	shell32         = windows.NewLazySystemDLL("shell32.dll")
	pShellExecuteEx = shell32.NewProc("ShellExecuteExW")
)

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	size       uint32
	mask       uint32
	hwnd       uintptr
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instApp    uintptr
	idList     uintptr
	class      *uint16
	keyClass   uintptr
	hotKey     uint32
	iconOrMon  uintptr
	process    windows.Handle
}

// ErrDeclined is returned by Install when the person at the PC said no to the administrator
// prompt.
var ErrDeclined = errors.New("the administrator prompt was declined")

// Install puts the driver installer go-nx carries in dir, checks it (size, SHA-256 and
// Authenticode signature), runs it silently as administrator and waits for it. Windows asks
// the person at the PC to confirm, on the secure desktop: a viewer cannot see or answer
// that prompt.
func Install(dir string) error {
	if err := checkInstaller(installer); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, InstallerName)
	if err := os.WriteFile(path, installer, 0o700); err != nil {
		return err
	}
	if err := verifySignature(path); err != nil {
		return fmt.Errorf("the driver installer's signature: %w", err)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(path)
	args, _ := windows.UTF16PtrFromString("/S")
	const seeMaskNoCloseProcess = 0x40
	info := &shellExecuteInfo{mask: seeMaskNoCloseProcess, verb: verb, file: file, parameters: args, show: windows.SW_HIDE}
	info.size = uint32(unsafe.Sizeof(*info))
	if r, _, err := pShellExecuteEx.Call(uintptr(unsafe.Pointer(info))); r == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return ErrDeclined
		}
		return fmt.Errorf("starting the driver installer: %w", err)
	}
	defer windows.CloseHandle(info.process)
	if ev, err := windows.WaitForSingleObject(info.process, 5*60*1000); err != nil || ev != windows.WAIT_OBJECT_0 {
		return errors.New("the driver installer did not finish within 5 minutes")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err == nil && code != 0 {
		return fmt.Errorf("the driver installer ended with code %d", code)
	}
	return nil
}

// verifySignature checks the file's Authenticode signature with Windows.
func verifySignature(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	data := &windows.WinTrustData{
		Size:             uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:         windows.WTD_UI_NONE,
		RevocationChecks: windows.WTD_REVOKE_NONE, // the PC may be offline; the hash is pinned anyway
		UnionChoice:      windows.WTD_CHOICE_FILE,
		StateAction:      windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&windows.WinTrustFileInfo{
			Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
			FilePath: p,
		}),
	}
	verr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	return verr
}
