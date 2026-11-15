//go:build windows

package thirdpartycli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Spec §3.2's link/location rule is normative. This real junction fixture
// supplies the Windows receipt needed to check the user docs' limitation;
// a static compile alone cannot establish whether that limitation is accurate.
func TestSharedProgramPath_WindowsJunctionLocation(t *testing.T) {
	base := t11cWindowsLongDir(t)
	t.Setenv(config.EnvHome, t11cWindowsLongDir(t))
	writable := filepath.Join(base, "writable")
	installed := filepath.Join(base, "installed")
	require.NoError(t, os.MkdirAll(writable, 0o700))
	require.NoError(t, os.MkdirAll(installed, 0o700))
	self, err := os.Executable()
	require.NoError(t, err)
	image, err := os.ReadFile(self)
	require.NoError(t, err)
	protected := filepath.Join(writable, "program.exe")
	allowed := filepath.Join(installed, "program.exe")
	for _, path := range []string{protected, allowed} {
		require.NoError(t, os.WriteFile(path, image, 0o700))
	}
	resolved, err := CheckProgramPath("codex", allowed, []string{writable}, nil)
	require.NoError(t, err, "control: the real program outside the writable folder is admitted")
	wantAllowed, err := filepath.EvalSymlinks(allowed)
	require.NoError(t, err)
	require.Equal(t, wantAllowed, resolved)
	wantProtected, err := filepath.EvalSymlinks(protected)
	require.NoError(t, err)
	resolved, err = CheckProgramPath("codex", protected, []string{writable}, nil)
	t11cWindowsCheck(t, resolved, err, "agent_writable_location", wantProtected)

	junction := filepath.Join(base, "folder-link")
	cmd := exec.CommandContext(t.Context(), "cmd.exe", "/D", "/C", "mklink", "/J", junction, writable)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "junction fixture must be created, never silently skipped: %s", output)
	defer func() { require.NoError(t, os.Remove(junction), "remove only the sandbox-contained junction") }()
	via := filepath.Join(junction, "program.exe")
	physical, err := filepath.EvalSymlinks(via)
	require.NoError(t, err, "instrument: the real Windows junction resolves")
	require.Equal(t, wantProtected, physical, "instrument: junction and direct fixture reach the same file")
	viaInfo, err := os.Stat(via)
	require.NoError(t, err)
	targetInfo, err := os.Stat(protected)
	require.NoError(t, err)
	require.True(t, os.SameFile(viaInfo, targetInfo), "instrument: physical identity, not a textual-path assumption")
	resolved, err = CheckProgramPath("codex", via, []string{writable}, nil)
	t11cWindowsCheck(t, resolved, err, "agent_writable_location", wantProtected)
}
