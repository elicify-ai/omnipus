//go:build !windows

package agent

// r17 10.6: actual approved writer -> protected state -> actual dispatch ->
// real CLI driver -> a real child. Only model/person and external CLI protocol
// are fixtures. The CLI writes its own TOKEN, never an environment assembled
// by test code. No per-agent CLIPath or test driver factory bypass is used.
import (
	"context"
	"encoding/json"
	"fmt"
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

func TestT8Env_ApprovedWriterReachesRealChildUnmasked(t *testing.T) {
	cfg, home := baseLoopDenialTestConfig(t)
	t.Setenv(config.EnvHome, home)
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	cfg.Sandbox.ToolPolicies["update_agent"] = "ask"
	const secret = "t8-artificial-child-token-24680"
	capture := filepath.Join(t.TempDir(), "actual-child-token.txt")
	program := filepath.Join(t.TempDir(), "claude-runtime-fixture")
	body := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '2.0.0\\n'; exit 0; fi\nprintf '%%s' \"$TOKEN\" > '%s'\nprintf '%%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"child completed\"}'\n", capture)
	require.NoError(t, os.WriteFile(program, []byte(body), 0o700))
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	sandbox, ok := wire["sandbox"].(map[string]any)
	require.True(t, ok)
	sandbox["third_party_agents"] = map[string]any{"clis": map[string]any{"claude-code": map[string]any{"path": program, "preset_args": ""}}}
	raw, err = json.Marshal(wire)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, cfg))
	target := config.AgentConfig{ID: "child-env-target", Name: "Child runtime", Type: config.AgentTypeWorker, Home: filepath.Join(home, "agents", "child-env-target"), Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"}}}
	cfg.Agents.List = append(cfg.Agents.List, target)
	store := agentstore.New(home)
	_, err = store.CreateState(target.ID, &target, "")
	require.NoError(t, err)
	state, err := store.ReadState(target.ID)
	require.NoError(t, err)
	args, err := json.Marshal(map[string]any{"id": target.ID, "revision": state.Revision, "env_overrides": map[string]string{"TOKEN": secret}})
	require.NoError(t, err)
	provider := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["update_agent"]}`).WithToolCall("update_agent", string(args)).WithText("runtime approved")
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	t8WireRealAgentTools(t, al, home)
	mia, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"ToolSearch": "allow", "update_agent": "ask"}, Policies: map[string]config.ToolPolicy{"update_agent": "ask"}})
	decision := &t8DecisionRecorder{allow: true}
	al.SetToolApprover(decision)
	_, err = al.ProcessDirectAs(context.Background(), "mia", "set the worker token", "")
	require.NoError(t, err)
	require.Len(t, decision.snapshot(), 1, "instrument: a real ask was approved")
	after, err := store.ReadState(target.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"TOKEN": secret}, after.Agent.Subagents.Executor.EnvOverrides, "instrument: the approved production writer stored the original value")
	worker, ok := al.GetRegistry().GetAgent(target.ID)
	require.True(t, ok, "production activation publishes the changed worker")
	worker.MaxIterations = extTestEffectiveLimit
	_, err = runExternalCLISubTurn(context.Background(), al, &turnState{agent: worker, agentID: worker.ID, turnID: "t8-child-token-turn", transcriptSessionID: "t8-child-token-session"}, "Return child completed")
	require.NoError(t, err)
	got, err := os.ReadFile(capture)
	require.NoError(t, err, "instrument: the actual driver really spawned the Settings program")
	assert.Equal(t, secret, string(got), "10.6 display masking never replaces the execution value")
}
