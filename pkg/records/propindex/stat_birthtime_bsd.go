// Omnipus — ADR-068 D24 / spec FR-133: the platforms whose stat structure
// records a birth time.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin || freebsd || netbsd

package propindex

import (
	"os"
	"syscall"
	"time"
)

// birthTime reads st_birthtim, which these platforms record natively.
//
// A zero birth time is reported as ABSENT rather than as the epoch: some
// filesystems leave the field unset, and 1970-01-01 is a plausible-looking
// instant that would sort every such note to the front of `file.ctime` forever.
func birthTime(_ string, fi os.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return time.Time{}, false
	}
	// Timespec.Unix() rather than two conversions: Sec and Nsec are int64 on
	// some of these platforms and int32 on others, so a written-out conversion
	// is either required or redundant depending on GOARCH — and `unconvert`
	// flags the redundant case on whichever one the linter happens to run for.
	sec, nsec := st.Birthtimespec.Unix()
	if sec <= 0 && nsec == 0 {
		return time.Time{}, false
	}
	return time.Unix(sec, nsec).UTC(), true
}
