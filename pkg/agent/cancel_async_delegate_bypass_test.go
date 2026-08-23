package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// TestRepro_SpawnSubTurn_RawStoreBypassesPreArmedCancel is a deterministic
// (no goroutine timing, no TTL race) proof of the exact code-level defect
// behind the e2e T24a regression (tests/e2e/cancel-cross-channel.spec.ts:665
// — "cancel cascades to background subagent (delegate async=true)").
//
// spawnSubTurn (pkg/agent/subturn.go) registers the child turn directly via
// `al.activeTurnStates.Store(childID, childTS)` — NOT via registerActiveTurn
// (turn.go). Only registerActiveTurn calls consumePreArmedCancel
// (cancel_prearm.go), so a pre-registration cancel latch armed for this
// session (RequestCancel found no active turn — e.g. because an async
// delegate's own parent turn already finished, the T24a production window)
// is NEVER consumed at the moment the child first becomes visible.
//
// In production the latch usually still gets consumed a bit later, inside
// al.runTurn's OWN internal registerActiveTurn call (loop.go) — but that
// call is not reached until AFTER workspace-dir resolution and everything
// else runTurn does first, widening the window unnecessarily. This test
// removes runTurn from the picture entirely (dispatching to an agent whose
// executor resolves to the reserved "remote-a2a" kind, so spawnSubTurn
// returns before ever calling al.runTurn) to prove — with zero timing
// dependence — that the raw Store, on its own, provides no pre-arm
// protection whatsoever: the latch is still sitting armed after
// spawnSubTurn returns, exactly the state that lets an unlucky TTL race
// swallow a real Stop click (retry #2 of the e2e failure: "the Stop button
// stayed visible" — the turn ran to completion, uncanceled).
//
// Fixing spawnSubTurn to register the child via al.registerActiveTurn
// instead of a bare activeTurnStates.Store closes this at the earliest
// possible moment, regardless of anything runTurn does afterward.
func TestRepro_SpawnSubTurn_RawStoreBypassesPreArmedCancel(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			// Home MUST be an isolated t.TempDir(): AgentHomeBasePath() (and
			// therefore the shared session store's baseDir) resolves an empty
			// Home to the process's current working directory, which every
			// test in this package/binary that leaves Home unset then shares
			// — a deterministic childID like "child-bypass-repro" (below)
			// leaks a real, un-cleaned-up session directory into the repo
			// tree (verified: pkg/agent/sessions/child-bypass-repro existed
			// from a prior run and collided with FR-096's new collision
			// guard) instead of vanishing with t.TempDir()'s cleanup.
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}, Home: t.TempDir()},
			List: []config.AgentConfig{
				{
					ID:   "remote-target",
					Name: "Remote Target",
					// Kind="remote-a2a" is accepted in schema but rejected at
					// dispatch (runner.ResolveDispatch -> ErrRemoteA2AReserved,
					// v0.1.0) — a real, production-supported way to guarantee
					// spawnSubTurn returns WITHOUT ever calling al.runTurn,
					// deterministically, no timing/goroutine race required.
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A},
					},
				},
			},
		},
	}
	msgBus := bus.NewMessageBus()
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
		turnID:              "parent-bypass-repro",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: preArmKeysForTurn
		// (cancel_prearm.go) now derives its "s:"+id consumption key from
		// routingSessionID, not transcriptSessionID — and childTS inherits
		// this field verbatim from parentTS (spawnSubTurn). Left unset, the
		// child's registration would carry no session-id pre-arm key at all,
		// so the latch this test arms under "s:"+sessionID would never be
		// found/consumed regardless of the fix under test.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		session:          newEphemeralSession(nil),
		pendingResults:   make(chan *tools.ToolResult, 16),
		concurrencySem:   make(chan struct{}, testMaxConcurrentSubTurns),
		al:               al,
		finishedChan:     make(chan struct{}),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)
	// Deliberately never registered in al.activeTurnStates — simulating the
	// parent's own turn having already finished (T24a's real race window)
	// before the Stop click's RequestCancel arrives.

	// PREMISE NOTE (design-flaw fix, cancel_prearm.go): see the identical
	// note in cancel_async_delegate_repro_test.go — armCancelOrFindActiveTurn
	// now additionally requires turnImminentForIdentity, whose only signal
	// (sessionWorker inbox/inTurn) does not cover the async-delegate-spawn
	// path this test exercises. Primed explicitly; the resulting gap for
	// this specific path is real and documented in this fix's own report.
	primeImminentSessionWorker(al, sessionID)

	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.False(t, outcome.Fired, "nothing is registered yet — RequestCancel must not report Fired")
	require.True(t, outcome.Armed, "RequestCancel must arm a pre-registration latch when nothing is registered")

	latchKey := "s:" + sessionID
	al.cancelPreArm.mu.Lock()
	_, armedBefore := al.cancelPreArm.latches[latchKey]
	al.cancelPreArm.mu.Unlock()
	require.True(t, armedBefore, "sanity: the latch must be armed under the session-id key before spawnSubTurn runs")

	spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-bypass-repro")
	subCfg := SubTurnConfig{
		Model:             "slow-model",
		TargetAgentID:     "remote-target", // resolves to the reserved remote-a2a dispatch kind
		Async:             true,
		Critical:          true,
		DelegateSessionID: "child-bypass-repro",
	}

	_, spawnErr := spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	require.Error(t, spawnErr, "remote-a2a dispatch must be rejected in v0.1.0")
	require.True(t, errors.Is(spawnErr, runner.ErrRemoteA2AReserved),
		"expected the reserved-dispatch rejection, got: %v", spawnErr)

	// The child WAS momentarily live in al.activeTurnStates (spawnSubTurn's
	// own bookkeeping registers it, then its deferred cleanup deletes it
	// again before returning) but al.runTurn was NEVER invoked — the FIX
	// under test is whether that brief registration alone was enough to
	// consume the pre-armed latch.
	al.cancelPreArm.mu.Lock()
	_, armedAfter := al.cancelPreArm.latches[latchKey]
	al.cancelPreArm.mu.Unlock()
	require.False(t, armedAfter,
		"spawnSubTurn's own child registration must consume the pre-armed cancel latch "+
			"the moment the child becomes visible in al.activeTurnStates, even when the "+
			"subsequent dispatch is rejected before al.runTurn ever runs — a bypassed raw "+
			"Store leaves the latch orphaned until TTL expiry, which is the e2e T24a "+
			"regression (a Stop click that silently never lands)")
}
