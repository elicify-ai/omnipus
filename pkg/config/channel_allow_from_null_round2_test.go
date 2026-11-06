package config_test

import (
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

// Chief ruling C (2026-11-02 08:09Z, CHECK F4): preserve the release's
// deny-all meaning for an explicitly saved null. Missing still means allow
// all. Test the public admission decision, never a particular null sentinel.
func TestRound2_LoadNullAllowFromBlocksEverySender(t *testing.T) {
	round2CheckLoadedAdmission(t, `,"allow_from":null`, false)
}

func TestRound2_LoadMissingAllowFromAllowsEverySender(t *testing.T) {
	round2CheckLoadedAdmission(t, "", true)
}

func round2CheckLoadedAdmission(t *testing.T, allowFromJSON string, wantAllowed bool) {
	t.Helper()
	for _, key := range []string{"discord", "discord.legacy", "whatsapp", "whatsapp.legacy"} {
		for _, explicitType := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit_type_%t", key, explicitType), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("OMNIPUS_HOME", home)
				platform, _ := config.ParseInstanceKey(key)
				typeJSON := ""
				if explicitType {
					typeJSON = fmt.Sprintf(`,"type":%q`, platform)
				}
				path := filepath.Join(home, "config.json")
				fixture := fmt.Sprintf(`{"version":1,"agents":{"defaults":{"home":%q},"list":[]},"providers":[],"channels":{%q:{"enabled":false%s%s}}}`,
					home, key, typeJSON, allowFromJSON)
				require.NoError(t, os.WriteFile(path, []byte(fixture), 0o600))
				loaded, err := config.LoadConfig(path)
				require.NoError(t, err, "instrument: the valid saved config must load before testing admission")
				instance, exists := loaded.Channels[key]
				require.True(t, exists, "instrument: the exact addressed instance must survive loading")
				adapter := channels.NewBaseChannel(key, instance, bus.NewMessageBus(), instance.AllowFrom)
				for _, id := range []string{"200000000000000001", "another-sender", "1234567890@s.whatsapp.net"} {
					sender := bus.SenderInfo{Platform: platform, PlatformID: id, CanonicalID: platform + ":" + id}
					assert.Equal(t, wantAllowed, adapter.IsAllowedSender(sender),
						"chief C: explicit null denies every sender; an absent key permits every sender (instance=%s, sender=%s)", key, sender.CanonicalID)
				}
			})
		}
	}
}
