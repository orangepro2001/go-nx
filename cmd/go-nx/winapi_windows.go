//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	pEnumWindows         = user32.NewProc("EnumWindows")
	pGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	pIsWindowVisible     = user32.NewProc("IsWindowVisible")
	pGetWindow           = user32.NewProc("GetWindow")
	pShowWindowAsync     = user32.NewProc("ShowWindowAsync")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pMessageBox          = user32.NewProc("MessageBoxW")
	pCreateMutex         = kernel32.NewProc("CreateMutexW")
	iphlpapi             = syscall.NewLazyDLL("iphlpapi.dll")
	pGetExtUDPTable      = iphlpapi.NewProc("GetExtendedUdpTable")
	pOpenProcess         = kernel32.NewProc("OpenProcess")
	pCloseHandle         = kernel32.NewProc("CloseHandle")
	pQueryImageName      = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	swHide           = 0
	swShow           = 5
	swRestore        = 9
	gwOwner          = 4
	errAlreadyExists = 183
)

// mainWindow is the handle of this process's visible top-level window, found once
// after the window exists (a hidden window can no longer be enumerated by visibility).
var mainWindow atomic.Uintptr

// windowHidden is true while the window is hidden in the tray.
var windowHidden atomic.Bool

// findMainWindow records this process's first visible, unowned top-level window.
func findMainWindow() {
	pid := uint32(os.Getpid())
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var wpid uint32
		pGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		visible, _, _ := pIsWindowVisible.Call(hwnd)
		owner, _, _ := pGetWindow.Call(hwnd, gwOwner)
		if wpid == pid && visible != 0 && owner == 0 {
			mainWindow.Store(hwnd)
			return 0 // stop enumerating
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
}

// hideMainWindow and showMainWindow must use ShowWindowAsync. ShowWindow called
// from another thread waits for the window's thread to handle the message, and
// Ebiten runs Update on a different thread while the window's thread waits for
// Update to return: calling it from Update would deadlock and freeze the window.
func hideMainWindow() {
	if h := mainWindow.Load(); h != 0 {
		windowHidden.Store(true)
		pShowWindowAsync.Call(h, swHide)
	}
}

func showMainWindow() {
	if h := mainWindow.Load(); h != 0 {
		windowHidden.Store(false)
		pShowWindowAsync.Call(h, swShow)
		pShowWindowAsync.Call(h, swRestore)
		pSetForegroundWindow.Call(h)
	}
}

// alreadyRunning reports whether another go-nx of this user session holds the mutex.
// After an update the previous instance may still be exiting, so wait lets the
// check keep trying for that long before it concludes the other one is staying.
func alreadyRunning(wait time.Duration) bool {
	name, _ := syscall.UTF16PtrFromString(`Local\go-nx-single-instance`)
	deadline := time.Now().Add(wait)
	for {
		h, _, err := pCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
		if h == 0 || err != syscall.Errno(errAlreadyExists) {
			return false // this process now holds the mutex until it exits
		}
		// The handle just returned refers to the other process's mutex. Keeping it
		// would keep the mutex alive after that process exits, so no retry could win.
		pCloseHandle.Call(h)
		if !time.Now().Before(deadline) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func messageBox(title, msg string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(msg)
	pMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40) // MB_ICONINFORMATION
}

// udpPortOwner returns the PID of the process bound to the local UDP port.
func udpPortOwner(port int) (uint32, bool) {
	const udpTableOwnerPID = 1
	// Row layouts of MIB_UDPROW_OWNER_PID (IPv4) and MIB_UDP6ROW_OWNER_PID (IPv6).
	for _, fam := range []struct{ af, rowSize, portOff, pidOff int }{{2, 12, 4, 8}, {23, 28, 20, 24}} {
		var size uint32
		pGetExtUDPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(fam.af), udpTableOwnerPID, 0)
		if size == 0 {
			continue
		}
		buf := make([]byte, size)
		if r, _, _ := pGetExtUDPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(fam.af), udpTableOwnerPID, 0); r != 0 {
			continue
		}
		n := int(binary.LittleEndian.Uint32(buf))
		for i := 0; i < n && 4+(i+1)*fam.rowSize <= len(buf); i++ {
			row := buf[4+i*fam.rowSize:]
			p := binary.LittleEndian.Uint32(row[fam.portOff:])
			if int(p&0xFF)<<8|int(p>>8&0xFF) == port { // stored in network byte order
				return binary.LittleEndian.Uint32(row[fam.pidOff:]), true
			}
		}
	}
	return 0, false
}

// processImage returns the full path of the executable of process pid.
func processImage(pid uint32) string {
	const queryLimited = 0x1000
	h, _, _ := pOpenProcess.Call(queryLimited, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer pCloseHandle.Call(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if r, _, _ := pQueryImageName.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// explainListenError turns a failed bind of UDP port into advice, naming the
// process that holds the port when it can be found.
func explainListenError(err error, port int) string {
	if !errors.Is(err, syscall.Errno(10048)) { // WSAEADDRINUSE
		return err.Error()
	}
	who := "another program"
	if pid, ok := udpPortOwner(port); ok {
		if pid == uint32(os.Getpid()) {
			return fmt.Sprintf("UDP port %d is already in use by this go-nx", port)
		}
		who = fmt.Sprintf("process %d", pid)
		if img := processImage(pid); img != "" {
			who = fmt.Sprintf("%s (process %d)", filepath.Base(img), pid)
		}
	}
	return fmt.Sprintf("UDP port %d is in use by %s. End the older go-nx / host.exe in Task Manager, or start with -port.", port, who)
}
