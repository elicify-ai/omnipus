package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// TestRepro_AsyncDelegateCancel_ArmsBeforeChildRegisters is the Go-level
// reproduction of the e2e T24a regression (tests/e2e/cancel-cross-channel.spec.ts:665
// — "cancel cascades to background subagent (delegate async=true)").
//
// Root cause: an ASYNC (async=true) delegate's own turn frequently finishes
// (its "OK, delegating..." follow-up completes and Finish() removes it from
// al.activeTurnStates) BEFORE the backgrounded spawnSubTurn goroutine has
// even reached its own bookkeeping — DelegateTool.executeAsync dispatches
// SpawnSubTurn on a fresh goroutine and returns an immediate ack to the
// parent (pkg/tools/delegate.go:1198-1213), so there is a real window in
// which NEITHER the parent NOR the child turn is present in
// al.activeTurnStates at all. A Stop click landing in that exact window is
// precisely the "cancel arrives before its turn registers" race
// cancel_prearm.go closes for TOP-LEVEL turns — RequestCancel finds nothing
// and ARMS a latch instead of silently no-op'ing.
//
// The bug: spawnSubTurn (pkg/agent/subturn.go) registers the child directly
// via `al.activeTurnStates.Store(childID, childTS)` — bypassing
// registerActiveTurn (turn.go), and therefore bypassing
// consumePreArmedCancel, entirely. The latch armed above is never consumed
// at the EARLIEST moment the child becomes visible; the only consumption
// point left is runTurn's OWN internal registerActiveTurn call deep inside
// al.runTurn (loop.go), which runs only after workspace-dir resolution,
// citation-tracker setup, and every other per-turn setup step in between —
// widening the exact race window this mechanism exists to close, for
// delegate sub-turns specifically.
//
// This test drives the REAL production sequence: delete the parent from
// activeTurnStates (simulating its own turn already finished), call the REAL
// RequestCancel (arms a latch, since nothing is registered), THEN dispatch
// spawnSubTurn on a goroutine exactly as executeAsync does. It asserts the
// child sub-turn still unblocks (proving the pre-arm latch was honored) and
// that a turn_canceled transcript entry lands with a non-empty
// DescendantsCancelled — the exact assertion T24a makes.
func TestRepro_AsyncDelegateCancel_ArmsBeforeChildRegisters(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Provider: "mock"},
		},
	}
	msgBus := bus.NewMessageBus()
	// Long enough the child never completes on its own before the latch's
	// cascade should have unblocked it.
	provider := &slowMockProvider{delay: 30 * time.Second}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	ctx := context.Background()
	parentTS := &turnState{
		turnID:              "parent-async-cancel-repro",
		transcriptSessionID: sessionID,
		depth:               0,
		session:             newEphemeralSession(nil),
		pendingResults:      make(chan *tools.ToolResult, 16),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		al:                  al,
		finishedChan:        make(chan struct{}),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)

	// Mimic the real timeline: the parent turn registers, then finishes and
	// clears itself (its own quick "OK, delegating..." follow-up completed)
	// BEFORE the backgrounded async goroutine has done anything at all.
	al.activeTurnStates.Store(sessionID, parentTS)
	al.activeTurnStates.Delete(sessionID)

	// PREMISE NOTE (design-flaw fix, cancel_prearm.go): armCancelOrFindActiveTurn
	// now additionally requires turnImminentForIdentity — real evidence (a
	// queued or in-flight message on the identity's sessionWorker) that a
	// turn is genuinely about to exist, closing the false-positive arm a
	// FINISHED session used to trigger (TestWS_Cancel_OnlyInterruptsTargetSession,
	// pkg/gateway/websocket_multisession_test.go). That discriminator has no
	// visibility into THIS test's scenario: an async delegate spawn is
	// dispatched by pkg/tools/delegate.go's executeAsync directly from a
	// tool call, never through the session-worker message-dispatch path this
	// fix's signal is built on, and — by construction, mirroring the real
	// T24a race — the spawning goroutine has not even been launched yet at
	// the moment RequestCancel runs below. There is currently NO
	// dispatcher-level signal (comparable to sessionWorker.inTurn/inbox) for
	// "an async delegate spawn is imminent," so this call is primed
	// explicitly to stand in for one, and the resulting gap is real: closing
	// it for the message-dispatch path narrows, but does not eliminate, the
	// original race for THIS specific path (async delegate whose parent
	// turn finishes before its child registers) — see this fix's own report
	// for the recommended follow-up (a dedicated pending-spawn marker in
	// pkg/tools/delegate.go / pkg/agent/subturn.go, both out of this fix's
	// scope).
	primeImminentSessionWorker(al, sessionID)

	// REAL production Stop-button path, arriving in the window where NEITHER
	// the parent NOR the not-yet-spawned child is registered. Must ARM a
	// latch rather than silently no-op.
	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.False(t, outcome.Fired, "nothing is registered yet — RequestCancel must not report Fired")
	require.True(t, outcome.Armed, "RequestCancel must arm a pre-registration latch when nothing is registered")

	// NOW dispatch the async delegate exactly as executeAsync does: a fresh
	// goroutine calling spawnSubTurn, with Async:true, Critical:true.
	spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-async-cancel-repro")
	subCfg := SubTurnConfig{Model: "slow-model", Async: true, Critical: true}

	var wg sync.WaitGroup
	wg.Add(1)
	spawnDone := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(spawnDone)
		_, _ = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	// The child MUST unblock well within the hard-abort/detach ladder if the
	// armed latch was honored. If this times out, the async delegate cancel
	// cascade is broken — the e2e T24a regression.
	select {
	case <-spawnDone:
		// PASS: async spawn unblocked after the pre-armed cancel was consumed.
	case <-time.After(15 * time.Second):
		t.Fatal("FAIL: async delegate (async=true) spawnSubTurn did NOT unblock within 15s " +
			"of an armed pre-registration cancel latch — the background-subagent cancel " +
			"cascade is stalled (e2e T24a regression)")
	}
	wg.Wait()

	// Assert the transcript recorded turn_canceled with a non-empty
	// descendants_canceled array — the exact e2e T24a assertion.
	require.Eventually(t, func() bool {
		entries, err := store.ReadTranscript(sessionID)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.Type == session.EntryTypeTurnCancelled && len(e.DescendantsCancelled) > 0 {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond,
		"transcript must contain a turn_canceled entry with a non-empty descendants_canceled array")
}
