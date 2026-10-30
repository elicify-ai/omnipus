package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// #1452 SEC-3 follow-up (page route): spec D2 A2.2 says an ordinary result
// must not carry the reserved `_history_preview` key, and a server-side
// marker failure becomes the D4 per-item placeholder, never a sent marker.
// A saved result that happens to be a schema-valid marker is not a reader
// preview, so on the page carriers (REST message and snapshot frames) it
// degrades to the placeholder with the content-free saved_preview_marker
// diagnostic, exactly as on the full-entry route.
func TestHistoryPreviewMarker_SavedValidMarkerOnPageBecomesPlaceholder(t *testing.T) {
	const canary = "SAVED_VALID_MARKER_CANARY"
	stored := map[string]any{"_history_preview": true, "original_size_bytes": 60000, "preview": canary}
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	msg, serverErr := validateBodyAgainstSchema("HistoryResultPreview", raw)
	require.False(t, serverErr)
	require.Empty(t, msg, "positive control: the saved result is a schema-valid marker")

	readLogs := pagingLogCapture(t)
	before := len(readLogs())
	position := session.HistoryPosition("hp1_" + "B0123456789abcdefghijklmnopqrstuvwxyzABCDEF")
	e := session.ChatHistoryEntry{Position: position, Entry: session.TranscriptEntry{ID: "saved-marker-owner", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC), ToolCalls: []session.ToolCall{{ID: "saved-marker-call", Tool: "read_file", Status: "success", ContentState: "full", Parameters: map[string]any{}, Result: stored}}}}
	request, err := newHistoryPageRequest("saved-marker-session", session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{})
	require.NoError(t, err)
	request.reset("fixture-cut")
	g, err := request.prepareGroup(context.Background(), session.ChatHistoryPage{SessionID: "saved-marker-session"}, e)
	require.NoError(t, err, "the item degrades locally, not a whole-page failure")

	require.NotNil(t, g.message.Content)
	require.Equal(t, historyPlaceholderText, *g.message.Content, "a saved marker is never sent as a marker on the page")
	require.Nil(t, g.message.ToolCalls)
	require.Equal(t, "saved-marker-owner", g.message.Id)
	require.Equal(t, string(position), *g.message.HistoryPosition)
	require.NotContains(t, string(g.encoded), "_history_preview")
	require.NotContains(t, string(g.encoded), canary)
	for _, frame := range g.frames {
		require.NotContains(t, string(frame), "_history_preview", "snapshot carrier")
		require.NotContains(t, string(frame), canary, "snapshot carrier")
	}

	found := false
	for _, row := range readLogs()[before:] {
		encoded, e := json.Marshal(row)
		require.NoError(t, e)
		require.NotContains(t, string(encoded), canary, "the diagnostic is content-free")
		if row["event"] == "history_item_placeholder" && row["reason"] == "saved_preview_marker" {
			found = true
		}
	}
	require.True(t, found, "A2.2 sanitized diagnostic with reason saved_preview_marker")
}
