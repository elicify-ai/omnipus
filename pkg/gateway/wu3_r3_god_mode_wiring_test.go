//go:build goolm && stdjson

package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pr-test-analyzer R2-03: the production wiring (wireSystemTools) hands the
// live God Mode state to the system tools (Deps.GodModeActive). Booted with
// God Mode available and switched on, get_agent tool_permissions reports what runs: a
// global ask does not clamp an agent's allow. Switched off, the same stored
// values are clamped (control). Without the wiring the God Mode case would
// report the clamp.
func TestWU3ProductionWiringPassesGodModeToSystemTools(t *testing.T) {
	agent.SetGodModeAvailable(true)
	t.Cleanup(func() { agent.SetGodModeAvailable(false) })
	for _, tc := range []struct {
		name      string
		godMode   bool
		effective string
	}{
		{"god mode on", true, "allow"},
		{"god mode off", false, "ask"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := wu3BootRunContext(t, func(cfg *config.Config) {
				cfg.Sandbox.GodMode = tc.godMode
				cfg.Sandbox.ToolPolicies["read_file"] = "ask"
				cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
					ID: "god-mode-subject", Name: "Subject", Type: config.AgentTypeCustom,
					Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
						Policies: map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow},
					}},
				})
			})
			require.Equal(t, tc.godMode, agent.GodModeActive(rc.agentLoop.GetConfig()), "control: God Mode state of this boot")
			inst, ok := rc.agentLoop.GetRegistry().GetAgent("omnipus")
			require.True(t, ok)
			tool, ok := inst.Tools.Get("get_agent")
			require.True(t, ok)
			res := tool.Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{"id": "god-mode-subject", "fields": []any{"tool_permissions"}})
			require.False(t, res.IsError, res.ForLLM)
			// r17 7.12.1: production-wired read-back of the effective policy per tool.
			state, err := agentstore.New(rc.homePath).ReadState("god-mode-subject")
			require.NoError(t, err)
			identity := map[string]any{"id": "god-mode-subject", "revision": state.Revision}
			body := defaultToolsParseInventory(t, res.ForLLM, identity, rc.centralBuiltinReg.All())
			assert.Equal(t, tc.effective, body["read_file"], "production-wired God Mode must govern the compact effective policy")
		})
	}
}
