package discord

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestT15b_PersonStart_RealManagerKeepsReplyPlaceholderWhileOpeningTheDM(t *testing.T) {
	// S-85 case3/S-12: never substitute a fake DM opener inside the adapter.
	start := t09StartMessage(t, "333", "S1")
	start.AgentID, start.WorkspaceID, start.Operation = "mia", "ws-t15", bus.OperationStart
	adapter, network := t09Adapter(t,
		t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"999","type":1}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"777","channel_id":"999","content":"S1"}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"reply-placeholder","channel_id":"333","content":"R1"}`},
	)
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{}}
	mgr, err := channels.NewManager(cfg, nil, mb, nil)
	require.NoError(t, err)
	// Seed initial state before dispatch starts. The real adapter/session is
	// already running at the external-HTTP fixture boundary; no live gateway.
	cfg.Channels["discord"] = config.ChannelInstanceConfig{
		Type: "discord", Enabled: true, WorkspaceID: "ws-t15",
		Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"},
		Outbound: &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"discord:333"}},
	}
	mgr.SetPolicySource(func() *config.Config { return cfg })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, mgr.StartAll(ctx))
	mgr.RegisterChannel("discord", adapter)
	t.Cleanup(func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		require.NoError(t, mgr.StopAll(stopCtx))
	})
	mgr.RecordPlaceholder("discord", "333", "reply-placeholder")
	require.NoError(t, mgr.SendMessage(ctx, start))
	startRoutes := make([]string, 0, 5) // S-85: four start requests, then the one reply edit below
	startRoutes = append(startRoutes, t09Route("GET", "/channels/333"), t09Route("GET", "/users/333"),
		t09Route("POST", "/users/@me/channels"), t09Route("POST", "/channels/999/messages"))
	calls := t09RequireRoutes(t, network, startRoutes)
	var recipient map[string]string
	require.NoError(t, json.Unmarshal([]byte(calls[2].Body), &recipient))
	require.Equal(t, map[string]string{"recipient_id": "333"}, recipient)
	var posted map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(calls[3].Body), &posted))
	require.JSONEq(t, `"S1"`, string(posted["content"]))
	// The start did not steal the reply's placeholder: the reply still edits
	// it once, with R1, instead of posting a second ordinary message.
	require.NoError(t, mgr.SendMessage(ctx, bus.OutboundMessage{Channel: "discord", ChatID: "333", Content: "R1"}))
	calls = t09RequireRoutes(t, network, append(startRoutes, t09Route("PATCH", "/channels/333/messages/reply-placeholder")))
	var edited map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(calls[4].Body), &edited))
	require.JSONEq(t, `"R1"`, string(edited["content"]), "S-85: only the owning reply edits its pending message")
}
