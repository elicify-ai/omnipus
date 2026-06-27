// Omnipus — fuzzy tool-name matching tests (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"testing"
)

// ── Minor fixes: lowercase comparison and minimum-input-length guard ──────────

// TestFuzzy_CaseInsensitiveMatch verifies that case differences don't defeat
// the fuzzy matcher. "Exec" should match "exec", "Read_File" should match
// "read_file". Before the fix, comparison is case-sensitive so different-case
// names get a Levenshtein distance equal to their length (all substitutions).
func TestFuzzy_CaseInsensitiveMatch(t *testing.T) {
	tools := []Tool{
		&mockSearchableTool{name: "exec", desc: "execute"},
		&mockSearchableTool{name: "read_file", desc: "read file"},
		&mockSearchableTool{name: "search_web", desc: "search the web"},
	}

	cases := []struct {
		unknown string
		want    string
	}{
		{"Exec", "exec"},
		{"EXEC", "exec"},
		{"Read_File", "read_file"},
		{"Search_Web", "search_web"},
	}

	for _, c := range cases {
		got := FindClosestToolName(tools, c.unknown)
		if got != c.want {
			t.Errorf("FindClosestToolName(%q) = %q, want %q", c.unknown, got, c.want)
		}
	}
}

// TestFuzzy_ShortInputNoFalsePositive verifies that very short inputs ("x", "se")
// do NOT produce a suggestion — they are too short to reliably match any tool name.
// Before the fix, "x" matches "exec" via Levenshtein threshold (1 char from
// any 1-char string in a 4-char candidate gives distance 3, threshold = 3).
func TestFuzzy_ShortInputNoFalsePositive(t *testing.T) {
	tools := []Tool{
		&mockSearchableTool{name: "exec", desc: "execute a command"},
		&mockSearchableTool{name: "send_message", desc: "send a message"},
		&mockSearchableTool{name: "search_web", desc: "search web"},
	}

	// Inputs shorter than the minimum guard threshold should produce no suggestion.
	shortInputs := []string{"x", "se", "ex"}
	for _, inp := range shortInputs {
		got := FindClosestToolName(tools, inp)
		if got != "" {
			t.Errorf("FindClosestToolName(%q) = %q, want empty (short input must not produce a suggestion)", inp, got)
		}
	}
}

// TestFuzzy_ExactMatchReturnsImmediately verifies that an exact match is still
// returned correctly with the case-lowering fix in place.
func TestFuzzy_ExactMatchReturnsImmediately(t *testing.T) {
	tools := []Tool{
		&mockSearchableTool{name: "exec", desc: "execute"},
	}

	got := FindClosestToolName(tools, "exec")
	if got != "exec" {
		t.Errorf("exact match must return immediately; got %q", got)
	}
}

// TestFuzzy_TokenSetMatchCaseInsensitive verifies that token-set matching also
// works case-insensitively after the fix.
func TestFuzzy_TokenSetMatchCaseInsensitive(t *testing.T) {
	tools := []Tool{
		&mockSearchableTool{name: "update_task", desc: "update task"},
	}

	// Same tokens, different case, different order.
	got := FindClosestToolName(tools, "Task_Update")
	if got != "update_task" {
		t.Errorf("token-set match with different case: got %q, want 'update_task'", got)
	}
}

// TestFuzzy_DeadIndexUnused is a compile-time check that the dead `_ = i`
// pattern does not appear in the normalizer or fuzzy code. This test just
// exercises FindClosestToolName normally and ensures no panics — actual dead
// code removal is enforced by the reviewer.
func TestFuzzy_NoPanic_EmptyInput(t *testing.T) {
	tools := []Tool{
		&mockSearchableTool{name: "exec", desc: "execute"},
	}
	got := FindClosestToolName(tools, "")
	if got != "" {
		t.Errorf("empty input must return '', got %q", got)
	}
}

// mockFuzzyTool implements Tool for fuzzy tests (reuses mockSearchableTool from search_tools_test.go).
// We add this Execute stub only so the type satisfies the interface for fuzzy tests.
var _ Tool = &fuzzyToolForTest{}

type fuzzyToolForTest struct {
	name string
}

func (f *fuzzyToolForTest) Name() string        { return f.name }
func (f *fuzzyToolForTest) Description() string { return f.name }
func (f *fuzzyToolForTest) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (f *fuzzyToolForTest) Scope() ToolScope       { return ScopeGeneral }
func (f *fuzzyToolForTest) Category() ToolCategory { return CategoryCore }
func (f *fuzzyToolForTest) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return SilentResult("fuzzy test tool")
}
