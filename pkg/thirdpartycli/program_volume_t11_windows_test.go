//go:build windows

package thirdpartycli

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// Spec §3.2 / shared-location rule: separate volumes are not containment.
// This exercises the shared decision without assuming a second mounted volume;
// it does not certify cross-volume filesystem inspection or process execution.
func TestSharedProgramPath_WindowsVolumeContainment(t *testing.T) {
	for _, tc := range []struct { // not-wire-format: lexical decision cases
		name, root, program string
		inside              bool
	}{
		{"separate_volumes", `C:\fixture\writable`, `D:\fixture\installed\program.exe`, false},
		{"separate_volumes_reversed", `D:\fixture\writable`, `C:\fixture\installed\program.exe`, false},
		{"same_volume_outside", `C:\fixture\writable`, `C:\fixture\installed\program.exe`, false},
		{"same_volume_inside", `C:\fixture\writable`, `C:\fixture\writable\program.exe`, true},
		{"root_itself", `C:\fixture\writable`, `C:\fixture\writable`, true},
		{"sibling_is_outside", `C:\fixture\writable`, `C:\fixture\writable-other\program.exe`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.inside, pathWithin(tc.root, tc.program),
				"spec §3.2: only equality or an actual descendant is inside")
		})
	}
}

// The volume distinction must not turn real containment or examination errors
// into permission. These controls use actual files on the runner's one volume.
func TestSharedProgramPath_WindowsRootDecisions(t *testing.T) {
	base := t11cWindowsLongDir(t)
	t.Setenv(config.EnvHome, t11cWindowsLongDir(t))
	writable := filepath.Join(base, "writable")
	installed := filepath.Join(base, "installed")
	require.NoError(t, os.MkdirAll(writable, 0o700))
	require.NoError(t, os.MkdirAll(installed, 0o700))
	self, executableErr := os.Executable()
	require.NoError(t, executableErr)
	image, imageErr := os.ReadFile(self)
	require.NoError(t, imageErr)
	outside := filepath.Join(installed, "program.exe")
	inside := filepath.Join(writable, "program.exe")
	for _, path := range []string{outside, inside} {
		require.NoError(t, os.WriteFile(path, image, 0o700))
	}
	t.Run("outside_control", func(t *testing.T) {
		resolved, err := CheckProgramPath("codex", outside, []string{writable}, nil)
		require.NoError(t, err)
		want, err := filepath.EvalSymlinks(outside)
		require.NoError(t, err)
		require.Equal(t, want, resolved)
	})
	t.Run("inside_refused", func(t *testing.T) {
		resolved, err := CheckProgramPath("codex", inside, []string{writable}, nil)
		want, resolveErr := filepath.EvalSymlinks(inside)
		require.NoError(t, resolveErr)
		t11cWindowsCheck(t, resolved, err, "agent_writable_location", want)
	})
	t.Run("examination_error_refused", func(t *testing.T) {
		// An invalid root spelling gives a real examination error, not a missing
		// folder or a mocked collector error. It does not prove ACL/I/O cases.
		unexaminable := filepath.Join(base, "unexaminable\x00root")
		_, statErr := os.Stat(unexaminable)
		require.Error(t, statErr, "instrument: the operating system cannot examine this root")
		require.NotErrorIs(t, statErr, fs.ErrNotExist, "a missing root is a different specified state")
		resolved, err := CheckProgramPath("codex", outside, []string{unexaminable}, nil)
		t11cWindowsCheck(t, resolved, err, "location_unverifiable", outside)
	})
}

func t11cWindowsCheck(t *testing.T, resolved string, err error, check, at string) {
	t.Helper()
	require.Error(t, err)
	require.Empty(t, resolved, "refusal returns no file to launch")
	var ce *CheckError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, "codex", ce.CLI)
	require.Equal(t, check, ce.Check)
	require.Equal(t, at, ce.At)
}

func t11cWindowsLongDir(t *testing.T) string {
	t.Helper()
	path, err := windows.UTF16PtrFromString(t.TempDir())
	require.NoError(t, err)
	buf := make([]uint16, 32768)
	n, err := windows.GetLongPathName(path, &buf[0], uint32(len(buf)))
	require.NoError(t, err)
	require.Greater(t, n, uint32(0))
	require.Less(t, n, uint32(len(buf)))
	return windows.UTF16ToString(buf[:n])
}
