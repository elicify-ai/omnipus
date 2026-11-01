package gateway

// T-13: spec S-13–S-16/S-20–S-22/S-84, T-2 and D1. Expectations are
// independently derived canonical values, exact codes and unchanged-state
// relationships. Real REST/auth/config writer/manager, fake platform only.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/require"
)

func TestT13_Writer_TelegramCanonicalValidationAndDistinctCountBoundary(t *testing.T) {
	cases := []struct {
		name string
		raw  []string
		want []string
		code string
		item string
	}{
		{name: "empty", raw: []string{}, want: []string{}},
		{name: "one", raw: []string{"123456"}, want: []string{"telegram:123456"}},
		{name: "person_group_topic_offline", raw: []string{"123456", "-1001234567890", "-1001234567890/45"}, want: []string{"telegram:123456", "telegram:-1001234567890", "telegram:-1001234567890/45"}},
		{name: "dedup_before_count", raw: []string{"222", "telegram:222", " Telegram:222 "}, want: []string{"telegram:222"}},
		{name: "signed_min", raw: []string{"-9223372036854775808"}, want: []string{"telegram:-9223372036854775808"}},
		{name: "signed_max", raw: []string{"9223372036854775807"}, want: []string{"telegram:9223372036854775807"}},
		{name: "topic_max", raw: []string{"-100/2147483647"}, want: []string{"telegram:-100/2147483647"}},
		{name: "wildcard", raw: []string{"*"}, code: "invalid_targets", item: "wildcard_not_allowed"},
		{name: "username", raw: []string{"@alice"}, code: "invalid_targets", item: "username_not_allowed"},
		{name: "other_platform", raw: []string{"discord:5"}, code: "invalid_targets", item: "wrong_platform"},
		{name: "zero", raw: []string{"0"}, code: "invalid_targets", item: "invalid_id"},
		{name: "signed_overflow", raw: []string{"9223372036854775808"}, code: "invalid_targets", item: "invalid_id"},
		{name: "topic_zero", raw: []string{"-100/0"}, code: "invalid_targets", item: "invalid_id"},
		{name: "topic_overflow", raw: []string{"-100/2147483648"}, code: "invalid_targets", item: "invalid_id"},
		{name: "positive_chat_topic", raw: []string{"123456/7"}, code: "invalid_targets", item: "invalid_id"},
	}
	// T-2 maximum 20 distinct targets: exercise the immediate neighbors too.
	for _, count := range []int{19, 20, 21} {
		row := struct {
			name string
			raw  []string
			want []string
			code string
			item string
		}{name: "distinct_" + strconv.Itoa(count), raw: []string{}, want: []string{}}
		for i := 1; i <= count; i++ {
			id := strconv.Itoa(i)
			row.raw = append(row.raw, id)
			row.want = append(row.want, "telegram:"+id)
		}
		if count == 21 {
			row.code = "too_many_targets"
		}
		cases = append(cases, row)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, adapter := t13Fixture(t, "telegram", "disabled", "reply_only", []string{})
			before := t13Before(t, f)
			r1 := t14Read(t, f, "telegram").Revision
			rec := t14Put(t, f, "telegram", "agent_may_start", tc.raw, r1)
			if tc.code != "" {
				rejected := t14Rejection(t, rec, http.StatusBadRequest, tc.code)
				if tc.item != "" {
					require.NotNil(t, rejected.TargetErrors)
					require.Len(t, *rejected.TargetErrors, 1)
					require.Equal(t, 0, (*rejected.TargetErrors)[0].Index)
					require.Equal(t, tc.item, string((*rejected.TargetErrors)[0].Code))
				}
				t13Unchanged(t, f, before)
			} else {
				require.Equal(t, http.StatusOK, rec.Code, "S-15/target boundary: %s", rec.Body.String())
				var written gen.ChannelOutboundWriteResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &written))
				require.Equal(t, "agent_may_start", string(written.Outbound.Policy))
				require.Equal(t, tc.want, written.Outbound.Targets)
				require.NotEqual(t, r1, written.Revision, "permission change moves the conflict token")
				require.Equal(t, written.Revision, t14Read(t, f, "telegram").Revision)
				t13AssertOutbound(t, f, "telegram", "agent_may_start", tc.want)
			}
			require.Equal(t, []string{}, adapter.lookupSnapshot(), "S-15: Telegram syntax/kind validation is local even offline")
		})
	}
}

func TestT13_Writer_OfflineRevocationKeepsOrRemovesExactlyTheRequestedTargets(t *testing.T) {
	cases := []struct {
		state   string
		policy  string
		targets []string
		want    []string
	}{
		{"disabled", "reply_only", []string{"222", "333"}, []string{"discord:222", "discord:333"}},
		{"not_loaded", "agent_may_start", []string{"222"}, []string{"discord:222"}},
		{"not_running", "agent_may_start", []string{}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			f, adapter := t13Fixture(t, "discord", tc.state, "agent_may_start", []string{"discord:222", "discord:333"})
			if tc.state == "not_running" {
				require.Equal(t, "not_running", string(t14Read(t, f, "discord").Availability.State))
			} else {
				require.Equal(t, "not_loaded", string(t14Read(t, f, "discord").Availability.State))
			}
			rec := t14Put(t, f, "discord", tc.policy, tc.targets, t14Read(t, f, "discord").Revision)
			require.Equal(t, http.StatusOK, rec.Code, "S-20: %s revocation is available offline: %s", tc.state, rec.Body.String())
			t13AssertOutbound(t, f, "discord", tc.policy, tc.want)
			require.Equal(t, []string{}, adapter.lookupSnapshot(), "FR-016: no added target, no network validation")
		})
	}
}

func TestT13_Writer_DiscordAdditionNeedsRunningConnectionEvenUnderReplyOnly(t *testing.T) {
	for _, state := range []string{"disabled", "not_loaded", "not_running"} {
		for _, policy := range []string{"reply_only", "agent_may_start"} {
			t.Run(state+"/"+policy, func(t *testing.T) {
				f, adapter := t13Fixture(t, "discord", state, "reply_only", []string{"discord:222"})
				before := t13Before(t, f)
				rec := t14Put(t, f, "discord", policy, []string{"222", "555"}, t14Read(t, f, "discord").Revision)
				body := t14Rejection(t, rec, http.StatusConflict, "connect_first")
				require.Equal(t, "connect the channel first to add Discord ids", body.Error, "S-21/S-84 exact actionable refusal")
				t13Unchanged(t, f, before)
				require.Equal(t, []string{}, adapter.lookupSnapshot(), "offline refusal precedes external lookup")
			})
		}
	}
}

func TestT13_Writer_ForumAdditionRefusedForBothPoliciesAndCannotPolluteLaterFlip(t *testing.T) {
	for _, policy := range []string{"reply_only", "agent_may_start"} {
		t.Run(policy, func(t *testing.T) {
			f, adapter := t13Fixture(t, "discord", "running", "reply_only", []string{"discord:222"})
			adapter.lookup = func(context.Context, string) channels.OutboundTargetResult {
				return channels.OutboundTargetResult{Code: "unsupported_channel_kind"}
			}
			before := t13Before(t, f)
			rec := t14Put(t, f, "discord", policy, []string{"222", "601"}, t14Read(t, f, "discord").Revision)
			body := t14Rejection(t, rec, http.StatusBadRequest, "invalid_targets")
			require.NotNil(t, body.TargetErrors)
			require.Len(t, *body.TargetErrors, 1)
			require.Equal(t, 1, (*body.TargetErrors)[0].Index)
			require.Equal(t, "unsupported_channel_kind", string((*body.TargetErrors)[0].Code))
			require.Equal(t, []string{"discord:601"}, adapter.lookupSnapshot(), "only the added target is checked, not retained 222")
			t13Unchanged(t, f, before)
			flip := t14Put(t, f, "discord", "agent_may_start", []string{"222"}, t14Read(t, f, "discord").Revision)
			require.Equal(t, http.StatusOK, flip.Code, "S-84: later flip of the unchanged valid list succeeds: %s", flip.Body.String())
			t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222"})
			require.Equal(t, []string{"discord:601"}, adapter.lookupSnapshot(), "the later no-addition flip makes no new lookup")
		})
	}
}

func TestT13_Writer_ConfigureInvalidatesTheConflictTokenThenFreshReadCanCommit(t *testing.T) {
	f := t13ConfiguredTelegramFixture(t)
	r1 := t14Read(t, f, "telegram").Revision
	configured := t14Request(t, f, http.MethodPut, "/api/v1/channels/telegram/configure", `{"allow_from":["qa-new-sender"]}`, true)
	require.Equal(t, http.StatusOK, configured.Code, "S-22: real Configure establishes concurrent connection change: %s", configured.Body.String())
	r2 := t14Read(t, f, "telegram").Revision
	require.NotEqual(t, r1, r2, "Configure allow_from must invalidate the whole-instance conflict token")
	before := t13Before(t, f)
	rejected := t14Put(t, f, "telegram", "agent_may_start", []string{"123456"}, r1)
	t14Rejection(t, rejected, http.StatusConflict, "stale_revision")
	t13Unchanged(t, f, before)
	fresh := t14Read(t, f, "telegram")
	require.Equal(t, r2, fresh.Revision)
	require.Equal(t, "reply_only", string(fresh.Outbound.Policy))
	require.Equal(t, []string{}, fresh.Outbound.Targets)
	accepted := t14Put(t, f, "telegram", "agent_may_start", []string{"123456"}, fresh.Revision)
	require.Equal(t, http.StatusOK, accepted.Code, "S-22: re-read-and-retry succeeds: %s", accepted.Body.String())
	t13AssertOutbound(t, f, "telegram", "agent_may_start", []string{"telegram:123456"})
}

func TestT13_Writer_UnsupportedPlatformRefusalsStillAllowAdditionFreeReplyOnly(t *testing.T) {
	for _, tc := range []struct{ platform, code, exactText string }{
		{"slack", "platform_cannot_start", ""},
		{"dingtalk", "platform_cannot_message_people", "this platform can only answer, not start a direct message"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			f, _ := t13Fixture(t, tc.platform, "disabled", "reply_only", []string{})
			before := t13Before(t, f)
			rec := t14Put(t, f, tc.platform, "agent_may_start", []string{"555"}, t14Read(t, f, tc.platform).Revision)
			body := t14Rejection(t, rec, http.StatusUnprocessableEntity, tc.code)
			if tc.exactText != "" {
				require.Equal(t, tc.exactText, body.Error, "S-16 exact person-start explanation")
			}
			t13Unchanged(t, f, before)
			control := t14Put(t, f, tc.platform, "reply_only", []string{}, t14Read(t, f, tc.platform).Revision)
			require.Equal(t, http.StatusOK, control.Code, "S-16: addition-free reply_only is allowed on unsupported platforms: %s", control.Body.String())
			t13AssertOutbound(t, f, tc.platform, "reply_only", []string{})
		})
	}
}
