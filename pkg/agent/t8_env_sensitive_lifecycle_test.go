package agent

// r17 10.7: actual update_agent writer publishes/removes its sensitive-value
// registration; a later real read_file result crosses the native model loop.
// Only model replies are scripted. No scrubber/replacer is supplied by the test.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8EnvSensitiveValues_SetThenRemoveChangesLaterFiltering(t *testing.T) {
	cfg, home := baseLoopDenialTestConfig(t)
	t.Setenv(config.EnvHome, home)
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	cfg.Sandbox.ToolPolicies["update_agent"] = "allow"
	cfg.Sandbox.ToolPolicies["read_file"] = "allow"
	cfg.Tools.FilterSensitiveData = true
	target := config.AgentConfig{ID: "lifecycle-target", Name: "Runtime worker", Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"}}}
	cfg.Agents.List = append(cfg.Agents.List, target)
	store := agentstore.New(home)
	_, err := store.CreateState(target.ID, &target, "")
	require.NoError(t, err)
	state, err := store.ReadState(target.ID)
	require.NoError(t, err)
	const secret = "t8-artificial-sensitive-value-98765"
	fixture := filepath.Join(cfg.Agents.Defaults.Home, "runtime-result.txt")
	require.NoError(t, os.WriteFile(fixture, []byte("ordinary result: "+secret), 0o600))
	args, err := json.Marshal(map[string]any{"id": target.ID, "revision": state.Revision, "env_overrides": map[string]string{"TOKEN": secret}})
	require.NoError(t, err)
	provider := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["update_agent"]}`).WithToolCall("update_agent", string(args)).WithText("set finished")
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	t8WireRealAgentTools(t, al, home)
	caller, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	caller.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"ToolSearch": "allow", "update_agent": "allow", "read_file": "allow"}, Policies: map[string]config.ToolPolicy{"update_agent": "allow", "read_file": "allow"}})
	_, err = al.ProcessDirectAs(context.Background(), "mia", "set worker runtime", "")
	require.NoError(t, err)
	afterSet, err := store.ReadState(target.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"TOKEN": secret}, afterSet.Agent.Subagents.Executor.EnvOverrides, "instrument: the actual writer stored the value before the filter is tested")
	readArgs, err := json.Marshal(map[string]any{"path": fixture})
	require.NoError(t, err)
	before := len(provider.AllRequests())
	provider.WithToolCall("ToolSearch", `{"names":["read_file"]}`).WithToolCall("read_file", string(readArgs)).WithText("read while set")
	_, err = al.ProcessDirectAs(context.Background(), "mia", "read the runtime result", "")
	require.NoError(t, err)
	whileSet := provider.AllRequests()
	require.Greater(t, len(whileSet), before, "instrument: a new model request read the result")
	t8AssertSecretAbsent(t, "later read_file result while env value is set", secret, whileSet[before:])
	removeArgs, err := json.Marshal(map[string]any{"id": target.ID, "revision": afterSet.Revision, "env_overrides_remove": []string{"TOKEN"}})
	require.NoError(t, err)
	provider.WithToolCall("ToolSearch", `{"names":["update_agent"]}`).WithToolCall("update_agent", string(removeArgs)).WithText("remove finished")
	_, err = al.ProcessDirectAs(context.Background(), "mia", "remove worker runtime token", "")
	require.NoError(t, err)
	afterRemove, err := store.ReadState(target.ID)
	require.NoError(t, err)
	require.NotContains(t, afterRemove.Agent.Subagents.Executor.EnvOverrides, "TOKEN", "instrument: the actual writer removed the value")
	before = len(provider.AllRequests())
	provider.WithToolCall("ToolSearch", `{"names":["read_file"]}`).WithToolCall("read_file", string(readArgs)).WithText("read after remove")
	_, err = al.ProcessDirectAs(context.Background(), "mia", "read the same runtime result again", "")
	require.NoError(t, err)
	after := provider.AllRequests()
	require.Greater(t, len(after), before)
	raw, err := json.Marshal(after[before:])
	require.NoError(t, err)
	assert.Contains(t, string(raw), secret, "10.7 removed env value is no longer registered for later result filtering")
}
