package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec §6.5 / FR-007: the constructor used by real create_agent must grant
// private-secret tools to a custom main, explicitly deny them to a worker,
// and deny management tools to both. This is not a policy-map-only fixture.
func TestL1WU2CustomNativeCreation_SecretGrantsFollowMainVersusWorkerTier(t *testing.T) {
	for _, kind := range []string{"Main", "Subagent"} {
		t.Run(kind, func(t *testing.T) {
			deps, _ := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)
			args := createNativeArgs("Role Subject")
			args["agent_type"] = kind
			result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
			require.False(t, result.IsError, "real creation positive control: %s", result.ForLLM)
			id, ok := parseSuccess(t, result.ForLLM)["id"].(string)
			require.True(t, ok)
			ac, err := agentstore.New(deps.Home).Get(id)
			require.NoError(t, err)
			require.NotNil(t, ac.Tools)
			assert.Equal(t, "Role Subject", ac.Name)
			for _, name := range []string{"request_secret", "list_secrets", "read_conversation"} {
				want := config.ToolPolicyAllow
				// SPEC.md Table (2026-10-22): Worker list_secrets is Allow; request_secret/read_conversation stay Off.
				if kind == "Subagent" && name != "list_secrets" {
					want = config.ToolPolicyDeny
				}
				got, present := ac.Tools.Builtin.Policies[name]
				assert.True(t, present, "§6.5 custom %s must explicitly classify %s", kind, name)
				assert.Equal(t, want, got, "§6.5 custom %s/%s", kind, name)
			}
			for _, name := range []string{"create_agent", "add_mcp_server", "set_agent_permissions", "remove_secret", "select_secret_version", "connect_channel", "connect_mailbox", "list_mailboxes"} {
				got, present := ac.Tools.Builtin.Policies[name]
				assert.True(t, present, "management/Omnipus-only surface must not be silently inherited")
				assert.Equal(t, config.ToolPolicyDeny, got, "%s/%s", kind, name)
			}
		})
	}
}
