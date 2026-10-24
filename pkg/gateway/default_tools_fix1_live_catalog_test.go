package gateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
	reader, ok := owner.Tools.Get("get_agent_tools")
	require.True(t, ok, "retrieve the real production-wired tool before metadata changes")
	ctx := tools.WithAgentID(context.Background(), "omnipus")
	read := func(extra ...tools.Tool) string {
		state, err := agentstore.New(rc.homePath).ReadState(id)
		require.NoError(t, err)
		result := reader.Execute(ctx, map[string]any{"id": id})
		require.False(t, result.IsError, result.ForLLM)
		header := fmt.Sprintf("agent: %s | name: Catalog Subject | kind: main | revision: %s", id, state.Revision)
		expected := append(rc.centralBuiltinReg.All(), extra...)
		fix1InventoryPolicyRows(t, result.ForLLM, header, expected)
		return result.ForLLM
	}
	read() // the reader is already wired and used while the MCP registry is empty
	lookup := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "lookup", Description: "Look up a document. Old details."})
	obsolete := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "obsolete", Description: "Old operation."})
	private := tools.NewMCPTool(nil, "private", &mcp.Tool{Name: "lookup", Description: "Do not expose this tool."})
	require.Empty(t, rc.centralMCPReg.RegisterServerTools("docs", []tools.Tool{lookup, obsolete}, rc.centralBuiltinReg))
	require.Empty(t, rc.centralMCPReg.RegisterServerTools("private", []tools.Tool{private}, rc.centralBuiltinReg))
	first := read(lookup, obsolete)
	assert.Contains(t, strings.Split(first, "\n"), lookup.Name()+" | allow | Look up a document.", "T1: post-wiring metadata must appear with its exact compact row")
	assert.NotContains(t, first, private.Name(), "unassigned-server control")

	updated := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "lookup", Description: "Find the updated document. New details."})
	added := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "new_operation", Description: "New operation."})
	require.Empty(t, rc.centralMCPReg.RegisterServerTools("docs", []tools.Tool{updated, added}, rc.centralBuiltinReg))
	second := read(updated, added)
	assert.Contains(t, strings.Split(second, "\n"), updated.Name()+" | allow | Find the updated document.", "T1: reconnect replaces the description")
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

// T1/T2 inspect producer membership and policies. Q21's exact description
// shape is independently enforced by defaultToolsParseCompact and its cases.
func fix1InventoryPolicyRows(t *testing.T, raw, header string, expected []tools.Tool) map[string]string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	require.Equal(t, header, lines[0])
	require.Len(t, lines, len(expected)+1, "every expected catalog tool appears once, with no extra sections")
	policies := map[string]string{}
	var names, wanted []string
	for _, line := range lines[1:] {
		parts := strings.SplitN(line, " | ", 3)
		require.GreaterOrEqual(t, len(parts), 2)
		_, duplicate := policies[parts[0]]
		require.False(t, duplicate)
		assert.Contains(t, []string{"allow", "ask", "deny"}, parts[1])
		policies[parts[0]] = parts[1]
		names = append(names, parts[0])
	}
	for _, tool := range expected {
		wanted = append(wanted, tool.Name())
	}
	sort.Strings(wanted)
	assert.Equal(t, wanted, names, "exact live catalog membership and byte order")
	return policies
}
