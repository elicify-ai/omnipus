// Omnipus — ADR-068 D24 / spec FR-133: every platform that records no birth
// time, where the honest answer is that there is none.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !darwin && !freebsd && !netbsd && !linux && !windows

package propindex

import (
	"os"
	"time"
)

// birthTime reports that this platform has no creation time to give.
//
// This is FR-133's whole point stated as a build constraint: openbsd, plan9,
// js/wasm and anything else lands here and `file.ctime` is ABSENT — flagged,
// reported as unknown, never filled in from the inode-change time that shares
// the name.
func birthTime(_ string, _ os.FileInfo) (time.Time, bool) {
	return time.Time{}, false
}
