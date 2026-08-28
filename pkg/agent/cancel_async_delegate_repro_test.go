package agent

import (
	"context"
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
// parent (pkg/tools/delegate.go), so there is a real window in which NEITHER
// the parent NOR the child turn is present in al.activeTurnStates at all. A
// Stop click landing in that exact window is precisely the "cancel arrives
// before its turn registers" race cancel_prearm.go closes for TOP-LEVEL
// turns — RequestCancel finds nothing and ARMS a latch instead of silently
// no-op'ing.
//
// The gate that decides whether to arm, armCancelOrFindActiveTurn, only arms
// when turnImminentForIdentity reports a turn as genuinely imminent — not
// merely "nothing registered right now" (see that function's own doc
// comment: a just-FINISHED session must never arm, or a stale latch would
// poison the user's next, unrelated turn). Its evidence used to be
// al.sessionWorkers ALONE — populated exclusively by the top-level
// inbound-message dispatch loop (loop.go), which a delegate sub-turn NEVER
// goes through (spawnSubTurn dispatches straight to al.runTurn from a bare
// goroutine kicked off by executeAsync). Two PRIOR passes at this exact test
// papered over that gap by calling primeImminentSessionWorker to fabricate
// sessionWorker evidence that has NO production analog for this path — the
// test passed, but proved nothing about whether a delegate spawn made
// turnImminentForIdentity return true in production, which is the actual
// bug T24a is about.
//
// THE FIX under test here: pkg/tools/delegate.go's executeAsync now calls a
// real production signal — MarkPendingDelegateSpawn (the DelegateSpawnMarker
// seam, wired automatically by SetSpawner onto *agent.AgentLoopSpawner) —
// synchronously, on the delegating parent's own tool-execution goroutine,
// immediately before dispatching the goroutine that will call SpawnSubTurn.
// turnImminentForIdentity now checks this pending-spawn marker (see
// cancel_prearm.go's pendingSpawns field and hasPendingSpawnLocked) ahead of
// the sessionWorkers scan, so a delegate spawn is imminent EVIDENCE, not a
// fabricated stand-in for it. spawnSubTurn clears the marker the instant the
// child registers (or on any early return that never reaches registration —
// see its own registeredForCancel/pendingSpawnKeysForThisCall).
//
// This test drives the REAL production dispatch chain end to end:
// DelegateTool.Execute(action="run", async=true) — exactly what a real LLM
// tool call would invoke — with NO test-only priming helper anywhere in it.
// The only thing this test controls that production does not is WHEN the
// spawned goroutine is allowed to proceed past its concurrency-semaphore
// wait (parentTS.concurrencySem, pre-filled to capacity below): that
// substitutes a deterministic hand-off for what is, in production, a real
// but narrow goroutine-scheduling race — the same "delegate's own turn
// already finished, child not yet registered" window T24a hit live, just
// pinned open long enough for this test to observe it without flakiness. It
// asserts the child sub-turn is still unblocked (proving the pre-arm latch
// was honored) and that a turn_canceled transcript entry lands with a
// non-empty DescendantsCancelled — the exact assertion T24a makes.
func TestRepro_AsyncDelegateCancel_ArmsBeforeChildRegisters(t *testing.T) {
	// No "main" sentinel to fall back to anymore — self-delegation below
	// resolves the parent's own agent identity via the registry, so cfg
	// must register a REAL agent for this to succeed.
	const testAgentID = "mia"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: testAgentID, Name: "Mia"}},
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
	meta, err := store.NewSession(session.SessionTypeChat, "web", testAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	// Real production DelegateTool, wired the same way loop.go's own
	// registerSharedTools wiring pass does (NewDelegateTool + SetSpawner(
	// NewSubTurnSpawner(al))) — that SAME SetSpawner call ALSO wires the
	// real DelegateSpawnMarker automatically (see delegate.go's SetSpawner
	// doc comment for the type-assertion mechanism), which is the exact
	// production behavior this test exercises. A permissive background
	// deny-checker is wired so the call reaches the spawner at all (the
	// gate fails CLOSED when unwired, by design — unrelated to this fix).
	delegateTool := tools.NewDelegateTool("slow-model", 0, 0)
	delegateTool.SetSpawner(NewSubTurnSpawner(al))
	// ADR-057 U14 fixture repair: DelegateTool now refuses to start a
	// delegated session without a durable lifecycle store wired (fail-closed
	// rather than an untracked, unrecoverable session) — mirrors
	// message_parent_real_context_test.go's wiring.
	lifecycleStore := session.NewLifecycleStore(t.TempDir())
	delegateTool.SetLifecycleStore(lifecycleStore)
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)

	parentTS := &turnState{
		turnID:              "parent-async-cancel-repro",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair `[grill C-1]`: RequestCancel's
		// role-B predicates (GetActiveTurnHookForSession, the pre-arm
		// consumption path) now match on routingSessionID, not
		// transcriptSessionID.
		routingSessionID: session.RoutingSessionID(sessionID),
		agentID:          testAgentID,
		channel:          "web",
		chatID:           "main",
		depth:            0,
		session:          newEphemeralSession(nil),
		pendingResults:   make(chan *tools.ToolResult, 16),
		// Deliberately capacity 1 and pre-filled (below) to capacity, so
		// spawnSubTurn's own semaphore acquisition (step 0) deterministically
		// BLOCKS until this test releases a slot — the child is PROVABLY not
		// yet registered at the moment this test issues its Stop click, with
		// no dependence on goroutine-scheduling luck.
		concurrencySem: make(chan struct{}, 1),
		al:             al,
		finishedChan:   make(chan struct{}),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(context.Background())
	parentTS.concurrencySem <- struct{}{} // fill the size-1 buffer: any send now blocks

	// Mimic the real timeline: the parent turn registers, then finishes and
	// clears itself (its own quick "OK, delegating..." follow-up completed)
	// BEFORE the backgrounded async goroutine has done anything at all.
	al.activeTurnStates.Store(sessionID, parentTS)
	al.activeTurnStates.Delete(sessionID)

	// Build the same ctx shape a real tool dispatch carries (loop.go's
	// runTurn injects AgentLoop/turnState/agentID/transcriptSessionID/
	// channel+chatID before every tool call — see
	// pkg/tools/registry.go's ExecuteWithContext and loop.go's turnCtx
	// construction).
	ctx := WithAgentLoop(context.Background(), al)
	ctx = withTurnState(ctx, parentTS)
	ctx = withSpawnToolCallID(ctx, "call-async-cancel-repro")
	ctx = tools.WithAgentID(ctx, parentTS.agentID)
	ctx = tools.WithTranscriptSessionID(ctx, sessionID)
	ctx = tools.WithToolContext(ctx, parentTS.channel, parentTS.chatID)

	// Drive the REAL production dispatch: DelegateTool.Execute(action="run",
	// async=true — the default). Unlike the two prior passes at this test,
	// nothing here fabricates dispatcher evidence: executeAsync's own
	// MarkPendingDelegateSpawn call (synchronous, before its `go func`) is
	// what makes the upcoming RequestCancel see this spawn as imminent.
	execResult := delegateTool.Execute(ctx, map[string]any{
		"task":  "background task for the pre-arm cancel repro",
		"async": true,
	})
	require.NotNil(t, execResult)
	require.False(t, execResult.IsError, "delegate run must be accepted: %+v", execResult)

	// Execute has returned, so the mark call — synchronous, inside
	// executeAsync, strictly before its own `go func` — is GUARANTEED to
	// have already happened. The spawned goroutine may or may not have
	// started, but even if it has, it is GUARANTEED to be blocked at
	// spawnSubTurn's semaphore acquisition: parentTS.concurrencySem is a
	// full, size-1 buffered channel, and nothing has received from it yet.
	// Registration is therefore PROVABLY unreached — there is no scheduling
	// race left to lose.

	// REAL production Stop-button path, arriving in the window where NEITHER
	// the parent NOR the not-yet-spawned child is registered. Must ARM a
	// latch rather than silently no-op — via the REAL pending-spawn marker
	// executeAsync just recorded, not a test-fabricated sessionWorker.
	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.False(t, outcome.Fired, "nothing is registered yet — RequestCancel must not report Fired")
	require.True(t, outcome.Armed, "RequestCancel must arm a pre-registration latch when nothing is registered")

	// Release the semaphore slot: the blocked spawnSubTurn goroutine can now
	// proceed to registration, where it consumes the latch armed above.
	<-parentTS.concurrencySem

	// The child MUST unblock well within the hard-abort/detach ladder if the
	// armed latch was honored. If this times out, the async delegate cancel
	// cascade is broken — the e2e T24a regression.
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
	}, 15*time.Second, 50*time.Millisecond,
		"transcript must contain a turn_canceled entry with a non-empty descendants_canceled array "+
			"— the async delegate cancel cascade is stalled (e2e T24a regression)")

	// Drain the background goroutine before the test (and its al.Close
	// cleanup) returns, mirroring WaitForAsyncTasks' own documented purpose.
	waitDone := make(chan struct{})
	go func() {
		delegateTool.WaitForAsyncTasks()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(15 * time.Second):
		t.Fatal("FAIL: the async delegate goroutine did not finish unwinding after cancellation")
	}
}
