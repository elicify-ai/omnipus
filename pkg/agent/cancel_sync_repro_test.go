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

// TestRepro_SyncDelegateCancel_RequestCancel asserts that a SYNC (async=false)
// sub-turn — the exact path executeSync/delegate async=false takes, where the
// child runs INLINE on the parent's goroutine — unblocks after a REAL
// RequestCancel (the production Stop-button path) within the hard-abort window.
//
// This is the e2e T24b scenario (cancel cascades to awaited subagent). The
// sibling TestSpawnSubTurn_ExplicitCancelViaRequestCancel covers Async:true
// only; there is no existing Async:false + real-timer coverage.
func TestRepro_SyncDelegateCancel_RequestCancel(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			// Home MUST be an isolated t.TempDir() — an empty Home resolves the
			// agent workspace/session-store base to the process's current
			// working directory (verified: "Failed to create agent workspace
			// directory error=\"mkdir : no such file or directory\" workspace="),
			// which both leaks real session dirs into the repo tree across runs
			// and makes spawnSubTurn's child registration flaky under load.
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}, Home: t.TempDir()},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	// Long enough the child never completes on its own before cancel fires.
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
		turnID:              "parent-sync-cancel-repro",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: GetActiveTurnHookForSession
		// (the role-B predicate RequestCancel's ClaimCancel gate uses) now
		// matches on routingSessionID, not transcriptSessionID.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		session:          newEphemeralSession(nil),
		pendingResults:   make(chan *tools.ToolResult, 16),
		concurrencySem:   make(chan struct{}, testMaxConcurrentSubTurns),
		al:               al,
		finishedChan:     make(chan struct{}),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)
	al.activeTurnStates.Store(sessionID, parentTS)
	defer al.activeTurnStates.Delete(sessionID)

	// Mimic the production SYNC path: the parent goroutine is BLOCKED inside
	// spawnSubTurn(async=false) — executeSync calls SpawnSubTurn inline. We
	// launch it on a goroutine here only so the test can drive RequestCancel
	// concurrently; the spawn itself runs with Async:false (child inline).
	spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-sync-cancel-repro")
	subCfg := SubTurnConfig{Model: "slow-model", Async: false, Critical: true}

	var wg sync.WaitGroup
	wg.Add(1)
	spawnDone := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(spawnDone)
		_, _ = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	// Wait for the child to register on the parent before canceling.
	require.Eventually(t, func() bool {
		parentTS.mu.RLock()
		defer parentTS.mu.RUnlock()
		return len(parentTS.childTurnIDs) > 0
	}, 2*time.Second, 5*time.Millisecond, "child sub-turn must register before cancel")

	// REAL production Stop-button path: RequestCancel starts the graceful
	// cascade immediately and the hard-abort timer at t=3s.
	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.True(t, outcome.Fired, "RequestCancel must fire for the active parent turn")

	// The spawn MUST return (parent unblocks) within a generous window that
	// covers the 3s graceful + 3s hard-abort timer. If this hangs, the sync
	// cancel cascade is broken — the e2e T24b regression.
	select {
	case <-spawnDone:
		// PASS: sync spawn unblocked after cancel.
	case <-time.After(15 * time.Second):
		t.Fatal("FAIL: sync delegate (async=false) spawnSubTurn did NOT unblock within 15s " +
			"after RequestCancel — the awaited-subagent cancel cascade is stalled (e2e T24b regression)")
	}
	wg.Wait()
}
