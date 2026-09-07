// Omnipus — ADR-068 D24 / spec FR-133: Windows, where the creation time is a
// first-class field of the file attribute data.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package fileutil

import (
	"os"
	"syscall"
	"time"
)

// BirthTime reads the Win32 creation time.
// BirthTime is exported because TWO packages need it and neither may import
// the other: pkg/records/propindex stores the value, and pkg/knowledge's
// collection walk is where the os.FileInfo it comes from already exists.
// propindex's own tests import pkg/knowledge, so knowledge importing propindex
// is an import cycle in the test binary — which is how this landed in the one
// package both can depend on.
func BirthTime(_ string, fi os.FileInfo) (time.Time, bool) {
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
