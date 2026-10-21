package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func pagingDiagnosticRecords(t *testing.T, text string) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "real gateway structured-file record")
		out = append(out, row)
	}
	return out
}

func pagingLogCapture(t *testing.T) func() []map[string]any {
	t.Helper()
	path := enableGatewayLogFile(t) // Existing production file sink, not a new JSON formatter.
	previous := slog.Default()
	installSlogBridge()
	t.Cleanup(func() { slog.SetDefault(previous) })
	read := func() []map[string]any {
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		return pagingDiagnosticRecords(t, string(b))
	}
	logsafeInfo("history diagnostic instrument", "event", "paging_capture_control", "count", 1)
	control := 0
	for _, row := range read() {
		if row["event"] == "paging_capture_control" {
			control++
			require.Equal(t, "history diagnostic instrument", row["message"])
			require.EqualValues(t, 1, row["count"])
		}
	}
	require.Equal(t, 1, control, "positive control: installed bridge/file sink preserves message and fields")
	return read
}

func pagingSummaryRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0)
	for _, row := range rows {
		if row["message"] == "history_page" || row["event"] == "history_page" {
			out = append(out, row)
		}
	}
	return out
}

func pagingCounter(t *testing.T, row map[string]any, key string) float64 {
	t.Helper()
	v, ok := row[key].(float64)
	require.True(t, ok, "required measured nonnegative integer %s, never missing-as-zero", key)
	require.False(t, math.IsNaN(v) || math.IsInf(v, 0))
	require.GreaterOrEqual(t, v, float64(0))
	require.Equal(t, math.Trunc(v), v, "integer I/O counter %s", key)
	return v
}

// Oracle: spec34612f172 Content-free observability and seams-decision H.
// Exact required fields apply on failures too. Physical archive categories are
// disjoint; a missing counter cannot be defaulted to0 and called measured work.
func pagingAssertSummary(t *testing.T, row map[string]any, outcome, mode string, success, optionsKnown bool, canaries []string) {
	t.Helper()
	require.Equal(t, "history_page", row["message"])
	require.Equal(t, "history_page", row["event"])
	require.Equal(t, "rest_messages", row["carrier"])
	require.Equal(t, mode, row["mode"])
	require.Equal(t, outcome, row["outcome"])
	duration, ok := row["duration_ms"].(float64)
	require.True(t, ok)
	require.False(t, math.IsNaN(duration) || math.IsInf(duration, 0))
	require.GreaterOrEqual(t, duration, float64(0))
	require.Equal(t, float64(4194304), pagingCounter(t, row, "soft_byte_budget"))
	var records, bytesRead float64
	for _, category := range []string{"selected", "lookahead", "dependency", "recovery", "discarded"} {
		records += pagingCounter(t, row, category+"_archive_records")
		bytesRead += pagingCounter(t, row, category+"_archive_bytes")
	}
	require.Equal(t, records, pagingCounter(t, row, "archive_records_read"), "each actual physical read charged once")
	require.Equal(t, bytesRead, pagingCounter(t, row, "archive_bytes_read"), "encoded bytes, including actual failed/discarded work")
	for _, key := range []string{"disposition_records_read", "disposition_bytes_read", "index_bytes_read", "dependency_count", "recovery_items", "recovery_anchor_records", "recovery_overflow_records", "retention_retries"} {
		pagingCounter(t, row, key)
	}
	for _, key := range []string{"limit", "min_rows"} {
		_, present := row[key]
		require.Equal(t, optionsKnown, present, "conditional validated option %s", key)
	}
	_, requested := row["requested_limit"]
	require.False(t, requested, "these requests omit a successfully parsed explicit limit")
	for _, key := range []string{"source_group_count", "row_count", "projected_bytes", "has_earlier", "has_later", "byte_budget_exceeded", "stop_reason", "retention_generation"} {
		_, present := row[key]
		require.Equal(t, success, present, "success-only metadata %s", key)
	}
	if success {
		pagingCounter(t, row, "source_group_count")
		pagingCounter(t, row, "row_count")
		pagingCounter(t, row, "projected_bytes")
		for _, key := range []string{"has_earlier", "has_later", "byte_budget_exceeded"} {
			_, ok := row[key].(bool)
			require.True(t, ok, "typed boolean %s", key)
		}
		require.Contains(t, []string{"row_target", "source_cap", "clear", "end", "range_end", "byte_budget"}, row["stop_reason"])
		generation, ok := row["retention_generation"].(string)
		require.True(t, ok)
		require.NotEmpty(t, generation)
	}
	_, override := row["budget_override"]
	require.Equal(t, success && row["byte_budget_exceeded"] == true, override)
	if override {
		require.Contains(t, []string{"height_floor", "atomic_group"}, row["budget_override"])
	}
	for _, key := range []string{"session_id", "agent_id", "user_id", "query", "raw_query", "position", "history_position", "address", "partition_key", "byte_offset", "path", "error", "cause", "allocated_bytes"} {
		_, present := row[key]
		require.False(t, present, "forbidden identifier/private/guessed-allocation field %s", key)
	}
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	for _, canary := range canaries {
		require.NotContains(t, string(encoded), canary, "new history summary leaked planted content/query/identity/private mark")
	}
}

func TestHistoryPageDiagnostics_ContentFreeOnEveryOutcome(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	body, parameter, result, credential := "HISTORY_BODY_CANARY_1266", "HISTORY_PARAMETER_CANARY_1266", "HISTORY_RESULT_CANARY_1266", "HISTORY_CREDENTIAL_CANARY_1266"
	agentID, userID := "HISTORY_AGENT_ID_CANARY_1266", "HISTORY_USER_ID_CANARY_1266"
	address, err := store.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "diagnostic-owner", Role: "assistant", Content: body, AgentID: agentID, Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), ToolCalls: []session.ToolCall{{ID: "diagnostic-call", Tool: "read_file", Status: "success", Parameters: map[string]any{"path": parameter, "credential": credential}, Result: map[string]any{"text": result}}}})
	require.NoError(t, err)
	unindexed := pagingUnindexedSession(t, store)
	read := pagingLogCapture(t)
	position := pagingPositionOracle(sid, address)
	canaries := []string{sid, unindexed, agentID, userID, body, parameter, result, credential, position, fmt.Sprintf("%+v", address), "HISTORY_QUERY_CANARY_1266", "HISTORY_PRIVATE_MARK_CANARY_1266"}
	cases := []struct {
		name, sid, query, outcome, mode string
		status                          int
		success, known, canceled        bool
	}{
		{"latest", sid, "", "success", "latest", 200, true, true, false},
		{"older_raw_position", sid, "before=" + position + "&private_mark=HISTORY_PRIVATE_MARK_CANARY_1266", "success", "older", 200, true, true, false},
		{"limit_refusal", sid, "limit=19&query=HISTORY_QUERY_CANARY_1266", "invalid_request", "unparsed", 400, false, false, false},
		{"raw_query_refusal", sid, "unused=%ZZ&private_mark=HISTORY_QUERY_CANARY_1266", "invalid_request", "unparsed", 400, false, false, false},
		{"missing_index", unindexed, "", "index_unavailable", "latest", 500, false, true, false},
		{"abort", sid, "", "abort", "latest", 0, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(pagingSummaryRows(read()))
			r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+tc.sid+"/messages", nil)
			r.URL.RawQuery = tc.query
			ctx := context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: userID})
			if tc.canceled {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			w := httptest.NewRecorder()
			api.HandleSessions(w, r.WithContext(ctx))
			rows := pagingSummaryRows(read())
			require.Len(t, rows, before+1, "exactly one real history_page per authorized attempt, not per candidate/layer")
			row := rows[before]
			mode, known := tc.mode, tc.known
			if tc.canceled {
				// A pre-canceled request may stop before selector validation; H
				// specifies conditional fields, not a mandatory parser-before-cancel.
				// Both allowed phases still require a real abort and every I/O field.
				require.Contains(t, []string{"latest", "unparsed"}, row["mode"])
				if row["mode"] == "unparsed" {
					mode, known = "unparsed", false
				}
			}
			pagingAssertSummary(t, row, tc.outcome, mode, tc.success, known, canaries)
			if tc.status != 0 {
				require.Equal(t, tc.status, w.Code, "request must have reached the intended diagnostic outcome")
			}
			if tc.name == "latest" {
				require.EqualValues(t, 1, row["source_group_count"])
				require.EqualValues(t, 2, row["row_count"])
				require.EqualValues(t, 1, row["selected_archive_records"])
				require.Greater(t, pagingCounter(t, row, "selected_archive_bytes"), float64(0))
				require.Equal(t, false, row["has_earlier"])
				require.Equal(t, false, row["has_later"])
			}
		})
	}
	t.Run("access_refusal_emits_no_summary", func(t *testing.T) {
		before := len(pagingSummaryRows(read()))
		pagingRefusal(t, pagingRequest(api, "missing-child", "unused=%ZZ"), 404, "session not found")
		require.Len(t, pagingSummaryRows(read()), before, "access refusal precedes page attempt and emits no history_page summary")
	})
}

func TestHistoryPageDiagnostics_RawPositionStorageFailureAndPlantedLeak(t *testing.T) {
	pagingRequiredEffectStorageDiagnostic(t)
}
