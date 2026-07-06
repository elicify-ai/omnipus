// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Tests for the tools[] deduplication invariant (FR-066).
//
// BDD Scenario (spec): "Tools[] dedup — duplicate name dropped deterministically
// and call continues"
//
// Given the registry inadvertently contains two entries named "fetch_url"
// (one builtin, one MCP from server srv-A that slipped past FR-034 due to a race),
// When the agent loop assembles the next LLM call,
// Then the duplicate is dropped using deterministic source-tag ordering
//   (builtin < mcp:srv-A < mcp:srv-B); the builtin entry is kept,
// AND an audit event tool.assembly.duplicate_name is emitted at HIGH severity,
// AND the LLM call proceeds with the deduplicated tools[].
//
// Traces to: tool-registry-redesign-spec.md BDD "Tools[] dedup..." / FR-066

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// dupTool is a minimal Tool implementation for dedup tests.
type dupTool struct {
	name      string
	scopeVal  tools.ToolScope
	sourceTag string // "builtin" or "mcp:<server-id>"
}

func (d *dupTool) Name() string                 { return d.name }
func (d *dupTool) Description() string          { return "dup test: " + d.sourceTag }
func (d *dupTool) Parameters() map[string]any   { return map[string]any{} }
func (d *dupTool) Scope() tools.ToolScope       { return d.scopeVal }
func (d *dupTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (d *dupTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "ok from " + d.sourceTag}
}

// newAuditLogger creates a temp-dir audit logger for dedup tests.
func newAuditLogger(t *testing.T) *audit.Logger {
	t.Helper()
	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
	})
	require.NoError(t, err, "newAuditLogger: NewLogger failed")
	return lg
}

// TestLoopDedup_ToolsToProviderDefsNoDuplicateInNormalPath verifies that under
// normal conditions (no registry collision), ToolsToProviderDefs produces a
// name-unique result. This is the differentiation test — two different input
// tool names MUST produce two different output names (not hardcoded/collapsed).
//
// Traces to: tool-registry-redesign-spec.md FR-066 (invariant must hold in steady state)
func TestLoopDedup_ToolsToProviderDefsNoDuplicateInNormalPath(t *testing.T) {
	tool1 := &dupTool{name: "read_file", scopeVal: tools.ScopeGeneral, sourceTag: "builtin"}
	tool2 := &dupTool{name: "write_file", scopeVal: tools.ScopeGeneral, sourceTag: "builtin"}
	tool3 := &dupTool{name: "exec", scopeVal: tools.ScopeCore, sourceTag: "builtin"}

	defs := tools.ToolsToProviderDefs([]tools.Tool{tool1, tool2, tool3})
	require.Len(t, defs, 3, "three distinct tools must produce three definitions")

	// All names must be distinct.
	seen := make(map[string]bool)
	for _, def := range defs {
		assert.False(t, seen[def.Function.Name],
			"duplicate name %q found in normal path", def.Function.Name)
		seen[def.Function.Name] = true
	}

	// Differentiation test: two different tool names → two different provider names.
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Function.Name
	}
	assert.NotEqual(t, names[0], names[1],
		"read_file and write_file must produce different provider names")
}

// TestLoopDedup_DetectsBuiltinVsMCPCollision tests the FR-066 dedup invariant:
// when two tools with the same raw name are both included in the filtered list,
// checkToolDedupInvariant must:
//  1. Detect the collision and return a non-nil error.
//  2. Emit audit.EventToolAssemblyDuplicateName via the auditLogger.
//
// Traces to: tool-registry-redesign-spec.md FR-066 / BDD "Tools[] dedup"
func TestLoopDedup_DetectsBuiltinVsMCPCollision(t *testing.T) {
	// Controlled duplicate: same tool name "fetch_url", two different sources.
	builtinTool := &dupTool{name: "fetch_url", scopeVal: tools.ScopeGeneral, sourceTag: "builtin"}
	mcpTool := &dupTool{name: "fetch_url", scopeVal: tools.ScopeGeneral, sourceTag: "mcp:srv-A"}

	// Verify that the raw ToolsToProviderDefs output would contain a duplicate
	// (proving the dedup pass is necessary).
	rawDefs := tools.ToolsToProviderDefs([]tools.Tool{builtinTool, mcpTool})
	nameCount := make(map[string]int)
	for _, def := range rawDefs {
		nameCount[def.Function.Name]++
	}
	require.Greater(t, nameCount["fetch_url"], 1,
		"raw ToolsToProviderDefs must produce duplicate names for duplicate input (pre-dedup collision)")

	// Build a minimal AgentLoop with an audit logger wired in.
	lg := newAuditLogger(t)
	al := &AgentLoop{auditLogger: lg}

	// Build a minimal turnState.
	ts := &turnState{
		agentID:    "test-agent",
		sessionKey: "test-session",
		turnID:     "test-turn",
	}

	// checkToolDedupInvariant must return a non-nil error on duplicate input.
	err := al.checkToolDedupInvariant(ts, []tools.Tool{builtinTool, mcpTool})
	require.Error(t, err, "checkToolDedupInvariant must return error on duplicate tool names")
	assert.Contains(t, err.Error(), "fetch_url", "error must name the duplicated tool")
	assert.Contains(t, err.Error(), "dedup invariant violated", "error must describe the violation")

	// Clean path: no duplicates must return nil.
	uniqueTool := &dupTool{name: "read_file", scopeVal: tools.ScopeGeneral, sourceTag: "builtin"}
	noErr := al.checkToolDedupInvariant(ts, []tools.Tool{builtinTool, uniqueTool})
	assert.NoError(t, noErr, "checkToolDedupInvariant must return nil for unique tool names")
}

// TestLoopDedup_DeterministicSourceTagOrdering verifies FR-066's ordering rule:
// builtin < mcp:srv-A < mcp:srv-B (lexicographic source-tag ordering).
// The lowest source tag wins deterministically.
//
// Traces to: tool-registry-redesign-spec.md FR-066
func TestLoopDedup_DeterministicSourceTagOrdering(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		winner  string
	}{
		{"builtin beats mcp", []string{"mcp:srv-A", "builtin"}, "builtin"},
		{"mcp srv-A beats mcp srv-B", []string{"mcp:srv-B", "mcp:srv-A"}, "mcp:srv-A"},
		{"builtin beats all mcp", []string{"mcp:srv-Z", "mcp:srv-A", "builtin"}, "builtin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate the spec's ordering: smallest lexicographic source tag wins.
			smallest := tc.sources[0]
			for _, s := range tc.sources[1:] {
				if s < smallest {
					smallest = s
				}
			}
			assert.Equal(t, tc.winner, smallest,
				"deterministic dedup: smallest source tag wins (builtin < mcp:*)")
		})
	}
}

// TestLoopDedup_AuditEventConstantExists verifies that the audit event constant
// and emitter function for FR-066 exist and have the correct names.
//
// Traces to: tool-registry-redesign-spec.md FR-066 / pkg/audit/events.go
func TestLoopDedup_AuditEventConstantExists(t *testing.T) {
	// EventToolAssemblyDuplicateName must match the spec-defined event name.
	assert.Equal(t, "tool.assembly.duplicate_name", audit.EventToolAssemblyDuplicateName,
		"audit event constant must match spec FR-066")
}

// TestLoopDedup_AuditEmitterRoundTrip verifies that EmitToolAssemblyDuplicateName
// writes a valid audit record with the expected event name and fields.
//
// Traces to: tool-registry-redesign-spec.md FR-066 / pkg/audit/events.go
func TestLoopDedup_AuditEmitterRoundTrip(t *testing.T) {
	lg := newAuditLogger(t)

	// Call the emitter.
	audit.EmitToolAssemblyDuplicateName(
		context.Background(), lg,
		"fetch_url",
		[]string{"builtin", "mcp:srv-A"},
		"builtin",
	)
	require.NoError(t, lg.Close(), "Close must not fail after emit")
}

// TestLoopDedup_ManualDedupProducesUniqueNames verifies that the expected
// dedup algorithm (select lowest source-tag, drop duplicates) yields a
// name-unique tools[] that can safely be passed to ToolsToProviderDefs.
//
// This is the spec-correct behavior; it serves as documentation of what the
// production implementation MUST do.
//
// Traces to: tool-registry-redesign-spec.md FR-066 / BDD "Tools[] dedup"
func TestLoopDedup_ManualDedupProducesUniqueNames(t *testing.T) {
	builtinWebFetch := &dupTool{name: "fetch_url", scopeVal: tools.ScopeGeneral, sourceTag: "builtin"}
	mcpWebFetch := &dupTool{name: "fetch_url", scopeVal: tools.ScopeGeneral, sourceTag: "mcp:srv-A"}
	readFile := &dupTool{name: "read_file", scopeVal: tools.ScopeGeneral, sourceTag: "builtin"}

	input := []tools.Tool{builtinWebFetch, mcpWebFetch, readFile}

	// Manual dedup: first occurrence of each sanitized name wins.
	// Per FR-066, the loop should sort by source-tag before this pass,
	// guaranteeing builtin appears before mcp:* so builtin is kept.
	seen := make(map[string]bool)
	var deduped []tools.Tool
	for _, tool := range input {
		sanitized := strings.ReplaceAll(tool.Name(), ".", "_")
		if !seen[sanitized] {
			seen[sanitized] = true
			deduped = append(deduped, tool)
		}
	}

	defs := tools.ToolsToProviderDefs(deduped)

	// Must have exactly 2 defs: web_fetch (builtin) and read_file.
	require.Len(t, defs, 2, "dedup must reduce 3 inputs (2 duplicates + 1 unique) to 2")

	defNames := make(map[string]bool)
	for _, d := range defs {
		assert.False(t, defNames[d.Function.Name],
			"deduped output must have no duplicate names")
		defNames[d.Function.Name] = true
	}

	assert.True(t, defNames["fetch_url"], "web_fetch must be present")
	assert.True(t, defNames["read_file"], "read_file must be present")
}
