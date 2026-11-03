package systools_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-025 / BDD-14.3 (founder-approved security fix, 2026-10-14): removing a
// new agent's management-tool entry is a grant too — the constructor's
// explicit Deny disappears and the agent rides the ceiling (allow or ask).
// create_agent must refuse a `remove` of any management tool before
// persistence, exactly like a non-deny `set`.
func TestCreateAgent_RefusesRemovalOfAnyManagementDenyBeforePersistence(t *testing.T) {
	management := []string{"create_agent", "update_agent", "delete_agent", "create_workspace", "update_workspace",
		"delete_workspace", "install_skill", // r15 §10: editing leaves management; retired verbs are absent.
		"add_mcp_server",
		"remove_mcp_server", "configure_provider", "enable_channel", "disable_channel", "configure_channel",
		"connect_channel", "set_channel_outbound", "connect_mailbox", "set_agent_permissions", "remove_secret", "select_secret_version", "set_config"}
	for _, name := range management {
		t.Run(name, func(t *testing.T) {
			deps, cfgPath := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)
			beforeConfig, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			args := createNativeArgs("Remove Subject")
			args["tool_policy_changes"] = map[string]any{"remove": []any{name}}
			result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
			require.NotNil(t, result)
			assert.True(t, result.IsError, "FR-025: removing %s's deny must be refused, got %s", name, result.ForLLM)
			assert.Contains(t, result.ForLLM, name, "refusal names the management tool")
			assert.Contains(t, strings.ToLower(result.ForLLM), "management")
			stored, skipped, err := agentstore.New(deps.Home).List()
			require.NoError(t, err)
			require.Empty(t, skipped)
			assert.Empty(t, stored, "a refused create publishes no agent")
			afterConfig, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			assert.Equal(t, beforeConfig, afterConfig)
		})
	}
	t.Run("removing_a_non_management_deny_positive_control", func(t *testing.T) {
		deps, _ := newTestDepsWithRealSave(t)
		t.Setenv("OMNIPUS_HOME", deps.Home)
		args := createNativeArgs("Remove Control")
		args["tool_policy_changes"] = map[string]any{"remove": []any{"bash"}}
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
		require.False(t, result.IsError, "removing a non-management deny stays allowed: %s", result.ForLLM)
		id, ok := parseSuccess(t, result.ForLLM)["id"].(string)
		require.True(t, ok)
		ac, err := agentstore.New(deps.Home).Get(id)
		require.NoError(t, err)
		_, has := ac.Tools.Builtin.Policies["bash"]
		assert.False(t, has, "the removed non-management entry rides the ceiling")
		assert.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies["create_agent"],
			"management tools keep the constructor's explicit deny")
	})
}

// Skills r15 §10 explicitly removes edit_skill from the management refusal.
// Positive persistence controls prevent merely deleting it from the negative
// table from hiding a still-active management fence. Other denials remain.
func TestCreateAgent_R15EditSkillIsNotAManagementGrant(t *testing.T) {
	for _, policy := range []string{"ask", "allow"} {
		t.Run("set_"+policy, func(t *testing.T) {
			deps, _ := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)
			args := createNativeArgs("Skill Editor")
			args["tool_policy_changes"] = map[string]any{"set": map[string]any{"edit_skill": policy}}
			result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
			require.False(t, result.IsError, "r15 §10 allows explicit edit_skill grants: %s", result.ForLLM)
			id, ok := parseSuccess(t, result.ForLLM)["id"].(string)
			require.True(t, ok)
			ac, err := agentstore.New(deps.Home).Get(id)
			require.NoError(t, err)
			assert.Equal(t, config.ToolPolicy(policy), ac.Tools.Builtin.Policies["edit_skill"])
			assert.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies["create_agent"], "management fence remains")
		})
	}
	t.Run("remove", func(t *testing.T) {
		deps, _ := newTestDepsWithRealSave(t)
		t.Setenv("OMNIPUS_HOME", deps.Home)
		args := createNativeArgs("Skill Ceiling")
		args["tool_policy_changes"] = map[string]any{"remove": []any{"edit_skill"}}
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
		require.False(t, result.IsError, "r15 §10 removes the management-entry-removal fence: %s", result.ForLLM)
		id, ok := parseSuccess(t, result.ForLLM)["id"].(string)
		require.True(t, ok)
		ac, err := agentstore.New(deps.Home).Get(id)
		require.NoError(t, err)
		_, has := ac.Tools.Builtin.Policies["edit_skill"]
		assert.False(t, has, "explicit removal rides the ceiling")
		assert.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies["create_agent"])
	})
}
