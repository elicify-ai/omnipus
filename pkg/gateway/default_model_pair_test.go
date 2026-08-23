package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ADR-068 D14.1 / FR-018 / FR-020 (T068-07): the default model is the pair
// agents.defaults.default_model {provider, model}. agents.defaults.model_name
// is gone, and with it the two boot/reload guards that used to back-fill it
// from the startup provider's model id — no boot or reload path may ever
// write the default model; onboarding's explicit pick (and, once T068-11
// lands, the default-model PUT) are its only writers.

// TestOnboardingComplete_ApiKey_WritesDefaultModelPair_ReloadDoesNotOverwrite
// is TDD row 22's api_key half (the sign_in half lands with T068-16): the
// completion handler writes agents.defaults.default_model ONCE as the exact
// pair the operator picked, config.json carries no model_name key, and a
// subsequent ReloadProviderAndConfig leaves the pair untouched (FR-020).
func TestOnboardingComplete_ApiKey_WritesDefaultModelPair_ReloadDoesNotOverwrite(t *testing.T) {
	api, tmpDir := newAuthMethodOnboardingAPI(t)
	upstream := startFakeProviderUpstream(t)
	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-pin-key","model":"gpt-4o"},"admin":{"username":"admin","password":"secret123"}}`,
		upstream,
	)

	w := postOnboardingComplete(api, body)
	require.Equal(t, 200, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var cfgRaw map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfgRaw))
	defaults := cfgRaw["agents"].(map[string]any)["defaults"].(map[string]any)
	_, hasAlias := defaults["model_name"]
	assert.False(t, hasAlias, "config.json must carry no agents.defaults.model_name key (CRIT-001): %s", raw)
	assert.Equal(t, map[string]any{"provider": "openai", "model": "gpt-4o"}, defaults["default_model"],
		"onboarding completion writes the exact pair once")

	// The persisted pair loads back as the typed pair...
	loaded, err := config.LoadConfig(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	want := config.DefaultModel{Provider: "openai", Model: "gpt-4o"}
	require.Equal(t, want, loaded.Agents.Defaults.DefaultModel)

	// ...and it resolves EXACTLY to the row onboarding created.
	mc, err := loaded.GetModelConfig("openai", "gpt-4o")
	require.NoError(t, err)
	assert.Equal(t, "openai_API_KEY", mc.APIKeyRef)

	// FR-020: a reload with the loaded config never overwrites the pair.
	require.NoError(t, api.agentLoop.ReloadProviderAndConfig(context.Background(), &restMockProvider{}, loaded))
	assert.Equal(t, want, api.agentLoop.GetConfig().Agents.Defaults.DefaultModel,
		"ReloadProviderAndConfig must leave default_model exactly as written")
}

// TestGateway_NoBootPathWritesDefaultModel pins the deletion of the two
// `ModelName == ""` guards (gateway.go's post-createStartupProvider back-fill
// and handleConfigReload's twin). A `git merge` of an older branch can
// resurrect them as a conflict-free addition, so the absence is asserted
// over the package's non-test sources rather than trusted.
func TestGateway_NoBootPathWritesDefaultModel(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	// Any assignment INTO the default-model pair from Go code in this package.
	writer := regexp.MustCompile(`Agents\.Defaults\.DefaultModel(\.Provider|\.Model)?\s*=[^=]`)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		if loc := writer.FindIndex(src); loc != nil {
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")
			t.Errorf("%s:%d assigns agents.defaults.default_model from Go code; only onboarding completion and the default-model PUT (config-JSON writers) may set it (FR-020)", name, line)
		}
		if strings.Contains(string(src), "Defaults.ModelName") {
			t.Errorf("%s still references the deleted agents.defaults.model_name alias", name)
		}
	}
}

// TestDefaultModelCredentialBlocked_ByPair: the limited-mode check matches the
// default model's backing rows by the exact (provider, model) pair — a row
// serving the same model under a DIFFERENT provider is not a candidate.
func TestDefaultModelCredentialBlocked_ByPair(t *testing.T) {
	t.Run("blocked when every row backing the pair has an unresolved ref", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Provider: "openai", Model: "gpt-4o"},
			}},
			Providers: []*config.ModelConfig{
				{Provider: "openai", Model: "gpt-4o", APIKeyRef: "T068_07_UNSET_REF"},
			},
		}
		reason, blocked := defaultModelCredentialBlocked(cfg)
		require.True(t, blocked)
		assert.Contains(t, reason, "T068_07_UNSET_REF")
		assert.Contains(t, reason, "openai/gpt-4o")
	})
	t.Run("not blocked when the only unresolved row is under another provider", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Provider: "openai", Model: "gpt-4o"},
			}},
			Providers: []*config.ModelConfig{
				{Provider: "openrouter", Model: "gpt-4o", APIKeyRef: "T068_07_UNSET_REF"},
			},
		}
		_, blocked := defaultModelCredentialBlocked(cfg)
		assert.False(t, blocked, "a row under a different provider never backs the pair; that is CreateProvider's not-found error to report")
	})
	t.Run("zero pair is never blocked", func(t *testing.T) {
		_, blocked := defaultModelCredentialBlocked(&config.Config{})
		assert.False(t, blocked)
	})
}
