// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// TestSessionCookie_SurvivesManualReload reproduces the exact sequence
// tests/e2e/retention.spec.ts's session_past_retention_threshold_is_swept
// performs against a live gateway, entirely in-process:
//
//  1. Onboard an admin (issues an omnipus-session cookie, persists
//     gateway.users[0].session_token_hash to config.json via
//     safeUpdateConfigJSON — mirrors global-setup.ts's onboardViaAPI).
//  2. Read config.json's raw bytes, parse to a generic map, flip
//     gateway.dev_mode_bypass to false, write it back — mirrors the E2E
//     test's own JSON.parse/JSON.stringify round-trip at
//     tests/e2e/retention.spec.ts:277-296.
//  3. Call config.LoadConfigWithStoreAndSelfHealHook — this is the EXACT
//     function pkg/gateway/gateway.go's manualReloadChan branch (the code
//     path POST /reload drives) calls to rebuild the in-memory config.
//  4. Assert the cookie minted in step 1 still authenticates
//     (checkBearerAuth's FR-009 cookie fallback) against the config
//     produced in step 3.
//
// This isolates whether a config reload is capable of invalidating an
// already-issued browser session — the root-cause question for the
// deterministic 401 on POST /api/v1/security/retention/sweep the E2E test
// hits immediately after flipping dev_mode_bypass off and reloading.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

func TestSessionCookie_SurvivesManualReload(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	minimalCfg := []byte(
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"gateway":{"dev_mode_bypass":true}}`,
	)
	require.NoError(t, os.WriteFile(configPath, minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	// Step 1: onboard admin — issues the omnipus-session cookie and persists
	// session_token_hash to config.json.
	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"admin123"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleCompleteOnboarding(w, req)
	require.Equal(t, http.StatusOK, w.Code, "onboarding must succeed: %s", w.Body.String())

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == middleware.SessionCookieName {
			sessionCookie = c
		}
	}
	require.NotNil(t, sessionCookie, "onboarding must issue an omnipus-session cookie")
	require.NotEmpty(t, sessionCookie.Value)

	// Sanity: the freshly onboarded user resolves via the cookie against the
	// LIVE in-memory config before any reload happens.
	preReloadCfg := al.GetConfig()
	preReq := httptest.NewRequest(http.MethodPost, "/api/v1/security/retention/sweep", nil)
	preReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionCookie.Value})
	preW := httptest.NewRecorder()
	preResult := checkBearerAuth(context.Background(), preW, preReq, preReloadCfg)
	require.True(t, preResult.Authenticated, "cookie must authenticate BEFORE any reload (sanity check)")

	// Step 2: mimic retention.spec.ts's raw config.json read -> JSON.parse ->
	// flip gateway.dev_mode_bypass -> JSON.stringify -> write back.
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	gw, ok := m["gateway"].(map[string]any)
	require.True(t, ok, "gateway key must exist after onboarding persisted the admin")
	gw["dev_mode_bypass"] = false
	m["gateway"] = gw
	rewritten, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, rewritten, 0o600))

	// Step 3: this is the EXACT call gateway.go's manualReloadChan branch
	// (POST /reload) makes to rebuild the config from disk.
	newCfg, err := config.LoadConfigWithStoreAndSelfHealHook(configPath, api.credStore, nil)
	require.NoError(t, err, "manual reload's config load must succeed")
	require.False(t, newCfg.Gateway.DevModeBypass, "dev_mode_bypass must be false in the reloaded config")
	require.Len(t, newCfg.Gateway.Users, 1, "the onboarded admin must survive the reload")

	// Step 4: the cookie minted in step 1 must still authenticate against
	// the RELOADED config — otherwise every operator's active browser
	// session is silently invalidated by any config reload (hot-reload
	// poller OR the manual /reload trigger), not just this E2E test.
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/security/retention/sweep", nil)
	postReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionCookie.Value})
	postW := httptest.NewRecorder()
	postResult := checkBearerAuth(context.Background(), postW, postReq, newCfg)

	require.True(t, postResult.Authenticated,
		"the pre-reload session cookie must still authenticate against the post-reload config: body=%s",
		postW.Body.String(),
	)
	require.NotNil(t, postResult.User)
	require.Equal(t, "admin", postResult.User.Username)
}
