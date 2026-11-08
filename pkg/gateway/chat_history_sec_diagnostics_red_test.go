package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SEC-2; spec gateway Diagnostics. Classification literals were agreed by
// team-lead on 2026-10-30, before test authoring. No payload is a log field.
func TestHistoryPageSecurity_MalformedToolErrorLogsOnlySafeClassification(t *testing.T) {
	const canary = "SEC2_SYNTHETIC_PRIVATE_CANARY"
	const malformed = `{"error":"` + canary // Intentionally no closing JSON syntax.
	const warning = "gateway: structured tool-failure payload failed to parse; rendering as prose"

	store, sid, _ := pagingProjectionFixture(t)
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "sec2-owner", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{ID: "sec2-call", Tool: "read_file", Status: "error", Error: malformed, ContentState: "full", Parameters: map[string]any{}}},
	}))
	require.NoError(t, store.Close())
	readLogs := pagingLogCapture(t) // Real logger file sink + bridge positive control.
	before := len(readLogs())
	// Make the first-warning sampling boundary deterministic, not silent. This
	// test is serial, and the process-global counter is restored on every exit.
	prior := unparseableFailureWarns.Swap(0)
	t.Cleanup(func() { unparseableFailureWarns.Store(prior) })
	request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{})
	require.NoError(t, err)
	prepared, err := request.Read(context.Background(), store)
	require.NoError(t, err)
	require.Len(t, prepared.Response.Entries, 1)

	// Instrument: actual persisted error reached the real replay builder. Do
	// not call parseStructuredToolFailure or the warning helper directly.
	errorFrames := 0
	for _, raw := range prepared.ReplayFrames {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["type"] == "tool_call_result" {
			errorFrames++
			require.Equal(t, "error", frame["status"])
			require.Equal(t, malformed, frame["error"], "saved prose remains in the transcript carrier, not in diagnostics")
		}
	}
	require.Equal(t, 1, errorFrames)

	warnings := 0
	for _, record := range readLogs()[before:] {
		encoded, err := json.Marshal(record)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), canary, "SEC-2: EVERY emitted log record must exclude private persisted error text")
		if record["message"] == warning {
			warnings++
			assert.Equal(t, "warn", record["level"], "retain the actual warning; silence is not redaction")
			assert.Equal(t, "structured_tool_failure_parse_failed", record["event"], "fixed event agreed before authoring")
			assert.Equal(t, "invalid_json", record["reason"], "fixed reason, never a payload-derived parse message")
			assert.EqualValues(t, len(malformed), record["len"], "safe byte count remains useful")
			assert.EqualValues(t, 1, record["occurrences"], "first sampled occurrence is observable")
		}
	}
	require.Equal(t, 1, warnings, "SEC-2: the real warning must still occur once with its fixed message and classification")
}
