package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Existing T23 extended by D2: actual HTTP summary reports byte_ceiling,
// without adding body/source selectors to diagnostics.
func TestHistoryPageDiagnostics_ByteCeilingStop(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	for i := 0; i < 3; i++ {
		require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: fmt.Sprintf("D2_LOG_ID_CANARY-%d", i), Type: session.EntryTypeSystem, Role: "system", AgentID: "mia", ViewMembership: session.ViewMembershipChat, Content: "D2_LOG_CONTENT_CANARY" + strings.Repeat("s", 4194304)}))
	}
	read := pagingLogCapture(t)
	p := pagingRead(t, api, sid, "limit=100&min_rows=100")
	require.Equal(t, "byte_ceiling", p.PageInfo.StopReason)
	rows := pagingSummaryRows(read())
	require.Len(t, rows, 1, "exactly one actual summary for this authorized attempt")
	require.Equal(t, "byte_ceiling", rows[0]["stop_reason"])
	require.EqualValues(t, p.PageInfo.ProjectedBytes, rows[0]["projected_bytes"])
	for _, key := range []string{"session_id", "agent_id", "history_position", "query", "raw_query", "content", "parameters", "result", "path"} {
		require.NotContains(t, rows[0], key)
	}
	for _, row := range read() {
		require.NotContains(t, fmt.Sprint(row), "D2_LOG_CONTENT_CANARY")
		require.NotContains(t, fmt.Sprint(row), "D2_LOG_ID_CANARY")
	}
}
