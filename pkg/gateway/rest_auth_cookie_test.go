// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Cookie-issuance and disk-state tests for HandleLogin.
// BDD test IDs: #70b, #70c
// Traces to: path-sandbox-and-capability-tiers-spec.md / (v4)
//
// (#70a, HandleRegisterAdmin cookie issuance, removed — single-user model,
// the register-admin endpoint was deleted along with the rest of the
// multi-account machinery.)

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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
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
				{Username: "user1"},
				{Username: "user2"},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
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
// #70c — HandleLogout clears both cookies
// BDD: Given an authenticated user,
// When HandleLogout is called,
// Then both omnipus-session and csrf cookies are revoked (MaxAge ≤ 0).
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestHandleLogout_ClearsBothCookies(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "logoutcookie", "LogoutPass1")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = injectUser(req, "logoutcookie")
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
// BDD: Given a user who logged in for real (session_token_hash is non-empty
// on disk — the precondition proving login actually wrote something),
// When that user POSTs /auth/logout,
// Then session_token_hash is present on disk as an empty string (not merely
// absent — a missing key must not be mistaken for a cleared one).
//
// qa-lead note: the earlier version of this test skipped login entirely (it
// injected the user context directly) and read against a fixture that never
// seeded session_token_hash at all. A failed map type-assertion on an absent
// key silently yields "", so the old assert.Empty passed before HandleLogout
// ever ran — deleting the production clear line did not turn this test red.
// Traces to: path-sandbox-and-capability-tiers-spec.md
func TestHandleLogout_ClearsSessionTokenHashOnDisk(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "disklogout", "DiskLogoutPass1")

	// Log in for real so a session_token_hash is actually written to disk.
	loginBody := `{"username":"disklogout","password":"DiskLogoutPass1"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code, "login must succeed: %s", loginW.Body.String())

	// Precondition: session_token_hash must be non-empty on disk after login.
	// Without this check, a broken login that never writes the hash would
	// still let the post-logout assertion pass vacuously.
	usersBefore := loadDiskUsers(t, tmpDir)
	require.NotEmpty(t, usersBefore)
	sessionHashBefore, _ := usersBefore[0]["session_token_hash"].(string)
	require.NotEmpty(t, sessionHashBefore,
		"precondition: session_token_hash must be non-empty on disk after login")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = injectUser(req, "disklogout")
	w := httptest.NewRecorder()

	api.HandleLogout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)

	usersAfter := loadDiskUsers(t, tmpDir)
	require.NotEmpty(t, usersAfter)
	// Assert the key is PRESENT with an empty value, not merely absent — a
	// failed type assertion on a missing key also yields "", which would let
	// this pass even if HandleLogout stopped writing the field entirely.
	sessionHashValue, keyPresent := usersAfter[0]["session_token_hash"]
	require.True(t, keyPresent,
		"session_token_hash key must still be present (as empty string) after logout, not simply absent")
	sessionHashAfter, isString := sessionHashValue.(string)
	require.True(t, isString, "session_token_hash must be a string")
	assert.Empty(t, sessionHashAfter,
		"session_token_hash must be cleared on disk after logout (MAJ-004)")
}

// TestRevokeUserToken_ClearsLegacyTokenHashWhenPresentedTokenVerifies covers
// revokeUserToken's legacy single-token_hash branch directly. This branch had
// ZERO test coverage anywhere in the suite before this test: grep confirmed no
// test file referenced revokeUserToken at all. Mutation-proven — deleting the
// legacy-clear block entirely left TestHandleLogout*/TestHandleLogin*/
// TestHandleChangePassword* all green (0 --- FAIL), because none of them ever
// populates a legacy token_hash in the first place; HandleLogin only ever
// writes to the tokens[] set (SEC-1/UAT #399).
func TestRevokeUserToken_ClearsLegacyTokenHashWhenPresentedTokenVerifies(t *testing.T) {
	const rawToken = "legacy-raw-token-abc123"
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(rawToken), bcrypt.DefaultCost)
	require.NoError(t, err, "test setup: bcrypt hash of the legacy raw token")

	userMap := map[string]any{
		"token_hash": string(hashBytes),
	}

	revokeUserToken(userMap, rawToken, "")

	got, ok := userMap["token_hash"].(string)
	require.True(t, ok, "token_hash key must remain present (cleared to empty, not deleted)")
	assert.Empty(t, got,
		"revokeUserToken must clear the legacy token_hash when the presented raw token "+
			"verifies against it — this is the branch rest_auth.go:807-810 implements and it had "+
			"no test anywhere in the suite before this one")
}

// TestRevokeUserToken_LeavesLegacyTokenHashWhenPresentedTokenDoesNotVerify is
// the negative control for the test above — proves the clear is conditional
// on verification, not unconditional. Without this, a mutant that always
// clears token_hash regardless of the presented token would pass the
// positive test above.
func TestRevokeUserToken_LeavesLegacyTokenHashWhenPresentedTokenDoesNotVerify(t *testing.T) {
	const rawToken = "legacy-raw-token-abc123"
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(rawToken), bcrypt.DefaultCost)
	require.NoError(t, err, "test setup: bcrypt hash of the legacy raw token")

	userMap := map[string]any{
		"token_hash": string(hashBytes),
	}

	revokeUserToken(userMap, "a-completely-different-wrong-token", "")

	got, ok := userMap["token_hash"].(string)
	require.True(t, ok)
	assert.Equal(t, string(hashBytes), got,
		"revokeUserToken must NOT clear token_hash when the presented token does not verify "+
			"against it — the clear is conditional, not unconditional")
}
