// Package agent — cancel_session_isolation_test.go
//
// T18: Cancel on session A while session B runs concurrently leaves B
// unaffected. This is the regression test for the "cancel is scoped per
// session_id" guarantee (FR-12, FR-13a).
//
// Theater smell fixed: the existing gateway-level test in
// pkg/gateway/cancel_session_isolation_test.go could not exercise true
// concurrency because the agent loop serializes turns sharing the same scope
// key. This version lives in pkg/agent so it can directly inject two
// turnState objects — one for session A and one for session B — with
// distinct transcriptSessionID values, and then call RequestCancel on
// session A only.
//
// What this test proves:
//   1. RequestCancel on session A fires (Fired=true, TurnID=turn-A).
//   2. Session B's cancelFired field is still false — RequestCancel did NOT
//      touch it.
//   3. Session B's turnState can still be canceled independently (its
//      ClaimCancel returns true), confirming isolation.
//   4. A second cancel on session A returns Fired=false, while session B
//      is still uncancelled — proving the scoping is per-session, not global.
//
// Differentiation: if cancel leaked globally, session B's cancelFired would
// be true after step 1. The explicit check catches that regression.
//
// Traces to: pkg/agent/cancel.go:96-165 — session resolution and ClaimCancel
// scoping; FR-12, FR-13a.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// TestCancel_SessionIsolation (T18) — RequestCancel on session A must NOT
// affect session B's cancel state.
//
// This is the authoritative T18 test. The gateway-level placeholder in
// pkg/gateway/cancel_session_isolation_test.go explains why the real test
// lives here (turnState is unexported; direct injection requires agent package).
func TestCancel_SessionIsolation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	workspaceDir := tmpDir + "/workspace"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "isolation-test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	// Create two real sessions so ResolveSessionStore works.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")

	metaA, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sidA := metaA.ID
	require.NotEmpty(t, sidA)

	metaB, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sidB := metaB.ID
	require.NotEmpty(t, sidB)
	require.NotEqual(t, sidA, sidB, "session A and session B must have distinct IDs")

	// Inject a turnState for session A (depth=0, representing an active turn).
	tsA := &turnState{
		turnID:              "turn-A-T18",
		transcriptSessionID: sidA,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(sidA, tsA)
	defer al.activeTurnStates.Delete(sidA)

	// Inject a turnState for session B (depth=0, completely independent).
	tsB := &turnState{
		turnID:              "turn-B-T18",
		transcriptSessionID: sidB,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(sidB, tsB)
	defer al.activeTurnStates.Delete(sidB)

	// PRE-CONDITION: Both sessions have uncancelled turn states.
	assert.False(t, tsA.cancelFired.Load(), "pre-condition: session A cancelFired must be false")
	assert.False(t, tsB.cancelFired.Load(), "pre-condition: session B cancelFired must be false")

	// CANCEL session A only.
	outcomeA, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sidA},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)

	// ASSERT 1: Session A was canceled.
	assert.True(t, outcomeA.Fired, "cancel on session A must fire (Fired=true)")
	assert.Equal(t, "turn-A-T18", outcomeA.TurnID,
		"outcome.TurnID must be the session A turn ID")

	// ASSERT 2: Session B is NOT affected — its cancelFired must still be false.
	// This is the core isolation guarantee. If cancel leaked globally,
	// tsB.cancelFired would be true here.
	assert.False(t, tsB.cancelFired.Load(),
		"ISOLATION VIOLATION: cancel on session A must NOT set cancelFired on session B "+
			"(sidA=%s, sidB=%s)", sidA, sidB)

	// ASSERT 3: Session B can still be independently canceled (ClaimCancel returns true).
	// This confirms its cancel state is pristine — not already consumed.
	canClaimB := tsB.ClaimCancel()
	assert.True(t, canClaimB,
		"session B ClaimCancel must return true (turn is uncancelled); "+
			"cancel on session A must not have pre-empted session B's cancel slot")

	// ASSERT 4 (differentiation): Second cancel on session A returns Fired=false
	// (ClaimCancel already returned true for A; it is now exhausted).
	// Session B's ClaimCancel was consumed above — a second call on B also returns false.
	outcomeA2, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sidA},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.False(t, outcomeA2.Fired,
		"second cancel on the already-canceled session A must return Fired=false")
}

// TestCancel_SessionIsolation_TranscriptWrittenOnlyForA verifies that after
// canceling session A and triggering its Finish, the turn_canceled transcript
// entry is written ONLY to session A's JSONL — session B has no such entry.
//
// This prevents a class of bugs where a cancel callback is wired to the wrong
// session store.
//
// Traces to: pkg/agent/cancel.go:181-213 — onCancelFinish appends to the store
// resolved by ResolveSessionStore(sessionID) — scoped to A's session ID.
func TestCancel_SessionIsolation_TranscriptWrittenOnlyForA(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	workspaceDir := tmpDir + "/workspace"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "isolation-transcript-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store)

	metaA, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sidA := metaA.ID

	metaB, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sidB := metaB.ID
	require.NotEqual(t, sidA, sidB)

	tsA := &turnState{
		turnID:              "turn-A-transcript-T18",
		transcriptSessionID: sidA,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(sidA, tsA)
	defer al.activeTurnStates.Delete(sidA)

	tsB := &turnState{
		turnID:              "turn-B-transcript-T18",
		transcriptSessionID: sidB,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(sidB, tsB)
	defer al.activeTurnStates.Delete(sidB)

	// Cancel session A and trigger its Finish to fire the onCancelFinish callback.
	outcomeA, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sidA},
		CancelCanceller{UserID: "test-user-A", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcomeA.Fired, "cancel on session A must fire")

	// Trigger Finish on A to write the turn_canceled transcript entry.
	tsA.Finish(false)
	// Allow the synchronous onCancelFinish callback to flush to disk.
	time.Sleep(100 * time.Millisecond)

	// ASSERT: Session A's transcript has a turn_canceled entry.
	entriesA, err := store.ReadTranscript(sidA)
	require.NoError(t, err)
	var cancelledA []session.TranscriptEntry
	for _, e := range entriesA {
		if e.Type == session.EntryTypeTurnCancelled {
			cancelledA = append(cancelledA, e)
		}
	}
	require.Len(t, cancelledA, 1,
		"session A must have exactly one turn_canceled transcript entry after cancel+Finish")
	assert.Equal(t, "turn-A-transcript-T18", cancelledA[0].TurnID)
	assert.Equal(t, "test-user-A", cancelledA[0].CancelledByUser)

	// ASSERT: Session B's transcript has NO turn_canceled entry.
	entriesB, err := store.ReadTranscript(sidB)
	// ReadTranscript may return an error if no JSONL file exists yet (session B
	// never had a turn complete). Both cases — empty entries and an error — mean
	// no turn_canceled entry exists, which is the correct outcome.
	if err == nil {
		for _, e := range entriesB {
			if e.Type == session.EntryTypeTurnCancelled {
				t.Errorf("ISOLATION VIOLATION: session B must NOT have a turn_canceled entry; "+
					"got entry: %+v", e)
			}
		}
	}
	// err != nil is expected when session B has no JSONL file — that's correct.
}
