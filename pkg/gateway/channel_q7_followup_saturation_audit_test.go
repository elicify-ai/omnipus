package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/require"
)

// R-S3 / CHECK F2: the shared approval state table requires a denied/saturated
// audit even when admission skips pending. R-S3 requires that decision's
// request counterpart; accepting neither would hide the mandated denial.
func TestQ7FollowupS3_SaturatedAskAuditsRequestAndMatchingDecision(t *testing.T) {
	f := newWU6Fixture(t)
	require.NotNil(t, f.al.AuditLogger(), "instrument: real boot-created audit logger")
	sid := q7TurnSession(t, f)
	const pendingCap = 1 // Configurable cap: one admitted call puts the next at max+1.
	reg := newApprovalRegistryV2(pendingCap, time.Hour)
	reg.terminalRetention = 0
	t.Cleanup(func() { reg.cancelAllPendingForRestart() })
	ws := &WSHandler{agentLoop: f.al, approvalRegV2: reg}
	reg.setResolutionListener(ws.broadcastToolApprovalResolved)
	adapter := newPolicyApproverAdapter(reg, ws)
	blocker, admitted := reg.requestApproval("q7-cap-blocker", "get_agent", map[string]any{"id": "mia"}, "omnipus", sid, "q7-cap-turn")
	require.True(t, admitted, "instrument: the real registry admits the call below its cap")
	require.NotNil(t, blocker)
	require.Len(t, reg.pendingApprovals(), pendingCap, "instrument: the cap is actually occupied")

	req := agent.PolicyApprovalReq{
		ToolCallID: "q7-saturated-call", ToolName: "set_channel_outbound",
		Args:    map[string]any{"id": "telegram", "targets": []any{"987654321"}},
		AgentID: "omnipus", SessionID: sid, TurnID: "q7-saturated-turn",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approved, reason, grant := adapter.RequestApproval(ctx, req)
	require.False(t, approved, "FR-016: saturation refuses the call")
	require.Equal(t, "saturated", reason, "instrument: pre-admission cap refusal, not cancel or timeout")
	require.False(t, grant, "R-S3: a refused call cannot record a standing grant")
	pending := reg.pendingApprovals()
	require.Len(t, pending, pendingCap, "FR-016: the refused call is never admitted as pending")
	require.Equal(t, blocker.ApprovalID, pending[0].ApprovalID, "instrument: saturation did not replace the original pending call")

	rows := q7AuditRead(t, f, sid, req.ToolName, false)
	var requested, denied, granted []map[string]any
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if fields["tool_call_id"] != req.ToolCallID || fields["session_id"] != sid {
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
	t.Logf("Saturated audit: requested=%d denied=%d granted=%d", len(requested), len(denied), len(granted))
	require.Len(t, denied, 1, "FR-038/047 and approval state table: saturation must audit exactly one denial")
	require.Empty(t, granted, "R-S3: saturation never grants consent")
	approvalID, ok := denied[0]["approval_id"].(string)
	require.True(t, ok, "R-S3: the refused call still has an approval identity")
	require.NotEmpty(t, approvalID)
	entry := &approvalEntry{
		ApprovalID: approvalID, ToolCallID: req.ToolCallID, ToolName: req.ToolName,
		AgentID: req.AgentID, SessionID: sid, TurnID: req.TurnID,
	}
	hash := q7FollowupDigest(t, req.Args)
	q7AuditIdentity(t, denied[0], entry, hash)
	require.Equal(t, "saturated", denied[0]["reason"], "FR-047: exact pre-admission refusal reason")
	require.Equal(t, "", denied[0]["approver_user_id"], "R-S3: saturation is a system decision, not a person's")
	require.Len(t, requested, 1, "R-S3 / F2: a saturated decision must have exactly one matching request, never an orphan decision")
	q7AuditIdentity(t, requested[0], entry, hash)
}
