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
// characterizes that DOT-segment wildcards (".*") do NOT match underscore-delimited
// MCP tool names. "mcp.*" and "mcp_search.*" use dot delimiters and therefore
// never match "mcp_search_query" (a single-segment underscore name).
//
// Note: G10 added UNDERSCORE wildcard support ("_*") which DOES bulk-deny a server's
// MCP tools — see TestFilterToolsByPolicy_MCPTool_UnderscoreWildcard_BulkDeny below.
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
			"dot-segment wildcard %q must NOT match underscore MCP names — both tools survive", wildcard)
		assert.Equal(t, "allow", policyMap["mcp_search_query"],
			"dot-segment wildcard %q must leave mcp_search_query allowed (no dot-segment match)", wildcard)
		assert.Equal(t, "allow", policyMap["mcp_search_index"],
			"dot-segment wildcard %q must leave mcp_search_index allowed", wildcard)
	}

	// 2. An EXACT key denies just that one tool.
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

// TestFilterToolsByPolicy_MCPTool_UnderscoreWildcard_BulkDeny verifies G10: a
// trailing "_*" wildcard (e.g., "mcp_search_*") bulk-denies all tools from a
// server whose names start with "mcp_search_".
//
// BDD:
//
//	Given MCP tools ["mcp_search_query", "mcp_search_index"] from server "search-server",
//	When GlobalPolicies{"mcp_search_*": "deny"} is applied,
//	Then BOTH tools are denied and absent from the result.
//	When GlobalPolicies{"mcp_search_*": "deny", "mcp_search_query": "allow"} is applied,
//	Then "mcp_search_query" is allowed (exact wins) and "mcp_search_index" is denied.
//
// Traces to: G10 (underscore wildcard for MCP server bulk-deny).
func TestFilterToolsByPolicy_MCPTool_UnderscoreWildcard_BulkDeny(t *testing.T) {
	mkTools := func() []Tool {
		return makeMCPAdapters("search-server", "mcp_search_query", "mcp_search_index")
	}

	// 1. "_*" wildcard bulk-denies all tools from the server.
	t.Run("bulk_deny_all_server_tools", func(t *testing.T) {
		cfg := &ToolPolicyCfg{
			DefaultPolicy:       "allow",
			GlobalPolicies:      map[string]string{"mcp_search_*": "deny"},
			GlobalDefaultPolicy: "allow",
		}
		got, policyMap := FilterToolsByPolicy(mkTools(), "custom", cfg)
		assert.Empty(t, got,
			"underscore wildcard mcp_search_* must deny all mcp_search_* tools")
		_, q := policyMap["mcp_search_query"]
		assert.False(t, q, "mcp_search_query must be denied by mcp_search_*")
		_, idx := policyMap["mcp_search_index"]
		assert.False(t, idx, "mcp_search_index must be denied by mcp_search_*")
	})

	// 2. Exact key beats the "_*" wildcard (exact-wins precedence).
	t.Run("exact_beats_underscore_wildcard", func(t *testing.T) {
		cfg := &ToolPolicyCfg{
			DefaultPolicy: "allow",
			GlobalPolicies: map[string]string{
				"mcp_search_*":     "deny",
				"mcp_search_query": "allow", // exact override
			},
			GlobalDefaultPolicy: "allow",
		}
		got, policyMap := FilterToolsByPolicy(mkTools(), "custom", cfg)
		require.Len(t, got, 1, "exact allow must override the wildcard deny for mcp_search_query")
		assert.Equal(t, "mcp_search_query", got[0].Name(),
			"exact-key allow must win over underscore wildcard deny")
		assert.Equal(t, "allow", policyMap["mcp_search_query"],
			"mcp_search_query must have effective policy 'allow' (exact beats wildcard)")
		_, idx := policyMap["mcp_search_index"]
		assert.False(t, idx,
			"mcp_search_index has no exact override, so the wildcard deny still applies")
	})

	// 3. Per-agent "_*" wildcard (not just global) also works.
	t.Run("agent_level_underscore_wildcard", func(t *testing.T) {
		cfg := &ToolPolicyCfg{
			DefaultPolicy:       "allow",
			Policies:            map[string]string{"mcp_search_*": "deny"},
			GlobalDefaultPolicy: "allow",
		}
		got, _ := FilterToolsByPolicy(mkTools(), "custom", cfg)
		assert.Empty(t, got,
			"agent-level mcp_search_* wildcard must also bulk-deny server tools")
	})
}

// TestFilterToolsByPolicy_MCPTool_UnderscoreWildcard_LongerPrefixWins verifies that
// when two "_*" wildcards could both match, the longer (more specific) prefix wins.
//
// BDD:
//
//	Given policies {"mcp_*": "ask", "mcp_search_*": "deny"},
//	When FilterToolsByPolicy resolves for "mcp_search_query",
//	Then effective policy is "deny" (longer "mcp_search_*" wins over "mcp_*").
//	When FilterToolsByPolicy resolves for "mcp_other_tool",
//	Then effective policy is "ask" (only "mcp_*" matches).
//
// Traces to: G10, FR-071 (longest-prefix-wins among wildcards).
func TestFilterToolsByPolicy_MCPTool_UnderscoreWildcard_LongerPrefixWins(t *testing.T) {
	allTools := append(
		makeMCPAdapters("search-server", "mcp_search_query"),
		makeMCPAdapters("other-server", "mcp_other_tool")...,
	)

	cfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		GlobalPolicies: map[string]string{
			"mcp_*":        "ask",
			"mcp_search_*": "deny",
		},
		GlobalDefaultPolicy: "allow",
	}

	got, policyMap := FilterToolsByPolicy(allTools, "custom", cfg)

	// mcp_search_query: "mcp_search_*" (longer) wins over "mcp_*" → deny.
	for _, tool := range got {
		assert.NotEqual(t, "mcp_search_query", tool.Name(),
			"mcp_search_query must be denied by longer mcp_search_* wildcard")
	}
	_, searchDenied := policyMap["mcp_search_query"]
	assert.False(t, searchDenied, "mcp_search_query must be absent (denied by mcp_search_*)")

	// mcp_other_tool: only "mcp_*" matches → ask.
	p, ok := policyMap["mcp_other_tool"]
	assert.True(t, ok, "mcp_other_tool must survive (mcp_* = ask, not deny)")
	assert.Equal(t, "ask", p, "mcp_other_tool must have effective policy 'ask' from mcp_*")
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

// TestFilterToolsByPolicy_BareWildcardKeysAreIgnored guards against the
// empty-prefix wildcard pathology (G10 review): a bare "_*" or ".*" policy key
// has an empty prefix and would otherwise match (nearly) every tool. The matcher
// must ignore such keys, leaving tools at their default policy.
func TestFilterToolsByPolicy_BareWildcardKeysAreIgnored(t *testing.T) {
	for _, bare := range []string{"_*", ".*"} {
		tools := makeMCPAdapters("any-server", "mcp_anysvr_alpha", "mcp_anysvr_beta")
		cfg := &ToolPolicyCfg{
			DefaultPolicy:       "allow",
			GlobalPolicies:      map[string]string{bare: "deny"},
			GlobalDefaultPolicy: "allow",
		}
		got, policyMap := FilterToolsByPolicy(tools, "custom", cfg)
		assert.Len(t, got, 2,
			"bare wildcard %q must be ignored — both tools survive at default allow", bare)
		assert.Equal(t, "allow", policyMap["mcp_anysvr_alpha"],
			"bare wildcard %q must not deny mcp_anysvr_alpha", bare)
	}
}
