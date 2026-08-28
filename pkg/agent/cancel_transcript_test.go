// Package agent — cancel_transcript_test.go
//
// T3: Drives cancel through the real RequestCancel path and asserts a
// turn_canceled TranscriptEntry lands in the day-partitioned JSONL with
// the correct canceled_by_user, canceled_by_channel, and cancel_method.
//
// Theater smell fixed: old test built a transcript entry by hand and wrote it
// directly — it never called RequestCancel at all. This version calls
// RequestCancel directly with a real turnState (injected into activeTurnStates)
// so the onCancelFinish callback runs through the real cancel state machine.
//
// Why pkg/agent and not pkg/gateway?
//   - The gateway-level test relies on WebSocket infrastructure and a blocking
//     LLM provider. The blocking provider returns when ctx.Done() fires, but
//     when run in sequence with other tests (e.g. -count=N or the full test
//     suite), the context can be pre-canceled, causing the turn to finish before
//     the cancel claim. The result is a flaky test: passes in isolation, fails
//     when preceded by another gateway cancel test.
//   - This version injects a synthetic turnState directly into activeTurnStates,
//     calls RequestCancel, then calls parentTS.Finish(false) to trigger the
//     onCancelFinish callback synchronously. No WebSocket infrastructure needed.
//   - The result is deterministic regardless of test ordering.
//
// Traces to: pkg/agent/cancel.go:181-213 — activeTurn.SetOnCancelFinish callback
// that appends session.TranscriptEntry{Type: EntryTypeTurnCancelled}.
// Spec ref: FR-10, FR-12, FR-13a.

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

// TestCancel_TranscriptTurnCancelledEntry (T3) — calls RequestCancel on a
// session with an active turnState (depth=0) and asserts that:
//  1. The onCancelFinish callback fires when Finish(false) is called.
//  2. A turn_canceled TranscriptEntry is written to the JSONL.
//  3. The entry has CancelledByUser, CancelledByChannel, CancelMethod "graceful".
//  4. The TurnID matches the canceled turn.
//
// This test is deterministic: it does not rely on network or timer races.
// The onCancelFinish callback runs synchronously in the Finish() call.
//
// Theater smell: the original version built a transcript entry by hand and
// called store.AppendTranscript directly — it never called RequestCancel at all.
// This version uses the real cancel state machine (RequestCancel → ClaimCancel →
// SetOnCancelFinish → Finish → onCancelFinish callback → AppendTranscript).
func TestCancel_TranscriptTurnCancelledEntry(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	workspaceDir := tmpDir + "/workspace"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "transcript-cancel-model"},
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
	require.NotNil(t, store, "shared session store must be non-nil")

	// Create a real session so ResolveSessionStore can find it.
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID
	require.NotEmpty(t, sessionID)

	// Inject a synthetic root turnState (depth=0) into activeTurnStates.
	// This simulates an in-flight turn — the cancel state machine requires
	// an active turnState to fire (otherwise ClaimCancel returns false and
	// no callback is registered).
	// ADR-057 fixture repair: the role-B predicates (GetActiveTurnHookForSession
	// et al.) match on routingSessionID, not transcriptSessionID.
	ts := &turnState{
		turnID:              "turn-T3-transcript",
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(sessionID, ts)
	defer al.activeTurnStates.Delete(sessionID)

	// Call the real RequestCancel — this is the actual cancel state machine:
	//   1. Validates scope
	//   2. ClaimCancel() — sets cancelFired=true atomically
	//   3. Emits turn_cancel_attempt audit event
	//   4. Calls InterruptSession (sends graceful interrupt signal)
	//   5. Calls SetOnCancelFinish — registers the transcript callback
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "dev-token", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired,
		"RequestCancel must fire (Fired=true) for an active session with a registered turn")
	require.Equal(t, "turn-T3-transcript", outcome.TurnID,
		"TurnID must be the root turn ID registered in activeTurnStates")

	// ASSERT: cancelFired is now true (ClaimCancel was called by RequestCancel).
	assert.True(t, ts.cancelFired.Load(),
		"cancelFired must be true after RequestCancel — ClaimCancel sets it atomically")

	// Trigger Finish(false) to fire the onCancelFinish callback.
	// Finish() checks cancelFired.Load() → calls onCancelFinish("graceful").
	// The callback runs synchronously inside Finish(), so the transcript entry
	// is written before Finish returns.
	ts.Finish(false)

	// Allow disk flush — the callback calls store.AppendTranscript which uses
	// fileutil.WriteFileAtomic (temp-file + rename). 100ms is ample.
	time.Sleep(100 * time.Millisecond)

	// ASSERT: a turn_canceled entry is written to the JSONL transcript.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "transcript must be readable after cancel+Finish")

	var cancelledEntry *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeTurnCancelled {
			cp := entries[i]
			cancelledEntry = &cp
			break
		}
	}

	require.NotNil(t, cancelledEntry,
		"a turn_canceled TranscriptEntry must be written to the JSONL by the onCancelFinish callback; "+
			"entries found: %v", entries)

	// ASSERT: entry fields match the cancel call parameters.
	assert.Equal(t, session.EntryTypeTurnCancelled, cancelledEntry.Type,
		"entry type must be 'turn_canceled'")
	assert.Equal(t, "turn-T3-transcript", cancelledEntry.TurnID,
		"TurnID must match the canceled turn")
	assert.Equal(t, "dev-token", cancelledEntry.CancelledByUser,
		"CancelledByUser must match the canceller.UserID from RequestCancel")
	assert.Equal(t, "web", cancelledEntry.CancelledByChannel,
		"CancelledByChannel must match the canceller.Channel from RequestCancel")
	assert.Equal(t, "graceful", cancelledEntry.CancelMethod,
		"CancelMethod must be 'graceful' because Finish(false=isHardAbort) was called")

	// DIFFERENTIATION: a DIFFERENT cancel call with different canceller credentials
	// produces a DIFFERENT entry — proving the callback uses the captured closure
	// parameters, not hardcoded values.
	meta2, err := store.NewSession(session.SessionTypeChat, "cli", "main")
	require.NoError(t, err)
	sessionID2 := meta2.ID

	// ADR-057 fixture repair: see the first turnState literal above.
	ts2 := &turnState{
		turnID:              "turn-T3-diff",
		transcriptSessionID: sessionID2,
		routingSessionID:    session.RoutingSessionID(sessionID2),
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(sessionID2, ts2)
	defer al.activeTurnStates.Delete(sessionID2)

	outcome2, err2 := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID2},
		CancelCanceller{UserID: "other-user", Channel: "cli"},
		CancelHooks{},
	)
	require.NoError(t, err2)
	require.True(t, outcome2.Fired)

	ts2.Finish(false)
	time.Sleep(100 * time.Millisecond)

	entries2, err := store.ReadTranscript(sessionID2)
	require.NoError(t, err)
	var cancelledEntry2 *session.TranscriptEntry
	for i := range entries2 {
		if entries2[i].Type == session.EntryTypeTurnCancelled {
			cp := entries2[i]
			cancelledEntry2 = &cp
			break
		}
	}
	require.NotNil(t, cancelledEntry2)
	assert.Equal(t, "other-user", cancelledEntry2.CancelledByUser,
		"second cancel's CancelledByUser must differ from first — proves callback captures closure params")
	assert.Equal(t, "cli", cancelledEntry2.CancelledByChannel,
		"second cancel's CancelledByChannel must differ from first")
	assert.NotEqual(t, cancelledEntry.CancelledByUser, cancelledEntry2.CancelledByUser,
		"two different cancellers must produce two different CancelledByUser values (not hardcoded)")
}
