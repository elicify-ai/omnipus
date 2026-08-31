// Omnipus — ADR-068 D24 / spec FR-133: Windows, where the creation time is a
// first-class field of the file attribute data.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package propindex

import (
	"os"
	"syscall"
	"time"
)

// birthTime reads the Win32 creation time.
func birthTime(_ string, fi os.FileInfo) (time.Time, bool) {
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok || d == nil {
		return time.Time{}, false
	}
	ns := d.CreationTime.Nanoseconds()
	if ns <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, ns).UTC(), true
}
