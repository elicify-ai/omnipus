package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
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
//
// There is no default-policy fallback (CLAUDE.md hard constraint 6): every
// ToolPolicyCfg below relies on explicit Policies/GlobalPolicies entries. A
// tool with no exact-or-wildcard match on EITHER side fails closed to "deny"
// (see TestFilterToolsByPolicy_NilConfig_FailsClosedToDenyAll).

// TestFilterToolsByPolicy_GlobalDeny_RemovesTool verifies that a global "deny"
// on a specific tool removes it from the output regardless of agent policy.
func TestFilterToolsByPolicy_GlobalDeny_RemovesTool(t *testing.T) {
	cfg := &ToolPolicyCfg{
		GlobalPolicies: map[string]config.ToolPolicy{"search_web": "deny"},
	}

	got, policyMap := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	// NOTE: the range variable was previously named `t`, shadowing the
	// *testing.T parameter; a failure inside the loop called panic() instead
	// of t.Errorf(), which kills the whole test binary with zero `--- FAIL`
	// lines on failure (indistinguishable from a hang). Renamed to `tool` and
	// switched to t.Error so a real regression here reports as an ordinary
	// FAIL.
	for _, tool := range got {
		if tool.Name() == "search_web" {
			t.Error("search_web must be removed when globally denied")
		}
	}
	if _, exists := policyMap["search_web"]; exists {
		t.Error("denied tool must not appear in policyMap")
	}
}

// TestFilterToolsByPolicy_GlobalAsk_AgentAllow_EffectiveAsk verifies that when
// global policy is "ask" and agent policy is "allow", the effective result is "ask".
func TestFilterToolsByPolicy_GlobalAsk_AgentAllow_EffectiveAsk(t *testing.T) {
	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"search_web": "allow"},
		GlobalPolicies: map[string]config.ToolPolicy{"search_web": "ask"},
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
		Policies:       map[string]config.ToolPolicy{"search_web": "deny"},
		GlobalPolicies: map[string]config.ToolPolicy{"search_web": "allow"},
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
		Policies:       map[string]config.ToolPolicy{"search_web": "ask"},
		GlobalPolicies: map[string]config.ToolPolicy{"search_web": "allow"},
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
		Policies:       map[string]config.ToolPolicy{"search_web": "allow"},
		GlobalPolicies: map[string]config.ToolPolicy{"search_web": "allow"},
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
		Policies: map[string]config.ToolPolicy{"system.*": "deny"},
	}

	got, _ := FilterToolsByPolicy(allPolicyTools(), "core", cfg)

	for _, tool := range got {
		if tool.Name() == "system.agent.list" {
			t.Error("system.* tool must be blocked when Policies[\"system.*\"]=deny")
		}
	}
}

// TestFilterToolsByPolicy_ScopeCore_CustomAgent verifies that a ScopeCore tool
// is blocked for a custom agent when the effective policy is "deny", and
// passes through when explicitly allowed.
func TestFilterToolsByPolicy_ScopeCore_CustomAgent(t *testing.T) {
	// Custom agent + explicit deny policy for exec → blocked.
	denyCfg := &ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"exec": "deny"},
	}
	got, _ := FilterToolsByPolicy(allPolicyTools(), "custom", denyCfg)
	for _, tool := range got {
		if tool.Name() == "exec" {
			t.Error("core-scoped tool must be blocked for custom agent with deny policy")
		}
	}

	// Custom agent + explicit allow policy for exec → allowed through.
	allowCfg := &ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"exec": "allow"},
	}
	_, policyMap := FilterToolsByPolicy(allPolicyTools(), "custom", allowCfg)
	if _, ok := policyMap["exec"]; !ok {
		t.Error("core-scoped tool must be allowed for custom agent with explicit allow policy")
	}
}

// TestFilterToolsByPolicy_NilConfig_FailsClosedToDenyAll verifies that a nil
// config (no policy maps at all) fails closed to "deny" for every tool —
// CLAUDE.md hard constraint 6 forbids a hardcoded "allow" fallback. Coverage
// (an explicit global and/or per-agent entry for every static builtin tool,
// for every agent) is enforced structurally at boot and at every agent write
// by config.ValidateToolPolicyCoverage; a genuinely uncovered tool reaching
// this resolver is a bug signal, not a normal path.
func TestFilterToolsByPolicy_NilConfig_FailsClosedToDenyAll(t *testing.T) {
	got, policyMap := FilterToolsByPolicy(allPolicyTools(), "core", nil)

	if len(got) != 0 {
		t.Errorf("expected 0 tools for nil config (fail-closed, no coverage), got %d", len(got))
	}
	if len(policyMap) != 0 {
		t.Errorf("expected empty policyMap for nil config (fail-closed), got %v", policyMap)
	}
}

// TestFilterToolsByPolicy_UnknownScope_Denied verifies that a tool with an
// unknown/zero-value scope is denied (fail-closed) by the scope gate, while an
// explicitly-allowed general-scope tool still passes alongside it.
func TestFilterToolsByPolicy_UnknownScope_Denied(t *testing.T) {
	unknownScopeTool := makeScopedTool("mystery_tool", ToolScope("unknown"))
	toolSet := []Tool{unknownScopeTool, makeScopedTool("search_web", ScopeGeneral)}

	cfg := &ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"search_web": "allow"},
	}

	got, _ := FilterToolsByPolicy(toolSet, "core", cfg)

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

// TestFilterToolsByPolicy_UnknownScope_DeniedEvenWithMatchingAllowPolicy is
// the load-bearing scope-gate test.
//
// TestFilterToolsByPolicy_UnknownScope_Denied above (and, before this file
// was written, effectiveToolPolicyMatrix's sole "unknown-scope" case) both
// give the unknown-scope tool NO policy entry at all — so
// resolveEffectivePolicyWith's own no-coverage fail-closed branch
// ("g == \"\" && a == \"\"" in compositor.go) denies it before passesScopeGate
// is ever consulted. That was verified directly: with the scope gate
// short-circuited exactly as `if false && !passesScopeGate(...)` at
// compositor.go:278, `go test ./pkg/tools/` still reports `ok` — those tests
// pass through the wrong mechanism and cannot detect the gate's removal.
//
// This test isolates the gate: the unknown-scope tool has an EXPLICIT,
// matching "allow" entry on BOTH the agent and the global side, so the
// global×agent merge ALONE would resolve it to "allow". Only
// passesScopeGate's fail-closed default (compositor.go's `default: return
// false`) can still deny it. Deleting or bypassing the gate makes this test
// fail.
//
// Traces to: pkg/tools/compositor.go passesScopeGate doc comment ("the
// structural guard that policy cannot bypass"); CLAUDE.md Hard Constraint 6.
func TestFilterToolsByPolicy_UnknownScope_DeniedEvenWithMatchingAllowPolicy(t *testing.T) {
	unknownScopeTool := makeScopedTool("mystery_tool", ToolScope("unknown"))
	knownTool := makeScopedTool("search_web", ScopeGeneral)

	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"mystery_tool": "allow", "search_web": "allow"},
		GlobalPolicies: map[string]config.ToolPolicy{"mystery_tool": "allow", "search_web": "allow"},
	}

	got, policyMap := FilterToolsByPolicy([]Tool{unknownScopeTool, knownTool}, "core", cfg)

	for _, tool := range got {
		if tool.Name() == "mystery_tool" {
			t.Error("unknown-scope tool must be denied by the scope gate even when both agent and global policy explicitly allow it")
		}
	}
	if _, exists := policyMap["mystery_tool"]; exists {
		t.Error("unknown-scope tool must not appear in policyMap even when explicitly allowed by policy")
	}

	// Differentiation: the known-scope tool carrying the IDENTICAL "allow"
	// policy must still pass — proves this is a scope-specific denial, not a
	// blanket failure of FilterToolsByPolicy for this config.
	found := false
	for _, tool := range got {
		if tool.Name() == "search_web" {
			found = true
		}
	}
	if !found {
		t.Error("known-scope tool with the same explicit allow policy must still pass")
	}
	if p, ok := policyMap["search_web"]; !ok || p != "allow" {
		t.Errorf("expected search_web effective policy 'allow', got %q (ok=%v)", p, ok)
	}
}

// TestFilterToolsByPolicy_UnknownScope_WildcardAllow_StillDenied proves the
// gate survives a WILDCARD allow match too, not just an exact-name entry —
// wildcard resolution happens inside the same global×agent merge the gate
// sits in front of.
func TestFilterToolsByPolicy_UnknownScope_WildcardAllow_StillDenied(t *testing.T) {
	unknownScopeTool := makeScopedTool("mystery_probe", ToolScope("unknown"))

	cfg := &ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"mystery_*": "allow"},
	}

	got, policyMap := FilterToolsByPolicy([]Tool{unknownScopeTool}, "core", cfg)

	if len(got) != 0 {
		t.Errorf("expected unknown-scope tool matched by a wildcard allow to be denied, got %d tool(s) returned", len(got))
	}
	if _, exists := policyMap["mystery_probe"]; exists {
		t.Error("unknown-scope tool matched by wildcard allow must not appear in policyMap")
	}
}

// TestFilterToolsByPolicy_ZeroValueScope_DeniedEvenWithMatchingAllowPolicy
// covers the specific failure mode named in the defect report and in
// passesScopeGate's own doc comment: a tool whose Scope() method was never
// given a return value (Go's zero value for ToolScope is "") must be denied
// exactly like any other unknown scope, even with an explicit matching allow
// on both sides.
func TestFilterToolsByPolicy_ZeroValueScope_DeniedEvenWithMatchingAllowPolicy(t *testing.T) {
	zeroScopeTool := makeScopedTool("unset_scope_tool", ToolScope(""))

	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"unset_scope_tool": "allow"},
		GlobalPolicies: map[string]config.ToolPolicy{"unset_scope_tool": "allow"},
	}

	got, policyMap := FilterToolsByPolicy([]Tool{zeroScopeTool}, "core", cfg)

	if len(got) != 0 {
		t.Errorf("expected zero-value-scope tool to be denied (fail-closed), got %d tool(s) returned", len(got))
	}
	if _, exists := policyMap["unset_scope_tool"]; exists {
		t.Error("zero-value-scope tool must not appear in policyMap even when explicitly allowed by policy")
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
	policies := map[string]config.ToolPolicy{
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
		Policies: map[string]config.ToolPolicy{
			"system.*":        "ask",
			"system.config.*": "deny",
		},
	}
	toolSet := []Tool{
		makeScopedTool("system.config.set", ScopeCore),
		makeScopedTool("system.agent.list", ScopeCore),
	}
	got, policyMap := FilterToolsByPolicy(toolSet, "core", cfg)

	// system.config.set must be denied (removed from result).
	// NOTE: range variable renamed `t` -> `tool` (it shadowed *testing.T) and
	// panic() replaced with t.Error() — an unrecovered panic in a shadowed
	// loop kills the whole test binary with zero `--- FAIL` lines, which
	// this project's own conventions say to read as a hang, not a finding.
	for _, tool := range got {
		if tool.Name() == "system.config.set" {
			t.Error("system.config.set must be denied by more-specific wildcard")
		}
	}
	// system.agent.list must be "ask" (matched by system.*)
	if p, ok := policyMap["system.agent.list"]; !ok || p != "ask" {
		t.Errorf("system.agent.list must be 'ask' via system.* wildcard, got %q (ok=%v)", p, ok)
	}
}
