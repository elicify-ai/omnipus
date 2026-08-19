// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestIsDirMode_FileTypesThatShareTheDirectoryBit is the unit pin for a bug
// that took the whole sandbox down on a real kernel.
//
// addLandlockPathRule strips directory-only access rights before calling
// landlock_add_rule, because the kernel rejects them for non-directories with
// EINVAL. The type test was `mode & S_IFDIR != 0`. That is not a type check:
// the file-type field is 4 bits wide and S_IFDIR is 0040000, so S_IFSOCK
// (0140000) and S_IFBLK (0060000) both carry that bit. Both were treated as
// directories, kept the directory-only rights, and were rejected.
//
// An EINVAL there aborts the spawn (only ENOENT is tolerated), so ONE stray
// unix socket inside a granted directory made every command fail under
// mode=enforce. Measured on a GitHub runner with Landlock ABI v7 on
// 2026-08-19: a leftover /tmp/dotnet-diagnostic-1980-8777-socket turned a
// plain `echo` into "landlock: re-add path ...: invalid argument".
func TestIsDirMode_FileTypesThatShareTheDirectoryBit(t *testing.T) {
	cases := []struct {
		name string
		mode uint32
		want bool
	}{
		{"directory", unix.S_IFDIR, true},
		{"socket shares the S_IFDIR bit but is not a directory", unix.S_IFSOCK, false},
		{"block device shares the S_IFDIR bit but is not a directory", unix.S_IFBLK, false},
		{"regular file", unix.S_IFREG, false},
		{"character device", unix.S_IFCHR, false},
		{"fifo", unix.S_IFIFO, false},
		{"symlink", unix.S_IFLNK, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDirMode(tc.mode | 0o644); got != tc.want {
				t.Errorf("isDirMode(%#o) = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}

	// Differentiation: prove the OLD predicate really did disagree, so this
	// test cannot quietly pass against a reintroduced `mode & S_IFDIR` check.
	for _, m := range []uint32{unix.S_IFSOCK, unix.S_IFBLK} {
		if m&unix.S_IFDIR == 0 {
			t.Errorf("premise broken: %#o no longer shares the S_IFDIR bit, "+
				"this regression pin is measuring nothing", m)
		}
	}
}
