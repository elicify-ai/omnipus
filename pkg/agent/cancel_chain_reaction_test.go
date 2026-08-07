// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cancel_chain_reaction_test.go — regression coverage for the
// gate-plus-recursion supersession of ADR-057 FR-024 (2026-08-04).
//
// THE RACE: RequestCancel's PHASE A used to compute the live subtree ONCE and
// thread that frozen snapshot through PHASE B/C's escalation gates. A
// sub-turn that registered into al.activeTurnStates AFTER PHASE A ran — the
// common shape being a `delegate async=true` spawn, whose delegating parent
// turn frequently finishes gracefully within milliseconds of a Stop while the
// backgrounded spawnSubTurn goroutine keeps running and registers its child
// moments later — was invisible to that frozen list no matter how long it
// kept running: PHASE B/C's gate (al.liveTurnStatesAmong(descendants)) could
// only ever look for turn ids that were already in the snapshot.
//
// WHY RECURSION ALONE IS NOT ENOUGH: re-scanning (or re-arming a latch for a
// spawn already known to be imminent) only ever reaches a child that has
// ALREADY registered, or is ALREADY known to be about to. It cannot stop a
// BRAND NEW child from being born after cancellation has begun — the child's
// context is deliberately NOT derived from the parent's
// (spawnSubTurn's childCtx is context.WithTimeout(context.Background(), ...),
// so a Critical async delegate can outlive its parent's own graceful finish —
// re-parenting it would break that), so ordinary context-cancellation
// propagation gives spawnSubTurn no signal at all that its own parent is
// being cancelled. A parent whose "cancel my children" step has already run
// can still spawn a new child a moment later, and that child would be born
// free of anything recursion already resolved.
//
// THE COMPLETE FIX: gate + recursion, together.
//  1. THE GATE (turn.go's turnState.cancelling, steering.go's
//     markTurnsCancelling, subturn.go's spawnSubTurn ancestor-chain check,
//     ErrSessionCancelling): Interrupt/InterruptSessionHard mark every
//     resolved target — the anchor and every currently-known live
//     descendant — as cancelling FIRST, atomically, before firing any
//     interrupt signal. spawnSubTurn, before creating any new child, walks
//     parentTS's own ancestor chain (parentTurnState) checking this flag at
//     every level; a hit refuses the spawn outright. This is what actually
//     closes the race by construction: no new child can ever be born once
//     its parent (or an ancestor) has been marked cancelling.
//  2. THE RECURSION (cancel.go's fresh re-scan + hasPendingDescendantSpawn/
//     armChainReactionCancelLatch): for whatever the gate could not have
//     prevented — a child that ALREADY registered before PHASE A ran, or
//     whose spawn was ALREADY dispatched (pendingSpawns marker set) before
//     the gate could mark its parent — PHASE B/C re-derive the live
//     descendant set fresh at each checkpoint and arm a chain-reaction latch
//     when a spawn is still outstanding, so consumePreArmedCancel catches it
//     the instant it registers and gives it its own full recursive cancel
//     cycle (which re-applies the SAME gate to ITS OWN children, closing the
//     race inductively at arbitrary depth).
//
// Per binding Rule 4 every positive assertion below has a companion negative
// (sibling/unrelated-session, or "the gate does not over-block") assertion in
// the same test.
package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestRequestCancel_LateChildRegisteredDuringEscalation_IsStillHardAborted is
// the required RED/GREEN reproduction: a sub-turn that registers AFTER
// RequestCancel has already found, claimed, and begun escalating an EARLIER
// turn (the root) must still be reached by PHASE B's hard-abort.
//
// RED (pre-fix): PHASE A's frozen descendants list is [root.turnID] only —
// the child does not exist in al.activeTurnStates yet. Once the root is
// marked finished (simulating its own quick graceful completion, the common
// `delegate async=true` shape), al.liveTurnStatesAmong([root.turnID]) is
// empty, so PHASE B returns immediately WITHOUT calling InterruptSessionHard
// at all — and because PHASE C's own timer was only ever scheduled from
// inside that now-skipped branch, PHASE C never even runs either. The late
// child is never touched: hardAbortRequested() stays false forever, and the
// Stop that already began never reaches it — exactly the race the operator
// rejected the "frozen snapshot" premise over.
//
// GREEN (post-fix): PHASE B re-derives the descendant set FRESH via
// al.collectDescendantTurnIDs(sessionID) instead of trusting the PHASE-A
// snapshot, finds the now-registered child alive (it shares the root's
// routingSessionID, inherited verbatim regardless of when it registered),
// and calls InterruptSessionHard(sessionID, ScopeSubtree, hint) — which
// itself resolves its targets fresh and reaches the child.
func TestRequestCancel_LateChildRegisteredDuringEscalation_IsStillHardAborted(t *testing.T) {
	// Not t.Parallel(): mutates the package-level escalation-delay vars.
	origHard := cancelHardAbortDelay
	origDetach := cancelDetachDelay
	cancelHardAbortDelay = 150 * time.Millisecond
	cancelDetachDelay = 150 * time.Millisecond
	t.Cleanup(func() {
		cancelHardAbortDelay = origHard
		cancelDetachDelay = origDetach
	})

	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("chain-root-%d", nonce)

	// Negative control (Rule 4), registered BEFORE the cancel and left
	// running throughout: a wholly unrelated session's own active turn must
	// never be reached by rootID's cancel cascade.
	unrelatedID := fmt.Sprintf("chain-unrelated-%d", nonce)
	unrelatedTS := u15RegisterActiveTurn(t, al, unrelatedID, "chain-unrelated-turn", 0, "")

	rootTS := u15RegisterActiveTurn(t, al, rootID, "chain-root-turn", 0, "")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)
	require.ElementsMatch(t, []string{"chain-root-turn"}, outcome.Descendants,
		"precondition: PHASE A must not have seen the child yet — it does not exist")

	// The root finishes gracefully almost immediately — its own "OK,
	// delegating..." follow-up completes — BEFORE the async spawn it
	// dispatched has registered its own child turnState. Mirrors runTurn's
	// REAL cleanup order (loop.go's `defer ts.Finish(false)` registered
	// BEFORE `defer al.clearActiveTurn(ts)` — Go defers run LIFO, so
	// clearActiveTurn ALWAYS runs before Finish() in production): a "finished"
	// turn's own activeTurnStates entry is gone, not merely flagged, by the
	// time anything else can observe it as finished. Simulating only the flag
	// without also removing the entry would leave a stale root match that
	// GetActiveTurnHookForSession's root-preference rule (H1, turn.go) could
	// pick up in place of a REAL child — production never has this residue.
	rootTS.isFinished.Store(true)
	al.clearActiveTurnStateEntry(rootID, rootTS)

	// THE RACE: the late child registers — the Stop has already begun (root
	// claimed, PHASE A ran, root already finished and cleared) — yet this
	// child only exists in al.activeTurnStates from this point forward.
	childTS := u15RegisterChildTurn(t, al, rootID, fmt.Sprintf("chain-child-session-%d", nonce),
		"chain-child-turn", "chain-root-turn", 1, false)
	require.True(t, childTS.IsAlive(), "precondition: the late child must be alive when it registers")
	require.False(t, childTS.hardAbortRequested(), "precondition: not yet hard-aborted")

	require.Eventually(t, childTS.hardAbortRequested, 3*time.Second, 20*time.Millisecond,
		"the late-registering child must still be hard-aborted by PHASE B's freshly re-derived (not frozen) "+
			"descendant set — a sub-turn that registers after cancellation has begun must still be cancelled")

	assert.False(t, unrelatedTS.hardAbortRequested(),
		"an unrelated session's own active turn must never be reached by rootID's cancel cascade")
	assert.True(t, unrelatedTS.IsAlive(), "an unrelated session's turn must remain alive throughout")
}

// TestRequestCancel_ChainReactionLatch_CatchesChildRegisteringAfterLastCheckpoint
// proves the SECOND half of the chain-reaction fix — the part the fresh
// re-scan above cannot cover on its own: a child whose registration is so
// slow it doesn't land until AFTER every scheduled escalation checkpoint
// (PHASE B and PHASE C) has already fired. This is the "arbitrary depth /
// arbitrary delay" case the operator's chosen design must still close "by
// construction", not merely "if it happens to land inside our polling
// window".
//
// Mechanism under test: a delegate spawn marker (the real production
// MarkPendingDelegateSpawn primitive) is set for rootID before either
// checkpoint fires. PHASE C (the LAST scheduled checkpoint) finds nothing
// currently alive but the marker still outstanding, and arms a
// chain-reaction latch (armChainReactionCancelLatch) under rootID's own
// identity key. Only THEN — well after both checkpoints have elapsed — does
// the child actually register, via the real registerActiveTurn production
// path. consumePreArmedCancel must find the armed latch and re-invoke
// RequestCancel for the child, interrupting it immediately (synchronously,
// inside registerActiveTurn) — not on any further timer.
func TestRequestCancel_ChainReactionLatch_CatchesChildRegisteringAfterLastCheckpoint(t *testing.T) {
	// Not t.Parallel(): mutates the package-level escalation-delay vars.
	origHard := cancelHardAbortDelay
	origDetach := cancelDetachDelay
	cancelHardAbortDelay = 30 * time.Millisecond
	cancelDetachDelay = 30 * time.Millisecond
	t.Cleanup(func() {
		cancelHardAbortDelay = origHard
		cancelDetachDelay = origDetach
	})

	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("chain-latch-root-%d", nonce)

	// Negative control (Rule 4): an unrelated session's own active turn,
	// registered around the same time, must remain untouched throughout.
	unrelatedID := fmt.Sprintf("chain-latch-unrelated-%d", nonce)
	unrelatedTS := u15RegisterActiveTurn(t, al, unrelatedID, "chain-latch-unrelated-turn", 0, "")

	rootTS := u15RegisterActiveTurn(t, al, rootID, "chain-latch-root-turn", 0, "")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)

	// The root finishes immediately and its entry is cleared — mirroring
	// runTurn's REAL cleanup order (see the identical comment in
	// TestRequestCancel_LateChildRegisteredDuringEscalation_IsStillHardAborted
	// above for why simulating "finished" must also remove the map entry,
	// not just flip the flag) — and, mirroring pkg/tools/delegate.go's
	// executeAsync, a delegate spawn marker is recorded for this SAME
	// identity right away: the real production evidence that a child is
	// dispatched and will register soon, even though nothing is registered
	// for it yet.
	rootTS.isFinished.Store(true)
	al.clearActiveTurnStateEntry(rootID, rootTS)
	al.cancelPreArm.markPendingSpawn(time.Now(), pendingSpawnKeys(rootID, "", "")...)

	// Wait past BOTH escalation checkpoints (30ms + 30ms) with a healthy
	// margin — by construction, nothing must have been alive at either
	// check (only the marker), so PHASE C must have armed a chain-reaction
	// latch for rootID's identity by now.
	require.Eventually(t, func() bool {
		al.cancelPreArm.mu.Lock()
		_, armed := al.cancelPreArm.latches["s:"+rootID]
		al.cancelPreArm.mu.Unlock()
		return armed
	}, 2*time.Second, 10*time.Millisecond,
		"PHASE C (the last scheduled checkpoint) must arm a chain-reaction latch for rootID's identity "+
			"when a delegate spawn is still outstanding and nothing else is currently alive")

	// ONLY NOW — well after every scheduled checkpoint already fired — does
	// the slow-to-register child actually register, via the REAL
	// al.registerActiveTurn production path — deliberately NOT
	// u15RegisterChildTurn's raw activeTurnStates.Store (that helper exists
	// for fixtures that only need to be VISIBLE to a Range scan, not for
	// exercising registration's consumePreArmedCancel side effect — see
	// cancel_async_delegate_bypass_test.go's own regression for exactly this
	// distinction: a raw Store never consumes a latch, only
	// registerActiveTurn does).
	childTS := &turnState{
		sessionKey:          "chain-latch-child-key-" + fmt.Sprint(nonce),
		turnID:              "chain-latch-child-turn",
		transcriptSessionID: fmt.Sprintf("chain-latch-child-session-%d", nonce),
		routingSessionID:    session.RoutingSessionID(rootID), // inherited verbatim, FR-011
		depth:               1,
		parentTurnID:        "chain-latch-root-turn",
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(childTS)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(childTS.sessionKey, childTS) })

	require.Eventually(t, func() bool {
		interrupted, _ := childTS.gracefulInterruptRequested()
		return interrupted
	}, 500*time.Millisecond, 10*time.Millisecond,
		"the chain-reaction latch must interrupt the child the INSTANT it registers, even though it registered "+
			"well after every scheduled escalation checkpoint had already fired — this is the 'closes by "+
			"construction, not by hoping a re-scan lands in time' half of the fix")

	assert.False(t, unrelatedTS.hardAbortRequested(),
		"an unrelated session's own active turn must never be reached by rootID's chain-reaction cascade")
	assert.True(t, unrelatedTS.IsAlive(), "an unrelated session's turn must remain alive throughout")
}

// TestSpawnSubTurn_RefusedWhileParentIsCancelling is the required GATE proof:
// a child whose creation is ATTEMPTED DURING its parent's cancellation must
// not end up running — refused outright, not merely cancelled after the
// fact. This is the test that must FAIL against recursion alone (no
// turnState.cancelling field, no markTurnsCancelling call, no spawnSubTurn
// ancestor check) and PASS with the gate: recursion only ever reaches a
// child that has already registered or is already known to be imminent; it
// has no way to stop a spawn attempt that starts fresh, this instant, on a
// parent that is already being cancelled.
func TestSpawnSubTurn_RefusedWhileParentIsCancelling(t *testing.T) {
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	sessionID := fmt.Sprintf("gate-parent-session-%d", nonce)

	parentTS := &turnState{
		turnID:              "gate-parent-turn",
		sessionKey:          sessionID,
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		al:                  al,
		finishedChan:        make(chan struct{}),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(context.Background())
	al.registerActiveTurn(parentTS)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(sessionID, parentTS) })

	require.False(t, parentTS.cancelling.Load(), "precondition: not yet marked cancelling")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)
	require.True(t, parentTS.cancelling.Load(),
		"RequestCancel's PHASE-A Interrupt call must mark the claimed turn cancelling (the GATE half) "+
			"the instant it resolves it as a cancel target")

	// THE ATTEMPT: a delegate call reaching spawnSubTurn WHILE the parent is
	// already marked cancelling — mirroring a real `delegate` tool call
	// racing with a Stop click that landed a moment earlier.
	result, spawnErr := spawnSubTurn(withSpawnToolCallID(parentTS.ctx, "call-during-cancel"), al, parentTS, SubTurnConfig{
		Model:             "test-model",
		SystemPrompt:      "attempted mid-cancellation",
		TargetAgentID:     "main",
		DelegateSessionID: "gate-attempted-child",
	})

	require.Nil(t, result, "a refused spawn must never produce a result")
	require.True(t, errors.Is(spawnErr, ErrSessionCancelling),
		"a child whose creation is attempted DURING its parent's cancellation must be refused outright, not "+
			"merely cancelled after the fact — this is the GATE half of the fix")

	_, registered := al.activeTurnStates.Load("gate-attempted-child")
	assert.False(t, registered, "no child turnState must ever have been registered for the refused spawn")

	// Positive lower bound (Rule 4): an UNRELATED, non-cancelling parent's
	// spawn attempt must proceed PAST the gate — proven by it failing for a
	// DIFFERENT, expected reason (empty Model -> ErrInvalidSubTurnConfig,
	// checked well after the gate) rather than ErrSessionCancelling. A gate
	// that refused every spawn indiscriminately would make the negative
	// assertion above pass for the wrong reason; this rules that out.
	healthySessionID := fmt.Sprintf("gate-unrelated-session-%d", nonce)
	healthyTS := &turnState{
		turnID:              "gate-unrelated-turn",
		transcriptSessionID: healthySessionID,
		routingSessionID:    session.RoutingSessionID(healthySessionID),
		al:                  al,
		finishedChan:        make(chan struct{}),
	}
	healthyTS.ctx, healthyTS.cancelFunc = context.WithCancel(context.Background())
	require.False(t, healthyTS.cancelling.Load(),
		"precondition: the unrelated parent was never touched by the cancel above")

	_, unrelatedErr := spawnSubTurn(withSpawnToolCallID(healthyTS.ctx, "call-unrelated"), al, healthyTS, SubTurnConfig{
		// Model deliberately left empty — triggers ErrInvalidSubTurnConfig
		// AFTER the gate check, proving the gate itself did not fire.
	})
	require.True(t, errors.Is(unrelatedErr, ErrInvalidSubTurnConfig),
		"an unrelated, non-cancelling parent's spawn attempt must reach PAST the gate")
	assert.False(t, errors.Is(unrelatedErr, ErrSessionCancelling),
		"the gate must never fire for a parent that was never marked cancelling")
}

// TestSpawnSubTurn_RefusedWhileAncestorIsCancelling proves the "or any
// ancestor" half of the gate: an intermediate parent that was never itself
// resolved as a cancel TARGET (never registered in al.activeTurnStates at
// spawn time — mirroring collectLiveDescendantTurnStates's own documented
// KNOWN LIMITATION, steering.go, for an intermediate delegate no longer
// tracked there) must still be refused, because ITS OWN ancestor (the actual
// RequestCancel target) has been marked cancelling. This is what makes the
// fix an ancestor WALK (parentTurnState chain) rather than a shallow
// "check parentTS itself" gate.
func TestSpawnSubTurn_RefusedWhileAncestorIsCancelling(t *testing.T) {
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("gate-ancestor-root-%d", nonce)

	grandparentTS := &turnState{
		turnID:              "gate-ancestor-grandparent-turn",
		sessionKey:          rootID,
		transcriptSessionID: rootID,
		routingSessionID:    session.RoutingSessionID(rootID),
		al:                  al,
		finishedChan:        make(chan struct{}),
	}
	al.registerActiveTurn(grandparentTS)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(rootID, grandparentTS) })

	// The intermediate parent is deliberately NOT registered in
	// al.activeTurnStates — only linked via parentTurnState, exactly the
	// shape collectLiveDescendantTurnStates's own KNOWN LIMITATION doc
	// comment describes for an intermediate delegate no longer tracked
	// in-memory. Its own cancelling flag must therefore stay false.
	parentTS := &turnState{
		turnID:              "gate-ancestor-parent-turn",
		transcriptSessionID: fmt.Sprintf("gate-ancestor-parent-session-%d", nonce),
		routingSessionID:    session.RoutingSessionID(rootID),
		depth:               1,
		parentTurnID:        grandparentTS.turnID,
		parentTurnState:     grandparentTS,
		al:                  al,
		finishedChan:        make(chan struct{}),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(context.Background())

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)
	require.True(t, grandparentTS.cancelling.Load(),
		"precondition: the grandparent (the actual RequestCancel target) must be marked cancelling")
	require.False(t, parentTS.cancelling.Load(),
		"precondition: the intermediate parent, never itself resolved as a target, must NOT be marked "+
			"cancelling directly — this is what makes the test exercise the ANCESTOR WALK, not a shallow "+
			"parentTS-only check")

	result, spawnErr := spawnSubTurn(withSpawnToolCallID(parentTS.ctx, "call-ancestor-cancel"), al, parentTS, SubTurnConfig{
		Model:             "test-model",
		SystemPrompt:      "attempted mid-ancestor-cancellation",
		TargetAgentID:     "main",
		DelegateSessionID: "gate-ancestor-attempted-grandchild",
	})

	require.Nil(t, result)
	require.True(t, errors.Is(spawnErr, ErrSessionCancelling),
		"spawnSubTurn must walk parentTS's OWN ancestor chain (parentTurnState) and refuse when ANY "+
			"ancestor — not just the immediate parent — has been marked cancelling")

	_, registered := al.activeTurnStates.Load("gate-ancestor-attempted-grandchild")
	assert.False(t, registered, "no grandchild turnState must ever have been registered")
}
