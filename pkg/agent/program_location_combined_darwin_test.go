//go:build darwin

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/require"
)

func TestCombinedProgramLocation_CaseSpelledMount_ActualDispatch_R4(t *testing.T) {
	var mountedRoot string
	f := t11cNativeFixtureFor(t, func(source string) string {
		base, err := filepath.EvalSymlinks(t11combined.TrustedProgramDir(t))
		require.NoError(t, err)
		mountedRoot = filepath.Join(base, "MountCase")
		alternate := filepath.Join(base, "mountcase")
		require.NotEqual(t, mountedRoot, alternate)
		require.NoError(t, os.MkdirAll(mountedRoot, 0o700))
		one, err := os.Stat(mountedRoot)
		require.NoError(t, err)
		two, err := os.Stat(alternate)
		require.NoError(t, err, "fixture requires a volume resolving the alternate case")
		require.True(t, os.SameFile(one, two), "instrument: same physical mount under different case")
		data, err := os.ReadFile(source)
		require.NoError(t, err)
		program := filepath.Join(mountedRoot, "combined-codex")
		require.NoError(t, os.WriteFile(program, data, 0o755))
		altProgram := filepath.Join(alternate, filepath.Base(program))
		a, err := os.Stat(program)
		require.NoError(t, err)
		b, err := os.Stat(altProgram)
		require.NoError(t, err)
		require.True(t, os.SameFile(a, b), "instrument: same executable, not a different file")
		return altProgram
	})
	mount, _, err := workspace.CreateMount(f.home, f.ts.opts.WorkspaceID, "case-mount", mountedRoot)
	require.NoError(t, err)
	require.Equal(t, mountedRoot, mount.HostPath, "instrument: saved mount retains the configured case")
	t11cAssertNativeRefused(t, f, "agent_writable_location")
}
