package agent

// Q7 / FR-033/038 / T-35: isolated consent gate with a real grant store and
// inheritance. Only the human's one-request decision is scripted. The real
// gateway endpoint/card/persistence integration is tested in pkg/gateway.

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQ7T35_ExistingGrantsCannotReplaceFreshApproval(t *testing.T) {
	for _, scope := range []string{"exact", "inherited_exact"} {
		t.Run(scope, func(t *testing.T) {
			al, cleanup := newAL(t)
			defer cleanup()
			const sid = "q7-acting-session"
			args := map[string]any{"id": "discord", "policy": "agent_may_start", "targets": []string{"222"}, "expected_revision": "q7-unchanged-live-token"}
			store := security.NewApprovalGrantStore()
			if scope == "exact" {
				require.True(t, store.Record(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args))
			} else {
				require.True(t, store.Record("q7-parent-session", string(coreagent.IDOmnipus), "set_channel_outbound", args))
				store.InheritFrom("q7-parent-session", string(coreagent.IDOmnipus), sid, string(coreagent.IDOmnipus))
			}
			require.True(t, store.IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args), "instrument: a real matching grant must exist, not an empty-key/fingerprint miss")
			al.approvalGrants = store
			approver := &autoRecordingApprover{approve: true}
			al.SetToolApprover(approver)
			for _, callID := range []string{"q7-first", "q7-identical-repeat"} {
				approved, reason, record := al.CheckGrantOrRequestApproval(context.Background(), sid, string(coreagent.IDOmnipus), "set_channel_outbound", callID, "q7-turn", args)
				assert.True(t, approved, "FR-038: the explicit approve-once decision authorizes this call")
				assert.Equal(t, "", reason)
				assert.False(t, record, "FR-038: approve-once requests no standing grant")
			}
			requests := approver.requests()
			require.Len(t, requests, 2, "T-10 R14/S-29: matching grants must not replace either fresh approval, including an identical repeat")
			for i, req := range requests {
				assert.Equal(t, "set_channel_outbound", req.ToolName)
				assert.Equal(t, sid, req.SessionID)
				assert.Equal(t, string(coreagent.IDOmnipus), req.AgentID)
				assert.Equal(t, args, req.Args, "FR-038: a real prompt covers the actual request, not an empty substitute")
				assert.Equal(t, []string{"q7-first", "q7-identical-repeat"}[i], req.ToolCallID)
			}
			require.True(t, store.IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args), "FR-038: ignoring a preexisting grant is not deleting it to force a miss")
		})
	}
}

func TestQ7T35_ApproveOnceRepeatsWithoutAGrant_PositiveControl(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	approver := &autoRecordingApprover{approve: true}
	al.SetToolApprover(approver)
	const sid = "q7-once-control"
	args := map[string]any{"id": "discord", "policy": "reply_only", "targets": []string{}, "expected_revision": "q7-same-token"}
	for _, callID := range []string{"first", "identical-repeat"} {
		approved, reason, record := al.CheckGrantOrRequestApproval(context.Background(), sid, string(coreagent.IDOmnipus), "set_channel_outbound", callID, "q7-turn", args)
		require.True(t, approved)
		require.Equal(t, "", reason)
		require.False(t, record)
		require.False(t, al.ApprovalGrants().IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args), "T-10 R1: approve-once never installs a remembered grant")
	}
	require.Len(t, approver.requests(), 2, "FR-038: a matching repeat must ask again without a grant")
}

func TestQ7T35_OtherToolsKeepExactAndInheritedGrants_PositiveControl(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	store := security.NewApprovalGrantStore()
	args := map[string]any{"id": "telegram", "action": "enable"}
	require.True(t, store.Record("q7-parent", string(coreagent.IDOmnipus), "enable_channel", args))
	store.InheritFrom("q7-parent", string(coreagent.IDOmnipus), "q7-child", string(coreagent.IDOmnipus))
	al.approvalGrants = store
	approver := &autoRecordingApprover{approve: false}
	al.SetToolApprover(approver)
	for _, sid := range []string{"q7-parent", "q7-child"} {
		require.True(t, store.IsAllowed(sid, string(coreagent.IDOmnipus), "enable_channel", args), "instrument: existing ordinary-tool grant resolves")
		approved, reason, record := al.CheckGrantOrRequestApproval(context.Background(), sid, string(coreagent.IDOmnipus), "enable_channel", "q7-other", "q7-turn", args)
		require.True(t, approved, "NB-19: ordinary-tool grants still settle the call")
		require.Equal(t, "", reason)
		require.False(t, record)
	}
	require.Empty(t, approver.requests(), "NB-19: an ordinary granted call must not prompt")
}

func TestQ7T35_GodModeNoteHasOnlyTheSpecifiedException(t *testing.T) {
	// FR-036 preserves the founder's existing wording apart from this one
	// inserted clause; this is literal spec copy, not Replace(actual,...).
	const want = "God Mode is active: approvals are skipped — post the plan summary as an ordinary message and proceed without the Go question and without tool approvals — except set_channel_outbound, which always asks for approval —, including changes another agent asked for. Clarification questions for genuinely missing information still appear. You can write anywhere on this machine and use the network, and programs you start see the gateway environment, including API keys. Omnipus's internal files stay protected."
	require.Equal(t, want, omnipusGodModeNote, "FR-036/Q22 B: add exactly the always-ask exception, preserving every other word")
}
