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

	gotRoots, rootsErr := roots(home, &config.Config{})
	require.NoError(t, rootsErr)
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
	// Founder Q2b, spec §3.2 / §5: who can write the program is not checked.
	mustAllow := func(t *testing.T, path string) {
		t.Helper()
		final, err := filepath.EvalSymlinks(path)
		require.NoError(t, err)
		resolved, err := check(path, forbidden)
		require.NoError(t, err, "spec §3.2 founder Q2b: mode is not a reason to refuse %s", path)
		require.Equal(t, final, resolved, "spec §3.2: the returned file is the one callers launch")
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
	t.Run("group-writable file outside forbidden roots is allowed", func(t *testing.T) {
		p := t11WriteExe(t, t.TempDir(), "fakecli", 0o775)
		fi, err := os.Stat(p)
		require.NoError(t, err)
		require.NotZero(t, fi.Mode().Perm()&0o020, "fixture: the file is group-writable")
		mustAllow(t, p)
	})
	t.Run("other-writable file outside forbidden roots is allowed", func(t *testing.T) {
		p := t11WriteExe(t, t.TempDir(), "fakecli", 0o707)
		fi, err := os.Stat(p)
		require.NoError(t, err)
		require.NotZero(t, fi.Mode().Perm()&0o002, "fixture: the file is writable by others")
		mustAllow(t, p)
	})
	t.Run("other-writable parent outside forbidden roots is allowed", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o707))
		fi, err := os.Stat(dir)
		require.NoError(t, err)
		require.NotZero(t, fi.Mode().Perm()&0o002, "fixture: the parent is writable by others")
		mustAllow(t, t11WriteExe(t, dir, "fakecli", 0o755))
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
		// A symlink's own mode is 0777. Spec §3.2 founder Q2b does not look
		// at mode: a link outside the forbidden roots is allowed.
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

// TestCheckProgramPath_GroupWritableOutsideRootsAllowed is the founder Q2b
// positive control (spec §3.2 / §5). A group-writable directory, and a
// group-writable folder that holds a link, used to be refused. They are
// allowed when neither the file, the link, nor their folders sit inside a
// forbidden root. Mode is not a check anymore. The sticky-folder cases were
// removed with that rule: sticky was only an exception to a permission check.
func TestCheckProgramPath_GroupWritableOutsideRootsAllowed(t *testing.T) {
	check, _ := loadT11CLITrust(t)

	t.Run("parent directory group writable", func(t *testing.T) {
		dir := t.TempDir()
		p := t11WriteExe(t, dir, "fakecli", 0o755)
		require.NoError(t, os.Chmod(dir, 0o775))
		fi, err := os.Stat(dir)
		require.NoError(t, err)
		require.NotZero(t, fi.Mode().Perm()&0o020, "fixture: the parent is group-writable")
		final, err := filepath.EvalSymlinks(p)
		require.NoError(t, err)
		resolved, err := check(p, nil)
		require.NoError(t, err, "spec §3.2 founder Q2b: a group-writable directory outside the forbidden roots is allowed")
		require.Equal(t, final, resolved)
	})

	t.Run("link folder group writable", func(t *testing.T) {
		finalDir := t.TempDir()
		final := t11WriteExe(t, finalDir, "fakecli", 0o755)
		require.NoError(t, os.Chmod(finalDir, 0o700))
		linkDir := t.TempDir()
		require.NoError(t, os.Chmod(linkDir, 0o775))
		fi, err := os.Stat(linkDir)
		require.NoError(t, err)
		require.NotZero(t, fi.Mode().Perm()&0o020, "fixture: the link folder is group-writable")
		link := filepath.Join(linkDir, "link")
		require.NoError(t, os.Symlink(final, link))
		resolved, err := check(link, nil)
		require.NoError(t, err, "spec §3.2 founder Q2b: a group-writable link folder outside the forbidden roots is allowed")
		want, err := filepath.EvalSymlinks(final)
		require.NoError(t, err)
		require.Equal(t, want, resolved, "callers launch the resolved file, not the link")
	})
}

// TestAgentWritableRoots_RuntimeHomeSpelling is S3. Forbidden roots use the
// runtime's own home resolution. "~/" expands to the user home. A per-agent
// Home is trimmed; the default home is kept as written, and its trimmed
// spelling is forbidden as well so a differently spelled path is still refused.
func TestAgentWritableRoots_RuntimeHomeSpelling(t *testing.T) {
	check, roots := loadT11CLITrust(t)

	t.Run("tilde spelling", func(t *testing.T) {
		t.Setenv("OMNIPUS_HOME", "")
		userHome, err := os.UserHomeDir()
		require.NoError(t, err)
		name := fmt.Sprintf("t11-s3-%d", os.Getpid())
		dir := filepath.Join(userHome, name)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		prog := t11WriteExe(t, dir, "fakecli", 0o755)
		cfg := &config.Config{}
		cfg.Agents.List = []config.AgentConfig{{ID: "t11s3", Home: "~/" + name}}
		resolved, err := check(prog, t11MustRoots(t, roots, t.TempDir(), cfg))
		require.Error(t, err, "spec §3.2: ~/ spelling is the same writable home")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "home")
	})

	t.Run("per-agent home is trimmed", func(t *testing.T) {
		t.Setenv("OMNIPUS_HOME", "")
		userHome, err := os.UserHomeDir()
		require.NoError(t, err)
		name := fmt.Sprintf("t11-s3b-%d", os.Getpid())
		dir := filepath.Join(userHome, name)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		prog := t11WriteExe(t, dir, "fakecli", 0o755)
		cfg := &config.Config{}
		padded := "  ~/" + name + "  "
		cfg.Agents.List = []config.AgentConfig{{ID: "t11s3b", Home: padded}}
		got := t11MustRoots(t, roots, t.TempDir(), cfg)
		require.Truef(t, t11PathListed(got, dir),
			"spec §3.2: a per-agent home is trimmed and ~/ is expanded; %s not in %v", dir, got)
		require.Falsef(t, t11PathListed(got, padded),
			"the runtime trims a per-agent home, so the padded spelling is not a root; got %v", got)
		resolved, err := check(prog, got)
		require.Error(t, err, "spec §3.2: the trimmed per-agent home is forbidden")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "home")
	})

	t.Run("default home is not trimmed", func(t *testing.T) {
		// S3: the runtime does not trim Defaults.Home. Every forbidden root is
		// then made absolute against the gateway's working directory. The relative
		// spelling itself is not a root; both absolute spellings are.
		cwd := t.TempDir()
		t.Chdir(cwd)
		rel := "t11-default-home"
		dir := filepath.Join(cwd, rel)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		prog := t11WriteExe(t, dir, "fakecli", 0o755)
		cfg := &config.Config{}
		padded := "  " + rel + "  "
		cfg.Agents.Defaults.Home = padded
		got := t11MustRoots(t, roots, t.TempDir(), cfg)
		absPadded, err := filepath.Abs(padded)
		require.NoError(t, err)
		absTrim, err := filepath.Abs(rel)
		require.NoError(t, err)
		require.Truef(t, filepath.IsAbs(absPadded) && filepath.IsAbs(absTrim), "both spellings are absolute")
		require.Truef(t, t11PathListed(got, absPadded), "the untrimmed default home, made absolute; got %v", got)
		require.Truef(t, t11PathListed(got, absTrim), "spec §3.2: the trimmed spelling is also forbidden; got %v", got)
		require.Falsef(t, t11PathListed(got, padded), "a relative spelling is not itself a root; got %v", got)
		resolved, err := check(prog, got)
		require.Error(t, err, "spec §3.2: a program at the trimmed absolute home is refused")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "home")
	})

	t.Run("relative agent home is forbidden at its absolute path", func(t *testing.T) {
		cwd := t.TempDir()
		t.Chdir(cwd)
		rel := "t11-agent-rel"
		dir := filepath.Join(cwd, rel)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		prog := t11WriteExe(t, dir, "fakecli", 0o755)
		cfg := &config.Config{}
		cfg.Agents.List = []config.AgentConfig{{ID: "t11rel", Home: rel}}
		got := t11MustRoots(t, roots, t.TempDir(), cfg)
		require.Truef(t, t11PathListed(got, dir), "spec §3.2 S3: the relative home is stored absolute; got %v", got)
		require.Falsef(t, t11PathListed(got, rel), "the relative spelling is not a root; got %v", got)
		resolved, err := check(prog, got)
		require.Error(t, err, "spec §3.2: a program inside a relative agent home is refused")
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "home")
	})
}

// TestAgentWritableRoots_IncompleteListRefuses is S6. An empty data folder, or
// a workspace list that cannot be read, is an error and not a shorter list:
// a missing root would turn a refusal into an allow.
func TestAgentWritableRoots_IncompleteListRefuses(t *testing.T) {
	_, roots := loadT11CLITrust(t)

	t.Run("blank data folder", func(t *testing.T) {
		for _, home := range []string{"", "  "} {
			got, err := roots(home, &config.Config{})
			require.Errorf(t, err, "spec §3.2: data folder %q cannot list the folders agents can write", home)
			require.Empty(t, got, "a failed list must not be usable as an allow-list")
		}
	})

	t.Run("workspaces path is not a directory", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces"), []byte("not-a-directory"), 0o600))
		got, err := roots(home, &config.Config{})
		require.Error(t, err, "spec §3.2: a workspace list that cannot be read fails closed")
		require.Empty(t, got)
		require.Contains(t, strings.ToLower(err.Error()), "workspace")
	})
}

func t11MustRoots(t *testing.T, roots func(string, *config.Config) ([]string, error), home string, cfg *config.Config) []string {
	t.Helper()
	got, err := roots(home, cfg)
	require.NoError(t, err)
	return got
}
