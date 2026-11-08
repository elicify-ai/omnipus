//go:build darwin || linux

package thirdpartycli_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Combine plan §1.1: relative roots must not silently disappear at the checker.
func TestSharedProgramPath_RelativeForbiddenRootRefused(t *testing.T) {
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(cwd)
	t.Setenv(config.EnvHome, t.TempDir())
	program := t11WriteExe(t, filepath.Join(cwd, "agent-root"), "cli", 0o755)
	resolved, checkErr := thirdpartycli.ValidateProgramPath("claude-code", program, "agent-root")
	require.Error(t, checkErr, "Q28 union: relative forbidden root is absolute against the working directory")
	require.Empty(t, resolved)
	t11RequireCheck(t, checkErr, "agent_writable_location")
}

func TestSharedAgentWritableRoots_UnionAndAbsoluteAllowedPaths(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	userHome, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("HOME", userHome)
	t.Setenv(config.EnvHome, home)
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(cwd)
	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{{ID: "union-home", Home: filepath.Join(cwd, "agent-home")}}
	cfg.Agents.Defaults.Home = filepath.Join(cwd, "default-home")
	cfg.Sandbox.AllowedPaths = []string{"relative-grant", "~/tilde-grant"}
	roots, rootsErr := thirdpartycli.AgentWritableRoots(home, cfg)
	require.NoError(t, rootsErr)
	for _, want := range []string{home, cfg.Agents.List[0].Home, cfg.Agents.Defaults.Home,
		filepath.Join(cwd, "relative-grant"), filepath.Join(userHome, "tilde-grant")} {
		assert.Contains(t, roots, want, "plan §1: union includes the absolute expanded spelling")
	}
	for _, root := range roots {
		assert.True(t, filepath.IsAbs(root), "plan §1: no relative root survives collection: %q", root)
		assert.False(t, strings.HasPrefix(root, "~"), "expand ~ before filepath.Abs")
	}
}

// Combine plan §1.2 and Q37: an existing root that cannot be examined is not
// silently ignored. ELOOP is deterministic even for privileged test users.
func TestSharedProgramPath_UnexaminableRootRefused_Q37(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv(config.EnvHome, t.TempDir())
	program := t11WriteExe(t, base, "allowed-cli", 0o755)
	loop := filepath.Join(base, "loop-root")
	require.NoError(t, os.Symlink("loop-root", loop))
	_, err = os.Stat(loop)
	require.Error(t, err, "instrument: real root inspection fails")
	require.NotErrorIs(t, err, fs.ErrNotExist, "instrument: this is NOT a missing root")
	resolved, controlErr := thirdpartycli.ValidateProgramPath("codex", program)
	require.NoError(t, controlErr, "control: same file is valid without the broken root")
	require.Equal(t, program, resolved)
	t.Run("check", func(t *testing.T) {
		resolved, checkErr := thirdpartycli.CheckProgramPath("codex", program, []string{loop}, nil)
		require.Error(t, checkErr, "an unexaminable root must refuse every otherwise valid program")
		require.Empty(t, resolved)
		t11RequireCheck(t, checkErr, "location_unverifiable")
		var ce *thirdpartycli.CheckError
		require.ErrorAs(t, checkErr, &ce)
		assert.Equal(t, program, ce.At, "plan §1.2: entered path, not the broken root")
		assert.Equal(t, t11combined.CodexSaveRefusal, ce.SaveRefusal())
	})
	t.Run("run", func(t *testing.T) {
		resolved, refusal := thirdpartycli.RunProgram("codex", program, []string{loop}, nil)
		assert.Empty(t, resolved)
		assert.Regexp(t, `^This agent cannot run: `, refusal)
		assert.Contains(t, refusal, "location_unverifiable")
		assert.Contains(t, refusal, t11combined.LocationUnverifiableCause)
		assert.NotContains(t, refusal, "Omnipus could not read the list of mounted folders")
	})
	t.Run("warning", func(t *testing.T) {
		warning := thirdpartycli.ProgramWarning("codex", program, []string{loop}, nil)
		assert.Regexp(t, `^This agent cannot run: `, warning)
		assert.Contains(t, warning, "location_unverifiable")
		assert.Contains(t, warning, t11combined.LocationUnverifiableCause)
		assert.NotContains(t, warning, "Omnipus could not read the list of mounted folders")
	})
}
