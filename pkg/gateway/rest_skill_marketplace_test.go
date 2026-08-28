// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newTestRestAPIWithClawHub builds a restAPI whose config enables (or disables)
// the ClawHub skill marketplace and optionally sets a GitHub registry token ref.
// Used to exercise the marketplace-gating paths (GET /skills/marketplace plus the
// 409 refusal on search/install when no marketplace is enabled).
func newTestRestAPIWithClawHub(t *testing.T, clawhubEnabled bool, githubTokenRef string) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "") // disable auth in tests

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: clawhubEnabled, BaseURL: "https://clawhub.ai"},
	}
	if githubTokenRef != "" {
		cfg.Tools.Skills.Marketplaces = append(cfg.Tools.Skills.Marketplaces, config.MarketplaceConfig{
			Name: "github", Type: "github", Enabled: true, TokenRef: githubTokenRef,
		})
	}
	seedTestAgents(cfg)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
}

// TestSkillMarketplaceStatusEnabledByDefault verifies GET /skills/marketplace
// reports enabled:true when ClawHub is enabled (the production default).
// BDD: Given ClawHub is enabled,
// When GET /api/v1/skills/marketplace is called,
// Then 200 with enabled:true and a clawhub registry marked enabled.
func TestSkillMarketplaceStatusEnabledByDefault(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, true, "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/marketplace", nil)
	r.URL.Path = "/api/v1/skills/marketplace"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var status gen.SkillMarketplaceStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.True(t, status.Enabled, "marketplace must be enabled when ClawHub is enabled")
	require.Len(t, status.Registries, 1, "only the clawhub registry is reported when no github token")
	assert.Equal(t, "clawhub", status.Registries[0].Name)
	assert.True(t, status.Registries[0].Enabled)
}

// TestSkillMarketplaceStatusGithubTokenEnables verifies a configured GitHub
// registry token enables the marketplace even when ClawHub is disabled, and that
// the github registry is reported.
func TestSkillMarketplaceStatusGithubTokenEnables(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "GITHUB_TOKEN")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/marketplace", nil)
	r.URL.Path = "/api/v1/skills/marketplace"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var status gen.SkillMarketplaceStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.True(t, status.Enabled, "marketplace must be enabled via the github token")

	byName := map[string]bool{}
	for _, reg := range status.Registries {
		byName[reg.Name] = reg.Enabled
	}
	enabled, ok := byName["clawhub"]
	require.True(t, ok, "clawhub registry must always be reported")
	assert.False(t, enabled, "clawhub must report disabled")
	enabled, ok = byName["github"]
	require.True(t, ok, "github registry must be reported when a token ref is set")
	assert.True(t, enabled, "github must report enabled")
}

// TestSkillMarketplaceStatusDisabled verifies GET /skills/marketplace reports
// enabled:false when ClawHub is disabled and no GitHub token is configured.
// BDD: Given ClawHub is disabled and no GitHub token is set,
// When GET /api/v1/skills/marketplace is called,
// Then 200 with enabled:false (clawhub still listed, disabled).
func TestSkillMarketplaceStatusDisabled(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/marketplace", nil)
	r.URL.Path = "/api/v1/skills/marketplace"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var status gen.SkillMarketplaceStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.False(t, status.Enabled, "marketplace must be disabled when neither registry is enabled")
	require.Len(t, status.Registries, 1)
	assert.Equal(t, "clawhub", status.Registries[0].Name)
	assert.False(t, status.Registries[0].Enabled)
}

// TestSearchSkillsConflictWhenMarketplaceDisabled verifies that searching the
// marketplace is refused with 409 when no marketplace is enabled — before any
// registry call is attempted.
// BDD: Given no skill marketplace is enabled,
// When GET /api/v1/skills/search?q=x is called,
// Then 409 with "no skill marketplace is enabled".
func TestSearchSkillsConflictWhenMarketplaceDisabled(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web", nil)
	r.URL.Path = "/api/v1/skills/search"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "no skill marketplace is enabled")
}

// TestInstallSkillConflictWhenMarketplaceDisabled verifies that installing a
// skill by slug is refused with 409 when no marketplace is enabled — before any
// registry call is attempted.
// BDD: Given no skill marketplace is enabled,
// When POST /api/v1/skills/install with a valid slug is called,
// Then 409 with "no skill marketplace is enabled".
func TestInstallSkillConflictWhenMarketplaceDisabled(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "")

	body, err := json.Marshal(gen.SkillInstallRequest{Slug: "web-search"})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", bytes.NewReader(body))
	r.URL.Path = "/api/v1/skills/install"
	r.Header.Set("Content-Type", "application/json")
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "no skill marketplace is enabled")
}
