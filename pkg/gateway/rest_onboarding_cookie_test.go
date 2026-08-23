// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// FR-011 — HandleCompleteOnboarding session-cookie issuance (Wave 2,
// preview-on-main-listener).
//
// BDD (S13): Given a fresh install, When POST /api/v1/onboarding/complete
// succeeds, Then an omnipus-session cookie bound to the admin username is
// set (after user-persist) And a subsequent /api/v1/* with it authenticates
// And When IssueSessionCookie errors Then onboarding returns 500 and is not
// marked complete.
// Traces to: docs/internal/specs/preview-on-main-listener-spec.md FR-011.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// TestOnboardingIssuesSessionCookie_RoundTrip proves that a successful
// onboarding both (a) sets the omnipus-session cookie bound to the admin
// username, with the disk-persisted SessionTokenHash bcrypt-validating
// against the cookie value, and (b) that cookie subsequently authenticates a
// real /api/v1/* request through the actual checkBearerAuth cookie-fallback
// path added in Wave 2 (FR-009) — proving the two halves of the feature
// (onboarding issuance + gateway acceptance) interoperate end to end.
func TestOnboardingIssuesSessionCookie_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	require.False(t, api.onboardingMgr.IsComplete(), "onboarding should not be complete initially")

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	require.Equal(t, http.StatusOK, w.Code, "onboarding must succeed: %s", w.Body.String())

	// --- (a) the omnipus-session cookie was issued, bound to the admin username ---
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == middleware.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "Set-Cookie must include omnipus-session after onboarding completes")
	assert.True(t, sessionCookie.HttpOnly, "session cookie must be HttpOnly")
	assert.Equal(t, "/", sessionCookie.Path)
	assert.NotEmpty(t, sessionCookie.Value)

	// Disk state: SessionTokenHash must be set on the admin user and must
	// bcrypt-validate against the issued cookie's plaintext value.
	users := loadDiskUsers(t, tmpDir)
	require.Len(t, users, 1)
	assert.Equal(t, "admin", users[0]["username"])
	sessionHash, _ := users[0]["session_token_hash"].(string)
	require.NotEmpty(t, sessionHash, "SessionTokenHash must be persisted on disk after onboarding")
	require.NoError(
		t,
		bcrypt.CompareHashAndPassword([]byte(sessionHash), []byte(sessionCookie.Value)),
		"disk SessionTokenHash must bcrypt-validate against the cookie value",
	)

	// --- (b) the cookie subsequently authenticates a real /api/v1/* request ---
	// al.GetConfig() reflects the just-persisted user because safeUpdateConfigJSON
	// (used by both the admin-persist write and IssueSessionCookie) refreshes
	// the in-memory config synchronously before returning.
	liveCfg := al.GetConfig()

	authReq := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	authReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionCookie.Value})
	authW := httptest.NewRecorder()
	result := checkBearerAuth(context.Background(), authW, authReq, liveCfg)

	require.True(t, result.Authenticated, "the onboarding-issued cookie must authenticate a subsequent request")
	require.NotNil(t, result.User)
	assert.Equal(t, "admin", result.User.Username)
}

// TestOnboardingCookieFailureReturns500 proves that when session-cookie
// issuance fails (simulated here via the issueSessionCookieFn test seam —
// see rest_onboarding.go — standing in for a real-world disk fault mid-
// onboarding), HandleCompleteOnboarding:
//   - responds 500 (never 200-without-cookie),
//   - does NOT set the omnipus-session cookie on the response, and
//   - does NOT mark onboarding complete (commitOnboarding/state.json is never
//     reached — the reservation is released, so a retry is possible).
func TestOnboardingCookieFailureReturns500(t *testing.T) {
	orig := issueSessionCookieFn
	t.Cleanup(func() { issueSessionCookieFn = orig })
	issueSessionCookieFn = func(
		_ http.ResponseWriter,
		_ *http.Request,
		_ string,
		_ func(func(map[string]any) error) error,
	) (string, error) {
		return "", errors.New("simulated session cookie issuance failure")
	}

	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"onboarding must 500 when the session cookie cannot be issued — never 200-without-cookie")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["error"])

	for _, c := range w.Result().Cookies() {
		assert.NotEqual(t, middleware.SessionCookieName, c.Name,
			"no omnipus-session cookie must be set when IssueSessionCookie fails")
	}

	assert.False(t, api.onboardingMgr.IsComplete(),
		"onboarding must NOT be marked complete when session-cookie issuance fails")
}
