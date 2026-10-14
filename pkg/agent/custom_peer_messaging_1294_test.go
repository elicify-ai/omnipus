package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder decision #1294 (2026-10-14): "new custom agents can talk to each
// other by default; can still be switched off per agent". Oracle: a custom
// agent built by the real creation constructor (coreagent.NewCustomAgentToolsCfg,
// the one both REST create and create_agent use) is admitted as a peer
// receiver by the real address router, and its own registered answer_request
// tool delivers the answer back to the asker. Control: the same constructor
// with an explicit per-agent send_message deny is refused.
func TestFounder1294_FreshCustomAgentReceivesAndAnswersAPeerRequest(t *testing.T) {
	const ws = "ws-1294"
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	closed := coreagent.NewCustomAgentToolsCfg()
	closed.Builtin.Policies["send_message"] = config.ToolPolicyDeny
	cfg.Agents.List = []config.AgentConfig{
		{ID: "jim", Type: config.AgentTypeCore},
		{ID: "scout", Name: "Scout", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()},
		{ID: "closed", Name: "Closed", Type: config.AgentTypeCustom, Tools: closed},
	}
	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(al.Close)
	al.SetAddressDeps(&fakeAddressDeps{eligible: map[string]bool{ws + "/scout": true, ws + "/closed": true}})

	asker, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)
	wsCopy := ws
	require.NoError(t, al.GetSessionStore().SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &wsCopy}))
	request := func(to string) (tools.PeerReceipt, error) {
		return al.NewAddressRouter().SendPeer(context.Background(), tools.PeerRequest{
			RecipientWorkspaceID: ws, RecipientAgentID: to, Content: "what is the launch date?",
			Sender: tools.SendOrigin{WorkspaceID: ws, AgentID: "jim"}, SenderSessionID: asker.ID,
		})
	}

	t.Run("explicit_deny_still_refuses", func(t *testing.T) {
		_, refusal := request("closed")
		require.Error(t, refusal)
		assert.True(t, errors.Is(refusal, ErrPeerRefused), "per-agent deny must refuse the peer request: %v", refusal)
	})

	receipt, err := request("scout")
	require.NoError(t, err, "#1294: a fresh custom agent is a peer receiver by default")
	require.NotEmpty(t, receipt.RequestID)
	wake, ok := addrDrainInbound(t, msgBus)
	require.True(t, ok, "the receiver's turn is started with the request")
	assert.Equal(t, receipt.SessionID, wake.SessionID)

	scout, ok := al.GetRegistry().GetAgent("scout")
	require.True(t, ok)
	assert.Equal(t, "allow", tools.ResolveEffectivePolicy(scout.LoadToolPolicy(), "answer_request"),
		"#1294: the receiver may call answer_request")
	answer, ok := scout.Tools.Get("answer_request")
	require.True(t, ok, "answer_request is registered on the custom agent")
	ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), ws), "scout")
	ctx = tools.WithTranscriptSessionID(ctx, receipt.SessionID)
	res := answer.Execute(ctx, map[string]any{"request_id": receipt.RequestID, "text": "LAUNCH-ON-NOV-3"})
	require.False(t, res.IsError, res.ForLLM)

	var delivered bool
	for i := 0; i < 5; i++ {
		msg, ok := addrDrainInbound(t, msgBus)
		if !ok {
			break
		}
		if msg.SessionID == asker.ID && msg.Metadata[ownerWakeMetadataKey] != "" {
			assert.Contains(t, msg.Content, "LAUNCH-ON-NOV-3", "the answer body reaches Jim's conversation")
			delivered = true
			break
		}
	}
	assert.True(t, delivered, "#1294: Jim is woken with the custom agent's answer")
}
