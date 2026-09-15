// subturn_result_test.go: tests for collect, format and return a sub-turn's result to the parent.

package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from subturn.go tests 2026-09-15 ---

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
//
// The REAL property under test is behavioural, not temporal: when the
// record is already present, updateToolCallStatusWithRetry must resolve on
// the FIRST lookup and must never enter its retry/backoff loop at all — i.e.
// zero calls to the sleep primitive. "How long did it take?" was only ever a
// proxy for "did it sleep through a retry backoff?", and a proxy with a
// ~10ms margin against a real stopwatch is a false-signal generator in BOTH
// directions: it fails spuriously whenever the machine is loaded enough that
// two store calls plus scheduling jitter exceed the margin (seen even on the
// pre-existing release baseline), and on a fast/quiet machine it would pass
// even if the retry logic regressed, because the margin is wide relative to
// what a single extra store lookup costs.
//
// WHY NOT TIMING AT ALL: this assertion used to be
// `elapsed < 10*time.Millisecond`, then `elapsed < baseline+10ms` — both
// wall-clock proxies for the same underlying fact (attempt count). Asserting
// the fact directly removes the machine-load dependency entirely: this test
// swaps updateToolCallStatusSleep (subturn.go) for a counting stub and
// asserts the count is exactly zero, which is deterministic on any machine
// at any load and still fails hard if an already-present record wrongly
// takes the retry/sleep path (see the mutation-test evidence in the delivery
// report for this fix).
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

		// Intercept the retry loop's sleep primitive so we can count how many
		// times it fires instead of timing it. Any call at all means the loop
		// was entered, i.e. the first lookup did NOT resolve the record.
		var sleepCalls int
		origSleep := updateToolCallStatusSleep
		updateToolCallStatusSleep = func(time.Duration) { sleepCalls++ }
		t.Cleanup(func() { updateToolCallStatusSleep = origSleep })

		found, updateErr := updateToolCallStatusWithRetry(store, sessionID, callID, "success", 100, async, nil)

		require.NoError(t, updateErr)
		assert.True(t, found)
		assert.Zero(t, sleepCalls,
			"an already-present record must resolve on the first attempt — zero calls to the "+
				"retry-backoff sleep primitive (async=%v, sleepCalls=%d)",
			async, sleepCalls)
	}
}

// TestDeliverSubTurnResultNoDeadlock verifies that deliverSubTurnResult doesn't
// deadlock when multiple goroutines are accessing the parent turnState concurrently.
func TestDeliverSubTurnResultNoDeadlock(t *testing.T) {
	parent := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-deadlock-test",
		depth:          0,
		pendingResults: make(chan *tools.ToolResult, 2), // Small buffer to test blocking
	}

	// Simulate multiple child turns delivering results concurrently
	var wg sync.WaitGroup
	numChildren := 10

	for i := 0; i < numChildren; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			result := &tools.ToolResult{ForLLM: fmt.Sprintf("result-%d", id)}
			deliverSubTurnResult(nil, parent, fmt.Sprintf("child-%d", id), result)
		}(i)
	}

	// Concurrently read from the channel to prevent blocking
	// and to actually retrieve the matched number of results
	go func() {
		for i := 0; i < numChildren; i++ {
			select {
			case <-parent.pendingResults:
			case <-time.After(5 * time.Second):
				t.Error("timeout waiting for result")
				return
			}
		}
	}()

	// Wait for all deliveries to complete (with timeout)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-time.After(3 * time.Second):
		t.Fatal("deadlock detected: deliverSubTurnResult blocked")
	}
}

// TestDeliverSubTurnResult_RaceWithFinish verifies that deliverSubTurnResult handles
// the race condition where Finish() is called while results are being delivered.
func TestDeliverSubTurnResult_RaceWithFinish(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled
	defer cleanup()

	// Collect events via real EventBus
	var mu sync.Mutex
	var deliveredCount, orphanCount int
	sub := al.SubscribeEvents(64)
	defer al.UnsubscribeEvents(sub.ID)
	go func() {
		for evt := range sub.C {
			mu.Lock()
			switch evt.Kind {
			case EventKindSubTurnResultDelivered:
				deliveredCount++
			case EventKindSubTurnOrphan:
				orphanCount++
			}
			mu.Unlock()
		}
	}()

	ctx := context.Background()
	parentTS := &turnState{
		ctx:            ctx,
		turnID:         "parent-race-test",
		depth:          0,
		pendingResults: make(chan *tools.ToolResult, 16),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)

	// Launch goroutines that deliver results while another goroutine calls Finish()
	const numResults = 20
	var wg sync.WaitGroup
	wg.Add(numResults + 1)

	// Goroutine that calls Finish() after a short delay
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		parentTS.Finish(false)
	}()

	// Goroutines that deliver results
	for i := 0; i < numResults; i++ {
		go func(id int) {
			defer wg.Done()
			result := &tools.ToolResult{
				ForLLM: fmt.Sprintf("result-%d", id),
			}
			// This should not panic, even if Finish() is called concurrently
			deliverSubTurnResult(al, parentTS, fmt.Sprintf("child-%d", id), result)
		}(i)
	}

	wg.Wait()
	time.Sleep(20 * time.Millisecond) // let event goroutine flush

	// Get final counts
	mu.Lock()
	finalDelivered := deliveredCount
	finalOrphan := orphanCount
	mu.Unlock()

	t.Logf("Delivered: %d, Orphan: %d, Total: %d", finalDelivered, finalOrphan, finalDelivered+finalOrphan)

	// With the new drainPendingResults behavior, the total events may be >= numResults
	// because Finish() drains remaining results from the channel and emits them as orphans.
	// So we expect:
	// - Some results were delivered successfully (before Finish())
	// - Some results became orphans (after Finish() or channel full)
	// - Some results were in the channel when Finish() was called and got drained as orphans
	// The total should be at least numResults (could be more due to drain)
	if finalDelivered+finalOrphan < numResults {
		t.Errorf("Expected at least %d total events, got %d delivered + %d orphan = %d",
			numResults, finalDelivered, finalOrphan, finalDelivered+finalOrphan)
	}

	// Should have at least some orphan results (those that arrived after Finish() or were drained)
	if finalOrphan == 0 {
		t.Error("Expected at least some orphan results after Finish()")
	}
}
