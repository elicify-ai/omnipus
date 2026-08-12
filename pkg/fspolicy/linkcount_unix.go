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
	return uint64(st.Nlink), true
}
