package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWhatsAppConfigParsing verifies that WhatsAppConfig parses all expected fields
// including the new group_trigger configuration added in Wave 4.
// Traces to: wave4-whatsapp-browser-spec.md line 997 (Test #7: TestWhatsAppConfigParsing)
// BDD: Given a JSON config with whatsapp fields,
// When config is parsed,
// Then all fields are correctly populated including group_trigger.

func TestWhatsAppConfigParsing(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 997 (Test #7)
	tests := []struct {
		name    string
		json    string
		wantCfg WhatsAppConfig
	}{
		{
			name: "basic enabled config",
			json: `{
				"enabled": true,
				"session_store_path": "/home/user/.omnipus/whatsapp/store.db",
				"allow_from": ["1234567890", "0987654321"]
			}`,
			wantCfg: WhatsAppConfig{
				Enabled:          true,
				SessionStorePath: "/home/user/.omnipus/whatsapp/store.db",
				AllowFrom:        FlexibleStringSlice{"1234567890", "0987654321"},
			},
		},
		{
			name: "group trigger mention-only",
			json: `{
				"enabled": true,
				"group_trigger": {
					"mention_only": true
				}
			}`,
			wantCfg: WhatsAppConfig{
				Enabled:      true,
				GroupTrigger: GroupTriggerConfig{MentionOnly: true},
			},
		},
		{
			name: "group trigger prefix list",
			json: `{
				"enabled": true,
				"group_trigger": {
					"prefixes": ["/ask", "/bot"]
				}
			}`,
			wantCfg: WhatsAppConfig{
				Enabled:      true,
				GroupTrigger: GroupTriggerConfig{Prefixes: []string{"/ask", "/bot"}},
			},
		},
		{
			name: "disabled by default",
			json: `{}`,
			wantCfg: WhatsAppConfig{
				Enabled: false,
			},
		},
		{
			// WhatsApp is always native now; a stale legacy bridge_url key must be
			// silently ignored (unknown JSON fields are dropped on load) rather than
			// failing the parse.
			name: "legacy bridge_url is ignored",
			json: `{
				"enabled": true,
				"bridge_url": "http://localhost:8080"
			}`,
			wantCfg: WhatsAppConfig{
				Enabled: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got WhatsAppConfig
			err := json.Unmarshal([]byte(tc.json), &got)
			require.NoError(t, err, "JSON unmarshal of WhatsAppConfig must not error")

			assert.Equal(t, tc.wantCfg.Enabled, got.Enabled, "Enabled mismatch")
			assert.Equal(t, tc.wantCfg.SessionStorePath, got.SessionStorePath, "SessionStorePath mismatch")
			assert.Equal(
				t,
				tc.wantCfg.GroupTrigger.MentionOnly,
				got.GroupTrigger.MentionOnly,
				"GroupTrigger.MentionOnly mismatch",
			)
			assert.Equal(
				t,
				tc.wantCfg.GroupTrigger.Prefixes,
				got.GroupTrigger.Prefixes,
				"GroupTrigger.Prefixes mismatch",
			)

			if tc.wantCfg.AllowFrom != nil {
				assert.Equal(t, []string(tc.wantCfg.AllowFrom), []string(got.AllowFrom), "AllowFrom mismatch")
			}
		})
	}
}

// TestWhatsAppConfigDefaults verifies that zero-value WhatsAppConfig has
// deny-by-default settings (disabled, no allow-list).
// Traces to: wave4-whatsapp-browser-spec.md line 997 (Test #7 — defaults)
// BDD: Given no whatsapp configuration, Then channel is disabled by default.

func TestWhatsAppConfigDefaults(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 997 (Test #7)
	var cfg WhatsAppConfig
	assert.False(t, cfg.Enabled, "WhatsApp channel must be disabled by default (deny-by-default)")
	assert.Empty(t, cfg.SessionStorePath, "SessionStorePath must be empty by default")
	assert.Empty(t, cfg.AllowFrom, "AllowFrom must be empty by default")
	assert.False(t, cfg.GroupTrigger.MentionOnly, "GroupTrigger.MentionOnly must be false by default")
	assert.Empty(t, cfg.GroupTrigger.Prefixes, "GroupTrigger.Prefixes must be empty by default")
}
