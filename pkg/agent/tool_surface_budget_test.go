// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// mcpishTool stands in for an MCP-server tool: lazy tier (its name is not in
// fullManifestToolNames), with a realistically-sized schema and description.
// Real MCP tools are registered through ToolRegistry.Register exactly like
// these (pkg/agent/loop_mcp.go), which is why they land in ToProviderDefs.
type mcpishTool struct {
	tools.BaseTool
	n string
}

func (m *mcpishTool) Name() string { return m.n }
func (m *mcpishTool) Description() string {
	return "Query the remote service for records matching a filter expression, " +
		"returning a paginated result set with cursors, totals and per-row metadata."
}

func (m *mcpishTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filter": map[string]any{"type": "string", "description": "Filter expression to apply."},
			"limit":  map[string]any{"type": "integer", "description": "Maximum rows to return."},
			"cursor": map[string]any{"type": "string", "description": "Opaque pagination cursor."},
			"fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"filter"},
	}
}
func (m *mcpishTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (m *mcpishTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "ok"}
}

// newBudgetTestLoop builds a minimal AgentLoop with the compressed manifest ON
// (the shipped default) so sentToolSurfaceTokens takes its real path.
func newBudgetTestLoop(t *testing.T, compressed bool) *AgentLoop {
	t.Helper()
	cfg := &config.Config{}
	cfg.Tools.Manifest.Compressed = compressed
	al := &AgentLoop{}
	al.cfg = cfg
	return al
}

func registerLazyTools(reg *tools.ToolRegistry, n int) {
	for i := range n {
		reg.Register(&mcpishTool{n: fmt.Sprintf("mcp_remote_query_%03d", i)})
	}
}

// TestSentToolSurface_LazyToolsAreNotChargedAsFullSchemas is the operator's
// case: connect ~15 MCP servers (200 lazy tools) and the history budget must
// stay usable. Before the fix each lazy tool was charged a full JSON schema
// even though none of them is sent — the over-count exceeded the whole context
// window and drove the history budget negative, so the trimmer bottomed out on
// its FR-003 floor and kept only the last user message, on every turn.
func TestSentToolSurface_LazyToolsAreNotChargedAsFullSchemas(t *testing.T) {
	al := newBudgetTestLoop(t, true)

	reg := tools.NewToolRegistry()
	registerLazyTools(reg, 200)
	agent := &AgentInstance{ID: "budget-agent", Tools: reg, ContextWindow: 128000}

	got := al.sentToolSurfaceTokens(agent, "sess-1")

	// What the old code charged: a full schema for every registered tool.
	naive := estimateToolDefsTokens(reg.ToProviderDefs())

	require.Positive(t, naive, "sanity: the naive count must be non-zero")
	require.Less(t, got, naive/2,
		"200 lazy tools are NOT sent — they cost one manifest line each, not a full schema. "+
			"charged=%d naive(full-schema-for-all)=%d", got, naive)

	// The property that actually matters: history still has room.
	const maxTokens = 4096
	budget := agent.ContextWindow - maxTokens - (agent.ContextWindow+19)/20 - got
	require.Positive(t, budget,
		"the history budget must stay positive with 200 lazy tools connected; "+
			"tool surface charged %d of a %d window", got, agent.ContextWindow)
	require.Greater(t, budget, 50000,
		"with 200 unsent tools the agent should still retain a usable window, got budget=%d", budget)
}

// TestSentToolSurface_LoadedLazyToolIsChargedFully pins the other direction:
// once a lazy tool is actually loaded for the session it IS sent, so it must be
// charged its real schema cost. Fixing the over-count must not become an
// under-count.
func TestSentToolSurface_LoadedLazyToolIsChargedFully(t *testing.T) {
	al := newBudgetTestLoop(t, true)

	reg := tools.NewToolRegistry()
	registerLazyTools(reg, 20)
	agent := &AgentInstance{ID: "budget-agent", Tools: reg, ContextWindow: 128000}

	before := al.sentToolSurfaceTokens(agent, "sess-load")

	// Mark one lazy tool loaded for this session, as ToolSearch would.
	al.loadedToolsMu.Lock()
	if al.loadedTools == nil {
		al.loadedTools = map[string]map[string]bool{}
	}
	al.loadedTools["sess-load"] = map[string]bool{"mcp_remote_query_000": true}
	al.loadedToolsMu.Unlock()

	after := al.sentToolSurfaceTokens(agent, "sess-load")

	require.Greater(t, after, before,
		"a lazy tool that has been LOADED is sent as a full schema and must be charged for it; "+
			"before=%d after=%d", before, after)
}

// TestSentToolSurface_UncompressedChargesEverything: when the compressed
// manifest is off, every tool really is sent, so the whole registry is the
// correct answer. The fix must not quietly under-charge that configuration.
func TestSentToolSurface_UncompressedChargesEverything(t *testing.T) {
	al := newBudgetTestLoop(t, false)

	reg := tools.NewToolRegistry()
	registerLazyTools(reg, 30)
	agent := &AgentInstance{ID: "budget-agent", Tools: reg, ContextWindow: 128000}

	got := al.sentToolSurfaceTokens(agent, "sess-2")
	want := estimateToolDefsTokens(reg.ToProviderDefs())

	require.Equal(t, want, got,
		"with the compressed manifest OFF every tool is sent, so the full-registry count is correct")
}
