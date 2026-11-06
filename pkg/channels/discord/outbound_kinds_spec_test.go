package discord

// T-34: the complete T-9 ChannelType dataset, not a predicate copied from the
// implementation. Real cache/session/resolver; only external HTTP is scripted.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/require"
)

func TestT34_AllChannelKindsAgreeBetweenRealCacheAndREST(t *testing.T) {
	// Spec T-9 enumerates five supported and nine unsupported kinds. Values
	// 7,8,9 are not defined ChannelType constants in the pinned dependency.
	cases := []struct {
		name string
		kind discordgo.ChannelType
		code channels.TargetErrorCode
	}{
		{"text", 0, ""},
		{"announcement", 5, ""},
		{"announcement_thread", 10, ""},
		{"public_thread", 11, ""},
		{"private_thread", 12, ""},
		{"dm_channel", 1, "unsupported_channel_kind"},
		{"voice", 2, "unsupported_channel_kind"},
		{"group_dm_channel", 3, "unsupported_channel_kind"},
		{"category", 4, "unsupported_channel_kind"},
		{"store", 6, "unsupported_channel_kind"},
		{"stage_voice", 13, "unsupported_channel_kind"},
		{"directory", 14, "unsupported_channel_kind"},
		{"forum", 15, "unsupported_channel_kind"},
		{"media", 16, "unsupported_channel_kind"},
	}
	for _, tc := range cases {
		for _, source := range []string{"cache", "rest"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				ch, network := t09Adapter(t, t09HTTPResponse{Status: http.StatusOK, Body: fmt.Sprintf(`{"id":"601","type":%d}`, tc.kind)})
				routes := []string{t09Route("GET", "/channels/601")}
				if source == "cache" {
					require.NoError(t, ch.session.State.GuildAdd(&discordgo.Guild{ID: "qa-t34-guild"}))
					require.NoError(t, ch.session.State.ChannelAdd(&discordgo.Channel{ID: "601", GuildID: "qa-t34-guild", Type: tc.kind}))
					cached, err := ch.session.State.Channel("601")
					require.NoError(t, err, "instrument: actual State contains this case")
					require.Equal(t, tc.kind, cached.Type)
					routes = []string{}
				} else {
					_, err := ch.session.State.Channel("601")
					require.ErrorIs(t, err, discordgo.ErrStateNotFound, "instrument: real cache misses before REST resolution")
				}
				result := t09Resolver(t, ch).ResolveOutboundTarget(context.Background(), "discord:601")
				require.Equal(t, tc.code, result.Code, "T-9: %s kind%d from%s", tc.name, tc.kind, source)
				t09RequireRoutes(t, network, routes) // includes zero network for cache; no User fallback for any known channel kind.
			})
		}
	}
}

func TestT34_DeadlineErrorsAreUnavailableNotNotFoundAtEitherRESTStep(t *testing.T) {
	for _, step := range []string{"channel", "user"} {
		t.Run(step, func(t *testing.T) {
			responses := []t09HTTPResponse{{Err: context.DeadlineExceeded}}
			routes := []string{t09Route("GET", "/channels/606")}
			if step == "user" {
				responses = append([]t09HTTPResponse{{Status: http.StatusNotFound, Body: `{"code":10003,"message":"Unknown Channel"}`}}, responses...)
				routes = append(routes, t09Route("GET", "/users/606"))
			}
			ch, network := t09Adapter(t, responses...)
			result := t09Resolver(t, ch).ResolveOutboundTarget(context.Background(), "discord:606")
			require.Equal(t, channels.TargetErrorCode("lookup_unavailable"), result.Code, "T-9/S-14: a transport deadline error is not definite not_found")
			t09RequireRoutes(t, network, routes)
		})
	}
}

func TestT34_ExternalHTTPControl_RESTChannelSuccessDoesNotPopulateLocalCache(t *testing.T) {
	ch, network := t09Adapter(t, t09HTTPResponse{Status: http.StatusOK, Body: `{"id":"604","type":11}`})
	_, err := ch.session.State.Channel("604")
	require.ErrorIs(t, err, discordgo.ErrStateNotFound, "instrument: cache initially misses")
	channel, err := ch.session.Channel("604", discordgo.WithRestRetries(0))
	require.NoError(t, err, "instrument: actual REST decoder succeeds")
	require.Equal(t, "604", channel.ID)
	require.Equal(t, discordgo.ChannelTypeGuildPublicThread, channel.Type)
	_, err = ch.session.State.Channel("604")
	require.ErrorIs(t, err, discordgo.ErrStateNotFound, "T-5: REST success does not establish a local cache label")
	t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/604")})
}
