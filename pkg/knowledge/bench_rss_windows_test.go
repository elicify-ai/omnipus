//go:build windows

// Omnipus — peak resident memory is not read on Windows (spec test 38 / MV-2).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// peakRSSBytes reports that the measurement is unavailable rather than
// returning a made-up number. MV-2's benchmark skips loudly on this platform;
// a zero here would read as "0 MB peak", which would pass the budget while
// measuring nothing.
func peakRSSBytes() (uint64, bool) { return 0, false }
