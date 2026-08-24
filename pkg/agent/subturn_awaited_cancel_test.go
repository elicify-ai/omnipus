// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T24b regression coverage (cancel-cross-channel-spec.md T24, US-4.1, FR-6a):
// the e2e suite (tests/e2e/cancel-cross-channel.spec.ts) drives two sibling
// scenarios through the SAME helper (assertCancelCascadesToSubagent), differing
// ONLY in the `delegate` tool's `async` argument:
//
//	T24a — delegate(async=true)  (background): PASSES reliably.
//	T24b — delegate(async=false) (await):      times out waiting on a
//	                                            toBeVisible() assertion.
//
// Every existing Go-level cancel/cascade test (TestCancel_SubAgentCascade,
// TestSpawnSubTurn_ParentHardAbort_RecordsInterruptedStatus,
// TestSpawnSubTurn_ExplicitCancelViaRequestCancel_RecordsCancelledAndReason)
// either injects synthetic turnStates directly into activeTurnStates (never
// exercising the real spawnSubTurn plumbing) or drives spawnSubTurn with
// Async:true/Critical:true — i.e. the BACKGROUND delegation shape, spawned in
// its OWN goroutine independent of the caller. None of them drive spawnSubTurn
// the way DelegateTool.executeSync (pkg/tools/delegate.go) actually does in
// production for async=false: Async:false, Critical left at its zero value
// (false), invoked BLOCKING on the calling (tool-dispatch) goroutine — the
// goroutine that also owns the parent turnState.
//
// This test closes that gap: it drives spawnSubTurn with the EXACT config
// executeSync uses, spawned only because Go requires a second goroutine to let
// the test's main goroutine fire RequestCancel concurrently — mirroring how in
// production the AGENT LOOP's own tool-dispatch goroutine is the one blocked,
// while RequestCancel is invoked from the gateway's WS handler goroutine.
//
// CONFIRMED: this test PASSES — the pkg/agent cascade for the AWAITED/SYNC
// delegate path is sound and symmetric with the async path. The blocked
// spawnSubTurn call returns in well under 3s (no reliance on the 3s/8s
// hard-abort escalation timers — the graceful providerCancel() alone is
// sufficient), Descendants/DescendantsCancelled correctly include the
// awaited child's turn ID, and Interrupted/Status/Reason all match the async
// sibling test's assertions verbatim. This rules out pkg/agent's cancel
// cascade as the T24b root cause; see the investigation notes in the PR/task
// history for the frontend-side candidate (src/store/chat.ts's startSpan:
// `if (!lastMsgId) return {}` silently drops a subagent_start WS frame when
// no assistant message exists yet in the store for that session).

package agent

import (
	"context"
	"sort"
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

// TestSpawnSubTurn_AwaitedSyncCancel_CascadesAndRecordsDescendants drives the
// REAL await (async=false) delegation shape — spawnSubTurn called BLOCKING,
// on a goroutine standing in for the agent loop's own tool-dispatch goroutine,
// with the exact SubTurnConfig{Async: false} (no Critical) DelegateTool.
// executeSync passes — then fires a REAL RequestCancel (the same production
// path a Stop-button click drives) while the child is still blocked inside a
// slow (simulated-streaming) LLM call, and asserts:
//
//  1. RequestCancel.Descendants includes BOTH the parent's and the child's
//     turn ID (the Go-level equivalent of the e2e assertion that
//     transcript.jsonl's turn_canceled entry carries a non-empty
//     descendants_canceled array — FR-6a).
//  2. spawnSubTurn's blocking call actually RETURNS promptly (bounded wait,
//     not the full 5s LLM delay) — proving the graceful cascade's
//     providerCancel() reaches the child's in-flight call without needing the
//     3s/8s hard-abort escalation timers, and without deadlocking the
//     blocked caller goroutine.
//  3. The returned ToolResult carries Interrupted:true — the exact flag
//     DelegateTool.executeSync (pkg/tools/delegate.go) inspects to decide
//     whether to take its generic "Delegate execution failed" shortcut
//     (which it must NOT take for a cancel) versus formatting a normal
//     "Subagent task completed" result the LLM/UI can render.
//  4. EventKindSubTurnEnd is recorded Status=Interrupted, Reason=
//     the awaited-cancel wire reason value — matching the async sibling
//     verbatim, proving parity between the two delegation modes.
//  5. The session transcript's turn_canceled entry actually contains the
//     child's turn ID in DescendantsCancelled — the literal on-disk field the
//     e2e test parses out of transcript.jsonl.
func TestSpawnSubTurn_AwaitedSyncCancel_CascadesAndRecordsDescendants(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock", Model: "gpt-4o-mini"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	// Long enough that RequestCancel below reliably fires while the child's
	// LLM call is still blocked waiting on ctx.Done() (see slowMockProvider).
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
	parentTS := &turnState{
		turnID:              "parent-awaited-sync-cancel",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011: newTurnState (turn.go) always defaults
		// routingSessionID to the turn's own transcriptSessionID for a root
		// turn — GetActiveTurnHookForSession (and every other role-B cancel
		// predicate) matches on routingSessionID, not transcriptSessionID.
		// This test builds the turnState literal directly (bypassing
		// newTurnState), so it must replicate that default itself or
		// RequestCancel below can never find this turn at all.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		session:          newEphemeralSession(nil),
		pendingResults:   make(chan *tools.ToolResult, 16),
		concurrencySem:   make(chan struct{}, testMaxConcurrentSubTurns),
		al:               al,
		finishedChan:     make(chan struct{}),
		transcriptStore:  store,
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)
	al.activeTurnStates.Store(sessionID, parentTS)
	defer al.activeTurnStates.Delete(sessionID)

	var subResult *tools.ToolResult
	var subErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-awaited-sync-cancel-1")
		// EXACTLY DelegateTool.executeSync's production config: Async:false,
		// Critical left unset (zero value, false) — no goroutine wrapper, this
		// blocks the calling goroutine until spawnSubTurn itself returns.
		subCfg := SubTurnConfig{Model: "slow-model", Async: false}
		subResult, subErr = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	// Wait for the child to register itself on the parent — proves
	// spawnSubTurn reached activeTurnStates registration before blocking on
	// the child's own LLM call (matches the pattern the sibling async tests
	// use to avoid racing the cascade ahead of registration).
	require.Eventually(t, func() bool {
		parentTS.mu.RLock()
		defer parentTS.mu.RUnlock()
		return len(parentTS.childTurnIDs) > 0
	}, 2*time.Second, 5*time.Millisecond,
		"child sub-turn must register itself on the parent before the cancel fires")

	parentTS.mu.RLock()
	childSpawnKey := parentTS.childTurnIDs[0] // the child's activeTurnStates map key (childID) — NOT its turnID; see below
	parentTS.mu.RUnlock()
	require.NotEmpty(t, childSpawnKey)

	// Wait for the child to actually be blocked inside its LLM call — i.e.
	// its OWN providerCancel is set (loop.go's per-call `providerCtx,
	// providerCancel := context.WithCancel(turnCtx); ts.setProviderCancel(...)`).
	// "Registered on the parent" (above) only proves spawnSubTurn's
	// bookkeeping ran; it fires well BEFORE the child's own runTurn reaches
	// its provider call. Interrupt's graceful cascade (steering.go) fires
	// ts.providerCancel (nil-safe no-op) and ONLY THEN sets the graceful
	// flag — by design, a graceful interrupt that arrives before the flag's
	// first LLM call does not abort that call, it lets it run once (FR-15's
	// "one more, then wrap up" semantics) and only the 3s hard-abort
	// escalation would stop it. Firing RequestCancel any earlier than this
	// point races that design and would need the full hard-abort window
	// instead of the graceful path this test exists to verify — matching
	// this test's own docstring ("fires a REAL RequestCancel ... while the
	// child is still blocked inside a slow ... LLM call").
	require.Eventually(t, func() bool {
		val, ok := al.activeTurnStates.Load(childSpawnKey)
		if !ok {
			return false
		}
		childTS, ok := val.(*turnState)
		if !ok || childTS == nil {
			return false
		}
		childTS.mu.RLock()
		defer childTS.mu.RUnlock()
		return childTS.providerCancel != nil
	}, 2*time.Second, 5*time.Millisecond,
		"child sub-turn must be blocked inside its LLM call (providerCancel set) before the cancel fires")

	// The REAL production cancel path: RequestCancel -> ClaimCancel -> graceful
	// InterruptSession cascade -> providerCancel() on every matching turnState
	// (parent AND child, matched by transcriptSessionID). Fired while the
	// spawnSubTurn call above is still blocked mid-flight on the calling
	// goroutine — exactly the shape a live Stop-button click hits against an
	// awaited delegate.
	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.True(t, outcome.Fired, "RequestCancel must fire for the actively-registered parent turn")

	// ASSERT 1: descendants must include BOTH the parent's and the child's
	// turn ID — the Go-level proof behind the e2e's non-empty
	// descendants_canceled assertion (FR-6a), specifically for the SYNC/AWAIT
	// shape (unlike TestCancel_SubAgentCascade, which injects synthetic
	// turnStates and never actually calls spawnSubTurn/DelegateTool.executeSync).
	//
	// Note: childSpawnKey (parentTS.childTurnIDs[0], == the child's
	// activeTurnStates map key / SessionKey, e.g. "subturn-1") is a DIFFERENT
	// identifier namespace from childTS.turnID (e.g. "main-turn-1", minted by
	// al.newTurnEventScope's global turnSeq counter — turn.go/loop.go
	// newTurnEventScope). collectDescendantTurnIDs (which RequestCancel.
	// Descendants and the transcript's descendants_canceled both come from)
	// reports turnID, not the map key — so the "other" entry (not
	// parentTS.turnID) IS the child's real turnID.
	descSorted := append([]string(nil), outcome.Descendants...)
	sort.Strings(descSorted)
	require.Len(t, descSorted, 2,
		"RequestCancel's cascade must signal both the parent and the awaited child; got: %v", descSorted)
	assert.Contains(t, descSorted, parentTS.turnID)
	var childRealTurnID string
	for _, id := range descSorted {
		if id != parentTS.turnID {
			childRealTurnID = id
		}
	}
	require.NotEmpty(t, childRealTurnID,
		"a second, non-parent turn ID must be present in the cascade — proving the awaited child was signaled too")
	t.Logf("child spawn key (activeTurnStates key) = %q; child real turnID (wire descendants_canceled value) = %q",
		childSpawnKey, childRealTurnID)

	// ASSERT 2: the blocked spawnSubTurn call must actually return promptly —
	// well under the 5s slowMockProvider delay, and without needing the 3s/8s
	// hard-abort escalation timers RequestCancel arms — proving the graceful
	// cascade alone is sufficient and the caller goroutine does not deadlock.
	select {
	case <-done:
		// good
	case <-time.After(3 * time.Second):
		t.Fatal("spawnSubTurn (awaited/sync) did not return within 3s of RequestCancel firing — " +
			"the graceful cancel cascade failed to reach the blocked child, or the caller goroutine deadlocked")
	}

	// ASSERT 3: the returned ToolResult must be marked Interrupted — this is
	// the exact flag DelegateTool.executeSync inspects (`err != nil &&
	// !result.Interrupted` gate) to avoid the generic "Delegate execution
	// failed" shortcut for a genuine cancel.
	require.Error(t, subErr, "spawnSubTurn must return a non-nil error when its context was canceled")
	// spawnSubTurn's native-path return (subturn.go, "Convert turnResult to
	// tools.ToolResult") passes al.runTurn's returned error through as-is
	// (err = turnErr) with no rewrapping, so whatever error chain runTurn
	// itself produces is what callers see; DelegateTool.executeSync does not
	// inspect the error chain at all (only result.Interrupted, asserted
	// below), so this is logged for diagnostic completeness rather than
	// asserted on a specific wrapped sentinel.
	t.Logf("spawnSubTurn returned err = %v (%T)", subErr, subErr)
	require.NotNil(t, subResult, "spawnSubTurn must still return a non-nil result on cancel (not nil-result-on-error)")
	assert.True(t, subResult.Interrupted,
		"the ToolResult returned to DelegateTool.executeSync must carry Interrupted:true so a session "+
			"reload shows 'interrupted' (matching the live WS frame) instead of 'failed'")

	// ASSERT 4: EventKindSubTurnEnd parity with the async sibling test.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(subTurnEndEvents) > 0
	}, 2*time.Second, 5*time.Millisecond, "EventKindSubTurnEnd must be observed by the subscriber")

	mu.Lock()
	require.Len(t, subTurnEndEvents, 1, "exactly one EventKindSubTurnEnd must be emitted")
	assert.Equal(t, SubTurnStatusInterrupted, subTurnEndEvents[0].Status,
		"an awaited (sync) sub-turn canceled via the parent's graceful cascade must be recorded as "+
			"'interrupted', not 'error'")
	assert.Equal(t, "parent_cancelled", subTurnEndEvents[0].Reason, //nolint:misspell // wire value, frontend TS union
		"once the parent's own cancel was explicitly claimed via RequestCancel, the cascaded "+
			"awaited child's reason must be 'parent_cancelled'")
	mu.Unlock()

	// Trigger the parent's own Finish so the onCancelFinish callback (which
	// writes the turn_canceled transcript entry) fires, mirroring the real
	// production sequence: the tool call returns to loop.go's runTurn, which
	// then finishes the parent turn once it observes the graceful interrupt.
	parentTS.Finish(false)
	time.Sleep(100 * time.Millisecond) // grace period for the synchronous callback to flush

	// ASSERT 5: the on-disk transcript's turn_canceled entry must list the
	// child's turn ID in DescendantsCancelled — the literal field the e2e
	// test parses out of transcript.jsonl / <date>.jsonl.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var cancelledEntry *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeTurnCancelled {
			cancelledEntry = &entries[i]
			break
		}
	}
	require.NotNil(t, cancelledEntry, "transcript must contain a turn_canceled entry")
	descOnDisk := append([]string(nil), cancelledEntry.DescendantsCancelled...)
	sort.Strings(descOnDisk)
	require.Len(t, descOnDisk, 2, "on-disk descendants_canceled must list both parent and child; got: %v", descOnDisk)
	assert.Contains(t, descOnDisk, childRealTurnID,
		"on-disk descendants_canceled must include the awaited child's turn ID — this is the exact "+
			"array the e2e test (T24b) asserts is non-empty")
}
