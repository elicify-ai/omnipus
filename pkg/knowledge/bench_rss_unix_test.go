//go:build !windows

// Omnipus — peak resident memory, normalised (spec test 38 / MV-2).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import "syscall"

// peakRSSBytes returns the process's high-water resident set size in BYTES.
//
// The normalisation is the point. getrusage's ru_maxrss is kilobytes on Linux
// and bytes on Darwin/BSD, so a benchmark that compares the raw number against
// a byte budget is wrong by a factor of 1024 on one of the two platforms — and
// wrong in the direction that PASSES on Linux, which is where CI runs.
func peakRSSBytes() (uint64, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return uint64(ru.Maxrss) * uint64(maxRSSUnitBytes), true //nolint:gosec // Maxrss is a non-negative high-water mark
}
