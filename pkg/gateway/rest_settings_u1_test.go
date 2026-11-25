package gateway

// Settings #1519 U1-1, from G-09 hands-on evidence on 5a8c4355c0.
// Oracle: docs/internal/specs/settings-redesign-spec.md::Active transcriber
// needs the password (US-3 AS5) and docs/settings.md::Voice input: the new
// transcriber is STORED. Real handlers, consent, encrypted store and config
// file; only the absent harness reload pipeline is supplied at its edge.

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestSettingsU1_ActiveTranscriberSavePersistsAcrossFreshConfigLoad(t *testing.T) {
	for _, tc := range []struct{ from, to, ref string }{
		{from: "elevenlabs", to: "groq", ref: "GROQ_API_KEY"},
		{from: "groq", to: "elevenlabs", ref: "ELEVENLABS_API_KEY"},
	} {
		t.Run(tc.from+"_to_"+tc.to, func(t *testing.T) {
			api, user, _ := newRolesTestAPI(t)
			storeWebSearchKeys(t, api, "ELEVENLABS_API_KEY", "GROQ_API_KEY")
			seedSettingsU1Voice(t, api, api.agentLoop.GetConfig(), tc.from)
			before := getIntegrationList(t, api, user)
			require.Equal(t, []string{tc.from}, activeSettingsU1VoiceIDs(before), "U1-1 premise: exactly the old transcriber is active")
			api.agentLoop.SetReloadFunc(func() error {
				fresh, err := config.LoadConfig(api.configPath())
				if err != nil {
					return err
				}
				api.agentLoop.GetConfig().Voice = fresh.Voice
				return nil
			})

			w := putRoles(t, api, user, tc.to, `{"kind":"voice","active":true}`)
			require.Equal(t, http.StatusOK, w.Code, "U1-1: valid authenticated activation: %s", w.Body.String())
			// Do not infer storage from 200 or from the same mutable config pointer.
			fresh, err := config.LoadConfig(api.configPath())
			require.NoError(t, err)
			assert.Equal(t, tc.to, api.activeVoiceProviderID(fresh), "U1-1: successful Save must store the NEW transcriber for a restart")
			refs := map[string]string{"groq": fresh.Voice.GroqAPIKeyRef, "elevenlabs": fresh.Voice.ElevenLabsAPIKeyRef}
			assert.Equal(t, tc.ref, refs[tc.to], "U1-1: selected service's saved credential reference")
			after := getIntegrationList(t, api, user)
			assert.Equal(t, []string{tc.to}, activeSettingsU1VoiceIDs(after), "U1-1: fresh GET must mark exactly the selected transcriber active")
		})
	}
}

// U1-C1: the configured prerequisite is part of the IntegrationProvider
// contract (audio-model needs voice.model_name) and docs/settings.md::Voice
// input. US-3 AS5 requires the selected transcriber to be stored. An unready
// choice must not destroy the previously working transcriber (CHECK C1).
func TestSettingsU1_KeylessAudioModelReadinessPersistsAcrossFreshConfigLoad(t *testing.T) {
	for _, from := range []string{"elevenlabs", "groq"} {
		for _, ready := range []bool{false, true} {
			name := from + "_unready"
			if ready {
				name = from + "_ready"
			}
			t.Run(name, func(t *testing.T) {
				api, user, cfg := newRolesTestAPI(t)
				storeWebSearchKeys(t, api, "ELEVENLABS_API_KEY", "GROQ_API_KEY")
				seedSettingsU1Voice(t, api, cfg, from)
				if ready {
					seedSettingsU1AudioModel(t, api, cfg)
				}
				before, err := config.LoadConfig(api.configPath())
				require.NoError(t, err)
				beforeBytes, err := os.ReadFile(api.configPath())
				require.NoError(t, err)
				if !ready {
					require.Empty(t, before.Voice.ModelName)
					require.Equal(t, []string{from}, activeSettingsU1VoiceIDs(getIntegrationList(t, api, user)))
				}
				api.agentLoop.SetReloadFunc(func() error {
					fresh, loadErr := config.LoadConfig(api.configPath())
					if loadErr != nil {
						return loadErr
					}
					cfg.Voice, cfg.Providers = fresh.Voice, fresh.Providers
					return nil
				})

				w := putRoles(t, api, user, "audio-model", `{"kind":"voice","active":true}`)
				fresh, err := config.LoadConfig(api.configPath())
				require.NoError(t, err)
				if !ready {
					assert.Equal(t, http.StatusBadRequest, w.Code, "U1-C1: an unconfigured Audio Model must be refused: %s", w.Body.String())
					var problem gen.ErrorResponse
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
					// No prose is prescribed: require the reason to name the missing
					// prerequisite from IntegrationProvider.configured, not auth.
					assert.Contains(t, problem.Error, "voice.model_name", "readiness error names the missing prerequisite")
					afterBytes, readErr := os.ReadFile(api.configPath())
					require.NoError(t, readErr)
					assert.Equal(t, beforeBytes, afterBytes, "refusal writes no config")
					assert.Equal(t, before.Voice, fresh.Voice, "old transcriber and credential references survive a fresh disk load")
					assert.Equal(t, from, api.activeVoiceProviderID(fresh))
					assert.Equal(t, []string{from}, activeSettingsU1VoiceIDs(getIntegrationList(t, api, user)))
					return
				}
				assert.Equal(t, http.StatusOK, w.Code, "U1-C1: a configured Audio Model can be activated: %s", w.Body.String())
				assert.Equal(t, "u1-audio-model", fresh.Voice.ModelName)
				assert.Empty(t, fresh.Voice.ElevenLabsAPIKeyRef, "ready activation clears the previous keyed selection")
				assert.Empty(t, fresh.Voice.GroqAPIKeyRef, "ready activation clears the previous keyed selection")
				assert.Equal(t, "audio-model", api.activeVoiceProviderID(fresh), "successful activation is stored, not only acknowledged")
				assert.Equal(t, []string{"audio-model"}, activeSettingsU1VoiceIDs(getIntegrationList(t, api, user)))
			})
		}
	}
}

func seedSettingsU1AudioModel(t *testing.T, api *restAPI, cfg *config.Config) {
	t.Helper()
	b, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	raw["voice"].(map[string]any)["model_name"] = "u1-audio-model"
	raw["providers"] = []*config.ModelConfig{{
		Provider: "openai", Model: "u1-audio-model", Protocol: "openai-compatible",
	}}
	b, err = json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), b, 0o600))
	fresh, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	cfg.Voice, cfg.Providers = fresh.Voice, fresh.Providers
}

func activeSettingsU1VoiceIDs(resp gen.IntegrationProvidersResponse) []string {
	var ids []string
	for _, row := range resp.Voice {
		if row.Active != nil && *row.Active {
			ids = append(ids, string(row.Id))
		}
	}
	return ids
}

func seedSettingsU1Voice(t *testing.T, api *restAPI, cfg *config.Config, active string) {
	t.Helper()
	b, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	refs := map[string]string{"elevenlabs": "ELEVENLABS_API_KEY", "groq": "GROQ_API_KEY"}
	raw["voice"] = map[string]any{active + "_api_key_ref": refs[active]}
	b, err = json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), b, 0o600))
	fresh, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	cfg.Voice = fresh.Voice
}
