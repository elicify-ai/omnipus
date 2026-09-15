// Omnipus — UAT 2026-09-13 D-96: the "unknown tool (did you mean …)" hint
// must point at a tool that can do what the caller asked for.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// suggestFakeTool is the smallest Tool that carries a name and a parameter
// schema — the two inputs the suggester ranks on.
type suggestFakeTool struct {
	tools.BaseTool
	name   string
	params map[string]any
}

func (f suggestFakeTool) Name() string               { return f.name }
func (f suggestFakeTool) Description() string        { return "fake " + f.name }
func (f suggestFakeTool) Parameters() map[string]any { return f.params }
func (f suggestFakeTool) Scope() tools.ToolScope     { return tools.ScopeGeneral }
func (f suggestFakeTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.NewToolResult("")
}

func opEnumParams(ops ...string) map[string]any {
	enum := make([]string, 0, len(ops))
	enum = append(enum, ops...)
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"op":   map[string]any{"type": "string", "enum": enum},
			"path": map[string]any{"type": "string"},
		},
	}
}

func knowledgeFamily() []tools.Tool {
	return []tools.Tool{
		suggestFakeTool{name: "knowledge_read", params: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
		suggestFakeTool{name: "knowledge_find", params: map[string]any{"type": "object"}},
		suggestFakeTool{name: "knowledge_edit", params: opEnumParams("create", "set_property", "append_section", "link", "embed", "relation", "replace_body")},
		suggestFakeTool{name: "knowledge_restructure", params: opEnumParams("rename", "move", "trash", "restore")},
		suggestFakeTool{name: "knowledge_configure", params: opEnumParams("create_record_type", "edit_record_type", "delete_record_type", "create_view", "write_view")},
		suggestFakeTool{name: "knowledge_describe", params: map[string]any{"type": "object"}},
		suggestFakeTool{name: "update_task", params: map[string]any{"type": "object"}},
		suggestFakeTool{name: "create_task", params: map[string]any{"type": "object"}},
		suggestFakeTool{name: "search_web", params: map[string]any{"type": "object"}},
	}
}

// TestSuggestUnknownToolName_ObservedCase is the D-96 transcript: the agent
// asked for `knowledge_create` and was told "did you mean 'knowledge_read'?"
// — the one knowledge tool that cannot create anything. The right answer is
// knowledge_edit, whose `op` enum carries "create".
func TestSuggestUnknownToolName_ObservedCase(t *testing.T) {
	name, hint := suggestUnknownToolName(knowledgeFamily(), "knowledge_create")
	assert.Equal(t, "knowledge_edit", name)
	assert.Equal(t, `op "create"`, hint, "the hint must name the op that does what the caller asked")
}

// TestSuggestUnknownToolName_SegmentMatchBeatsRawEditDistance — the family
// prefix plus the trailing verb decide, not the character count alone.
func TestSuggestUnknownToolName_SegmentMatchBeatsRawEditDistance(t *testing.T) {
	for _, tc := range []struct{ unknown, want, hint string }{
		{"knowledge_move", "knowledge_restructure", `op "move"`},
		{"knowledge_rename", "knowledge_restructure", `op "rename"`},
		{"knowledge_create_view", "knowledge_configure", `op "create_view"`},
		{"knowledge_set_property", "knowledge_edit", `op "set_property"`},
		{"knowledge_search", "knowledge_find", ""},
		{"knowledge_reed", "knowledge_read", ""},
		{"task_update", "update_task", ""},
		{"knowlege_edit", "knowledge_edit", ""},
		{"knowledge_trash", "knowledge_restructure", `op "trash"`},
	} {
		name, hint := suggestUnknownToolName(knowledgeFamily(), tc.unknown)
		assert.Equal(t, tc.want, name, "unknown %q", tc.unknown)
		assert.Equal(t, tc.hint, hint, "unknown %q", tc.unknown)
	}
}

// TestSuggestUnknownToolName_NoSignalNoSuggestion — a name that shares
// nothing with any registered tool gets no guess at all: a wrong hint costs
// the caller a round trip.
func TestSuggestUnknownToolName_NoSignalNoSuggestion(t *testing.T) {
	name, hint := suggestUnknownToolName(knowledgeFamily(), "zzzz_qqqq")
	assert.Empty(t, name)
	assert.Empty(t, hint)
	name, _ = suggestUnknownToolName(nil, "knowledge_create")
	assert.Empty(t, name)
	name, _ = suggestUnknownToolName(knowledgeFamily(), "")
	assert.Empty(t, name)
}

// TestLoadTool_UnknownKnowledgeCreateSuggestsKnowledgeEdit runs the observed
// case through the real ToolSearch door for a core agent whose policy allows
// knowledge_edit, asserting the rendered hint.
func TestLoadTool_UnknownKnowledgeCreateSuggestsKnowledgeEdit(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	tt := loadToolFor(t, al, "mia")
	ctx := execCtx("mia", "sess-d96-unknown")
	result := tt.Execute(ctx, map[string]any{"names": []any{"knowledge_create"}})

	require.True(t, result.IsError, "knowledge_create is not a tool and must be rejected")
	assert.Contains(t, result.ForLLM, "did you mean 'knowledge_edit'", result.ForLLM)
	assert.Contains(t, result.ForLLM, `op "create"`, result.ForLLM)
	assert.False(t, strings.Contains(result.ForLLM, "knowledge_read"),
		"the one knowledge tool that cannot create must not be the suggestion: %s", result.ForLLM)
}
