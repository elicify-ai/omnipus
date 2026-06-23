//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// MCP coverage tests for GET /api/v1/tools (FR-027).
//
// Closes the gap where MCP tools' presence in the registry endpoint and their
// source discriminator are only assumed, not asserted by any test.
//
// Tests added:
//   - TestREST_GetTools_IncludesMCPTools — GET /api/v1/tools returns an entry
//     with source="mcp" when an MCP server is wired into api.mcpRegistry.
//   - TestREST_GetTools_MCPToolDedup — a tool name registered first as builtin
//     is NOT replaced by an MCP registration (first-wins dedup).

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// registryMCPTestTool is a minimal Tool implementation used to seed the MCPRegistry
// in gateway-level tests. It lives in this file to avoid polluting the other test
// files and to keep the tool type name unambiguous (distinct from mcpTestTool in
// pkg/tools, which is in a different package).
type registryMCPTestTool struct {
	name string
}

func (t *registryMCPTestTool) Name() string               { return t.name }
func (t *registryMCPTestTool) Description() string        { return "mcp tool: " + t.name }
func (t *registryMCPTestTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (t *registryMCPTestTool) Scope() tools.ToolScope     { return tools.ScopeGeneral }
func (t *registryMCPTestTool) RequiresAdminAsk() bool     { return false }
func (t *registryMCPTestTool) Category() tools.ToolCategory {
	return tools.CategoryMCP
}
func (t *registryMCPTestTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.SilentResult("ok")
}

// newTestRestAPIWithMCPRegistry returns a restAPI with a live MCPRegistry wired in.
// The MCPRegistry contains one server ("uat") with one tool (toolName).
// builtinRegistry is left nil so HandleToolsRegistry does NOT fall back to the
// agent loop's tool set — the only source of tools is the MCP registry.
//
// Note: Keeping builtinRegistry=nil means the fallback path (reading from the
// default agent's tool set) is active in addition to the MCP registry path.
// The handler appends MCP tools after builtins (or the fallback set), so the
// MCP entry is always present in the response.
func newTestRestAPIWithMCPRegistry(t *testing.T, toolName string) (*restAPI, *tools.MCPRegistry) {
	t.Helper()
	api := newTestRestAPIWithHome(t)

	mcpReg := tools.NewMCPRegistry()
	builtins := tools.NewBuiltinRegistry()
	collisions := mcpReg.RegisterServerTools("uat", []tools.Tool{
		&registryMCPTestTool{name: toolName},
	}, builtins)
	require.Empty(t, collisions, "test MCP tool registration must not produce collisions")

	api.mcpRegistry = mcpReg
	return api, mcpReg
}

// TestREST_GetTools_IncludesMCPTools verifies that GET /api/v1/tools returns an
// entry with source="mcp" when an MCP server is wired into api.mcpRegistry.
//
// BDD: Given a gateway with an MCPRegistry containing server "uat" and tool "mcp_uat_echo",
//
//	When GET /api/v1/tools is called with a valid auth token,
//	Then 200 with a JSON array that contains an entry where
//	  name == "mcp_uat_echo" AND source == "mcp".
//
// Enforcement level: REST handler (HandleToolsRegistry) — verifies that the
// handler iterates mcpRegistry.All() and tags entries with source="mcp".
// Traces to: tool-registry-redesign-spec.md FR-027 (source discriminator).
func TestREST_GetTools_IncludesMCPTools(t *testing.T) {
	const mcpToolName = "mcp_uat_echo"
	api, _ := newTestRestAPIWithMCPRegistry(t, mcpToolName)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolsRegistry(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /api/v1/tools must return 200: %s", w.Body)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries), "response must unmarshal as array")

	// Find the MCP tool entry by name.
	var mcpEntry map[string]any
	for _, e := range entries {
		if e["name"] == mcpToolName {
			mcpEntry = e
			break
		}
	}
	require.NotNil(t, mcpEntry, "response must contain an entry with name=%q; got %d entries total", mcpToolName, len(entries))

	// Assert source discriminator is "mcp" (FR-027).
	assert.Equal(t, "mcp", mcpEntry["source"],
		"MCP tool entry must have source=\"mcp\"; got %q", mcpEntry["source"])

	// Assert the other required fields are present and non-empty.
	assert.NotEmpty(t, mcpEntry["name"], "entry must have non-empty name")
	assert.Contains(t, mcpEntry, "description", "entry must contain description")
	assert.Contains(t, mcpEntry, "scope", "entry must contain scope")
	assert.Contains(t, mcpEntry, "category", "entry must contain category")
}

// TestREST_GetTools_MCPToolDedup verifies the first-registration-wins deduplication
// rule: when a tool name appears first as a builtin, the MCP registry's entry for
// the same name is silently dropped from the GET /api/v1/tools response.
//
// BDD: Given a gateway with:
//   - builtinRegistry containing a tool named "shared.tool" (source="builtin")
//   - mcpRegistry containing server "uat" also offering "shared.tool"
//
// When GET /api/v1/tools is called,
// Then the response contains exactly one entry for "shared.tool" with source="builtin".
//
// Traces to: rest_tool_registry.go — addTool dedup (first-wins, FR-027 dedup note).
func TestREST_GetTools_MCPToolDedup(t *testing.T) {
	const sharedName = "shared.tool"

	api := newTestRestAPIWithHome(t)

	// Wire a builtin registry with the shared tool name.
	builtinReg := tools.NewBuiltinRegistry()
	err := builtinReg.RegisterBuiltin(&registryMCPTestTool{name: sharedName})
	require.NoError(t, err, "must register builtin tool")
	api.builtinRegistry = builtinReg

	// Wire an MCP registry with the same tool name.
	mcpReg := tools.NewMCPRegistry()
	// Pass nil builtins here so the name isn't rejected for system.* prefix.
	mcpReg.RegisterServerTools("uat", []tools.Tool{
		&registryMCPTestTool{name: sharedName},
	}, nil)
	api.mcpRegistry = mcpReg

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolsRegistry(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// Count occurrences of shared.tool.
	var found []map[string]any
	for _, e := range entries {
		if e["name"] == sharedName {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1, "shared.tool must appear exactly once (dedup)")
	assert.Equal(t, "builtin", found[0]["source"],
		"dedup: builtin registration must win over MCP; got source=%q", found[0]["source"])
}
