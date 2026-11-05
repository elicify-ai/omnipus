package discord

// T-34/T-5/FR-006: moved from pkg/agent so the real adapter's private session
// can be seeded and inspected without a production seam. T-25 covers agent-side
// per-turn wiring. These label oracles are from T-5 and S-14, not adapter output.

import (
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestT34_ChannelsKindLabelsUseOnlyTheRealLocalCache(t *testing.T) {
	for _, tc := range []struct {
		name       string
		id         string
		kind       discordgo.ChannelType
		cached     bool
		targetLine string
	}{
		{"rest_success_cache_miss_is_person_or_group", "604", 11, false, "  - 604 (person or group)"},
		{"supported_local_cache_hit_is_group", "605", 5, true, "  - 605 (group)"},
		{"unsupported_local_cache_hit_is_unknown_label", "601", 15, true, "  - 601 (person or group)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, network := t09Adapter(t, t09HTTPResponse{Status: http.StatusOK, Body: `{"id":"604","type":11}`})
			previous := http.DefaultTransport
			http.DefaultTransport = network
			t.Cleanup(func() { http.DefaultTransport = previous })

			// Positive control for the HTTP observer, and T-5's actual REST/cache
			// premise: decoding a public thread does not populate discordgo.State.
			_, cacheErr := adapter.session.State.Channel("604")
			require.ErrorIs(t, cacheErr, discordgo.ErrStateNotFound, "instrument: actual State initially misses 604")
			resolved, err := adapter.session.Channel("604", discordgo.WithRestRetries(0))
			require.NoError(t, err, "instrument: real Discord REST decoding succeeds")
			require.Equal(t, "604", resolved.ID)
			require.Equal(t, discordgo.ChannelTypeGuildPublicThread, resolved.Type, "S-14: 604 is a public thread")
			_, cacheErr = adapter.session.State.Channel("604")
			require.ErrorIs(t, cacheErr, discordgo.ErrStateNotFound, "T-5: REST success is not evidence of a local cache hit")
			beforeRender := t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/604")})

			if tc.cached {
				require.NoError(t, adapter.session.State.GuildAdd(&discordgo.Guild{ID: "qa-t34-labels-guild"}))
				require.NoError(t, adapter.session.State.ChannelAdd(&discordgo.Channel{ID: tc.id, GuildID: "qa-t34-labels-guild", Type: tc.kind}))
				cached, stateErr := adapter.session.State.Channel(tc.id)
				require.NoError(t, stateErr, "instrument: actual State contains the label case")
				require.Equal(t, tc.id, cached.ID)
				require.Equal(t, tc.kind, cached.Type, "S-14: 605 is announcement; 601 is forum")
			}

			manager := channels.NewManagerForTesting(nil)
			manager.RegisterChannel("discord.qa", adapter)
			cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{
				"discord.qa": {
					Type: "discord", Enabled: true, WorkspaceID: "ws-t34",
					Identity: &config.ChannelIdentity{Kind: "agent", ID: "agent-t34"},
					Outbound: &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"discord:" + tc.id}},
				},
			}}
			got := channels.RenderChannelsBlock(channels.ChannelsBlockInput{
				Config: cfg, WorkspaceID: "ws-t34", AgentID: "agent-t34",
				Availability: manager.Availability, CachedGroup: manager.CachedPostableChannel,
			})
			want := "## Channels\nConnections bound to you in this workspace (live, this turn):\n" +
				"- discord.qa (Discord): you may also start messages to:\n" + tc.targetLine +
				"\n  To start one: send_message channel=\"discord.qa\" chat_id=\"<one id above>\" content=\"…\". The result says \"accepted for sending (delivery not confirmed)\"."
			require.Equal(t, want, got, "T-5: the complete real block uses only the supported local-cache evidence for its kind hint")
			require.Equal(t, beforeRender, network.snapshot(), "FR-006/S-06: rendering adds exactly zero HTTP calls after the observed REST control")
		})
	}
}
