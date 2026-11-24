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
