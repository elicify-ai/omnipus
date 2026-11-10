//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

// Round 6, CK-1. r17 3.5 and 3.6: ValidateProgramPath returns the FULLY
// resolved checked file, which is the file that is executed, and checks the
// containing folder of every symlink on the way. A directory component that is
// a symlink must therefore be unfolded in the returned path.
// Unix-only because it needs Unix symlinks and permission bits; Windows
// coverage of the same rows is the Windows fixtures in pkg/agent.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t8DirLinkFixture struct {
	root, safeDir, otherDir, link string
}

func t8WriteExe(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	require.NoError(t, os.Chmod(path, mode))
}

func t8DirLink(t *testing.T) t8DirLinkFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", filepath.Join(t.TempDir(), "omnipus-home"))
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	f := t8DirLinkFixture{root: root, safeDir: filepath.Join(root, "a"), otherDir: filepath.Join(root, "b"), link: filepath.Join(root, "tools")}
	for _, d := range []string{f.safeDir, f.otherDir} {
		require.NoError(t, os.Mkdir(d, 0o755))
		require.NoError(t, os.Chmod(d, 0o755))
	}
	t8WriteExe(t, filepath.Join(f.safeDir, "codex"), 0o700)
	return f
}

func t8RequireCheck(t *testing.T, got string, err error, want string) {
	t.Helper()
	require.Error(t, err, "expected refusal %s; returned %q", want, got)
	require.IsType(t, &CheckError{}, err, "r17 3.5: the error is a *CheckError directly, not wrapped")
	var ce *CheckError
	require.True(t, errors.As(err, &ce), "%T", err)
	assert.Equal(t, want, ce.Check)
}

// 3.5/3.6: a directory link to a safe directory returns the resolved file.
func TestValidateProgramPath_DirectoryLink_ReturnsResolvedFile_3_5(t *testing.T) {
	f := t8DirLink(t)
	require.NoError(t, os.Symlink(f.safeDir, f.link))
	entered := filepath.Join(f.link, "codex")

	got, err := ValidateProgramPath("codex", entered)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(f.safeDir, "codex"), got, "the resolved file, not the entered path through the directory link")
	assert.NotEqual(t, entered, got)
}

// 3.6: retargeting the directory link after validation must not change which
// file the returned path names.
func TestValidateProgramPath_DirectoryLinkRetargetAfterCheck_3_6(t *testing.T) {
	f := t8DirLink(t)
	t8WriteExe(t, filepath.Join(f.otherDir, "codex"), 0o777) // an unchecked, world-writable file
	require.NoError(t, os.Symlink(f.safeDir, f.link))
	checked, err := os.Stat(filepath.Join(f.safeDir, "codex"))
	require.NoError(t, err)

	got, err := ValidateProgramPath("codex", filepath.Join(f.link, "codex"))
	require.NoError(t, err)
	require.NoError(t, os.Remove(f.link))
	require.NoError(t, os.Symlink(f.otherDir, f.link))

	after, err := os.Stat(got)
	require.NoError(t, err)
	assert.True(t, os.SameFile(checked, after), "the returned path %q still names the checked file after the directory link moved", got)
}

// Founder Q13 A / Q2b supersedes the old permission gate: a world-writable
// container outside agent-writable roots is allowed. Full resolution still holds.
func TestValidateProgramPath_DirectoryLinkIntoWorldWritableDir_Allowed_Q13(t *testing.T) {
	f := t8DirLink(t)
	t8WriteExe(t, filepath.Join(f.otherDir, "codex"), 0o700)
	require.NoError(t, os.Chmod(f.otherDir, 0o777))
	require.NoError(t, os.Symlink(f.otherDir, f.link))

	got, err := ValidateProgramPath("codex", filepath.Join(f.link, "codex"))
	require.NoError(t, err, "Q13 A: other users' write permission is not a program-path policy")
	assert.Equal(t, filepath.Join(f.otherDir, "codex"), got, "the fully resolved executable is still returned")
}

// Founder Q13 A / Q2b: a link's containing folder is not permission-gated.
// Forbidden agent-writable location and link-chain checks remain separate.
func TestValidateProgramPath_DirectoryLinkInWorldWritableFolder_Allowed_Q13(t *testing.T) {
	f := t8DirLink(t)
	shared := filepath.Join(f.root, "shared")
	require.NoError(t, os.Mkdir(shared, 0o755))
	require.NoError(t, os.Chmod(shared, 0o777))
	link := filepath.Join(shared, "tools")
	require.NoError(t, os.Symlink(f.safeDir, link))

	got, err := ValidateProgramPath("codex", filepath.Join(link, "codex"))
	require.NoError(t, err, "Q13 A: a world-writable link folder outside forbidden roots is allowed")
	assert.Equal(t, filepath.Join(f.safeDir, "codex"), got)
}

// 3.5: a directory link into an agent-writable location is refused.
func TestValidateProgramPath_DirectoryLinkIntoAgentWritable_Refused_3_5(t *testing.T) {
	f := t8DirLink(t)
	t8WriteExe(t, filepath.Join(f.otherDir, "codex"), 0o700)
	require.NoError(t, os.Symlink(f.otherDir, f.link))

	got, err := ValidateProgramPath("codex", filepath.Join(f.link, "codex"), f.otherDir)
	t8RequireCheck(t, got, err, CheckAgentWritableLocation)
}

// Control: a final-component link resolves to the target file.
func TestValidateProgramPath_FinalLink_ReturnsTarget_Control(t *testing.T) {
	f := t8DirLink(t)
	link := filepath.Join(f.root, "codex-link")
	require.NoError(t, os.Symlink(filepath.Join(f.safeDir, "codex"), link))

	got, err := ValidateProgramPath("codex", link)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(f.safeDir, "codex"), got)
}

// Founder Q13 A / Q2b also removes the file-permission gate, not only the
// folder-permission gate. The file must still be a regular executable.
func TestValidateProgramPath_FinalLinkToWorldWritableFile_Allowed_Q13(t *testing.T) {
	f := t8DirLink(t)
	t8WriteExe(t, filepath.Join(f.otherDir, "codex"), 0o777)
	link := filepath.Join(f.root, "codex-link")
	require.NoError(t, os.Symlink(filepath.Join(f.otherDir, "codex"), link))

	got, err := ValidateProgramPath("codex", link)
	require.NoError(t, err, "Q13 A: who else can write this executable is the person's responsibility")
	assert.Equal(t, filepath.Join(f.otherDir, "codex"), got)
}
