//go:build !linux && !windows

package main

import "time"

func cpuTime() time.Duration { return 0 }
func residentBytes() uint64  { return 0 }
func privateBytes() uint64   { return 0 }
