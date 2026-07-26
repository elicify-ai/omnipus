// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FIX 4 regression coverage: DelegateTool.executeAsync (pkg/tools/delegate.go)
// launches the child sub-turn in a goroutine and returns to the parent
// immediately, while the PARENT writes this SAME tool call's own placeholder
// ack record only after further processing (hooks, media, events) back in
// its own call stack (pkg/agent/loop.go). When the child's dispatch fails
// fast, spawnSubTurn's cleanup defer (pkg/agent/subturn.go) can reach
// UpdateToolCallStatus BEFORE the parent's placeholder write lands —
// confirmed independently by silent-failure-hunter, architect, and
// code-reviewer. updateToolCallStatusWithRetry closes this race with a
// short, bounded retry for the async case.

package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestUpdateToolCallStatusWithRetry_AsyncWaitsForDelayedPlaceholder is the
// dedicated, deterministic concurrency regression test for the race:
// REAL goroutines race — a writer goroutine that deliberately delays before
// appending the placeholder record (simulating the parent's slower
// hooks/media/event processing) against updateToolCallStatusWithRetry called
// with async=true BEFORE that write lands. The retry must find the record
// once the writer catches up, closing the race window instead of leaving the
// placeholder permanent.
//
// Negative-test discipline: this test was confirmed to FAIL against the
// pre-fix code (a single, non-retrying UpdateToolCallStatus call) before the
// fix was applied — see the delivery report.
func TestUpdateToolCallStatusWithRetry_AsyncWaitsForDelayedPlaceholder(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	const callID = session.ToolCallID("c-race")

	// The writer goroutine simulates the PARENT: it does NOT write the
	// placeholder ack record until AFTER a short delay (standing in for the
	// parent's hooks/media/event processing between ExecuteWithContext
	// returning and ts.appendToolCallTranscript actually persisting it).
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(60 * time.Millisecond)
		require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
			ID:      "c-race",
			Type:    session.EntryTypeToolCall,
			AgentID: "jim",
			ToolCalls: []session.ToolCall{
				{ID: callID, Tool: "delegate", Status: "success", DurationMS: 0},
			},
		}))
	}()

	// The CHILD's cleanup defer calls this immediately — racing ahead of the
	// writer goroutine above, exactly as a fast-failing dispatch would.
	found, updateErr := updateToolCallStatusWithRetry(store, sessionID, callID, "error", 42, true, nil)
	require.NoError(t, updateErr)
	assert.True(t, found,
		"async retry must wait out the race and find the placeholder once the parent's "+
			"delayed write lands — a single non-retrying attempt would race ahead and miss it")

	wg.Wait()

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].ToolCalls, 1)
	assert.Equal(t, "error", entries[0].ToolCalls[0].Status,
		"the retried update must have actually corrected the record")
	assert.EqualValues(t, 42, entries[0].ToolCalls[0].DurationMS)
}

// TestUpdateToolCallStatusWithRetry_SyncDoesNotWaitForDelayedRecord proves
// the complementary invariant: for SYNCHRONOUS delegation (async=false), a
// delayed write must NOT be waited for — found=false on the very first
// attempt is the permanent, expected outcome (the caller writes the record
// itself moments later via the normal tool-completion path), and retrying
// would only waste the backoff budget on every synchronous delegation call
// for no benefit.
func TestUpdateToolCallStatusWithRetry_SyncDoesNotWaitForDelayedRecord(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	const callID = session.ToolCallID("c-sync")

	// Baseline: bare NOT-FOUND store lookups (the exact path this test's single
	// attempt takes), max of 3 samples, so the threshold tracks this machine's
	// current cost instead of a fixed number. See the sibling test below for why
	// a hardcoded `elapsed < 10*time.Millisecond` was wrong here: 10ms IS
	// updateToolCallStatusRetryDelays[0], so it had zero margin and could not
	// distinguish "one slow attempt under CI load" from "one retry".
	var baseline time.Duration
	for i := 0; i < 3; i++ {
		bStart := time.Now()
		_, bErr := store.UpdateToolCallStatusAndResult(sessionID, callID, "error", 42, nil)
		if sample := time.Since(bStart); sample > baseline {
			baseline = sample
		}
		require.NoError(t, bErr)
	}

	start := time.Now()
	found, updateErr := updateToolCallStatusWithRetry(store, sessionID, callID, "error", 42, false, nil)
	elapsed := time.Since(start)

	require.NoError(t, updateErr)
	assert.False(t, found, "sync delegation's first-attempt not-found must remain terminal")
	// One retry would add at least updateToolCallStatusRetryDelays[0] of sleep on
	// top of a second lookup, so at or below (baseline + first delay) provably
	// means exactly one attempt was made.
	assert.Less(t, elapsed, baseline+updateToolCallStatusRetryDelays[0],
		"sync delegation must make exactly one attempt — no retry backoff — since a delayed "+
			"write is never coming for this call site (baseline=%v, first retry delay=%v)",
		baseline, updateToolCallStatusRetryDelays[0])
}

// TestUpdateToolCallStatusWithRetry_FoundOnFirstAttemptSkipsRetry verifies
// the common case (record already present) resolves immediately without any
// retry delay, for both async and sync.
func TestUpdateToolCallStatusWithRetry_FoundOnFirstAttemptSkipsRetry(t *testing.T) {
	for _, async := range []bool{true, false} {
		store, err := session.NewUnifiedStore(t.TempDir())
		require.NoError(t, err)
		meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
		require.NoError(t, err)
		sessionID := meta.ID

		const callID = session.ToolCallID("c-immediate")
		require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
			ID:      "c-immediate",
			Type:    session.EntryTypeToolCall,
			AgentID: "jim",
			ToolCalls: []session.ToolCall{
				{ID: callID, Tool: "delegate", Status: "success", DurationMS: 0},
			},
		}))

		// Baseline: time BARE store updates on the same already-present record
		// (no retry wrapper, so structurally zero retries). Take the max of a
		// few samples so the threshold absorbs whatever this machine's disk and
		// scheduler cost happens to be right now.
		//
		// WHY NOT A FIXED THRESHOLD: this assertion used to be
		// `elapsed < 10*time.Millisecond`, which is EXACTLY
		// updateToolCallStatusRetryDelays[0]. That gave it zero margin — it
		// could not distinguish "no retry on a loaded machine" from "one
		// retry", so it failed under parallel CI load and was absorbed by the
		// flake filter twice (2026-07-26) while asserting nothing reliable.
		var baseline time.Duration
		for i := 0; i < 3; i++ {
			bStart := time.Now()
			_, bErr := store.UpdateToolCallStatusAndResult(sessionID, callID, "success", 0, nil)
			if sample := time.Since(bStart); sample > baseline {
				baseline = sample
			}
			require.NoError(t, bErr)
		}

		start := time.Now()
		found, updateErr := updateToolCallStatusWithRetry(store, sessionID, callID, "success", 100, async, nil)
		elapsed := time.Since(start)

		require.NoError(t, updateErr)
		assert.True(t, found)
		// A single retry costs at least updateToolCallStatusRetryDelays[0] of
		// sleep ON TOP of a second store call, so anything at or below
		// (baseline + first delay) provably took the no-retry path.
		assert.Less(t, elapsed, baseline+updateToolCallStatusRetryDelays[0],
			"an already-present record must resolve on the first attempt, no retry delay "+
				"(async=%v, baseline=%v, first retry delay=%v)",
			async, baseline, updateToolCallStatusRetryDelays[0])
	}
}
