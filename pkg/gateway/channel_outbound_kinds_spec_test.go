package gateway

// T-34/S-14/S-84: actual outbound REST + actual Discord resolver. External
// HTTP only is scripted. No invented Reason field on the resolver result.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func t34TargetError(t *testing.T, body gen.ChannelOutboundRejection, code, reason string) {
	t.Helper()
	require.NotNil(t, body.TargetErrors, "T-9/S-14: definite refusal names the offending target")
	errors := *body.TargetErrors
	require.Len(t, errors, 1, "one added target is refused; the retained222 is not revalidated")
	require.Equal(t, 1, errors[0].Index, "index is its raw request position")
	require.Equal(t, "601", errors[0].Target)
	require.Equal(t, code, string(errors[0].Code))
	if reason != "" { // The spec supplies fixed reason text for these rows only.
		require.NotNil(t, errors[0].Reason, "T-9: this refusal has an actionable fixed reason")
		require.Equal(t, reason, *errors[0].Reason)
	}
	require.Nil(t, body.OmittedCount, "the single invalid entry is not omitted")
}

func TestT34_UnsupportedKindsHaveTheirExactActionableReasonAndSaveNothing(t *testing.T) {
	const postedReason = "this kind of Discord channel cannot receive a posted message; use a text channel, announcement channel or thread id"
	const personReason = "use the person's user id instead"
	cases := []struct {
		name   string
		kind   int
		reason string
	}{
		{"dm_channel", 1, personReason},
		{"voice", 2, postedReason},
		{"group_dm_channel", 3, personReason},
		{"category", 4, postedReason},
		{"store", 6, postedReason},
		{"stage_voice", 13, postedReason},
		{"directory", 14, postedReason},
		{"forum", 15, postedReason},
		{"media", 16, postedReason},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := t32Fixture(t, "discord", []string{"discord:222"})
			_ = t33RealDiscord(t, f, "discord")
			network := t33ExternalHTTP(t)
			network.handle = func(req *http.Request, _ int) (*http.Response, error) {
				return t33HTTPResponse(req, http.StatusOK, fmt.Sprintf(`{"id":"601","type":%d}`, tc.kind)), nil
			}
			before := t13Before(t, f)
			rec := t14Put(t, f, "discord", "agent_may_start", []string{"222", "601"}, t14Read(t, f, "discord").Revision)
			body := t14Rejection(t, rec, http.StatusBadRequest, "invalid_targets")
			t34TargetError(t, body, "unsupported_channel_kind", tc.reason)
			t13Unchanged(t, f, before)
			paths, _ := network.snapshot()
			require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/601"}, paths, "a known unsupported kind is not retried as a User")
		})
	}
}

func TestT34_RESTFailureClassesHaveDifferentStatusesAndNeverSave(t *testing.T) {
	cases := []struct {
		name                     string
		channelStatus            int
		channelBody              string
		channelErr               error
		userStatus               int
		userErr                  error
		status                   int
		code, targetCode, reason string
	}{
		{name: "channel403_missing_access", channelStatus: 403, channelBody: `{"code":50001,"message":"Missing Access"}`, status: 400, code: "invalid_targets", targetCode: "not_found", reason: "the bot cannot see this channel"},
		{name: "channel404_user404", channelStatus: 404, channelBody: `{"code":10003,"message":"Unknown Channel"}`, userStatus: 404, status: 400, code: "invalid_targets", targetCode: "not_found"},
		{name: "channel_transport_deadline", channelErr: context.DeadlineExceeded, status: 502, code: "lookup_unavailable"},
		{name: "user_transport_deadline", channelStatus: 404, channelBody: `{"code":10003,"message":"Unknown Channel"}`, userErr: context.DeadlineExceeded, status: 502, code: "lookup_unavailable"},
		{name: "channel429", channelStatus: 429, channelBody: `{"retry_after":0,"global":false}`, status: 502, code: "lookup_unavailable"},
		{name: "channel502_one_attempt", channelStatus: 502, channelBody: `{"code":0,"message":"QA bad gateway"}`, status: 502, code: "lookup_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := t32Fixture(t, "discord", []string{"discord:222"})
			_ = t33RealDiscord(t, f, "discord")
			network := t33ExternalHTTP(t)
			network.handle = func(req *http.Request, index int) (*http.Response, error) {
				if index == 0 {
					if tc.channelErr != nil {
						return nil, tc.channelErr
					}
					return t33HTTPResponse(req, tc.channelStatus, tc.channelBody), nil
				}
				if index != 1 || (tc.userStatus == 0 && tc.userErr == nil) {
					return nil, fmt.Errorf("instrument: unexpected HTTP request %s", req.URL.Path)
				}
				if tc.userErr != nil {
					return nil, tc.userErr
				}
				return t33HTTPResponse(req, tc.userStatus, `{"code":10013,"message":"Unknown User"}`), nil
			}
			before := t13Before(t, f)
			rec := t14Put(t, f, "discord", "agent_may_start", []string{"222", "601"}, t14Read(t, f, "discord").Revision)
			body := t14Rejection(t, rec, tc.status, tc.code)
			if tc.targetCode != "" {
				t34TargetError(t, body, tc.targetCode, tc.reason)
			} else {
				require.Equal(t, "Discord could not be reached to check 601; nothing was saved; try again", body.Error, "T-9 exact single-id unavailable text")
			}
			t13Unchanged(t, f, before)
			routes := []string{"GET /api/v" + discordgo.APIVersion + "/channels/601"}
			if tc.userStatus != 0 || tc.userErr != nil {
				routes = append(routes, "GET /api/v"+discordgo.APIVersion+"/users/601")
			}
			paths, _ := network.snapshot()
			require.Equal(t, routes, paths, "T-9: only definite Channel-not-found falls back; no hidden retry")
		})
	}
}

func TestT34_ReplyOnlyForumAdditionIsRefusedAndCannotSurfaceOnLaterPolicyFlip(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Channels["discord"].Outbound.Policy = "reply_only"
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	_ = t33RealDiscord(t, f, "discord")
	network := t33ExternalHTTP(t)
	network.handle = func(req *http.Request, _ int) (*http.Response, error) {
		return t33HTTPResponse(req, http.StatusOK, `{"id":"601","type":15}`), nil
	}
	r1 := t14Read(t, f, "discord").Revision
	before := t13Before(t, f)
	refused := t14Put(t, f, "discord", "reply_only", []string{"222", "601"}, r1)
	body := t14Rejection(t, refused, http.StatusBadRequest, "invalid_targets")
	t34TargetError(t, body, "unsupported_channel_kind", "this kind of Discord channel cannot receive a posted message; use a text channel, announcement channel or thread id")
	t13Unchanged(t, f, before)
	t13AssertOutbound(t, f, "discord", "reply_only", []string{"discord:222"})
	require.Equal(t, r1, t14Read(t, f, "discord").Revision, "refusal did not move the token")

	// S-84's motivating flip changes only the policy of the unchanged valid
	// list. Forum601 must not have entered either live or saved state.
	flipped := t14Put(t, f, "discord", "agent_may_start", []string{"222"}, r1)
	require.Equal(t, http.StatusOK, flipped.Code, flipped.Body.String())
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222"})
	require.NotEqual(t, r1, t14Read(t, f, "discord").Revision)
	paths, _ := network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/601"}, paths, "reply_only addition was validated; unchanged-list policy flip makes no lookup")
}

func TestT34_Channel404ThenVisiblePersonSavesWithoutOpeningADM(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	_ = t33RealDiscord(t, f, "discord")
	network := t33ExternalHTTP(t)
	network.handle = func(req *http.Request, index int) (*http.Response, error) {
		switch index {
		case 0:
			return t33HTTPResponse(req, http.StatusNotFound, `{"code":10003,"message":"Unknown Channel"}`), nil
		case 1:
			return t33HTTPResponse(req, http.StatusOK, `{"id":"601","username":"qa-t34-person"}`), nil
		default:
			return nil, fmt.Errorf("instrument: unexpected save-time HTTP request %s", req.URL.Path)
		}
	}
	r1 := t14Read(t, f, "discord").Revision
	rec := t14Put(t, f, "discord", "agent_may_start", []string{"222", "601"}, r1)
	require.Equal(t, http.StatusOK, rec.Code, "T-9: visible person is a valid target after Unknown Channel: %s", rec.Body.String())
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222", "discord:601"})
	require.NotEqual(t, r1, t14Read(t, f, "discord").Revision)
	paths, _ := network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/601", "GET /api/v" + discordgo.APIVersion + "/users/601"}, paths, "save-time validation never opens a DM or posts a message")
}
