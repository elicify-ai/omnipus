// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil/fileutiltest"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// mutateToolCallInTranscript rewrites transcript.jsonl with
// fileutil.WriteFileAtomic (temp file + rename) under a cross-process
// fileutil.WithFlock lock that used to be taken on transcript.jsonl itself.
// Each rewrite moved the transcript to a new inode, so a rewrite that opened
// the path before another's rename and one that opened it after held locks on
// two different files and ran together — the later one overwriting the
// earlier one's settled tool call. And because WithFlock opens with O_CREATE,
// locking a transcript that did not exist yet created it empty.

func TestTranscriptMutate_TakesTheTranscriptSidecarLock(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_lock_sidecar")
	recordAskPendingToolCall(ts, callID, "run_task", map[string]any{"task_id": "t-lock"})
	require.Len(t, toolCallsFor(t, store, sessionID, callID), 1, "precondition: the pending placeholder is on disk")

	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	fileutiltest.RequireWaitsForSidecarLock(t, transcriptPath, func() error {
		if !mutateToolCallInTranscript(store, sessionID, callID, toolCallStatusPending,
			func(tc *session.ToolCall) { tc.Status = "settled_by_lock_test" }) {
			return errors.New("the pending tool_call entry was not rewritten")
		}
		return nil
	})

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1)
	require.Equal(t, "settled_by_lock_test", calls[0].Status)
}

// A session whose transcript has not been written yet has no tool_call entry to
// settle. Taking the lock for that lookup must not create the transcript, and
// the miss is still reported as "entry not found" — the session exists.
func TestTranscriptMutate_LockingNeverCreatesTheTranscript(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	transcriptPath := filepath.Join(store.BaseDir(), meta.ID, "transcript.jsonl")
	require.NoError(t, os.Remove(transcriptPath), "arrange a session whose transcript does not exist yet")

	logs := u22CaptureLogs(t)
	before := TranscriptMutateMissed()
	got := mutateToolCallInTranscript(store, meta.ID, "call_lock_absent", toolCallStatusPending,
		func(*session.ToolCall) {})

	require.False(t, got)
	require.NoFileExists(t, transcriptPath, "locking the transcript for a rewrite must never create the transcript")
	require.Equal(t, before+1, TranscriptMutateMissed())
	out := logs.String()
	require.Contains(t, out, "entry_not_found", "the session exists, so the miss is an entry miss; got:\n%s", out)
	require.NotContains(t, out, "session_not_found", "the session exists; got:\n%s", out)
}
