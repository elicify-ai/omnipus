package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Actual required correction-body damage, not a fake logger/error page or a
// malformed optional public item. The later saved anchor remains intact.
func pagingRequiredEffectStorageDiagnostic(t *testing.T) {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	s := api.agentLoop.GetSessionStore()
	a, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "required-effect-owner", Type: session.EntryTypeToolCall, AgentID: "mia", ToolCalls: []session.ToolCall{{ID: "required-call", Tool: "read_file", Status: "pending"}}})
	require.NoError(t, err)
	effect, err := s.SettleToolCall(sid, a, "pending", session.ToolCall{ID: "required-call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "PRIVATE_REQUIRED_EFFECT_CANARY"}})
	require.NoError(t, err)
	pagingSeed(t, s, sid, "later-anchor", 1)
	require.NoError(t, session.VerifyChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), sid)), "actual healthy baseline before corrupting one required body")
	path := filepath.Join(s.BaseDir(), sid, "transcript.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte{'!'}, effect.ByteOffset)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())
	read := pagingLogCapture(t)
	before := len(pagingSummaryRows(read()))
	w := pagingRequest(api, sid, "")
	rows := pagingSummaryRows(read())
	require.Len(t, rows, before+1, "one real failed-read diagnostic")
	row := rows[before]
	// A consistency guard may detect the changed saved prefix first; either
	// route must be a truthful history failure, never a stale successful tool.
	require.Contains(t, []string{"storage", "index_unavailable"}, row["outcome"])
	outcome, outcomeOK := row["outcome"].(string)
	require.True(t, outcomeOK, "real diagnostic outcome must be a string")
	pagingAssertSummary(t, row, outcome, "latest", false, true, []string{sid, path, "PRIVATE_REQUIRED_EFFECT_CANARY"})
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "PRIVATE_REQUIRED_EFFECT_CANARY")
	require.NotContains(t, w.Body.String(), path)
}
