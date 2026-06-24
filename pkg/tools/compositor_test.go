package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// --- test doubles ---

// testMCPCaller implements MCPCaller for compositor tests.
type testMCPCaller struct {
	serverTools map[string][]*mcp.Tool
}

func (m *testMCPCaller) GetAllTools() map[string][]*mcp.Tool {
	return m.serverTools
}

func (m *testMCPCaller) CallTool(
	_ context.Context,
	_, _ string,
	_ map[string]any,
) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "mcp result"},
		},
	}, nil
}

// --- scopedMockTool — configurable-scope mock for FilterToolsByPolicy tests ---

// scopedMockTool is a mock Tool with a user-supplied ToolScope.
type scopedMockTool struct {
	name  string
	scope ToolScope
}

func (s *scopedMockTool) Name() string               { return s.name }
func (s *scopedMockTool) Description() string        { return "scoped mock tool" }
func (s *scopedMockTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (s *scopedMockTool) Scope() ToolScope           { return s.scope }
func (s *scopedMockTool) RequiresAdminAsk() bool     { return false }
func (s *scopedMockTool) Category() ToolCategory     { return CategoryCore }
func (s *scopedMockTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return SilentResult("ok")
}

func makeScopedTool(name string, scope ToolScope) Tool {
	return &scopedMockTool{name: name, scope: scope}
}

// allPolicyTools returns a representative tool set covering both scopes.
func allPolicyTools() []Tool {
	return []Tool{
		makeScopedTool("system.agent.list", ScopeCore),
		makeScopedTool("exec", ScopeCore),
		makeScopedTool("search_web", ScopeGeneral),
	}
}

// --- FilterToolsByPolicy tests ---

// TestFilterToolsByPolicy_GlobalDeny_RemovesTool verifies that a global "deny"
// on a specific tool removes it from the output regardless of agent policy.
func TestFilterToolsByPolicy_GlobalDeny_RemovesTool(t *testing.T) {
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalPolicies:      map[string]string{"search_web": "deny"},
		GlobalDefaultPolicy: "allow",
	}

	got, policyMap := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	for _, t := range got {
		if t.Name() == "search_web" {
			panic("web_search must be removed when globally denied")
		}
	}
	if _, exists := policyMap["search_web"]; exists {
		panic("denied tool must not appear in policyMap")
	}
}

// TestFilterToolsByPolicy_GlobalAsk_AgentAllow_EffectiveAsk verifies that when
// global policy is "ask" and agent policy is "allow", the effective result is "ask".
func TestFilterToolsByPolicy_GlobalAsk_AgentAllow_EffectiveAsk(t *testing.T) {
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"search_web": "allow"},
		GlobalPolicies:      map[string]string{"search_web": "ask"},
		GlobalDefaultPolicy: "allow",
	}

	_, policyMap := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	if p, ok := policyMap["search_web"]; !ok || p != "ask" {
		t.Errorf("expected effective policy 'ask' for web_search, got %q (ok=%v)", p, ok)
	}
}

// TestFilterToolsByPolicy_GlobalAllow_AgentDeny_EffectiveDeny verifies that
// agent-level "deny" wins over global "allow".
func TestFilterToolsByPolicy_GlobalAllow_AgentDeny_EffectiveDeny(t *testing.T) {
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"search_web": "deny"},
		GlobalPolicies:      map[string]string{},
		GlobalDefaultPolicy: "allow",
	}

	got, _ := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	for _, tool := range got {
		if tool.Name() == "search_web" {
			t.Error("web_search must be absent when agent policy is deny")
		}
	}
}

// TestFilterToolsByPolicy_GlobalAllow_AgentAsk_EffectiveAsk verifies that
// agent "ask" + global "allow" yields "ask".
func TestFilterToolsByPolicy_GlobalAllow_AgentAsk_EffectiveAsk(t *testing.T) {
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"search_web": "ask"},
		GlobalPolicies:      map[string]string{},
		GlobalDefaultPolicy: "allow",
	}

	_, policyMap := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	if p, ok := policyMap["search_web"]; !ok || p != "ask" {
		t.Errorf("expected effective policy 'ask' for web_search, got %q (ok=%v)", p, ok)
	}
}

// TestFilterToolsByPolicy_AllAllow verifies that global "allow" + agent "allow"
// yields effective "allow".
func TestFilterToolsByPolicy_AllAllow(t *testing.T) {
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"search_web": "allow"},
		GlobalPolicies:      map[string]string{"search_web": "allow"},
		GlobalDefaultPolicy: "allow",
	}

	_, policyMap := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	if p, ok := policyMap["search_web"]; !ok || p != "allow" {
		t.Errorf("expected effective policy 'allow' for web_search, got %q (ok=%v)", p, ok)
	}
}

// TestFilterToolsByPolicy_SystemWildcardDeny_BlocksSystemTools verifies that a
// per-agent "system.*: deny" wildcard policy blocks system.* tools.
func TestFilterToolsByPolicy_SystemWildcardDeny_BlocksSystemTools(t *testing.T) {
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"system.*": "deny"},
		GlobalDefaultPolicy: "allow",
	}

	got, _ := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	for _, tool := range got {
		if tool.Name() == "system.agent.list" {
			t.Error("system.* tool must be blocked when Policies[\"system.*\"]=deny")
		}
	}
}

// TestFilterToolsByPolicy_ScopeCore_BlockedForCustomUnlessExplicit verifies that
// a ScopeCore tool is blocked for a custom agent when the effective policy is "deny".
func TestFilterToolsByPolicy_ScopeCore_CustomAgent(t *testing.T) {
	// Custom agent + deny policy for exec → blocked
	denyCfg := &ToolPolicyCfg{
		DefaultPolicy:       "deny",
		GlobalDefaultPolicy: "allow",
	}
	got, _ := FilterToolsByPolicy(allPolicyTools(), "custom", denyCfg)
	for _, tool := range got {
		if tool.Name() == "exec" {
			t.Error("core-scoped tool must be blocked for custom agent with deny policy")
		}
	}

	// Custom agent + allow policy for exec → allowed through
	allowCfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalDefaultPolicy: "allow",
	}
	_, policyMap := FilterToolsByPolicy(allPolicyTools(), "custom", allowCfg)
	if _, ok := policyMap["exec"]; !ok {
		t.Error("core-scoped tool must be allowed for custom agent with allow policy")
	}
}

// TestFilterToolsByPolicy_EmptyConfig_DefaultsToAllow verifies that a nil config
// defaults to allow-all.
func TestFilterToolsByPolicy_EmptyConfig_DefaultsToAllow(t *testing.T) {
	got, policyMap := FilterToolsByPolicy(allPolicyTools(), "core", nil)

	if len(got) != 3 {
		t.Errorf("expected 3 tools for core agent with nil config, got %d", len(got))
	}
	for _, name := range []string{"system.agent.list", "exec", "search_web"} {
		if p, ok := policyMap[name]; !ok || p != "allow" {
			t.Errorf("expected policy 'allow' for %q, got %q (ok=%v)", name, p, ok)
		}
	}
}

// TestFilterToolsByPolicy_UnknownScope_Denied verifies that a tool with an
// unknown/zero-value scope is denied (fail-closed).
func TestFilterToolsByPolicy_UnknownScope_Denied(t *testing.T) {
	unknownScopeTool := makeScopedTool("mystery_tool", ToolScope("unknown"))
	tools := []Tool{unknownScopeTool, makeScopedTool("search_web", ScopeGeneral)}

	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalDefaultPolicy: "allow",
	}

	got, _ := FilterToolsByPolicy(tools, "core", cfg)

	for _, tool := range got {
		if tool.Name() == "mystery_tool" {
			t.Error("tool with unknown scope must be denied (fail-closed)")
		}
	}
	found := false
	for _, tool := range got {
		if tool.Name() == "search_web" {
			found = true
		}
	}
	if !found {
		t.Error("general-scope tool must still pass alongside an unknown-scope tool")
	}
}

// TestMCPToolAdapter_Execute_TextContent verifies that mcpToolAdapter.Execute
// forwards the call through MCPCaller and returns concatenated text content.
func TestMCPToolAdapter_Execute_TextContent(t *testing.T) {
	caller := &testMCPCaller{serverTools: map[string][]*mcp.Tool{}}

	toolDef := &mcp.Tool{
		Name:        "search_code",
		Description: "Search codebase",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
		},
	}
	adapter := newMCPToolAdapter("my-server", toolDef, caller)

	assert.Equal(t, "search_code", adapter.Name(), "adapter name must match tool definition")
	assert.Equal(t, "Search codebase", adapter.Description(), "adapter description must match tool definition")
	assert.NotNil(t, adapter.Parameters(), "adapter parameters must not be nil")

	result := adapter.Execute(context.Background(), map[string]any{"query": "test"})

	assert.False(t, result.IsError, "successful MCP call must not produce error result")
	assert.Equal(t, "mcp result", result.ForLLM, "adapter must return MCPCaller text content")
}

// TestMCPContentText_ConcatenatesTextContent verifies that mcpContentText
// joins TextContent entries without a separator and silently skips non-text items.
func TestMCPContentText_ConcatenatesTextContent(t *testing.T) {
	tests := []struct {
		name     string
		content  []mcp.Content
		expected string
	}{
		{
			name:     "nil content returns empty string",
			content:  nil,
			expected: "",
		},
		{
			name:     "single text item",
			content:  []mcp.Content{&mcp.TextContent{Text: "hello"}},
			expected: "hello",
		},
		{
			name: "multiple text items are concatenated without separator",
			content: []mcp.Content{
				&mcp.TextContent{Text: "first"},
				&mcp.TextContent{Text: "second"},
			},
			expected: "firstsecond",
		},
		{
			name: "non-text content is skipped",
			content: []mcp.Content{
				&mcp.TextContent{Text: "text only"},
				&mcp.ImageContent{Data: []byte("img"), MIMEType: "image/png"},
			},
			expected: "text only",
		},
		{
			name:     "all non-text content produces empty string",
			content:  []mcp.Content{&mcp.ImageContent{Data: []byte("img"), MIMEType: "image/jpeg"}},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpContentText(tc.content)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestBuildWildcardIndex_SegmentPrimarySort verifies FR-071: segment count is the
// primary sort key so "system.config.*" (2 segments) sorts before "system.*" (1 segment).
func TestBuildWildcardIndex_SegmentPrimarySort(t *testing.T) {
	policies := map[string]string{
		"system.*":        "ask",
		"system.config.*": "deny",
		"a.*":             "allow",
	}
	idx := buildWildcardIndex(policies)

	// "system.config.*" has 2 segments, "system.*" has 1, "a.*" has 1.
	// Expect: system.config.* first, then system.* and a.* (lex tiebreak: a.* < system.*).
	if len(idx) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(idx))
	}
	// "system.config.*" has 2 segments → first.
	if idx[0].prefix != "system.config" {
		t.Errorf("first entry must be system.config (most segments), got %q", idx[0].prefix)
	}
	// "system.*" and "a.*" both have 1 segment; "system" is 6 chars vs "a" is 1 char
	// → char-count tiebreak puts "system" before "a".
	if idx[1].prefix != "system" {
		t.Errorf("second entry must be system (longer prefix than a), got %q", idx[1].prefix)
	}
	if idx[2].prefix != "a" {
		t.Errorf("third entry must be a, got %q", idx[2].prefix)
	}
}

// TestFilterToolsByPolicy_WildcardSegmentPrecedence verifies that a more-specific
// wildcard (more segments) wins over a less-specific one when both match.
func TestFilterToolsByPolicy_WildcardSegmentPrecedence(t *testing.T) {
	// system.config.set matches both "system.*" (ask) and "system.config.*" (deny).
	// The more-specific "system.config.*" must win → deny.
	cfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"system.*":        "ask",
			"system.config.*": "deny",
		},
		GlobalDefaultPolicy: "allow",
	}
	tools := []Tool{
		makeScopedTool("system.config.set", ScopeCore),
		makeScopedTool("system.agent.list", ScopeCore),
	}
	got, policyMap := FilterToolsByPolicy(tools, "core", cfg)

	// system.config.set must be denied (removed from result)
	for _, t := range got {
		if t.Name() == "system.config.set" {
			panic("system.config.set must be denied by more-specific wildcard")
		}
	}
	// system.agent.list must be "ask" (matched by system.*)
	if p, ok := policyMap["system.agent.list"]; !ok || p != "ask" {
		t.Errorf("system.agent.list must be 'ask' via system.* wildcard, got %q (ok=%v)", p, ok)
	}
}
