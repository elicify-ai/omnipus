package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// A record the streamed display read cannot hold becomes the D4 placeholder
// on the page carriers, with a content-free diagnostic.
func TestChatHistoryPage_StreamUnreadableRecordIsPlaceholder(t *testing.T) {
	const canary = "STREAM_UNREADABLE_CANARY"
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	s := api.agentLoop.GetSessionStore()
	sid := createTestSession(t, api)
	key := strings.Repeat("k", session.ChatHistoryTextDisplayMaxBytes+8)
	require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "unreadable-owner", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: time.Date(2026, 10, 30, 5, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{ID: "unreadable-call", Tool: "read_file", Status: "success", Parameters: map[string]any{key: canary}, Result: map[string]any{"text": canary}}}}))
	require.NoError(t, s.Close())
	readLogs := pagingLogCapture(t)
	before := len(readLogs())
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{ToolStore: newToolResultStore(api.homePath)})
	require.NoError(t, err)
	p, err := r.Read(context.Background(), s)
	require.NoError(t, err, "the item degrades locally, not a whole-page failure")
	require.Len(t, p.Response.Entries, 1)
	m := p.Response.Entries[0]
	require.Equal(t, "unreadable-owner", m.Id)
	require.NotNil(t, m.Content)
	require.Equal(t, historyPlaceholderText, *m.Content)
	require.Nil(t, m.ToolCalls)
	body, err := json.Marshal(p.Response)
	require.NoError(t, err)
	require.NotContains(t, string(body), canary)
	for _, f := range p.ReplayFrames {
		require.NotContains(t, string(f), canary, "snapshot carrier")
	}
	found := false
	for _, row := range readLogs()[before:] {
		encoded, e := json.Marshal(row)
		require.NoError(t, e)
		require.NotContains(t, string(encoded), canary, "the diagnostic is content-free")
		if row["event"] == "history_item_placeholder" && row["reason"] == "display_stream_unreadable" {
			found = true
		}
	}
	require.True(t, found, "sanitized diagnostic with reason display_stream_unreadable")
}
