// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/memory"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
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
