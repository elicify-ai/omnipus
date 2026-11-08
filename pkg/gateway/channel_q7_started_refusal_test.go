package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// T-M1 / Q7 merge interaction: an OWN_CONNECTION pre-approval refusal still
// creates a dispatch-time started record. The thread must settle that SAME
// call exactly once as error, preserving its arguments, without asking anyone.
// These expectations come from T-M1 and R-Q4(b)/FD-11, not the scratch probe.
func TestQ7Merge_OwnConnectionRefusalSettlesExactlyOneStartedRecord(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "mia")
	sid := q7FollowupCaller(t, f, "mia", false, false)
	args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
	const callID = "q7-m1-own-refused-call"
	f.provider.setDefaultScript(q7Script(callID, args))
	events := q7FollowupStartAs(t, f, sid, "mia")
	pending := q7FollowupPendingOrCompleted(t, f, events, sid, callID)
	require.Nil(t, pending, "T-M1 / FD-11: own-connection refusal must complete before any approval card")
	require.Empty(t, f.reg.pendingApprovals(), "T-M1: no approval card is left pending")

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(f.provider.lastToolResult()), &result))
	require.Equal(t, false, result["success"], "instrument: the real tool call was refused, not silently accepted")
	failure, ok := result["error"].(map[string]any)
	require.True(t, ok, "instrument: the real turn returned a structured tool refusal")
	require.Equal(t, "OWN_CONNECTION", failure["code"], "T-M1: this is the own-connection pre-approval refusal, not a different error")
	require.Equal(t, "a connection's own agent cannot change its posting list; ask the operator", failure["message"], "FD-11: exact own-connection refusal guidance")

	entries, err := f.al.GetSessionStore().ReadTranscript(sid)
	require.NoError(t, err, "instrument: read the real thread transcript after the actual turn completed")
	var records []session.ToolCall
	for _, entry := range entries {
		// Inspect all tool-call collections, not just one entry type, so a
		// duplicate with this ID elsewhere in the thread cannot evade the count.
		for _, call := range entry.ToolCalls {
			if call.ID == session.ToolCallID(callID) {
				records = append(records, call)
			}
		}
	}
	require.Len(t, records, 1, "T-M1: exactly one thread record for the refused call; never a second terminal receipt")
	require.Equal(t, "set_channel_outbound", records[0].Tool, "T-M1: the record belongs to the real refused tool")
	require.Equal(t, args, records[0].Parameters, "T-M1: preserve the whole argument map, including targets and expected_revision")
	t.Logf("T-M1 transcript: call=%s records=%d status=%q arguments_match=true", callID, len(records), records[0].Status)
	require.Equal(t, "error", records[0].Status, "T-M1: the refused call's started record must settle as error, never remain running")

	// A completed turn plus no pending entry rules out a waiting card; reading
	// the real request audit also catches a card that was resolved too quickly
	// for the pending-entry observer. q7AuditRead checks the logger's startup row.
	rows := q7AuditRead(t, f, sid, "set_channel_outbound", false)
	requests := 0
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == audit.EventToolPolicyAskRequested && fields["tool_call_id"] == callID {
			requests++
		}
	}
	require.Zero(t, requests, "T-M1 / R-Q4(b): own-connection refusal must not raise any approval card")
}
