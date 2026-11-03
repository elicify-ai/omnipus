package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestU7_LoadNoTypeAllowFromSelfHealsAndSavesArray(t *testing.T) {
	// Chief's 2026-11-02 07:53Z request: missing type must not lose the
	// map key's separator rule. These exact lists come from the request and
	// option B (WhatsApp commas only), never from the production splitter.
	cases := []struct {
		name     string
		key      string
		platform string
		text     string
		want     []string
	}{
		{name: "discord_ids", key: "discord", platform: "discord", text: "id1,id2 id3", want: []string{"id1", "id2", "id3"}},
		{name: "discord_namespaced_ids", key: "discord.legacy", platform: "discord", text: "id1,id2 id3", want: []string{"id1", "id2", "id3"}},
		{name: "whatsapp_ids", key: "whatsapp", platform: "whatsapp", text: "+1 234 567,x@s.whatsapp.net", want: []string{"+1 234 567", "x@s.whatsapp.net"}},
		{name: "whatsapp_namespaced_ids", key: "whatsapp.legacy", platform: "whatsapp", text: "+1 234 567,x@s.whatsapp.net", want: []string{"+1 234 567", "x@s.whatsapp.net"}},
		{name: "discord_empty_allows_all", key: "discord", platform: "discord", text: "", want: []string{}},
		{name: "discord_namespaced_empty_allows_all", key: "discord.legacy", platform: "discord", text: "", want: []string{}},
		{name: "whatsapp_empty_allows_all", key: "whatsapp", platform: "whatsapp", text: "", want: []string{}},
		{name: "whatsapp_namespaced_empty_allows_all", key: "whatsapp.legacy", platform: "whatsapp", text: "", want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			path := filepath.Join(home, "config.json")
			// Deliberately no type field: only the map key identifies the platform.
			legacy := fmt.Sprintf(`{"version":1,"agents":{"defaults":{"home":%q},"list":[]},"providers":[],"channels":{%q:{"enabled":false,"allow_from":%q}}}`,
				home, tc.key, tc.text)
			require.NotContains(t, legacy, `"type"`, "instrument: fixture must exercise the no-type load path")
			require.NoError(t, os.WriteFile(path, []byte(legacy), 0o600))

			loaded, err := config.LoadConfig(path)
			require.NoError(t, err, "instrument: valid legacy config must load")
			instance, exists := loaded.Channels[tc.key]
			require.True(t, exists, "instrument: bare/namespaced instance must survive load")
			assert.Equal(t, tc.platform, instance.Type, "effective type must come from the instance key")
			assert.Equal(t, tc.want, []string(instance.AllowFrom),
				"no-type self-heal must apply the map key's separators without fragmenting a WhatsApp phone")

			// The unlisted sender is admitted only by an empty list. Nonempty
			// cases are negative controls against an always-allow matcher.
			channel := channels.NewBaseChannel(tc.key, instance, bus.NewMessageBus(), instance.AllowFrom)
			outsider := bus.SenderInfo{Platform: tc.platform, PlatformID: "unlisted-selfheal-sender", CanonicalID: tc.platform + ":unlisted-selfheal-sender"}
			assert.Equal(t, len(tc.want) == 0, channel.IsAllowedSender(outsider),
				"empty Allow From means allow all; a nonempty list must reject this unlisted sender")

			require.NoError(t, config.SaveConfig(path, loaded), "instrument: save through the public persistence path")
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			var root map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(saved, &root))
			var instances map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(root["channels"], &instances))
			var entry map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(instances[tc.key], &entry))
			if len(tc.want) > 0 {
				require.Contains(t, entry, "allow_from", "nonempty Allow From must be persisted")
				var savedIDs []string
				require.NoError(t, json.Unmarshal(entry["allow_from"], &savedIDs),
					"saved allow_from must be a JSON array, never the old scalar text")
				assert.Equal(t, tc.want, savedIDs, "raw saved array must contain exactly the healed IDs")
			} else {
				// Characterization guard: existing omitempty omits an empty list;
				// the new requirement is empty/allow-all semantics, not writing [].
				assert.NotContains(t, entry, "allow_from", "existing empty-list omission must not persist a scalar")
			}
			// Inspect actual saved JSON, not private in-memory bookkeeping.
			for _, hidden := range []string{"allowFromText", "AllowFromText", "allow_from_text", "pendingAllowFromText"} {
				assert.NotContains(t, string(saved), `"`+hidden+`"`, "transient Allow From text must never be saved")
			}

			reloaded, err := config.LoadConfig(path)
			require.NoError(t, err, "instrument: saved config must reload")
			require.Contains(t, reloaded.Channels, tc.key)
			assert.Equal(t, tc.want, append([]string{}, reloaded.Channels[tc.key].AllowFrom...),
				"save/reload must preserve healed IDs and empty-list semantics")
		})
	}
}
