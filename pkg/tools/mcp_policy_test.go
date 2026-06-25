// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests proving that FilterToolsByPolicy enforces deny policies on MCP-sourced tools.
//
// This closes the coverage gap where MCP tools' policy enforcement is only assumed:
// previously no test verified that an MCP tool with a "deny" entry is excluded from
// the filtered set sent to the LLM.
//
// Enforcement level tested: FilterToolsByPolicy (pkg/tools/compositor.go:218).
// This is the exact gate the agent loop invokes at LLM-call assembly time
// (loop.go ~4788). Testing at this level is the most direct faithful proof —
// no full AgentLoop turn required.
//
// Traces to: tool-registry-redesign-spec.md FR-027 (source discriminator),
// pkg/tools/compositor.go FilterToolsByPolicy (policy enforcement gate).

package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mcpCategoryTool is a minimal Tool whose Category() returns CategoryMCP.
// Used to represent a tool sourced from an MCP server in FilterToolsByPolicy tests.
// CategoryMCP is what mcpToolAdapter (compositor.go:337) returns in production.
type mcpCategoryTool struct {
	name string
}

func (m *mcpCategoryTool) Name() string               { return m.name }
func (m *mcpCategoryTool) Description() string        { return "mcp cat tool: " + m.name }
func (m *mcpCategoryTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (m *mcpCategoryTool) Scope() ToolScope           { return ScopeGeneral }
func (m *mcpCategoryTool) Category() ToolCategory     { return CategoryMCP }
func (m *mcpCategoryTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return SilentResult("ok")
}

// TestFilterToolsByPolicy_DeniedMCPTool_ExcludedFromLLMView verifies that when an
// agent's ToolPolicyCfg maps an MCP tool's name to "deny", FilterToolsByPolicy
// removes it from the tool set that would be sent to the LLM.
//
// BDD: Given an agent's available tools include an MCP-category tool named "mcp_uat_search"
//
//	And ToolPolicyCfg maps "mcp_uat_search" to "deny"
//	When FilterToolsByPolicy is applied,
//	Then "mcp_uat_search" is absent from the returned tool list.
//
// Enforcement level: FilterToolsByPolicy — the exact gate used at LLM-call assembly
// time in loop.go. No full AgentLoop turn required; this is cheaper and equally
// faithful because the enforcement path is a direct call to this function.
//
// Traces to: pkg/tools/compositor.go FilterToolsByPolicy Layer 2 (effective policy gate).
func TestFilterToolsByPolicy_DeniedMCPTool_ExcludedFromLLMView(t *testing.T) {
	const deniedTool = "mcp_uat_search"
	const allowedTool = "mcp_uat_echo"

	allTools := []Tool{
		&mcpCategoryTool{name: deniedTool},
		&mcpCategoryTool{name: allowedTool},
		makeScopedTool("search_web", ScopeGeneral), // builtin, unrelated
	}

	policyCfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			deniedTool: "deny",
		},
	}

	filtered, policyMap := FilterToolsByPolicy(allTools, "custom", policyCfg)

	// Denied MCP tool must not appear in the filtered list.
	for _, tool := range filtered {
		assert.NotEqual(t, deniedTool, tool.Name(),
			"denied MCP tool %q must be excluded from filtered set", deniedTool)
	}

	// The allowed MCP sibling must still be present.
	var gotAllowed bool
	for _, tool := range filtered {
		if tool.Name() == allowedTool {
			gotAllowed = true
		}
	}
	assert.True(t, gotAllowed,
		"allowed MCP tool %q must survive filtering; only %q is denied", allowedTool, deniedTool)

	// The denied tool must not appear in policyMap either (it was dropped, not recorded).
	_, inMap := policyMap[deniedTool]
	assert.False(t, inMap,
		"denied tool must not appear in policyMap — it was excluded, not just downgraded")
}

// TestFilterToolsByPolicy_MCPToolRegistryRoundTrip verifies that an MCP tool
// registered via MCPRegistry.RegisterServerTools and then retrieved via All()
// is correctly filtered by FilterToolsByPolicy when a deny policy applies.
//
// This is the full round-trip: MCPRegistry → All() → FilterToolsByPolicy.
// It proves the deny enforcement works end-to-end from registry storage to
// the LLM-assembly gate.
//
// BDD: Given an MCPRegistry with server "uat" offering two tools:
//
//	"mcp_uat_allowed" and "mcp_uat_denied",
//	And ToolPolicyCfg denies "mcp_uat_denied",
//	When the tools from All() are passed through FilterToolsByPolicy,
//	Then "mcp_uat_denied" is absent and "mcp_uat_allowed" is present.
func TestFilterToolsByPolicy_MCPToolRegistryRoundTrip(t *testing.T) {
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	collisions := reg.RegisterServerTools("uat", []Tool{
		&mcpCategoryTool{name: "mcp_uat_allowed"},
		&mcpCategoryTool{name: "mcp_uat_denied"},
	}, builtins)
	require.Empty(t, collisions, "test tool registration must not produce collisions")

	allFromRegistry := reg.All()
	require.Len(t, allFromRegistry, 2, "MCPRegistry must contain both tools before filtering")

	policyCfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"mcp_uat_denied": "deny",
		},
	}

	filtered, _ := FilterToolsByPolicy(allFromRegistry, "custom", policyCfg)

	var names []string
	for _, t := range filtered {
		names = append(names, t.Name())
	}

	assert.NotContains(t, names, "mcp_uat_denied",
		"denied MCP tool must be excluded from filtered set")
	assert.Contains(t, names, "mcp_uat_allowed",
		"allowed MCP tool must survive filtering")
}
