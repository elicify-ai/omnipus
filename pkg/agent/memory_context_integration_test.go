// memory_context_integration_test.go — integration tests for memory-context wiring gaps.
//
// These tests use a real AgentLoop / MemoryStore with a stub provider (mockProvider).
// They close three specific coverage gaps that were only partially verified elsewhere:
//
//  1. AutoInject20Recent — GetMemoryContext injects EXACTLY 20 of 25 written memories.
//  2. RecallSpansThreeScopes — recall_memory search + GetMemoryContext coverage of
//     long-term memory, last-session.md, and retrospectives.
//  3. ContextWindowTrimOverflow — windowTrim fires and produces a well-formed
//     (non-corrupt) reduced history without writing a compression summary.
//
// Build: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestIntegration_' -p 1 ./pkg/agent/
// Traces to: pkg/agent/memory.go GetMemoryContext (line 936), pkg/agent/loop.go windowTrim.

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// 1. TestIntegration_AutoInject20Recent
//
// BDD:
//   Given a MemoryStore with 25 written long-term memories
//   When GetMemoryContext is called (which is what BuildSystemPrompt injects)
//   Then exactly 20 memories appear, not all 25 and not fewer
//
// The "20" cap is the SearchEntries("", 20) call at memory.go:936.
// Existing tests (TestMemoryStore_GetMemoryContext_ContainsBothSections) only
// verify section presence — NOT the 20-memory count boundary.
//
// Note on ordering: SearchEntries("", 20) uses BM25 score ordering (all-zero
// scores with empty query fall back to mtime ordering when the filesystem
// provides distinct mtimes). This test asserts the COUNT boundary only, not
// which specific 5 are excluded, since mtime granularity on fast filesystems
// can collapse multiple writes into the same second.
//
// Traces to: pkg/agent/memory.go line 936 (GetMemoryContext → SearchEntries("", 20))
// ---------------------------------------------------------------------------

func TestIntegration_AutoInject20Recent(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	ms := NewMemoryStore(workspace, home)
	defer ms.Close()

	// Write 25 memories with distinct, identifiable prefix so we can count them.
	const total = 25
	for i := 1; i <= total; i++ {
		// Format: "ctxIT_mem_NN_unique_fact" — zero-padded so string search is unambiguous.
		content := "ctxIT_mem_" + fmt.Sprintf("%02d", i) + "_unique_fact"
		if err := ms.AppendLongTerm(content, "reference"); err != nil {
			t.Fatalf("AppendLongTerm(%d): %v", i, err)
		}
	}

	// GetMemoryContext injects at most 20 entries via SearchEntries("", 20).
	ctx := ms.GetMemoryContext()

	if !strings.Contains(ctx, "## Long-term memory") {
		t.Fatal("GetMemoryContext missing '## Long-term memory' section with 25 memories written")
	}

	// Count how many ctxIT_mem_ markers appear in the output.
	// Each memory contributes exactly one "ctxIT_mem_NN_unique_fact" substring.
	count := strings.Count(ctx, "ctxIT_mem_")
	assert.Equal(t, 20, count,
		"GetMemoryContext must inject exactly 20 memories (SearchEntries cap = 20), not %d; "+
			"writing 25 must trigger the cap and exclude 5", count)

	// Differentiation guard: the injected window must hold 20 DISTINCT written
	// memories (not one repeated, not hardcoded/empty). We deliberately do NOT
	// assert WHICH 20 of the 25 appear: all 25 are written within a single
	// filesystem mtime tick, so the recency ordering (SearchEntries mtime
	// fallback for an empty query — see the note above) ties and the excluded 5
	// are non-deterministic. Asserting a specific id (e.g. the oldest mem_01, or
	// the newest mem_25) was a flaky bug — it passed only when the mtime
	// collision happened to include that id. In production, memories are created
	// over real time (distinct mtimes), so this collision is a test-only artifact.
	distinct := 0
	for i := 1; i <= total; i++ {
		if strings.Contains(ctx, "ctxIT_mem_"+fmt.Sprintf("%02d", i)+"_unique_fact") {
			distinct++
		}
	}
	assert.Equal(t, 20, distinct,
		"the injected window must contain exactly 20 DISTINCT written memories, got %d", distinct)
}

// ---------------------------------------------------------------------------
// 2. TestIntegration_RecallSpansThreeScopes
//
// BDD:
//   Given a MemoryStore with:
//     - a long-term memory containing "ctxIT_topic_flock"
//     - a last-session.md containing "ctxIT_topic_flock"
//     - a retrospective containing "ctxIT_topic_flock"
//   When recall_memory is called for query "ctxIT_topic_flock"
//   Then long-term memory results are returned (via SearchEntries)
//   AND matching retrospectives are ALSO returned (finding #1 fix: recall spans
//     long-term + retros via the tools.RetroSearcher path)
//   AND when GetMemoryContext is called, long-term + last-session content appears
//   AND retrospectives do NOT appear in GetMemoryContext (they stay out of the
//     per-turn auto-inject; they are recall-only, surfaced on demand).
//
// This locks in the finding #1 fix: recall_memory now searches retrospectives
// (RetroSearcher on the adapter), so the tool matches its "spans long-term +
// retrospectives" contract. last-session is auto-injected each turn, so it is
// always in context rather than searched by recall.
//
// Traces to: pkg/tools/memory.go (RecallMemoryTool + RetroSearcher)
//            pkg/agent/memory_adapter.go (MemoryStoreAdapter.SearchRetros)
//            pkg/agent/memory.go (GetMemoryContext — long-term + last-session)
// ---------------------------------------------------------------------------

func TestIntegration_RecallSpansThreeScopes(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	ms := NewMemoryStore(workspace, home)
	defer ms.Close()

	adapter := NewMemoryStoreAdapter(ms)

	const needle = "ctxIT_topic_flock"

	// 1. Seed a long-term memory.
	if err := ms.AppendLongTerm(needle+" is the canonical concurrency primitive", "lesson_learned"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	// 2. Seed a last-session.md.
	if err := ms.WriteLastSession("## Last session\nWe applied " + needle + " everywhere."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}

	// 3. Seed a retrospective.
	sessionID := "ctxIT-session-001"
	retro := Retro{
		Timestamp:        time.Now().UTC(),
		Trigger:          TriggerJoined,
		WentWell:         []string{needle + " adoption succeeded"},
		NeedsImprovement: []string{"document " + needle + " rationale"},
	}
	if err := ms.AppendRetro(sessionID, retro); err != nil {
		t.Fatalf("AppendRetro: %v", err)
	}

	// -- Section A: recall_memory tool covers long-term memories ---------------
	recallTool := tools.NewRecallMemoryTool(adapter)
	recallResult := recallTool.Execute(context.Background(), map[string]any{
		"query": needle,
	})
	require.NotNil(t, recallResult, "recall_memory must not return nil")
	require.False(t, recallResult.IsError, "recall_memory returned error: %s", recallResult.ForLLM)

	// Long-term memory hit is present.
	assert.Contains(t, recallResult.ForLLM, needle,
		"recall_memory must surface the long-term memory entry matching the query")

	// recall_memory now ALSO spans retrospectives (finding #1 fix): the seeded
	// retro's content matches the query, so recall surfaces it alongside the
	// long-term hit, labeled as a retrospective.
	assert.Contains(t, recallResult.ForLLM, "Went well",
		"recall_memory must surface matching retrospective content (recall spans retros)")
	assert.Contains(t, recallResult.ForLLM, "retrospective",
		"recall_memory retro hits are labeled retrospective")

	// Differentiation: a different query must NOT find it.
	recallResultOther := recallTool.Execute(context.Background(), map[string]any{
		"query": "completely_unrelated_xyz987",
	})
	require.NotNil(t, recallResultOther)
	assert.NotContains(t, recallResultOther.ForLLM, needle,
		"recall_memory must not return memories for an unrelated query (differentiation test)")

	// -- Section B: GetMemoryContext covers long-term + last-session -----------
	memCtx := ms.GetMemoryContext()

	assert.Contains(t, memCtx, "## Last Session",
		"GetMemoryContext must include the ## Last Session section")
	assert.Contains(t, memCtx, needle,
		"GetMemoryContext must surface needle from both last-session and long-term memory")
	assert.Contains(t, memCtx, "## Long-term memory",
		"GetMemoryContext must include the ## Long-term memory section")

	// -- Section C: retros are recall-only, not in the per-turn auto-inject ------
	// After the finding #1 fix, recall_memory spans long-term + retrospectives
	// (asserted in Section A). The per-turn GetMemoryContext auto-inject still
	// covers long-term + last-session only — retros stay out of the always-on
	// context and are surfaced on demand via recall_memory / ReadRetros.
	assert.NotContains(t, memCtx, "ctxIT-session-001",
		"GetMemoryContext (auto-inject) does NOT include retrospective content — retros are recall-only")

	// ReadRetros DOES surface the retro (verifying it was written correctly).
	retros, err := ms.ReadRetros(1)
	require.NoError(t, err, "ReadRetros must not error")
	require.NotEmpty(t, retros, "ReadRetros must find the written retrospective")
	retroFound := false
	for _, r := range retros {
		for _, item := range r.WentWell {
			if strings.Contains(item, needle) {
				retroFound = true
				break
			}
		}
	}
	assert.True(
		t,
		retroFound,
		"ReadRetros must surface the retrospective content — it was written correctly but is not exposed via recall_memory or GetMemoryContext",
	)
}

// ---------------------------------------------------------------------------
// 3. TestIntegration_ContextCompactionOverflow
//
// BDD:
//   Given an AgentInstance with a small ContextWindow (e.g. 1000 tokens)
//   And a session history that exceeds the window (many large messages)
//   When windowTrim is called
//   Then:
//     - trim fires (returns ok=true)
//     - the resulting history has fewer messages (oldest Turns evicted to budget)
//     - the message sequence is well-formed (no orphaned tool_result, no empty roles)
//     - NO compression note is stored in the session summary (FR-004)
//     - a distinctive early user fact is NOT in the remaining history (it was evicted)
//     - the most-recent user message IS in the remaining history (kept)
//
// Traces to: pkg/agent/loop.go windowTrim (FR-001/002/003/004)
// ---------------------------------------------------------------------------

func TestIntegration_ContextCompactionOverflow(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al and cleanup are needed for this test
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent, "default agent must exist after NewAgentLoop")

	// Set a small context window to make windowTrim trigger reliably.
	// budget = 1000 - 512 - ceil(0.05*1000) = 1000 - 512 - 50 = 438 tokens
	agent.ContextWindow = 1000
	agent.MaxTokens = 512

	const sessionKey = "ctxIT-compaction-session"

	// Build a history large enough to exceed the window.
	// We use 8 messages (4 turns): user/assistant pairs.
	// The earliest user message has a distinctive "ctxIT_early_fact" marker.
	// The latest assistant message has a "ctxIT_recent_question" marker.
	history := []providers.Message{
		{Role: "user", Content: "ctxIT_early_fact: we decided to use flock for all writes"},
		{Role: "assistant", Content: strings.Repeat("acknowledged, I will use flock. ", 20)},
		{Role: "user", Content: strings.Repeat("next step: configure the sandbox. ", 20)},
		{Role: "assistant", Content: strings.Repeat("sandbox configured successfully. ", 20)},
		{Role: "user", Content: strings.Repeat("now run the test suite. ", 20)},
		{Role: "assistant", Content: strings.Repeat("test suite passed, all green. ", 20)},
		{Role: "user", Content: strings.Repeat("apply the landlock policy. ", 20)},
		{Role: "assistant", Content: "ctxIT_recent_question: landlock policy applied, any final steps?"},
	}

	agent.Sessions.SetHistory(sessionKey, history)
	require.NoError(t, agent.Sessions.Save(sessionKey))

	beforeCount := len(history)

	// -- Act: invoke windowTrim directly --------------------------------------
	result, ok := al.windowTrim(agent, "", sessionKey)

	// -- Assert: trim fired (ok=true) ----------------------------------------
	require.True(t, ok,
		"windowTrim must return ok=true for a history with %d messages (> 2)", beforeCount)

	assert.Greater(t, result.DroppedMessages, 0,
		"DroppedMessages must be > 0 after trim")
	assert.Greater(t, result.RemainingMessages, 0,
		"RemainingMessages must be > 0 after trim; must not drop everything")
	assert.Equal(t, beforeCount, result.DroppedMessages+result.RemainingMessages,
		"DroppedMessages + RemainingMessages must equal original count")

	// -- Assert: resulting history is well-formed -----------------------------
	remaining := agent.Sessions.GetHistory(sessionKey)
	require.Equal(t, result.RemainingMessages, len(remaining),
		"Sessions.GetHistory must reflect the trimmed count")

	for i, msg := range remaining {
		assert.NotEmpty(t, msg.Role,
			"message[%d] has empty role — history is corrupt after trim", i)
		assert.True(t, msg.Role == "user" || msg.Role == "assistant" || msg.Role == "tool",
			"message[%d] has unexpected role %q", i, msg.Role)
	}

	// No orphaned tool_result: check the simpler invariant.
	for i, msg := range remaining {
		if msg.Role == "tool" {
			assert.Greater(t, i, 0,
				"tool_result at index 0 is orphaned — no preceding assistant message")
			if i > 0 {
				assert.Equal(t, "assistant", remaining[i-1].Role,
					"tool_result at index %d must be immediately preceded by an assistant message", i)
			}
		}
	}

	// -- Assert: earliest fact was dropped, most-recent kept ------------------
	allRemaining := strings.Join(func() []string {
		out := make([]string, len(remaining))
		for i, m := range remaining {
			out[i] = m.Content
		}
		return out
	}(), " ")

	assert.NotContains(t, allRemaining, "ctxIT_early_fact",
		"oldest user message (ctxIT_early_fact) must have been evicted by windowTrim")

	// With a very small context window (cw=1000, mt=512) the tool-definition
	// tokens alone (~5876) exceed the history budget, so windowTrim always hits
	// the FR-003 emergency floor: only the last USER message is retained (the
	// assistant response is also dropped because even the bare user message may
	// exceed budget when tool defs are included).  The key invariant is that the
	// most-recent USER content survives and the early fact is gone.
	assert.Contains(t, allRemaining, "apply the landlock policy",
		"most-recent user message content must survive trim (FR-003 floor keeps last user msg)")

	// -- Assert: NO compression marker (FR-004) -------------------------------
	// windowTrim MUST NOT inject an "Emergency compression dropped" marker.
	assert.NotContains(t, allRemaining, "Emergency compression dropped",
		"windowTrim MUST NOT inject a compression marker (FR-004)")
}
