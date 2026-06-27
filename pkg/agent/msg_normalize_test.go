// Omnipus — normalizeMessagesForProvider tests (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Reproduction tests for F1, F2, F3 findings. Written BEFORE the fix so they
// fail on the current broken implementation and prove the fix works.
//
// F1: 3 parallel tool results → only 1 survives (Rule 2 inspects out's last
//     element instead of checking open tool_call IDs).
// F2: two assistant messages each carrying ToolCalls are merged, orphaning
//     the 2nd set of ToolCalls.
// F3: appendOrMerge drops ReasoningContent and SystemParts when merging.

package agent

import (
	"reflect"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/providers/protocoltypes"
)

// normMsg is a local helper to build a providers.Message with role+content only.
// Named distinctly from the existing msg() helper in context_test.go which also
// takes role+content — we need our own to avoid redeclaration.
func normMsg(role, content string) providers.Message {
	return providers.Message{Role: role, Content: content}
}

// normTC builds a ToolCall for normalizer tests.
func normTC(id, name string) providers.ToolCall {
	return providers.ToolCall{ID: id, Function: &protocoltypes.FunctionCall{Name: name, Arguments: "{}"}}
}

// normCB builds a ContentBlock for normalizer tests.
func normCB(text string) protocoltypes.ContentBlock {
	return protocoltypes.ContentBlock{Type: "text", Text: text}
}

// ── F1 reproduction: 3 parallel tool results all survive ────────────────────

// TestNormalize_F1_ThreeParallelToolResultsSurvive verifies that when an
// assistant message carries N ToolCalls followed by N role="tool" result
// messages, ALL N results survive normalization with their ToolCallIDs intact.
// On the old code only 1 survives because hasPrecedingAssistantWithToolCalls
// checks out's LAST element — after the first tool result is appended it is no
// longer an "assistant" message, so every subsequent tool result is dropped.
func TestNormalize_F1_ThreeParallelToolResultsSurvive(t *testing.T) {
	assistant := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			normTC("id1", "tool_a"),
			normTC("id2", "tool_b"),
			normTC("id3", "tool_c"),
		},
	}
	toolResult := func(id string) providers.Message {
		return providers.Message{Role: "tool", ToolCallID: id, Content: "result for " + id}
	}

	input := []providers.Message{
		assistant,
		toolResult("id1"),
		toolResult("id2"),
		toolResult("id3"),
	}

	got := normalizeMessagesForProvider(input)

	// We expect all 4 messages (1 assistant + 3 tool results).
	if len(got) != 4 {
		t.Fatalf("F1: expected 4 messages out, got %d: %+v", len(got), got)
	}
	// Verify each tool result is present with its original ToolCallID.
	toolIDs := map[string]bool{}
	for _, m := range got {
		if m.Role == "tool" {
			toolIDs[m.ToolCallID] = true
		}
	}
	for _, wantID := range []string{"id1", "id2", "id3"} {
		if !toolIDs[wantID] {
			t.Errorf("F1: tool result with ToolCallID=%q was dropped", wantID)
		}
	}
}

// ── F2 reproduction: two assistants each with ToolCalls are NOT merged ───────

// TestNormalize_F2_TwoAssistantsWithToolCallsNotMerged verifies that two
// consecutive assistant messages that each carry ToolCalls pass through
// unchanged (not merged). Merging them would orphan the 2nd set of ToolCalls.
func TestNormalize_F2_TwoAssistantsWithToolCallsNotMerged(t *testing.T) {
	a1 := providers.Message{
		Role:    "assistant",
		Content: "first assistant",
		ToolCalls: []providers.ToolCall{
			normTC("id_a1", "tool_x"),
		},
	}
	a2 := providers.Message{
		Role:    "assistant",
		Content: "second assistant",
		ToolCalls: []providers.ToolCall{
			normTC("id_a2", "tool_y"),
		},
	}

	input := []providers.Message{a1, a2}
	got := normalizeMessagesForProvider(input)

	// Both messages must survive independently.
	if len(got) != 2 {
		t.Fatalf("F2: expected 2 messages, got %d: %+v", len(got), got)
	}
	// The second assistant's ToolCalls must not be lost.
	if len(got[1].ToolCalls) == 0 {
		t.Error("F2: second assistant's ToolCalls were lost (merged into first)")
	}
	if got[1].ToolCalls[0].ID != "id_a2" {
		t.Errorf("F2: expected second assistant ToolCall ID=id_a2, got %q", got[1].ToolCalls[0].ID)
	}
}

// ── F3 reproduction: merge preserves ReasoningContent and SystemParts ────────

// TestNormalize_F3_MergePreservesReasoningContent verifies that when two
// plain-text assistant messages are merged, the ReasoningContent of the first
// message is preserved in the merged output.
func TestNormalize_F3_MergePreservesReasoningContent(t *testing.T) {
	a1 := providers.Message{
		Role:             "assistant",
		Content:          "first",
		ReasoningContent: "reasoning from first",
	}
	a2 := providers.Message{
		Role:    "assistant",
		Content: "second",
	}

	input := []providers.Message{a1, a2}
	got := normalizeMessagesForProvider(input)

	// The two plain-text assistants SHOULD merge (the actual fix we want).
	if len(got) != 1 {
		t.Fatalf("F3/RC: expected 1 merged message, got %d", len(got))
	}
	if got[0].ReasoningContent != "reasoning from first" {
		t.Errorf("F3: ReasoningContent lost after merge; got %q", got[0].ReasoningContent)
	}
}

// TestNormalize_F3_MergePreservesSystemParts verifies that when two plain-text
// assistant messages are merged, SystemParts from the first is preserved.
func TestNormalize_F3_MergePreservesSystemParts(t *testing.T) {
	a1 := providers.Message{
		Role:        "assistant",
		Content:     "first",
		SystemParts: []protocoltypes.ContentBlock{normCB("part-A")},
	}
	a2 := providers.Message{
		Role:    "assistant",
		Content: "second",
	}

	input := []providers.Message{a1, a2}
	got := normalizeMessagesForProvider(input)

	if len(got) != 1 {
		t.Fatalf("F3/SP: expected 1 merged message, got %d", len(got))
	}
	if len(got[0].SystemParts) == 0 || got[0].SystemParts[0].Text != "part-A" {
		t.Errorf("F3: SystemParts lost after merge; got %v", got[0].SystemParts)
	}
}

// ── Real fix: two adjacent plain-text assistants DO merge ────────────────────

// TestNormalize_TwoPlainTextAssistantsMerge is the actual real-world fix for the
// Z.AI 1214 trigger: two consecutive plain-text assistant messages must merge
// into one with Content joined by "\n".
func TestNormalize_TwoPlainTextAssistantsMerge(t *testing.T) {
	a1 := normMsg("assistant", "hello")
	a2 := normMsg("assistant", "world")

	got := normalizeMessagesForProvider([]providers.Message{a1, a2})

	if len(got) != 1 {
		t.Fatalf("merge: expected 1 merged message, got %d", len(got))
	}
	if got[0].Content != "hello\nworld" {
		t.Errorf("merge: expected 'hello\\nworld', got %q", got[0].Content)
	}
}

// ── Valid alternating history passes through unchanged ────────────────────────

// TestNormalize_ValidHistoryPassThrough verifies that a normal alternating
// user/assistant/tool history is returned unchanged (fast path: no allocation).
func TestNormalize_ValidHistoryPassThrough(t *testing.T) {
	assistant := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			normTC("id1", "tool_a"),
		},
	}
	input := []providers.Message{
		normMsg("user", "hello"),
		assistant,
		{Role: "tool", ToolCallID: "id1", Content: "tool result"},
		normMsg("user", "follow up"),
		normMsg("assistant", "response"),
	}

	// Deep copy to detect mutation.
	inputCopy := make([]providers.Message, len(input))
	copy(inputCopy, input)

	got := normalizeMessagesForProvider(input)

	if !reflect.DeepEqual(got, inputCopy) {
		t.Error("valid history must pass through unchanged (fast path)")
	}
	if len(got) != len(input) {
		t.Errorf("expected same length %d, got %d", len(input), len(got))
	}
}

// ── Empty assistant drop still works ─────────────────────────────────────────

// TestNormalize_EmptyAssistantDropped verifies Rule A still works after the rewrite.
// Note: dropping the blank assistant leaves two consecutive user messages which Rule B
// then merges — so the final result is 1 merged user message.
func TestNormalize_EmptyAssistantDropped(t *testing.T) {
	input := []providers.Message{
		normMsg("user", "hi"),
		normMsg("assistant", "   "), // blank — should be dropped
		normMsg("user", "there"),
	}

	got := normalizeMessagesForProvider(input)

	// After drop of empty assistant: two adjacent user messages → merge to 1.
	if len(got) != 1 {
		t.Fatalf("empty assistant drop: expected 1 merged user message, got %d: %+v", len(got), got)
	}
	for _, m := range got {
		if m.Role == "assistant" {
			t.Error("empty assistant message must be dropped")
		}
	}
	// Content should be merged.
	if got[0].Content != "hi\nthere" {
		t.Errorf("expected merged content 'hi\\nthere', got %q", got[0].Content)
	}
}

// TestNormalize_EmptyAssistantDropped_WithUser verifies a blank assistant between
// a user and an assistant message is dropped and the surrounding messages stay separate.
func TestNormalize_EmptyAssistantDropped_WithUser(t *testing.T) {
	input := []providers.Message{
		normMsg("user", "question"),
		normMsg("assistant", ""), // blank — should be dropped (Rule A)
		normMsg("assistant", "answer"),
	}

	got := normalizeMessagesForProvider(input)

	// user + "answer" assistant = 2 messages (after dropping blank assistant,
	// the remaining assistant is a plain-text one; user and assistant are different
	// roles so they don't merge).
	if len(got) != 2 {
		t.Fatalf("blank-then-answer: expected 2 messages, got %d: %+v", len(got), got)
	}
	if got[1].Content != "answer" {
		t.Errorf("expected second message content 'answer', got %q", got[1].Content)
	}
}

// ── Role "tool" messages NEVER merged ────────────────────────────────────────

// TestNormalize_ToolMessagesNeverMerged verifies that two consecutive role="tool"
// messages (both preceded by an assistant with ToolCalls) are NOT merged.
func TestNormalize_ToolMessagesNeverMerged(t *testing.T) {
	assistant := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			normTC("id1", "tool_a"),
			normTC("id2", "tool_b"),
		},
	}
	input := []providers.Message{
		assistant,
		{Role: "tool", ToolCallID: "id1", Content: "r1"},
		{Role: "tool", ToolCallID: "id2", Content: "r2"},
	}

	got := normalizeMessagesForProvider(input)

	// All 3 messages must survive (1 assistant + 2 tool results).
	if len(got) != 3 {
		t.Fatalf("tool no-merge: expected 3 messages, got %d: %+v", len(got), got)
	}
}

// ── Two user messages merge (existing behavior preserved) ────────────────────

// TestNormalize_TwoUserMessagesMerge verifies that two consecutive user messages
// are still merged (Rule B covers user/assistant/system without ToolCalls).
func TestNormalize_TwoUserMessagesMerge(t *testing.T) {
	input := []providers.Message{
		normMsg("user", "first"),
		normMsg("user", "second"),
	}

	got := normalizeMessagesForProvider(input)

	if len(got) != 1 {
		t.Fatalf("user merge: expected 1 merged message, got %d", len(got))
	}
	if got[0].Content != "first\nsecond" {
		t.Errorf("user merge: expected 'first\\nsecond', got %q", got[0].Content)
	}
}

// ── Merge carries Media from both messages ────────────────────────────────────

// TestNormalize_MergeCarriesMedia verifies that Media from the second message is
// appended to the merged result.
func TestNormalize_MergeCarriesMedia(t *testing.T) {
	a1 := providers.Message{Role: "user", Content: "text", Media: []string{"img1.png"}}
	a2 := providers.Message{Role: "user", Content: "more", Media: []string{"img2.png"}}

	got := normalizeMessagesForProvider([]providers.Message{a1, a2})

	if len(got) != 1 {
		t.Fatalf("media merge: expected 1 message, got %d", len(got))
	}
	if len(got[0].Media) != 2 {
		t.Errorf("media merge: expected 2 media items, got %v", got[0].Media)
	}
}

// ── Fix A: Rule D — true orphan tool results are dropped ─────────────────────

// TestNormalize_RuleD_TrueOrphanDropped verifies that a role="tool" message
// whose ToolCallID is not declared by any preceding assistant ToolCalls is
// dropped by the normalizer (Fix A). This guards the transcript-less path
// (processSystemMessage has no TranscriptStore → repairHistory is skipped).
func TestNormalize_RuleD_TrueOrphanDropped(t *testing.T) {
	input := []providers.Message{
		normMsg("system", "sys"),
		normMsg("user", "hi"),
		{Role: "tool", ToolCallID: "undeclared-id", Content: "orphan result"},
	}

	got := normalizeMessagesForProvider(input)

	// The orphan must be dropped; system and user survive.
	for _, m := range got {
		if m.Role == "tool" {
			t.Errorf("RuleD: true orphan (ToolCallID=%q not declared) must be dropped", m.ToolCallID)
		}
	}
	if len(got) != 2 {
		t.Errorf("RuleD: expected 2 messages (system+user) after drop, got %d", len(got))
	}
}

// TestNormalize_RuleD_F1_PairedToolResultsKept verifies that 3 parallel
// paired tool results all survive (F1 fix still holds under Rule D).
// Each ToolCallID is declared by the preceding assistant's ToolCalls — none
// is an orphan.
func TestNormalize_RuleD_F1_PairedToolResultsKept(t *testing.T) {
	assistant := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			normTC("id1", "tool_a"),
			normTC("id2", "tool_b"),
			normTC("id3", "tool_c"),
		},
	}
	input := []providers.Message{
		assistant,
		{Role: "tool", ToolCallID: "id1", Content: "r1"},
		{Role: "tool", ToolCallID: "id2", Content: "r2"},
		{Role: "tool", ToolCallID: "id3", Content: "r3"},
	}

	got := normalizeMessagesForProvider(input)

	// All 4 messages must survive: 1 assistant + 3 paired tool results.
	if len(got) != 4 {
		t.Fatalf("RuleD/F1: expected 4 messages, got %d: %+v", len(got), got)
	}
	ids := map[string]bool{}
	for _, m := range got {
		if m.Role == "tool" {
			ids[m.ToolCallID] = true
		}
	}
	for _, want := range []string{"id1", "id2", "id3"} {
		if !ids[want] {
			t.Errorf("RuleD/F1: paired tool result %q was wrongly dropped", want)
		}
	}
}

// TestNormalize_RuleD_EmptyToolCallID_AfterAssistantWithToolCalls verifies that
// a role="tool" message with an empty ToolCallID is KEPT when the immediately
// preceding kept message is an assistant carrying ToolCalls (single-call
// omitted-id case).
func TestNormalize_RuleD_EmptyToolCallID_AfterAssistantWithToolCalls(t *testing.T) {
	assistant := providers.Message{
		Role:      "assistant",
		ToolCalls: []providers.ToolCall{normTC("id1", "tool_a")},
	}
	input := []providers.Message{
		assistant,
		{Role: "tool", ToolCallID: "", Content: "result without id"},
	}

	got := normalizeMessagesForProvider(input)

	// Both must survive: assistant + tool-with-empty-id (single-call case).
	if len(got) != 2 {
		t.Fatalf("RuleD/emptyID: expected 2 messages, got %d: %+v", len(got), got)
	}
	found := false
	for _, m := range got {
		if m.Role == "tool" {
			found = true
		}
	}
	if !found {
		t.Error("RuleD/emptyID: tool result with empty ToolCallID after assistant-with-toolcalls must be kept")
	}
}

// TestNormalize_RuleD_TranscriptlessShape verifies that an orphan in a
// transcript-less shape (system, user, tool(orphan)) is dropped. This is the
// exact scenario the processSystemMessage background-task path can produce
// when repairHistory is skipped.
func TestNormalize_RuleD_TranscriptlessShape(t *testing.T) {
	input := []providers.Message{
		normMsg("system", "you are helpful"),
		normMsg("user", "do something"),
		{Role: "tool", ToolCallID: "orphan-call", Content: "lost result"},
	}

	got := normalizeMessagesForProvider(input)

	// Orphan must be dropped; system and user survive.
	if len(got) != 2 {
		t.Fatalf("RuleD/transcriptless: expected 2 messages after orphan drop, got %d: %+v", len(got), got)
	}
	for _, m := range got {
		if m.Role == "tool" {
			t.Errorf("RuleD/transcriptless: orphan tool result must be dropped, found: %+v", m)
		}
	}
}
