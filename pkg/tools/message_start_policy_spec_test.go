package tools

// T-12's real start-policy fixture. This is only the policy injection bridge;
// classification stays in the actual MessageTool. T-32 independently exercises
// the actual AgentLoop-installed policy and bus callback. Expected sentences
// below were derived from r8 T-3/S-41/D2 before execution, never from results.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

type t12StartCall struct{ channel, chat, workspace, agent string }
type t12Publication struct {
	channel, chat, content string
	origin                 SendOrigin
}

type t12RunningAdapter struct{ *channels.BaseChannel }

func (*t12RunningAdapter) Start(context.Context) error { return nil }
func (*t12RunningAdapter) Stop(context.Context) error  { return nil }
func (*t12RunningAdapter) Send(context.Context, bus.OutboundMessage) error {
	return errors.New("SETUP: policy fixture must not deliver")
}

type t12StartPolicy struct {
	t       *testing.T
	cfg     *config.Config
	live    func() *config.Config
	manager *channels.Manager
	calls   []t12StartCall
	adapter *t12RunningAdapter
}

func (*t12StartPolicy) IsConnector(channel string) bool {
	switch channel {
	case "webchat", "system", "internal", "":
		return false
	default:
		return true
	}
}

func (p *t12StartPolicy) CheckStart(channel, chat, workspace, agent string) StartVerdict {
	p.calls = append(p.calls, t12StartCall{channel, chat, workspace, agent})
	snapshot := p.live()
	decision := channels.StartAllowed(snapshot, p.manager.Availability(channel), channel, chat, workspace, agent)
	if !decision.Allowed {
		return StartVerdict{Text: decision.Text}
	}
	canonical, code := channels.CanonicalizeTarget(channels.InstancePlatform(channel, snapshot.Channels[channel]), chat)
	require.Empty(p.t, code, "instrument: an allowed target must canonicalize")
	return StartVerdict{Allowed: true, BareChatID: channels.BareTargetID(canonical)}
}

func t12WiredStart(t *testing.T, platform string) (*MessageTool, *t12StartPolicy, *[]t12Publication) {
	t.Helper()
	p := &t12StartPolicy{t: t, manager: channels.NewManagerForTesting(nil), cfg: &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{platform: {
			Type: platform, Enabled: true, WorkspaceID: "ws-t12", Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			// D2 writes its fixture list as [222]; store that literal bare id,
			// avoiding an invented display choice for T-3's Listed placeholder.
			Outbound: &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"222"}},
		}},
	}}
	p.live = func() *config.Config { return p.cfg }
	p.adapter = &t12RunningAdapter{channels.NewBaseChannel(platform, nil, nil, nil)}
	p.adapter.SetRunning(true)
	p.manager.RegisterChannel(platform, p.adapter)
	p.manager.SetPolicySource(p.live)
	publications := []t12Publication{}
	tool := NewMessageTool()
	tool.SetStartPolicy(p)
	tool.SetMainConnectorGuard(func(sessionID, channel string) bool { return sessionID == "main-1" && p.IsConnector(channel) })
	tool.SetSendCallback(func(channel, chat, content string, origin SendOrigin) error {
		publications = append(publications, t12Publication{channel, chat, content, origin})
		return nil
	})
	return tool, p, &publications
}

func t12StartCtx(session, channel, chat string) context.Context {
	return WithTranscriptSessionID(WithToolContext(WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-t12"), channel, chat), session)
}

func t12RequireAccepted(t *testing.T, tool *MessageTool, result *ToolResult, publications []t12Publication, channel, chat, content string) {
	t.Helper()
	require.NoError(t, result.Err)
	require.False(t, result.IsError)
	require.True(t, result.Silent)
	require.Equal(t, fmt.Sprintf("accepted for sending to %s:%s (delivery not confirmed)", channel, chat), result.ForLLM, "FR-041 exact success sentence, never a delivery claim")
	require.Equal(t, []t12Publication{{channel, chat, content, SendOrigin{AgentID: "mia", WorkspaceID: "ws-t12", Operation: "start"}}}, publications, "FR-043: one start-classified publication, bare id and exact acting pair")
	require.True(t, tool.HasSentInRound())
}

func t12RequireRefused(t *testing.T, tool *MessageTool, result *ToolResult, publications []t12Publication, want string) {
	t.Helper()
	require.True(t, result.IsError)
	require.ErrorIs(t, result.Err, ErrStartRefused)
	require.Equal(t, want, result.ForLLM, "S-41: whole independent T-3 refusal sentence")
	require.Equal(t, []t12Publication{}, publications, "refused start publishes nothing")
	require.False(t, tool.HasSentInRound(), "a refusal is not a sent round")
}

type t12RefusalRow struct {
	name, platform, channel, chat, want string
	setup                               func(*t12StartPolicy)
}

func t12RefusalRows() []t12RefusalRow {
	unchanged := func(*t12StartPolicy) {}
	return []t12RefusalRow{
		{"unknown", "discord", "bogus_channel_xyz", "222", `no connection named "bogus_channel_xyz": check your Channels block or list_channels. Nothing was sent.`, unchanged},
		{"bound to B", "discord", "discord", "222", `cannot start a message on "discord": it is not bound to you in this workspace (you have no connection here). Nothing was sent.`, func(p *t12StartPolicy) { p.cfg.Channels["discord"].Identity.ID = "other-agent" }},
		{"unbound", "discord", "discord", "222", `cannot start a message on "discord": it is not bound to you in this workspace (you have no connection here). Nothing was sent.`, func(p *t12StartPolicy) {
			inst := p.cfg.Channels["discord"]
			inst.WorkspaceID, inst.Identity = "", nil
			p.cfg.Channels["discord"] = inst
		}},
		{"unsupported slack", "slack", "slack", "C1", `starting messages is not supported yet on Slack ("slack"). Nothing was sent.`, func(p *t12StartPolicy) { p.cfg.Channels["slack"].Outbound.Targets = []string{"C1"} }},
		{"reply only", "discord", "discord", "222", `cannot start a message on "discord": this connection is set to "Only answer". The operator can change it in Connectors > Discord > Configure ("Answer and also start messages"). Nothing was sent.`, func(p *t12StartPolicy) {
			inst := p.cfg.Channels["discord"]
			inst.Outbound = nil
			p.cfg.Channels["discord"] = inst
		}},
		{"unlisted", "discord", "discord", "555", `cannot start a message to "555" on "discord": it is not on this connection's list. Listed: 222. Nothing was sent.`, unchanged},
		{"not running", "discord", "discord", "222", `"discord" is configured but not connected right now (not started). Nothing was sent.`, func(p *t12StartPolicy) { p.adapter.SetRunning(false) }},
	}
}

func TestT12_StartPolicyWired_MainRequiresBothExplicitRawArguments(t *testing.T) {
	// Spec Regression test requirements: keep the unwired main refusal test
	// unchanged, and add this wired counterpart for T-4 step2 / FR-042.
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"content only", map[string]any{"content": "x"}},
		{"channel only", map[string]any{"content": "x", "channel": "telegram"}},
		{"chat only", map[string]any{"content": "x", "chat_id": "222"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, policy, sent := t12WiredStart(t, "telegram")
			res := tool.Execute(t12StartCtx("main-1", "telegram", "chat-1"), tc.args)
			require.True(t, res.IsError)
			require.ErrorIs(t, res.Err, ErrMainConnectorReplyOnly)
			require.Equal(t, "in your main conversation a connector is reached only by replying to a request (send_message reply_to=<request id> with your answer as content), or by starting a message with both channel and chat_id where your Channels block allows it. Nothing was sent.", res.ForLLM)
			require.Empty(t, policy.calls, "T-4 step2 precedes T-3")
			require.Equal(t, []t12Publication{}, *sent)
		})
	}
	t.Run("both explicit unlisted reaches T3", func(t *testing.T) {
		tool, policy, sent := t12WiredStart(t, "telegram")
		res := tool.Execute(t12StartCtx("main-1", "telegram", "chat-1"), map[string]any{"content": "x", "channel": "telegram", "chat_id": "555"})
		t12RequireRefused(t, tool, res, *sent, `cannot start a message to "555" on "telegram": it is not on this connection's list. Listed: 222. Nothing was sent.`)
		require.Equal(t, []t12StartCall{{"telegram", "555", "ws-t12", "mia"}}, policy.calls)
	})
}

func TestT12_StartPolicyWired_PreservedFormsBypassStartCheck(t *testing.T) {
	for _, tc := range []struct{ name, session, channel, chat string }{
		{"main webchat", "main-1", "webchat", "w"},
		{"nonmain own connector chat", "chat-session", "telegram", "chat-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, policy, sent := t12WiredStart(t, "telegram")
			res := tool.Execute(t12StartCtx(tc.session, tc.channel, tc.chat), map[string]any{"content": "unchanged"})
			require.False(t, res.IsError)
			require.NoError(t, res.Err)
			require.Equal(t, "Message sent to "+tc.channel+":"+tc.chat, res.ForLLM)
			require.Len(t, *sent, 1)
			require.Equal(t, "checked", (*sent)[0].origin.Operation, "FR-043: non-start is checked, never a start")
			require.Empty(t, policy.calls)
		})
	}
	t.Run("steered restriction before start", func(t *testing.T) {
		tool, policy, sent := t12WiredStart(t, "discord")
		tool.SetSteerAudienceResolver(&fakeSteerAudience{audience: map[string]steer.Audience{"child-1": steer.AudienceSteeringSession}})
		res := tool.Execute(t12StartCtx("child-1", "telegram", "chat-1"), map[string]any{"content": "x", "channel": "discord", "chat_id": "222"})
		require.True(t, res.IsError)
		require.ErrorIs(t, res.Err, ErrSteeredSessionOwnChatOnly)
		require.Empty(t, policy.calls, "T-4 step1 cannot be bypassed by a grant")
		require.Equal(t, []t12Publication{}, *sent)
	})
	t.Run("captured reply precedes classification", func(t *testing.T) {
		_, policy, _ := t12WiredStart(t, "telegram")
		tool, _, reply, sent := addrTool()
		tool.SetStartPolicy(policy)
		tool.SetMainConnectorGuard(t12ConnectorGuard)
		res := tool.Execute(t12MainCtx(), map[string]any{"content": "here you go", "reply_to": "q1"})
		require.False(t, res.IsError)
		require.NoError(t, res.Err)
		require.Len(t, reply.calls, 1)
		require.False(t, *sent)
		require.Empty(t, policy.calls)
	})
}
