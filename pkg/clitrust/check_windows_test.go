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
	"golang.org/x/sys/windows"
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
		// Spec §3.2 refuses an 8.3 short name before the location check.
		// GitHub TEMP is often C:\\Users\\RUNNER~1\\..., so this fixture is a long path.
		home := t11LongDir(t)
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
		dir := t11LongDir(t)
		p := filepath.Join(dir, "codex.exe")
		require.NoError(t, os.WriteFile(p, []byte("MZ"), 0o755))
		final, err := filepath.EvalSymlinks(p)
		require.NoError(t, err)
		resolved, err := check(p, nil)
		require.NoError(t, err)
		// Spec §3.2: callers launch the resolved final file. EvalSymlinks is
		// that file when a parent folder is a junction.
		require.Equal(t, final, resolved)
	})
}

// t11LongDir is a temp directory whose path has no 8.3 component. Spec §3.2
// refuses "~" followed by a digit, so a location case built under the usual
// short TEMP would fail for the wrong reason.
func t11LongDir(t *testing.T) string {
	t.Helper()
	parent := t11ToLongPath(t, os.TempDir())
	dir, err := os.MkdirTemp(parent, "t11long")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	long := t11ToLongPath(t, dir)
	if t11HasShortComponent(long) {
		t.Fatalf("spec §3.2 refuses 8.3 names; fixture %q still has one, so this case would not test location", long)
	}
	return long
}

func t11HasShortComponent(p string) bool {
	rest := p[len(filepath.VolumeName(p)):]
	for _, c := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		for i := 0; i+1 < len(c); i++ {
			if c[i] == '~' && c[i+1] >= '0' && c[i+1] <= '9' {
				return true
			}
		}
	}
	return false
}

func t11ToLongPath(t *testing.T, path string) string {
	t.Helper()
	p16, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	buf := make([]uint16, 32768)
	n, err := windows.GetLongPathName(p16, &buf[0], uint32(len(buf)))
	require.NoError(t, err)
	if n == 0 || int(n) > len(buf) {
		t.Fatalf("GetLongPathName(%q) returned length %d", path, n)
	}
	return windows.UTF16ToString(buf[:n])
}
