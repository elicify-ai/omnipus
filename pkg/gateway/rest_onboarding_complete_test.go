// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_onboarding_complete_test.go — T068-16 (ADR-068 FR-035 / FR-020 /
// FR-036, TDD row 22).
//
// POST /api/v1/onboarding/complete consumes the `provider` discriminated union
// (OnboardingProviderApiKey | OnboardingProviderSignIn) and writes the SAME two
// things either way: one provider row stamped with the auth method it uses, and
// agents.defaults.default_model as the exact (provider, model) pair the operator
// picked — written once, by this handler, and never rewritten by a reload.
//
// What separates the variants is only the credential:
//   - api_key  — probed, then stored encrypted; api_key_ref on the row.
//   - sign_in  — nothing probed and NOTHING stored (FR-007: the vendor CLI holds
//     the login and Omnipus only ever reads it). No api_key_ref, no
//     api_key, and no "stored in plaintext" warning, which would be a
//     lie about a credential that does not exist.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// readOnboardedConfig reads the config.json onboarding just wrote, both raw
// (so an ABSENT key is distinguishable from an empty one) and typed.
func readOnboardedConfig(t *testing.T, tmpDir string) (map[string]any, *config.Config) {
	t.Helper()
	path := filepath.Join(tmpDir, "config.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	loaded, err := config.LoadConfig(path)
	require.NoError(t, err)
	return m, loaded
}

// providerRow returns the single persisted providers[] entry for (id, model).
func providerRow(t *testing.T, cfgRaw map[string]any, id, model string) map[string]any {
	t.Helper()
	rows, _ := cfgRaw["providers"].([]any)
	require.NotEmpty(t, rows, "onboarding must persist a provider row: %v", cfgRaw["providers"])
	for _, row := range rows {
		rm, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if rm["provider"] == id && rm["model"] == model {
			return rm
		}
	}
	t.Fatalf("no providers[] entry for {provider:%q, model:%q}: %v", id, model, rows)
	return nil
}

// TestOnboardingComplete_AuthMethod is TDD row 22 / the BDD scenario
// "Onboarding complete with sign-in".
func TestOnboardingComplete_AuthMethod(t *testing.T) {
	t.Run("sign_in without a key completes, stores no credential and writes the pair",
		func(t *testing.T) {
			api, tmpDir := newAuthMethodOnboardingAPI(t)
			body := `{"provider":{"auth_method":"sign_in","id":"codex-cli","model":"gpt-5.3-codex"},` +
				`"admin":{"username":"admin","password":"secret123"}}`

			w := postOnboardingComplete(api, body)

			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.NotEmpty(t, resp["token"])
			assert.Equal(t, "admin", resp["username"])
			// The plaintext-key warning must NOT fire: there is no key. Before
			// the sign-in path existed, an empty credRefName was proof of a
			// failed encrypted store; on this variant it is the normal state.
			assert.Nil(t, resp["warning"],
				"a sign-in completion has no key, so it can carry no key warning")

			// FR-007: nothing reached the credential store under any name.
			_, err := api.credStore.Get("codex-cli_API_KEY")
			assert.Error(t, err, "a sign-in completion must store no credential")

			cfgRaw, loaded := readOnboardedConfig(t, tmpDir)
			row := providerRow(t, cfgRaw, "codex-cli", "gpt-5.3-codex")
			assert.Equal(t, "sign_in", row["auth_method"],
				"the persisted row records HOW it authenticates (FR-003)")
			_, hasRef := row["api_key_ref"]
			assert.False(t, hasRef, "no credential ref may be written for a sign-in row: %v", row)
			_, hasKey := row["api_key"]
			assert.False(t, hasKey, "no api_key may be written for a sign-in row: %v", row)

			// FR-020: the pair, written once, exactly as picked.
			defaults, _ := cfgRaw["agents"].(map[string]any)["defaults"].(map[string]any)
			assert.Equal(t,
				map[string]any{"provider": "codex-cli", "model": "gpt-5.3-codex"},
				defaults["default_model"])
			_, hasAlias := defaults["model_name"]
			assert.False(t, hasAlias, "agents.defaults.model_name is gone (CRIT-001)")

			want := config.DefaultModel{Provider: "codex-cli", Model: "gpt-5.3-codex"}
			require.Equal(t, want, loaded.Agents.Defaults.DefaultModel)
			// …and the pair resolves EXACTLY to the row onboarding created.
			mc, err := loaded.GetModelConfig("codex-cli", "gpt-5.3-codex")
			require.NoError(t, err)
			assert.Equal(t, config.AuthMethodSignIn, mc.AuthMethod)
			assert.Empty(t, mc.APIKeyRef)

			// FR-020 / MIN-009: a later reload never overwrites the pair. The
			// `ModelName == ""` guards that used to back-fill it are gone, and
			// this is the sign_in half of that assertion.
			require.NoError(t, api.agentLoop.ReloadProviderAndConfig(
				context.Background(), &restMockProvider{}, loaded))
			assert.Equal(t, want, api.agentLoop.GetConfig().Agents.Defaults.DefaultModel,
				"ReloadProviderAndConfig must leave default_model exactly as written")

			assert.True(t, api.onboardingMgr.IsComplete())
		})

	t.Run("sign_in with no model falls back to the row's first Recommended model",
		func(t *testing.T) {
			api, tmpDir := newAuthMethodOnboardingAPI(t)
			body := `{"provider":{"auth_method":"sign_in","id":"codex-cli"},` +
				`"admin":{"username":"admin","password":"secret123"}}`

			w := postOnboardingComplete(api, body)
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

			cfgRaw, loaded := readOnboardedConfig(t, tmpDir)
			pair := loaded.Agents.Defaults.DefaultModel
			assert.Equal(t, "codex-cli", pair.Provider)
			require.NotEmpty(t, pair.Model,
				"the pair must name a real model, never the api_key branch's gpt-4o guess")
			assert.NotEqual(t, "gpt-4o", pair.Model,
				"a sign-in row must not inherit the api_key branch's vendor default")

			// Whatever it picked, the row it points at exists and is a sign-in row.
			row := providerRow(t, cfgRaw, "codex-cli", pair.Model)
			assert.Equal(t, "sign_in", row["auth_method"])
			mc, err := loaded.GetModelConfig(pair.Provider, pair.Model)
			require.NoError(t, err)
			assert.Equal(t, config.AuthMethodSignIn, mc.AuthMethod)
		})

	t.Run("sign_in carrying an api_key is 400 and persists nothing", func(t *testing.T) {
		api, tmpDir := newAuthMethodOnboardingAPI(t)
		body := `{"provider":{"auth_method":"sign_in","id":"codex-cli","model":"gpt-5.3-codex",` +
			`"api_key":"sk-should-not-be-here"},"admin":{"username":"admin","password":"secret123"}}`

		w := postOnboardingComplete(api, body)

		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		// The strict variant decode is what refuses it — unconditionally, with
		// no dependence on ValidateInbound (ADR-034) — and it names the schema
		// the body violated so the operator knows which shape they sent.
		assert.Contains(t, w.Body.String(), "field not allowed on provider auth_method")
		assert.Contains(t, w.Body.String(), "OnboardingProviderSignIn")

		assert.False(t, api.onboardingMgr.IsComplete())
		raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "sk-should-not-be-here",
			"a rejected body must never leave its key on disk")
		assert.NotContains(t, string(raw), "codex-cli")
	})

	t.Run("api_key without a key is 400", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		body := `{"provider":{"auth_method":"api_key","id":"openai","model":"gpt-4o"},` +
			`"admin":{"username":"admin","password":"secret123"}}`

		w := postOnboardingComplete(api, body)

		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "provider.api_key is required", resp["error"])
		assert.False(t, api.onboardingMgr.IsComplete())
	})

	t.Run("sign_in on a key-only provider is 400", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		body := `{"provider":{"auth_method":"sign_in","id":"openrouter","model":"openai/gpt-4o"},` +
			`"admin":{"username":"admin","password":"secret123"}}`

		w := postOnboardingComplete(api, body)

		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, onboardingSignInUnsupportedMsg, resp["error"])
		assert.Equal(t, "id", resp["field"])
		assert.False(t, api.onboardingMgr.IsComplete())
	})
}
