// Package agent — cancel_race_test.go
//
// TestRequestCancel_CallbackFiresWhenFinishRacesRegistration is a regression
// test for the race where Finish() runs between ClaimCancel() and
// SetOnCancelFinish().
//
// Race window (pre-fix):
//   1. ClaimCancel() sets cancelFired=true.
//   2. InterruptSession spawns goroutines that call providerCancel() → the
//      agent goroutine unwinds and reaches Finish(false).
//   3. SetOnCancelFinish(...) registers the callback.
//
// If Finish() fires between steps 2 and 3, it sees cancelFired==true but
// onCancelFinish==nil and returns immediately. The callback is then registered
// (step 3) but Finish() never runs a second time, so the callback is
// permanently orphaned: no turn_canceled transcript entry, no audit event.
//
// Fix (Option A): collectDescendantTurnIDs + SetOnCancelFinish BEFORE
// InterruptSession so the callback is always registered before any goroutine
// awakened by InterruptSession can reach Finish().
//
// Test strategy: inject a providerCancel that synchronously calls Finish(false)
// from inside InterruptSession's goroutine (the worst-case race scenario),
// then assert the transcript entry is still written correctly.

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

// TestRequestCancel_CallbackFiresWhenFinishRacesRegistration — drives the
// race where Finish() is called synchronously from inside InterruptSession's
// providerCancel callback, BEFORE the old code would have registered
// SetOnCancelFinish. The fix registers the callback first, so the turn_canceled
// transcript entry must always be written regardless of when Finish() fires.
func TestRequestCancel_CallbackFiresWhenFinishRacesRegistration(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "race-test-model",
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
	require.NotNil(t, store, "shared session store must be non-nil")

	// Create a real session so ResolveSessionStore finds it and the transcript
	// JSONL is written to a real file path.
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID
	require.NotEmpty(t, sessionID)

	// Inject a synthetic root turnState that simulates the worst-case race:
	// providerCancel calls Finish(false) synchronously from inside
	// InterruptSession's goroutine. With the bug, this fires Finish before
	// SetOnCancelFinish is called, orphaning the callback. With the fix,
	// SetOnCancelFinish is called before InterruptSession, so Finish always
	// finds the registered callback.
	ts := &turnState{
		turnID:              "turn-race-001",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	// Wire providerCancel to immediately call Finish(false), simulating the
	// agent goroutine unwinding instantly when its HTTP stream is canceled.
	ts.providerCancel = func() {
		ts.Finish(false)
	}

	al.activeTurnStates.Store(sessionID, ts)
	defer al.activeTurnStates.Delete(sessionID)

	// Call the real RequestCancel. With the fix, the execution order is:
	//   1. collectDescendantTurnIDs — captures descendants (["turn-race-001"])
	//   2. SetOnCancelFinish — registers the transcript callback under ts.mu
	//   3. InterruptSession — fires providerCancel → Finish(false) runs
	//      synchronously, finds onCancelFinish != nil, calls it → transcript written
	//
	// With the bug (pre-fix), the execution order was:
	//   1. InterruptSession — fires providerCancel → Finish(false) runs, sees
	//      onCancelFinish==nil, returns immediately, callback is lost
	//   2. SetOnCancelFinish — registers callback, but Finish will never run again
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "race-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired,
		"RequestCancel must fire for an active session with a registered turn")
	require.Equal(t, "turn-race-001", outcome.TurnID)

	// Allow a brief window for any async work to settle (the callback itself is
	// synchronous in the Finish() call, but AppendTranscript writes via
	// fileutil.WriteFileAtomic which is also synchronous — 100ms is ample).
	time.Sleep(100 * time.Millisecond)

	// Assert: the turn_canceled transcript entry must be written despite the race.
	// With the bug this entry would be missing (callback was orphaned).
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "transcript must be readable after cancel + race-Finish")

	var cancelledEntry *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeTurnCancelled {
			cp := entries[i]
			cancelledEntry = &cp
			break
		}
	}

	require.NotNil(t, cancelledEntry,
		"BUG REGRESSION: turn_canceled TranscriptEntry was not written — "+
			"callback was orphaned (Finish ran before SetOnCancelFinish). "+
			"Fix: register callback before calling InterruptSession. "+
			"entries found: %v", entries)

	assert.Equal(t, session.EntryTypeTurnCancelled, cancelledEntry.Type)
	assert.Equal(t, "turn-race-001", cancelledEntry.TurnID,
		"TurnID must match the canceled turn")
	assert.Equal(t, "race-user", cancelledEntry.CancelledByUser,
		"CancelledByUser must match the canceller passed to RequestCancel")
	assert.Equal(t, "web", cancelledEntry.CancelledByChannel,
		"CancelledByChannel must match the canceller channel")
	assert.Equal(t, "graceful", cancelledEntry.CancelMethod,
		"CancelMethod must be 'graceful' — providerCancel→Finish(false=isHardAbort)")
}
