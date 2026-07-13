// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for AgentLoop.IsSubTurnActiveForSpawnCall — the
// liveness primitive introduced to close a reload/replay data-corruption
// bug class found by live UAT re-verification (2026-07): a session
// reload/replay landing in the window between async delegation's
// placeholder ack write (Status="success", DurationMS≈0, written the
// instant the spawning "delegate" tool call returns) and the real
// EventKindSubTurnEnd correction (spawnSubTurn's cleanup defer, once the
// delegate ACTUALLY finishes) previously presented that placeholder
// literally — a fabricated "done 0ms" for a turn that was, in truth, still
// working. This regressed observably once 7dd9e7a5 ("background delegate's
// final answer lost when parent finishes first") let Critical:true
// delegates run for their full, genuine duration instead of exiting early
// the instant their parent turn ended, making the placeholder-staleness
// window routinely wide enough to lose a reload race.
//
// pkg/gateway/replay.go (isSpanActive) and pkg/gateway/websocket.go's
// orphan watchdog both consult this primitive before trusting a persisted
// terminal snapshot or synthesizing one of their own — see
// TestStreamReplay_ActiveSpawnSpan_WithholdsFabricatedDoneSnapshot (note:
// SpawnSpan, not SpawnCall — pkg/gateway/replay_span_liveness_test.go) and
// TestOrphanWatchdog_GenuinelyActiveDelegate_NeverSynthesizesInterrupted in
// pkg/gateway for the consuming-side regression tests.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestIsSubTurnActiveForSpawnCall_LiveMatch_ReturnsTrue is the direct
// positive case: a turnState registered in activeTurnStates whose
// parentSpawnCallID matches the queried ID, and which has not yet finished
// (IsAlive()==true), must be reported active.
func TestIsSubTurnActiveForSpawnCall_LiveMatch_ReturnsTrue(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:            "child-turn-1",
		parentSpawnCallID: "delegate-call-1",
		finishedChan:      make(chan struct{}),
	}
	al.activeTurnStates.Store(ts.turnID, ts)

	assert.True(t, al.IsSubTurnActiveForSpawnCall("delegate-call-1"),
		"a live turnState whose parentSpawnCallID matches must be reported active")
}

// TestIsSubTurnActiveForSpawnCall_FinishedAndPersisted_ReturnsFalse proves the
// liveness check is not a mere presence check: a turnState that matches by
// ID, has already Finish()'d, AND whose spawning tool-call record has
// already been corrected (subTurnRecordPersisted==true) must be reported
// inactive — this is what lets replay/the orphan watchdog trust a REAL
// terminal snapshot once the sub-turn genuinely completes AND that
// completion has been durably persisted. See
// TestIsSubTurnActiveForSpawnCall_FinishedButNotYetPersisted_ReturnsTrue
// below for the companion case proving isFinished alone is NOT sufficient.
func TestIsSubTurnActiveForSpawnCall_FinishedAndPersisted_ReturnsFalse(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:            "child-turn-2",
		parentSpawnCallID: "delegate-call-2",
		finishedChan:      make(chan struct{}),
	}
	al.activeTurnStates.Store(ts.turnID, ts)
	ts.isFinished.Store(true)             // simulates Finish() having already run
	ts.subTurnRecordPersisted.Store(true) // simulates spawnSubTurn's cleanup defer having completed

	assert.False(t, al.IsSubTurnActiveForSpawnCall("delegate-call-2"),
		"a finished AND persisted turnState must not be reported active even though it still matches "+
			"by ID (activeTurnStates is only cleared lazily; IsAlive()+subTurnRecordPersisted together "+
			"are the authoritative signal)")
}

// TestIsSubTurnActiveForSpawnCall_FinishedButNotYetPersisted_ReturnsTrue is
// the direct regression test for finding 2 (7-reviewer gate): runTurn's own
// `defer ts.Finish(false)` flips isFinished true and fully returns BEFORE
// spawnSubTurn's own cleanup defer even begins correcting the placeholder
// ToolCall record (updateToolCallStatusWithRetry, up to ~935ms of retry
// backoff). A turnState caught in exactly that gap — isFinished==true,
// subTurnRecordPersisted still false — must still be reported ACTIVE so a
// reload/replay landing in the gap withholds the fabricated "done 0ms"
// snapshot instead of serving it as genuine. Collapsing this into a bare
// isFinished check (the pre-fix behavior) would report false here — the
// exact bug this fix closes.
func TestIsSubTurnActiveForSpawnCall_FinishedButNotYetPersisted_ReturnsTrue(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:            "child-turn-gap",
		parentSpawnCallID: "delegate-call-gap",
		finishedChan:      make(chan struct{}),
	}
	al.activeTurnStates.Store(ts.turnID, ts)
	ts.isFinished.Store(true)
	// subTurnRecordPersisted deliberately left at its zero value (false) —
	// modeling the window between isFinished flipping and spawnSubTurn's
	// cleanup defer completing the persistence correction.

	assert.True(t, al.IsSubTurnActiveForSpawnCall("delegate-call-gap"),
		"BUG REGRESSION: a finished-but-not-yet-persisted turnState must still be reported active — "+
			"isFinished alone is not sufficient to trust the persisted spawn-call record")
}

// TestIsSubTurnActiveForSpawnCall_NoMatch_ReturnsFalse proves an unrelated
// or already-cleared parentSpawnCallID (e.g. a genuinely orphaned span with
// no backing turn at all) correctly reports inactive rather than panicking
// or defaulting to "active" (which would wedge the orphan watchdog forever
// and permanently withhold replay's terminal frame).
func TestIsSubTurnActiveForSpawnCall_NoMatch_ReturnsFalse(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:            "child-turn-3",
		parentSpawnCallID: "delegate-call-3",
		finishedChan:      make(chan struct{}),
	}
	al.activeTurnStates.Store(ts.turnID, ts)

	assert.False(t, al.IsSubTurnActiveForSpawnCall("delegate-call-does-not-exist"),
		"a parentSpawnCallID with no matching live turnState must report inactive")
}

// TestIsSubTurnActiveForSpawnCall_EmptyID_ReturnsFalse guards the empty-ID
// edge case (e.g. W1-12's degenerate-input path, pkg/agent/subturn.go,
// where an empty parentSpawnCallID deliberately skips span lifecycle
// emission entirely) — must never report "active" for an ID nothing could
// legitimately carry.
func TestIsSubTurnActiveForSpawnCall_EmptyID_ReturnsFalse(t *testing.T) {
	al := &AgentLoop{}
	assert.False(t, al.IsSubTurnActiveForSpawnCall(""),
		"an empty parentSpawnCallID must always report inactive")
}

// TestIsSubTurnActiveForSpawnCall_RealRetryWindow_StaysActiveUntilPersisted
// is the end-to-end companion to
// TestIsSubTurnActiveForSpawnCall_FinishedButNotYetPersisted_ReturnsTrue: it
// drives a REAL spawnSubTurn call (native dispatch, real runTurn, real
// updateToolCallStatusWithRetry) instead of hand-constructing a turnState,
// proving the production code path — not just the primitive's field
// semantics — closes the gap.
//
// The placeholder ack record for the spawn call is deliberately NEVER
// seeded (mirrors TestSpawnSubTurn_AsyncAckNeverFound_LogsWarnAfterRetryBudgetExhausted
// in wave3_fix5b_test.go), which forces updateToolCallStatusWithRetry to
// exhaust its full ~935ms retry backoff before giving up — this IS the
// "hold updateToolCallStatusWithRetry open past the point where isFinished
// flips true" window the fix must bridge, using the real bounded-retry delay
// already in the production code rather than an injected test hook. Because
// the child's own runTurn (backed by a fast mock provider) completes in low
// tens of milliseconds while the retry backoff runs for up to ~935ms,
// isFinished flips true well before the persistence correction attempt is
// done, and the whole ~900ms window is directly observable by polling.
func TestIsSubTurnActiveForSpawnCall_RealRetryWindow_StaysActiveUntilPersisted(t *testing.T) {
	al := newWave5bTestAgentLoop(t, &slowMockProvider{delay: 5 * time.Millisecond})

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	// Deliberately NOT seeding a placeholder ack record for this callID —
	// guarantees UpdateToolCallStatus finds nothing on every retry attempt,
	// so the ~935ms retry budget is genuinely, deterministically exhausted
	// (not just raced), giving this test a wide, reliable window to observe.
	const callID = "c-liveness-gap-real"

	baseAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, baseAgent, "default agent must exist")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-liveness-gap-real",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 10),
		session:             &ephemeralSessionStore{},
		agent:               baseAgent,
		transcriptStore:     store,
		transcriptSessionID: sessionID,
	}

	cfg := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}, Async: true}
	ctx := withSpawnToolCallID(context.Background(), callID)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = spawnSubTurn(ctx, al, parent, cfg)
	}()

	// findChildTS locates the (single) registered child turnState whose
	// parentSpawnCallID matches callID, by direct scan of activeTurnStates —
	// mirroring exactly what IsSubTurnActiveForSpawnCall itself does, so the
	// test can assert on the underlying isFinished/subTurnRecordPersisted
	// fields directly, not just the primitive's boolean output.
	findChildTS := func() *turnState {
		var found *turnState
		al.activeTurnStates.Range(func(_, value any) bool {
			ts, ok := value.(*turnState)
			if !ok {
				return true
			}
			ts.mu.RLock()
			matches := ts.parentSpawnCallID == callID
			ts.mu.RUnlock()
			if matches {
				found = ts
				return false
			}
			return true
		})
		return found
	}

	// Poll until we directly observe the gap: isFinished==true (runTurn's
	// own defer already ran) but subTurnRecordPersisted==false (spawnSubTurn's
	// cleanup defer has not yet finished its retry-backoff correction) —
	// and confirm IsSubTurnActiveForSpawnCall reports active throughout.
	var sawGapWithActiveTrue bool
	deadline := time.After(3 * time.Second)
poll:
	for {
		select {
		case <-done:
			break poll
		case <-deadline:
			t.Fatal("BLOCKED: spawnSubTurn did not complete within timeout")
		default:
		}
		if ts := findChildTS(); ts != nil {
			isFinished := ts.isFinished.Load()
			persisted := ts.subTurnRecordPersisted.Load()
			if isFinished && !persisted {
				require.True(t, al.IsSubTurnActiveForSpawnCall(callID),
					"BUG REGRESSION: IsSubTurnActiveForSpawnCall must report active while "+
						"isFinished is true but subTurnRecordPersisted is still false — this is "+
						"exactly the reload/replay fabricated-status window finding 2 closes")
				sawGapWithActiveTrue = true
			}
		}
		time.Sleep(2 * time.Millisecond)
	}

	assert.True(t, sawGapWithActiveTrue,
		"the test must have observed the isFinished-but-not-persisted gap at least once — if not, "+
			"the retry-backoff window closed too fast to exercise (check updateToolCallStatusRetryDelays "+
			"and slowMockProvider's delay are still wide enough apart)")

	// Once spawnSubTurn's goroutine has fully returned (cleanup defer,
	// including the persistence correction, complete), the primitive must
	// report inactive.
	assert.False(t, al.IsSubTurnActiveForSpawnCall(callID),
		"must report inactive once spawnSubTurn's cleanup — including the persistence correction — "+
			"has fully completed")
}

// TestIsSubTurnActiveForSpawnCall_MultipleTurns_OnlyMatchingOneCounts
// proves the scan correctly distinguishes between several concurrently
// active turns (e.g. two unrelated background delegates on the same
// session) and only reports active for the one actually matching the
// queried spawn call ID.
func TestIsSubTurnActiveForSpawnCall_MultipleTurns_OnlyMatchingOneCounts(t *testing.T) {
	al := &AgentLoop{}
	tsA := &turnState{turnID: "turn-a", parentSpawnCallID: "call-a", finishedChan: make(chan struct{})}
	tsB := &turnState{turnID: "turn-b", parentSpawnCallID: "call-b", finishedChan: make(chan struct{})}
	al.activeTurnStates.Store(tsA.turnID, tsA)
	al.activeTurnStates.Store(tsB.turnID, tsB)

	assert.True(t, al.IsSubTurnActiveForSpawnCall("call-a"))
	assert.True(t, al.IsSubTurnActiveForSpawnCall("call-b"))
	assert.False(t, al.IsSubTurnActiveForSpawnCall("call-c"))
}
