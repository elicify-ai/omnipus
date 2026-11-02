package config_test

// U7: saved Allow From strings must self-heal without changing other list
// fields. Oracles: channels-spec-r8 FR-054 / US-7; the Configure help text;
// the chief's separator ruling and the explicit U7 compatibility cases.
// Chief option B (2026-11-02 06:38Z): WhatsApp uses commas only; every other
// platform uses commas or whitespace. No production parser supplies expectations.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestU7_LoadAllowFromNormalizesSavedStringsForEveryPlatform(t *testing.T) {
	// All 13 platforms retain list/empty compatibility; WhatsApp alone keeps
	// internal whitespace inside an ID (chief option B).
	platforms := []string{
		"discord", "telegram", "whatsapp", "slack", "feishu", "dingtalk", "qq",
		"wecom", "matrix", "line", "irc", "weixin", "google-chat",
	}
	cases := []struct {
		name         string
		raw          string
		want         []string
		whatsappWant []string
	}{
		{name: "mixed_separators_self_heal", raw: `"a, b c"`, want: []string{"a", "b", "c"}, whatsappWant: []string{"a", "b c"}},
		{name: "comma_separators_self_heal", raw: `"a,b,c"`, want: []string{"a", "b", "c"}},
		{name: "space_separators_self_heal", raw: `"a b c"`, want: []string{"a", "b", "c"}, whatsappWant: []string{"a b c"}},
		{name: "empty_string_allows_all", raw: `""`, want: []string{}},
		{name: "single_id_is_unchanged", raw: `"a"`, want: []string{"a"}},
		{name: "proper_list_is_unchanged", raw: `["a","b","c"]`, want: []string{"a", "b", "c"}},
		{name: "empty_list_is_unchanged", raw: `[]`, want: []string{}},
	}
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					want := tc.want
					if platform == "whatsapp" && tc.whatsappWant != nil {
						want = tc.whatsappWant
					}
					home := t.TempDir()
					t.Setenv("OMNIPUS_HOME", home)
					path := filepath.Join(home, "config.json")
					saved := fmt.Sprintf(`{"version":1,"agents":{"defaults":{"home":%q},"list":[]},"providers":[],"channels":{%q:{"type":%q,"enabled":false,"allow_from":%s}}}`,
						home, platform, platform, tc.raw)
					require.NoError(t, os.WriteFile(path, []byte(saved), 0o600), "instrument: write the saved legacy config")

					loaded, err := config.LoadConfig(path)
					require.NoError(t, err, "instrument: this valid saved config must load before checking U7")
					instance, exists := loaded.Channels[platform]
					require.True(t, exists, "instrument: the loader must retain the selected platform instance")
					// nil and [] both mean no restrictions; compare their exact
					// sequence as [] so representation alone is not a false RED.
					got := append([]string{}, instance.AllowFrom...)
					assert.Equal(t, want, got,
						"U7/FR-054: %s allow_from %s must use its platform's separators; empty input must not become one empty ID",
						platform, tc.raw)
				})
			}
		})
	}
}

func TestU7_LoadAllowFromHonorsWhatsAppCommaOnly(t *testing.T) {
	// Exact phone/Discord literals and comma-only policy: chief option B.
	// Preserve internal spaces: no phone-to-JID rewrite is specified. The
	// native adapter's handleIncoming matches JIDs via identity.MatchAllowed;
	// these load assertions do not claim a formatted phone matches that JID.
	cases := []struct {
		name     string
		platform string
		raw      string
		want     []string
	}{
		{
			name: "whatsapp_single_spaced_phone", platform: "whatsapp",
			raw: `"+1 234 567"`, want: []string{"+1 234 567"},
		},
		{
			name: "whatsapp_two_comma_separated_spaced_phones", platform: "whatsapp",
			raw: `"+1 234 567, +4 987 654"`, want: []string{"+1 234 567", "+4 987 654"},
		},
		{
			name: "discord_spaces_still_separate_ids", platform: "discord",
			raw: `"111 222"`, want: []string{"111", "222"},
		},
		{
			name: "whatsapp_comma_separated_jids_unchanged", platform: "whatsapp",
			raw:  `"1234567@s.whatsapp.net, 4987654@s.whatsapp.net"`,
			want: []string{"1234567@s.whatsapp.net", "4987654@s.whatsapp.net"},
		},
		{
			name: "whatsapp_proper_phone_list_unchanged", platform: "whatsapp",
			raw: `["+1 234 567","+4 987 654"]`, want: []string{"+1 234 567", "+4 987 654"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			path := filepath.Join(home, "config.json")
			saved := fmt.Sprintf(`{"version":1,"agents":{"defaults":{"home":%q},"list":[]},"providers":[],"channels":{%q:{"type":%q,"enabled":false,"allow_from":%s}}}`,
				home, tc.platform, tc.platform, tc.raw)
			require.NoError(t, os.WriteFile(path, []byte(saved), 0o600))
			loaded, err := config.LoadConfig(path)
			require.NoError(t, err, "instrument: valid saved config must load")
			instance, exists := loaded.Channels[tc.platform]
			require.True(t, exists, "instrument: selected platform must survive load")
			assert.Equal(t, tc.want, []string(instance.AllowFrom),
				"chief option B: WhatsApp keeps phone spaces and splits only at commas; Discord still splits spaces")
		})
	}
}

func TestU7_LoadAllowFromDoesNotSplitOutboundTargetWithSpaces(t *testing.T) {
	// Explicit U7 non-interference oracle: only allowed-sender fields gain
	// separator normalization. A generic FlexibleStringSlice change is wrong.
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	path := filepath.Join(home, "config.json")
	saved := fmt.Sprintf(`{"version":1,"agents":{"defaults":{"home":%q},"list":[]},"providers":[],"channels":{"discord":{"type":"discord","enabled":false,"allow_from":["a"],"outbound":{"policy":"reply_only","targets":"destination with a space"}}}}`, home)
	require.NoError(t, os.WriteFile(path, []byte(saved), 0o600))

	loaded, err := config.LoadConfig(path)
	require.NoError(t, err, "instrument: load the saved config through the public loader")
	outbound := loaded.Channels["discord"].Outbound
	require.NotNil(t, outbound, "instrument: load must retain the saved outbound object")
	assert.Equal(t, []string{"destination with a space"}, []string(outbound.Targets),
		"U7 guard: an outbound-target string is one value, not an Allow From field to split")
}
