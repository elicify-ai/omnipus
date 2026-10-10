package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wsRevokingApprover approves, but only after running revoke: the change made
// while the person's approval is pending.
type wsRevokingApprover struct{ revoke func() }

func (r wsRevokingApprover) RequestApproval(context.Context, agent.PolicyApprovalReq) (bool, string, bool) {
	r.revoke()
	return true, "", false
}

// U8 F1 remainder: authorization revoked while an Ask approval is pending, over
// the real WebSocket intake, must leave the source chat untouched - nothing
// saved, nothing echoed, no capture, no receiver main, no inbound request.
func TestRecipient_RevocationDuringAskSavesNothingInTheSourceChat(t *testing.T) {
	cases := map[string]func(h *WSHandler, d *recipientDeps){
		"membership removed": func(_ *WSHandler, d *recipientDeps) { d.mu.Lock(); d.eligible = map[string]bool{}; d.mu.Unlock() },
		"policy became deny": func(h *WSHandler, _ *recipientDeps) {
			inst, ok := h.agentLoop.GetRegistry().GetAgent("ray")
			require.True(t, ok)
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyDeny}})
		},
	}
	for name, revoke := range cases {
		t.Run(name, func(t *testing.T) {
			h, b, deps := newRecipientHandler(t)
			wc := makeTestConn()
			sid := mintOwnerChat(t, h, b, wc)
			inst, ok := h.agentLoop.GetRegistry().GetAgent("ray")
			require.True(t, ok)
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAsk}})
			h.agentLoop.SetToolApprover(wsRevokingApprover{revoke: func() { revoke(h, deps) }})
			store := h.agentLoop.ResolveSessionStore(sid)
			before, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}

			h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray secret request", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

			_, started := nextInbound(t, b, 300*time.Millisecond)
			assert.False(t, started, "no request may be published for a revoked authorization")
			after, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			assert.Len(t, after, len(before), "the refused request must not be saved in the source chat")
			mainID, _ := session.MainSessionID(to.WorkspaceID, to.AgentID)
			if _, err := store.GetMeta(mainID); err == nil {
				t.Error("the receiver's main was created for a revoked request")
			}
		})
	}
}

// Positive control: approved and still authorized admits, and the message is saved.
func TestRecipient_ApprovedAskStillAuthorizedIsAdmitted(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	inst, ok := h.agentLoop.GetRegistry().GetAgent("ray")
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAsk}})
	h.agentLoop.SetToolApprover(wsRevokingApprover{revoke: func() {}})
	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}

	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray please review", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

	in, ok := nextInbound(t, b, 2*time.Second)
	require.True(t, ok, "an approved, still-authorized request must be published")
	assert.Equal(t, "ray", in.Metadata["agent_id"])
}
