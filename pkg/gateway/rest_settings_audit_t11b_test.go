package gateway

// Founder Q7 A, relayed by team-lead 2026-11-06: preset-change audit old and
// new text follows the same tools.filter_sensitive_data switch as run audit.
// Real signed-in-person Settings PUTs, persistence, audit writer and JSONL;
// neither the handler nor the sensitive-data filter is mocked.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t11bAuditPresetValue(t *testing.T, values []string, model string) string {
	t.Helper()
	var matches []string
	for _, value := range values {
		if strings.Contains(value, model) {
			matches = append(matches, value)
		}
	}
	require.Len(t, matches, 1, "audit must actually record the old/new preset with model %q", model)
	require.Contains(t, matches[0], "--api-key", "record the parameter text, not only an isolated model label")
	return matches[0]
}

func TestSettingsThirdPartyAgents_PresetChangeAuditFollowsFilterSwitch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{{"on", true}, {"off", false}} {
		t.Run(tc.name, func(t *testing.T) {
			api, f, _ := t11bSettingsAPI(t)
			// Real saves reload config and re-arm the filter from credential refs.
			// These values are deliberately NOT provider-key-shaped: OFF must be
			// observable independently of the audit logger's always-on pattern mask.
			const oldSecret = "t11b-settings-registered-old-value"
			const newSecret = "t11b-settings-registered-new-value"
			const oldRef = "T11B_AUDIT_OLD"
			const newRef = "T11B_AUDIT_NEW"
			t.Setenv(oldRef, "")
			t.Setenv(newRef, "")
			api.credStore = newUnlockedStore(t, api.homePath)
			require.NoError(t, api.credStore.Set(oldRef, oldSecret))
			require.NoError(t, api.credStore.Set(newRef, newSecret))
			require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
				cfg.Tools.FilterSensitiveData = tc.enabled
				cfg.Tools.Web.Tavily.APIKeyRef = oldRef
				cfg.Tools.Web.Tavily.Enabled = false
				cfg.Tools.Web.Exa.APIKeyRef = newRef
				cfg.Tools.Web.Exa.Enabled = false
				return config.SaveConfig(api.configPath(), cfg)
			}))
			require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
			oldPreset := "--api-key " + oldSecret + " --model t11b-old-model"
			newPreset := "--api-key " + newSecret + " --model t11b-new-model"
			program := t11bWriteExe(t, "codex-settings-audit")
			t.Cleanup(func() { api.agentLoop.GetConfig().RegisterSensitiveValues(nil) })
			for _, preset := range []string{oldPreset, newPreset} {
				cfg := api.agentLoop.GetConfig()
				require.NotContains(t, cfg.SensitiveDataReplacer().Replace(oldSecret), oldSecret,
					"instrument: the real filter knows both complete synthetic credentials")
				require.NotContains(t, cfg.SensitiveDataReplacer().Replace(newSecret), newSecret)
				require.Equal(t, tc.enabled, cfg.Tools.IsFilterSensitiveDataEnabled())
				body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: program, PresetArgs: preset}, gen.CliPreset{})
				w := t11bPut(t, f, body)
				require.Equal(t, http.StatusOK, w.Code, "real person save: %s", w.Body.String())
				current := api.agentLoop.GetConfig()
				assert.Equal(t, preset, current.Sandbox.ThirdPartyCLI("codex").PresetArgs,
					"filter the audit copy only; never alter the saved preset")
				require.Equal(t, tc.enabled, current.Tools.IsFilterSensitiveDataEnabled(), "instrument: the switch survives the actual Settings reload")
				require.NotContains(t, current.SensitiveDataReplacer().Replace(oldSecret), oldSecret, "instrument: OLD secret survives through the real credential-store re-arm")
				require.NotContains(t, current.SensitiveDataReplacer().Replace(newSecret), newSecret, "instrument: NEW secret survives through the real credential-store re-arm")
			}
			// Public logger read boundary flushes its buffer; no test-side filter.
			_, err := api.auditor.Verify(t.Context())
			require.NoError(t, err)
			entries := readAuditEntries(t, f.auditDir, "settings.third_party_agents.preset_changed")
			require.Len(t, entries, 2, "each of the two actual preset changes emits exactly one event")
			values := t8AuditScalars(entries[1])
			assert.Contains(t, values, "codex")
			assert.Contains(t, values, "t8-person")
			// The existing Settings audit contract specifies old/new values, not
			// key spellings. Select their unique benign model values independently.
			oldValue := t11bAuditPresetValue(t, values, "t11b-old-model")
			newValue := t11bAuditPresetValue(t, values, "t11b-new-model")
			if tc.enabled {
				for _, entry := range entries {
					raw, err := json.Marshal(entry)
					require.NoError(t, err)
					assert.NotContains(t, string(raw), oldSecret, "Q7 A: the OLD parameter text is filtered")
					assert.NotContains(t, string(raw), newSecret, "Q7 A: the NEW parameter text is filtered")
				}
				assert.NotContains(t, oldValue, oldSecret)
				assert.NotContains(t, newValue, newSecret)
			} else {
				assert.Equal(t, oldPreset, oldValue, "Q7 A / Q5 B: OFF records the old value as typed")
				assert.Equal(t, newPreset, newValue, "Q7 A / Q5 B: OFF records the new value as typed")
			}
		})
	}
}
