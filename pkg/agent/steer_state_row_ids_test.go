// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// subagentStateRowIDs returns, in transcript order, the ids of every persisted
// subagent_state row of the parent whose frame reports state.
func subagentStateRowIDs(t *testing.T, al *AgentLoop, parentID, state string) []string {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	require.NoError(t, err)
	var ids []string
	for _, e := range entries {
		if e.SystemSubtype == session.SystemSubtypeSubagentState && e.SubagentState != nil && e.SubagentState.State == state {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// BDD: Given a steered child that was launched (queued), run and then STOPPED,
// When a person re-queues it through the public same-generation resume,
// Then the parent transcript holds two `queued` subagent_state rows with two
// DISTINCT ids - a repeated id crashes the chat (assistant-ui refuses a message
// id it already holds).
//
// The public resume leaves the record's execution identity unstamped and clears
// the stop note, so the id built from origin call + generation + state alone was
// identical to the launch-time `queued` row.
func TestSubagentState_RequeueAfterStopGetsADistinctRowID(t *testing.T) {
	al, provider := setupQueuedReplacementHarness(t)
	lifecycle := al.GetSessionLifecycleStore()
	parentID := newTestSteeringSession(t, al, "ws-dupid-requeue")

	childRec, _, _ := dispatchParkedRunningChild(t, al, provider, parentID, "call-dupid-requeue", queuedReplOldTask)
	childID, childGen := childRec.SessionID, childRec.Generation
	require.Equal(t, []string{"call-dupid-requeue:1:state:queued"}, subagentStateRowIDs(t, al, parentID, "queued"),
		"premise: launch persisted exactly one queued row")

	owner := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dupid-owner"}
	_, err := al.steerCanceller().CancelSubtree(context.Background(), childID, owner)
	require.NoError(t, err)
	waitRecordState(t, lifecycle, childID, "the stop to land stopped", func(rec *session.LifecycleRecord) bool {
		return rec.State == session.LifecycleStopped && rec.StopNote != nil
	})

	// Production wiring (gateway_boot.go): the canceller reports the state the
	// revival durably landed through WriteSteerRevivalState.
	resumer := NewSteerCanceller(lifecycle, al.SteerGenerationCancel).SetRevivalStateWriter(al.WriteSteerRevivalState)
	resumedGen, err := resumer.Revive(context.Background(), childID, owner)
	require.NoError(t, err)
	require.Equal(t, childGen, resumedGen, "premise: the resume keeps the generation")

	queued := subagentStateRowIDs(t, al, parentID, "queued")
	require.Len(t, queued, 2, "launch and re-queue each persist a queued row")
	require.NotEqual(t, queued[0], queued[1], "two queued rows must not share one message id")

	// Stable for one transition: re-computing the discriminator for the same
	// re-queued record names the same stop, so a replay of the same frame
	// carries the same id (it does not mint a new one).
	requeued, err := lifecycle.Load(childID)
	require.NoError(t, err)
	first := al.requeueIDSuffix(requeued)
	require.NotEmpty(t, first, "a child re-queued after a landed stop must carry the stop in its row id")
	require.Equal(t, first, al.requeueIDSuffix(requeued))
	require.Equal(t, queued[1], "call-dupid-requeue:1:state:queued"+first)

	// And no id repeats anywhere in the parent transcript.
	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, e := range entries {
		if e.ID == "" {
			continue
		}
		require.Falsef(t, seen[e.ID], "duplicate transcript id %q", e.ID)
		seen[e.ID] = true
	}
}
