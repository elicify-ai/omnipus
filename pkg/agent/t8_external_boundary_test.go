package agent

// r17 AG-04 / 12.3. The runner is the process-edge observation point;
// dispatch, instruction loading, home files, and argument construction up to
// RunOptions remain production code. This does not claim real CLI argv coverage.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8ExternalWorker_SoulNeverReachesRunner(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	al, ts := newExternalTestLoop(t, "codex", "")
	t.Cleanup(al.Close)
	ts.agent.MaxIterations = extTestEffectiveLimit
	for _, dir := range []string{filepath.Join(home, "workspace"), filepath.Join(home, "agents", ts.agent.ID), ts.agent.Home} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("SECRET-PERSONA"), 0o600))
	}
	// Populate the specified config boundary without declaring a parallel Go
	// config field. An implementation that drops the new JSON setting will
	// fail its real dispatch/read tests, not be masked by a test-owned accessor.
	program := filepath.Join(t.TempDir(), "codex")
	require.NoError(t, os.WriteFile(program, []byte("test process edge"), 0o755))
	raw, err := json.Marshal(al.GetConfig())
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	sandbox, ok := cfg["sandbox"].(map[string]any)
	if !ok {
		sandbox = map[string]any{}
		cfg["sandbox"] = sandbox
	}
	sandbox["third_party_agents"] = map[string]any{"clis": map[string]any{"codex": map[string]any{"path": program, "preset_args": ""}}}
	raw, err = json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, al.GetConfig()))
	fr, restore := withFakeDriver(t)
	defer restore()
	require.NoError(t, fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "finished"}}))
	require.NoError(t, fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd}))
	fr.Cancel()
	r, err := runExternalCLISubTurn(context.Background(), al, ts, "ORIGINAL-TASK-TEXT")
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Equal(t, "finished", r.ForLLM)
	opts := fr.RecordedRunOpts()
	require.Len(t, opts, 1, "instrument: real dispatch reached the process boundary")
	assert.Contains(t, opts[0].Input, "ORIGINAL-TASK-TEXT", "an empty/constant handoff cannot certify persona exclusion")
	assert.NotContains(t, opts[0].Input, "SECRET-PERSONA", "12.3 external workers never read any planted SOUL.md")
}
