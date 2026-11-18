package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/require"
)

// R-S3 / CHECK F1: Stop first cancels the turn context, then runs the
// gateway's pending-approval cancellation hook. Both are production inputs;
// neither the registry's outcome nor an audit row is synthesized by the test.
// Repeating the two-ready-channels interleaving also detects duplicate audits
// if a fix writes the decision from both the registry and its blocked waiter.
func TestQ7FollowupS3_SessionStopAuditsExactlyOneCancelPerRequest(t *testing.T) {
	f := newWU6Fixture(t)
	require.NotNil(t, f.al.AuditLogger(), "instrument: use the real boot-created audit logger")
	sid := q7TurnSession(t, f)
	ws := &WSHandler{agentLoop: f.al, approvalRegV2: f.reg}
	f.reg.setResolutionListener(ws.broadcastToolApprovalResolved)
	adapter := newPolicyApproverAdapter(f.reg, ws)
	hooks := ws.buildCancelHooks(nil)
	require.NotNil(t, hooks.CancelPendingApprovals, "instrument: use Stop's real gateway cancellation hook")
	// No retained entries/timers are needed after this test has joined a waiter.
	f.reg.terminalRetention = 0

	const iterations = 200 // Requested regression stress count, not a timing oracle.
	args := map[string]any{"id": "telegram", "targets": []any{"987654321"}}
	hash := q7FollowupDigest(t, args) // FR-080: independent SHA-256 over sorted-key JSON.
	entries := make([]*approvalEntry, 0, iterations)
	branches := map[string]int{}
	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		callID := fmt.Sprintf("q7-stop-audit-call-%03d", i)
		done := make(chan ApprovalOutcome, 1)
		go func() {
			approved, reason, grant := adapter.RequestApproval(ctx, agent.PolicyApprovalReq{
				ToolCallID: callID, ToolName: "set_channel_outbound", Args: args,
				AgentID: "omnipus", SessionID: sid, TurnID: "q7-stop-audit-turn",
			})
			done <- ApprovalOutcome{Approved: approved, Reason: reason, RecordGrant: grant}
		}()
		var entry *approvalEntry
		require.Eventually(t, func() bool {
			for _, pending := range f.reg.pendingApprovals() {
				if pending.SessionID == sid && pending.ToolCallID == callID {
					entry = pending
					return true
				}
			}
			return false
		}, 5*time.Second, time.Millisecond, "instrument: iteration %d must have a real pending card", i)
		// Same ordering as RequestCancel.interruptGracefully: Interrupt cancels
		// the turn context BEFORE CancelPendingApprovals resolves the card.
		cancel()
		hooks.CancelPendingApprovals(sid, denialReasonSessionCanceled)
		select {
		case outcome := <-done:
			require.False(t, outcome.Approved, "R-S3: a stopped request must not be approved")
			require.False(t, outcome.RecordGrant, "R-S3: Stop must not create a standing grant")
			branches[outcome.Reason]++
		case <-time.After(5 * time.Second):
			t.Fatal("instrument: canceled approval waiter did not finish")
		}
		entries = append(entries, entry)
	}
	require.Empty(t, f.reg.pendingApprovals(), "instrument: every stopped card reached a terminal state")
	rows := q7AuditRead(t, f, sid, "set_channel_outbound", false)
	requested, denied, granted := 0, 0, 0
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if fields["session_id"] != sid {
			continue
		}
		switch row["event"] {
		case audit.EventToolPolicyAskRequested:
			requested++
		case audit.EventToolPolicyAskDenied:
			denied++
		case audit.EventToolPolicyAskGranted:
			granted++
		}
	}
	t.Logf("Stop audit: requested=%d denied=%d granted=%d iterations=%d waiter_reasons=%v", requested, denied, granted, iterations, branches)
	require.Equal(t, iterations, requested, "R-S3: one request audit for every stopped ask")
	require.Equal(t, iterations, denied, "R-S3 / F1: every session Stop must audit exactly one terminal cancel, even when registry cancellation wins the waiter race")
	require.Zero(t, granted, "R-S3: cancellation never writes a granted decision")
	for _, entry := range entries {
		request := q7AuditEvent(t, rows, audit.EventToolPolicyAskRequested, entry.ApprovalID)
		q7AuditIdentity(t, request, entry, hash)
		decision := q7AuditEvent(t, rows, audit.EventToolPolicyAskDenied, entry.ApprovalID)
		q7AuditIdentity(t, decision, entry, hash)
		require.Equal(t, "cancel", decision["reason"], "FR-047: Stop is a cancel, not a user denial or timeout")
		require.Equal(t, "", decision["approver_user_id"], "R-S3: system cancellation has no fabricated human approver")
	}
}
