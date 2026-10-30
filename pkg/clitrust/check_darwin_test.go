//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package clitrust_test

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCheckProgramPath_ApplicationsAdminGroupAllowed pins the founder decision
// for T11 slice A: a program reached through /Applications is allowed when that
// folder is owned by root, grouped admin, and mode 0775. Other group-writable
// and other-writable folders stay refused; that case is
// TestCheckProgramPath_GroupWritableDirectory.
//
// There is no owner/group injection seam, so this uses the real folder. It
// fails the fixture if this Mac's /Applications is not that shape. macOS CI
// stays off, so this file is not a CI expectation.
func TestCheckProgramPath_ApplicationsAdminGroupAllowed(t *testing.T) {
	check, _ := loadT11CLITrust(t)
	const dir = "/Applications"
	fi, err := os.Lstat(dir)
	require.NoError(t, err)
	require.True(t, fi.IsDir(), "fixture: /Applications is a directory")
	st, ok := fi.Sys().(*syscall.Stat_t)
	require.True(t, ok, "fixture: /Applications has a Unix owner")
	require.Equal(t, uint32(0), st.Uid, "fixture: /Applications is owned by root")
	admin, err := user.LookupGroup("admin")
	require.NoError(t, err)
	gid, err := strconv.ParseUint(admin.Gid, 10, 32)
	require.NoError(t, err)
	require.Equal(t, uint32(gid), st.Gid, "fixture: /Applications group is admin")
	require.Equal(t, os.FileMode(0o775), fi.Mode().Perm(), "fixture: /Applications is mode 0775")
	require.Zero(t, fi.Mode()&os.ModeSticky, "fixture: this allow is not the sticky-folder exception")

	// Safari.app is a link in /Applications. Its resolved file lives outside
	// that folder, and every other directory on the walk is mode 0755, so the
	// only group-writable folder the check meets is /Applications itself.
	submitted := "/Applications/Safari.app/Contents/MacOS/Safari"
	link, err := os.Lstat("/Applications/Safari.app")
	require.NoError(t, err)
	require.NotZero(t, link.Mode()&os.ModeSymlink, "fixture: Safari.app is the link that sits in /Applications")
	final, err := filepath.EvalSymlinks(submitted)
	require.NoError(t, err)
	require.Falsef(t, strings.HasPrefix(final, dir+"/"),
		"fixture: the resolved file must sit outside /Applications so the allow is the folder exception, got %s", final)

	resolved, err := check(submitted, nil)
	require.NoError(t, err, "a program reached through /Applications (root:admin, mode 0775) is allowed")
	require.Equal(t, final, resolved, "callers launch the resolved file")
}
