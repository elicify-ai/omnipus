//go:build !windows

package agent

// Founder Q12 A / S2; T11 §5. A grant added AFTER save must be noticed at
// the next real dispatch. Positive control executes the same harmless file
// before the real persisted mount grant exists.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalDispatch_MountAddedAfterSaveRefusesBeforeProcess_S2(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	al, ts := newExternalTestLoop(t, "codex", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	program, marker := t11bNativeProgram(t, t11bNativeCLIs()[1], "codex-mount-after-save")
	t11bSaveRunSettings(t, al, "codex", program, "")
	require.Empty(t, al.GetConfig().Sandbox.AllowedPaths, "isolate actual mount grants from allowed_paths")
	ws, ok := workspace.FindForAgent(home, ts.agent.ID)
	require.True(t, ok, "instrument: the real dispatching agent belongs to this workspace")
	ts.opts.WorkspaceID = ws
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := runExternalCLISubTurn(ctx, al, ts, "valid saved program before mount")
	require.NoError(t, err)
	before := t11bNativeCalls(t, marker)
	require.Len(t, before, 1, "instrument: the saved file actually ran through the real driver")
	allBefore, err := os.ReadFile(marker + ".all")
	require.NoError(t, err, "instrument: count every executable start, including version probes")

	target := filepath.Dir(program)
	_, _, err = workspace.CreateMount(home, ws, "program-host", target)
	require.NoError(t, err)
	require.Contains(t, workspace.AllowedMountRoots(home, ws), target)
	// The retained continuation still checks current grants BEFORE Resume.
	ts.opts.ExternalCLIResume = true
	_, err = runExternalCLISubTurn(ctx, al, ts, "must not execute after writable mount was granted")
	want := fmt.Sprintf("This agent cannot run: the program set for Codex in Settings → Third-party agents does not pass its checks (agent_writable_location at %s). A person can fix it there.", program)
	if assert.Error(t, err, "S2: mount-host programs are refused before another driver/process start") {
		assert.Equal(t, want, err.Error())
	}
	assert.Equal(t, before, t11bNativeCalls(t, marker), "S2: there must be no second actual process invocation")
	allAfter, readErr := os.ReadFile(marker + ".all")
	require.NoError(t, readErr)
	assert.Equal(t, string(allBefore), string(allAfter), "S2: even a --version probe must not execute an agent-writable program")
}
