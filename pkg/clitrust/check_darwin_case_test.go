//go:build darwin

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
	"github.com/stretchr/testify/require"
)

// TestCheckProgramPath_CaseSpellingOfForbiddenRoot is spec §3.2. A program
// planted in an agent working folder is refused under every spelling the
// volume resolves to that same folder. On a case-insensitive volume the
// different-case spelling is the same file. The program is never launched.
// A case-sensitive volume cannot host that spelling; the test records the
// measured os.SameFile result and skips only then.
func TestCheckProgramPath_CaseSpellingOfForbiddenRoot(t *testing.T) {
	check, rootsFn := loadT11CLITrust(t)
	base := t.TempDir()
	root := filepath.Join(base, "AgentHome")
	require.NoError(t, os.MkdirAll(root, 0o700))

	probe := filepath.Join(base, "CaseProbe")
	require.NoError(t, os.WriteFile(probe, []byte("probe"), 0o600))
	altProbe := filepath.Join(base, "caseprobe")
	fi1, err1 := os.Lstat(probe)
	fi2, err2 := os.Lstat(altProbe)
	same := err1 == nil && err2 == nil && os.SameFile(fi1, fi2)
	t.Logf("volume property: os.SameFile(CaseProbe, caseprobe)=%v", same)
	if !same {
		t.Skip("measured case-sensitive volume (os.SameFile is false); a different-case spelling is a different directory")
	}

	prog := t11WriteExe(t, root, "fakecli", 0o755)
	info, err := os.Lstat(prog)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular(), "fixture: the planted program is a regular file and is not launched")

	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{{ID: "t11case", Home: root}}
	roots, err := rootsFn(t.TempDir(), cfg)
	require.NoError(t, err)
	require.Truef(t, t11PathListed(roots, root), "the agent home is a forbidden root; got %v", roots)

	t.Run("correct spelling is refused", func(t *testing.T) {
		resolved, err := check(prog, roots)
		require.Error(t, err, "spec §3.2: a program inside the agent home is refused")
		require.Empty(t, resolved, "a refusal returns no file to launch")
		require.Contains(t, strings.ToLower(err.Error()), "home")
	})

	t.Run("different-case spelling of the same root is refused", func(t *testing.T) {
		altRoot := filepath.Join(filepath.Dir(root), strings.ToLower(filepath.Base(root)))
		altProg := filepath.Join(altRoot, "fakecli")
		a, err := os.Lstat(root)
		require.NoError(t, err)
		b, err := os.Lstat(altRoot)
		require.NoError(t, err)
		require.True(t, os.SameFile(a, b), "fixture: the two root spellings are the same directory")
		pa, err := os.Lstat(prog)
		require.NoError(t, err)
		pb, err := os.Lstat(altProg)
		require.NoError(t, err)
		require.True(t, os.SameFile(pa, pb), "fixture: the two program spellings are the same file")
		resolved, err := check(altProg, roots)
		require.Error(t, err, "spec §3.2: a different-case spelling of a forbidden root is still inside it")
		require.Empty(t, resolved, "a refusal returns no file to launch")
	})

	t.Run("program outside the roots is allowed", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		outside := t11WriteExe(t, dir, "fakecli", 0o755)
		final, err := filepath.EvalSymlinks(outside)
		require.NoError(t, err)
		resolved, err := check(outside, roots)
		require.NoError(t, err, "spec §3.2: a program outside the forbidden roots is allowed")
		require.Equal(t, final, resolved)
	})
}
