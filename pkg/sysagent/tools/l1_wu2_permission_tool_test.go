package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Input authority: chief ruling 2026-10-13, recorded in HANDOFFS.md:
// {id, revision, tool_policy_changes, mcp_servers}; one capability field is
// required; existing value shapes and validation are retained. FR-024.
func l1WU2PermissionFixture(t *testing.T, id string) (*systools.Deps, *agentstore.Store, *agentstore.State, tools.Tool) {
	t.Helper()
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	deps.GetCfg().Tools.MCP.Servers["docs"] = config.MCPServerConfig{Enabled: false, Command: "not-executed"}
	ac := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
	if id == "omnipus" {
		ac.Name, ac.Type, ac.Locked = "Omnipus", config.AgentTypeCore, true
		// Valid fixture pre-state: no old custom-role Ask may interfere
		// with the single new bash policy being tested below.
		for name, value := range ac.Tools.Builtin.Policies {
			if value == config.ToolPolicyAsk {
				ac.Tools.Builtin.Policies[name] = config.ToolPolicyDeny
			}
		}
	}
	store := agentstore.New(deps.Home)
	require.NoError(t, store.Create(id, &ac))
	before, err := store.ReadState(id)
	require.NoError(t, err)
	t.Run("get_agent_positive_control", func(t *testing.T) {
		lookup := systools.NewAgentGetTool(deps).Execute(context.Background(), map[string]any{"id": id})
		require.False(t, lookup.IsError, "positive control: real get_agent sees the same durable entity: %s", lookup.ForLLM)
		readback := parseSuccess(t, lookup.ForLLM)
		assert.Equal(t, id, readback["id"])
		assert.Equal(t, before.Revision, readback["revision"])
	})
	permission, ok := systools.BuildRegistry(deps).Get("set_agent_permissions")
	if !ok {
		t.Fatal("BLOCKED: set_agent_permissions not implemented — required by FR-024 / BDD-14.1/14.4; chief input ruling 2026-10-13")
	}
	return deps, store, before, permission
}

func TestL1WU2PermissionTool_OnlyCapabilityFieldsAndRequiredRevision(t *testing.T) {
	for _, row := range []struct {
		name, message string
		args          map[string]any
	}{
		{"missing_revision", "revision is required", map[string]any{"id": "subject", "tool_policy_changes": map[string]any{"set": map[string]any{"bash": "deny"}}}},
		{"no_capability_change", "at least one", map[string]any{"id": "subject"}},
		{"ordinary_edit_not_allowed", "unknown field", map[string]any{"id": "subject", "description": "not a permission", "tool_policy_changes": map[string]any{"set": map[string]any{"bash": "deny"}}}},
		{"null_connector_list", "non-null array", map[string]any{"id": "subject", "mcp_servers": nil}},
		{"stale_revision", "CONFLICT", map[string]any{"id": "subject", "revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "tool_policy_changes": map[string]any{"set": map[string]any{"bash": "deny"}}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, store, before, permission := l1WU2PermissionFixture(t, "subject")
			if row.name != "missing_revision" && row.name != "stale_revision" {
				row.args["revision"] = before.Revision
			}
			result := permission.Execute(context.Background(), row.args)
			assert.True(t, result.IsError, "FR-024: malformed capability write must refuse: %s", result.ForLLM)
			assert.Contains(t, result.ForLLM, row.message)
			after, err := store.ReadState("subject")
			require.NoError(t, err)
			assert.Equal(t, before, after, "invalid permission input is zero-write")
		})
	}
}

func TestL1WU2PermissionTool_PolicyAndConnectorWritesReadBackOmnipusAskRefused(t *testing.T) {
	for _, row := range []struct {
		id, policy string
		refuse     bool
	}{
		{"subject", "ask", false}, // another agent may have per-agent Ask
		{"omnipus", "deny", false},
		{"omnipus", "ask", true},
	} {
		t.Run(row.id+"_"+row.policy, func(t *testing.T) {
			_, store, before, permission := l1WU2PermissionFixture(t, row.id)
			result := permission.Execute(context.Background(), map[string]any{
				"id": row.id, "revision": before.Revision,
				"tool_policy_changes": map[string]any{"set": map[string]any{"bash": row.policy}},
				"mcp_servers":         []any{map[string]any{"id": "docs", "tools": []any{}}},
			})
			after, err := store.ReadState(row.id)
			require.NoError(t, err)
			if row.refuse {
				assert.True(t, result.IsError, "FR-022: Omnipus takes no per-agent Ask: %s", result.ForLLM)
				assert.Contains(t, result.ForLLM, "Omnipus takes no per-agent Ask; set Ask globally or use Deny")
				assert.Equal(t, before, after, "one forbidden member must reject the entire permission change")
			} else {
				require.False(t, result.IsError, "valid capability-change positive control: %s", result.ForLLM)
				assert.Equal(t, config.ToolPolicy(row.policy), after.Agent.Tools.Builtin.Policies["bash"])
				require.Len(t, after.Agent.Tools.MCP.Servers, 1)
				assert.Equal(t, "docs", after.Agent.Tools.MCP.Servers[0].ID)
				assert.True(t, after.Agent.Tools.MCP.Servers[0].ToolsSpecified)
				assert.Empty(t, after.Agent.Tools.MCP.Servers[0].Tools)
				assert.Equal(t, before.Agent.Name, after.Agent.Name, "permission tool must not edit identity")
				assert.Equal(t, before.Soul, after.Soul)
				assert.NotEqual(t, before.Revision, after.Revision)
			}
		})
	}
}
