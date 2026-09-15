//go:build darwin || linux

package tools

import (
	"os"
	"syscall"
	"time"
)

// symlinkChangeTime returns the inode change time (ctime) of a symlink from
// its Lstat result. ctime is set by the kernel on creation and on every
// metadata change and cannot be backdated by `touch -h`, which is what makes
// it the right clock for "was this link created by the command that just
// ran". Falls back to mtime when the platform stat is unavailable.
func symlinkChangeTime(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return statCtime(st)
	}
	return info.ModTime()
}
