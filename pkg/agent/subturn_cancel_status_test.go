// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FIX 5 regression coverage: canceling a parent turn while an async
// delegate is in flight must record the sub-turn's EventKindSubTurnEnd
// status as SubTurnStatusInterrupted, not SubTurnStatusError — the latter
// is indistinguishable from a genuine failure on replay, sitting right next
// to the parent's own correctly-labeled "(interrupted)" entry. Before this
// fix, spawnSubTurn's cleanup defer computed a strictly binary
// success/error status; SubTurnStatusCancelled/SubTurnStatusInterrupted were
// defined (pkg/agent/events.go) but never assigned anywhere.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSpawnSubTurn_ParentHardAbort_RecordsInterruptedStatus drives a REAL
// hard-abort cascade — pkg/agent/turn.go's Finish(true) walks
// parentTS.childTurnIDs and calls Finish(true) on each registered child,
// which cancels the child's OWN context (childTS.cancelFunc, assigned from
// the context.WithTimeout call in spawnSubTurn) — and asserts the resulting
// EventKindSubTurnEnd carries Status=SubTurnStatusInterrupted.
//
// BDD:
//
//	Given a parent turn with an async delegate sub-turn in flight (blocked
//	  on a slow LLM call),
//	When the parent is hard-aborted (as RequestCancel's escalation path
//	  does after a graceful-cancel timeout, or a user-initiated /stop),
//	Then the cascade cancels the child's context, and the sub-turn's
//	  EventKindSubTurnEnd is recorded with Status="interrupted" — not
//	  "error".
//
// Negative-test discipline: this test was confirmed to FAIL (Status ==
// "error") against the pre-fix binary success/error computation before the
// fix was applied — see the delivery report.
func TestSpawnSubTurn_ParentHardAbort_RecordsInterruptedStatus(t *testing.T) {
	// ADR-057 fixture repair: spawnSubTurn's child registration now also
	// creates the child agent's real workspace directory under Home — an
	// empty Home fails "mkdir : no such file or directory" and spawnSubTurn
	// returns before ever appending to parentTS.childTurnIDs.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}, Home: t.TempDir()},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	// Long enough that the hard abort below reliably fires while the child's
	// LLM call is still blocked waiting on ctx.Done() (see slowMockProvider).
	provider := &slowMockProvider{delay: 5 * time.Second}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	// ADR-057 FR-005 fixture repair: spawnSubTurn now mints the child via
	// al.GetSessionStore().CreateSessionWithID(childID, parentTS.transcriptSessionID,
	// ...) against the REAL shared store — a parent turnState with no
	// transcriptSessionID is an invalid parent and spawnSubTurn returns an
	// error before ever registering the child, which is why the
	// require.Eventually below used to time out waiting for childTurnIDs.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	var mu sync.Mutex
	var subTurnEndEvents []SubTurnEndPayload
	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)
	go func() {
		for evt := range sub.C {
			if payload, ok := evt.Payload.(SubTurnEndPayload); ok {
				mu.Lock()
				subTurnEndEvents = append(subTurnEndEvents, payload)
				mu.Unlock()
			}
		}
	}()

	ctx := context.Background()
	parentTS := &turnState{
		turnID:              "parent-hard-abort",
		depth:               0,
		session:             newEphemeralSession(nil),
		pendingResults:      make(chan *tools.ToolResult, 16),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		al:                  al, // required: Finish(true)'s cascade reads ts.al to find children
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		transcriptStore:     store,
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-hard-abort-1")
		// Critical: true is required for this scenario — a NON-critical
		// sub-turn takes an earlier, unrelated graceful-exit shortcut
		// (runTurn's own ts.IsParentEnded() check, loop.go ~5481) the
		// instant it sees the parent has finished, regardless of hard vs.
		// graceful, WITHOUT ever touching childCtx.Err(). Critical:true
		// disables that shortcut ("All SubTurns are canceled regardless of
		// Critical flag" under a hard abort specifically — see
		// SubTurnConfig.Critical's doc comment), so the child instead
		// relies on the cascade's context cancellation, exercising the
		// exact path this fix targets.
		subCfg := SubTurnConfig{Model: "slow-model", Async: true, Critical: true}
		_, _ = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	// Give the child goroutine time to register itself (activeTurnStates,
	// parentTS.childTurnIDs) and enter its blocking LLM call before the
	// cascade fires — without this the cascade could race ahead of
	// registration and find no children to cancel.
	require.Eventually(t, func() bool {
		parentTS.mu.RLock()
		defer parentTS.mu.RUnlock()
		return len(parentTS.childTurnIDs) > 0
	}, 2*time.Second, 5*time.Millisecond, "child sub-turn must register itself on the parent before the abort fires")

	parentTS.Finish(true) // hard abort — cascades to the in-flight child

	wg.Wait()

	// wg.Wait() only guarantees spawnSubTurn has returned — the EventBus
	// subscription is consumed by a SEPARATE goroutine (started above), so a
	// brief poll is needed for it to have actually processed the event.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(subTurnEndEvents) > 0
	}, 2*time.Second, 5*time.Millisecond, "EventKindSubTurnEnd must be observed by the subscriber")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, subTurnEndEvents, 1, "exactly one EventKindSubTurnEnd must be emitted")
	assert.Equal(t, SubTurnStatusInterrupted, subTurnEndEvents[0].Status,
		"a sub-turn canceled via the parent's hard-abort cascade must be recorded as "+
			"'interrupted', not 'error' — 'error' is indistinguishable from a genuine failure "+
			"on replay, sitting right next to the parent's own correctly-labeled (interrupted) entry")
	// FIX 4: this test calls parentTS.Finish(true) DIRECTLY (bypassing
	// RequestCancel/ClaimCancel entirely — see the setup above), so
	// parentTS.cancelFired stays false. The honest reason in that case is
	// "unknown" (see spawnSubTurn's cleanup defer doc comment) — proving
	// the fallback path, complementary to
	// TestSpawnSubTurn_ExplicitCancelViaRequestCancel_RecordsCancelledAndReason's
	//nolint:misspell // documents the literal wire enum value, matches frontend TS union
	// "parent_cancelled" case below, which drives a REAL RequestCancel.
	assert.Equal(t, "unknown", subTurnEndEvents[0].Reason,
		"when the parent's own cancel was never claimed via RequestCancel/ClaimCancel, the "+
			"honest reason is 'unknown', not a fabricated 'parent_cancelled'")
}

// TestSpawnSubTurn_ExplicitCancelViaRequestCancel_RecordsCancelledAndReason
// drives a REAL RequestCancel call against the parent's session — the actual
// production cancel path a live /stop click or scheduled-run deadline
// force-abort takes — and asserts two things FIX 4 adds:
//  1. parentTS.cancelFired becomes true (RequestCancel's ClaimCancel claims
//     it), so the cascaded child's Reason is populated as "parent_cancelled"
//     — not the "unknown" fallback the sibling test above exercises.
//  2. The status is still SubTurnStatusInterrupted (not Cancelled) for this
//     cascade case, since the CHILD's own cancelFired stays false — only the
//     PARENT's was claimed. SubTurnStatusCancelled is reserved for the
//     narrower case where the sub-turn's OWN session is targeted directly
//     (see SubTurnStatusCancelled's doc comment, pkg/agent/events.go).
//
// Negative-test discipline: confirmed to FAIL (Reason == "" instead of
// "parent_cancelled") against the pre-fix cleanup defer (which never set
// endReason at all) before the fix was applied.
//
// "parent_cancelled" and the SubTurnStatusCancelled identifier verbatim; both match the frontend TS union.
//
//nolint:misspell // this func's doc comment + assertions reference the literal wire enum value
func TestSpawnSubTurn_ExplicitCancelViaRequestCancel_RecordsCancelledAndReason(t *testing.T) {
	// ADR-057 fixture repair: spawnSubTurn's child registration now also
	// creates the child agent's real workspace directory under Home — an
	// empty Home fails "mkdir : no such file or directory" and spawnSubTurn
	// returns before ever appending to parentTS.childTurnIDs.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock", Model: "gpt-4o-mini"}, Home: t.TempDir()},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &slowMockProvider{delay: 5 * time.Second}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	var mu sync.Mutex
	var subTurnEndEvents []SubTurnEndPayload
	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)
	go func() {
		for evt := range sub.C {
			if payload, ok := evt.Payload.(SubTurnEndPayload); ok {
				mu.Lock()
				subTurnEndEvents = append(subTurnEndEvents, payload)
				mu.Unlock()
			}
		}
	}()

	ctx := context.Background()
	// ADR-057 fixture repair: RequestCancel's role-B predicates match on
	// routingSessionID, not transcriptSessionID.
	parentTS := &turnState{
		turnID:              "parent-explicit-cancel",
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               0,
		session:             newEphemeralSession(nil),
		pendingResults:      make(chan *tools.ToolResult, 16),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		al:                  al,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)
	al.activeTurnStates.Store(sessionID, parentTS)
	defer al.activeTurnStates.Delete(sessionID)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-explicit-cancel-1")
		subCfg := SubTurnConfig{Model: "slow-model", Async: true, Critical: true}
		_, _ = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	require.Eventually(t, func() bool {
		parentTS.mu.RLock()
		defer parentTS.mu.RUnlock()
		return len(parentTS.childTurnIDs) > 0
	}, 2*time.Second, 5*time.Millisecond, "child sub-turn must register itself on the parent before the cancel fires")

	// The REAL production cancel path: RequestCancel -> ClaimCancel (sets
	// parentTS.cancelFired=true) -> ... -> Finish(true) via the escalation
	// this test drives directly for determinism (RequestCancel itself only
	// starts the graceful->hard timer; calling Finish(true) here is
	// equivalent to that timer firing, without waiting out its real delay).
	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.True(t, outcome.Fired, "RequestCancel must fire for the actively-registered parent turn")
	require.True(t, parentTS.cancelFired.Load(), "RequestCancel's ClaimCancel must claim parentTS.cancelFired")

	parentTS.Finish(true) // simulates the graceful->hard escalation timer firing

	wg.Wait()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(subTurnEndEvents) > 0
	}, 2*time.Second, 5*time.Millisecond, "EventKindSubTurnEnd must be observed by the subscriber")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, subTurnEndEvents, 1, "exactly one EventKindSubTurnEnd must be emitted")
	//nolint:misspell // shorthand for SubTurnStatusCancelled, spelled to match
	assert.Equal(t, SubTurnStatusInterrupted, subTurnEndEvents[0].Status,
		"the CHILD's own cancelFired was never claimed (only the parent's was) — this remains "+
			"the cascade case (Interrupted), not the direct-target case (Cancelled)")
	assert.Equal(t, "parent_cancelled", subTurnEndEvents[0].Reason, //nolint:misspell // wire value, frontend TS union
		"once the parent's own cancel was explicitly claimed via RequestCancel, the cascaded "+
			"child's reason must be 'parent_cancelled', not the 'unknown' fallback")
}
