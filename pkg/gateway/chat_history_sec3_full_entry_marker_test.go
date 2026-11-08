package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// #1452 security gate SEC-3 (backend part): the full-entry route returns the
// unshortened projection, which is never a HistoryResultPreview (spec D2
// A2.2, 200 row) and in which an ordinary result must not carry the reserved
// `_history_preview` key (A2.2 reserved-key row). A persisted result that
// happens to be a schema-valid marker therefore fails the server-side marker
// check and becomes the existing D4 per-item placeholder (A2.2 validation
// row), never a sent marker and never the saved body.
func TestHistoryEntryRead_StoredValidMarkerBecomesPlaceholder(t *testing.T) {
	const canary = "FULL_ENTRY_MARKER_CANARY"
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sec3-route-test-token")
	sid := createTestSession(t, api)
	stored := map[string]any{"_history_preview": true, "original_size_bytes": 60000, "preview": canary}
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	msg, serverErr := validateBodyAgainstSchema("HistoryResultPreview", raw)
	require.False(t, serverErr)
	require.Empty(t, msg, "positive control: the saved result is a schema-valid marker")
	addr, err := api.agentLoop.GetSessionStore().AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "sec3-src", Type: session.EntryTypeToolCall, AgentID: "mia", ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: "sec3-call", Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: stored}}})
	require.NoError(t, err)
	position := pagingPositionOracle(sid, addr)

	w := d2EntryRequest(api, sid, position, "Bearer sec3-route-test-token", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "_history_preview", "SEC-3: a full-entry response carried a preview marker")
	require.NotContains(t, w.Body.String(), canary)
	var m generated.Message
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	require.Equal(t, "sec3-src", m.Id, "the owning occurrence identity is kept")
	require.NotNil(t, m.HistoryPosition)
	require.Equal(t, position, *m.HistoryPosition)
	require.NotNil(t, m.Content)
	require.Equal(t, historyPlaceholderText, *m.Content)
	require.Nil(t, m.ToolCalls)
	require.Nil(t, m.ContentOriginalSizeBytes)
}
