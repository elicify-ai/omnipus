// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// window_projection_effects_test.go — Gap 2 (R1 coverage-gaps brief):
// commitWindowProjections (window_projection_effects.go) must propagate a
// failed transcript-rewrite undo, never swallow it. The function already
// does: on a recordWindowProjections failure it calls RestoreWindow and
// returns errors.Join(err, restoreErr). This test forces that failure for
// real (an addressed transcript-line rewrite against a transcript.jsonl
// that was never created — the "addressed transcript is missing" path in
// pkg/session/transcript_rewrite.go::rewriteTranscriptToolCallsAt) and
// proves the caller actually sees it.

//go:build goolm && stdjson

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestCommitWindowProjections_TranscriptUndoFailure_PropagatesNotSwallowed(t *testing.T) {
	al, agent := midTurnFixture(t, 40_000, 0)
	ctx := context.Background()
	key := "cw-gap2-projection-undo"

	store, ok := agent.Sessions.(session.ContextWindowStore)
	require.True(t, ok, "fixture's agent.Sessions must support ContextWindowStore")
	transcriptStore, ok := agent.Sessions.(*session.UnifiedStore)
	require.True(t, ok, "fixture's agent.Sessions must be a *session.UnifiedStore to drive transcriptStore")

	toolMsg := providers.Message{Role: "tool", ToolCallID: "tc-gap2", Content: "a real archived tool result"}
	snap0, err := store.AppendWindowMessage(ctx, key, toolMsg)
	require.NoError(t, err, "seed: AppendWindowMessage")
	require.Equal(t, 1, snap0.State.Count, "precondition: one archived message")
	require.Len(t, snap0.Archive, 1)
	require.Equal(t, "tool", snap0.Archive[0].Role)

	// transcriptSessionID deliberately names a session that was NEVER
	// created via NewSession/AppendTranscript — its transcript.jsonl does
	// not exist on disk. An addressed (TranscriptLine != nil) projection
	// update against a missing transcript file is the real, deterministic
	// failure rewriteTranscriptToolCallsAt reports as "addressed transcript
	// is missing" (pkg/session/transcript_rewrite.go).
	ts := newTurnState(agent, processOptions{
		SessionKey:          key,
		TranscriptSessionID: "gap2-transcript-never-created",
		TranscriptStore:     transcriptStore,
	}, turnEventScope{turnID: "gap2-turn"})

	projKey := memory.ProjectionKey{ToolCallID: "tc-gap2", ArchiveLine: 0}
	newState := snap0.State.Clone()
	newState.Projection.Entries[projKey] = memory.ProjectionEmptied
	line := 3
	newState.Projection.TranscriptLine[projKey] = line

	p := &windowCheckpoint{
		ts:       ts,
		snapshot: session.WindowViewFromSnapshot(snap0),
		state:    newState,
		messages: []providers.Message{toolMsg},
		lines:    []int{0},
	}

	changes, err := al.commitWindowProjections(ctx, p, store)
	require.Error(t, err, "a failed transcript-rewrite undo must be visible to the caller, "+
		"never swallowed into only a log line")
	require.Nil(t, changes, "no changes are returned on a failed commit")
	require.True(t, strings.Contains(err.Error(), "addressed transcript is missing"),
		"the original recordWindowProjections failure must survive in the returned error (errors.Join), got: %v", err)

	// The undo itself must actually have run and succeeded: the window
	// metadata reverts to the pre-checkpoint snapshot, not stay stuck on
	// the half-applied `after` state.
	final, err := store.SnapshotWindow(ctx, key)
	require.NoError(t, err, "SnapshotWindow after failed commit")
	_, stillEmptied := final.State.Projection.Entries[projKey]
	require.False(t, stillEmptied,
		"RestoreWindow must have undone the CommitWindow: the projection entry must not remain Emptied "+
			"after the transcript-rewrite failure forced a rollback")
}
