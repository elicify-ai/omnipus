package discord

// T-9 / FR-013 / FR-046. Expected codes, fallbacks and ids are from the
// normative T-9 table and S-12/S-14, never from an observed adapter result.
// Only the external HTTP edge is scripted; the adapter/session/cache/decoder
// are real. T-24b/T-33 separately pin the wrapper/limiter concurrency contract.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/require"
)

// These are test-only observations of an external service, not gateway wire types.
type t09HTTPCall struct {
	Method string
	Path   string
	Body   string
}

type t09HTTPResponse struct {
	Status int
	Body   string
	Err    error
}

type t09Transport struct {
	mu        sync.Mutex
	calls     []t09HTTPCall
	responses []t09HTTPResponse
}

func (n *t09Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}
	n.mu.Lock()
	index := len(n.calls)
	n.calls = append(n.calls, t09HTTPCall{req.Method, req.URL.Path, string(body)})
	// An unexpected call is observable and fails closed. A retry must not get
	// a second scripted success or loop indefinitely on a scripted 429.
	response := t09HTTPResponse{Status: http.StatusInternalServerError, Body: `{"code":0,"message":"unexpected QA request"}`}
	if index < len(n.responses) {
		response = n.responses[index]
	}
	n.mu.Unlock()
	if response.Err != nil {
		return nil, response.Err
	}
	return &http.Response{
		StatusCode: response.Status,
		Status:     fmt.Sprintf("%d %s", response.Status, http.StatusText(response.Status)),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response.Body)),
		Request:    req,
	}, nil
}

func (n *t09Transport) snapshot() []t09HTTPCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]t09HTTPCall(nil), n.calls...)
}

func t09Adapter(t *testing.T, responses ...t09HTTPResponse) (*DiscordChannel, *t09Transport) {
	t.Helper()
	session, err := discordgo.New("Bot qa-not-a-live-token")
	require.NoError(t, err)
	network := &t09Transport{responses: responses}
	session.Client = &http.Client{Transport: network}
	base := channels.NewBaseChannel("discord", nil, nil, nil)
	base.SetRunning(true)
	return &DiscordChannel{BaseChannel: base, session: session, ctx: context.Background()}, network
}

func t09Route(method, suffix string) string {
	return method + " /api/v" + discordgo.APIVersion + suffix
}

func t09RequireRoutes(t *testing.T, network *t09Transport, expected []string) []t09HTTPCall {
	t.Helper()
	calls := network.snapshot()
	actual := make([]string, 0, len(calls))
	for _, call := range calls {
		actual = append(actual, call.Method+" "+call.Path)
	}
	require.Equal(t, expected, actual, "T-9: exact HTTP ordering, no fallback/retry/send outside the specified path")
	return calls
}

func t09Resolver(t *testing.T, ch *DiscordChannel) channels.OutboundTargetResolver {
	t.Helper()
	resolver, ok := any(ch).(channels.OutboundTargetResolver)
	if !ok {
		t.Fatal("BLOCKED: DiscordChannel.ResolveOutboundTarget not implemented — required by T-9/FR-013/FR-046")
	}
	return resolver
}

// Build the real bus envelope, not a parallel test carrier. The frozen WP-2
// handback still lacks the specified Operation field: make that omission loud
// instead of turning the entire package into a compile failure.
func t09StartMessage(t *testing.T, chatID, content string) bus.OutboundMessage {
	t.Helper()
	msg := bus.OutboundMessage{Channel: "discord", ChatID: chatID, Content: content}
	operation := reflect.ValueOf(&msg).Elem().FieldByName("Operation")
	if !operation.IsValid() || operation.Kind() != reflect.String || !operation.CanSet() {
		t.Fatal("BLOCKED: bus.OutboundMessage.Operation not implemented as a string field — required by FR-040/T-9/FR-046")
	}
	operation.SetString("start")
	return msg
}

func TestT09_ExternalHTTPInstrument_RealDiscordgoParsesChannelAndUser(t *testing.T) {
	ch, network := t09Adapter(t,
		t09HTTPResponse{Status: 200, Body: `{"id":"222","type":0}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`},
	)
	channel, err := ch.session.Channel("222")
	require.NoError(t, err, "instrument: real discordgo Channel reaches fake external HTTP")
	require.Equal(t, "222", channel.ID)
	require.Equal(t, discordgo.ChannelTypeGuildText, channel.Type)
	user, err := ch.session.User("333")
	require.NoError(t, err, "instrument: real discordgo User decoder reaches fake external HTTP")
	require.Equal(t, "333", user.ID)
	t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/222"), t09Route("GET", "/users/333")})
}

func TestT09_DiscordLookup_FollowsOnlyTheSpecifiedFallbackAndErrorPaths(t *testing.T) {
	// Codes are literal T-9 oracles. Keeping default session retry settings
	// exposes a missing WithRetryOnRatelimit(false) / WithRestRetries(0).
	unknownChannel := t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`}
	user := t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`}
	channel := t09HTTPResponse{Status: 200, Body: `{"id":"333","type":0}`}
	getChannel := t09Route("GET", "/channels/333")
	getUser := t09Route("GET", "/users/333")
	cases := []struct {
		name      string
		cached    *discordgo.Channel
		responses []t09HTTPResponse
		code      channels.TargetErrorCode
		routes    []string
	}{
		{"cache_text", &discordgo.Channel{ID: "333", Type: 0}, nil, "", []string{}},
		{"cache_forum", &discordgo.Channel{ID: "333", Type: 15}, nil, "unsupported_channel_kind", []string{}},
		{"cache_dm", &discordgo.Channel{ID: "333", Type: 1}, nil, "unsupported_channel_kind", []string{}},
		{"rest_text", nil, []t09HTTPResponse{channel}, "", []string{getChannel}},
		{"rest_forum_no_user_fallback", nil, []t09HTTPResponse{{Status: 200, Body: `{"id":"333","type":15}`}}, "unsupported_channel_kind", []string{getChannel}},
		{"channel_404_then_person", nil, []t09HTTPResponse{unknownChannel, user}, "", []string{getChannel, getUser}},
		{"channel_10003_even_without_404", nil, []t09HTTPResponse{{Status: 400, Body: `{"code":10003,"message":"Unknown Channel"}`}, user}, "", []string{getChannel, getUser}},
		{"channel_403_no_user_fallback", nil, []t09HTTPResponse{{Status: 403, Body: `{"code":50001,"message":"Missing Access"}`}}, "not_found", []string{getChannel}},
		{"channel_50001_without_403", nil, []t09HTTPResponse{{Status: 400, Body: `{"code":50001,"message":"Missing Access"}`}}, "not_found", []string{getChannel}},
		{"user_404", nil, []t09HTTPResponse{unknownChannel, {Status: 404, Body: `{"code":10013,"message":"Unknown User"}`}}, "not_found", []string{getChannel, getUser}},
		{"user_10013_without_404", nil, []t09HTTPResponse{unknownChannel, {Status: 400, Body: `{"code":10013,"message":"Unknown User"}`}}, "not_found", []string{getChannel, getUser}},
		{"channel_network_error", nil, []t09HTTPResponse{{Err: errors.New("QA external network failure")}}, "lookup_unavailable", []string{getChannel}},
		{"channel_429_one_attempt", nil, []t09HTTPResponse{{Status: 429, Body: `{"retry_after":0,"global":false}`}}, "lookup_unavailable", []string{getChannel}},
		{"channel_500", nil, []t09HTTPResponse{{Status: 500, Body: `{"code":0,"message":"outage"}`}}, "lookup_unavailable", []string{getChannel}},
		{"channel_502_one_attempt", nil, []t09HTTPResponse{{Status: 502, Body: `{"code":0,"message":"bad gateway"}`}}, "lookup_unavailable", []string{getChannel}},
		{"user_network_error", nil, []t09HTTPResponse{unknownChannel, {Err: errors.New("QA external network failure")}}, "lookup_unavailable", []string{getChannel, getUser}},
		{"user_429_one_attempt", nil, []t09HTTPResponse{unknownChannel, {Status: 429, Body: `{"retry_after":0,"global":false}`}}, "lookup_unavailable", []string{getChannel, getUser}},
		{"user_502_one_attempt", nil, []t09HTTPResponse{unknownChannel, {Status: 502, Body: `{"code":0,"message":"bad gateway"}`}}, "lookup_unavailable", []string{getChannel, getUser}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch, network := t09Adapter(t, tc.responses...)
			if tc.cached != nil {
				require.NoError(t, ch.session.State.GuildAdd(&discordgo.Guild{ID: "qa-guild"}))
				tc.cached.GuildID = "qa-guild"
				require.NoError(t, ch.session.State.ChannelAdd(tc.cached), "instrument: seed real cache")
			}
			resolver := t09Resolver(t, ch)
			result := resolver.ResolveOutboundTarget(context.Background(), "discord:333")
			require.Equal(t, tc.code, result.Code, "T-9: exact classification for %s", tc.name)
			t09RequireRoutes(t, network, tc.routes)
		})
	}
}

func TestT09_DiscordStart_ResolvesPersonToDMAndNeverSendsAfterOpenFailure(t *testing.T) {
	unknownChannel := t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`}
	person := t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`}
	dm := t09HTTPResponse{Status: 200, Body: `{"id":"999","type":1}`}
	posted := t09HTTPResponse{Status: 200, Body: `{"id":"777","channel_id":"999","content":"QA T-9"}`}
	lookup := []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333")}
	open := t09Route("POST", "/users/@me/channels")
	cases := []struct {
		name      string
		responses []t09HTTPResponse
		routes    []string
		wantError bool
	}{
		{"person_opens_dm_999", []t09HTTPResponse{unknownChannel, person, dm, posted}, append(append([]string{}, lookup...), open, t09Route("POST", "/channels/999/messages")), false},
		{"open_403_no_send", []t09HTTPResponse{unknownChannel, person, {Status: 403, Body: `{"code":50001,"message":"Missing Access"}`}}, append(append([]string{}, lookup...), open), true},
		{"open_429_one_attempt_no_send", []t09HTTPResponse{unknownChannel, person, {Status: 429, Body: `{"retry_after":0,"global":false}`}}, append(append([]string{}, lookup...), open), true},
		{"open_502_one_attempt_no_send", []t09HTTPResponse{unknownChannel, person, {Status: 502, Body: `{"code":0,"message":"bad gateway"}`}}, append(append([]string{}, lookup...), open), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch, network := t09Adapter(t, tc.responses...)
			err := ch.Send(context.Background(), t09StartMessage(t, "333", "QA T-9"))
			calls := t09RequireRoutes(t, network, tc.routes)
			if tc.wantError {
				require.Error(t, err, "FR-046: failed DM opening is an adapter error, not successful delivery")
			} else {
				require.NoError(t, err, "S-12: accepted person start must be posted to opened DM")
			}
			var recipient map[string]string
			require.NoError(t, json.Unmarshal([]byte(calls[2].Body), &recipient))
			require.Equal(t, map[string]string{"recipient_id": "333"}, recipient, "S-12: exact DM recipient")
			if !tc.wantError {
				var message map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(calls[3].Body), &message))
				require.JSONEq(t, `"QA T-9"`, string(message["content"]), "S-12: actual serialized message content")
			}
		})
	}
}

func TestT09_DiscordOrdinaryReply_ControlDoesNotLookupOrOpenADM(t *testing.T) {
	ch, network := t09Adapter(t, t09HTTPResponse{Status: 200, Body: `{"id":"777","channel_id":"222","content":"QA reply"}`})
	// FR-040: an unset operation preserves the ordinary-reply path.
	err := ch.Send(context.Background(), bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA reply"})
	require.NoError(t, err, "positive control: existing conversation replies remain direct")
	calls := t09RequireRoutes(t, network, []string{t09Route("POST", "/channels/222/messages")})
	var message map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(calls[0].Body), &message))
	require.JSONEq(t, `"QA reply"`, string(message["content"]), "positive control: real reply content survives serialization")
}
