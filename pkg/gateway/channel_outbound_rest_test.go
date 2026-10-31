package gateway

// T-14: the registered outbound REST routes, through real authentication and
// RequireNotBypass. Oracles: channels-spec-r8 FR-012, FR-016, FR-017,
// FR-021/022; contract table; D1 rows 15b, 25–29; S-20/21/22/37/84.
//
// No Discord network look-up is needed here: additions are tested on Telegram
// (locally validated) or on an offline Discord instance (connect_first).
// Successful writes revoke a disabled Discord instance, as S-20 permits.
// Fixture data is seeded before the action under test with SaveConfig and the
// real loop's copy-on-write publisher, never through a protected generic write.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

const t14Bearer = "synthetic-channels-red-bearer"

func t14Fixture(t *testing.T) (*wu6Fixture, *systools.Deps) {
	t.Helper()
	f, _, d := orderingWriterFixture(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", t14Bearer)
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Gateway.DevModeBypass = false
		for _, platform := range []string{"discord", "telegram"} {
			cfg.Channels[platform] = config.ChannelInstanceConfig{
				Type: platform, Enabled: false,
				Outbound: &config.ChannelOutboundConfig{
					Policy: "agent_may_start", Targets: []string{platform + ":222"},
				},
			}
		}
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	// Positive instrument: this request traverses the same registered channel
	// dispatcher and real bearer middleware as the new subroutes.
	rec := t14Request(t, f, http.MethodGet, "/api/v1/channels", "", true)
	require.Equal(t, http.StatusOK, rec.Code, "fixture must reach the authenticated channel dispatcher: %s", rec.Body.String())
	return f, d
}

func t14Request(t *testing.T, f *wu6Fixture, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = uniqueTestSourceIP()
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+t14Bearer)
	}
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, f.al.GetConfig())
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func t14Read(t *testing.T, f *wu6Fixture, id string) gen.ChannelOutboundRead {
	t.Helper()
	rec := t14Request(t, f, http.MethodGet, "/api/v1/channels/"+id+"/outbound", "", true)
	require.Equal(t, http.StatusOK, rec.Code, "FR-021: registered GET must return the live read: %s", rec.Body.String())
	var body gen.ChannelOutboundRead
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEmpty(t, body.Revision, "contract: the revision is required")
	return body
}

func t14Put(t *testing.T, f *wu6Fixture, id, policy string, targets []string, revision string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(gen.ChannelOutboundUpdateRequest{
		Policy: gen.ChannelOutboundUpdateRequestPolicy(policy), Targets: targets, ExpectedRevision: revision,
	})
	require.NoError(t, err)
	return t14Request(t, f, http.MethodPut, "/api/v1/channels/"+id+"/outbound", string(body), true)
}

func t14ToolRevision(t *testing.T, d *systools.Deps, id string) string {
	t.Helper()
	res := systools.NewChannelListTool(d).Execute(context.Background(), nil)
	require.False(t, res.IsError, "fixture list_channels: %s", res.ForLLM)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &body))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body["channels"], &rows))
	for _, row := range rows {
		var rowID, revision string
		require.NoError(t, json.Unmarshal(row["id"], &rowID))
		if rowID == id {
			require.NoError(t, json.Unmarshal(row["revision"], &revision))
			require.NotEmpty(t, revision, "fixture: list_channels revision")
			return revision
		}
	}
	t.Fatalf("fixture list_channels lacks %q", id)
	return ""
}

func t14Rejection(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) gen.ChannelOutboundRejection {
	t.Helper()
	require.Equal(t, status, rec.Code, "contract rejection %s: %s", code, rec.Body.String())
	var body gen.ChannelOutboundRejection
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, code, string(body.Code))
	require.NotEmpty(t, body.Error, "a refusal explains why nothing was saved")
	return body
}

func TestT14_GetOutbound_ShapeDefaultsAndLiveRevision(t *testing.T) {
	f, d := t14Fixture(t)
	body := t14Read(t, f, "line.healthy")
	require.Equal(t, "reply_only", string(body.Outbound.Policy), "FR-010: absent setting defaults to reply_only")
	require.Equal(t, []string{}, body.Outbound.Targets, "contract: the empty list is an array, not null")
	require.Equal(t, "running", string(body.Availability.State), "fixture's healthy LINE adapter really started")
	require.Nil(t, body.Availability.Reason, "running has no reason")
	require.False(t, body.StartSupported, "T-1: LINE does not support starts")
	require.Equal(t, t14ToolRevision(t, d, "line.healthy"), body.Revision, "S-37: REST and list_channels use the same live token")
}

func TestT14_GetOutbound_UnknownType404AfterKnownRouteWorks(t *testing.T) {
	f, _ := t14Fixture(t)
	_ = t14Read(t, f, "discord") // prevents a missing route from passing this row
	rec := t14Request(t, f, http.MethodGet, "/api/v1/channels/unknownplatform/outbound", "", true)
	require.Equal(t, http.StatusNotFound, rec.Code, "contract: unknown platform type")
}

func TestT14_PutOutbound_OfflineRevocationPersistsAndMovesToken(t *testing.T) {
	f, d := t14Fixture(t)
	before := t14ToolRevision(t, d, "discord")
	rec := t14Put(t, f, "discord", "reply_only", []string{"222"}, before)
	require.Equal(t, http.StatusOK, rec.Code, "S-20: revocation with no additions works while disabled: %s", rec.Body.String())
	var written gen.ChannelOutboundWriteResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &written))
	require.Equal(t, "reply_only", string(written.Outbound.Policy))
	require.Equal(t, []string{"discord:222"}, written.Outbound.Targets, "canonical stored values")
	require.NotEqual(t, before, written.Revision, "FR-088: the conflict token includes outbound")
	require.Nil(t, written.Warning, "warning only when the live refresh failed")
	read := t14Read(t, f, "discord")
	require.Equal(t, written.Revision, read.Revision)
	require.Equal(t, "reply_only", string(read.Outbound.Policy))
	channels := readConfigChannelsFile(t, f)
	inst, ok := channels["discord"].(map[string]any)
	require.True(t, ok)
	stored, ok := inst["outbound"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"policy": "reply_only", "targets": []any{"discord:222"}}, stored)
}

func TestT14_PutOutbound_InvalidTargets400WithRequestIndicesAndNoWrite(t *testing.T) {
	f, d := t14Fixture(t)
	before, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	rec := t14Put(t, f, "telegram", "agent_may_start", []string{"111", "*", "@alice"}, t14ToolRevision(t, d, "telegram"))
	body := t14Rejection(t, rec, http.StatusBadRequest, "invalid_targets")
	require.NotNil(t, body.TargetErrors)
	entries := *body.TargetErrors
	require.Len(t, entries, 2, "both invalid positions, request order")
	require.Equal(t, 1, entries[0].Index)
	require.Equal(t, "*", entries[0].Target)
	require.Equal(t, "wildcard_not_allowed", string(entries[0].Code))
	require.Equal(t, 2, entries[1].Index)
	require.Equal(t, "@alice", entries[1].Target)
	require.Equal(t, "username_not_allowed", string(entries[1].Code))
	after, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "refused validation writes no bytes")
}

func TestT14_PutOutbound_DistinctTargetLimit400(t *testing.T) {
	f, d := t14Fixture(t)
	targets := make([]string, 21)
	for i := range targets {
		targets[i] = strconv.Itoa(i + 1)
	}
	rec := t14Put(t, f, "telegram", "agent_may_start", targets, t14ToolRevision(t, d, "telegram"))
	t14Rejection(t, rec, http.StatusBadRequest, "too_many_targets")
}

func TestT14_PutOutbound_RawEntryOver256Schema400(t *testing.T) {
	f, d := t14Fixture(t)
	rec := t14Put(t, f, "telegram", "agent_may_start", []string{strings.Repeat("1", 300)}, t14ToolRevision(t, d, "telegram"))
	require.Equal(t, http.StatusBadRequest, rec.Code, "D1 row 15b: schema rejects an overlong raw entry: %s", rec.Body.String())
}

func TestT14_PutOutbound_StaleRevision409(t *testing.T) {
	f, _ := t14Fixture(t)
	rec := t14Put(t, f, "telegram", "reply_only", []string{}, "not-the-current-token")
	t14Rejection(t, rec, http.StatusConflict, "stale_revision")
}

func TestT14_PutOutbound_AddedDiscordTargetOffline409EvenReplyOnly(t *testing.T) {
	for _, policy := range []string{"agent_may_start", "reply_only"} {
		t.Run(policy, func(t *testing.T) {
			f, d := t14Fixture(t)
			rec := t14Put(t, f, "discord", policy, []string{"222", "555"}, t14ToolRevision(t, d, "discord"))
			body := t14Rejection(t, rec, http.StatusConflict, "connect_first")
			require.Equal(t, "connect the channel first to add Discord ids", body.Error, "S-21 / S-84")
		})
	}
}

func TestT14_PutOutbound_UnsupportedPlatformExactly422(t *testing.T) {
	f, d := t14Fixture(t)
	rec := t14Put(t, f, "line.healthy", "agent_may_start", []string{}, t14ToolRevision(t, d, "line.healthy"))
	t14Rejection(t, rec, http.StatusUnprocessableEntity, "platform_cannot_start")
}

func TestT14_PutOutbound_RealMiddleware401And503(t *testing.T) {
	t.Run("missing bearer is unauthorized", func(t *testing.T) {
		f, _ := t14Fixture(t)
		rec := t14Request(t, f, http.MethodPut, "/api/v1/channels/discord/outbound", `{}`, false)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "real authentication must precede the outbound writer")
	})
	t.Run("development bypass is refused", func(t *testing.T) {
		f := newWU6Fixture(t)
		rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/outbound", `{}`)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, "FR-022: real RequireNotBypass: %s", rec.Body.String())
		var body gen.ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, "this action is disabled while dev_mode_bypass is active", body.Error)
	})
}
