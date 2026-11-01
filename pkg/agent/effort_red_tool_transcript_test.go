package agent

// CHECK-11 / S-63: real loop dispatch -> production registry -> durable entity
// -> real tool-result transcript. Registry-only positives do not prove this.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT_Tools_S63_ActualDispatchStoresEffortCallAndResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	installConversationCatalog(t, []string{"none", "low", "high", "max"}, true, true)
	target := agentstore.New(home)
	_, err := target.CreateState("effort-target", &config.AgentConfig{ID: "effort-target", Name: "Effort target", Type: "Main", Model: &config.AgentModelConfig{Provider: "openrouter", Primary: "primary-m"}}, "test target")
	require.NoError(t, err)
	revision, err := target.PeekRevision("effort-target")
	require.NoError(t, err)
	args := map[string]any{"id": "effort-target", "revision": revision, "reasoning_effort": "high", "fallback_models": []any{map[string]any{"provider": "openrouter", "model": "primary-m", "reasoning_effort": "on"}}}
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	scenario := testutil.NewScenario().WithToolCall("update_agent", string(raw)).WithToolCall("get_agent", `{"id":"effort-target","fields":["reasoning_effort","fallback_models"]}`).WithText("tool transaction complete")
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	cfg.Sandbox.ToolPolicies["update_agent"] = string(config.ToolPolicyAllow)
	cfg.Sandbox.ToolPolicies["get_agent"] = string(config.ToolPolicyAllow)
	al, store, _ := newContainmentLoop(t, cfg, scenario)
	configPath := filepath.Join(home, "config.json")
	require.NoError(t, config.SaveConfig(configPath, cfg))
	deps := &systools.Deps{Home: home, ConfigPath: configPath, GetCfg: al.GetConfig, MutateConfig: al.MutateConfig, SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(configPath, c) }}
	reg := systools.BuildRegistry(deps)
	ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	for _, name := range []string{"update_agent", "get_agent"} {
		tool, ok := reg.Get(name)
		require.True(t, ok)
		ag.Tools.RegisterReplacing(tool)
	}
	ag.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"update_agent": config.ToolPolicyAllow, "get_agent": config.ToolPolicyAllow}})
	response, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("use the production tools to set and read effort"))
	require.NoError(t, err)
	require.Equal(t, "tool transaction complete", response)
	requests := scenario.AllRequests()
	require.Len(t, requests, 3)
	results := map[string]string{}
	for _, message := range requests[2] {
		if message.Role == "tool" {
			results[message.ToolCallID] = message.Content
		}
	}
	require.Len(t, results, 2, "actual update and read both reached the runtime and returned")
	for _, id := range []string{"update_agent-0", "get_agent-0"} {
		require.Contains(t, results, id)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(results[id]), &payload), "the production tool result is JSON")
		canonical, encodeErr := json.Marshal(payload)
		require.NoError(t, encodeErr)
		require.Contains(t, string(canonical), `"reasoning_effort":"high"`, "S-63 successful tool results must show the stored primary effort; an unknown-field refusal is missing implementation")
	}
	bytes, err := os.ReadFile(filepath.Join(home, "entities", "agents", "effort-target.json"))
	require.NoError(t, err)
	var saved map[string]any
	require.NoError(t, json.Unmarshal(bytes, &saved))
	assert.Equal(t, "high", saved["reasoning_effort"])
	entries, err := store.ReadTranscript(containmentSessionID)
	require.NoError(t, err)
	calls := 0
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.Tool != "update_agent" {
				continue
			}
			calls++
			assert.Equal(t, args, call.Parameters)
			assert.Equal(t, "success", call.Status)
			result, err := json.Marshal(call.Result)
			require.NoError(t, err)
			require.Contains(t, string(result), `"reasoning_effort":"high"`, "durable tool result includes the stored effort")
		}
	}
	require.Equal(t, 1, calls, "caller transcript records the actual parameters and successful result exactly once")
}
