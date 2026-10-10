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

	// A role:"tool" result must name the assistant occurrence that issued its
	// call (effects design / Decision A), so the seed archives the assistant
	// call first, then the result it answers.
	asstMsg := providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{
		ID: "tc-gap2", Type: "function", Function: &providers.FunctionCall{Name: "read", Arguments: "{}"},
	}}}
	_, err := store.AppendWindowMessage(ctx, key, asstMsg)
	require.NoError(t, err, "seed: AppendWindowMessage(assistant call)")

	toolMsg := providers.Message{Role: "tool", ToolCallID: "tc-gap2", Content: "a real archived tool result"}
	snap0, err := store.AppendWindowMessage(ctx, key, toolMsg)
	require.NoError(t, err, "seed: AppendWindowMessage(tool result)")
	require.Equal(t, 2, snap0.State.Count, "precondition: the assistant call and its result are archived")
	require.Len(t, snap0.Archive, 2)
	require.Equal(t, "tool", snap0.Archive[1].Role)

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

	projKey := memory.ProjectionKey{ToolCallID: "tc-gap2", ArchiveLine: 1}
	newState := snap0.State.Clone()
	newState.Projection.Entries[projKey] = memory.ProjectionEmptied
	// The projection names an ArchiveAddress that cannot be resolved: its
	// session's partition does not exist on disk, so the settle/projection
	// effect append (ProjectToolCalls) fails for real. (Effects design D5: the
	// projection is an appended effect addressed by archive identity, so the
	// missing-transcript failure the old line-index path produced is now a
	// missing-address failure.)
	newState.Projection.TranscriptAddr[projKey] = session.ArchiveAddress{
		PartitionKey: "2026-01-01", ByteOffset: 3, EntryID: "gap2-missing-record",
	}

	p := &windowCheckpoint{
		ts:       ts,
		snapshot: session.WindowViewFromSnapshot(snap0),
		state:    newState,
		messages: []providers.Message{toolMsg},
		lines:    []int{1},
	}

	changes, err := al.commitWindowProjections(ctx, p, store)
	require.Error(t, err, "a failed projection-effect append must be visible to the caller, "+
		"never swallowed into only a log line")
	require.Nil(t, changes, "no changes are returned on a failed commit")
	require.NotEmpty(t, err.Error(), "the recordWindowProjections failure must survive in the returned error (errors.Join)")

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
