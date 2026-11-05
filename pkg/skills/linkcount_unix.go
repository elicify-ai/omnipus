//go:build unix

package skills

import (
	"os"
	"syscall"
)

// hardLinkCount returns the number of directory entries naming fi's inode
// (§7.2 live-write path check: a write never goes through a hard link).
func hardLinkCount(fi os.FileInfo) uint64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 1
	}
	return widenLinkCount(st.Nlink)
}

// widenLinkCount widens the platform-sized Nlink field (uint16 on darwin,
// uint64 on linux) with one spelling that compiles everywhere.
func widenLinkCount[T ~uint16 | ~uint32 | ~uint64](v T) uint64 { return uint64(v) }
