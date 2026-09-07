// Omnipus — ADR-068 D24 / spec FR-133: Linux, where the birth time exists only
// through statx(2) and only on some filesystems.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package fileutil

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// BirthTime asks statx(2) for STATX_BTIME.
//
// THREE ways this legitimately returns false, and none of them is an error the
// operator can act on:
//
//   - the kernel predates statx(2) (pre-4.11), so the call itself fails;
//   - the filesystem does not record a creation time, so the kernel clears
//     STATX_BTIME in the returned mask even though the call succeeded — the
//     mask is why this is CHECKED rather than assumed, since the Btime field is
//     simply left zero and reads as 1970-01-01;
//   - the file predates the field being recorded.
//
// In every case `file.ctime` is ABSENT (FR-133). st_ctime is sitting right
// there in the same structure and is NOT substituted: it is the inode-change
// time, which moves on a chmod and is routinely later than the modification
// time.
//
// The path is required because statx takes one; a caller with only an
// os.FileInfo gets an honest absence rather than a wrong instant.
// BirthTime is exported because TWO packages need it and neither may import
// the other: pkg/records/propindex stores the value, and pkg/knowledge's
// collection walk is where the os.FileInfo it comes from already exists.
// propindex's own tests import pkg/knowledge, so knowledge importing propindex
// is an import cycle in the test binary — which is how this landed in the one
// package both can depend on.
func BirthTime(path string, _ os.FileInfo) (time.Time, bool) {
	if path == "" {
		return time.Time{}, false
	}
	var st unix.Statx_t
	// AT_SYMLINK_NOFOLLOW so this reads the SAME inode the caller's os.FileInfo
	// describes. The collection walk uses Lstat semantics and refuses symlinks
	// outright (pkg/knowledge/scan.go), so a following statx would be asking
	// about a different file from the one the size and mtime came from — and
	// the mismatch would be invisible: three plausible numbers, one of them
	// about something else.
	//
	// statx does NOT open the file. That matters here beyond tidiness: the
	// scanner's contract is that it never opens anything, least of all an
	// attachment (FR-039a), and a birth-time lookup that opened a file would
	// break a property that package counts rather than claims.
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &st); err != nil {
		return time.Time{}, false
	}
	if st.Mask&unix.STATX_BTIME == 0 {
		return time.Time{}, false
	}
	sec, nsec := st.Btime.Sec, int64(st.Btime.Nsec)
	if sec <= 0 && nsec == 0 {
		return time.Time{}, false
	}
	return time.Unix(sec, nsec).UTC(), true
}
