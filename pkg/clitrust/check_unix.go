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

// checkFolderNotWritableByOthers refuses a folder other users can write: they
// could replace what it holds between the check and the start.
func checkFolderNotWritableByOthers(dir, what string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s (%q) cannot be read: %w", what, dir, err)
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("%s (%q) is writable by other users", what, dir)
	}
	return nil
}

// checkFinalFile requires a regular file, not writable by its group or by
// other users, in a folder other users cannot write, that the gateway's
// effective user can execute (faccessat with AT_EACCESS).
func checkFinalFile(final string) error {
	fi, err := os.Lstat(final)
	if err != nil {
		return fmt.Errorf("%q cannot be read: %w", final, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", final)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%q is writable by its group or by other users", final)
	}
	if err := checkFolderNotWritableByOthers(filepath.Dir(final), "the folder of "+quote(final)); err != nil {
		return err
	}
	if err := unix.Faccessat(unix.AT_FDCWD, final, unix.X_OK, unix.AT_EACCESS); err != nil {
		if errors.Is(err, unix.EACCES) {
			return fmt.Errorf("%q is not executable for the user running Omnipus", final)
		}
		return fmt.Errorf("%q could not be checked as executable: %w", final, err)
	}
	return nil
}
