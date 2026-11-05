// Skills r15 §10 supersedes the historical create_skill approval oracle.
// The real edit_skill gate must ask across agent types. A real seeded Omnipus
// rides fresh Ask or an explicit operator Allow, never a test-only tool stub.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func TestRunTurn_EditSkillAskPolicy_ApproverConsulted(t *testing.T) {
	for _, agentType := range []config.AgentType{"", config.AgentTypeCore, config.AgentTypeCustom} {
		t.Run(string(agentType)+"_agentType", func(t *testing.T) {
			cfg, _ := baseLoopDenialTestConfig(t)
			cfg.Agents.List[0].Type = agentType
			if agentType == config.AgentTypeCore {
				cfg.Agents.List[0].Locked = true
			}

			provider := testutil.NewScenario().
				WithToolCall("edit_skill", `{"action":"delete","skill":"uat-s67"}`).
				WithText("done")

			msgBus := bus.NewMessageBus()
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			defer al.Close()

			// Register the real supported action tool, not a synthetic stub.
			al.RegisterTool(systools.NewSkillEditTool(&systools.Deps{}))
			setAskPolicyForAllAgents(t, al, "edit_skill", config.ToolPolicyAsk)

			approver := &countingDenyApprover{reason: "user"}
			al.SetToolApprover(approver)

			_, err := al.processDirectPrimaryForTest(context.Background(), "please create the uat-s67 skill", "test-session-uat-s67")
			require.NoError(t, err)

			assert.Equal(t, 1, approver.callCount(),
				"edit_skill is policied ask — the approver MUST be consulted before it executes")
		})
	}
}

// §10: fresh Ask is normative; Reconcile must retain an explicit operator Allow.
// Both cases use the actual seed, ToolSearch and wired system tool registry.
func TestRunTurn_OmnipusEditSkillCeiling_RealSeed(t *testing.T) {
	for _, tc := range []struct {
		name, ceiling string
		asks          int
	}{
		{"fresh-ask", "ask", 1}, {"operator-allow", "allow", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			tmpHome := t.TempDir()
			t.Setenv(config.EnvHome, tmpHome)
			t.Setenv(config.EnvBuiltinSkills, t.TempDir())
			workspaceDir := filepath.Join(tmpHome, "workspace")
			require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
			cfg.Agents.Defaults.Home = workspaceDir
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
			cfg.Agents.Defaults.MaxTokens = 4096
			cfg.Agents.Defaults.MaxToolIterations = 10
			cfg.Sandbox.AutoApprove = false
			cfg.Sandbox.AuditLog = true
			require.Equal(t, "ask", cfg.Sandbox.ToolPolicies["edit_skill"], "§10 fresh ceiling")
			cfg.Sandbox.ToolPolicies["edit_skill"] = tc.ceiling
			coreagent.SeedConfig(cfg)
			provider := testutil.NewScenario().
				WithToolCall("ToolSearch", `{"names":["edit_skill"]}`).
				WithToolCall("edit_skill", `{"action":"delete","skill":"uat-s67"}`).
				WithText("done")
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			t.Cleanup(al.Close)
			al.WireSysagentDeps(&systools.Deps{})
			approver := &countingDenyApprover{reason: "user"}
			al.SetToolApprover(approver)
			_, err := al.ProcessDirectAs(context.Background(), string(coreagent.IDOmnipus), "delete the uat-s67 skill", "test-session-r15-"+tc.name)
			require.NoError(t, err)
			assert.Equal(t, tc.asks, approver.callCount(), "real seeded Omnipus rides the chosen global ceiling")
		})
	}
}
