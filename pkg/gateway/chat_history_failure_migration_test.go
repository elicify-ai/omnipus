package gateway

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// A real filesystem fault, not the retired attachReadTranscript/full-reader
// override. Block the parent for every caller (including root), retaining the
// real writer-created metadata/index and exact bytes for subsequent Retry.
func historyMigrationUnreadable(t *testing.T, store *session.UnifiedStore, sid string) func() {
	t.Helper()
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "history-fault-source", Role: "user", Content: "saved before storage failed",
		AgentID: "mia", ViewMembership: session.ViewMembershipChat,
		Timestamp: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
	}))
	require.NoError(t, store.FlushSessionStats(sid))
	return sf1BlockParent(t, filepath.Join(store.BaseDir(), sid, "transcript.jsonl"))
}

// D7a/D10 deliberately replaces old N4's error-only/no-complete expectation.
// History failure must never wipe or claim a loaded page, but authorized current
// state/live binding completes independently. A canceled attach remains a
// different regression, with no complete, in the existing cancellation test.
func historyMigrationAssertLiveOnly(t *testing.T, sid string, frames []wireFrame) {
	t.Helper()
	require.Equal(t, -1, indexOfType(frames, "session_snapshot"), "N4: failed history never sends a wipe")
	require.Equal(t, -1, indexOfType(frames, "replay_message"), "no fabricated empty/partial reconstruction")
	require.Equal(t, -1, indexOfType(frames, "done"), "history failure must not end a running turn or claim replay success")
	require.Equal(t, -1, indexOfType(frames, "error"), "D7a history_state carries the error, never a generic turn ErrorFrame")
	counts := map[string]int{}
	for _, frame := range frames {
		counts[frame.Type]++
		switch frame.Type {
		case "session_state":
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(frame.raw, &state))
			require.Equal(t, &sid, state.SessionId)
			require.NotNil(t, state.HistoryState)
			require.Equal(t, "unavailable", state.HistoryState.Status)
			require.NotNil(t, state.HistoryState.Reason)
			require.Equal(t, "storage_error", *state.HistoryState.Reason)
			require.NotNil(t, state.HistoryState.Message)
			require.Equal(t, "This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log.", *state.HistoryState.Message)
			require.NotNil(t, state.PendingApprovals, "authorized current controls remain a real array")
		case "catch_up_complete":
			var complete generated.CatchUpCompleteFrame
			require.NoError(t, json.Unmarshal(frame.raw, &complete))
			require.Equal(t, sid, complete.SessionId)
			require.Equal(t, "live_only", complete.Mode)
		}
	}
	for _, typ := range []string{"session_state", "catch_up_complete"} {
		require.Equal(t, 1, counts[typ], "one actual %s, not a missing-path or duplicate success", typ)
	}
	require.Greater(t, indexOfType(frames, "catch_up_complete"), indexOfType(frames, "session_state"))
}
