// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- test doubles -----------------------------------------------------------

// stubArchive is an in-memory ConversationArchiveReader for tests.
type stubArchive struct {
	msgs []memory.ArchivedMessage
	// perKey maps session key to message slice (for session-scope tests).
	perKey map[string][]memory.ArchivedMessage
}

func (s *stubArchive) ReadArchive(_ context.Context, key string) ([]memory.ArchivedMessage, error) {
	if s.perKey != nil {
		if msgs, ok := s.perKey[key]; ok {
			return msgs, nil
		}
		return nil, nil
	}
	return s.msgs, nil
}

// stubSpanSetter records the last-set span and drop calls.
type stubSpanSetter struct {
	spans     map[string]*RecallSpan
	dropCalls []struct{ key, reason string }
}

func newStubSpanSetter() *stubSpanSetter {
	return &stubSpanSetter{spans: make(map[string]*RecallSpan)}
}

func (s *stubSpanSetter) setRecallSpan(key string, span *RecallSpan) {
	if span == nil {
		delete(s.spans, key)
	} else {
		s.spans[key] = span
	}
}

func (s *stubSpanSetter) dropRecallSpan(key, reason string) {
	s.dropCalls = append(s.dropCalls, struct{ key, reason string }{key, reason})
	delete(s.spans, key)
	// Propagate to the global counter so RecallSpanDropCount assertions work.
	recordRecallSpanDropped(reason)
}

// makeUserMsg returns a user message with the given content.
func makeUserMsg(content string) memory.ArchivedMessage {
	return memory.ArchivedMessage{Message: providers.Message{Role: "user", Content: content}}
}

// makeUserMsgTS returns a user message with a specific unix timestamp.
func makeUserMsgTS(content string, ts int64) memory.ArchivedMessage {
	return memory.ArchivedMessage{Message: providers.Message{Role: "user", Content: content}, TS: ts}
}

// makeAssistantMsg returns an assistant message.
func makeAssistantMsg(content string) memory.ArchivedMessage {
	return memory.ArchivedMessage{Message: providers.Message{Role: "assistant", Content: content}}
}

// makeAssistantWithTool returns an assistant message that includes a tool call.
func makeAssistantWithTool(content, toolID, toolName string) memory.ArchivedMessage {
	return memory.ArchivedMessage{Message: providers.Message{
		Role:    "assistant",
		Content: content,
		ToolCalls: []providers.ToolCall{{
			ID:   toolID,
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      toolName,
				Arguments: `{"arg":"val"}`,
			},
		}},
	}}
}

// makeToolResultMsg returns a tool result message matching the given call ID.
func makeToolResultMsg(toolID, content string) memory.ArchivedMessage {
	return memory.ArchivedMessage{Message: providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolID,
	}}
}

// makeTool creates a RecallConversationTool wired to the given archive and setter.
func makeTool(archive ConversationArchiveReader, setter RecallSpanSetter) *RecallConversationTool {
	return NewRecallConversationTool(archive, setter)
}

// makeCtx returns a context carrying the given session key.
func makeCtx(sessionKey string) context.Context {
	return tools.WithSessionKey(context.Background(), sessionKey)
}

// --- T12 TestRecallConversation_QueryBM25 -----------------------------------

// T12: a nonce query hits the BM25-ranked turn; the result is a whole Turn,
// BM25-ranked (the matching turn appears in the span), not the non-matching turn.
func TestRecallConversation_QueryBM25(t *testing.T) {
	const nonce = "NONCE_ALPHA_42"
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("Hello world, tell me something"),
		makeAssistantMsg("Sure here is some general info"),
		makeUserMsg("Now tell me about " + nonce + " specifically"),
		makeAssistantMsg("Details about " + nonce + " are as follows"),
		makeUserMsg("Unrelated topic entirely different subject"),
		makeAssistantMsg("Unrelated response here"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-bm25")
	result := tool.Execute(ctx, map[string]any{"query": nonce})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	span, ok := setter.spans["sess-bm25"]
	if !ok {
		t.Fatal("no span was set")
	}
	// The span must contain the nonce turn (turn 2 in 1-based).
	found := false
	for _, m := range span.Msgs {
		if strings.Contains(m.Content, nonce) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("nonce %q not found in span messages: %v", nonce, span.Msgs)
	}
	// The result message must be a whole Turn (starts with demarcation marker).
	if len(span.Msgs) < 2 {
		t.Errorf("expected at least marker + messages, got %d", len(span.Msgs))
	}
}

// --- T13 TestRecallConversation_TurnRange -----------------------------------

// T13: turn_range "2-3" returns exactly turns 2 and 3 (1-based), verbatim, in order.
func TestRecallConversation_TurnRange(t *testing.T) {
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("turn one user"),
		makeAssistantMsg("turn one assistant"),
		makeUserMsg("turn two user"),        // turn 2 starts here
		makeAssistantMsg("turn two assist"), // turn 2 ends here
		makeUserMsg("turn three user"),      // turn 3
		makeAssistantMsg("turn three asst"),
		makeUserMsg("turn four user"),
		makeAssistantMsg("turn four asst"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-range")
	result := tool.Execute(ctx, map[string]any{"turn_range": "2-3"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	span := setter.spans["sess-range"]
	if span == nil {
		t.Fatal("no span was set for turn_range")
	}
	// span.FromTurn=2 span.ToTurn=3
	if span.FromTurn != 2 || span.ToTurn != 3 {
		t.Errorf("expected FromTurn=2 ToTurn=3, got %d/%d", span.FromTurn, span.ToTurn)
	}
	// Verify content of both turns appears in order.
	combined := joinSpanContent(span)
	if !strings.Contains(combined, "turn two user") {
		t.Errorf("turn 2 content missing: %s", combined)
	}
	if !strings.Contains(combined, "turn three user") {
		t.Errorf("turn 3 content missing: %s", combined)
	}
	// Order: turn two must appear before turn three.
	if idx2 := strings.Index(combined, "turn two user"); idx2 < 0 {
		t.Error("turn two user not found")
	} else if idx3 := strings.Index(combined, "turn three user"); idx3 < idx2 {
		t.Error("turn three appeared before turn two — order violation")
	}
}

// --- T14 TestRecallConversation_TimeWindow ----------------------------------

// T14: time-window filter selects turns by per-line TS; TS==0 legacy → session-start,
// no crash.
func TestRecallConversation_TimeWindow(t *testing.T) {
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsgTS("legacy no ts", 0), // TS==0 legacy
		makeAssistantMsg("reply legacy"),
		makeUserMsgTS("early turn", 1000), // ts=1000
		makeAssistantMsg("early reply"),
		makeUserMsgTS("middle turn", 2000), // ts=2000 ← in window
		makeAssistantMsg("middle reply"),
		makeUserMsgTS("late turn", 3000), // ts=3000
		makeAssistantMsg("late reply"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-time")
	// Query [1500, 2500] — should include "middle turn" (ts=2000), not "early" (1000) or "late" (3000).
	result := tool.Execute(ctx, map[string]any{
		"time": map[string]any{
			"from": float64(1500),
			"to":   float64(2500),
		},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	span := setter.spans["sess-time"]
	if span == nil {
		t.Fatal("no span set")
	}
	combined := joinSpanContent(span)
	if !strings.Contains(combined, "middle turn") {
		t.Errorf("expected 'middle turn' in span, got: %s", combined)
	}
	if strings.Contains(combined, "early turn") {
		t.Errorf("did not expect 'early turn' in span, got: %s", combined)
	}
	if strings.Contains(combined, "late turn") {
		t.Errorf("did not expect 'late turn' in span, got: %s", combined)
	}

	// TS==0 (legacy) — ensure no crash when from=0 (session start).
	result2 := tool.Execute(ctx, map[string]any{
		"time": map[string]any{
			"from": float64(0),
			"to":   float64(500),
		},
	})
	// Should not error — TS==0 is treated as 0 (included when from=0).
	if result2.IsError {
		t.Errorf("legacy TS==0 should not cause an error: %s", result2.ForLLM)
	}
}

// --- T15 TestRecallConversation_OutputBounded --------------------------------

// T15: overflow → result truncated to ≤8 turns / ≤4000 tokens; "N more" note appended.
func TestRecallConversation_OutputBounded(t *testing.T) {
	// Build 12 turns each matching the query so overflow is forced.
	const nonce = "OVERFLOW_TOKEN_XYZ"
	msgs := make([]memory.ArchivedMessage, 0, 24)
	for i := 0; i < 12; i++ {
		msgs = append(msgs,
			makeUserMsg(fmt.Sprintf("turn %d about %s something", i+1, nonce)),
			makeAssistantMsg(fmt.Sprintf("answer %d", i+1)),
		)
	}
	archive := &stubArchive{msgs: msgs}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-bound")
	result := tool.Execute(ctx, map[string]any{"query": nonce})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	span := setter.spans["sess-bound"]
	if span == nil {
		t.Fatal("no span set")
	}

	// Verify the turn count in the span is ≤ recallDefaultTurns (8).
	// Use len(Ordinals) — the authoritative count of kept turns — rather than
	// the ToTurn-FromTurn+1 range arithmetic which overcounts for sparse recalls.
	if len(span.Ordinals) > recallDefaultTurns {
		t.Errorf("expected ≤%d turns in span (Ordinals), got %d", recallDefaultTurns, len(span.Ordinals))
	}

	// The result must mention "N more" when truncated.
	if !strings.Contains(result.ForLLM, "more") {
		t.Errorf("expected 'N more' hint in result when truncated; got: %s", result.ForLLM)
	}

	// Token cap: span.Tokens must be ≤ recallDefaultTokens.
	if span.Tokens > recallDefaultTokens {
		t.Errorf("span tokens %d exceeds cap %d", span.Tokens, recallDefaultTokens)
	}
}

// --- T16 TestRecallConversation_SessionScoped --------------------------------

// T16: session isolation + empty-query error.
func TestRecallConversation_SessionScoped(t *testing.T) {
	// Two sessions with different content.
	archive := &stubArchive{
		perKey: map[string][]memory.ArchivedMessage{
			"sess-A": {
				makeUserMsg("session A data secret"),
				makeAssistantMsg("session A reply"),
			},
			"sess-B": {
				makeUserMsg("session B content public"),
				makeAssistantMsg("session B reply"),
			},
		},
	}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	// T16.1: empty query → error (US-4.6)
	ctx := makeCtx("sess-A")
	result := tool.Execute(ctx, map[string]any{"query": ""})
	if !result.IsError {
		t.Error("empty query should return an error")
	}

	// T16.2: querying sess-A never returns sess-B data.
	result = tool.Execute(ctx, map[string]any{"query": "secret"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	spanA := setter.spans["sess-A"]
	if spanA == nil {
		t.Fatal("no span for sess-A")
	}
	combined := joinSpanContent(spanA)
	if strings.Contains(combined, "session B") {
		t.Error("sess-A span contains sess-B data — session isolation violated")
	}

	// T16.3: querying from a separate ctx (sess-B) does not return sess-A data.
	ctxB := makeCtx("sess-B")
	result = tool.Execute(ctxB, map[string]any{"query": "public"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	spanB := setter.spans["sess-B"]
	if spanB == nil {
		t.Fatal("no span for sess-B")
	}
	combinedB := joinSpanContent(spanB)
	if strings.Contains(combinedB, "session A") {
		t.Error("sess-B span contains sess-A data — session isolation violated")
	}
}

// --- T17 TestRecallSpan_ReinjectedProviderValid ----------------------------

// T17: a tool-bearing Turn is re-injected with rewritten recall_* IDs; every
// tool_call_id resolves; no collision with the live window; the tool is NOT re-executed.
func TestRecallSpan_ReinjectedProviderValid(t *testing.T) {
	const origID = "tc_original_001"
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("do something"),
		makeAssistantWithTool("I will call read_file", origID, "read_file"),
		makeToolResultMsg(origID, "file contents here"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-toolvalid")
	result := tool.Execute(ctx, map[string]any{"turn_range": "1-1"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	span := setter.spans["sess-toolvalid"]
	if span == nil {
		t.Fatal("no span set")
	}

	// Collect all tool_call_id references in the span.
	callIDs := make(map[string]int)   // assistant ToolCalls[].ID
	resultIDs := make(map[string]int) // tool message ToolCallID

	for _, m := range span.Msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				callIDs[tc.ID]++
			}
		}
		if m.ToolCallID != "" {
			resultIDs[m.ToolCallID]++
		}
	}

	// Every tool result ID must appear in callIDs (provider-valid pairs).
	for rid := range resultIDs {
		if callIDs[rid] == 0 {
			t.Errorf("tool result id %q has no matching assistant ToolCall — provider-invalid", rid)
		}
	}

	// The original ID must NOT appear (it was rewritten).
	if _, found := callIDs[origID]; found {
		t.Errorf("original tool_call_id %q was NOT rewritten — collision risk", origID)
	}
	if _, found := resultIDs[origID]; found {
		t.Errorf("original tool_call_id %q in result was NOT rewritten — collision risk", origID)
	}

	// All rewritten IDs must use the recall_ prefix.
	for id := range callIDs {
		if !strings.HasPrefix(id, "recall_") {
			t.Errorf("rewritten ID %q does not use recall_ prefix", id)
		}
	}
}

// --- T28 TestRecallSpan_NotPersistedToArchive --------------------------------

// T28: after a recall, the archive is unchanged (span is in-memory only).
func TestRecallSpan_NotPersistedToArchive(t *testing.T) {
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("hello"),
		makeAssistantMsg("hi"),
		makeUserMsg("world"),
		makeAssistantMsg("indeed"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-persist")
	before := len(archive.msgs)

	result := tool.Execute(ctx, map[string]any{"query": "hello"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// Archive must not have grown.
	after := len(archive.msgs)
	if after != before {
		t.Errorf("archive grew from %d to %d after recall — span must NOT be persisted", before, after)
	}
}

// --- T30 TestRecallSpan_ReplacedOnNextRecall --------------------------------

// T30: a second recall replaces the prior span (not accumulates).
func TestRecallSpan_ReplacedOnNextRecall(t *testing.T) {
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("alpha content first"),
		makeAssistantMsg("alpha reply"),
		makeUserMsg("beta content second"),
		makeAssistantMsg("beta reply"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-replace")

	// First recall: "alpha"
	r1 := tool.Execute(ctx, map[string]any{"query": "alpha"})
	if r1.IsError {
		t.Fatalf("first recall error: %s", r1.ForLLM)
	}
	span1 := setter.spans["sess-replace"]
	if span1 == nil {
		t.Fatal("no span after first recall")
	}

	// Verify "replaced" drop was NOT called yet (first recall has nothing to replace).
	droppedBeforeSecond := countDrops(setter.dropCalls, "sess-replace", "replaced")

	// Second recall: "beta"
	r2 := tool.Execute(ctx, map[string]any{"query": "beta"})
	if r2.IsError {
		t.Fatalf("second recall error: %s", r2.ForLLM)
	}
	span2 := setter.spans["sess-replace"]
	if span2 == nil {
		t.Fatal("no span after second recall")
	}

	// span2 must be different from span1 (beta content, not alpha).
	combined2 := joinSpanContent(span2)
	if !strings.Contains(combined2, "beta") {
		t.Errorf("second recall span should contain 'beta', got: %s", combined2)
	}

	// A "replaced" drop must have been emitted for the second recall.
	droppedAfterSecond := countDrops(setter.dropCalls, "sess-replace", "replaced")
	if droppedAfterSecond <= droppedBeforeSecond {
		t.Error("second recall did not emit a 'replaced' drop for the prior span")
	}

	// There should be exactly ONE active span (not two).
	if _, extra := setter.spans["sess-replace-extra"]; extra {
		t.Error("unexpected extra span")
	}
}

// --- FR-018 counter tests ---------------------------------------------------

func TestRecallConversation_HitCounter(t *testing.T) {
	before := RecallConversationCallCount("hit")
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("test content here"),
		makeAssistantMsg("test reply"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)
	ctx := makeCtx("sess-counter")
	r := tool.Execute(ctx, map[string]any{"query": "content"})
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.ForLLM)
	}
	after := RecallConversationCallCount("hit")
	if after <= before {
		t.Errorf("hit counter did not increment: before=%d after=%d", before, after)
	}
}

func TestRecallConversation_ErrorCounter(t *testing.T) {
	before := RecallConversationCallCount("error")
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("content"),
		makeAssistantMsg("reply"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)
	ctx := makeCtx("sess-errctr")
	// No mode → error.
	tool.Execute(ctx, map[string]any{})
	after := RecallConversationCallCount("error")
	if after <= before {
		t.Errorf("error counter did not increment: before=%d after=%d", before, after)
	}
}

func TestRecallSpan_DropCounter(t *testing.T) {
	before := RecallSpanDropCount("replaced")
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("alpha"),
		makeAssistantMsg("a reply"),
		makeUserMsg("beta"),
		makeAssistantMsg("b reply"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)
	ctx := makeCtx("sess-dropcount")

	// First recall: no prior span to drop.
	tool.Execute(ctx, map[string]any{"query": "alpha"})
	// Second recall: should drop the prior span with "replaced".
	tool.Execute(ctx, map[string]any{"query": "beta"})

	after := RecallSpanDropCount("replaced")
	if after <= before {
		t.Errorf("replaced drop counter did not increment: before=%d after=%d", before, after)
	}
}

// --- M1: top-relevance turn selected when >8 turns match -------------------

// TestRecallConversation_QueryBM25_TopRelevanceKept proves that when more than
// recallDefaultTurns (8) turns match the query, the bounds step keeps the
// TOP-relevance hits (by BM25 score), NOT the earliest 8 turns by position.
//
// Fixture: 10 turns that all contain the query term "RELEVANCE_NONCE", but
// only turn 10 (the last one) also contains the rare high-scoring term
// "SPECIAL_RARE_SIGNAL_XQYZ". With >8 hits, the old code's pre-bounds
// chronological sort would keep turns 1–8, dropping the highest-scoring turn
// 10. The fixed code keeps the top-N by score first, so turn 10 must appear.
func TestRecallConversation_QueryBM25_TopRelevanceKept(t *testing.T) {
	const common = "RELEVANCE_NONCE"
	const rare = "SPECIAL_RARE_SIGNAL_XQYZ"

	msgs := make([]memory.ArchivedMessage, 0, 20)
	for i := 0; i < 9; i++ {
		// Turns 1–9: contain the common term only.
		msgs = append(msgs,
			makeUserMsg(fmt.Sprintf("turn %d about %s general", i+1, common)),
			makeAssistantMsg(fmt.Sprintf("answer %d", i+1)),
		)
	}
	// Turn 10: contains both the common term AND the rare high-scoring term.
	// BM25 will rank this higher because the rare term has high IDF.
	msgs = append(msgs,
		makeUserMsg(fmt.Sprintf("turn 10 about %s and also %s important detail", common, rare)),
		makeAssistantMsg("answer 10 with rare content"),
	)

	archive := &stubArchive{msgs: msgs}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-toprel")
	// Query for the rare term — only turn 10 matches it, so turn 10 must appear
	// in the span even though it is the last (10th) turn and >8 turns match the
	// common term alone.
	result := tool.Execute(ctx, map[string]any{"query": rare})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	span := setter.spans["sess-toprel"]
	if span == nil {
		t.Fatal("no span was set")
	}
	combined := joinSpanContent(span)
	if !strings.Contains(combined, rare) {
		t.Errorf("top-relevance turn (containing %q) was not returned in span: %s", rare, combined)
	}
}

// --- Sparse marker: query/time modes produce honest non-contiguous marker ----

// TestRecallConversation_SparseMarker verifies that when BM25/time mode returns
// non-contiguous turns, the demarcation marker and confirmation string list the
// actual ordinals rather than advertising a contiguous range.
//
// Fixture: 5 turns where the query term appears only in turns 1 and 5, so
// keptIdxs = [0, 4] → ordinals [1, 5] (non-contiguous; gap at 2,3,4).
// The old code would have emitted "turns 1–5" (implying 5 turns); the fix
// must emit the explicit ordinal list.
func TestRecallConversation_SparseMarker(t *testing.T) {
	const nonce = "SPARSE_NONCE_ZETA"
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("turn one " + nonce + " first occurrence"),
		makeAssistantMsg("reply one"),
		makeUserMsg("turn two completely unrelated"),
		makeAssistantMsg("reply two"),
		makeUserMsg("turn three also unrelated"),
		makeAssistantMsg("reply three"),
		makeUserMsg("turn four nothing interesting"),
		makeAssistantMsg("reply four"),
		makeUserMsg("turn five " + nonce + " second occurrence"),
		makeAssistantMsg("reply five"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-sparse")
	result := tool.Execute(ctx, map[string]any{"query": nonce})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	span := setter.spans["sess-sparse"]
	if span == nil {
		t.Fatal("no span was set")
	}

	// Must have exactly the turns that contain the nonce (turns 1 and 5).
	// Ordinals must be [1, 5] — non-contiguous.
	if len(span.Ordinals) != 2 {
		t.Errorf("expected 2 ordinals, got %d: %v", len(span.Ordinals), span.Ordinals)
	}
	if span.FromTurn != 1 || span.ToTurn != 5 {
		t.Errorf("expected FromTurn=1 ToTurn=5, got %d/%d", span.FromTurn, span.ToTurn)
	}

	// The demarcation marker in Msgs[0] must list ordinals, NOT "turns 1–5".
	if len(span.Msgs) == 0 {
		t.Fatal("span has no messages")
	}
	markerContent := span.Msgs[0].Content
	if strings.Contains(markerContent, "turns 1–5") {
		t.Errorf("sparse marker must not claim contiguous 'turns 1–5': %q", markerContent)
	}
	if !strings.Contains(markerContent, "1") || !strings.Contains(markerContent, "5") {
		t.Errorf("sparse marker must list ordinals 1 and 5: %q", markerContent)
	}
	// The marker must mention "ordinals" or count-based language (not a range dash).
	if !strings.Contains(markerContent, "ordinals") {
		t.Errorf("sparse marker should mention 'ordinals': %q", markerContent)
	}

	// The confirmation result must also use ordinal-list form, not "turns 1–5".
	if strings.Contains(result.ForLLM, "turns 1–5") {
		t.Errorf("confirmation string must not claim contiguous 'turns 1–5': %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "ordinals") {
		t.Errorf("confirmation string should mention 'ordinals': %q", result.ForLLM)
	}
}

// TestRecallSpan_Ordinals_ContiguousRange verifies that turn_range mode (which
// always produces a contiguous set) populates Ordinals correctly and that the
// marker uses the compact "turns A–B" form (not the sparse ordinal-list form).
func TestRecallSpan_Ordinals_ContiguousRange(t *testing.T) {
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("turn one"),
		makeAssistantMsg("reply one"),
		makeUserMsg("turn two"),
		makeAssistantMsg("reply two"),
		makeUserMsg("turn three"),
		makeAssistantMsg("reply three"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	ctx := makeCtx("sess-contiguous")
	result := tool.Execute(ctx, map[string]any{"turn_range": "1-3"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	span := setter.spans["sess-contiguous"]
	if span == nil {
		t.Fatal("no span set")
	}

	// Ordinals must be [1, 2, 3].
	if len(span.Ordinals) != 3 {
		t.Errorf("expected 3 ordinals, got %d: %v", len(span.Ordinals), span.Ordinals)
	}
	for i, want := range []int{1, 2, 3} {
		if span.Ordinals[i] != want {
			t.Errorf("Ordinals[%d] = %d, want %d", i, span.Ordinals[i], want)
		}
	}

	// Marker must use the compact range form.
	if len(span.Msgs) == 0 {
		t.Fatal("span has no messages")
	}
	markerContent := span.Msgs[0].Content
	if !strings.Contains(markerContent, "turns 1–3") {
		t.Errorf("contiguous marker should use 'turns 1–3': %q", markerContent)
	}
	if strings.Contains(markerContent, "ordinals") {
		t.Errorf("contiguous marker should not mention 'ordinals': %q", markerContent)
	}
}

// TestRecallSpan_NewRecallSpan_FromTurnZeroPanics verifies the fromTurn>=1
// invariant: passing fromTurn=0 must panic.
func TestRecallSpan_NewRecallSpan_FromTurnZeroPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for fromTurn=0, but did not panic")
		}
	}()
	_ = newRecallSpan(0, 1,
		[]providers.Message{{Role: "user", Content: "x"}},
		[]int{1},
	)
}

// --- M6: empty-tokenizable query returns distinct error ---------------------

// TestRecallConversation_EmptyTokenizableQuery verifies that a query whose
// tokens are all punctuation/non-alphanumeric returns a distinct actionable
// error (not "no matching turns") and increments the "error" counter.
func TestRecallConversation_EmptyTokenizableQuery(t *testing.T) {
	archive := &stubArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("some content here"),
		makeAssistantMsg("some reply"),
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)
	ctx := makeCtx("sess-m6")

	errBefore := RecallConversationCallCount("error")

	// Punctuation-only query — retroTokenize will return empty slice.
	result := tool.Execute(ctx, map[string]any{"query": "!!! ??? ---"})
	if !result.IsError {
		t.Errorf("expected an error result for punctuation-only query, got: %s", result.ForLLM)
	}
	// The error message must be distinct (not "no matching turns").
	if strings.Contains(result.ForLLM, "no matching turns") {
		t.Errorf("error message must be distinct from 'no matching turns', got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "no searchable terms") {
		t.Errorf("error message should mention 'no searchable terms', got: %s", result.ForLLM)
	}
	errAfter := RecallConversationCallCount("error")
	if errAfter <= errBefore {
		t.Errorf("error counter did not increment: before=%d after=%d", errBefore, errAfter)
	}
}

// --- M7: newRecallSpan Tokens == Σ estimateMessageTokens --------------------

// TestRecallSpan_NewRecallSpan_TokensConsistent verifies that newRecallSpan
// computes Tokens as the exact sum of estimateMessageTokens over its messages,
// so Tokens and Msgs can never desync.
func TestRecallSpan_NewRecallSpan_TokensConsistent(t *testing.T) {
	spanMsgs := []providers.Message{
		{Role: "user", Content: "hello world this is a test message"},
		{Role: "assistant", Content: "acknowledgment response with some words"},
	}
	span := newRecallSpan(1, 1, spanMsgs, []int{1})

	// Independently compute the expected token sum.
	expected := 0
	for _, m := range spanMsgs {
		expected += estimateMessageTokens(m)
	}

	if span.Tokens != expected {
		t.Errorf("span.Tokens = %d, want Σ estimateMessageTokens = %d", span.Tokens, expected)
	}
	if span.Tokens == 0 {
		t.Errorf("token sum unexpectedly zero — fixture may be empty")
	}
	// Messages() must return exactly the input slice.
	got := span.Messages()
	if len(got) != len(spanMsgs) {
		t.Errorf("Messages() len = %d, want %d", len(got), len(spanMsgs))
	}
}

// --- helpers ----------------------------------------------------------------

// joinSpanContent concatenates all span message Content fields for easy assertion.
func joinSpanContent(span *RecallSpan) string {
	if span == nil {
		return ""
	}
	var sb strings.Builder
	for _, m := range span.Msgs {
		sb.WriteString(m.Content)
		sb.WriteString(" ")
	}
	return sb.String()
}

// countDrops returns the number of drop calls for the given key+reason pair.
func countDrops(calls []struct{ key, reason string }, key, reason string) int {
	n := 0
	for _, c := range calls {
		if c.key == key && c.reason == reason {
			n++
		}
	}
	return n
}

// --- T066-14: recall by tool_call_id, in pages -------------------------------
//
// ADR-066 §6.3 / FR-024…FR-027, FR-043 (per-page injection), FR-046.
// BDD B-28, B-29, B-29b, B-30, B-31, B-31b, B-53b. Data set DS-6.

// scanArchive is a ConversationArchiveReader that ALSO implements
// ConversationArchiveScanner and recallProjectionReader, counting how many
// lines a scan decoded and how many times the whole-archive ReadArchive path
// was taken — the two numbers B-31b is about.
type scanArchive struct {
	msgs     []memory.ArchivedMessage
	visited  int
	reads    int
	hydrated bool
}

func (s *scanArchive) ReadArchive(_ context.Context, _ string) ([]memory.ArchivedMessage, error) {
	s.reads++
	return s.msgs, nil
}

func (s *scanArchive) ScanArchive(
	_ context.Context, _ string, fn func(idx int, msg memory.ArchivedMessage) bool,
) error {
	for i, m := range s.msgs {
		s.visited++
		if !fn(i, m) {
			return nil
		}
	}
	return nil
}

func (s *scanArchive) Projection(string) memory.ProjectionMeta {
	return memory.ProjectionMeta{Hydrated: s.hydrated}
}

// incidentResult is the DS-6 payload: 1,178,522 characters, the size of the
// tool result that produced the incident this ADR is about. Every character
// is ASCII, so rune offsets and byte offsets coincide and a page can be
// compared against a plain slice of this string.
func incidentResult() string {
	return strings.Repeat("incident ", 130_947)[:1_178_522]
}

// idArchive returns the DS-6 archive: one user line, one assistant tool call
// for id on tool, and the tool result at archive line 2.
func idArchive(id, tool, content string) []memory.ArchivedMessage {
	return []memory.ArchivedMessage{
		makeUserMsg("find the invoices"),
		makeAssistantWithTool("", id, tool),
		makeToolResultMsg(id, content),
	}
}

// pageOf returns the recall page message a tool_call_id call installed, and
// splits its content into the framing line and the payload after it.
func pageOf(t *testing.T, setter *stubSpanSetter, key string) (msg providers.Message, framing, payload string) {
	t.Helper()
	span, ok := setter.spans[key]
	if !ok {
		t.Fatalf("no recall span installed for %q", key)
	}
	if len(span.Msgs) != 3 {
		t.Fatalf("page span has %d messages, want marker + assistant + page", len(span.Msgs))
	}
	msg = span.Msgs[2]
	if msg.Role != "tool" {
		t.Fatalf("page message role = %q, want tool", msg.Role)
	}
	nl := strings.IndexByte(msg.Content, '\n')
	if nl <= 0 {
		t.Fatalf("page content has no framing line: %.200q", msg.Content)
	}
	return msg, msg.Content[:nl], msg.Content[nl+1:]
}

// TestRecallConversation_ToolCallID_PageFitsAfterFraming — test 26, B-28,
// DS-6 #1. The first page of a 1,178,522-char archived result: payload =
// effective cap − framing, the whole message is ≤ the builtin-success cap,
// and it passes the choke point's cap unmodified.
func TestRecallConversation_ToolCallID_PageFitsAfterFraming(t *testing.T) {
	const id = "call_inc"
	content := incidentResult()
	archive := &scanArchive{msgs: idArchive(id, "mcp_gmail_search_email", content)}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	res := tool.Execute(makeCtx("sess-page-1"), map[string]any{"tool_call_id": id})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}

	msg, framing, payload := pageOf(t, setter, "sess-page-1")

	// The whole message — framing included — is inside the cap the recall
	// page is admitted under (builtin success: recall is a builtin).
	const capChars = config.DefaultBuiltinSuccessCap
	if n := utf8.RuneCountInString(msg.Content); n > capChars {
		t.Fatalf("page message is %d chars, want ≤ %d (B-28)", n, capChars)
	}
	// …and it passes the choke point unmodified: the pure cap is a no-op.
	projected, cut := projectToolResult(msg.Content, capChars, func(string) string { return "[mark]" })
	if cut || projected != msg.Content {
		t.Fatalf("B-28: the page must pass the choke point unmodified (cut=%v)", cut)
	}
	// The payload really uses the cap — a page must not be token-sized.
	if len(payload) < 63_000 {
		t.Fatalf("payload is %d chars; a page must be ~cap − framing, not %d", len(payload), len(payload))
	}
	// DS-6 #1: the payload is the head of the archived content.
	if payload != content[:len(payload)] {
		t.Fatalf("payload is not the first %d chars of the archived result", len(payload))
	}
	// The framing states the total and where the next page starts.
	if !strings.Contains(framing, "total=1178522") {
		t.Fatalf("framing must state the total size, got %q", framing)
	}
	if !strings.Contains(framing, fmt.Sprintf("next_offset=%d", len(payload))) {
		t.Fatalf("framing must state the next offset %d, got %q", len(payload), framing)
	}
	if !strings.Contains(framing, "tool_call_id="+id) {
		t.Fatalf("framing must name the tool_call_id, got %q", framing)
	}
	// FR-024: the streaming scan served this, not a whole-archive read.
	if archive.reads != 0 {
		t.Fatalf("ReadArchive was called %d times; the id mode must stream (B-31b)", archive.reads)
	}
}

// TestRecallConversation_ToolCallID_PagingReachesLastByte — test 27, B-29,
// DS-6 #2…#6. Contiguous pages reproduce the archived result byte for byte;
// offset at/past the end is an empty page stating the total; offset < 0 and
// length < 1 are errors; length above the page size is clamped.
func TestRecallConversation_ToolCallID_PagingReachesLastByte(t *testing.T) {
	const id, key = "call_inc", "sess-page-2"
	content := incidentResult()
	archive := &scanArchive{msgs: idArchive(id, "read_file", content)}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)
	ctx := makeCtx(key)

	// DS-6 #2: page from 0 following each page's next_offset to the end.
	var got strings.Builder
	offset, pages := 0, 0
	for {
		res := tool.Execute(ctx, map[string]any{"tool_call_id": id, "offset": offset})
		if res.IsError {
			t.Fatalf("page at offset %d errored: %s", offset, res.ForLLM)
		}
		msg, framing, payload := pageOf(t, setter, key)
		if n := utf8.RuneCountInString(msg.Content); n > config.DefaultBuiltinSuccessCap {
			t.Fatalf("page at offset %d is %d chars, over the cap", offset, n)
		}
		if payload != content[offset:offset+len(payload)] {
			t.Fatalf("page at offset %d is not the archived slice", offset)
		}
		got.WriteString(payload)
		pages++
		if pages > 100 {
			t.Fatalf("paging did not terminate after %d pages", pages)
		}
		if strings.Contains(framing, "next_offset=end") {
			break
		}
		offset += len(payload)
	}
	if got.String() != content {
		t.Fatalf("concatenated pages (%d chars over %d pages) != the archived result (%d chars)",
			got.Len(), pages, len(content))
	}
	if pages < 2 {
		t.Fatalf("a 1,178,522-char result must need more than one page, got %d", pages)
	}

	// DS-6 #3: offset at the end → an empty page that still states the total.
	res := tool.Execute(ctx, map[string]any{"tool_call_id": id, "offset": len(content)})
	if res.IsError {
		t.Fatalf("offset == total must not be an error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "1178522") {
		t.Fatalf("the empty page must state the total, got %q", res.ForLLM)
	}

	// DS-6 #4 / #6: invalid paging values are tool errors naming the field.
	for _, tc := range []struct {
		name  string
		args  map[string]any
		field string
	}{
		{"offset -1", map[string]any{"tool_call_id": id, "offset": -1}, "offset"},
		{"length 0", map[string]any{"tool_call_id": id, "length": 0}, "length"},
		{"archive_line -1", map[string]any{"tool_call_id": id, "archive_line": -1}, "archive_line"},
	} {
		r := tool.Execute(ctx, tc.args)
		if !r.IsError {
			t.Fatalf("%s must be a tool error, got %q", tc.name, r.ForLLM)
		}
		if !strings.Contains(r.ForLLM, tc.field) {
			t.Fatalf("%s error must name %q, got %q", tc.name, tc.field, r.ForLLM)
		}
	}

	// DS-6 #5: length above the page size is clamped to the page; a smaller
	// length is honoured exactly.
	full := tool.Execute(ctx, map[string]any{"tool_call_id": id, "offset": 0})
	if full.IsError {
		t.Fatalf("unexpected error: %s", full.ForLLM)
	}
	_, _, defaultPayload := pageOf(t, setter, key)
	clamped := tool.Execute(ctx, map[string]any{"tool_call_id": id, "offset": 0, "length": 70_000})
	if clamped.IsError {
		t.Fatalf("unexpected error: %s", clamped.ForLLM)
	}
	_, _, clampedPayload := pageOf(t, setter, key)
	if clampedPayload != defaultPayload {
		t.Fatalf("length 70,000 must clamp to the page size (%d chars), got %d",
			len(defaultPayload), len(clampedPayload))
	}
	small := tool.Execute(ctx, map[string]any{"tool_call_id": id, "offset": 10, "length": 25})
	if small.IsError {
		t.Fatalf("unexpected error: %s", small.ForLLM)
	}
	_, _, smallPayload := pageOf(t, setter, key)
	if smallPayload != content[10:35] {
		t.Fatalf("length 25 at offset 10 must return exactly chars 10–34, got %d chars", len(smallPayload))
	}
}

// TestRecallConversation_ToolCallID_DuplicateIds — test 28, B-29b, DS-6 #7
// and #8. Provider-generated ids are not unique across an archive: the most
// recent line wins, and archive_line addresses the older one.
func TestRecallConversation_ToolCallID_DuplicateIds(t *testing.T) {
	const id, key = "call_0", "sess-dupe"
	const older, newer = "OLDER-RESULT-PAYLOAD", "NEWER-RESULT-PAYLOAD"
	archive := &scanArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("first question"),              // 0
		makeAssistantWithTool("", id, "read_file"), // 1
		makeToolResultMsg(id, older),               // 2
		makeUserMsg("second question"),             // 3
		makeAssistantWithTool("", id, "read_file"), // 4
		makeToolResultMsg(id, newer),               // 5
	}}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)
	ctx := makeCtx(key)

	// DS-6 #7: no archive_line → the most recent line.
	if res := tool.Execute(ctx, map[string]any{"tool_call_id": id}); res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	_, framing, payload := pageOf(t, setter, key)
	if payload != newer {
		t.Fatalf("most recent line must win, got %q", payload)
	}
	if !strings.Contains(framing, "archive_line=5") {
		t.Fatalf("framing must cite archive line 5, got %q", framing)
	}

	// DS-6 #8: archive_line selects the older line.
	if res := tool.Execute(ctx, map[string]any{"tool_call_id": id, "archive_line": 2}); res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	_, framing, payload = pageOf(t, setter, key)
	if payload != older {
		t.Fatalf("archive_line 2 must return the older line, got %q", payload)
	}
	if !strings.Contains(framing, "archive_line=2") {
		t.Fatalf("framing must cite archive line 2, got %q", framing)
	}

	// An archive_line that holds no such result is an error naming the line.
	res := tool.Execute(ctx, map[string]any{"tool_call_id": id, "archive_line": 3})
	if !res.IsError {
		t.Fatalf("archive_line 3 holds a user message; want an error, got %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "3") || !strings.Contains(res.ForLLM, id) {
		t.Fatalf("the error must name the line and the id, got %q", res.ForLLM)
	}
}

// TestRecallConversation_ToolCallID_ExemptNotFoundExclusiveStreaming —
// test 29, B-30 / B-31 / B-31b. The addressed page is exempt from the
// 4,000 / 8,000-token span budgets; an unknown or rolled-back id is a tool
// error naming it; two modes at once is an error; and the archive is
// streamed, stopping at the addressed line.
func TestRecallConversation_ToolCallID_ExemptNotFoundExclusiveStreaming(t *testing.T) {
	const id = "call_inc"
	content := incidentResult()

	// B-30: a full page is far over both span budgets and is installed anyway.
	archive := &scanArchive{msgs: idArchive(id, "read_file", content)}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)
	if res := tool.Execute(makeCtx("sess-exempt"), map[string]any{"tool_call_id": id}); res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	span := setter.spans["sess-exempt"]
	if span == nil {
		t.Fatal("B-30: the page span must be installed")
	}
	if span.Tokens <= recallRangeTokens {
		t.Fatalf("B-30: the page span is %d tokens; the fixture must exceed the %d-token range budget "+
			"for the exemption to mean anything", span.Tokens, recallRangeTokens)
	}

	// B-31: an id that is in no archive line — unknown, or from a turn that
	// aborted and was rolled back (its tool line is gone, the assistant call
	// with it) — is a tool error naming the id.
	rolledBack := &scanArchive{msgs: []memory.ArchivedMessage{
		makeUserMsg("do the thing"),
		makeAssistantWithTool("", "call_aborted", "read_file"), // call recorded, result never archived
	}}
	rbTool := makeTool(rolledBack, newStubSpanSetter())
	for _, missing := range []string{"call_never_existed", "call_aborted"} {
		res := rbTool.Execute(makeCtx("sess-missing"), map[string]any{"tool_call_id": missing})
		if !res.IsError {
			t.Fatalf("%s must be a tool error, got %q", missing, res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, missing) {
			t.Fatalf("the error must name the id %q, got %q", missing, res.ForLLM)
		}
	}

	// B-31: exactly one mode.
	for _, args := range []map[string]any{
		{"tool_call_id": id, "query": "invoices"},
		{"tool_call_id": id, "turn_range": "1-2"},
		{"tool_call_id": id, "time": map[string]any{"from": 0}},
	} {
		res := tool.Execute(makeCtx("sess-modes"), args)
		if !res.IsError {
			t.Fatalf("two modes at once must error, got %q", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "exactly one") {
			t.Fatalf("the mode error must say exactly one, got %q", res.ForLLM)
		}
	}

	// B-31b: the scan streams and stops at the addressed line — the tail of
	// the archive is never decoded, and the whole-archive path is never taken.
	long := make([]memory.ArchivedMessage, 0, 203)
	long = append(long, idArchive(id, "read_file", "PAYLOAD")...)
	for i := 0; i < 200; i++ {
		long = append(long, makeUserMsg(fmt.Sprintf("later line %d", i)))
	}
	streaming := &scanArchive{msgs: long}
	sTool := makeTool(streaming, newStubSpanSetter())
	if res := sTool.Execute(makeCtx("sess-stream"), map[string]any{"tool_call_id": id, "archive_line": 2}); res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if streaming.reads != 0 {
		t.Fatalf("B-31b: ReadArchive was called %d times; the id mode must stream", streaming.reads)
	}
	if streaming.visited != 3 {
		t.Fatalf("B-31b: the scan decoded %d of %d lines; it must stop at the addressed line",
			streaming.visited, len(long))
	}
}

// TestRecallConversation_ToolCallID_HydratedSessionNotAvailable — B-53b /
// FR-046. A hydrated archive was rebuilt from the UI transcript, so the
// original tool-result bytes are not there to page.
func TestRecallConversation_ToolCallID_HydratedSessionNotAvailable(t *testing.T) {
	const id = "call_inc"
	archive := &scanArchive{msgs: idArchive(id, "read_file", "PAYLOAD"), hydrated: true}
	setter := newStubSpanSetter()
	tool := makeTool(archive, setter)

	res := tool.Execute(makeCtx("sess-hydrated"), map[string]any{"tool_call_id": id})
	if !res.IsError {
		t.Fatalf("recall by id on a hydrated session must be a tool error, got %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not available — session was rebuilt from the transcript") {
		t.Fatalf("FR-046 answer required, got %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, id) {
		t.Fatalf("the answer must name the id, got %q", res.ForLLM)
	}
	if len(setter.spans) != 0 {
		t.Fatal("no span may be installed for a hydrated session")
	}
}

// TestRecallConversation_CatalogMirrorParametersInSync — pkg/tools carries a
// metadata-only mirror of this tool (recall_conversation_meta.go) because it
// cannot import pkg/agent; the catalog and the Constraint #6 coverage
// universe read the mirror, not the real tool. Its header names
// pkg/agent/recall_conversation.go as the source of truth, and nothing
// enforced that until T066-14 found the mirror three modes out of date.
// pkg/agent CAN see pkg/tools, so the parameter names are compared here.
func TestRecallConversation_CatalogMirrorParametersInSync(t *testing.T) {
	live := makeTool(&stubArchive{}, newStubSpanSetter()).Parameters()
	var mirror map[string]any
	for _, meta := range tools.GeneralBuiltinMetadata() {
		if meta.Name() == "recall_conversation" {
			mirror = meta.Parameters()
			break
		}
	}
	if mirror == nil {
		t.Fatal("recall_conversation is missing from the general builtin catalog metadata")
	}
	names := func(params map[string]any) []string {
		props, _ := params["properties"].(map[string]any)
		out := make([]string, 0, len(props))
		for k := range props {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	got, want := names(mirror), names(live)
	if !slices.Equal(got, want) {
		t.Fatalf("catalog mirror parameters %v != the real tool's %v — keep "+
			"pkg/tools/recall_conversation_meta.go in sync with pkg/agent/recall_conversation.go", got, want)
	}
}
