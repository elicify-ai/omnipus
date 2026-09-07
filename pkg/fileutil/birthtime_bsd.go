// Omnipus — ADR-068 D24 / spec FR-133: reading a file's BIRTH time, where the
// platform records one. Shared by pkg/records/propindex and pkg/knowledge.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin || freebsd || netbsd

package fileutil

import (
	"os"
	"syscall"
	"time"
)

// BirthTime reads st_birthtim, which these platforms record natively.
//
// A zero birth time is reported as ABSENT rather than as the epoch: some
// filesystems leave the field unset, and 1970-01-01 is a plausible-looking
// instant that would sort every such note to the front of `file.ctime` forever.
// BirthTime is exported because TWO packages need it and neither may import
// the other: pkg/records/propindex stores the value, and pkg/knowledge's
// collection walk is where the os.FileInfo it comes from already exists.
// propindex's own tests import pkg/knowledge, so knowledge importing propindex
// is an import cycle in the test binary — which is how this landed in the one
// package both can depend on.
func BirthTime(_ string, fi os.FileInfo) (time.Time, bool) {
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
