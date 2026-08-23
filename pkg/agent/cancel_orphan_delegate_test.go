// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for a live-UAT-reported HIGH-severity bug: clicking
// "Stop generation" on a turn that had just dispatched a background (async)
// `delegate` call did not reliably cancel the delegate.
// No span/badge remained in chat at the moment of interruption — it LOOKED
// cleanly canceled — but the underlying delegate sub-turn kept running
// invisibly and resurfaced minutes later, sometimes concurrently with a
// LATER, unrelated delegate call, whose token streams then interleaved into
// one garbled message (see pkg/gateway/websocket_stream_ownership_test.go
// for the companion regression test on that half of the bug).
//
// Root cause: RequestCancel's PHASE B/C escalation timers (pkg/agent/
// cancel.go) gated hard-abort escalation on `activeTurn.IsAlive()` alone,
// where activeTurn is the single TurnCancelHook resolved ONCE at cancel time
// via GetActiveTurnHookForSession — which always prefers the ROOT turn when
// one exists (see that method's own doc comment). A background delegate
// sub-turn is a SEPARATE turnState sharing the parent's transcriptSessionID.
// When the root/parent turn finishes gracefully within the 3s escalation
// window — the common case, since InterruptSession's graceful cascade fires
// providerCancel() for the root immediately, which usually unwinds its
// in-flight LLM call fast — `activeTurn.IsAlive()` goes false and the OLD
// code skipped InterruptSessionHard for the WHOLE session, never reaching
// the still-running child at all. The child's own graceful nudge (also
// delivered by InterruptSession's cascade, since it matches every turn
// sharing the session) only aborts its CURRENT in-flight LLM call; the
// retry loop in pkg/agent/loop.go only treats a canceled call as terminal
// when THAT turn's own hardAbortRequested() is true, so an un-hard-aborted
// child simply retries with a fresh, uncanceled context and keeps running.
//
// Fix: AgentLoop.sessionTurnsStillAlive (pkg/agent/steering.go) checks
// liveness across EVERY turn matching the session — root and descendants —
// and cancel.go's PHASE B/C gates now use it instead of the single resolved
// turn's IsAlive().

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestSessionTurnsStillAlive_ReturnsOnlyAliveMatchingTurns is the fast,
// deterministic unit test for the new helper in isolation (no timers
// needed): given a session with a FINISHED root turn and a STILL-ALIVE
// child sub-turn (both registered in activeTurnStates, both sharing the
// same transcriptSessionID), sessionTurnsStillAlive must return only the
// child — proving it looks at the whole session cascade, not just whichever
// single turn a caller happens to be holding a reference to.
func TestSessionTurnsStillAlive_ReturnsOnlyAliveMatchingTurns(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	const sid = "session-still-alive-unit"

	// ADR-057 FR-011/FR-015 inversion `[grill C-1]`: sessionTurnsStillAlive
	// (steering.go) is one of the seven role-B predicates — it now matches on
	// routingSessionID, not transcriptSessionID, so the cascade this test
	// proves ("looks at the whole session, not just one turn") survives the
	// D1 identity split (a delegated child's transcriptSessionID becomes its
	// own, no longer equal to the session the cascade is keyed by).
	root := &turnState{
		turnID:              "root-1",
		transcriptSessionID: sid,
		routingSessionID:    session.RoutingSessionID(sid),
		finishedChan:        make(chan struct{}),
	}
	root.Finish(false) // root already finished — must NOT appear in the result

	child := &turnState{
		turnID:              "child-1",
		transcriptSessionID: sid,
		routingSessionID:    session.RoutingSessionID(sid),
		finishedChan:        make(chan struct{}),
	}
	// child intentionally left unfinished (simulates the still-running orphan).

	unrelated := &turnState{
		turnID:              "unrelated-1",
		transcriptSessionID: "other-session",
		routingSessionID:    session.RoutingSessionID("other-session"),
		finishedChan:        make(chan struct{}),
	}

	al.activeTurnStates.Store(root.turnID, root)
	al.activeTurnStates.Store(child.turnID, child)
	al.activeTurnStates.Store(unrelated.turnID, unrelated)
	defer al.activeTurnStates.Delete(root.turnID)
	defer al.activeTurnStates.Delete(child.turnID)
	defer al.activeTurnStates.Delete(unrelated.turnID)

	alive := al.sessionTurnsStillAlive(sid)
	require.Len(t, alive, 1, "exactly one still-alive turn must match sid")
	assert.Equal(t, "child-1", alive[0].turnID,
		"the finished root must be excluded and the unrelated session's turn must never be considered")

	// Once the child also finishes, nothing should remain.
	child.Finish(false)
	assert.Empty(t, al.sessionTurnsStillAlive(sid), "no turn should remain once every match has finished")

	// Empty sessionID must not panic and must return nothing.
	assert.Nil(t, al.sessionTurnsStillAlive(""))
}

// TestRequestCancel_OrphanedBackgroundDelegate_HardAbortedAfterParentGracefullyFinishes
// drives the REAL production cancel path end to end, reproducing the exact
// live-UAT timing: the resolved root/parent turn finishes gracefully
// (Finish(false)) the instant InterruptSession's graceful cascade fires its
// providerCancel — simulating a root turn that wraps up fast after Stop is
// clicked — while a background delegate sub-turn (a separate turnState
// sharing the same transcriptSessionID) keeps running, ignoring its own
// graceful nudge, exactly like an orphaned delegate would.
//
// BDD:
//
//	Given an active root turn and a background delegate sub-turn sharing one
//	  session, where the root finishes gracefully almost immediately once
//	  cancel fires (the common case),
//	When RequestCancel is called (the real Stop-generation path) and the
//	  full 3s graceful->hard escalation window elapses,
//	Then the delegate sub-turn — still alive the whole time — is hard-
//	  aborted too, not silently left running.
//
// Negative-test discipline: against the pre-fix code (PHASE B/C gated on
// the single resolved activeTurn.IsAlive()), this test fails on timeout —
// the child's hardAbortRequested() never becomes true because the root's
// own IsAlive() already reads false well before the 3s mark, short-
// circuiting InterruptSessionHard for the whole session.
func TestRequestCancel_OrphanedBackgroundDelegate_HardAbortedAfterParentGracefullyFinishes(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "orphan-delegate-test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	rootTS := &turnState{
		turnID:              "root-orphan-test",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: GetActiveTurnHookForSession
		// (RequestCancel's claim gate) and sessionTurnsStillAlive (the PHASE
		// B/C escalation gate this test exists to prove) are both role-B
		// predicates that now match on routingSessionID, not
		// transcriptSessionID.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		finishedChan:     make(chan struct{}),
		transcriptStore:  store,
	}
	// Simulates the common case: the root's own in-flight call is aborted by
	// InterruptSession's graceful cascade and the root wraps up almost
	// instantly (Finish(false)) — well before the 3s hard-abort mark.
	rootTS.providerCancel = func() { rootTS.Finish(false) }

	childTS := &turnState{
		turnID:              "child-orphan-delegate",
		transcriptSessionID: sessionID, // shares the parent's session — this is what makes it a "descendant" for cascade purposes
		// ADR-057 FR-011/FR-015: see the identical note on rootTS above —
		// the child inherits routingSessionID verbatim from the parent
		// (FR-011), exactly as spawnSubTurn's own
		// `childTS.routingSessionID = parentTS.routingSessionID` would set it.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            1,
		parentTurnID:     rootTS.turnID,
		parentTurnState:  rootTS,
		finishedChan:     make(chan struct{}),
		// providerCancel intentionally left nil: an orphaned background
		// delegate that ignores its own graceful nudge (e.g. mid multi-tool
		// task, not currently blocked on an interruptible LLM call) is
		// exactly what this test simulates — it must never finish on its
		// own during this test.
	}

	al.activeTurnStates.Store(sessionID, rootTS) // root, keyed by sessionID (registerActiveTurn's convention)
	al.activeTurnStates.Store(childTS.turnID, childTS)
	defer al.activeTurnStates.Delete(sessionID)
	defer al.activeTurnStates.Delete(childTS.turnID)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "alex", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired, "RequestCancel must fire for the actively-registered root turn")

	// The root must have finished gracefully almost immediately (proving the
	// scenario this test simulates is realistic).
	require.Eventually(t, func() bool { return !rootTS.IsAlive() }, 1*time.Second, 5*time.Millisecond,
		"root turn must finish gracefully shortly after InterruptSession's cascade fires providerCancel")

	// The orphaned child must still be alive at this point — the bug this
	// test targets is specifically about a child that OUTLIVES its parent's
	// graceful finish.
	assert.True(t, childTS.IsAlive(), "child must still be running once the root has already finished")

	// PHASE B fires at t=3s. Poll past it with margin for CI scheduling
	// jitter. Before the fix, this would time out: the child's
	// hardAbortRequested() never becomes true because PHASE B's gate,
	// keyed on the already-finished root alone, skips InterruptSessionHard
	// for the entire session.
	require.Eventually(t, childTS.hardAbortRequested, 5*time.Second, 20*time.Millisecond,
		"BUG REGRESSION: the orphaned background delegate sub-turn was never hard-aborted — "+
			"PHASE B's escalation gate must check session-wide liveness (sessionTurnsStillAlive), "+
			"not just the single resolved root turn's IsAlive()")
}
