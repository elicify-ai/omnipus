package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type pagingLogBuffer struct { // not-wire-format: thread-safe process-edge diagnostic capture.
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *pagingLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *pagingLogBuffer) text() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func pagingDiagnosticRecords(t *testing.T, text string) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "captured structured record")
		out = append(out, row)
	}
	return out
}

// T23, D-Z1/B33: required positive event/count assertions keep a silent logger
// from passing a no-canary search. Existing privileged storage causes are not
// silently redesigned: privacy assertions inspect only the new history_page logs.
func TestHistoryPageDiagnostics_ContentFreeOnEveryOutcome(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	body := "HISTORY_BODY_CANARY_1266"
	parameter := "HISTORY_PARAMETER_CANARY_1266"
	result := "HISTORY_RESULT_CANARY_1266"
	credential := "HISTORY_CREDENTIAL_CANARY_1266"
	address, err := store.AppendTranscriptAddressed(sid, session.TranscriptEntry{
		ID: "diagnostic-owner", Role: "assistant", Content: body, AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		ToolCalls: []session.ToolCall{{ID: "diagnostic-call", Tool: "read_file", Status: "success", Parameters: map[string]any{"path": parameter, "credential": credential}, Result: map[string]any{"text": result}}},
	})
	require.NoError(t, err)
	unindexed := pagingUnindexedSession(t, store)
	buf := &pagingLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	// Capture instrument control: the exact installed handler must see an event.
	slog.Info("history diagnostic instrument", "event", "paging_capture_control", "count", 1)
	valid := pagingRequest(api, sid, "")
	require.Equal(t, 200, valid.Code)
	position := pagingPositionOracle(sid, address)
	require.Equal(t, 200, pagingRequest(api, sid, "before="+position+"&private_mark=HISTORY_PRIVATE_MARK_CANARY_1266").Code, "known raw position on successful older request is still excluded from diagnostics")
	pagingRefusal(t, pagingRequest(api, sid, "limit=19&query=HISTORY_QUERY_CANARY_1266"), 400, "limit must be an integer from 20 to 100")
	pagingRefusal(t, pagingRequest(api, unindexed, ""), 500, pagingUnavailable)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api.HandleSessions(httptest.NewRecorder(), r.WithContext(ctx))
	rows := pagingDiagnosticRecords(t, buf.text())
	var control int
	byOutcome := map[string][]map[string]any{}
	for _, row := range rows {
		if row["event"] == "paging_capture_control" {
			control++
			require.EqualValues(t, 1, row["count"])
		}
		if row["event"] != "history_page" {
			continue
		}
		outcome, ok := row["outcome"].(string)
		require.True(t, ok, "new diagnostics have typed outcome")
		byOutcome[outcome] = append(byOutcome[outcome], row)
	}
	require.Equal(t, 1, control, "positive-controlled installed process-edge capture")
	for outcome, count := range map[string]int{"success": 2, "invalid_request": 1, "index_unavailable": 1, "abort": 1} {
		require.Len(t, byOutcome[outcome], count, "one real history_page summary per completed/refused/aborted request: %s", outcome)
	}
	success := byOutcome["success"][0]
	require.Equal(t, sid, success["session_id"])
	require.Equal(t, "latest", success["mode"])
	require.EqualValues(t, 1, success["source_group_count"])
	require.EqualValues(t, 2, success["row_count"], "one prose+one logical tool row")
	require.EqualValues(t, 40, success["limit"])
	require.EqualValues(t, 20, success["min_rows"])
	require.EqualValues(t, 4194304, success["soft_byte_budget"])
	for _, key := range []string{"projected_bytes", "duration_ms", "has_earlier", "has_later", "stop_reason", "retention_generation"} {
		_, ok := success[key]
		require.True(t, ok, "required positive content-free measurement %s", key)
	}
	for _, row := range rows {
		if row["event"] != "history_page" {
			continue
		}
		b, err := json.Marshal(row)
		require.NoError(t, err)
		for _, canary := range []string{body, parameter, result, credential, position, fmt.Sprintf("%+v", address), "HISTORY_QUERY_CANARY_1266", "HISTORY_PRIVATE_MARK_CANARY_1266", "history_position", "partition_key", "byte_offset"} {
			require.NotContains(t, string(b), canary, "new history diagnostic content/raw-address leak")
		}
	}
}

func TestHistoryPageDiagnostics_RawPositionStorageFailureAndPlantedLeak(t *testing.T) {
	t.Fatal("BLOCKED: new history_page diagnostic seam and immutable storage/read/abort fault barriers not implemented — required by D1/D4b/T23/B33/D-Z1; real success/refusal/abort/index/storage records must expose required counts but exclude issued raw-position/private-address canaries; independent CHECK must plant a raw-query/content log and confirm this behavioral canary oracle dies, not merely scan source text")
}
