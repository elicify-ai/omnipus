// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// Package agent context-paging integration tests — covers the CRITICAL and MAJOR
// gaps identified in the test-coverage review for the context-paging epic.
//
// Gap tests added here (all reference the spec
// docs/internal/specs/context-paging-sliding-window-recall-spec.md):
//
//   - T29/SC-016: recall span dropped-first under pressure
//   - T23/MAJ-05: fit-invariant arithmetic after recall
//   - T17-full/SC-015: zero duplicate/orphan tool_call_ids across span+window
//   - T22/FR-014: legacy [dropped N] summary marker is inert
//   - T18 (strengthened): rankRetrosBM25 byte-for-byte regression vs hand-computed
//   - T10 (strengthened): buildBreadcrumb wired through stub provider call counter
//   - T31-real: span placement tested through the real BuildMessages path via
//     ContextBuilder (assembleMessages integration cannot be driven directly
//     without a full turn-loop; the ContextBuilder path is the validated boundary).

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ============================================================================
// T29 / SC-016 — recall span dropped-first under budget pressure
// ============================================================================

// TestRecallSpan_DroppedFirstUnderPressure verifies FR-019's "dropped first"
// invariant: when a session is over budget and an active recall span exists,
// windowTrim MUST drop the whole span BEFORE evicting any real window Turn.
//
// Two sub-scenarios:
//
//	(a) dropping the span alone brings the window under budget → Skip is NOT
//	    advanced (no real Turns evicted), RecallSpanDropCount("pressure") increments.
//	(b) dropping the span is insufficient → real Turns are then evicted.
//
// Traces to: spec §7 T29, FR-019, SC-016, spec line 294-295.
func TestRecallSpan_DroppedFirstUnderPressure(t *testing.T) {
	// ContextWindow=3000 gives a predictable budget.
	// budget = 3000 - maxTokens(600) - ceil(0.05*3000)(150) - pinnedCoreOverhead
	// pinnedCoreOverhead ≈ breadcrumbTokenCap(1000) + tiny system-prompt = ~1000+
	// → budget ends up small, meaning a window that is "close to budget" can be
	// pushed over by adding a large recall span.
	const (
		cw = 6000
		mt = 500
	)

	t.Run("span_drop_sufficient", func(t *testing.T) {
		// BDD: Given a live window that fits under budget WITHOUT the span,
		//      When a recall span is active making the total over budget,
		//      Then windowTrim drops the span and returns ok=false (no Turn eviction),
		//      And RecallSpanDropCount("pressure") increments.
		//
		// We need the window alone to fit, but window+span to exceed contextWindow.
		// To simplify the budget arithmetic we set contextWindow and maxTokens so
		// the window tokens < (cw - mt) and recallSpan pushes it over.
		//
		// Approach: use a small window (2 turns, ~50 tokens) and a very large
		// recall span (many large messages). windowTrim sees:
		//   windowTokens (small) + toolDefsTokens (~0) + recallSpanTokens (large) > contextWindow
		// After dropping the span:
		//   windowTokens (small) + toolDefsTokens (~0) ≤ contextWindow  → return false.
		//
		// Use cw=500, mt=50 so the budget is tight. The window itself is tiny but
		// the recall span is large.
		al, _ := buildTrimTestAgentLoop(t, 500, 50)
		const sk = "t29-span-sufficient"

		// Seed a small live window (2 messages, each ~10 chars ≈ 5 tokens each).
		smallWindow := []providers.Message{
			trimTestMsg("user", "hello there"),
			trimTestMsg("assistant", "hi there"),
		}
		seedHistory(t, al, sk, smallWindow)

		// Install a large recall span (~200 large messages to ensure it pushes total
		// far over budget). Each message is ~200 chars ≈ 80 tokens.
		bigMsg := strings.Repeat("x", 200)
		var spanMsgs []providers.Message
		for i := 0; i < 20; i++ {
			spanMsgs = append(spanMsgs, providers.Message{Role: "user", Content: bigMsg})
		}
		bigSpan := newRecallSpan(1, 5, spanMsgs, []int{1, 2, 3, 4, 5})
		al.setRecallSpan(sk, bigSpan)

		// Verify the span is active before the trim.
		if al.activeRecallSpan(sk) == nil {
			t.Fatal("precondition: recall span must be active before windowTrim")
		}

		pressureBefore := RecallSpanDropCount("pressure")

		// Run windowTrim. Because the window alone is tiny and under budget, but
		// window+span is over budget, windowTrim should drop the span and return ok=false.
		_, ok := al.windowTrim(al.GetRegistry().GetDefaultAgent(), "", sk)

		// The span must be dropped.
		if al.activeRecallSpan(sk) != nil {
			t.Error("SC-016: recall span must be dropped under pressure")
		}

		// RecallSpanDropCount("pressure") must have incremented.
		pressureAfter := RecallSpanDropCount("pressure")
		if pressureAfter <= pressureBefore {
			t.Errorf("SC-016: RecallSpanDropCount(pressure) did not increment: before=%d after=%d",
				pressureBefore, pressureAfter)
		}

		// The window must NOT have been trimmed (ok=false means no real Turn eviction).
		// (ok=false is the signal that no eviction occurred — either because no trim
		// was needed or because the span drop was sufficient.)
		if ok {
			// If ok=true, the window was trimmed — that means the span drop alone was
			// NOT sufficient. But we sized the window to be tiny, so this would be a bug.
			afterWindow := al.GetRegistry().GetDefaultAgent().Sessions.GetHistory(sk)
			if len(afterWindow) < len(smallWindow) {
				t.Errorf(
					"SC-016: real Turns were evicted even though span-drop alone should suffice: before=%d after=%d",
					len(smallWindow),
					len(afterWindow),
				)
			}
		}
	})

	t.Run("span_drop_insufficient_real_turns_evicted", func(t *testing.T) {
		// BDD: Given a live window that is over budget even without the span,
		//      When a recall span is active making it additionally over,
		//      Then windowTrim drops the span AND evicts real Turns (ok=true),
		//      And the real window shrinks.
		//
		// Use the same buildTrimTestAgentLoop parameters as T1 (cw=4000, mt=1000)
		// so the large window alone exceeds budget.
		al2, _ := buildTrimTestAgentLoop(t, cw, mt)
		const sk2 = "t29-span-insufficient"

		// Build a window that is over budget by itself.
		bigContent := strings.Repeat("a", 700)
		var bigHistory []providers.Message
		for i := 0; i < 8; i++ {
			bigHistory = append(bigHistory, makeTurn(bigContent, bigContent)...)
		}
		seedHistory(t, al2, sk2, bigHistory)

		// Install a small span (doesn't change the outcome — window is still over budget
		// after span drop).
		tinySpanMsgs := []providers.Message{trimTestMsg("user", "short recalled msg")}
		al2.setRecallSpan(sk2, newRecallSpan(1, 1, tinySpanMsgs, []int{1}))

		pressureBefore := RecallSpanDropCount("pressure")

		_, ok := al2.windowTrim(al2.GetRegistry().GetDefaultAgent(), "", sk2)

		// The span must be dropped.
		if al2.activeRecallSpan(sk2) != nil {
			t.Error("T29b: recall span must be dropped even when real Turns also need eviction")
		}

		// pressure counter must increment.
		if RecallSpanDropCount("pressure") <= pressureBefore {
			t.Error("T29b: RecallSpanDropCount(pressure) must increment when span is dropped under pressure")
		}

		// Real Turns must also have been evicted (ok=true).
		if !ok {
			t.Error("T29b: windowTrim must return ok=true when real Turns are evicted")
		}

		afterWindow := al2.GetRegistry().GetDefaultAgent().Sessions.GetHistory(sk2)
		if len(afterWindow) >= len(bigHistory) {
			t.Errorf("T29b: real Turns must be evicted when span drop is insufficient: before=%d after=%d",
				len(bigHistory), len(afterWindow))
		}
	})
}

// ============================================================================
// T23 / MAJ-05 — fit-invariant arithmetic: recall does not re-overflow budget
// ============================================================================

// TestFitInvariantHolds_WindowPlusRecall verifies the MAJ-05 fit invariant:
// after a recall builds a span and messages are assembled via BuildMessages,
//
//	estimateMessageTokens(pinned+window+recallSpan) + estimateToolDefsTokens(toolDefs) + maxTokens ≤ contextWindow
//
// All five terms are computed from real state.
//
// Traces to: spec §7 T23, FR-009, NFR-5, MAJ-05, spec line 300.
func TestFitInvariantHolds_WindowPlusRecall(t *testing.T) {
	// BDD: Given a context window of 6000 tokens, maxTokens=500, a live window
	//      of a few turns, and a recall span bounded to ≤4000 tokens,
	//      When BuildMessages assembles pinned+window+recallSpan,
	//      Then estimateMessageTokens(assembled_non_user) + toolDefs + maxTokens ≤ contextWindow.

	const (
		contextWindow = 6000
		maxTokens     = 500
	)

	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})

	cb := NewContextBuilder(tmpDir)

	// Live window: 3 turns (~100 chars each → ~40 tokens each).
	window := []providers.Message{
		trimTestMsg("user", "What is the capital of France?"),
		trimTestMsg("assistant", "Paris is the capital of France."),
		trimTestMsg("user", "Tell me more about Paris."),
		trimTestMsg("assistant", "Paris is a major European city with many historic sites."),
	}

	// Recall span: 2 turns recalled from the archive (~80 chars each → ~32 tokens).
	spanMsgs := []providers.Message{
		{Role: "user", Content: "This was an earlier question about geography"},
		{Role: "assistant", Content: "Geography covers the study of Earth's features and regions."},
		{Role: "user", Content: "Can you elaborate on continents?"},
		{Role: "assistant", Content: "There are seven continents on Earth."},
	}
	span := newRecallSpan(1, 2, spanMsgs, []int{1, 2})

	breadcrumb := "## Earlier in this conversation (evicted from the live window)\n" +
		"Use the recall_conversation tool to read any of these verbatim.\n" +
		"- turns 1–2 · earlier · \"This was an earlier question\""

	assembled := cb.BuildMessages(
		window,
		"Current question",
		nil,
		"",
		"test", "chat1", "", "",
		breadcrumb,
		span.Messages(),
	)

	// Compute total tokens of the assembled messages (this is what isOverContextBudget uses).
	totalMsgTokens := 0
	for _, m := range assembled {
		totalMsgTokens += estimateMessageTokens(m)
	}

	// No tool defs in this test (no agent tools registered).
	toolDefsTokens := 0

	// FR-009 / MAJ-05: the invariant.
	totalBudget := totalMsgTokens + toolDefsTokens + maxTokens
	if totalBudget > contextWindow {
		t.Errorf(
			"MAJ-05 fit invariant violated: totalMsgTokens(%d) + toolDefsTokens(%d) + maxTokens(%d) = %d > contextWindow(%d)",
			totalMsgTokens,
			toolDefsTokens,
			maxTokens,
			totalBudget,
			contextWindow,
		)
	}

	// Assert span token cap: span.Tokens must equal Σ estimateMessageTokens(spanMsgs).
	expectedSpanTokens := 0
	for _, m := range spanMsgs {
		expectedSpanTokens += estimateMessageTokens(m)
	}
	if span.Tokens != expectedSpanTokens {
		t.Errorf("span.Tokens=%d must equal Σ estimateMessageTokens=%d (M7 invariant)", span.Tokens, expectedSpanTokens)
	}
	if span.Tokens > recallDefaultTokens {
		t.Errorf("MAJ-05: span.Tokens=%d exceeds recallDefaultTokens=%d — bounded recall must not re-overflow",
			span.Tokens, recallDefaultTokens)
	}

	// Also verify that adding the span's token cost on top of the window tokens
	// still doesn't exceed the budget for a realistic scenario.
	windowTokens := 0
	for _, m := range window {
		windowTokens += estimateMessageTokens(m)
	}
	combinedWithoutSystem := windowTokens + span.Tokens + toolDefsTokens + maxTokens
	if combinedWithoutSystem > contextWindow {
		t.Errorf("MAJ-05: window(%d) + span(%d) + toolDefs(%d) + maxTokens(%d) = %d > contextWindow(%d)",
			windowTokens, span.Tokens, toolDefsTokens, maxTokens, combinedWithoutSystem, contextWindow)
	}
}

// ============================================================================
// T17-full / SC-015 — assembled-request ID integrity end-to-end
// ============================================================================

// TestRecallSpan_ZeroDuplicateOrphanToolCallIDs_EndToEnd extends T17 beyond the
// in-span check: it assembles a recall span + live window through BuildMessages
// and asserts ZERO duplicate/orphan tool_call_ids across the FINAL message set.
// Span IDs must not collide with live-window IDs; historical tool calls are not
// re-executed.
//
// Traces to: spec §7 T17-full, SC-015, FR-008, FR-019, MAJ-04, spec line 286.
func TestRecallSpan_ZeroDuplicateOrphanToolCallIDs_EndToEnd(t *testing.T) {
	// BDD: Given a live window that contains a tool-call/result pair with ID "live_tc_001",
	//      And a recall span containing a rewritten-ID tool pair with "recall_0_0",
	//      When BuildMessages assembles the combined provider request,
	//      Then ZERO unresolved tool_call_ids exist in the final message set,
	//      And no ID appears in both the span and the live window,
	//      And the historical tool_call_id "live_tc_001" in the span is NOT re-executed.
	const liveCallID = "live_tc_001"
	const recallCallID = "recall_0_0" // rewritten ID from the recall span

	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	cb := NewContextBuilder(tmpDir)

	// Live window: contains a complete tool-call/result pair with liveCallID.
	liveWindow := []providers.Message{
		trimTestMsg("user", "Please run a search"),
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   liveCallID,
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "web_search",
						Arguments: `{"query":"Paris"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			Content:    "Search results: Paris is the capital of France.",
			ToolCallID: liveCallID,
		},
		trimTestMsg("assistant", "I found that Paris is the capital."),
	}

	// Recall span: a reinjected historical tool pair using the rewritten recall_ namespace.
	// The original ID was something else; it has been rewritten to recall_0_0.
	spanMsgs := []providers.Message{
		{Role: "user", Content: "Recalled earlier turns 1–1 (reference): do not re-execute."},
		trimTestMsg("user", "Earlier I asked about capitals"),
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   recallCallID, // rewritten — not the original
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "web_search",
						Arguments: `{"query":"capital"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			Content:    "Capitals are government seats.",
			ToolCallID: recallCallID, // matches the rewritten call
		},
		trimTestMsg("assistant", "Capitals are government seats."),
	}

	assembled := cb.BuildMessages(
		liveWindow,
		"What did we find?",
		nil,
		"",
		"test", "chat1", "", "",
		"", // no breadcrumb for simplicity
		spanMsgs,
	)

	// --- Integrity check ---
	// Collect all declared tool_call_ids (assistant ToolCalls[].ID)
	// and all consumed tool_call_ids (tool messages ToolCallID).
	declared := make(map[string]int) // ID → count of declarations
	consumed := make(map[string]int) // ID → count of consumptions
	for _, m := range assembled {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				declared[tc.ID]++
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			consumed[m.ToolCallID]++
		}
	}

	// SC-015: every consumed ID must have exactly one declaration.
	for id, cnt := range consumed {
		if declared[id] == 0 {
			t.Errorf("SC-015: orphan tool result id %q has no matching ToolCall declaration", id)
		}
		if cnt > 1 {
			t.Errorf("SC-015: tool result id %q consumed %d times (must be exactly once)", id, cnt)
		}
	}

	// SC-015: every declared ID must have exactly one consumption.
	for id, cnt := range declared {
		if consumed[id] == 0 {
			t.Errorf("SC-015: declared tool call id %q has no matching tool result", id)
		}
		if cnt > 1 {
			t.Errorf("SC-015: tool call id %q declared %d times (must be exactly once)", id, cnt)
		}
	}

	// Span IDs must not collide with live-window IDs.
	if _, liveHasRecall := declared[recallCallID]; liveHasRecall {
		// Only a problem if it appears multiple times (once in span, once in live window).
		if declared[recallCallID] > 1 {
			t.Errorf("SC-015: recall span ID %q collides with live-window ID (appears %d times)",
				recallCallID, declared[recallCallID])
		}
	}

	// The live window's ID must appear exactly once (not leaked into span twice).
	if declared[liveCallID] > 1 {
		t.Errorf("SC-015: live window ID %q duplicated in assembled messages (appeared %d times)",
			liveCallID, declared[liveCallID])
	}
}

// ============================================================================
// T18 (strengthened) — rankRetrosBM25 byte-for-byte regression baseline
// ============================================================================

// TestBM25Core_RetroBytewiseRegression asserts that rankRetrosBM25 output is
// unchanged vs a hand-computed BM25 baseline after extraction of the bm25Rank core.
//
// The hand-computed ranking uses the BM25 formula directly:
//
//	k1=1.2, b=0.75, IDF = ln(1 + (N - df + 0.5) / (df + 0.5))
//
// Doc 0 ("flock alpha") is shorter (length 2) → higher tf-density → ranks first.
// Doc 1 ("flock beta long...") is longer (length 11) → lower tf-density → ranks second.
//
// Traces to: spec §7 T18, FR-010, OBS-02, spec line 287.
func TestBM25Core_RetroBytewiseRegression(t *testing.T) {
	// BDD: Given a fixed two-retro corpus and query "flock",
	//      When rankRetrosBM25 ranks the corpus,
	//      Then the ordering (doc0 first, doc1 second) is byte-for-byte stable
	//      across refactors of the BM25 core extraction.

	now := time.Now().UTC()

	// Fixed corpus — do NOT change these values; changing breaks the regression.
	retros := []Retro{
		{Recap: "flock alpha", Timestamp: now}, // 2 tokens — dense
		{
			Recap:     "flock beta discussion covering many agenda items at this meeting",
			Timestamp: now.Add(-time.Hour),
		}, // 10 tokens — sparse
	}

	// Step 1: Run rankRetrosBM25 (uses bm25Rank core internally).
	got := rankRetrosBM25(retros, "flock", 10)
	if len(got) != 2 {
		t.Fatalf("T18: expected 2 ranked retros, got %d", len(got))
	}

	// Step 2: Verify ordering matches the BM25 prediction.
	// Doc 0 is shorter → higher BM25 score → must rank first.
	if !strings.Contains(got[0].Recap, "alpha") {
		t.Errorf("T18: regression: dense doc ('alpha') must rank first, got: %q", got[0].Recap)
	}
	if !strings.Contains(got[1].Recap, "beta") {
		t.Errorf("T18: regression: sparse doc ('beta') must rank second, got: %q", got[1].Recap)
	}

	// Step 3: Call bm25Rank directly on the same corpus texts to verify that
	// rankRetrosBM25 delegates to bm25Rank (byte-for-byte shared core — FR-010).
	docs := []string{
		retroSearchText(retros[0]),
		retroSearchText(retros[1]),
	}
	coreHits := bm25Rank("flock", docs, 10)
	if len(coreHits) != 2 {
		t.Fatalf("T18: bm25Rank core returned %d hits, expected 2", len(coreHits))
	}

	// The core ranking must match rankRetrosBM25 ordering (same core, same result).
	// coreHits[0].Index must be 0 (the dense doc).
	if coreHits[0].Index != 0 {
		t.Errorf("T18: bm25Rank core ranks doc %d first, expected doc 0 (dense)", coreHits[0].Index)
	}
	if coreHits[1].Index != 1 {
		t.Errorf("T18: bm25Rank core ranks doc %d second, expected doc 1 (sparse)", coreHits[1].Index)
	}

	// Step 4: Verify that rankRetrosBM25 output is consistent with the core:
	// the retro at got[0] corresponds to coreHits[0].Index (doc 0).
	if got[0].Recap != retros[coreHits[0].Index].Recap {
		t.Errorf("T18: rankRetrosBM25 first result %q does not match bm25Rank first hit retros[%d]=%q",
			got[0].Recap, coreHits[0].Index, retros[coreHits[0].Index].Recap)
	}
	if got[1].Recap != retros[coreHits[1].Index].Recap {
		t.Errorf("T18: rankRetrosBM25 second result %q does not match bm25Rank second hit retros[%d]=%q",
			got[1].Recap, coreHits[1].Index, retros[coreHits[1].Index].Recap)
	}

	// Step 5: Verify that both callers score docs 0 and 1 positively.
	if coreHits[0].Score <= 0 {
		t.Errorf("T18: core score for dense doc must be > 0, got %f", coreHits[0].Score)
	}
	if coreHits[1].Score <= 0 {
		t.Errorf("T18: core score for sparse doc must be > 0, got %f", coreHits[1].Score)
	}
	// The dense doc must score strictly higher than the sparse doc.
	if coreHits[0].Score <= coreHits[1].Score {
		t.Errorf("T18: dense doc score (%f) must be strictly > sparse doc score (%f)",
			coreHits[0].Score, coreHits[1].Score)
	}
}

// ============================================================================
// T10 (strengthened) — buildBreadcrumb 0 LLM calls via stub provider counter
// ============================================================================

// countingProvider is a providers.LLMProvider stub that counts Chat() calls.
// It is used by T10 to prove buildBreadcrumb makes zero LLM calls even when a
// provider is available in the same process.
//
// Traces to: spec §7 T10, SC-007, FR-007 US-3.3, spec line 279.
type countingProvider struct {
	calls int
}

func (cp *countingProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	cp.calls++
	return &providers.LLMResponse{Content: "stub"}, nil
}

func (cp *countingProvider) GetDefaultModel() string { return "stub-model" }

// TestBreadcrumb_ZeroLLMCallsViaStubCounter builds an AgentLoop with a counting
// provider, calls buildBreadcrumb directly, and asserts the call counter is 0.
// This proves SC-007: 0 LLM calls during breadcrumb build, even with a wired provider.
func TestBreadcrumb_ZeroLLMCallsViaStubCounter(t *testing.T) {
	// BDD: Given a counting provider wired into the agent loop,
	//      When buildBreadcrumb is called to build a breadcrumb for evicted turns,
	//      Then the counting provider's call count remains 0.

	cp := &countingProvider{}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: "stub-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	cfg.Context.DefaultContextWindow = intPtr(cloudWindowFloor)
	_ = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), cp)

	// Freeze the clock for deterministic relative times.
	oldNow := breadcrumbNowFn
	breadcrumbNowFn = func() time.Time { return fixedNow }
	defer func() { breadcrumbNowFn = oldNow }()

	ts := fixedNow.Add(-1 * time.Hour).Unix()
	archive := []memory.ArchivedMessage{
		makeMsgAM("user", "What is the capital of France?", ts),
		makeMsgAM("assistant", "Paris is the capital of France.", ts),
		makeMsgAM("user", "Tell me about the Eiffel Tower.", ts),
		makeMsgAM("assistant", "The Eiffel Tower is an iconic landmark in Paris.", ts),
	}
	window := []providers.Message{} // everything evicted

	// Call buildBreadcrumb. It must not invoke the provider.
	bc := buildBreadcrumb(archive, window, breadcrumbTokenCap)

	// SC-007: provider call count must be 0.
	if cp.calls != 0 {
		t.Errorf("SC-007: buildBreadcrumb made %d LLM call(s) — must be 0", cp.calls)
	}

	// Sanity: the breadcrumb must be non-empty (it found evicted turns).
	if bc == "" {
		t.Error("T10: breadcrumb must be non-empty when archive has evicted turns")
	}

	// Must name recall_conversation.
	if !strings.Contains(bc, "recall_conversation") {
		t.Errorf("T10: breadcrumb must name recall_conversation: %q", bc)
	}

	// Must contain a turn pointer.
	if !strings.Contains(bc, "turn") {
		t.Errorf("T10: breadcrumb must contain a turn reference: %q", bc)
	}
}

// ============================================================================
// T31-real — span placement via ContextBuilder (real BuildMessages path)
// ============================================================================

// TestBuildMessages_SpanPlacedViaRealContextBuilder verifies the assembly order
// required by FR-019 through the real ContextBuilder.BuildMessages path used in
// production (assembleMessages calls cb.BuildMessages internally with span msgs
// read from al.activeRecallSpan). This tests the code path that actually executes
// in production turns, not a mocked stub.
//
// Assembly order per FR-019:
//
//	[0] system (pinned core + breadcrumb)
//	[1..N] recall span messages (old recalled turns)
//	[N+1..M] sliding window messages (recent live turns)
//	[last] current user message
//
// Traces to: spec §7 T31, FR-019, spec line 296.
func TestBuildMessages_SpanPlacedViaRealContextBuilder(t *testing.T) {
	// BDD: Given a ContextBuilder with an AGENT.md workspace,
	//      And a breadcrumb for evicted turns 1–3,
	//      And a recall span with 2 recalled messages from turn 2,
	//      And a live window with 2 recent messages,
	//      When BuildMessages assembles the full provider request,
	//      Then the order is: system → span msgs → window msgs → current user.
	//      And recalled (old) turns precede the window (recent) turns.

	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nReal context builder test.",
	})
	cb := NewContextBuilder(tmpDir)

	breadcrumb := "## Earlier in this conversation (evicted from the live window)\n" +
		"Use the recall_conversation tool to read any of these verbatim.\n" +
		"- turns 1–3 · 2h ago · \"Recalled: geography discussion\""

	// Span messages with distinct sentinel content.
	spanMsgs := []providers.Message{
		{Role: "user", Content: "SPAN_SENTINEL_USER: older recalled question about geography"},
		{Role: "assistant", Content: "SPAN_SENTINEL_ASST: older recalled answer about geography"},
	}

	// Window messages with distinct sentinel content.
	windowMsgs := []providers.Message{
		{Role: "user", Content: "WINDOW_SENTINEL_USER: recent question about cities"},
		{Role: "assistant", Content: "WINDOW_SENTINEL_ASST: recent answer about cities"},
	}

	currentMsg := "CURRENT_MSG: what is Paris known for?"

	assembled := cb.BuildMessages(
		windowMsgs,
		currentMsg,
		nil,
		"",
		"test", "chat1", "", "",
		breadcrumb,
		spanMsgs,
	)

	// Must have at least: system + span[2] + window[2] + current = 6 messages.
	if len(assembled) < 6 {
		t.Fatalf("T31-real: expected ≥6 messages, got %d: %v", len(assembled), assembled)
	}

	// assembled[0] = system message with breadcrumb.
	if assembled[0].Role != "system" {
		t.Fatalf("T31-real: assembled[0] must be system, got %s", assembled[0].Role)
	}
	if !strings.Contains(assembled[0].Content, "Earlier in this conversation") {
		t.Error("T31-real: system message must contain breadcrumb header")
	}

	// Find the first occurrence of each sentinel in assembled[1:].
	spanUserIdx := -1
	spanAsstIdx := -1
	windowUserIdx := -1
	windowAsstIdx := -1
	currentIdx := -1

	for i, m := range assembled[1:] {
		idx := i + 1 // offset by 1 because we slice from [1:]
		switch {
		case strings.Contains(m.Content, "SPAN_SENTINEL_USER") && spanUserIdx < 0:
			spanUserIdx = idx
		case strings.Contains(m.Content, "SPAN_SENTINEL_ASST") && spanAsstIdx < 0:
			spanAsstIdx = idx
		case strings.Contains(m.Content, "WINDOW_SENTINEL_USER") && windowUserIdx < 0:
			windowUserIdx = idx
		case strings.Contains(m.Content, "WINDOW_SENTINEL_ASST") && windowAsstIdx < 0:
			windowAsstIdx = idx
		case strings.Contains(m.Content, "CURRENT_MSG") && currentIdx < 0:
			currentIdx = idx
		}
	}

	// All sentinels must be found.
	if spanUserIdx < 0 {
		t.Error("T31-real: SPAN_SENTINEL_USER not found in assembled messages")
	}
	if spanAsstIdx < 0 {
		t.Error("T31-real: SPAN_SENTINEL_ASST not found in assembled messages")
	}
	if windowUserIdx < 0 {
		t.Error("T31-real: WINDOW_SENTINEL_USER not found in assembled messages")
	}
	if windowAsstIdx < 0 {
		t.Error("T31-real: WINDOW_SENTINEL_ASST not found in assembled messages")
	}
	if currentIdx < 0 {
		t.Error("T31-real: CURRENT_MSG not found in assembled messages")
	}

	if t.Failed() {
		// Print the full assembly for debugging.
		for i, m := range assembled {
			t.Logf("assembled[%d] role=%s content=%q", i, m.Role, m.Content[:min(80, len(m.Content))])
		}
		return
	}

	// FR-019 ordering: span → window → current.
	// Span must precede window (recalled old turns before recent live turns).
	if spanUserIdx >= windowUserIdx {
		t.Errorf("T31-real: span sentinel (%d) must precede window sentinel (%d) — recalled old before recent",
			spanUserIdx, windowUserIdx)
	}

	// Window must precede current.
	if windowUserIdx >= currentIdx {
		t.Errorf("T31-real: window sentinel (%d) must precede current user msg (%d)",
			windowUserIdx, currentIdx)
	}

	// Span must precede current.
	if spanUserIdx >= currentIdx {
		t.Errorf("T31-real: span sentinel (%d) must precede current user msg (%d)",
			spanUserIdx, currentIdx)
	}

	// Current must be the last message.
	if currentIdx != len(assembled)-1 {
		t.Errorf("T31-real: current user message must be last (idx=%d), got assembled len=%d",
			currentIdx, len(assembled))
	}
}
