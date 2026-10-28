//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// cpuTime is the CPU time this process has used so far, user and system.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano()) + time.Duration(ru.Stime.Nano())
}

// residentBytes is the memory this process holds in RAM (Linux: resident set).
func residentBytes() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, _ := strconv.ParseUint(f[1], 10, 64)
	return pages * uint64(os.Getpagesize())
}

// privateBytes is the memory only this process uses (Linux: resident anonymous
// memory, RssAnon in /proc/self/status).
func privateBytes() uint64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "RssAnon:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				kb, _ := strconv.ParseUint(f[0], 10, 64)
				return kb << 10
			}
		}
	}
	return 0
}
