package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S-65: provoke a returned tool call even though discovery hides it. The real
// execution compositor must deny BEFORE invoking the actual update tool.
func TestT_Tools_S65_DeniedEffortWriteCannotChangeBytes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	scenario := testutil.NewScenario().WithToolCall("update_agent", `{"id":"target","revision":"captured-revision","reasoning_effort":"high"}`).WithText("finished")
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = home
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	cfg.Sandbox.AuditLog = true
	al, _, _ := newContainmentLoop(t, cfg, scenario)
	path := filepath.Join(home, "config.json")
	require.NoError(t, config.SaveConfig(path, cfg))
	entity := filepath.Join(home, "entities", "agents", "target.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(entity), 0700))
	require.NoError(t, os.WriteFile(entity, []byte(`{"id":"target","name":"Target","type":"custom","reasoning_effort":"low"}`), 0600))
	deps := &systools.Deps{Home: home, ConfigPath: path, GetCfg: al.GetConfig, MutateConfig: al.MutateConfig, SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(path, c) }}
	ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	actual := systools.NewAgentUpdateTool(deps)
	ag.Tools.RegisterReplacing(actual)
	registered, ok := ag.Tools.Get("update_agent")
	require.True(t, ok)
	require.Same(t, actual, registered, "positive control: actual production tool is registered")
	policy := &tools.ToolPolicyCfg{}
	if old := ag.LoadToolPolicy(); old != nil {
		*policy = *old
	}
	policy.Policies = map[string]config.ToolPolicy{"update_agent": config.ToolPolicyDeny}
	ag.StoreToolPolicy(policy)
	beforeCfg, err := os.ReadFile(path)
	require.NoError(t, err)
	beforeEntity, err := os.ReadFile(entity)
	require.NoError(t, err)
	_, _, err = al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("try updating the effort"))
	require.NoError(t, err)
	requests := scenario.AllRequests()
	require.Len(t, requests, 2, "returned tool call reached the loop and generated a tool result")
	denial := ""
	for _, msg := range requests[1] {
		if msg.Role == "tool" && msg.ToolCallID == "update_agent-0" {
			denial = msg.Content
		}
	}
	assert.Contains(t, strings.ToLower(denial), "denied by policy", "caller receives the actual policy denial, not an unknown-field/revision error")
	afterCfg, err := os.ReadFile(path)
	require.NoError(t, err)
	afterEntity, err := os.ReadFile(entity)
	require.NoError(t, err)
	assert.Equal(t, beforeCfg, afterCfg)
	assert.Equal(t, beforeEntity, afterEntity)
}

// CHECK-15: both the loop/runtime AND the durable store are replaced. A fresh
// registry that retained the same Runtime was not a gateway restart.
func TestT_CMD_S39_RealLoopAndStoreRestartKeepsConversationEffort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	p := &effortScriptedProvider{content: "MODEL-MUST-NOT-HANDLE-COMMAND", finish: "stop"}
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = home
	al, store, _ := newContainmentLoop(t, cfg, p)
	sid := containmentSessionID
	msg := containmentTurn("/effort default")
	msg.Channel = "telegram"
	reply, _, err := al.processMessageAsPrimaryForTest(context.Background(), msg)
	require.NoError(t, err)
	require.Equal(t, "Effort for scripted-model: Provider default (the provider decides).", reply)
	meta, err := store.GetMeta(sid)
	require.NoError(t, err)
	raw, err := json.Marshal(meta)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.Equal(t, "provider_default", fields["reasoning_effort"], "conversation value was durably written")
	al.Stop()
	al.Close()
	freshStore, err := session.NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	freshLoop := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &effortScriptedProvider{content: "MODEL-MUST-NOT-HANDLE-COMMAND", finish: "stop"})
	freshLoop.sharedSessionStore = freshStore
	t.Cleanup(freshLoop.Close)
	msg.Content = "/effort"
	after, _, err := freshLoop.processMessageAsPrimaryForTest(context.Background(), msg)
	require.NoError(t, err)
	assert.Contains(t, after, "Provider default")
	assert.NotContains(t, after, "MODEL-MUST-NOT-HANDLE-COMMAND")
	restored, err := freshStore.GetMeta(sid)
	require.NoError(t, err)
	bytes, err := json.Marshal(restored)
	require.NoError(t, err)
	assert.Contains(t, string(bytes), `"reasoning_effort":"provider_default"`)
}
