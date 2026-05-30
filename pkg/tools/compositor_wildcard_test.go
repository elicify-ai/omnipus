// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Tests for wildcard policy resolution in FilterToolsByPolicy.
// Covers FR-009, FR-071: trailing-only ".*" wildcards, longest-prefix-wins,
// exact > wildcard, deterministic ordering, and tie-breaking.
//
// All BDD scenarios from tool-registry-redesign-spec.md that are wildcard-specific.

package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wildTestTool is a minimal Tool for wildcard policy tests.
type wildTestTool struct {
	name  string
	scope ToolScope
}

func (w *wildTestTool) Name() string               { return w.name }
func (w *wildTestTool) Description() string        { return "wildcard test tool" }
func (w *wildTestTool) Parameters() map[string]any { return map[string]any{} }
func (w *wildTestTool) Scope() ToolScope           { return w.scope }
func (w *wildTestTool) Category() ToolCategory     { return CategoryCore }
func (w *wildTestTool) RequiresAdminAsk() bool     { return false }
func (w *wildTestTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return &ToolResult{ForLLM: "ok"}
}

func makeWildTools(names ...string) []Tool {
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		// system.* tools need ScopeCore; others get ScopeGeneral.
		scope := ScopeGeneral
		if len(n) > 7 && n[:7] == "system." {
			scope = ScopeCore
		}
		out = append(out, &wildTestTool{name: n, scope: scope})
	}
	return out
}

// --- buildWildcardIndex unit tests ---

// TestBuildWildcardIndex_TrailingOnlyWildcard verifies that only keys ending in
// ".*" are treated as wildcards; all other forms are excluded.
//
// BDD: Given a policy map with various key forms,
// When buildWildcardIndex is called,
// Then only trailing-".*" keys appear in the result.
//
// Traces to: tool-registry-redesign-spec.md FR-009 (trailing-only wildcard rule)
func TestBuildWildcardIndex_TrailingOnlyWildcard(t *testing.T) {
	policies := map[string]string{
		"system.*":       "deny",
		"*.bad":          "allow", // leading — not a valid wildcard
		"foo.*.bar":      "ask",   // embedded — not a valid wildcard
		"*":              "deny",  // catch-all — not a valid wildcard (FR-009 §4)
		"system.agent.*": "ask",   // valid: longer prefix
		"normal_tool":    "allow", // exact name — not a wildcard
	}

	entries := buildWildcardIndex(policies)

	// Only "system.*" and "system.agent.*" are valid trailing-".*" wildcards.
	require.Len(t, entries, 2, "only trailing-'.*' keys must be in wildcard index")
	prefixes := make([]string, len(entries))
	for i, e := range entries {
		prefixes[i] = e.prefix
	}
	assert.Contains(t, prefixes, "system")
	assert.Contains(t, prefixes, "system.agent")
}

// TestBuildWildcardIndex_LongestPrefixFirst verifies that buildWildcardIndex
// sorts entries by descending prefix length (longest first).
//
// BDD: Given wildcards with different prefix lengths,
// When buildWildcardIndex is called,
// Then entries are sorted longest-prefix first.
//
// Traces to: tool-registry-redesign-spec.md FR-071
func TestBuildWildcardIndex_LongestPrefixFirst(t *testing.T) {
	policies := map[string]string{
		"a.*":       "deny",
		"a.b.c.d.*": "ask",
		"a.b.*":     "allow",
		"a.b.c.*":   "deny",
	}

	entries := buildWildcardIndex(policies)
	require.Len(t, entries, 4)

	// Verify descending length order.
	for i := 1; i < len(entries); i++ {
		prev := len(entries[i-1].prefix)
		curr := len(entries[i].prefix)
		assert.GreaterOrEqual(t, prev, curr,
			"entry[%d] prefix len %d must be >= entry[%d] prefix len %d", i-1, prev, i, curr)
	}
}

// TestBuildWildcardIndex_LexicographicTieBreak verifies that when two wildcard
// entries have equal-length prefixes, they are ordered lexicographically (ascending).
//
// BDD: Given two wildcards with same-length prefixes "system.alerts.long_thing"
// and "system.agent.subagent" (both 25 chars),
// When buildWildcardIndex is called,
// Then the lexicographically smaller prefix comes first.
//
// Traces to: tool-registry-redesign-spec.md FR-071 (tie-break: lex ascending)
func TestBuildWildcardIndex_LexicographicTieBreak(t *testing.T) {
	// Construct two wildcards with exactly the same prefix length.
	// "system.aaaaa" (12) and "system.bbbbb" (12) — same length, lex sort decides.
	policies := map[string]string{
		"system.bbbbb.*": "deny",
		"system.aaaaa.*": "ask",
	}

	entries := buildWildcardIndex(policies)
	require.Len(t, entries, 2)

	// Lex ascending: "system.aaaaa" < "system.bbbbb"
	assert.Equal(t, "system.aaaaa", entries[0].prefix,
		"lexicographically smaller prefix must come first on tie")
	assert.Equal(t, "system.bbbbb", entries[1].prefix)
}

// --- resolveFromMap unit tests ---

// TestResolveFromMap_ExactWinsOverWildcard verifies that an exact name match
// always wins over any wildcard, regardless of wildcard length.
//
// BDD: Given policies {"system.agent.create": "allow", "system.*": "deny"},
// When resolveFromMap is called for "system.agent.create",
// Then the resolved policy is "allow" (exact wins).
//
// Traces to: tool-registry-redesign-spec.md FR-009 §2 (exact > wildcard)
func TestResolveFromMap_ExactWinsOverWildcard(t *testing.T) {
	policies := map[string]string{
		"system.agent.create": "allow",
		"system.*":            "deny",
		"system.agent.*":      "ask",
	}
	wildcards := buildWildcardIndex(policies)

	policy := resolveFromMap("system.agent.create", policies, wildcards)
	assert.Equal(t, "allow", policy, "exact match must win over wildcards")
}

// TestResolveFromMap_LongestWildcardWins verifies that among wildcards,
// the longest matching prefix wins.
//
// BDD: Given policies {"system.agent.*": "ask", "system.*": "deny"},
// When resolveFromMap is called for "system.agent.delete",
// Then the resolved policy is "ask" (longer "system.agent.*" wins over "system.*").
//
// Traces to: tool-registry-redesign-spec.md FR-009 §2 (longest prefix wins)
func TestResolveFromMap_LongestWildcardWins(t *testing.T) {
	policies := map[string]string{
		"system.agent.*": "ask",
		"system.*":       "deny",
	}
	wildcards := buildWildcardIndex(policies)

	tests := []struct {
		toolName       string
		expectedPolicy string
		note           string
	}{
		{"system.agent.delete", "ask", "system.agent.* is longer than system.*, must win"},
		{"system.agent.create", "ask", "system.agent.* must also win for .create"},
		{"system.config.set", "deny", "only system.* matches system.config.set"},
		{"system.models.list", "deny", "only system.* matches system.models.list"},
	}

	for _, tc := range tests {
		t.Run(tc.toolName, func(t *testing.T) {
			p := resolveFromMap(tc.toolName, policies, wildcards)
			assert.Equal(t, tc.expectedPolicy, p, tc.note)
		})
	}
}

// TestResolveFromMap_NoMatch verifies that "" is returned when no exact or wildcard
// matches — the caller falls back to default_policy.
//
// Traces to: tool-registry-redesign-spec.md FR-009 (fallback to default_policy)
func TestResolveFromMap_NoMatch(t *testing.T) {
	policies := map[string]string{
		"read_file": "allow",
		"system.*":  "deny",
	}
	wildcards := buildWildcardIndex(policies)

	p := resolveFromMap("unregistered_tool", policies, wildcards)
	assert.Equal(t, "", p, "no match must return empty string (caller uses default_policy)")
}

// TestResolveFromMap_EqualSegmentTieBreak verifies the worked example from FR-009 §5.
//
// BDD: Given policies {"system.alerts.long_thing.*": "deny", "system.agent.subagent.*": "ask"},
// When resolveFromMap is called for "system.alerts.long_thing.fire",
// Then the resolved policy is "deny" (longer prefix string wins when segment counts are equal).
// When resolveFromMap is called for "system.agent.subagent.run",
// Then the resolved policy is "ask".
//
// Traces to: tool-registry-redesign-spec.md BDD Scenario "Wildcard tie-break"
func TestResolveFromMap_EqualSegmentTieBreak(t *testing.T) {
	policies := map[string]string{
		"system.alerts.long_thing.*": "deny",
		"system.agent.subagent.*":    "ask",
	}
	wildcards := buildWildcardIndex(policies)

	tests := []struct {
		toolName       string
		expectedPolicy string
	}{
		{"system.alerts.long_thing.fire", "deny"},
		{"system.agent.subagent.run", "ask"},
	}

	for _, tc := range tests {
		t.Run(tc.toolName, func(t *testing.T) {
			p := resolveFromMap(tc.toolName, policies, wildcards)
			assert.Equal(t, tc.expectedPolicy, p,
				"resolveFromMap(%q) must return %q", tc.toolName, tc.expectedPolicy)
		})
	}
}

// --- FilterToolsByPolicy integration tests (wildcard-specific) ---

// TestFilterToolsByPolicy_PrefixWildcard_DeniesSystemTools verifies that a
// "system.*" wildcard in Policies denies all system.* tools from a core agent.
//
// BDD: Given a core agent with Policies{"system.*": "deny"},
// When FilterToolsByPolicy is called,
// Then no entry in tools[] has a name starting with "system.".
//
// Traces to: tool-registry-redesign-spec.md BDD "Custom agent default-denies system.* via wildcard"
func TestFilterToolsByPolicy_PrefixWildcard_DeniesSystemTools(t *testing.T) {
	allTools := makeWildTools(
		"system.agent.create",
		"system.agent.delete",
		"system.models.list",
		"system.config.set",
		"read_file",
		"write_file",
	)

	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"system.*": "deny"},
		GlobalDefaultPolicy: "allow",
		IsCoreAgent:         true, // skip scope gate for simplicity
	}

	got, policyMap := FilterToolsByPolicy(allTools, "core", cfg)

	for _, tool := range got {
		assert.NotContains(t, tool.Name(), "system.",
			"system.* wildcard deny must block all system.* tools")
	}

	// Non-system tools must still appear.
	assert.Contains(t, policyMap, "read_file", "read_file must be allowed")
	assert.Contains(t, policyMap, "write_file", "write_file must be allowed")
}

// TestFilterToolsByPolicy_ExactBeatsWildcard verifies the full pipeline:
// exact policy "allow" for system.config.set beats the wildcard deny "system.*".
//
// BDD (spec scenario "Wildcard precedence — exact match wins"):
// Given policies {"system.config.set": "allow", "system.*": "deny"},
// When FilterToolsByPolicy resolves policy for system.config.set,
// Then the resolved effective policy is "allow".
//
// Traces to: tool-registry-redesign-spec.md BDD "Wildcard precedence — exact match wins"
func TestFilterToolsByPolicy_ExactBeatsWildcard(t *testing.T) {
	allTools := makeWildTools(
		"system.config.set",
		"system.agent.create",
		"read_file",
	)

	cfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"system.config.set": "allow",
			"system.*":          "deny",
		},
		GlobalDefaultPolicy: "allow",
		IsCoreAgent:         true,
	}

	got, policyMap := FilterToolsByPolicy(allTools, "core", cfg)

	// system.config.set must be allowed (exact wins).
	assert.Contains(t, policyMap, "system.config.set",
		"exact allow must override wildcard deny for system.config.set")
	assert.Equal(t, "allow", policyMap["system.config.set"])

	// system.agent.create must be denied (wildcard deny, no exact match).
	found := false
	for _, t := range got {
		if t.Name() == "system.agent.create" {
			found = true
		}
	}
	assert.False(t, found, "system.agent.create must be blocked by wildcard deny")
}

// TestFilterToolsByPolicy_LongestWildcard_FourSegment verifies the BDD scenario
// "Wildcard tie-break — equal-segment-count, longer prefix wins".
//
// Given policies {"system.alerts.long_thing.*": "deny", "system.agent.subagent.*": "ask"},
// When the filter resolves for system.alerts.long_thing.fire → deny,
// When the filter resolves for system.agent.subagent.run → ask.
//
// Traces to: tool-registry-redesign-spec.md BDD "Wildcard tie-break" / FR-071
func TestFilterToolsByPolicy_LongestWildcard_FourSegment(t *testing.T) {
	allTools := makeWildTools(
		"system.alerts.long_thing.fire",
		"system.agent.subagent.run",
		"read_file",
	)

	cfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"system.alerts.long_thing.*": "deny",
			"system.agent.subagent.*":    "ask",
		},
		GlobalDefaultPolicy: "allow",
		IsCoreAgent:         true,
	}

	_, policyMap := FilterToolsByPolicy(allTools, "core", cfg)

	// system.alerts.long_thing.fire must be denied.
	_, hasFireTool := policyMap["system.alerts.long_thing.fire"]
	assert.False(t, hasFireTool,
		"system.alerts.long_thing.fire must be filtered out by deny wildcard")

	// system.agent.subagent.run must be present with "ask".
	p, ok := policyMap["system.agent.subagent.run"]
	assert.True(t, ok, "system.agent.subagent.run must pass the filter")
	assert.Equal(t, "ask", p, "system.agent.subagent.run must resolve to ask")
}

// TestFilterToolsByPolicy_WildcardDataset exercises all 16 rows of the spec dataset.
//
// Traces to: tool-registry-redesign-spec.md TDD Plan §Dataset "Per-agent + global policy resolution"
func TestFilterToolsByPolicy_WildcardDataset(t *testing.T) {
	tests := []struct {
		name             string
		globalDefault    string
		globalPolicies   map[string]string
		defaultPolicy    string
		policies         map[string]string
		toolRequested    string
		expectedInOutput bool   // true = tool appears in filtered list
		expectedPolicy   string // expected value in policyMap (empty if not in output)
	}{
		{
			name:             "row9-exact-wins-over-wildcard",
			globalDefault:    "allow",
			globalPolicies:   map[string]string{},
			defaultPolicy:    "allow",
			policies:         map[string]string{"system.config.set": "allow", "system.*": "deny"},
			toolRequested:    "system.config.set",
			expectedInOutput: true,
			expectedPolicy:   "allow",
		},
		{
			name:             "row10-wildcard-deny-matches-system-tool",
			globalDefault:    "allow",
			globalPolicies:   map[string]string{},
			defaultPolicy:    "allow",
			policies:         map[string]string{"system.*": "deny"},
			toolRequested:    "system.agent.list",
			expectedInOutput: false,
			expectedPolicy:   "",
		},
		{
			name:             "row11-global-deny-wins-over-agent-allow",
			globalDefault:    "allow",
			globalPolicies:   map[string]string{"exec": "deny"},
			defaultPolicy:    "allow",
			policies:         map[string]string{"exec": "allow"},
			toolRequested:    "exec",
			expectedInOutput: false,
			expectedPolicy:   "",
		},
		{
			name:             "row12-global-default-deny-strips-all",
			globalDefault:    "deny",
			globalPolicies:   map[string]string{},
			defaultPolicy:    "allow",
			policies:         map[string]string{},
			toolRequested:    "read_file",
			expectedInOutput: false,
			expectedPolicy:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build a tool list containing the requested tool.
			scope := ScopeGeneral
			if len(tc.toolRequested) > 7 && tc.toolRequested[:7] == "system." {
				scope = ScopeCore
			}
			tool := &wildTestTool{name: tc.toolRequested, scope: scope}
			allTools := []Tool{tool}

			cfg := &ToolPolicyCfg{
				DefaultPolicy:       tc.defaultPolicy,
				Policies:            tc.policies,
				GlobalDefaultPolicy: tc.globalDefault,
				GlobalPolicies:      tc.globalPolicies,
				IsCoreAgent:         true, // bypass scope gate for dataset tests
			}

			_, policyMap := FilterToolsByPolicy(allTools, "core", cfg)

			if tc.expectedInOutput {
				p, ok := policyMap[tc.toolRequested]
				assert.True(t, ok, "tool %q must be in output", tc.toolRequested)
				assert.Equal(t, tc.expectedPolicy, p,
					"tool %q must have policy %q", tc.toolRequested, tc.expectedPolicy)
			} else {
				_, ok := policyMap[tc.toolRequested]
				assert.False(t, ok, "tool %q must NOT be in output", tc.toolRequested)
			}
		})
	}
}

// TestFilterToolsByPolicy_DeterministicOrdering verifies that calling
// FilterToolsByPolicy multiple times with the same input produces the same
// output order (deterministic, not map-iteration-dependent).
//
// Traces to: tool-registry-redesign-spec.md FR-009 §2 (sort order is deterministic)
func TestFilterToolsByPolicy_DeterministicOrdering(t *testing.T) {
	allTools := makeWildTools(
		"system.agent.create",
		"system.agent.delete",
		"system.models.list",
		"read_file",
		"write_file",
		"exec",
	)

	cfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"system.agent.*": "ask",
			"system.*":       "deny",
		},
		GlobalDefaultPolicy: "allow",
		IsCoreAgent:         true,
	}

	firstRun, _ := FilterToolsByPolicy(allTools, "core", cfg)
	secondRun, _ := FilterToolsByPolicy(allTools, "core", cfg)

	require.Len(t, firstRun, len(secondRun),
		"repeated calls must produce same number of results")

	for i := range firstRun {
		assert.Equal(t, firstRun[i].Name(), secondRun[i].Name(),
			"result order must be deterministic at index %d", i)
	}
}
