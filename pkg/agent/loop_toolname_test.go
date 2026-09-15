// loop_toolname_test.go: tests for suggest the nearest known tool name for an unknown one

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- moved from loop.go tests 2026-09-15 ---

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
