// Package agent — cancel_subagent_cascade_test.go
//
// T20b: Spawn a real sub-turn (depth >=1) under a parent turn, call
// RequestCancel on the parent's session, and assert that BOTH parent and
// descendant are interrupted. The parent transcript entry must have
// DescendantsCancelled listing the child turn ID.
//
// Theater smell fixed: the existing TestHandleCancel_InterruptSessionGatedByClaimCancel
// only checks that ClaimCancel returns false on a second call — no actual
// sub-turn cascade is involved.
//
// How cancel handles sub-turns:
//   - RequestCancel calls InterruptSession which signals ALL turns sharing the
//     session ID (parent + all descendants via activeTurnStates Range).
//   - The onCancelFinish callback is registered ONLY on the root/parent turn
//     (the one returned by GetActiveTurnHookForSession). That callback writes
//     ONE turn_canceled transcript entry for the parent, with the
//     DescendantsCancelled field listing ALL interrupted sub-turn IDs.
//   - Sub-turns do NOT get separate turn_canceled entries (they are listed
//     under the parent's DescendantsCancelled).
//
// This test:
//  1. Creates a parent (depth=0) and child (depth=1) turnState sharing a session.
//  2. Calls RequestCancel — asserts Descendants contains both IDs.
//  3. Triggers parent.Finish() to fire onCancelFinish.
//  4. Reads the transcript: one turn_canceled entry for the parent, with
//     DescendantsCancelled containing both turn IDs.
//
// Traces to: pkg/agent/cancel.go:168 InterruptSession (cascade),
//            pkg/agent/cancel.go:181-213 onCancelFinish (transcript write).
// Spec ref: FR-10, FR-12, FR-13a.

package agent

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// TestCancel_SubAgentCascade (T20b) verifies that RequestCancel on a session
// with a parent turn (depth=0) and a child turn (depth=1) both sharing the
// same transcriptSessionID:
//   - Returns Fired=true, TurnID=parent, Descendants=[parent, child].
//   - After parent.Finish(), writes exactly one turn_canceled entry whose
//     DescendantsCancelled contains both the parent and child turn IDs.
func TestCancel_SubAgentCascade(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	workspaceDir := tmpDir + "/workspace"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "cascade-test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	// Use the agent loop's shared session store.
	store := al.GetSessionStore()
	require.NotNil(t, store, "agent loop shared session store must be non-nil")

	// Create a real session so ResolveSessionStore can find it.
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	require.NoError(t, err)
	sessionID := meta.ID
	require.NotEmpty(t, sessionID)

	// Inject a parent turnState (depth=0).
	parentTS := &turnState{
		turnID:              "turn-parent-T20b",
		transcriptSessionID: sessionID,
		depth:               0,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	al.activeTurnStates.Store(sessionID, parentTS)
	defer al.activeTurnStates.Delete(sessionID)

	// Inject a child turnState (depth=1, same transcriptSessionID).
	childTS := &turnState{
		turnID:              "turn-child-T20b",
		transcriptSessionID: sessionID,
		depth:               1,
		parentTurnID:        "turn-parent-T20b",
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	childKey := sessionID + ":child"
	al.activeTurnStates.Store(childKey, childTS)
	defer al.activeTurnStates.Delete(childKey)

	// Call RequestCancel on the parent session.
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "test-channel"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "cancel must fire on an active session with turns")
	assert.Equal(t, "turn-parent-T20b", outcome.TurnID,
		"outcome.TurnID must be the root turn (depth=0) preferred by GetActiveTurnHookForSession")

	// ASSERT: descendants must include BOTH turn IDs.
	descendantsSorted := append([]string(nil), outcome.Descendants...)
	sort.Strings(descendantsSorted)
	require.Len(t, descendantsSorted, 2,
		"InterruptSession cascade must signal both parent and child; got: %v", descendantsSorted)
	assert.Contains(t, descendantsSorted, "turn-parent-T20b",
		"Descendants must include the parent turn ID")
	assert.Contains(t, descendantsSorted, "turn-child-T20b",
		"Descendants must include the child turn ID")

	// Trigger parent Finish to fire the onCancelFinish callback (which writes
	// the turn_canceled transcript entry with DescendantsCancelled).
	parentTS.Finish(false)

	// 100ms grace period for the synchronous callback to flush to disk.
	time.Sleep(100 * time.Millisecond)

	// ASSERT: exactly ONE turn_canceled entry for the parent, containing
	// both turn IDs in DescendantsCancelled.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var cancelledEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeTurnCancelled {
			cancelledEntries = append(cancelledEntries, e)
		}
	}

	require.Len(t, cancelledEntries, 1,
		"RequestCancel writes ONE turn_canceled entry (for the root/parent turn); "+
			"sub-turns are listed in DescendantsCancelled. Got entries: %v",
		cancelledEntries)

	rootEntry := cancelledEntries[0]
	assert.Equal(t, "turn-parent-T20b", rootEntry.TurnID,
		"the single turn_canceled entry must be for the parent turn")
	assert.Equal(t, "test-user", rootEntry.CancelledByUser)
	assert.Equal(t, "test-channel", rootEntry.CancelledByChannel)
	assert.Equal(t, "graceful", rootEntry.CancelMethod)

	// ASSERT: DescendantsCancelled must list both turns.
	descSorted := append([]string(nil), rootEntry.DescendantsCancelled...)
	sort.Strings(descSorted)
	require.Len(t, descSorted, 2,
		"DescendantsCancelled must contain both parent and child turn IDs; got: %v", descSorted)
	assert.Contains(t, descSorted, "turn-parent-T20b",
		"DescendantsCancelled must include the parent turn ID")
	assert.Contains(t, descSorted, "turn-child-T20b",
		"DescendantsCancelled must include the child turn ID")

	// DIFFERENTIATION: if we called RequestCancel on a session with ONLY the
	// parent turn (no child), DescendantsCancelled would have 1 entry. The
	// 2-entry check above proves the cascade walked both turns.
}
