package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-024 / BDD-14.2: the former inputs must be refused, not ignored,
// including clears and an ordinary edit bundled with the forbidden change.
func TestL1WU2UpdateAgent_RefusesPermissionAndConnectorFieldsWithoutWrites(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value any
	}{
		{"policy_set", "tool_policy_changes", map[string]any{"set": map[string]any{"bash": "allow"}}},
		{"policy_remove", "tool_policy_changes", map[string]any{"remove": []any{"bash"}}},
		{"connector_assign", "mcp_servers", []any{map[string]any{"id": "docs", "tools": []any{}}}},
		{"connector_clear", "mcp_servers", []any{}},
		{"connector_null", "mcp_servers", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfgPath := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)
			deps.GetCfg().Tools.MCP.Servers["docs"] = config.MCPServerConfig{Enabled: false, Command: "not-started-in-this-test"}
			store := agentstore.New(deps.Home)
			ac := config.AgentConfig{ID: "permission-subject", Name: "Subject", Type: config.AgentTypeCustom}
			require.NoError(t, store.Create(ac.ID, &ac))
			before, err := store.ReadState(ac.ID)
			require.NoError(t, err)
			beforeConfig, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			args := map[string]any{"id": ac.ID, "revision": before.Revision, "description": "must-not-be-saved", tc.field: tc.value}
			result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), args)
			require.NotNil(t, result)
			assert.True(t, result.IsError, "FR-024: update_agent must refuse %s; got %s", tc.field, result.ForLLM)
			assert.Contains(t, result.ForLLM, "set_agent_permissions", "BDD-14.2: refusal must point to the permission tool")
			after, err := store.ReadState(ac.ID)
			require.NoError(t, err)
			assert.Equal(t, before, after, "permission rejection must precede every durable edit")
			afterConfig, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			assert.Equal(t, beforeConfig, afterConfig)
		})
	}
	t.Run("ordinary_edit_positive_control", func(t *testing.T) {
		deps, _ := newTestDepsWithRealSave(t)
		t.Setenv("OMNIPUS_HOME", deps.Home)
		store := agentstore.New(deps.Home)
		ac := config.AgentConfig{ID: "edit-control", Name: "Control", Type: config.AgentTypeCustom}
		require.NoError(t, store.Create(ac.ID, &ac))
		before, err := store.ReadState(ac.ID)
		require.NoError(t, err)
		result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
			"id": ac.ID, "revision": before.Revision, "description": "ordinary change",
		})
		require.False(t, result.IsError, "control must really reach the update writer: %s", result.ForLLM)
		after, err := store.ReadState(ac.ID)
		require.NoError(t, err)
		assert.Equal(t, "ordinary change", after.Agent.Description)
		assert.NotEqual(t, before.Revision, after.Revision)
	})
}

// FR-025 / BDD-14.3: BOTH allow and ask are non-deny management grants.
func TestL1WU2CreateAgent_RefusesEveryNonDenyManagementGrantBeforePersistence(t *testing.T) {
	management := []string{"create_agent", "update_agent", "delete_agent", "create_workspace", "update_workspace",
		"delete_workspace", "create_skill", "edit_skill", "remove_skill", "install_skill", "add_mcp_server",
		"remove_mcp_server", "configure_provider", "enable_channel", "disable_channel", "configure_channel",
		"connect_channel", "connect_mailbox", "set_agent_permissions", "remove_secret", "select_secret_version", "set_config"}
	for _, name := range management {
		for _, policy := range []string{"allow", "ask"} {
			t.Run(name+"_"+policy, func(t *testing.T) {
				deps, cfgPath := newTestDepsWithRealSave(t)
				t.Setenv("OMNIPUS_HOME", deps.Home)
				beforeConfig, err := os.ReadFile(cfgPath)
				require.NoError(t, err)
				args := createNativeArgs("Grant Subject")
				args["tool_policy_changes"] = map[string]any{"set": map[string]any{name: policy}}
				result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
				require.NotNil(t, result)
				assert.True(t, result.IsError, "FR-025: %s:%s must be refused, got %s", name, policy, result.ForLLM)
				assert.Contains(t, result.ForLLM, name, "refusal identifies the attempted management grant")
				if result.IsError {
					assert.Contains(t, strings.ToLower(result.ForLLM), "management", "not an unrelated unknown-name/type error")
				}
				stored, skipped, err := agentstore.New(deps.Home).List()
				require.NoError(t, err)
				require.Empty(t, skipped, "unreadable entities must not hide a write")
				assert.Empty(t, stored, "FR-025: rejected create must not publish any agent")
				afterConfig, err := os.ReadFile(cfgPath)
				require.NoError(t, err)
				assert.Equal(t, beforeConfig, afterConfig)
			})
		}
	}
	t.Run("deny_management_and_allow_read_positive_control", func(t *testing.T) {
		deps, _ := newTestDepsWithRealSave(t)
		t.Setenv("OMNIPUS_HOME", deps.Home)
		args := createNativeArgs("Grant Control")
		args["tool_policy_changes"] = map[string]any{"set": map[string]any{"add_mcp_server": "deny", "read_file": "allow"}}
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
		require.False(t, result.IsError, "explicit Deny is not a management grant: %s", result.ForLLM)
		body := parseSuccess(t, result.ForLLM)
		id, ok := body["id"].(string)
		require.True(t, ok)
		ac, err := agentstore.New(deps.Home).Get(id)
		require.NoError(t, err)
		assert.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies["add_mcp_server"])
		assert.Equal(t, config.ToolPolicyAllow, ac.Tools.Builtin.Policies["read_file"])
	})
}

func TestL1WU2AgentToolNames_TrimmedCaseInsensitiveOmnipusIsReserved(t *testing.T) {
	// FR-002 / BDD-1.2: similar names are legal, exact reserved name is not.
	for _, name := range []string{"Omnipus", "omnipus", " OMNIPUS "} {
		t.Run("create_"+name, func(t *testing.T) {
			deps, _ := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)
			result := systools.NewAgentCreateTool(deps).Execute(context.Background(), createNativeArgs(name))
			assert.True(t, result.IsError, "FR-002: reserved name must not be accepted: %s", result.ForLLM)
			assert.Contains(t, result.ForLLM, "the name Omnipus is reserved")
			stored, skipped, err := agentstore.New(deps.Home).List()
			require.NoError(t, err)
			require.Empty(t, skipped, "unreadable entities must not hide a write")
			assert.Empty(t, stored)
		})
		t.Run("rename_"+name, func(t *testing.T) {
			deps, _ := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)
			store := agentstore.New(deps.Home)
			ac := config.AgentConfig{ID: "rename-subject", Name: "Researcher", Type: config.AgentTypeCustom}
			require.NoError(t, store.Create(ac.ID, &ac))
			before, err := store.ReadState(ac.ID)
			require.NoError(t, err)
			result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
				"id": ac.ID, "revision": before.Revision, "name": name,
			})
			assert.True(t, result.IsError, "FR-002: reserved rename must not save: %s", result.ForLLM)
			assert.Contains(t, result.ForLLM, "the name Omnipus is reserved")
			after, err := store.ReadState(ac.ID)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
	t.Run("similar_name_positive_control", func(t *testing.T) {
		deps, _ := newTestDepsWithRealSave(t)
		t.Setenv("OMNIPUS_HOME", deps.Home)
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), createNativeArgs("Omnipus Helper"))
		require.False(t, result.IsError, "BDD-1.2 similar-name control: %s", result.ForLLM)
		id, ok := parseSuccess(t, result.ForLLM)["id"].(string)
		if !ok {
			t.Fatal("similar-name positive control must return a string agent id")
		}
		ac, err := agentstore.New(deps.Home).Get(id)
		require.NoError(t, err)
		assert.Equal(t, "Omnipus Helper", ac.Name)
	})
}

func TestL1WU2WorkspaceTool_RefusesOmnipusMembershipOutsideDefault(t *testing.T) {
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	cfg := deps.GetCfg()
	cfg.Agents.List = []config.AgentConfig{{ID: "omnipus", Type: config.AgentTypeCore}, {ID: "mia", Type: config.AgentTypeCore}}
	path := filepath.Join(deps.Home, "workspaces", "sales.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	body := []byte(`{"id":"sales","name":"Sales","status":"active","core_team":["mia"]}`)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	state, err := workspacepkg.ReadStateLocked(deps.Home, "sales")
	require.NoError(t, err)
	result := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": "sales", "revision": state.Revision, "core_team": []any{"mia", "omnipus"},
	})
	assert.True(t, result.IsError, "FR-003 / BDD-1.3: Omnipus may belong only to default: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "default workspace", "membership rejection must explain the boundary")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, after, "membership refusal must be zero-write")
}

func TestL1WU2PermissionTool_IsExecutableAndAskGatedNotOnlyMetadata(t *testing.T) {
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	registry := systools.BuildRegistry(deps)
	_, control := registry.Get("get_agent")
	require.True(t, control, "known executable-tool registration positive control")
	permission, present := registry.Get("set_agent_permissions")
	if !present {
		t.Fatal("BLOCKED: executable set_agent_permissions not implemented — required by FR-024 / BDD-14.1/14.4")
	}
	assert.Equal(t, "set_agent_permissions", permission.Name())
	assert.Equal(t, "ask", deps.GetCfg().Sandbox.ToolPolicies[permission.Name()], "FR-021: permission changes must be approval-gated")
	class := tools.AutoApproveClassTable()
	value, explicit := class[permission.Name()]
	assert.True(t, explicit, "FR-021: cannot rely on unknown-name default Ask")
	assert.Equal(t, tools.AutoAsks, value)
}
