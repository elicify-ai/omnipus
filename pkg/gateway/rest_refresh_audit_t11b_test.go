package gateway

// Founder Q12 A / S5. Team-lead clarified retry A: a same-value retry must
// preserve the original event, not duplicate it (existing row 9.27).
// Real person PUT, disk write, locked credential-store refresh failure, and
// JSONL audit. No writer, refresh, audit or sensitive-data filter is mocked.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	t11bGateOldSecret = "t11b-gate-registered-old-value"
	t11bGateNewSecret = "t11b-gate-registered-new-value"
)

func t11bGateAuditFilter(t *testing.T, api *restAPI, enabled bool) {
	t.Helper()
	const oldRef, newRef = "T11B_GATE_OLD", "T11B_GATE_NEW"
	t.Setenv(oldRef, "")
	t.Setenv(newRef, "")
	api.credStore = newUnlockedStore(t, api.homePath)
	t.Cleanup(api.credStore.Close)
	require.NoError(t, api.credStore.Set(oldRef, t11bGateOldSecret))
	require.NoError(t, api.credStore.Set(newRef, t11bGateNewSecret))
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Tools.FilterSensitiveData = enabled
		cfg.Tools.Web.Tavily.APIKeyRef, cfg.Tools.Web.Tavily.Enabled = oldRef, false
		cfg.Tools.Web.Exa.APIKeyRef, cfg.Tools.Web.Exa.Enabled = newRef, false
		return config.SaveConfig(api.configPath(), cfg)
	}))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	t.Cleanup(func() { api.agentLoop.GetConfig().RegisterSensitiveValues(nil) })
	t11bGateAssertFilter(t, api, enabled)
}

func t11bGateAssertFilter(t *testing.T, api *restAPI, enabled bool) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	require.Equal(t, enabled, cfg.Tools.IsFilterSensitiveDataEnabled())
	require.NotNil(t, cfg.SensitiveDataReplacer(), "instrument: actual credential references re-arm the filter")
	for _, secret := range []string{t11bGateOldSecret, t11bGateNewSecret} {
		require.NotContains(t, cfg.SensitiveDataReplacer().Replace(secret), secret,
			"instrument: the real filter recognizes each entire synthetic secret")
	}
}

func TestSettingsThirdPartyAgents_DurableRefreshFailureAuditedAndRetryPreserves_S5(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "filter_on", false: "filter_off"}[enabled], func(t *testing.T) {
			api, f, _ := t11bSettingsAPI(t)
			t11bGateAuditFilter(t, api, enabled)
			oldProgram := t11bWriteExe(t, "codex-audit-before")
			newProgram := t11bWriteExe(t, "codex-audit-after")
			retryProgram := t11bWriteExe(t, "codex-audit-retry")
			oldPreset := "--model before --api-key " + t11bGateOldSecret
			newPreset := "--model after --api-key " + t11bGateNewSecret
			retryPreset := "--model retry --api-key " + t11bGateOldSecret
			body := func(path, preset string) gen.ThirdPartyAgentSettings {
				return t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: path, PresetArgs: preset}, gen.CliPreset{})
			}
			w := t11bPut(t, f, body(oldProgram, oldPreset))
			require.Equal(t, http.StatusOK, w.Code, "positive control: %s", w.Body.String())
			t11bGateAssertFilter(t, api, enabled)
			_, err := api.auditor.Verify(t.Context())
			require.NoError(t, err)
			require.Len(t, readAuditEntries(t, f.auditDir, "settings.third_party_agents.path_changed"), 1)
			require.Len(t, readAuditEntries(t, f.auditDir, "settings.third_party_agents.preset_changed"), 1)

			// Close is a real, reversible store-wide health failure. A missing
			// individual credential would merely degrade and is not this fault.
			api.credStore.Close()
			require.ErrorIs(t, api.credStore.HealthCheck(), credentials.ErrStoreLocked)
			w = t11bPut(t, f, body(newProgram, newPreset))
			require.Equal(t, http.StatusInternalServerError, w.Code, "the live refresh really fails: %s", w.Body.String())
			t11bGateAssertDiskRow(t, api, newProgram, newPreset)
			assert.Equal(t, oldProgram, api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("codex").Path,
				"instrument: disk advanced but the old live config was retained")
			t11bGateAssertChangeAudits(t, api, f, 2, oldProgram, newProgram, oldPreset, newPreset, enabled)

			require.NoError(t, credentials.Unlock(api.credStore))
			require.NoError(t, api.credStore.HealthCheck())
			w = t11bPut(t, f, body(newProgram, newPreset))
			require.Equal(t, http.StatusOK, w.Code, "same-value successful retry: %s", w.Body.String())
			t11bGateAssertFilter(t, api, enabled)
			assert.Equal(t, newProgram, api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("codex").Path)
			t11bGateAssertChangeAudits(t, api, f, 2, oldProgram, newProgram, oldPreset, newPreset, enabled)

			// A retry with ANOTHER actual durable change needs its own event,
			// including when live apply fails again. It is not a duplicate no-op.
			api.credStore.Close()
			w = t11bPut(t, f, body(retryProgram, retryPreset))
			require.Equal(t, http.StatusInternalServerError, w.Code)
			t11bGateAssertDiskRow(t, api, retryProgram, retryPreset)
			t11bGateAssertChangeAudits(t, api, f, 3, newProgram, retryProgram, newPreset, retryPreset, enabled)
		})
	}
}

func t11bGateAssertDiskRow(t *testing.T, api *restAPI, program, preset string) {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var disk config.Config
	require.NoError(t, json.Unmarshal(raw, &disk))
	assert.Equal(t, program, disk.Sandbox.ThirdPartyCLI("codex").Path, "the actual persisted program changed")
	assert.Equal(t, preset, disk.Sandbox.ThirdPartyCLI("codex").PresetArgs, "audit filtering must not rewrite the saved parameters")
}

func t11bGateAssertChangeAudits(t *testing.T, api *restAPI, f *t8SettingsFixture, count int, oldPath, newPath, oldPreset, newPreset string, enabled bool) {
	t.Helper()
	_, err := api.auditor.Verify(t.Context())
	require.NoError(t, err)
	for _, field := range []string{"path", "preset"} {
		entries := readAuditEntries(t, f.auditDir, "settings.third_party_agents."+field+"_changed")
		if !assert.Len(t, entries, count, "S5: audit each durable change immediately, even on failed refresh; same-value retry adds none") {
			continue // failure is already recorded; avoid an out-of-bounds panic
		}
		values := t8AuditScalars(entries[count-1])
		assert.Contains(t, values, "codex")
		assert.Contains(t, values, "t8-person")
		if field == "path" {
			assert.Contains(t, values, oldPath)
			assert.Contains(t, values, newPath)
		} else if !enabled {
			assert.Contains(t, values, oldPreset, "OFF: record the typed old parameters")
			assert.Contains(t, values, newPreset, "OFF: record the typed new parameters")
		} else {
			oldValue := t11bAuditPresetValue(t, values, strings.Fields(oldPreset)[1])
			newValue := t11bAuditPresetValue(t, values, strings.Fields(newPreset)[1])
			assert.NotContains(t, oldValue, t11bGateOldSecret)
			assert.NotContains(t, oldValue, t11bGateNewSecret)
			assert.NotContains(t, newValue, t11bGateOldSecret)
			assert.NotContains(t, newValue, t11bGateNewSecret)
		}
		if enabled {
			raw, err := json.Marshal(entries)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), t11bGateOldSecret)
			assert.NotContains(t, string(raw), t11bGateNewSecret)
		}
	}
}
