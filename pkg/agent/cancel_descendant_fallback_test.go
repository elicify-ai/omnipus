// Package agent — cancel_descendant_fallback_test.go
//
// Regression coverage for a wasFired-gate gap in RequestCancel
// (pkg/agent/cancel.go), the same bug CLASS commit 78bddc82 already fixed
// for hooks.KillBackgroundSessions (that fix decoupled the background-bash
// kill cascade from wasFired; this one closes the equivalent gap in the
// NATIVE descendant-cancellation cascade).
//
// RequestCancel resolves exactly ONE hook via GetActiveTurnHookForSession
// (root-preferring) and computes wasFired from THAT single hook's own
// ClaimCancel() result. That undercounts whenever a DIFFERENT turnState
// shares the session's transcriptSessionID, is still alive, and was NEVER
// claimed — e.g. the resolved root already fired from an earlier, unrelated
// cancel (a duplicate/racing Stop click, an orphan-watchdog reap, a prior
// RequestCancel invocation) while a background/Critical async delegate
// sub-turn (a wholly separate turnState, same transcriptSessionID) is still
// genuinely running and was never signaled. Before this fix, RequestCancel's
// entire descendant-cancellation cascade (InterruptSession) AND its
// turn_canceled transcript/audit write lived behind that single wasFired
// check — a claimable, live descendant would never be reached, never be
// interrupted, and the cancel would silently produce NO transcript record at
// all despite genuine background work continuing to run.
//
// This test drives the exact deterministic shape of that gap (no timing
// races needed): a root turnState whose cancel was ALREADY claimed by an
// earlier actor (cancelFired=true, but no callback ever registered or fired
// — modeling a prior RequestCancel/watchdog claim that has not yet reached
// Finish()) plus a live, never-canceled child turnState sharing the same
// transcriptSessionID. Before the fix, RequestCancel resolves the root
// (root-preferred), finds ClaimCancel() already false, and returns
// Fired:false with the descendant cascade and transcript write both
// skipped — even though the child was never touched. After the fix,
// RequestCancel's claimAnyTurnForSession fallback finds and claims the
// live child, the cascade signals both turns, and a turn_canceled entry is
// written with DescendantsCancelled listing both turn IDs.
//
// Traces to: pkg/agent/cancel.go RequestCancel (wasFired fallback),
// pkg/agent/turn.go claimAnyTurnForSession. Spec ref: FR-6a, FR-10, FR-15.

package agent

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestRequestCancel_RootAlreadyClaimed_FallsBackToLiveDescendant is the
// negative-test-discipline regression proof: this test FAILS against the
// pre-fix code (asserted below via the revert-prove procedure in the
// delivery report) and PASSES after the claimAnyTurnForSession fallback is
// wired into RequestCancel.
func TestRequestCancel_RootAlreadyClaimed_FallsBackToLiveDescendant(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	workspaceDir := tmpDir + "/workspace"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "cascade-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "agent loop shared session store must be non-nil")

	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	require.NoError(t, err)
	sessionID := meta.ID
	require.NotEmpty(t, sessionID)

	// Root turnState (depth=0) — simulate an EARLIER, unrelated cancel having
	// already claimed it (e.g. a prior RequestCancel call, or the orphan
	// watchdog) by calling ClaimCancel() directly, exactly as RequestCancel
	// itself would. No callback is registered and Finish() is never called on
	// it here — modeling the claim having landed without (yet) unwinding, so
	// this test isolates the "already claimed" state alone.
	parentTS := &turnState{
		turnID:              "turn-parent-already-claimed",
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	require.True(t, parentTS.ClaimCancel(), "precondition: first claim on the root must succeed")
	al.activeTurnStates.Store(sessionID, parentTS)
	defer al.activeTurnStates.Delete(sessionID)

	// Child turnState (depth=1, same transcriptSessionID) — alive, NEVER
	// claimed. This stands in for a live background/Critical async delegate
	// that the already-fired root never reached.
	childTS := &turnState{
		turnID:              "turn-child-live-unclaimed",
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               1,
		parentTurnID:        "turn-parent-already-claimed",
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	childKey := sessionID + ":child"
	al.activeTurnStates.Store(childKey, childTS)
	defer al.activeTurnStates.Delete(childKey)

	require.False(t, childTS.cancelFired.Load(), "precondition: child must not yet be claimed")

	// The REAL production entry point: RequestCancel on the shared session.
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "test-channel"},
		CancelHooks{},
	)
	require.NoError(t, err)

	// ASSERT 1 (the core regression fix): the cancel must still FIRE — a
	// live, claimable descendant exists even though the root's own
	// ClaimCancel lost the race.
	assert.True(t, outcome.Fired,
		"BUG REGRESSION: RequestCancel must fall back to a live, unclaimed descendant "+
			"when the primary resolved hook (root) was already claimed — a claimable "+
			"child must not be silently skipped")

	// ASSERT 2: the fallback must have actually claimed the child (the only
	// turn that COULD be claimed).
	assert.True(t, childTS.cancelFired.Load(),
		"the fallback claim must land on the live, unclaimed child turnState")
	assert.Equal(t, "turn-child-live-unclaimed", outcome.TurnID,
		"CancelOutcome.TurnID must reflect whichever turn was actually claimed — here, "+
			"the fallback-claimed child, since the root could not be claimed")

	// ASSERT 3: descendants must include BOTH turn IDs — collectDescendantTurnIDs
	// matches by transcriptSessionID regardless of claim state, and the cascade
	// must still reach the already-claimed root too (InterruptSession does not
	// gate on ClaimCancel/cancelFired).
	descendantsSorted := append([]string(nil), outcome.Descendants...)
	sort.Strings(descendantsSorted)
	require.Len(t, descendantsSorted, 2,
		"the cascade must signal both the root and the live child; got: %v", descendantsSorted)
	assert.Contains(t, descendantsSorted, "turn-parent-already-claimed")
	assert.Contains(t, descendantsSorted, "turn-child-live-unclaimed")

	// Trigger the CHILD's Finish (the fallback-claimed turn) to fire the
	// onCancelFinish callback registered on it.
	childTS.Finish(false)
	time.Sleep(100 * time.Millisecond) // grace period for the synchronous callback to flush

	// ASSERT 4: a turn_canceled transcript entry must exist, with
	// DescendantsCancelled listing both turn IDs — the exact on-disk shape
	// the e2e suite (cancel-cross-channel.spec.ts T24a/T24b) parses.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var cancelledEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeTurnCancelled {
			cancelledEntries = append(cancelledEntries, e)
		}
	}
	require.Len(t, cancelledEntries, 1,
		"BUG REGRESSION: RequestCancel must still write a turn_canceled transcript entry "+
			"when the fallback claimed a descendant — got entries: %v", cancelledEntries)

	rootEntry := cancelledEntries[0]
	assert.Equal(t, "turn-child-live-unclaimed", rootEntry.TurnID,
		"the turn_canceled entry's TurnID must be the fallback-claimed turn")
	descSorted := append([]string(nil), rootEntry.DescendantsCancelled...)
	sort.Strings(descSorted)
	require.Len(t, descSorted, 2,
		"DescendantsCancelled must list both turn IDs; got: %v", descSorted)
	assert.Contains(t, descSorted, "turn-parent-already-claimed")
	assert.Contains(t, descSorted, "turn-child-live-unclaimed")
}

// TestClaimAnyTurnForSession_NoMatch_ReturnsNil proves the helper reports a
// true nil TurnCancelHook (not a boxed-nil-pointer interface footgun) when
// nothing shares the queried sessionID.
func TestClaimAnyTurnForSession_NoMatch_ReturnsNil(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:              "unrelated",
		transcriptSessionID: "session-other",
		routingSessionID:    session.RoutingSessionID("session-other"),
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(ts.turnID, ts)

	got := al.claimAnyTurnForSession("session-target")
	assert.Nil(t, got, "no matching turnState must yield a real nil interface")
}

// TestClaimAnyTurnForSession_OnlyAlreadyClaimed_ReturnsNil proves the helper
// does not re-claim (and does not report success for) a turnState whose
// cancel was already fired — the double-cancel/no-extra-descendant case must
// remain a genuine no-op.
func TestClaimAnyTurnForSession_OnlyAlreadyClaimed_ReturnsNil(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:              "already-claimed",
		transcriptSessionID: "session-x",
		routingSessionID:    session.RoutingSessionID("session-x"),
		finishedChan:        make(chan struct{}),
	}
	require.True(t, ts.ClaimCancel())
	al.activeTurnStates.Store(ts.turnID, ts)

	got := al.claimAnyTurnForSession("session-x")
	assert.Nil(t, got, "an already-claimed turn must not be reported as a fresh claim")
}

// TestClaimAnyTurnForSession_OnlyFinished_ReturnsNil proves the helper skips
// a matching-but-finished turnState (IsAlive()==false) even if it was never
// claimed — claiming a finished turn's cancel would register a callback that
// can never fire (Finish() already ran).
func TestClaimAnyTurnForSession_OnlyFinished_ReturnsNil(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:              "finished-unclaimed",
		transcriptSessionID: "session-y",
		routingSessionID:    session.RoutingSessionID("session-y"),
		finishedChan:        make(chan struct{}),
	}
	ts.isFinished.Store(true)
	al.activeTurnStates.Store(ts.turnID, ts)

	got := al.claimAnyTurnForSession("session-y")
	assert.Nil(t, got, "a finished turn must never be claimed by the fallback")
	assert.False(t, ts.cancelFired.Load(), "the finished turn's cancelFired must remain untouched")
}

// TestClaimAnyTurnForSession_LiveUnclaimedMatch_Claims proves the direct
// positive case in isolation from the full RequestCancel flow above.
func TestClaimAnyTurnForSession_LiveUnclaimedMatch_Claims(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:              "live-unclaimed",
		transcriptSessionID: "session-z",
		routingSessionID:    session.RoutingSessionID("session-z"),
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(ts.turnID, ts)

	got := al.claimAnyTurnForSession("session-z")
	require.NotNil(t, got)
	assert.Equal(t, "live-unclaimed", got.TurnID())
	assert.True(t, ts.cancelFired.Load(), "claimAnyTurnForSession must actually claim the match")
}

// TestClaimAnyTurnForSession_EmptySessionID_NeverClaimsEmptyRoutingTurn guards
// the empty-sessionID gate (14-reviewer sign-off, cancel-cascade lens): turns
// with an empty routingSessionID legally exist (system messages with no async
// transcript id, channel messages with no msg.SessionID), and a Tier B cancel
// in an idle chat resolves its sessionID to "". The base release gated the
// RequestCancel fallback with sessionID != ""; the merge restoration (7f4eab0b)
// dropped that half, letting claimAnyTurnForSession("") claim an arbitrary
// unrelated empty-routing-id turn — consuming its first-cancel-wins latch and
// reporting fired=true for a cancel that reached nothing.
func TestClaimAnyTurnForSession_EmptySessionID_NeverClaimsEmptyRoutingTurn(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{
		turnID:              "empty-routing-victim",
		transcriptSessionID: "",
		routingSessionID:    session.RoutingSessionID(""),
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(ts.turnID, ts)

	got := al.claimAnyTurnForSession("")
	assert.Nil(t, got,
		"BUG REGRESSION: an empty-sessionID fallback query must claim nothing — "+
			"matching it against empty-routing-id turns cancels an unrelated turn")
	assert.False(t, ts.cancelFired.Load(),
		"the unrelated turn's first-cancel-wins latch must not be consumed")
}
