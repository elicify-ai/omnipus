package channels

// outbound_canonicalizer_test.go — T-10, the canonicalizer over dataset D1,
// plus the T-1 platform table (PlatformStartRuleFor) and TelegramTargetKind
// (channels-spec-r8: T-10 with D1; T-10b's round trip lives in the telegram
// package; FR-012; S-14/S-15).
//
// Oracle: dataset D1, row by row — expected values come from the spec's
// table, never from running the code. Rows 15b (300-char entry → REST schema
// 400) and 26 (21 distinct → writer-level too_many_targets) are enforced by
// the layers above the canonicalizer (T-14/T-13), not here; row 28's "schema
// accepts, server applies 20 rule" likewise. Row 15c's ≤-64-character
// canonical bound IS a canonicalizer invariant and is asserted.
//
// Bulk rules (D1 rows 25–28): CanonicalizeTargets collapses duplicates (in
// any input form) to their first occurrence, keeps request order, and does
// NOT apply the 20-target limit (its caller does).
//
// RED expectation: the WP-0 shells return zero values, so every row fails on
// the empty canonical/zero rule.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// d1Row is one row of dataset D1.
type d1Row struct {
	n         int
	platform  string
	raw       string
	canonical string // "" when the row expects a refusal code
	code      TargetErrorCode
}

func TestT10_CanonicalizeTarget_DatasetD1(t *testing.T) {
	rows := []d1Row{
		// Discord (S-14)
		{1, "discord", "111111111111111111", "discord:111111111111111111", ""},
		{2, "discord", "discord:1", "discord:1", ""},
		{3, "discord", "DISCORD:1", "discord:1", ""},
		{4, "discord", " 1 ", "discord:1", ""},
		{5, "discord", "", "", TargetErrInvalidID},
		{6, "discord", "   ", "", TargetErrInvalidID},
		{7, "discord", "*", "", TargetErrWildcardNotAllowed},
		{8, "discord", "discord:*", "", TargetErrWildcardNotAllowed},
		{9, "discord", "@alice", "", TargetErrUsernameNotAllowed},
		{10, "discord", "1|alice", "", TargetErrInvalidID},
		{11, "discord", "telegram:1", "", TargetErrWrongPlatform},
		{12, "discord", "12a4", "", TargetErrInvalidID},
		{13, "discord", "１２３", "", TargetErrInvalidID}, // full-width digits
		{14, "discord", "18446744073709551615", "discord:18446744073709551615", ""},
		{15, "discord", "18446744073709551616", "", TargetErrInvalidID}, // max + 1
		{15, "discord", "0", "", TargetErrInvalidID},
		{15, "discord", "012", "", TargetErrInvalidID},
		{15, "discord", "+12", "", TargetErrInvalidID},
		{15, "discord", "-12", "", TargetErrInvalidID},
		// Telegram (S-15)
		{16, "telegram", "123456", "telegram:123456", ""},
		{17, "telegram", "-1001234567890", "telegram:-1001234567890", ""},
		{18, "telegram", "-1001234567890/45", "telegram:-1001234567890/45", ""},
		{19, "telegram", "123456/7", "", TargetErrInvalidID},
		{20, "telegram", "0", "", TargetErrInvalidID},
		{21, "telegram", "-0", "", TargetErrInvalidID},
		{22, "telegram", "-100/abc", "", TargetErrInvalidID},
		{22, "telegram", "9223372036854775807", "telegram:9223372036854775807", ""},
		{22, "telegram", "-9223372036854775808", "telegram:-9223372036854775808", ""},
		{22, "telegram", "9223372036854775808", "", TargetErrInvalidID},
		{22, "telegram", "-9223372036854775809", "", TargetErrInvalidID},
		{22, "telegram", "-100/0", "", TargetErrInvalidID}, // topic must be ≥ 1
		{22, "telegram", "-100/-3", "", TargetErrInvalidID},
		{22, "telegram", "-100/1", "telegram:-100/1", ""},
		{22, "telegram", "-100/2147483647", "telegram:-100/2147483647", ""},
		{22, "telegram", "-100/2147483648", "", TargetErrInvalidID},
		{22, "telegram", "+5", "", TargetErrInvalidID},
		{22, "telegram", "007", "", TargetErrInvalidID},
		{22, "telegram", "-9223372036854775808/2147483647", "telegram:-9223372036854775808/2147483647", ""},
		// Slack / DingTalk (S-16): the platform-support refusals are the
		// writer's (T-1/T-13); the canonicalizer still normalizes the id.
		{23, "slack", "C123", "slack:C123", ""},
		{24, "dingtalk", "abc", "dingtalk:abc", ""},
	}
	for _, r := range rows {
		got, code := CanonicalizeTarget(r.platform, r.raw)
		if r.code == "" {
			require.Equal(t, r.canonical, got,
				"D1 row %d (%s %q): canonical value", r.n, r.platform, r.raw)
			require.Empty(t, code, "D1 row %d (%s %q): no refusal code", r.n, r.platform, r.raw)
			// D1 row 15c: every accepted canonical value ≤ 64 characters
			// with prefix.
			require.LessOrEqual(t, len(got), 64,
				"D1 row 15c: canonical value must stay ≤ 64 characters: %q", got)
		} else {
			require.Empty(t, got,
				"D1 row %d (%s %q): a refused entry has no canonical value", r.n, r.platform, r.raw)
			require.Equal(t, r.code, code,
				"D1 row %d (%s %q): refusal code", r.n, r.platform, r.raw)
		}
	}
}

func TestT10_CanonicalizeTargets_CollapsesDuplicatesKeepsOrder(t *testing.T) {
	// D1 rows 25–27: 20 distinct accepted; duplicates (in any input form)
	// collapse to their first occurrence with request order kept; the 20
	// limit is the writer's, not the canonicalizer's.
	canonical, errs := CanonicalizeTargets("discord", []string{
		"1", "discord:2", "3", "1", "DISCORD:5", "6", "7", "8", "9", "10",
		"11", "12", "13", "14", "15", "16", "17", "18", "19", "20",
		"2", " 21 ",
	})
	require.Empty(t, errs, "D1 rows 25–28: every entry is individually valid")
	require.Equal(t, []string{
		"discord:1", "discord:2", "discord:3", "discord:5", "discord:6",
		"discord:7", "discord:8", "discord:9", "discord:10", "discord:11",
		"discord:12", "discord:13", "discord:14", "discord:15", "discord:16",
		"discord:17", "discord:18", "discord:19", "discord:20", "discord:21",
	}, canonical,
		"D1 rows 25–28: duplicates collapsed to first occurrence, request order kept, 19+21 distinct stored")

	// D1 row 27: 21 entries with 2 duplicates → 19 distinct, accepted shape
	// at the canonicalizer (the writer applies its own limit over distinct
	// values).
	canonical, errs = CanonicalizeTargets("discord", []string{
		"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13",
		"14", "15", "16", "17", "18", "19", "1", "2",
	})
	require.Empty(t, errs)
	require.Len(t, canonical, 19, "D1 row 27: 21 entries, 2 duplicates → 19 distinct")
}

func TestT10_CanonicalizeTargets_ListsEveryRefusedEntryInRequestOrder(t *testing.T) {
	// The errs list carries every refused entry with its raw index, in
	// request order (FR-012; the contract's target_errors[].index).
	canonical, errs := CanonicalizeTargets("discord", []string{
		"111", "@alice", "222", "*", "333",
	})
	require.Equal(t, []string{"discord:111", "discord:222", "discord:333"}, canonical)
	require.Len(t, errs, 2, "FR-012: both refused entries are listed")
	require.Equal(t, 1, errs[0].Index, "target_errors[].index is the raw 0-based position")
	require.Equal(t, "@alice", errs[0].Raw)
	require.Equal(t, TargetErrUsernameNotAllowed, errs[0].Code)
	require.Equal(t, 3, errs[1].Index)
	require.Equal(t, "*", errs[1].Raw)
	require.Equal(t, TargetErrWildcardNotAllowed, errs[1].Code)
}

func TestT10_TelegramTargetKind_SignDecidesLocally(t *testing.T) {
	// T-1: Telegram's kind is decided locally from the id's sign.
	kind, ok := TelegramTargetKind("telegram:123456")
	require.True(t, ok)
	require.Equal(t, TargetKindPerson, kind, "positive id is a person")

	kind, ok = TelegramTargetKind("telegram:-1001234567890")
	require.True(t, ok)
	require.Equal(t, TargetKindGroup, kind, "negative id is a group")

	kind, ok = TelegramTargetKind("telegram:-1001234567890/45")
	require.True(t, ok)
	require.Equal(t, TargetKindGroup, kind, "a forum topic is a group target")

	_, ok = TelegramTargetKind("discord:1")
	require.False(t, ok, "a non-Telegram canonical value has no Telegram kind")
	_, ok = TelegramTargetKind("telegram:abc")
	require.False(t, ok, "a non-canonical Telegram value has no kind")
}

func TestT10_PlatformStartRuleFor_T1Table(t *testing.T) {
	// T-1: Telegram and Discord support starting; the messaging-limited
	// platforms refuse people targets with platform_cannot_message_people
	// (DingTalk, Weixin, Google Chat) and everything else with
	// platform_cannot_start (WhatsApp, Slack, Matrix, Feishu, LINE, WeCom,
	// IRC, QQ).
	supported := map[string]bool{
		"telegram": true, "discord": true,
		"dingtalk": false, "weixin": false, "googlechat": false, "google-chat": false,
		"whatsapp": false, "whatsapp_native": false, "slack": false, "matrix": false,
		"feishu": false, "line": false, "wecom": false, "irc": false, "qq": false,
	}
	for platform, want := range supported {
		rule := PlatformStartRuleFor(platform)
		require.Equal(t, want, rule.StartSupported,
			"T-1: %s StartSupported", platform)
	}

	for _, p := range []string{"dingtalk", "weixin", "googlechat"} {
		rule := PlatformStartRuleFor(p)
		require.Equal(t, "platform_cannot_message_people", rule.PersonRefusal,
			"T-1: %s person refusal", p)
		require.Equal(t, "platform_cannot_start", rule.GroupRefusal,
			"T-1: %s group refusal", p)
	}
	for _, p := range []string{"whatsapp", "whatsapp_native", "slack", "matrix", "feishu", "line", "wecom", "irc", "qq"} {
		rule := PlatformStartRuleFor(p)
		require.Equal(t, "platform_cannot_start", rule.PersonRefusal,
			"T-1: %s person refusal", p)
		require.Equal(t, "platform_cannot_start", rule.GroupRefusal,
			"T-1: %s group refusal", p)
	}

	tg := PlatformStartRuleFor("telegram")
	require.Equal(t, "Telegram", tg.DisplayName, "T-1: display name for user and agent texts")
	require.True(t, strings.TrimSpace(tg.DisplayName) != "")

	di := PlatformStartRuleFor("discord")
	require.Equal(t, "Discord", di.DisplayName)

	unknown := PlatformStartRuleFor("no_such_platform")
	require.False(t, unknown.StartSupported, "T-1: an unknown platform supports nothing")
}

// TestT10b_CanonicalIdempotence_T10b pins the canonicalizer's serializer
// equality half inside pkg/channels: canonicalizing an already-canonical
// value returns it unchanged (the property the telegram package's round trip
// relies on).
func TestT10b_CanonicalIdempotence_T10b(t *testing.T) {
	for _, c := range []string{
		"discord:111111111111111111", "telegram:123456",
		"telegram:-1001234567890", "telegram:-1001234567890/45",
		"telegram:9223372036854775807", "telegram:-9223372036854775808/2147483647",
	} {
		again, code := CanonicalizeTarget(platformOf(c), c)
		require.Empty(t, code, "T-10b: a canonical value re-canonicalizes cleanly: %q", c)
		require.Equal(t, c, again,
			"T-10b: serializer equality — canonical form is a fixed point: %q", c)
	}
}

// platformOf returns the platform prefix of a canonical "platform:id" value.
func platformOf(canonical string) string {
	if i := strings.IndexByte(canonical, ':'); i >= 0 {
		return canonical[:i]
	}
	return canonical
}

// Compile-time anchor: the config shell type this package's writer consumes.
var _ = config.ChannelOutboundConfig{}
