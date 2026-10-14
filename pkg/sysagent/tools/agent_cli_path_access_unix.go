//go:build !windows

// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// currentUserCanExecute answers truthfully whether the EFFECTIVE user can
// execute path right now: faccessat(AT_EACCESS, X_OK) — the same question the
// kernel asks at exec time. Mode bits alone lie (issue #1274 fix round 1,
// CHECK F01): a 0601 file owned by the current user carries other-execute,
// which its own owner class may not use, so Perm&0111 != 0 accepts a file the
// user cannot run; conversely a privileged user may execute a file with any
// single execute bit. Faccessat resolves both through the real credential and
// capability check. golang.org/x/sys/unix is the project's sanctioned pure-Go
// kernel interface (Hard Constraint #2) — no CGo, no spawned helper.
func currentUserCanExecute(path string) error {
	if err := unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS); err != nil {
		return fmt.Errorf("the current user lacks execute permission: %w", err)
	}
	return nil
}
