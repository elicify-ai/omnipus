package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// Q4 removes only outbound from the Omnipus-only management-grant fence.
// These positive controls replace its old negative rows without hiding a still
// active fence; the other management tools keep all their denial assertions.
func TestQ7FollowupQ4_CreateToolAllowsChatCapableOutboundAskDenyAndInheritance(t *testing.T) {
	for _, policy := range []string{"ask", "deny", "inherit", "allow"} {
		t.Run(policy, func(t *testing.T) {
			deps, _ := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)
			args := createNativeArgs("Q4 chat colleague")
			if policy == "inherit" {
				args["tool_policy_changes"] = map[string]any{"remove": []any{"set_channel_outbound"}}
			} else {
				args["tool_policy_changes"] = map[string]any{"set": map[string]any{"set_channel_outbound": policy}}
			}
			result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
			require.NotNil(t, result)
			if policy == "allow" {
				require.True(t, result.IsError)
				_, message := toolErrorCode(t, result.ForLLM)
				require.Equal(t, "set_channel_outbound always asks for approval; it can be set to ask or deny, not allow", message, "FR-037: the always-ask refusal replaces the obsolete management-only refusal")
				stored, skipped, err := agentstore.New(deps.Home).List()
				require.NoError(t, err)
				require.Empty(t, skipped)
				require.Empty(t, stored)
				return
			}
			require.False(t, result.IsError, "Q4: chat-capable outbound %s is legal on create: %s", policy, result.ForLLM)
			id, ok := parseSuccess(t, result.ForLLM)["id"].(string)
			require.True(t, ok)
			ac, err := agentstore.New(deps.Home).Get(id)
			require.NoError(t, err)
			want := "ask"
			if policy == "inherit" {
				require.NotContains(t, ac.Tools.Builtin.Policies, "set_channel_outbound")
			} else {
				require.Equal(t, config.ToolPolicy(policy), ac.Tools.Builtin.Policies["set_channel_outbound"])
				want = policy
			}
			cfg := config.DefaultConfig()
			cfg.Agents.List = []config.AgentConfig{*ac}
			resolved, _ := tools.BuildFallbackPolicyCfg(cfg, id)
			require.Equal(t, want, tools.ResolveEffectivePolicy(resolved, "set_channel_outbound"))
			require.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies["create_agent"], "Q4 does not open the unrelated management family")
		})
	}
}
