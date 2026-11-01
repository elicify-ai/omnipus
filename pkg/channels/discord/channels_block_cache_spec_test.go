package discord

// T-25/S-06: real Discord adapter + discordgo.State + Manager cache boundary.
// Expected labels are T-5/T-9's five supported kinds, not adapter predicates.

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

type t25CacheNoREST struct{ calls atomic.Int32 }

func (n *t25CacheNoREST) RoundTrip(*http.Request) (*http.Response, error) {
	n.calls.Add(1)
	return nil, errors.New("QA T25 forbids REST while rendering")
}

func TestT25_Block_RealDiscordCacheLabelsAndNoREST_S06(t *testing.T) {
	network := &t25CacheNoREST{}
	previous := http.DefaultTransport
	http.DefaultTransport = network
	t.Cleanup(func() { http.DefaultTransport = previous })
	s := &discordgo.Session{State: discordgo.NewState(), Client: &http.Client{Transport: network}}
	response, err := s.Client.Get("https://qa-t25.invalid/instrument-control")
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	require.ErrorContains(t, err, "QA T25 forbids REST while rendering")
	require.Equal(t, int32(1), network.calls.Load(), "instrument: real adapter HTTP client reaches the forbidden boundary")
	network.calls.Store(0)
	require.NoError(t, s.State.GuildAdd(&discordgo.Guild{ID: "qa-t25-guild"}))
	for _, tc := range []struct {
		id   string
		kind discordgo.ChannelType
	}{
		{"111", 0}, {"112", 5}, {"113", 10}, {"114", 11}, {"115", 12}, {"116", 2},
	} {
		require.NoError(t, s.State.ChannelAdd(&discordgo.Channel{ID: tc.id, GuildID: "qa-t25-guild", Type: tc.kind}))
		cached, cacheErr := s.State.Channel(tc.id)
		require.NoError(t, cacheErr)
		require.Equal(t, tc.kind, cached.Type, "instrument: actual session cache contains this kind")
	}
	_, err = s.State.Channel("117")
	require.ErrorIs(t, err, discordgo.ErrStateNotFound, "instrument: cache-miss target is genuinely absent")
	adapter := &DiscordChannel{BaseChannel: channels.NewBaseChannel("discord", nil, nil, nil), session: s}
	adapter.SetRunning(true) // local fixture state only; never open Discord
	manager := channels.NewManagerForTesting(nil)
	manager.RegisterChannel("discord.qa", adapter)
	cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{
		"discord.qa": {Type: "discord", Enabled: true, WorkspaceID: "ws-a", Identity: &config.ChannelIdentity{Kind: "agent", ID: "agent-a"},
			Outbound: &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"discord:111", "discord:112", "discord:113", "discord:114", "discord:115", "discord:116", "discord:117"}}},
	}}
	got := channels.RenderChannelsBlock(channels.ChannelsBlockInput{
		Config: cfg, WorkspaceID: "ws-a", AgentID: "agent-a", Availability: manager.Availability, CachedGroup: manager.CachedPostableChannel,
	})
	want := "## Channels\nConnections bound to you in this workspace (live, this turn):\n" +
		"- discord.qa (Discord): you may also start messages to:\n" +
		"  - 111 (group)\n  - 112 (group)\n  - 113 (group)\n  - 114 (group)\n  - 115 (group)\n" +
		"  - 116 (person or group)\n  - 117 (person or group)\n" +
		"  To start one: send_message channel=\"discord.qa\" chat_id=\"<one id above>\" content=\"…\". The result says \"accepted for sending (delivery not confirmed)\"."
	require.Equal(t, want, got, "T-5: five postable cached kinds are groups; unsupported/missing kinds remain ambiguous, never person")
	require.Equal(t, int32(0), network.calls.Load(), "FR-006/S-06: actual adapter and Manager cache path makes exactly zero REST calls")
}
