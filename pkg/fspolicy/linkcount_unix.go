//go:build unix

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"os"
	"syscall"
)

// linkCount returns the number of directory entries pointing at info's inode.
//
// It is the cheap gate in front of the hard-link scan: a file with exactly one
// link cannot be an alias for anything, and that is every ordinary file, so the
// scan never runs for them. Only a genuinely multiply-linked file pays for it.
//
// ok=false means the platform cannot answer; see linkcount_other.go for what
// that costs and why it is stated rather than silently absorbed.
func linkCount(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	// Stat_t.Nlink is uint16 on darwin and uint64 on linux/amd64. The cast is
	// therefore REQUIRED to compile on darwin and redundant on linux, and no
	// single form satisfies both linters: on linux unconvert flags the cast and
	// the directive suppresses it; on darwin unconvert never fires, so
	// nolintlint reports the directive as unused.
	//
	// Kept in the form that is correct for the platform CI LINTS (linux), where
	// the directive is used and the package is clean. A macOS-local `make lint`
	// reports this one line as an unused directive — that is the inverse of the
	// same platform split, not a defect, and removing the cast to silence it
	// would break the macOS build outright.
	//nolint:unconvert // required on darwin (uint16 Nlink); see above
	return uint64(st.Nlink), true
}
