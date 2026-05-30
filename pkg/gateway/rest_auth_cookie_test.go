//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Cookie-issuance and disk-state tests for HandleLogin and HandleRegisterAdmin.
// BDD test IDs: #70a, #70b, #70c
// Traces to: path-sandbox-and-capability-tiers-spec.md / (v4)

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// parseCookiesFromRecorder returns a map of Set-Cookie name → *http.Cookie.
func parseCookiesFromRecorder(w *httptest.ResponseRecorder) map[string]*http.Cookie {
	result := make(map[string]*http.Cookie)
	for _, c := range w.Result().Cookies() {
		result[c.Name] = c
	}
	return result
}

// loadDiskUsers reads config.json from dir and returns the users list.
func loadDiskUsers(t *testing.T, dir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	gw, ok := m["gateway"].(map[string]any)
	require.True(t, ok, "gateway key must be present")
	users, _ := gw["users"].([]any)
	var result []map[string]any
	for _, u := range users {
		um, ok := u.(map[string]any)
		if ok {
			result = append(result, um)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// #70b — HandleLogin cookie issuance
// BDD: Given a valid login request,
// When HandleLogin succeeds,
// Then Set-Cookie includes omnipus-session (HttpOnly, SameSite=Strict, Path=/,
// Max-Age=86400) and __Host-csrf (or csrf on plain HTTP).
// Disk state: Users[].SessionTokenHash is non-empty and bcrypt-validates
// against the cookie value.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestHandleLogin_IssuesSessionCookie(t *testing.T) {
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("TestPass123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "cookieuser", string(hash))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     taskstore.New(tmpDir + "/tasks"),
	}

	body := `{"username":"cookieuser","password":"TestPass123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleLogin(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	cookies := parseCookiesFromRecorder(w)

	// omnipus-session cookie must be present.
	sessionCookie, ok := cookies[middleware.SessionCookieName]
	require.True(t, ok, "Set-Cookie must include omnipus-session after login")

	// Session cookie attributes.
	assert.Equal(t, "/", sessionCookie.Path, "session cookie Path must be /")
	assert.True(t, sessionCookie.HttpOnly, "session cookie must be HttpOnly")
	assert.Equal(t, http.SameSiteStrictMode, sessionCookie.SameSite, "SameSite must be Strict")
	assert.Equal(t, middleware.SessionCookieMaxAge, sessionCookie.MaxAge, "MaxAge must be 86400")
	// Plain HTTP request: Secure must be false.
	assert.False(t, sessionCookie.Secure, "plain-HTTP login: Secure must be false")

	// CSRF cookie must also be present (either __Host-csrf or csrf).
	_, csrfTLS := cookies["__Host-csrf"]
	_, csrfHTTP := cookies["csrf"]
	assert.True(t, csrfTLS || csrfHTTP, "Set-Cookie must include CSRF cookie after login")

	// Disk state: SessionTokenHash must be set and bcrypt-validate against the cookie value.
	users := loadDiskUsers(t, tmpDir)
	require.Len(t, users, 1)
	sessionHash, _ := users[0]["session_token_hash"].(string)
	require.NotEmpty(t, sessionHash, "SessionTokenHash must be non-empty on disk after login")

	// Cookie value must bcrypt-match the stored hash.
	err = bcrypt.CompareHashAndPassword([]byte(sessionHash), []byte(sessionCookie.Value))
	assert.NoError(t, err, "disk SessionTokenHash must bcrypt-validate against the cookie value")
}

func TestHandleLogin_BothCookiesEmitted(t *testing.T) {
	// Differentiation: two different users produce different session cookie values.
	tmpDir := t.TempDir()
	hash1, _ := bcrypt.GenerateFromPassword([]byte("Pass1111"), bcrypt.DefaultCost)
	hash2, _ := bcrypt.GenerateFromPassword([]byte("Pass2222"), bcrypt.DefaultCost)

	cfgJSON := map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway": map[string]any{
			"users": []any{
				map[string]any{"username": "user1", "password_hash": string(hash1), "token_hash": "", "role": "admin"},
				map[string]any{"username": "user2", "password_hash": string(hash2), "token_hash": "", "role": "admin"},
			},
		},
	}
	data, err := json.Marshal(cfgJSON)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 8080,
			Users: []config.UserConfig{
				{Username: "user1", Role: config.UserRoleAdmin},
				{Username: "user2", Role: config.UserRoleAdmin},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     taskstore.New(tmpDir + "/tasks"),
	}

	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"username":"user1","password":"Pass1111"}`))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleLogin(w1, r1)

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"username":"user2","password":"Pass2222"}`))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleLogin(w2, r2)

	require.Equal(t, http.StatusOK, w1.Code)
	require.Equal(t, http.StatusOK, w2.Code)

	cookies1 := parseCookiesFromRecorder(w1)
	cookies2 := parseCookiesFromRecorder(w2)

	session1 := cookies1[middleware.SessionCookieName]
	session2 := cookies2[middleware.SessionCookieName]

	require.NotNil(t, session1, "user1 login must produce a session cookie")
	require.NotNil(t, session2, "user2 login must produce a session cookie")

	// Differentiation: two different logins must produce different cookie values.
	assert.NotEqual(t, session1.Value, session2.Value,
		"two different user logins must produce different session token values (not hardcoded)")
}

// ---------------------------------------------------------------------------
// #70a — HandleRegisterAdmin cookie issuance
// BDD: Given a valid register-admin request (fresh install),
// When HandleRegisterAdmin succeeds,
// Then Set-Cookie includes omnipus-session + CSRF cookie;
// disk state: SessionTokenHash is set and bcrypt-validates.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestHandleRegisterAdmin_IssuesSessionCookie(t *testing.T) {
	tmpDir := t.TempDir()

	// Write an empty config (no gateway section — fresh install).
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"),
		[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     taskstore.New(tmpDir + "/tasks"),
	}

	body := `{"username":"admin","password":"AdminPass1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register-admin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleRegisterAdmin(w, req)

	require.Equal(t, http.StatusOK, w.Code, "register-admin must return 200")

	cookies := parseCookiesFromRecorder(w)

	// omnipus-session must be issued.
	sessionCookie, ok := cookies[middleware.SessionCookieName]
	require.True(t, ok, "Set-Cookie must include omnipus-session after register-admin")

	assert.Equal(t, "/", sessionCookie.Path)
	assert.True(t, sessionCookie.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, sessionCookie.SameSite)
	assert.Equal(t, middleware.SessionCookieMaxAge, sessionCookie.MaxAge)

	// CSRF cookie must also be present.
	_, csrfTLS := cookies["__Host-csrf"]
	_, csrfHTTP := cookies["csrf"]
	assert.True(t, csrfTLS || csrfHTTP, "CSRF cookie must be present after register-admin")

	// Disk state.
	users := loadDiskUsers(t, tmpDir)
	require.Len(t, users, 1, "one admin user must be on disk")
	sessionHash, _ := users[0]["session_token_hash"].(string)
	require.NotEmpty(t, sessionHash, "session_token_hash must be non-empty on disk")

	err := bcrypt.CompareHashAndPassword([]byte(sessionHash), []byte(sessionCookie.Value))
	assert.NoError(t, err, "disk session_token_hash must bcrypt-validate against the cookie value")
}

// ---------------------------------------------------------------------------
// #70c — HandleLogout clears both cookies
// BDD: Given an authenticated user,
// When HandleLogout is called,
// Then both omnipus-session and csrf cookies are revoked (MaxAge ≤ 0).
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestHandleLogout_ClearsBothCookies(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "logoutcookie", "LogoutPass1")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = injectUser(req, "logoutcookie", config.UserRoleAdmin)
	w := httptest.NewRecorder()

	api.HandleLogout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code, "logout must return 204")

	cookies := parseCookiesFromRecorder(w)

	// omnipus-session must be revoked.
	sessionCookie, ok := cookies[middleware.SessionCookieName]
	require.True(t, ok, "Set-Cookie for omnipus-session must be present on logout")
	assert.LessOrEqual(t, sessionCookie.MaxAge, 0,
		"omnipus-session MaxAge must be ≤ 0 on logout (revoked)")

	// CSRF cookie must be revoked (either __Host-csrf or csrf).
	csrfTLS, hasTLS := cookies["__Host-csrf"]
	csrfHTTP, hasHTTP := cookies["csrf"]
	if hasTLS {
		assert.LessOrEqual(t, csrfTLS.MaxAge, 0, "__Host-csrf MaxAge must be ≤ 0 on logout")
	} else if hasHTTP {
		assert.LessOrEqual(t, csrfHTTP.MaxAge, 0, "csrf MaxAge must be ≤ 0 on logout")
	} else {
		t.Error("logout must revoke either __Host-csrf or csrf cookie (MAJ-004)")
	}
}

// TestHandleLogout_ClearsSessionTokenHashOnDisk verifies the disk-level revocation.
// Traces to: path-sandbox-and-capability-tiers-spec.md
func TestHandleLogout_ClearsSessionTokenHashOnDisk(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "disklogout", "DiskLogoutPass1")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = injectUser(req, "disklogout", config.UserRoleAdmin)
	w := httptest.NewRecorder()

	api.HandleLogout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)

	users := loadDiskUsers(t, tmpDir)
	require.NotEmpty(t, users)
	// session_token_hash must be empty (or absent via omitempty).
	sessionHash, _ := users[0]["session_token_hash"].(string)
	assert.Empty(t, sessionHash,
		"session_token_hash must be cleared on disk after logout (MAJ-004)")

	tokenHash, _ := users[0]["token_hash"].(string)
	assert.Empty(t, tokenHash,
		"token_hash must be cleared on disk after logout (FR-103)")
}
