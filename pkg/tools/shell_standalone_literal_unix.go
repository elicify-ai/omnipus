//go:build !windows

package tools

import (
	"errors"

	"golang.org/x/sys/unix"
)

// rootIsWritable is spec §3.1 condition 5 on Unix: the effective-access check
// (faccessat with AT_EACCESS, so the effective uid/gid decide, not the real
// ids) for write access to "/". EACCES (not allowed) and EROFS (a read-only
// root, such as the macOS sealed system volume) mean the root is not writable,
// so the stand-alone literal rule may apply. Any other error answers
// "writable", which turns the rule off and keeps today's refusal.
func rootIsWritable() bool {
	err := unix.Faccessat(unix.AT_FDCWD, "/", unix.W_OK, unix.AT_EACCESS)
	if err == nil {
		return true
	}
	return !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EROFS)
}
