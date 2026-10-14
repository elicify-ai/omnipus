package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final-gate SG-01 (High): another agent's peer request carries no human
// authority. The envelope is produced by the REAL SendPeer/AdmitRequest; it is
// then consumed against the d2b chat (retargeted by SessionID only, since the
// harness chat is not a computed main). However the person's Stop lands, a
// pending peer request must neither clear it nor start a turn.

func peerRequestEnvelope(t *testing.T, h *d2bRoot) bus.InboundMessage {
	t.Helper()
	deps := &fakeAddressDeps{eligible: map[string]bool{addrWS + "/" + testDefaultAgentID: true}}
	h.al.SetAddressDeps(deps)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAllow}})
	_, err := h.al.NewAddressRouter().SendPeer(context.Background(), tools.PeerRequest{
		RecipientWorkspaceID: addrWS, RecipientAgentID: testDefaultAgentID, Content: "please review X",
		Sender: tools.SendOrigin{WorkspaceID: addrWS, AgentID: "ray"}, SenderSessionID: "sender-session",
	})
	require.NoError(t, err)
	select {
	case m := <-h.al.bus.InboundChan():
		m.SessionID = h.id // consume against the harness chat
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("the peer request was not published")
		return bus.InboundMessage{}
	}
}

func TestPeerRequest_AfterLandedStop_DoesNotReviveOrRun(t *testing.T) {
	h := newD2bRoot(t)
	req := peerRequestEnvelope(t, h)         // pending ...
	h.seedState(t, session.LifecycleStopped) // ... a person's Stop lands ...
	before := h.journal(t)

	reply, _, perr := h.al.processMessageAsPrimaryForTest(context.Background(), req) // ... then it is consumed

	require.NoError(t, perr, "a stop is not a failure: the retained request reports no error")
	assert.Empty(t, reply)
	assert.Empty(t, h.provider.calls(), "a peer request must never run the model on a stopped chat")
	got := h.load(t)
	assert.Equal(t, session.LifecycleStopped, got.State, "the person's Stop must still hold")
	assert.Equal(t, 1, got.Generation)
	assert.Equal(t, string(before), string(h.journal(t)), "the request must not append to the lifecycle journal")
}

func TestPeerRequest_IsNotAHumanTurnAndNeverAnOperatorPrompt(t *testing.T) {
	h := newD2bRoot(t)
	req := peerRequestEnvelope(t, h)
	assert.False(t, reviveInboundIsHumanTurn(req), "an agent's request carries no human revival authority")
	assert.False(t, req.OperatorPrompt)
	assert.False(t, req.UserInitiated)
}

func TestPeerRequest_ControlsLiveChatStillGetsItAndHumanStillRevives(t *testing.T) {
	h := newD2bRoot(t)
	h.seedState(t, session.LifecycleCompleted) // an idle, unstopped chat
	req := peerRequestEnvelope(t, h)
	_, _, err := h.al.processMessageAsPrimaryForTest(context.Background(), req)
	require.NoError(t, err)
	assert.Len(t, h.provider.calls(), 1, "an unstopped receiver must still run on a peer request")

	h2 := newD2bRoot(t)
	h2.seedState(t, session.LifecycleStopped)
	_, err = h2.humanTurn(t, "carry on")
	require.NoError(t, err)
	assert.Len(t, h2.provider.calls(), 1, "a person's message must still continue a stopped chat")
	assert.NotEqual(t, session.LifecycleStopped, h2.load(t).State)
}
