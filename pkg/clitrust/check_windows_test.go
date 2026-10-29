//go:build windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package clitrust_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCheckProgramPath_Windows is the spec's Windows matrix (full spec T-A44w,
// slim spec §3.2): absolute drive or UNC, .exe/.cmd/.bat only, no 8.3 short
// names, no alternate data streams, nothing inside a workspace.
func TestCheckProgramPath_Windows(t *testing.T) {
	check, _ := loadT11CLITrust(t)

	mustRefuse := func(t *testing.T, path, needle string) {
		t.Helper()
		resolved, err := check(path, nil)
		require.Error(t, err, "spec §3.2: %s must be refused", path)
		require.Empty(t, resolved)
		low := strings.ToLower(err.Error())
		ok := false
		for _, n := range strings.Split(needle, "|") {
			if strings.Contains(low, n) {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("spec §3.2: error %q must name the failed check %q", err, needle)
		}
	}

	t.Run("relative path", func(t *testing.T) {
		mustRefuse(t, `codex.exe`, "absolute")
	})
	t.Run("ps1 refused", func(t *testing.T) {
		mustRefuse(t, `C:\Tools\codex.ps1`, "extension")
	})
	t.Run("8.3 short name", func(t *testing.T) {
		mustRefuse(t, `C:\PROGRA~1\codex.exe`, "8.3|short")
	})
	t.Run("alternate data stream", func(t *testing.T) {
		mustRefuse(t, `C:\Tools\codex.exe:stream`, "alternate|data stream")
	})
	t.Run("inside a workspace", func(t *testing.T) {
		home := t.TempDir()
		ws := filepath.Join(home, "workspaces", "t11", "work")
		require.NoError(t, os.MkdirAll(ws, 0o700))
		p := filepath.Join(ws, "codex.exe")
		require.NoError(t, os.WriteFile(p, []byte("MZ"), 0o755))
		resolved, err := check(p, []string{filepath.Join(home, "workspaces", "t11")})
		require.Error(t, err)
		require.Empty(t, resolved)
		require.Contains(t, strings.ToLower(err.Error()), "workspace")
	})
	t.Run("unc is absolute", func(t *testing.T) {
		// No server is required. UNC must not be rejected as a non-absolute path.
		_, err := check(`\\server\share\codex.exe`, nil)
		require.Error(t, err, "a missing UNC file is still not a runnable program")
		require.NotContains(t, strings.ToLower(err.Error()), "absolute",
			"spec §3.2: a UNC path is an absolute path")
	})
	t.Run("exe outside forbidden roots", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "codex.exe")
		require.NoError(t, os.WriteFile(p, []byte("MZ"), 0o755))
		resolved, err := check(p, nil)
		require.NoError(t, err)
		require.Equal(t, p, resolved)
	})
}
