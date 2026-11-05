package agent

// r17 19.7, 19.10 and 19.11: real loop policy/approval plus protected write.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8Permissions_RealLoopDefaultHolderGrantedJimAndDeniedMia(t *testing.T) {
	for _, tc := range []struct {
		actor         string
		granted, save bool
		approvals     int
	}{{"omnipus", false, true, 1}, {"jim", true, true, 1}, {"mia", false, false, 0}} {
		t.Run(tc.actor, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(config.EnvHome, home)
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
			cfg.Agents.Defaults.MaxTokens = 4096
			cfg.Agents.Defaults.MaxToolIterations = 10
			cfg.Agents.List = nil
			require.True(t, coreagent.SeedConfig(cfg))
			for i := range cfg.Agents.List {
				cfg.Agents.List[i].Home = filepath.Join(home, "agents", cfg.Agents.List[i].ID)
				if tc.granted && cfg.Agents.List[i].ID == tc.actor {
					cfg.Agents.List[i].Tools.Builtin.Policies["set_agent_permissions"] = config.ToolPolicyAsk
				}
			}
			target := config.AgentConfig{ID: "permission-worker", Name: "Permission worker", Type: config.AgentTypeWorker, Tools: coreagent.NewCustomAgentToolsCfg(), Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative}}}
			coreagent.RestrictForWorkerTier(target.Tools)
			target.Tools.Builtin.Policies["bash"] = config.ToolPolicyAsk
			cfg.Agents.List = append(cfg.Agents.List, target)
			store := agentstore.New(home)
			_, err := store.CreateState(target.ID, &target, "worker soul")
			require.NoError(t, err)
			before, err := store.ReadState(target.ID)
			require.NoError(t, err)
			args := map[string]any{"id": target.ID, "revision": before.Revision, "tool_policy_changes": map[string]any{"set": map[string]any{"bash": "deny"}}}
			raw, err := json.Marshal(args)
			require.NoError(t, err)
			prov := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["set_agent_permissions"]}`).WithToolCall("set_agent_permissions", string(raw)).WithText("permission change checked")
			al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), prov)
			t.Cleanup(al.Close)
			t8WireRealAgentTools(t, al, home)
			decider := &t8DecisionRecorder{allow: true}
			al.SetToolApprover(decider)
			reply, err := al.ProcessDirectAs(context.Background(), tc.actor, "set the worker shell policy to deny", "t8-permission-"+tc.actor)
			require.NoError(t, err)
			assert.Equal(t, "permission change checked", reply)
			requests := decider.snapshot()
			assert.Len(t, requests, tc.approvals)
			for _, request := range requests {
				assert.Equal(t, "set_agent_permissions", request.ToolName)
				assert.Equal(t, tc.actor, request.AgentID)
			}
			after, err := store.ReadState(target.ID)
			require.NoError(t, err)
			if tc.save {
				require.NotNil(t, after.Agent.Tools)
				assert.Equal(t, config.ToolPolicyDeny, after.Agent.Tools.Builtin.Policies["bash"])
				assert.NotEqual(t, before.Revision, after.Revision)
			} else {
				assert.Equal(t, before, after, "Mia's default policy rejects the write before approval")
				history, err := json.Marshal(prov.AllRequests())
				require.NoError(t, err)
				assert.Contains(t, string(history), "policy_denied", "19.11 the refusal actually returned to the model is the policy denial")
				assert.Contains(t, string(history), "set_agent_permissions")
			}
		})
	}
}
