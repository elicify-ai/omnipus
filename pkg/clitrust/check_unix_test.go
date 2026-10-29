//go:build darwin || linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package clitrust_test

import (
	"fmt"
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

// TestCheckProgramPath_GroupWritableDirectory is S4 / IMP-1. A file that is
// itself not group-writable is still refused when its directory, or the
// directory of a link to it, is group-writable (mode 0775). Spec §3.2 / §5.
func TestCheckProgramPath_GroupWritableDirectory(t *testing.T) {
	check, _ := loadT11CLITrust(t)

	t.Run("parent directory group writable", func(t *testing.T) {
		dir := t.TempDir()
		p := t11WriteExe(t, dir, "fakecli", 0o755)
		require.NoError(t, os.Chmod(dir, 0o775))
		resolved, err := check(p, nil)
		require.Error(t, err, "spec §3.2: a group-writable directory can replace the program")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "writable")
	})

	t.Run("link folder group writable", func(t *testing.T) {
		finalDir := t.TempDir()
		final := t11WriteExe(t, finalDir, "fakecli", 0o755)
		require.NoError(t, os.Chmod(finalDir, 0o700))
		linkDir := t.TempDir()
		require.NoError(t, os.Chmod(linkDir, 0o775))
		link := filepath.Join(linkDir, "link")
		require.NoError(t, os.Symlink(final, link))
		resolved, err := check(link, nil)
		require.Error(t, err, "spec §3.2: a group-writable link folder can retarget the program")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "writable")
	})
}

// TestAgentWritableRoots_RuntimeHomeSpelling is S3. The forbidden root is the
// home the runtime actually uses: "~/" expands to the user home, and
// surrounding whitespace is trimmed. A program at that location is refused.
func TestAgentWritableRoots_RuntimeHomeSpelling(t *testing.T) {
	check, roots := loadT11CLITrust(t)

	t.Run("tilde spelling", func(t *testing.T) {
		userHome, err := os.UserHomeDir()
		require.NoError(t, err)
		name := fmt.Sprintf("t11-s3-%d", os.Getpid())
		dir := filepath.Join(userHome, name)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		prog := t11WriteExe(t, dir, "fakecli", 0o755)
		cfg := &config.Config{}
		cfg.Agents.List = []config.AgentConfig{{ID: "t11s3", Home: "~/" + name}}
		resolved, err := check(prog, roots("", cfg))
		require.Error(t, err, "spec §3.2: ~/ spelling is the same writable home")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "home")
	})

	t.Run("surrounding whitespace", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		prog := t11WriteExe(t, dir, "fakecli", 0o755)
		cfg := &config.Config{}
		cfg.Agents.Defaults.Home = "  " + dir + "  "
		resolved, err := check(prog, roots("", cfg))
		require.Error(t, err, "spec §3.2: whitespace around a home is not a different directory")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "home")
	})
}
