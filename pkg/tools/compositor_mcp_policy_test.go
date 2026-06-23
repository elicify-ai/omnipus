// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// MCP-specific policy resolution tests for FilterToolsByPolicy.
//
// These tests close a coverage gap: the deny>ask>allow logic was previously
// proven only on builtin tools. They exercise the same paths with MCP-sourced
// tools (Category() == CategoryMCP).
//
// IMPORTANT — name fidelity: at runtime MCP tools reach the policy layer through
// MCPTool.Name() (pkg/tools/mcp_tool.go), which formats names as
// "mcp_<sanitizedServer>_<sanitizedTool>" — UNDERSCORE-delimited, no dots
// (sanitizeIdentifierComponent strips dots). These tests therefore use realistic
// underscore names (e.g. "mcp_codeserver_search"), NOT dot-namespaced names.
// This matters for wildcards: the policy matcher only supports trailing ".*"
// keys matched on dot segments (resolveFromMap), so a real MCP name has a single
// segment and CANNOT be caught by any "*.*" wildcard — only an exact key denies
// it. TestFilterToolsByPolicy_MCPTool_WildcardDoesNotMatch_ExactKeyRequired
// characterizes that (a real product limitation: you can't bulk-deny a server's
// MCP tools with a wildcard).
//
// Construction pattern: the makeMCPAdapter helper builds a Tool whose Name() is
// the supplied (already runtime-formatted) name and whose Category() is
// CategoryMCP — what FilterToolsByPolicy keys on. For RequiresAdminAsk, the tool
// is round-tripped through MCPRegistry.RegisterServerToolsWithOpts.
//
// Traces to: FR-009, FR-034, FR-061, FR-064, FR-071 (tool-registry-redesign-spec.md)

package tools

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeMCPAdapter is a test helper that builds a real mcpToolAdapter for a named
// MCP tool on the given server. The adapter uses a testMCPCaller stub.
func makeMCPAdapter(serverName, toolName string) Tool {
	caller := &testMCPCaller{serverTools: map[string][]*mcp.Tool{}}
	toolDef := &mcp.Tool{
		Name:        toolName,
		Description: "mcp policy test tool: " + toolName,
	}
	return newMCPToolAdapter(serverName, toolDef, caller)
}

// makeMCPAdapters returns a slice of mcpToolAdapter instances, all on the same
// server, with the given names.
func makeMCPAdapters(serverName string, names ...string) []Tool {
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, makeMCPAdapter(serverName, n))
	}
	return out
}

// TestFilterToolsByPolicy_MCPToolGlobalDeny verifies that a global "deny" policy
// targeting a specific MCP-sourced tool removes it from the output.
//
// BDD:
//
//	Given an MCP tool "mcp_codeserver_search" (runtime mcp_<server>_<tool> form),
//	And GlobalPolicies{"mcp_codeserver_search": "deny"},
//	When FilterToolsByPolicy is called with agentType "custom",
//	Then "mcp_codeserver_search" is absent from the result and the policyMap.
//
// Traces to: FR-009 (global deny), compositor.go resolveEffectivePolicyWith.
func TestFilterToolsByPolicy_MCPToolGlobalDeny(t *testing.T) {
	tools := makeMCPAdapters("code-server", "mcp_codeserver_search", "mcp_codeserver_lint")

	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalPolicies:      map[string]string{"mcp_codeserver_search": "deny"},
		GlobalDefaultPolicy: "allow",
	}

	got, policyMap := FilterToolsByPolicy(tools, "custom", cfg)

	for _, tool := range got {
		assert.NotEqual(t, "mcp_codeserver_search", tool.Name(),
			"globally denied MCP tool must be absent from result")
	}
	_, inMap := policyMap["mcp_codeserver_search"]
	assert.False(t, inMap, "globally denied MCP tool must not appear in policyMap")

	// Control: sibling tool with no deny policy must survive.
	assert.Contains(t, policyMap, "mcp_codeserver_lint",
		"undeniable sibling MCP tool must still be in policyMap")
	assert.Equal(t, "allow", policyMap["mcp_codeserver_lint"],
		"undeniable sibling MCP tool must have policy 'allow'")
}

// TestFilterToolsByPolicy_MCPToolGlobalDeny_OverridesAgentAllow proves that a
// global "deny" cannot be relaxed by a per-agent "allow" on the same MCP tool.
// This is the deny>ask>allow merge rule applied to MCP tools.
//
// BDD:
//
//	Given GlobalPolicies{"mcp_execsvr_run": "deny"} and Policies{"mcp_execsvr_run": "allow"},
//	When FilterToolsByPolicy is called,
//	Then "mcp_execsvr_run" is absent from the result (effective policy = deny).
//
// Traces to: FR-009, compositor.go resolveEffectivePolicyWith (deny wins).
func TestFilterToolsByPolicy_MCPToolGlobalDeny_OverridesAgentAllow(t *testing.T) {
	tools := makeMCPAdapters("exec-server", "mcp_execsvr_run", "mcp_execsvr_read")

	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"mcp_execsvr_run": "allow"},
		GlobalPolicies:      map[string]string{"mcp_execsvr_run": "deny"},
		GlobalDefaultPolicy: "allow",
	}

	got, policyMap := FilterToolsByPolicy(tools, "custom", cfg)

	for _, tool := range got {
		assert.NotEqual(t, "mcp_execsvr_run", tool.Name(),
			"global deny must win over per-agent allow for MCP tool")
	}
	_, inMap := policyMap["mcp_execsvr_run"]
	assert.False(t, inMap,
		"globally denied MCP tool must not appear in policyMap even with agent allow")

	// "mcp_execsvr_read" is unaffected: global allow + agent allow → allow.
	assert.Equal(t, "allow", policyMap["mcp_execsvr_read"],
		"sibling MCP tool with no deny must be allowed")
}

// TestFilterToolsByPolicy_MCPTool_WildcardDoesNotMatch_ExactKeyRequired
// characterizes a real limitation: because runtime MCP tool names are
// underscore-delimited single segments ("mcp_<server>_<tool>") and the policy
// matcher only supports trailing ".*" keys matched on DOT segments
// (resolveFromMap / buildWildcardIndex, FR-009/FR-071), NO "*.*" wildcard can
// catch an MCP tool. The ONLY way to deny an MCP tool is an exact key. A
// consequence: you cannot bulk-deny all of a server's MCP tools with one
// wildcard — see UAT plan KI-22.
//
// BDD:
//
//	Given MCP tools ["mcp_search_query", "mcp_search_index"] from one server,
//	When GlobalPolicies{"mcp.*": "deny"} (and "mcp_search.*": "deny") are applied,
//	Then BOTH tools survive (the dot-segment wildcard matches neither underscore name).
//	But when GlobalPolicies{"mcp_search_query": "deny"} (exact) is applied,
//	Then "mcp_search_query" is denied and "mcp_search_index" survives.
func TestFilterToolsByPolicy_MCPTool_WildcardDoesNotMatch_ExactKeyRequired(t *testing.T) {
	mkTools := func() []Tool {
		return makeMCPAdapters("search-server", "mcp_search_query", "mcp_search_index")
	}

	// 1. Dot-segment wildcards do NOT match underscore MCP names — both survive.
	for _, wildcard := range []string{"mcp.*", "mcp_search.*"} {
		cfg := &ToolPolicyCfg{
			DefaultPolicy:       "allow",
			GlobalPolicies:      map[string]string{wildcard: "deny"},
			GlobalDefaultPolicy: "allow",
		}
		got, policyMap := FilterToolsByPolicy(mkTools(), "custom", cfg)
		assert.Len(t, got, 2,
			"wildcard %q must NOT match underscore MCP names — both tools survive", wildcard)
		assert.Equal(t, "allow", policyMap["mcp_search_query"],
			"wildcard %q must leave mcp_search_query allowed (no dot-segment match)", wildcard)
		assert.Equal(t, "allow", policyMap["mcp_search_index"],
			"wildcard %q must leave mcp_search_index allowed", wildcard)
	}

	// 2. An EXACT key is the working mechanism — denies just that one tool.
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalPolicies:      map[string]string{"mcp_search_query": "deny"},
		GlobalDefaultPolicy: "allow",
	}
	got, policyMap := FilterToolsByPolicy(mkTools(), "custom", cfg)
	require.Len(t, got, 1, "exact deny must remove exactly one MCP tool")
	assert.Equal(t, "mcp_search_index", got[0].Name(),
		"only the non-denied sibling survives the exact-key deny")
	_, denied := policyMap["mcp_search_query"]
	assert.False(t, denied, "mcp_search_query must be denied by its exact key")
}

// TestFilterToolsByPolicy_MCPToolAdminAskFence verifies that for a custom agent,
// when an MCP tool has RequiresAdminAsk()==true and its effective policy resolves
// to "allow", FilterToolsByPolicy downgrades the effective policy to "ask"
// (FR-061 admin-ask fence).
//
// Construction: we use MCPRegistry.RegisterServerToolsWithOpts with
// RequiresAdminAsk: ["dangerous_mcp_tool"] to obtain a real mcpAdminAskTool
// from the registry, then pass it directly to FilterToolsByPolicy.
//
// Traces to: FR-061 (admin-ask fence), FR-064 (RequiresAdminAsk opt-in).
func TestFilterToolsByPolicy_MCPToolAdminAskFence(t *testing.T) {
	// Build an MCP tool that has RequiresAdminAsk()==true via the registry path.
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	collisions := reg.RegisterServerToolsWithOpts("secure-server", []Tool{
		makeMCPAdapter("secure-server", "mcp_secure_dangerous"),
		makeMCPAdapter("secure-server", "mcp_secure_safe"),
	}, builtins, MCPServerOpts{
		RequiresAdminAsk: []string{"mcp_secure_dangerous"},
	})
	require.Empty(t, collisions, "registration must not produce collisions")

	// Retrieve from registry — mcp_secure_dangerous is now wrapped by mcpAdminAskTool.
	dangerousTool, ok := reg.Get("mcp_secure_dangerous")
	require.True(t, ok, "mcp_secure_dangerous must be registered")
	require.True(t, dangerousTool.RequiresAdminAsk(),
		"mcp_secure_dangerous must have RequiresAdminAsk()==true (FR-064)")

	safeTool, ok2 := reg.Get("mcp_secure_safe")
	require.True(t, ok2, "mcp_secure_safe must be registered")
	require.False(t, safeTool.RequiresAdminAsk(),
		"mcp_secure_safe must have RequiresAdminAsk()==false")

	// Both tools have effective policy "allow" — no global/agent deny.
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalDefaultPolicy: "allow",
		IsCoreAgent:         false, // custom agent: admin-ask fence applies
	}

	_, policyMap := FilterToolsByPolicy([]Tool{dangerousTool, safeTool}, "custom", cfg)

	// mcp_secure_dangerous must be present but fenced to "ask".
	p, inMap := policyMap["mcp_secure_dangerous"]
	assert.True(t, inMap,
		"mcp_secure_dangerous must pass the deny gate (effective policy is not deny)")
	assert.Equal(t, "ask", p,
		"mcp_secure_dangerous effective policy must be fenced to 'ask' by FR-061 for custom agent")

	// mcp_secure_safe has RequiresAdminAsk()==false: stays "allow".
	p2, inMap2 := policyMap["mcp_secure_safe"]
	assert.True(t, inMap2, "mcp_secure_safe must be in policyMap")
	assert.Equal(t, "allow", p2,
		"mcp_secure_safe with RequiresAdminAsk()==false must remain 'allow'")
}

// TestFilterToolsByPolicy_MCPToolAllowed_WhenNoDeny is the control case: an MCP
// tool with no contrary global or agent policy survives filtering with effective
// policy "allow".
//
// BDD:
//
//	Given an MCP tool "mcp_docs_search" with no policy overrides,
//	And DefaultPolicy="allow", GlobalDefaultPolicy="allow",
//	When FilterToolsByPolicy is called for agentType "custom",
//	Then "mcp_docs_search" appears in the result with policy "allow".
//
// Traces to: compositor.go FilterToolsByPolicy — default-allow path.
func TestFilterToolsByPolicy_MCPToolAllowed_WhenNoDeny(t *testing.T) {
	tools := makeMCPAdapters("docs-server",
		"mcp_docs_search",
		"mcp_docs_index",
	)

	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalDefaultPolicy: "allow",
	}

	got, policyMap := FilterToolsByPolicy(tools, "custom", cfg)

	require.Len(t, got, 2,
		"both MCP tools must pass when no deny policy is configured")

	for _, name := range []string{"mcp_docs_search", "mcp_docs_index"} {
		p, inMap := policyMap[name]
		assert.True(t, inMap, "%q must appear in policyMap", name)
		assert.Equal(t, "allow", p,
			"%q must have effective policy 'allow' with no contrary configuration", name)
	}
}

// TestFilterToolsByPolicy_MCPTool_ScopeGeneral_PassesForAnyAgentType verifies that
// MCP tools (which always have ScopeGeneral) pass the scope gate for both core
// and custom agent types, so the policy layer is the sole arbiter.
//
// Traces to: compositor.go passesScopeGate — ScopeGeneral always passes.
func TestFilterToolsByPolicy_MCPTool_ScopeGeneral_PassesForAnyAgentType(t *testing.T) {
	mcpTools := makeMCPAdapters("any-server", "mcp_anysvr_tool_a", "mcp_anysvr_tool_b")

	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalDefaultPolicy: "allow",
	}

	for _, agentType := range []string{"core", "custom", "worker"} {
		got, policyMap := FilterToolsByPolicy(mcpTools, agentType, cfg)
		assert.Len(t, got, 2,
			"both MCP tools must pass scope gate for agentType=%q", agentType)
		for _, name := range []string{"mcp_anysvr_tool_a", "mcp_anysvr_tool_b"} {
			p, inMap := policyMap[name]
			assert.True(t, inMap,
				"%q must be in policyMap for agentType=%q", name, agentType)
			assert.Equal(t, "allow", p,
				"%q must be 'allow' for agentType=%q", name, agentType)
		}
	}
}
