package gateway

import (
	"context"
	"fmt"
	"strings"
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

// wsHookDeps runs onEligible on every membership read (1-based), so a test can
// change the receiver's policy at an exact point between decisions.
type wsHookDeps struct {
	*recipientDeps
	n          int
	onEligible func(n int)
}

func (d *wsHookDeps) PairEligible(ws, a string) (bool, error) {
	d.mu.Lock()
	d.n++
	n := d.n
	hook := d.onEligible
	d.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return d.recipientDeps.PairEligible(ws, a)
}

type denyApprover struct{ calls *int }

func (d denyApprover) RequestApproval(context.Context, agent.PolicyApprovalReq) (bool, string, bool) {
	*d.calls++
	return false, "user", false
}

// U8 security r3 F12: Allow at the preflight, Ask by the time admission reads
// the policy, the person DENIES. A denied late Ask must leave the source
// history, the receiver's main and the request untouched.
func TestRecipient_DeniedLateAskLeavesSourceHistoryUntouched(t *testing.T) {
	h, b, deps := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	inst, ok := h.agentLoop.GetRegistry().GetAgent("ray")
	require.True(t, ok)
	hd := &wsHookDeps{recipientDeps: deps}
	h.agentLoop.SetAddressDeps(hd)
	asks := 0
	h.agentLoop.SetToolApprover(denyApprover{calls: &asks})
	hd.mu.Lock()
	base := hd.n
	hd.onEligible = func(n int) {
		if n > base+1 { // after the preflight's own read
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAsk}})
		}
	}
	hd.mu.Unlock()
	store := h.agentLoop.ResolveSessionStore(sid)
	before, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	drainWC := func() int {
		n := 0
		for {
			select {
			case <-wc.sendCh:
				n++
			default:
				return n
			}
		}
	}
	drainWC()

	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray late ask", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

	assert.Equal(t, 1, asks, "the newly required Ask must be put to the person")
	_, started := nextInbound(t, b, 300*time.Millisecond)
	assert.False(t, started, "no request may be published after a denial")
	after, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "a denied late Ask must not leave the request saved in the source chat")
	mainID, _ := session.MainSessionID(to.WorkspaceID, to.AgentID)
	if _, err := store.GetMeta(mainID); err == nil {
		t.Error("the receiver's main was created for a denied request")
	}
	for i := drainWC(); i > 0; i-- { // the only frame allowed is the error
	}
}

// ---- U8 r4 F12/F13 ----

// userMessageEchoes counts user_message frames queued to a connection.
func userMessageEchoes(wc *wsConn) int {
	n := 0
	for {
		select {
		case b := <-wc.sendCh:
			if strings.Contains(string(b), `"type":"user_message"`) {
				n++
			}
		default:
			return n
		}
	}
}

// F12 (all late denials): the receiver policy is tightened to Ask at the Nth
// membership read, whichever decision that read belongs to, and the person
// DENIES. Whenever a denial happened, the source history, the echo, the
// receiver's main and the inbound request must all be untouched.
func TestRecipient_EveryLateDeniedAskLeavesNoSourceEffects(t *testing.T) {
	for n := 2; n <= 4; n++ {
		t.Run(fmt.Sprintf("tighten at membership read %d", n), func(t *testing.T) {
			h, b, deps := newRecipientHandler(t)
			wc := makeTestConn()
			sid := mintOwnerChat(t, h, b, wc)
			inst, ok := h.agentLoop.GetRegistry().GetAgent("ray")
			require.True(t, ok)
			hd := &wsHookDeps{recipientDeps: deps}
			h.agentLoop.SetAddressDeps(hd)
			asks := 0
			h.agentLoop.SetToolApprover(denyApprover{calls: &asks})
			hd.mu.Lock()
			hd.onEligible = func(read int) {
				if read == n {
					inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAsk}})
				}
			}
			hd.mu.Unlock()
			store := h.agentLoop.ResolveSessionStore(sid)
			before, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			userMessageEchoes(wc) // drain the owner chat's own frames
			to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}

			h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray late ask", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

			after, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			echoes := userMessageEchoes(wc)
			_, started := nextInbound(t, b, 300*time.Millisecond)
			mainID, _ := session.MainSessionID(to.WorkspaceID, to.AgentID)
			_, mainErr := store.GetMeta(mainID)
			if asks > 0 { // a denial happened: nothing may have happened anywhere
				assert.Len(t, after, len(before), "a denied Ask must not leave the request saved in the source chat")
				assert.Zero(t, echoes, "a denied Ask must not echo the request")
				assert.False(t, started, "no request may be published after a denial")
				assert.Error(t, mainErr, "the receiver's main must not exist after a denial")
			} else { // no Ask was reached at this read: the request is admitted normally
				assert.True(t, started, "an unasked request must be admitted")
			}
		})
	}
}

// F13: an approval receipt exists only if an approval/grant actually occurred.
// The receiver policy flips to Ask right after the first admission policy read;
// with an approver that DENIES, a request may be admitted only if the final
// admission itself read Allow, never on a flag manufactured from a later sample.
func TestRecipient_NoApprovalIsAssertedWithoutOne(t *testing.T) {
	for flipAfter := 1; flipAfter <= 3; flipAfter++ {
		t.Run(fmt.Sprintf("tighten after policy read %d", flipAfter), func(t *testing.T) {
			h, b, _ := newRecipientHandler(t)
			wc := makeTestConn()
			sid := mintOwnerChat(t, h, b, wc)
			inst, ok := h.agentLoop.GetRegistry().GetAgent("ray")
			require.True(t, ok)
			asks := 0
			h.agentLoop.SetToolApprover(denyApprover{calls: &asks})
			var reads []string
			h.agentLoop.SetPolicyReadHookForTest(func(p string) {
				reads = append(reads, p)
				if len(reads) == flipAfter {
					inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAsk}})
				}
			})
			to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}

			h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray x", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

			_, started := nextInbound(t, b, 400*time.Millisecond)
			if started && asks == 0 {
				require.NotEmpty(t, reads)
				assert.Equal(t, string(config.ToolPolicyAllow), reads[len(reads)-1],
					"admitted without an approver call although the final policy read the request was decided on was %v", reads)
			}
		})
	}
}
