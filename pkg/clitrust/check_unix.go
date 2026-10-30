//go:build !windows

package clitrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// checkPathShape is the Unix shape rule: the path must be absolute.
func checkPathShape(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path; enter the full path starting with /", path)
	}
	return nil
}

// checkFinalFile requires a regular file the gateway's effective user can
// execute (faccessat with AT_EACCESS). Who owns the file or can write it is
// the user's responsibility, not Omnipus's (founder decision 2026-10-30,
// "Omnipus runs the command and worries not about where the CLI accesses
// anything"); the only location rule is the agent-writable-root check in
// CheckProgramPath.
func checkFinalFile(final string) error {
	fi, err := os.Lstat(final)
	if err != nil {
		return fmt.Errorf("%q cannot be read: %w", final, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", final)
	}
	if err := unix.Faccessat(unix.AT_FDCWD, final, unix.X_OK, unix.AT_EACCESS); err != nil {
		if errors.Is(err, unix.EACCES) {
			return fmt.Errorf("%q is not executable for the user running Omnipus", final)
		}
		return fmt.Errorf("%q could not be checked as executable: %w", final, err)
	}
	return nil
}
