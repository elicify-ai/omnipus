package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Content-free observability deliberately replaces replay_start/replay_end and
// lifetime-load counts with one real bounded-preparation summary. Capture through
// the actual gateway bridge/file sink; its known-present control runs first.
func historyMigrationLoggedAttach(t *testing.T) map[string]any {
	t.Helper()
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	read := pagingLogCapture(t)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	const content = "LOG_MIGRATION_PRIVATE_SOURCE_CANARY"
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID: "log-migration-source", Role: "user", Content: content, AgentID: "mia",
		ViewMembership: session.ViewMembershipChat, Timestamp: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
	}))
	wc := &wsConn{sendCh: make(chan []byte, 512), doneCh: make(chan struct{})}
	handler.handleAttachSession(context.Background(), "chat-log-migration", meta.ID, nil, wc)
	frames := pagingDrain(t, wc)
	snapshot := indexOfType(frames, "session_snapshot")
	require.GreaterOrEqual(t, snapshot, 0, "a real prepared page must precede its summary assertion")
	historyMigrationSnapshot(t, meta.ID, frames[snapshot].raw, false, 1, 1)
	require.Greater(t, indexOfType(frames, "catch_up_complete"), snapshot)
	rows := pagingSummaryRows(read())
	require.Len(t, rows, 1, "exactly one summary per authorized bounded-page attempt, not start/end duplicates")
	row := rows[0]
	pagingAssertSummaryCarrier(t, row, "ws_attach", "success", "latest", true, true,
		[]string{meta.ID, "mia", content, "log-migration-source", store.BaseDir()})
	require.EqualValues(t, 1, row["source_group_count"], "one actual saved source, not a lifetime count")
	require.EqualValues(t, 1, row["row_count"])
	return row
}
