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
	return widenToUint64(st.Nlink), true
}

// deviceID returns the filesystem device an entry lives on.
//
// A hard link CANNOT cross filesystems — that is a property of the link(2)
// syscall, not a convention — so a carve-out root on a different device can
// never hold an alias of the candidate and does not need to be walked at all.
// ok=false means the platform cannot answer, and the caller must fall back to
// scanning rather than assuming.
func deviceID(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return widenToUint64(st.Dev), true
}

// widenToUint64 converts any platform-sized integer field of syscall.Stat_t to
// uint64.
//
// It exists to resolve a genuine platform split that no fixed spelling can
// satisfy: syscall.Stat_t.Nlink is uint16 on darwin and uint64 on linux, and
// Dev is int32 on darwin and uint64 on linux. A direct `uint64(st.Nlink)` is
// REQUIRED to compile on darwin and REDUNDANT on linux, so the file needed a
// //nolint:unconvert directive that was in turn flagged as unused by nolintlint
// on darwin — each platform reporting the other's correct form as a defect.
// Routing the widening through a generic keeps one spelling that is correct
// everywhere and silences neither linter by annotation.
func widenToUint64[T ~int8 | ~int16 | ~int32 | ~int64 | ~int | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uint](v T) uint64 {
	return uint64(v)
}
