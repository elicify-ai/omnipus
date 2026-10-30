//go:build !windows

package tools

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// rootWritability is spec §3.1 condition 5 on Unix: the effective-access check
// (faccessat with AT_EACCESS, so the effective uid/gid decide, not the real
// ids) for write access to "/". EACCES (not allowed) and EROFS (a read-only
// root, such as the macOS sealed system volume) mean the root is not writable,
// so the stand-alone literal rule may apply. Any other error answers
// "writable" — the rule is off and today's refusal stays — and returns the
// cause so the refusal can name it (T11 SF-5).
func rootWritability() (bool, error) {
	err := unix.Faccessat(unix.AT_FDCWD, "/", unix.W_OK, unix.AT_EACCESS)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EROFS):
		return false, nil
	default:
		return true, fmt.Errorf("could not check whether the filesystem root is writable: %w", err)
	}
}
