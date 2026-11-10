package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pr-test T1 / SPEC.md compact catalog and assignment contract: the actual
// production-wired reader must consult current registries, not startup data.
func TestDefaultToolsProductionInventoryTracksLiveCatalogAndAssignments(t *testing.T) {
	const id = "live-catalog-subject"
	rc := wu3BootRunContext(t, func(cfg *config.Config) {
		seed := coreagent.NewCustomAgentToolsCfg()
		seed.MCP.Servers = []config.AgentMCPServerBinding{{ID: "docs", ToolsSpecified: false}}
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: id, Name: "Catalog Subject", Type: config.AgentTypeCustom, Tools: seed})
		cfg.Sandbox.ToolPolicies["mcp_*"] = "allow"
	})
	owner, ok := rc.agentLoop.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	reader, ok := owner.Tools.Get("get_agent")
	require.True(t, ok, "retrieve the real production-wired tool before metadata changes")
	ctx := tools.WithAgentID(context.Background(), "omnipus")
	read := func(extra ...tools.Tool) string {
		state, err := agentstore.New(rc.homePath).ReadState(id)
		require.NoError(t, err)
		result := reader.Execute(ctx, map[string]any{"id": id, "fields": []any{"tool_permissions"}})
		require.False(t, result.IsError, result.ForLLM)
		identity := map[string]any{"id": id, "revision": state.Revision}
		expected := append(rc.centralBuiltinReg.All(), extra...)
		fix1InventoryPolicyRows(t, result.ForLLM, identity, expected)
		return result.ForLLM
	}
	read() // the reader is already wired and used while the MCP registry is empty
	lookup := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "lookup", Description: "Look up a document. Old details."})
	obsolete := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "obsolete", Description: "Old operation."})
	private := tools.NewMCPTool(nil, "private", &mcp.Tool{Name: "lookup", Description: "Do not expose this tool."})
	require.Empty(t, rc.centralMCPReg.RegisterServerTools("docs", []tools.Tool{lookup, obsolete}, rc.centralBuiltinReg))
	require.Empty(t, rc.centralMCPReg.RegisterServerTools("private", []tools.Tool{private}, rc.centralBuiltinReg))
	first := read(lookup, obsolete)
	assert.Contains(t, fix1Entries(t, first), fix1Entry(lookup.Name(), "allow", "Look up a document."), "T1: post-wiring metadata must appear with its exact inventory entry")
	assert.NotContains(t, first, private.Name(), "unassigned-server control")

	updated := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "lookup", Description: "Find the updated document. New details."})
	added := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "new_operation", Description: "New operation."})
	require.Empty(t, rc.centralMCPReg.RegisterServerTools("docs", []tools.Tool{updated, added}, rc.centralBuiltinReg))
	second := read(updated, added)
	assert.Contains(t, fix1Entries(t, second), fix1Entry(updated.Name(), "allow", "Find the updated document."), "T1: reconnect replaces the description")
	assert.NotContains(t, second, obsolete.Name(), "T1: reconnect evicts obsolete rows")
	assert.NotContains(t, second, "Look up a document.", "old description must not survive a reconnect")
	assert.NotContains(t, second, private.Name())

	_, err := agentstore.New(rc.homePath).Update(id, func(agent *config.AgentConfig) error {
		agent.Tools.MCP.Servers = nil
		return nil
	})
	require.NoError(t, err)
	_, stillLoaded := rc.centralMCPReg.Get(updated.Name())
	require.True(t, stillLoaded, "assignment removal must not be confused with metadata unloading")
	third := read()
	assert.NotContains(t, third, updated.Name(), "T1: removed assignment excludes still-loaded metadata")
	assert.NotContains(t, third, added.Name())
	assert.NotContains(t, third, private.Name())
}

// T1/T2 inspect producer membership and policies. The description shape is
// independently enforced by defaultToolsParseInventory and its cases.
func fix1InventoryPolicyRows(t *testing.T, raw string, identity map[string]any, expected []tools.Tool) map[string]string {
	t.Helper()
	return defaultToolsParseInventory(t, raw, identity, expected)
}

func fix1Entries(t *testing.T, raw string) []defaultToolsEntry {
	t.Helper()
	_, entries := defaultToolsDecode(t, raw)
	for i := range entries {
		entries[i].Override = nil // the live-catalog test asserts membership, policy and description
	}
	return entries
}

func fix1Entry(name, effective, description string) defaultToolsEntry {
	return defaultToolsEntry{Tool: name, Effective: effective, Description: &description}
}
