package gateway

// Oracle: r17 FR-150/FR-151 and 16.4. This drives the actual running-service
// setup and config reload before calling the rebuilt agent's production tool
// registry. A catalog definition alone, or the old registry, is not enough.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8Readers_ProductionRegistryStillReadsTaskAfterGatewayReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.EnvHome, home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	path := filepath.Join(home, "config.json")
	seed := config.DefaultConfig()
	seed.Gateway.Host, seed.Gateway.Port = "127.0.0.1", 0
	seed.Agents.Defaults.Home = filepath.Join(home, "workspace")
	// No model turn is needed for a registry read. Limited-mode reload avoids
	// an unrelated provider lookup; the real reload path still rebuilds tools.
	seed.Agents.Defaults.DefaultModel = config.DefaultModel{}
	seed.Sandbox.ToolPolicies["get_task"] = "allow"
	seed.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom, Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"get_task": "allow"}}}}}
	// agents.list is not serialized; boot and reload read the protected store.
	seedAgentEntities(t, home, seed.Agents.List)
	require.NoError(t, config.SaveConfig(path, seed))
	creds := newUnlockedStore(t, home)
	cfg, err := config.LoadConfigWithStore(path, creds)
	require.NoError(t, err)
	require.NoError(t, populateAgentsListFromEntityStoreStrict(cfg, home))
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	rs, err := setupAndStartServices(context.Background(), cfg, credentials.SecretBundle{}, al, msgBus, home, creds, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
	require.NoError(t, err)
	t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })
	rs.loadConfigForSwap = newReloadConfigLoader(path, home, creds, rs)
	tk := &task.Task{Title: "read after actual reload", Prompt: "stored instruction", Action: task.ActionLLM, Status: task.StatusInbox, AgentID: "mia", CreatedBy: "mia", CreatedByAgentID: "mia"}
	require.NoError(t, agent.GetTaskStore(al).Create(tk))
	provider := providers.LLMProvider(&restMockProvider{})
	require.NoError(t, handleConfigReload(context.Background(), al, cfg, &provider, rs, msgBus, true), "instrument: real running gateway reload completed")
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "instrument: resolve the actual post-reload agent, not a cached registry")
	if _, exists := inst.Tools.Get("get_task"); !exists {
		t.Fatal("BLOCKED: get_task not implemented/registered after real gateway config reload — required by r17 16.4")
	}
	ctx := tools.WithAgentID(context.Background(), "mia")
	result := inst.Tools.Execute(ctx, "get_task", map[string]any{"task_id": tk.ID, "fields": []any{"title", "prompt", "status"}})
	require.NotNil(t, result)
	require.False(t, result.IsError, "16.4 the real post-reload reader succeeds: %s", result.ForLLM)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &got))
	assert.Equal(t, map[string]any{"id": tk.ID, "title": "read after actual reload", "prompt": "stored instruction", "status": "inbox"}, got)
}
