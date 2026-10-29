//go:build darwin || linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package clitrust_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func TestCheckProgramPath_Unix(t *testing.T) {
	check, roots := loadT11CLITrust(t)
	home := t.TempDir()
	require.NoError(t, os.Chmod(home, 0o700))
	const id = "t11trust"
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: "T11"}))
	work, err := workspace.SafeWorkDir(home, id)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(work, 0o700))
	mountHost := t.TempDir()
	mount, _, err := workspace.CreateMount(home, id, "extra", mountHost)
	require.NoError(t, err)

	gotRoots := roots(home, &config.Config{})
	for _, want := range []string{home, workspace.WorkspaceDir(home, id), mount.HostPath} {
		require.Truef(t, t11PathListed(gotRoots, want),
			"spec §3.2 AgentWritableRoots must list %s, got %v", want, gotRoots)
	}
	forbidden := []string{home, workspace.WorkspaceDir(home, id), mount.HostPath}

	mustRefuse := func(t *testing.T, path, needle string) {
		t.Helper()
		resolved, err := check(path, forbidden)
		require.Error(t, err, "spec FR-5: %s must be refused", path)
		require.Empty(t, resolved, "a refusal returns no file to launch")
		require.Contains(t, strings.ToLower(err.Error()), needle,
			"spec §3.2: the error names the failed check; err=%v", err)
	}

	t.Run("inside omnipus home", func(t *testing.T) {
		p := t11WriteExe(t, filepath.Join(home, "bin"), "fakecli", 0o755)
		mustRefuse(t, p, "home")
	})
	t.Run("inside a workspace", func(t *testing.T) {
		p := t11WriteExe(t, work, "fakecli", 0o755)
		mustRefuse(t, p, "workspace")
	})
	t.Run("inside a mount", func(t *testing.T) {
		p := t11WriteExe(t, mount.HostPath, "fakecli", 0o755)
		mustRefuse(t, p, "mount")
	})
	t.Run("link target inside a workspace", func(t *testing.T) {
		inside := t11WriteExe(t, work, "target", 0o755)
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		link := filepath.Join(dir, "link")
		require.NoError(t, os.Symlink(inside, link))
		mustRefuse(t, link, "link")
	})
	t.Run("middle link sits in a workspace", func(t *testing.T) {
		final := t11WriteExe(t, t.TempDir(), "final", 0o755)
		mid := filepath.Join(work, "mid")
		require.NoError(t, os.Symlink(final, mid))
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		outer := filepath.Join(dir, "outer")
		require.NoError(t, os.Symlink(mid, outer))
		mustRefuse(t, outer, "link")
	})
	t.Run("group writable", func(t *testing.T) {
		p := t11WriteExe(t, t.TempDir(), "fakecli", 0o775)
		mustRefuse(t, p, "writable")
	})
	t.Run("other writable", func(t *testing.T) {
		p := t11WriteExe(t, t.TempDir(), "fakecli", 0o707)
		mustRefuse(t, p, "writable")
	})
	t.Run("parent other writable", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o707))
		p := t11WriteExe(t, dir, "fakecli", 0o755)
		mustRefuse(t, p, "writable")
	})
	t.Run("relative path", func(t *testing.T) {
		mustRefuse(t, "fakecli", "absolute")
	})
	t.Run("directory", func(t *testing.T) {
		mustRefuse(t, t.TempDir(), "regular")
	})
	t.Run("not executable", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "plain")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		mustRefuse(t, p, "executable")
	})
	t.Run("allowed file returns the final path", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		p := t11WriteExe(t, dir, "fakecli", 0o755)
		final, err := filepath.EvalSymlinks(p)
		require.NoError(t, err)
		resolved, err := check(p, forbidden)
		require.NoError(t, err)
		require.Equal(t, final, resolved, "spec §3.2: the returned file is the one callers launch")
	})
	t.Run("allowed symlink returns the final file not the link", func(t *testing.T) {
		// A symlink's own mode is 0777. The spec still allows a link whose
		// target is an ordinary executable outside the forbidden roots.
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		p := t11WriteExe(t, dir, "fakecli", 0o755)
		link := filepath.Join(dir, "link")
		require.NoError(t, os.Symlink(p, link))
		final, err := filepath.EvalSymlinks(link)
		require.NoError(t, err)
		resolved, err := check(link, forbidden)
		require.NoError(t, err)
		require.Equal(t, final, resolved)
	})
}

func t11PathListed(list []string, want string) bool {
	want = filepath.Clean(want)
	for _, g := range list {
		if filepath.Clean(g) == want {
			return true
		}
	}
	return false
}

func t11WriteExe(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	p := filepath.Join(dir, name)
	// A real executable bit is enough for the mode/location checks. The route
	// test is what actually launches a program.
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho ok\n"), mode))
	require.NoError(t, os.Chmod(p, mode))
	return p
}
