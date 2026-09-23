//go:build !unix

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// fileInode cannot be answered on this platform: os.FileInfo.Sys() returns
// no inode outside unix (on Windows it is a *syscall.Win32FileAttributeData,
// which carries attributes and timestamps only, and syscall.Stat_t does not
// exist at all) — see truncation_inode_unix_test.go for the unix
// implementation this mirrors (pkg/fspolicy's linkcount_unix.go/linkcount_
// other.go split for the identical reason). Callers must skip the inode
// assertion rather than fail when ok is false.
func fileInode(t *testing.T, path string) (ino uint64, ok bool) {
	t.Helper()
	_, err := os.Stat(path)
	require.NoError(t, err, "stat transcript file")
	return 0, false
}
