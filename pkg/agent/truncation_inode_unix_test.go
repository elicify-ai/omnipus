//go:build unix

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// fileInode returns path's platform inode number via os.FileInfo.Sys()'s
// *syscall.Stat_t, which exists on every unix GOOS this repo builds for
// (linux, darwin) but not on windows — see truncation_inode_other_test.go
// for that side, mirroring pkg/fspolicy's linkcount_unix.go/linkcount_
// other.go split for the identical reason.
func fileInode(t *testing.T, path string) (ino uint64, ok bool) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "stat transcript file")
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Ino, true
}
