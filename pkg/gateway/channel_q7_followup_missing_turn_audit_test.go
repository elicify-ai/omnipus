package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/require"
)

// R-S3 / CHECK F2b, team-lead ruling: the D-03 missing-turn-id refusal must
// leave a request/decision pair, not an orphan request or silence. The denied
// reason must remain missing_turn_id, so an auditor can distinguish this
// pre-admission refusal from user denial, cancellation or saturation.
func TestQ7FollowupS3_MissingTurnIDAskAuditsRequestAndMatchingDecision(t *testing.T) {
	f := newWU6Fixture(t)
	require.NotNil(t, f.al.AuditLogger(), "instrument: real boot-created audit logger")
	sid := q7TurnSession(t, f)
	reg := f.reg
	require.Empty(t, reg.pendingApprovals(), "instrument: no pending approval before the refused call")
	require.Zero(t, reg.missingTurnID.Load(), "instrument: fresh missing-turn-id counter")
	ws := &WSHandler{agentLoop: f.al, approvalRegV2: reg}
	adapter := newPolicyApproverAdapter(reg, ws)
	req := agent.PolicyApprovalReq{
		ToolCallID: "q7-missing-turn-audit-call", ToolName: "set_channel_outbound",
		Args:    map[string]any{"id": "telegram", "targets": []any{"987654321"}},
		AgentID: "omnipus", SessionID: sid, TurnID: "",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approved, reason, grant := adapter.RequestApproval(ctx, req)
	require.False(t, approved, "D-03: a missing turn id refuses the always-ask call")
	require.Equal(t, "missing_turn_id", reason, "T-F2b: exact refusal reason, never cancel, timeout or saturation")
	require.False(t, grant, "R-S3: a refused call never records a standing grant")
	require.EqualValues(t, 1, reg.missingTurnID.Load(), "instrument: the real D-03 fail-closed branch was exercised exactly once")
	require.Empty(t, reg.pendingApprovals(), "D-03: the refused call leaves no pending approval card")

	rows := q7AuditRead(t, f, sid, req.ToolName, false)
	var requested, denied, granted []map[string]any
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if fields["tool_call_id"] != req.ToolCallID {
			continue
		}
		switch row["event"] {
		case audit.EventToolPolicyAskRequested:
			requested = append(requested, fields)
		case audit.EventToolPolicyAskDenied:
			denied = append(denied, fields)
		case audit.EventToolPolicyAskGranted:
			granted = append(granted, fields)
		}
	}
	t.Logf("Missing-turn audit: requested=%d denied=%d granted=%d", len(requested), len(denied), len(granted))
	require.Len(t, requested, 1, "R-S3 / T-F2b: exactly one request audit for the missing-turn-id refusal")
	require.Empty(t, granted, "R-S3: missing-turn-id refusal never writes a granted decision")
	approvalID, ok := requested[0]["approval_id"].(string)
	require.True(t, ok, "R-S3: the refused request has an approval identity")
	require.NotEmpty(t, approvalID)
	require.Nil(t, reg.get(approvalID), "D-03: the refused request is not an admitted registry entry")
	entry := &approvalEntry{
		ApprovalID: approvalID, ToolCallID: req.ToolCallID, ToolName: req.ToolName,
		AgentID: req.AgentID, SessionID: sid, TurnID: "",
	}
	// These ASCII keys/values and JSON-native array make sorted-key encoding/json
	// equal FR-080 canonical JSON. The oracle uses no production audit hash helper.
	hash := q7FollowupDigest(t, req.Args)
	q7AuditIdentity(t, requested[0], entry, hash)
	require.Len(t, denied, 1, "R-S3 / T-F2b: exactly one terminal denied audit must match the missing-turn-id request; a process-log error is not a decision record")
	q7AuditIdentity(t, denied[0], entry, hash)
	require.Equal(t, "missing_turn_id", denied[0]["reason"], "T-F2b ruling: retain the exact pre-admission refusal reason")
	require.Equal(t, "", denied[0]["approver_user_id"], "R-S3: system refusal has no fabricated human approver")
}
