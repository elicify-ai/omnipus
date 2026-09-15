//go:build !darwin && !linux

package tools

import (
	"os"
	"time"
)

// symlinkChangeTime: platforms other than darwin/linux (Windows, the BSDs) do
// not expose ctime under one field name, so the link's modification time stands in.
// A link created by the command that just ran has an mtime at or after the
// command's start.
func symlinkChangeTime(info os.FileInfo) time.Time {
	return info.ModTime()
}
