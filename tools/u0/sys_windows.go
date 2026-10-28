//go:build windows

package main

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGetProcTimes = kernel32.NewProc("GetProcessTimes")
	procCurrentProc  = kernel32.NewProc("GetCurrentProcess")
	procProcMemory   = kernel32.NewProc("K32GetProcessMemoryInfo")
)

// processMemoryCountersEx is PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCountersEx struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
	privateUsage               uintptr
}

func memoryCounters() (c processMemoryCountersEx) {
	h, _, _ := procCurrentProc.Call()
	c.cb = uint32(unsafe.Sizeof(c))
	procProcMemory.Call(h, uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	return c
}

// cpuTime is the CPU time this process has used so far, kernel and user.
func cpuTime() time.Duration {
	h, _, _ := procCurrentProc.Call()
	var creation, exit, kernel, user syscall.Filetime
	r, _, _ := procGetProcTimes.Call(h, uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return 0
	}
	ft := func(f syscall.Filetime) time.Duration {
		return time.Duration(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) * 100
	}
	return ft(kernel) + ft(user)
}

// residentBytes is the working set: what is in RAM.
func residentBytes() uint64 { return uint64(memoryCounters().workingSetSize) }

// privateBytes is the private usage: memory only this process has committed (the
// "private working set" that Task Manager's details view is closest to).
func privateBytes() uint64 { return uint64(memoryCounters().privateUsage) }
