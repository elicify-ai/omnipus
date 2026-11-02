package config_test

// U7: saved Allow From strings must self-heal without changing other list
// fields. Oracles: channels-spec-r8 FR-054 / US-7; the Configure help text;
// the chief's comma-or-whitespace ruling and the explicit U7 compatibility
// cases in the test-author brief. No production parser supplies expectations.

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
	// The same saved-field contract applies to all 13 connector platform types.
	platforms := []string{
		"discord", "telegram", "whatsapp", "slack", "feishu", "dingtalk", "qq",
		"wecom", "matrix", "line", "irc", "weixin", "google-chat",
	}
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "mixed_separators_self_heal", raw: `"a, b c"`, want: []string{"a", "b", "c"}},
		{name: "comma_separators_self_heal", raw: `"a,b,c"`, want: []string{"a", "b", "c"}},
		{name: "space_separators_self_heal", raw: `"a b c"`, want: []string{"a", "b", "c"}},
		{name: "empty_string_allows_all", raw: `""`, want: []string{}},
		{name: "single_id_is_unchanged", raw: `"a"`, want: []string{"a"}},
		{name: "proper_list_is_unchanged", raw: `["a","b","c"]`, want: []string{"a", "b", "c"}},
		{name: "empty_list_is_unchanged", raw: `[]`, want: []string{}},
	}
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
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
					assert.Equal(t, tc.want, got,
						"U7/FR-054: %s allow_from %s must load as separate IDs; empty input must not become one empty ID",
						platform, tc.raw)
				})
			}
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
