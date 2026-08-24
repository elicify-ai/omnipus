package gateway

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// createTestConfigWithUser writes a minimal config.json with a gateway.users array
// containing one user with the given username and bcrypt password hash.
func createTestConfigWithUser(t *testing.T, dir, username, passwordHash string) {
	cfg := map[string]any{
		"version":   1, // required for LoadConfig/LoadConfigWithStore to skip v0 migration
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway": map[string]any{
			"users": []any{
				map[string]any{
					"username":      username,
					"password_hash": passwordHash,
					"token_hash":    "",
					"role":          "admin",
				},
			},
		},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dir+"/config.json", data, 0o600))
}

// newTestRestAPIWithHome creates a restAPI with homePath and onboardingMgr wired.
// This is used for tests that exercise tasks, state, onboarding, and config mutation endpoints.
// It writes a minimal config.json into the temp dir so safeUpdateConfigJSON can read and mutate it.
func newTestRestAPIWithHomeAuth(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
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
	// Write a minimal v1 config.json so safeUpdateConfigJSON can read and atomically update it.
	// version:1 prevents LoadConfig from treating it as v0 and running migration.
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
	}
}

// --- HandleLogin tests ---

// TestHandleLogin_Success verifies that POST /api/v1/auth/login with valid credentials
// returns 200 with a non-empty token and the user's role.
// BDD: Given a user "testuser" with password "password123" in config.json,
// When POST /api/v1/auth/login {"username":"testuser","password":"password123"} is called,
// Then 200 with {"token":"<token>","role":"admin","username":"testuser"}.
func TestHandleLogin_Success(t *testing.T) {
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "testuser", string(hash))

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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	body := `{"username":"testuser","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleLogin(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"], "token must be non-empty")
	assert.Equal(t, "testuser", resp["username"])
}

// assertLoginUnauthorized is a helper that POSTs a login body and asserts
// the response is 401 with {"error":"invalid credentials"}.
func assertLoginUnauthorized(t *testing.T, api *restAPI, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleLogin(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "invalid credentials", resp["error"])
}

// newRestAPIWithSingleUser creates a minimal restAPI with one configured user.
func newRestAPIWithSingleUser(t *testing.T, username, passwordHash string) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	createTestConfigWithUser(t, tmpDir, username, passwordHash)
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
	return &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}
}

// TestHandleLogin_WrongPassword verifies that POST /api/v1/auth/login with a valid
// username but wrong password returns 401 Unauthorized.
// BDD: Given a user "testuser" with password "correctpassword" in config.json,
// When POST /api/v1/auth/login {"username":"testuser","password":"wrongpassword"} is called,
// Then 401 with {"error":"invalid credentials"}.
func TestHandleLogin_WrongPassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.DefaultCost)
	require.NoError(t, err)
	api := newRestAPIWithSingleUser(t, "testuser", string(hash))
	assertLoginUnauthorized(t, api, `{"username":"testuser","password":"wrongpassword"}`)
}

// TestHandleLogin_UserNotFound verifies that POST /api/v1/auth/login with a
// non-existent username returns 401 Unauthorized.
// BDD: Given no user named "ghost" exists in config.json,
// When POST /api/v1/auth/login {"username":"ghost","password":"anypassword"} is called,
// Then 401 with {"error":"invalid credentials"}.
func TestHandleLogin_UserNotFound(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("somepassword"), bcrypt.DefaultCost)
	require.NoError(t, err)
	// Create config with "realuser" but the request uses "ghost"
	api := newRestAPIWithSingleUser(t, "realuser", string(hash))
	assertLoginUnauthorized(t, api, `{"username":"ghost","password":"anypassword"}`)
}

// TestHandleLogin_EmptyUsername verifies that POST /api/v1/auth/login with empty
// username returns 400 Bad Request.
// BDD: Given an empty username in the request body,
// When POST /api/v1/auth/login {"username":"","password":"password123"} is called,
// Then 400 with {"error":"username and password are required"}.
func TestHandleLogin_EmptyUsername(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	body := `{"username":"","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleLogin(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "username and password are required", resp["error"])
}

// TestHandleLogin_EmptyPassword verifies that POST /api/v1/auth/login with empty
// password returns 400 Bad Request.
// BDD: Given an empty password in the request body,
// When POST /api/v1/auth/login {"username":"testuser","password":""} is called,
// Then 400 with {"error":"username and password are required"}.
func TestHandleLogin_EmptyPassword(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	body := `{"username":"testuser","password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleLogin(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "username and password are required", resp["error"])
}

// TestHandleLogin_MethodNotAllowed verifies that GET /api/v1/auth/login returns 405.
// BDD: Given a GET request to /auth/login,
// When the request is processed,
// Then 405 Method Not Allowed is returned.
func TestHandleLogin_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
	w := httptest.NewRecorder()

	api.HandleLogin(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleLogin_DifferentInputProducesDifferentToken verifies that two different
// successful logins produce two different tokens (not hardcoded).
// BDD: Given two valid user accounts,
// When each logs in with their own credentials,
// Then each receives a different token.
func TestHandleLogin_DifferentInputProducesDifferentToken(t *testing.T) {
	tmpDir := t.TempDir()

	hash1, err := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.DefaultCost)
	require.NoError(t, err)
	hash2, err := bcrypt.GenerateFromPassword([]byte("password2"), bcrypt.DefaultCost)
	require.NoError(t, err)

	cfg := map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway": map[string]any{
			"users": []any{
				map[string]any{
					"username":      "user1",
					"password_hash": string(hash1),
					"token_hash":    "",
					"role":          "user",
				},
				map[string]any{
					"username":      "user2",
					"password_hash": string(hash2),
					"token_hash":    "",
					"role":          "admin",
				},
			},
		},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	configObj := &config.Config{
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
	al := mustAgentLoop(t, configObj, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	// Login as user1
	body1 := `{"username":"user1","password":"password1"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	api.HandleLogin(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)
	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	token1, ok1 := resp1["token"].(string)
	require.True(t, ok1, "login response token must be a string")

	// Login as user2
	body2 := `{"username":"user2","password":"password2"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	api.HandleLogin(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	token2, ok2 := resp2["token"].(string)
	require.True(t, ok2, "login response token must be a string")

	assert.NotEqual(t, token1, token2, "two different logins must produce different tokens")
}

// TestHandleLogin_ConcurrentRequests verifies that concurrent login requests for
// the same user all succeed (rate limiting allows multiple attempts from same IP
// within the time window).
// BDD: Given multiple concurrent POST /api/v1/auth/login requests for the same user,
// When all are handled simultaneously,
// Then each receives 200 with a (potentially different) token.
func TestHandleLogin_ConcurrentRequests(t *testing.T) {
	// Reset global rate limiter to avoid cross-test pollution.
	globalLoginLimiter = newLoginRateLimiter()
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "testuser", string(hash))

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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := `{"username":"testuser","password":"password123"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			api.HandleLogin(w, req)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		assert.Equal(t, http.StatusOK, code, "concurrent POST /auth/login[%d] must return 200", i)
	}
}

// --- HandleValidateToken tests ---

// TestHandleValidateToken_ValidToken verifies that GET /api/v1/auth/validate
// with a valid bearer token returns 200 with username and role.
// BDD: Given a valid bearer token for user "testuser",
// When GET /api/v1/auth/validate is called with Authorization: Bearer <token>,
// Then 200 with {"username":"testuser","role":"admin"}.
func TestHandleValidateToken_ValidToken(t *testing.T) {
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "testuser", string(hash))

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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	// Step 1: Login to get a token
	body := `{"username":"testuser","password":"password123"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code)
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	token, tokenOk := loginResp["token"].(string)
	require.True(t, tokenOk, "login response token must be a string")

	// After login, the token hash is written to disk but the in-memory config
	// doesn't update (no reload support in test). Read the updated config from
	// disk and inject the user context manually, simulating what withAuth does
	// in production after a successful reload.
	diskData, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(diskData, &diskCfg))
	gwMap, gwOk := diskCfg["gateway"].(map[string]any)
	require.True(t, gwOk, "config.gateway must be an object")
	users, usersOk := gwMap["users"].([]any)
	require.True(t, usersOk, "config.gateway.users must be an array")
	require.Len(t, users, 1)
	userMap, userMapOk := users[0].(map[string]any)
	require.True(t, userMapOk, "config.gateway.users[0] must be an object")
	// SEC-1: login writes the token into the bearer-token SET; the entry hash
	// is bcrypt of the secret BODY (config.TokenSecret), not the full token.
	tokens, ok := userMap["tokens"].([]any)
	require.True(t, ok, "tokens set should be present after login")
	require.Len(t, tokens, 1)
	entry, entryOk := tokens[0].(map[string]any)
	require.True(t, entryOk, "tokens[0] must be an object")
	tokenHash, _ := entry["hash"].(string)
	require.NotEmpty(t, tokenHash, "token entry hash should be set after login")

	// Verify the token matches the stored hash (sanity check).
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(tokenHash), []byte(config.TokenSecret(token))))

	// Step 2: Validate the token by injecting user context (as withAuth would
	// after a successful config reload).
	testUser := &config.UserConfig{
		Username: "testuser",
	}
	validateReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	validateReq.Header.Set("Authorization", "Bearer "+token)
	ctx := context.WithValue(validateReq.Context(), UserContextKey{}, testUser)
	validateReq = validateReq.WithContext(ctx)
	validateW := httptest.NewRecorder()

	api.HandleValidateToken(validateW, validateReq)

	assert.Equal(t, http.StatusOK, validateW.Code)
	var validateResp map[string]any
	require.NoError(t, json.Unmarshal(validateW.Body.Bytes(), &validateResp))
	assert.Equal(t, "testuser", validateResp["username"])
}

// TestHandleValidateToken_InvalidToken verifies that GET /api/v1/auth/validate
// with an invalid bearer token returns 401.
// BDD: Given an invalid bearer token "garbage-token",
// When GET /api/v1/auth/validate is called,
// Then 401 with {"error":"invalid token"}.
func TestHandleValidateToken_InvalidToken(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	req.Header.Set("Authorization", "Bearer garbage-token-does-not-exist")
	w := httptest.NewRecorder()

	api.HandleValidateToken(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "invalid token", resp["error"])
}

// TestHandleValidateToken_MissingToken verifies that GET /api/v1/auth/validate
// without an Authorization header returns 401.
// BDD: Given no Authorization header,
// When GET /api/v1/auth/validate is called,
// Then 401 with {"error":"invalid token"}.
func TestHandleValidateToken_MissingToken(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	w := httptest.NewRecorder()

	api.HandleValidateToken(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandleValidateToken_MethodNotAllowed verifies that POST /api/v1/auth/validate
// returns 405.
// BDD: Given a POST request to /auth/validate,
// When the request is processed,
// Then 405 Method Not Allowed is returned.
func TestHandleValidateToken_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/validate", nil)
	w := httptest.NewRecorder()

	api.HandleValidateToken(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleLogin_RateLimitBlocksAtLimit verifies that after 5 failed login attempts
// from the same IP+username, the 6th attempt is rejected with 429 Too Many Requests.
// BDD: Given 5 failed login attempts for "rateuser" from the same IP,
// When a 6th login attempt is made,
// Then 429 Too Many Requests is returned.
func TestHandleLogin_RateLimitBlocksAtLimit(t *testing.T) {
	// Reset global rate limiter to avoid cross-test pollution.
	globalLoginLimiter = newLoginRateLimiter()
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "rateuser", string(hash))

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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	// Use a unique username to avoid colliding with other tests' rate limit state.
	// The rate limiter tracks (IP+username), so each test gets its own slot.
	body := `{"username":"rateuser","password":"wrongpassword"}`

	// First 5 attempts should all return 401 (wrong password), not rate limited.
	// All attempts come from the same IP so the rate limiter accumulates failures.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()
		api.HandleLogin(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "attempt %d should return 401", i+1)
	}

	// 6th attempt from same IP+username should be rate limited.
	// Note: this test requires the ability to reset globalLoginLimiter between test runs
	// to be fully reliable. In CI, rate limit state persists across tests.
	// Skipping in short mode to avoid flakiness.
	if testing.Short() {
		t.Skip("skipping rate limit test in short mode")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()
	api.HandleLogin(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "6th attempt should be rate limited")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "too many login attempts")
}

// TestHandleLogin_SpoofedXFFDoesNotBypassLoginRateLimit is the direct
// regression test for the vulnerability the clientIP/TrustXFF fix closes:
// with gateway.trust_xff at its default (false), an attacker cannot defeat
// globalLoginLimiter's brute-force protection by sending a different spoofed
// X-Forwarded-For value on every request. Unlike TestHandleLogin_RateLimitBlocksAtLimit
// (which never sets X-Forwarded-For at all, so it wouldn't catch a
// regression that reintroduced XFF-trusting logic inside HandleLogin
// specifically), every attempt here comes from the SAME real RemoteAddr but
// a DIFFERENT spoofed XFF value — the 6th attempt must still be rate
// limited, proving clientIP resolves the real RemoteAddr for the actual
// HandleLogin call path, not just in isolation.
func TestHandleLogin_SpoofedXFFDoesNotBypassLoginRateLimit(t *testing.T) {
	globalLoginLimiter = newLoginRateLimiter()
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "xffuser", string(hash))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080}, // TrustXFF left at its zero-value default: false
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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	body := `{"username":"xffuser","password":"wrongpassword"}`

	// First 5 attempts: same real RemoteAddr, a DIFFERENT spoofed
	// X-Forwarded-For on every request (simulating the exact attack this
	// fix closes). All 5 return 401 (wrong password), not yet rate limited.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i+1))
		req.RemoteAddr = "192.168.1.200:12345"
		w := httptest.NewRecorder()
		api.HandleLogin(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "attempt %d should return 401", i+1)
	}

	if testing.Short() {
		t.Skip("skipping rate limit test in short mode")
	}

	// 6th attempt, yet another distinct spoofed XFF value: if clientIP were
	// still trusting X-Forwarded-For unconditionally, this would land in a
	// brand-new rate-limit bucket and return 401 (wrong password) instead of
	// 429 — the exact bypass this fix closes. It must be rate limited.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "10.0.0.99")
	req.RemoteAddr = "192.168.1.200:12345"
	w := httptest.NewRecorder()
	api.HandleLogin(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code,
		"6th attempt with a fresh spoofed X-Forwarded-For must still be rate limited (default trust_xff=false)")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "too many login attempts")
}

// TestHandleLogin_DevModeBypass_DenyByDefault verifies that when no users are configured
// and no OMNIPUS_BEARER_TOKEN env var is set, requests are rejected with 401 (deny-by-default).
// BDD: Given no users in config.json and no OMNIPUS_BEARER_TOKEN env var set,
// When an unauthenticated request is made,
// Then 401 Unauthorized is returned (not admin access).
func TestHandleLogin_DevModeBypass_DenyByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	// Write config with empty users array.
	cfg := map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway": map[string]any{
			"users": []any{},
		},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	testCfg := &config.Config{
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
	al := mustAgentLoop(t, testCfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	// Attempt login with no users configured.
	body := `{"username":"anyuser","password":"anypassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleLogin(w, req)

	// Should return 401, not admin access.
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "invalid credentials", resp["error"])
}

// --- HandleLogout tests ---

// newTestRestAPIWithUserAndDir creates a restAPI backed by tmpDir with a single user
// already written to config.json. Returns the api and tmpDir.
// Used by logout and change-password tests where the caller needs to control tmpDir.
func newTestRestAPIWithUser(t *testing.T, username, password string) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, username, string(hash))

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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}
	return api, tmpDir
}

// injectUser returns a copy of r with a UserContextKey injected into the context,
// simulating what withAuth middleware does after a successful token validation.
func injectUser(r *http.Request, username string) *http.Request {
	user := &config.UserConfig{Username: username}
	ctx := context.WithValue(r.Context(), UserContextKey{}, user)
	return r.WithContext(ctx)
}

// TestHandleLogout_Success verifies that POST /api/v1/auth/logout with a valid
// authenticated session returns 204 No Content and revokes the presented
// bearer token from the on-disk tokens[] set.
// BDD: Given a user who logged in for real (a bearer token is live in
// tokens[] on disk — the precondition proving login actually wrote
// something),
// When that user POSTs /auth/logout presenting that same bearer token,
// Then 204 No Content is returned and tokens[] no longer contains it.
//
// qa-lead note: the earlier version of this test never logged in — it
// injected the user context directly on top of a fixture seeded with
// token_hash:"" and no "tokens" key at all — then asserted
// userMap["token_hash"] == "". SEC-1/UAT #399 moved bearer tokens to the
// tokens[] SET (see the comment on TestHandleLogout_RevokesOnlyPresentedToken
// below); login never touches the legacy token_hash field, so that assertion
// was true before HandleLogout ever ran and could not have caught a
// regression. Fixed by logging in for real and asserting on tokens[], the
// field the code actually writes — mirrors
// TestHandleLogout_RevokesOnlyPresentedToken's approach for the single-token
// case.
// Traces to: Milestone 1 — HandleLogout implementation (pkg/gateway/rest_auth.go)
func TestHandleLogout_Success(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "logoutuser", "password123")

	// Log in for real so a bearer token is actually appended to tokens[] on disk.
	loginBody := `{"username":"logoutuser","password":"password123"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code, "login must succeed: %s", loginW.Body.String())
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	token, tokenOk := loginResp["token"].(string)
	require.True(t, tokenOk, "login response token must be a string")
	require.NotEmpty(t, token, "login must return a non-empty bearer token")

	// Precondition: tokens[] must be non-empty on disk after login. Without
	// this check, a login that never wrote the token would still let the
	// post-logout "tokens[] is empty" assertion pass vacuously.
	readUsers := func() []any {
		t.Helper()
		diskData, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
		require.NoError(t, err)
		var diskCfg map[string]any
		require.NoError(t, json.Unmarshal(diskData, &diskCfg))
		gwMap, ok := diskCfg["gateway"].(map[string]any)
		require.True(t, ok, "gateway config must be present on disk")
		users, ok := gwMap["users"].([]any)
		require.True(t, ok, "users must be present on disk")
		require.Len(t, users, 1)
		return users
	}
	userMapBefore, ok := readUsers()[0].(map[string]any)
	require.True(t, ok)
	tokensBefore, ok := userMapBefore["tokens"].([]any)
	require.True(t, ok, "tokens set must be present on disk after login")
	require.NotEmpty(t, tokensBefore, "precondition: tokens[] must be non-empty on disk after login")

	// POST /auth/logout presenting the real bearer token, with the user
	// injected into context (simulates withAuth middleware after validating
	// that same token).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = injectUser(req, "logoutuser")
	w := httptest.NewRecorder()

	api.HandleLogout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code, "logout must return 204 No Content")

	// Verify persistence: the presented token is removed from tokens[] on disk.
	userMapAfter, ok := readUsers()[0].(map[string]any)
	require.True(t, ok)
	tokensAfter, ok := userMapAfter["tokens"].([]any)
	require.True(t, ok, "tokens key must still be present (as an empty array) after logout")
	assert.Empty(t, tokensAfter, "the logged-out token must be removed from tokens[] after logout")
}

// TestHandleLogout_DevBypassUser_ClearsCookiesNo500 proves that a logout by the
// synthetic dev_mode_bypass identity ("_dev_bypass", which checkBearerAuth
// injects when gateway.dev_mode_bypass=true and no Bearer header is present)
// clears the browser cookies and returns 204 — instead of 500 "user not found"
// (the identity has no Gateway.Users row). This mirrors the CLI-token
// synthetic-identity path and is what makes the SPA's cookie-only UI logout
// clear the session cookie under the e2e harness (which always runs with
// dev_mode_bypass=true). Regression guard for ADR-044 FR-020 / auth.spec.ts (d).
func TestHandleLogout_DevBypassUser_ClearsCookiesNo500(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "realadmin", "password123")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	// Inject the synthetic bypass identity — deliberately NOT a Gateway.Users row.
	req = injectUser(req, "_dev_bypass")
	w := httptest.NewRecorder()

	api.HandleLogout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code,
		"logout by the synthetic _dev_bypass identity must return 204, not 500")

	// Both browser cookies must be expired (cleared value), so the browser drops
	// them from the jar — the exact behavior auth.spec.ts (d) asserts.
	var sawSessionCleared, sawCSRFCleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "omnipus-session" && c.Value == "" {
			sawSessionCleared = true
		}
		if (c.Name == "__Host-csrf" || c.Name == "csrf") && c.Value == "" {
			sawCSRFCleared = true
		}
	}
	assert.True(t, sawSessionCleared, "omnipus-session cookie must be cleared on bypass-user logout")
	assert.True(t, sawCSRFCleared, "CSRF cookie must be cleared on bypass-user logout")
}

// TestHandleLogout_TokenNoLongerValid verifies that after logout the token cannot
// be used to authenticate: HandleValidateToken returns 401 when no user is in context.
// BDD: Given a logged-out user (context has no UserContextKey),
// When GET /api/v1/auth/validate is called,
// Then 401 Unauthorized is returned.
// Traces to: Milestone 1 — HandleLogout implementation (pkg/gateway/rest_auth.go)
func TestHandleLogout_TokenNoLongerValid(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "logoutuser2", "password123")

	// Simulate post-logout state: no user in context (token_hash is empty, withAuth
	// would not inject a user for a token that doesn't match any hash).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	req.Header.Set("Authorization", "Bearer stale-token-after-logout")
	// No user injected in context — as withAuth would behave with invalid token.
	w := httptest.NewRecorder()

	api.HandleValidateToken(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"validate must return 401 when no user is in context (post-logout state)")
}

// TestHandleLogout_RevokesOnlyPresentedToken proves the SEC-1 / UAT #399 fix:
// logout removes ONLY the caller's presented bearer token from the set; tokens
// for other concurrent sessions stay valid.
//
// BDD: Given a user who logged in twice (two live tokens in the set),
// When that user POSTs /auth/logout presenting token-1,
// Then token-1 is removed from the on-disk set,
// And token-2 still verifies.
func TestHandleLogout_RevokesOnlyPresentedToken(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "multiuser", "multi-pass-123")

	login := func() string {
		t.Helper()
		body := `{"username":"multiuser","password":"multi-pass-123"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.HandleLogin(w, req)
		require.Equal(t, http.StatusOK, w.Code, "login must succeed: %s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tok, ok := resp["token"].(string)
		require.True(t, ok, "login response token must be a string")
		return tok
	}

	tok1 := login()
	tok2 := login()

	// Logout presenting tok1.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tok1)
	req = injectUser(req, "multiuser")
	w := httptest.NewRecorder()
	api.HandleLogout(w, req)
	require.Equal(t, http.StatusNoContent, w.Code, "logout must return 204")

	// Read the on-disk token set.
	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	gw, gwOk := diskCfg["gateway"].(map[string]any)
	require.True(t, gwOk, "config.gateway must be an object")
	users, usersOk := gw["users"].([]any)
	require.True(t, usersOk, "config.gateway.users must be an array")
	require.Len(t, users, 1)
	userMap, userMapOk := users[0].(map[string]any)
	require.True(t, userMapOk, "config.gateway.users[0] must be an object")
	tokens, ok := userMap["tokens"].([]any)
	require.True(t, ok, "tokens set must still exist after single-token logout")
	require.Len(t, tokens, 1, "exactly ONE token must remain after logging out one of two sessions")

	verifyAgainstSet := func(plain string) bool {
		for _, e := range tokens {
			entry, entryOk := e.(map[string]any)
			if !entryOk {
				continue
			}
			h, _ := entry["hash"].(string)
			if bcrypt.CompareHashAndPassword([]byte(h), []byte(config.TokenSecret(plain))) == nil {
				return true
			}
		}
		return false
	}
	assert.False(t, verifyAgainstSet(tok1), "the logged-out token (tok1) must be revoked")
	assert.True(t, verifyAgainstSet(tok2), "the other session's token (tok2) must remain valid")
}

// TestHandleLogout_Unauthenticated verifies that POST /api/v1/auth/logout without
// an authenticated user in context returns 401 Unauthorized.
// BDD: Given no user is in the request context,
// When POST /api/v1/auth/logout is called,
// Then 401 Unauthorized is returned.
// Traces to: Milestone 1 — HandleLogout implementation (pkg/gateway/rest_auth.go)
func TestHandleLogout_Unauthenticated(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "logoutuser3", "password123")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	// No user injected — simulates missing/invalid Bearer token.
	w := httptest.NewRecorder()

	api.HandleLogout(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "not authenticated", resp["error"])
}

// TestHandleLogout_MethodNotAllowed verifies that GET /api/v1/auth/logout returns 405.
// BDD: Given a GET request to /auth/logout,
// When the request is processed,
// Then 405 Method Not Allowed is returned.
// Traces to: Milestone 1 — HandleLogout implementation (pkg/gateway/rest_auth.go)
func TestHandleLogout_MethodNotAllowed(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "logoutuser4", "password123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/logout", nil)
	req = injectUser(req, "logoutuser4")
	w := httptest.NewRecorder()

	api.HandleLogout(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleLogout_CLITokenAuth_ReturnsNoContent proves the CLI-token logout
// regression fix. Commit 17bfff82 moved the CLI's machine bearer token out
// of Gateway.Users (where it lived as a role-less "cli" row) into the
// dedicated Gateway.CLIToken field, but HandleLogout still looked the
// caller up by username in the Gateway.Users JSON array — a CLI-token
// caller has no such row (a fresh CLI-only install can have ZERO
// Gateway.Users entries), so the lookup fell through to
// `fmt.Errorf("user not found in config")` and the handler responded 500.
//
// This drives the FULL real auth path — checkBearerAuth's Gateway.CLIToken
// branch via api.withAuth, not a hand-injected context — so it exercises
// exactly what a real CLI `omnipus ... logout` call does. Pre-fix (verified
// by temporarily reverting HandleLogout's CLITokenContextKey short-circuit),
// this test fails: safeUpdateConfigJSON runs against the on-disk config
// (whose gateway.users array is empty, matching a realistic CLI-only
// install) and returns the "user not found in config" error, producing a
// 500 instead of the expected 204.
func TestHandleLogout_CLITokenAuth_ReturnsNoContent(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	plainToken := "omnipus_" + strings.Repeat("d", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(plainToken), bcrypt.MinCost)
	require.NoError(t, err)

	// On-disk config mirrors a realistic CLI-only install: a CLI token but
	// ZERO Gateway.Users entries — there is no "cli" row to relocate
	// because this install never had one (fresh install, not a migrated
	// legacy config with a role-less "cli" user row).
	onDisk := map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway": map[string]any{
			"users":     []any{},
			"cli_token": map[string]any{"id": "", "hash": string(hash)},
		},
	}
	data, err := json.Marshal(onDisk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host:     "127.0.0.1",
			Port:     8080,
			CLIToken: &config.TokenEntry{Hash: config.BcryptHash(hash)},
		},
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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+plainToken)
	w := httptest.NewRecorder()

	api.withAuth(api.HandleLogout)(w, req)

	require.Equal(t, http.StatusNoContent, w.Code,
		"a CLI-token-authenticated logout must return 204, not 500 (body: %s)", w.Body.String())
}

// --- HandleChangePassword tests ---

// TestHandleChangePassword_Success verifies that POST /api/v1/auth/change-password
// with correct current_password and a valid new_password returns 200 {"success":true}
// and persists the new password hash to disk.
// BDD: Given authenticated user "cpuser" with password "OldPass123",
// When POST /auth/change-password {"current_password":"OldPass123","new_password":"NewPass456"} is called,
// Then 200 {"success":true} and config.json has an updated password_hash.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_Success(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "cpuser", "OldPass123")

	body := `{"current_password":"OldPass123","new_password":"NewPass456"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectUser(req, "cpuser")
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"], "change-password must return {success:true}")

	// Verify persistence: new password_hash matches "NewPass456" on disk.
	diskData, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(diskData, &diskCfg))
	gwMap, gwOk := diskCfg["gateway"].(map[string]any)
	require.True(t, gwOk, "config.gateway must be an object")
	users, usersOk := gwMap["users"].([]any)
	require.True(t, usersOk, "config.gateway.users must be an array")
	require.Len(t, users, 1)
	userMap, userMapOk := users[0].(map[string]any)
	require.True(t, userMapOk, "config.gateway.users[0] must be an object")
	newHash, _ := userMap["password_hash"].(string)
	require.NotEmpty(t, newHash, "password_hash must be updated on disk")
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(newHash), []byte("NewPass456")),
		"new password_hash must match the new password")
	// Differentiation: old password must NOT match the new hash.
	require.Error(t, bcrypt.CompareHashAndPassword([]byte(newHash), []byte("OldPass123")),
		"old password must not match the new hash — proves the hash was actually changed")
}

// TestHandleChangePassword_NewPasswordEnablesLogin verifies that after a password change
// the user can log in with the new password (end-to-end differentiation test).
// BDD: Given a user "cpuser2" whose password was changed from "OldPass999" to "NewPass999",
// When POST /auth/login with the new password is called,
// Then 200 OK with a token is returned.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_NewPasswordEnablesLogin(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser2", "OldPass999")

	// Change password.
	cpBody := `{"current_password":"OldPass999","new_password":"NewPass999"}`
	cpReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(cpBody))
	cpReq.Header.Set("Content-Type", "application/json")
	cpReq = injectUser(cpReq, "cpuser2")
	cpW := httptest.NewRecorder()
	api.HandleChangePassword(cpW, cpReq)
	require.Equal(t, http.StatusOK, cpW.Code, "change-password must succeed")

	// Login with new password — should succeed.
	loginBody := `{"username":"cpuser2","password":"NewPass999"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code, "login with new password must succeed")
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	assert.NotEmpty(t, loginResp["token"], "new login must return a non-empty token")
}

// TestHandleChangePassword_OldPasswordRejectedAfterChange verifies that after a
// password change, the old password can no longer be used to log in.
// BDD: Given a user "cpuser3" who changed password from "OldPassXXX" to "NewPassXXX",
// When POST /auth/login with the OLD password is called,
// Then 401 Unauthorized is returned.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_OldPasswordRejectedAfterChange(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser3", "OldPassXXX")

	// Change password.
	cpBody := `{"current_password":"OldPassXXX","new_password":"NewPassXXX"}`
	cpReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(cpBody))
	cpReq.Header.Set("Content-Type", "application/json")
	cpReq = injectUser(cpReq, "cpuser3")
	cpW := httptest.NewRecorder()
	api.HandleChangePassword(cpW, cpReq)
	require.Equal(t, http.StatusOK, cpW.Code)

	// Login with old password — must fail.
	loginBody := `{"username":"cpuser3","password":"OldPassXXX"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	assert.Equal(t, http.StatusUnauthorized, loginW.Code,
		"old password must be rejected after a successful change")
}

// TestHandleChangePassword_WrongCurrentPassword verifies that providing an incorrect
// current_password returns 401 Unauthorized.
// BDD: Given authenticated user "cpuser4",
// When POST /auth/change-password {"current_password":"WrongPassword","new_password":"NewPass456"} is called,
// Then 401 with {"error":"current password is incorrect"}.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_WrongCurrentPassword(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser4", "RealPass123")

	body := `{"current_password":"WrongPassword","new_password":"NewPass456"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectUser(req, "cpuser4")
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "current password is incorrect", resp["error"])
}

// TestHandleChangePassword_TooShort verifies that a new_password shorter than 8
// characters returns 400 Bad Request.
// BDD: Given authenticated user "cpuser5",
// When POST /auth/change-password {"current_password":"RealPass","new_password":"short"} is called,
// Then 400 with {"error":"new password must be at least 8 characters"}.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_TooShort(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser5", "RealPass")

	// Boundary: new_password is 5 chars — below the 8-char minimum.
	body := `{"current_password":"RealPass","new_password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectUser(req, "cpuser5")
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "new password must be at least 8 characters", resp["error"])
}

// TestHandleChangePassword_ExactlyEightChars verifies that a new_password of exactly
// 8 characters is accepted (boundary: min valid length).
// BDD: Given authenticated user "cpuser6",
// When POST /auth/change-password with new_password of exactly 8 chars is called,
// Then 200 {"success":true}.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_ExactlyEightChars(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser6", "OldPass8")

	// Boundary: new_password is exactly 8 chars — minimum valid length.
	body := `{"current_password":"OldPass8","new_password":"12345678"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectUser(req, "cpuser6")
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"new_password of exactly 8 chars must be accepted")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
}

// TestHandleChangePassword_MissingFields verifies that POST /auth/change-password
// with an empty body returns 400 Bad Request.
// BDD: Given an empty request body,
// When POST /auth/change-password {} is called,
// Then 400 with {"error":"current_password and new_password are required"}.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_MissingFields(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser7", "AnyPass123")

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectUser(req, "cpuser7")
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "current_password and new_password are required", resp["error"])
}

// TestHandleChangePassword_MethodNotAllowed verifies that GET /auth/change-password returns 405.
// BDD: Given a GET request to /auth/change-password,
// When the request is processed,
// Then 405 Method Not Allowed is returned.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_MethodNotAllowed(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser8", "AnyPass123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/change-password", nil)
	req = injectUser(req, "cpuser8")
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleChangePassword_Unauthenticated verifies that POST /auth/change-password
// without a user in context returns 401 Unauthorized.
// BDD: Given no user in request context,
// When POST /auth/change-password is called,
// Then 401 Unauthorized is returned.
// Traces to: Milestone 1 — HandleChangePassword implementation (pkg/gateway/rest_auth.go)
func TestHandleChangePassword_Unauthenticated(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser9", "AnyPass123")

	body := `{"current_password":"AnyPass123","new_password":"NewPass456"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No user injected in context.
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Token entropy and hash-storage regression guards ---

// TestGenerateUserToken_EntropyAndFormat verifies the canonical bearer token
// format (omnipus_<8 hex id>_<64 hex body>) and entropy properties (SEC-1).
//
// Four properties are asserted:
//  1. Format: every token matches ^omnipus_[0-9a-f]{8}_[0-9a-f]{64}$.
//  2. ID: the embedded non-secret ID prefix is recoverable (8 hex chars).
//  3. Byte length: the secret body decodes to exactly 32 bytes (256-bit).
//  4. Uniqueness: 100 invocations produce 100 distinct tokens (no collision
//     from a broken RNG, hardcoded seed, or constant return value).
func TestGenerateUserToken_EntropyAndFormat(t *testing.T) {
	const invocations = 100

	seen := make(map[string]struct{}, invocations)
	for i := 0; i < invocations; i++ {
		tok, err := generateUserToken("")
		require.NoError(t, err, "generateUserToken must not error (invocation %d)", i)

		// SEC-1: token now carries a non-secret ID prefix:
		// omnipus_<8 hex id>_<64 hex body>.
		assert.Regexp(t, `^omnipus_[0-9a-f]{8}_[0-9a-f]{64}$`, tok,
			"token must match omnipus_<id>_<body> format (invocation %d)", i)

		// The embedded ID must be recoverable and 8 hex chars (32-bit).
		id := config.TokenIDFromRaw(tok)
		assert.Regexp(t, `^[0-9a-f]{8}$`, id,
			"config.TokenIDFromRaw must recover the 8-hex-char ID (invocation %d)", i)

		// The secret body must decode to exactly 32 bytes (256-bit entropy).
		bodyHex := strings.TrimPrefix(tok, "omnipus_"+id+"_")
		decoded, decErr := hex.DecodeString(bodyHex)
		require.NoError(t, decErr, "body portion must be valid hex (invocation %d)", i)
		assert.Len(t, decoded, 32,
			"body portion must decode to exactly 32 bytes (invocation %d)", i)

		seen[tok] = struct{}{}
	}

	assert.Len(t, seen, invocations,
		"all %d token invocations must be distinct — collisions indicate broken entropy", invocations)
}

// TestHandleLogin_StoresBcryptedTokenHash verifies that HandleLogin writes a
// bcrypt hash of the plaintext token to disk, and that the plaintext token
// string never appears in config.json.
//
// BDD: Given a user "hashcheckuser" with a known password,
// When POST /api/v1/auth/login succeeds,
// Then config.json contains token_hash = bcrypt(plaintext token),
// And the plaintext token string is NOT present anywhere in config.json.
func TestHandleLogin_StoresBcryptedTokenHash(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "hashcheckuser", "hash-check-pw-123")

	loginBody := `{"username":"hashcheckuser","password":"hash-check-pw-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleLogin(w, req)

	require.Equal(t, http.StatusOK, w.Code, "login must succeed: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	plaintextToken, ok := resp["token"].(string)
	require.True(t, ok && plaintextToken != "", "login response must contain a non-empty token")

	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)

	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	gw, gwOk := diskCfg["gateway"].(map[string]any)
	require.True(t, gwOk, "config.gateway must be an object")
	usersRaw, usersRawOk := gw["users"].([]any)
	require.True(t, usersRawOk, "config.gateway.users must be an array")
	require.Len(t, usersRaw, 1)
	userMap, userMapOk := usersRaw[0].(map[string]any)
	require.True(t, userMapOk, "config.gateway.users[0] must be an object")

	// SEC-1: login now writes the token into the bearer-token SET ("tokens"),
	// not the legacy single token_hash field.
	tokensRaw, ok := userMap["tokens"].([]any)
	require.True(t, ok, "tokens set must be written to disk after login")
	require.Len(t, tokensRaw, 1, "first login must create exactly one token entry")
	entry, entryOk := tokensRaw[0].(map[string]any)
	require.True(t, entryOk, "tokensRaw[0] must be an object")
	tokenHash, _ := entry["hash"].(string)
	require.NotEmpty(t, tokenHash, "token entry hash must be written to disk after login")

	require.NoError(t,
		bcrypt.CompareHashAndPassword([]byte(tokenHash), []byte(config.TokenSecret(plaintextToken))),
		"stored token hash must be bcrypt of the token's secret body (SEC-1: ID prefix excluded)")

	assert.False(t, strings.Contains(string(raw), plaintextToken),
		"plaintext token must NOT appear anywhere in config.json — hash-only storage required")
}

// TestHandleLogin_AppendsTokenSet_AfterRepeatedLogin verifies the SEC-1 /
// UAT #399 fix: repeated logins APPEND to the bearer-token set rather than
// overwriting a single token_hash, so a second login from another tab/device
// does NOT invalidate the first client's token.
//
// BDD: Given a user "rotateuser",
// When POST /api/v1/auth/login is called twice,
// Then login-2 returns a distinct token,
// And the disk token set holds TWO entries,
// And BOTH issued tokens still verify against the stored set.
func TestHandleLogin_AppendsTokenSet_AfterRepeatedLogin(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "rotateuser", "rotate-pass-123")

	doLogin := func() (plaintextToken string) {
		t.Helper()
		body := `{"username":"rotateuser","password":"rotate-pass-123"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.HandleLogin(w, req)
		require.Equal(t, http.StatusOK, w.Code, "login must succeed: %s", w.Body.String())

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tok, _ := resp["token"].(string)
		require.NotEmpty(t, tok, "login response must include token")
		return tok
	}

	tok1 := doLogin()
	tok2 := doLogin()

	assert.NotEqual(t, tok1, tok2,
		"each login must issue a cryptographically distinct plaintext token")

	// Read the on-disk token set.
	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	gw, gwOk := diskCfg["gateway"].(map[string]any)
	require.True(t, gwOk, "config.gateway must be an object")
	usersRaw, usersRawOk := gw["users"].([]any)
	require.True(t, usersRawOk, "config.gateway.users must be an array")
	require.Len(t, usersRaw, 1)
	userMap, userMapOk := usersRaw[0].(map[string]any)
	require.True(t, userMapOk, "config.gateway.users[0] must be an object")
	tokens, ok := userMap["tokens"].([]any)
	require.True(t, ok, "tokens set must exist on disk")
	require.Len(t, tokens, 2, "second login must APPEND, leaving two live tokens (not overwrite)")

	// BOTH tokens must still verify against the stored set — the core SEC-1
	// guarantee that the first client is not logged out by the second login.
	verify := func(plain string) bool {
		for _, e := range tokens {
			entry, entryOk := e.(map[string]any)
			if !entryOk {
				continue
			}
			h, _ := entry["hash"].(string)
			// SEC-1: entry hashes are over the secret body, not the full token.
			if bcrypt.CompareHashAndPassword([]byte(h), []byte(config.TokenSecret(plain))) == nil {
				return true
			}
		}
		return false
	}
	assert.True(t, verify(tok1), "first login's token must remain valid after the second login")
	assert.True(t, verify(tok2), "second login's token must be valid")
}

// --- apiRateLimiter tests ---

// TestAPIRateLimiter_AllowsUnderLimit verifies that requests below the limit are allowed.
// BDD: Given a rate limiter with limit=3 per minute,
// When 3 requests come from the same IP,
// Then all 3 are allowed.
// Traces to: Milestone 1 — apiRateLimiter implementation (pkg/gateway/rest_auth.go)
func TestAPIRateLimiter_AllowsUnderLimit(t *testing.T) {
	limiter := newAPIRateLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		assert.True(t, limiter.allow("192.168.1.1"),
			"request %d should be allowed (under limit)", i+1)
	}
}

// TestAPIRateLimiter_BlocksAtLimit verifies that the (limit+1)th request is rejected.
// BDD: Given a rate limiter with limit=3 per minute,
// When 4 requests come from the same IP,
// Then the 4th request is rejected.
// Traces to: Milestone 1 — apiRateLimiter implementation (pkg/gateway/rest_auth.go)
func TestAPIRateLimiter_BlocksAtLimit(t *testing.T) {
	limiter := newAPIRateLimiter(3, time.Minute)

	// Exhaust the limit.
	for i := 0; i < 3; i++ {
		require.True(t, limiter.allow("10.0.0.1"), "request %d should be allowed", i+1)
	}

	// 4th request must be rejected.
	assert.False(t, limiter.allow("10.0.0.1"), "request 4 must be blocked — limit exhausted")
}

// TestAPIRateLimiter_DifferentIPsAreIndependent verifies that rate limiting is per-IP,
// so exhausting one IP does not affect another.
// BDD: Given a rate limiter with limit=2,
// When IP-A exhausts its limit and IP-B makes its first request,
// Then IP-B's request is still allowed.
// Traces to: Milestone 1 — apiRateLimiter implementation (pkg/gateway/rest_auth.go)
func TestAPIRateLimiter_DifferentIPsAreIndependent(t *testing.T) {
	limiter := newAPIRateLimiter(2, time.Minute)

	// Exhaust limit for IP-A.
	require.True(t, limiter.allow("1.1.1.1"))
	require.True(t, limiter.allow("1.1.1.1"))
	require.False(t, limiter.allow("1.1.1.1"), "IP-A should be blocked")

	// IP-B is unaffected.
	assert.True(t, limiter.allow("2.2.2.2"), "IP-B must not be blocked by IP-A exhaustion")
}

// TestAPIRateLimiter_RetryAfterIsPositiveWhenBlocked verifies that retryAfter returns
// a positive value when the IP is over the limit.
// Traces to: Milestone 1 — apiRateLimiter implementation (pkg/gateway/rest_auth.go)
func TestAPIRateLimiter_RetryAfterIsPositiveWhenBlocked(t *testing.T) {
	limiter := newAPIRateLimiter(1, time.Minute)

	// Exhaust the single-request window.
	require.True(t, limiter.allow("3.3.3.3"))
	require.False(t, limiter.allow("3.3.3.3"))

	// retryAfter should be > 0 since the window hasn't expired.
	after := limiter.retryAfter("3.3.3.3")
	assert.Greater(t, after, 0, "retryAfter must be positive when IP is over limit")
}

// TestAPIRateLimiter_RetryAfterIsZeroForUnknownIP verifies that retryAfter returns 0
// for an IP with no recorded timestamps.
// Traces to: Milestone 1 — apiRateLimiter implementation (pkg/gateway/rest_auth.go)
func TestAPIRateLimiter_RetryAfterIsZeroForUnknownIP(t *testing.T) {
	limiter := newAPIRateLimiter(10, time.Minute)
	assert.Equal(t, 0, limiter.retryAfter("unknown-ip"),
		"retryAfter must be 0 for an IP with no entries")
}

// TestWithRateLimit_Returns429WithRetryAfterHeader verifies the withRateLimit wrapper
// sets the Retry-After header and returns 429 when the limit is exceeded.
// Traces to: Milestone 1 — withRateLimit implementation (pkg/gateway/rest_auth.go)
func TestWithRateLimit_Returns429WithRetryAfterHeader(t *testing.T) {
	limiter := newAPIRateLimiter(1, time.Minute)
	handlerCalled := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled++
		w.WriteHeader(http.StatusOK)
	})
	wrapped := withRateLimit(limiter, inner)

	// First request: allowed.
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "5.5.5.5:1234"
	w1 := httptest.NewRecorder()
	wrapped.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code, "first request must be allowed")
	assert.Equal(t, 1, handlerCalled)

	// Second request: blocked (limit=1).
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "5.5.5.5:1234"
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusTooManyRequests, w2.Code, "second request must be 429")
	assert.Equal(t, 1, handlerCalled, "inner handler must not be called on 429")

	// Retry-After header must be present and parseable as an integer >= 1.
	retryAfterHeader := w2.Header().Get("Retry-After")
	assert.NotEmpty(t, retryAfterHeader, "Retry-After header must be set on 429")

	// Response body must contain error field.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	errMsg, errMsgOk := resp["error"].(string)
	require.True(t, errMsgOk, "response error field must be a string")
	assert.Contains(t, errMsg, "rate limit exceeded",
		"429 body must describe the rate limit error")
}

// --- clientIP / TrustXFF tests ---
//
// These prove the fix for the XFF-trust gap: clientIP used to trust
// X-Forwarded-For unconditionally, letting a caller send a different spoofed
// value on every request to get a fresh rate-limit bucket each time and
// defeat login/API brute-force protection. clientIP must now honor XFF only
// when the context config snapshot has Gateway.TrustXFF true, and must strip
// the port from the RemoteAddr fallback (matching canonicalRemoteIP).

// contextWithTrustXFF returns a context carrying a config snapshot with the
// given Gateway.TrustXFF value, simulating what configSnapshotMiddleware
// injects on every real request in production (see
// TestConfigSnapshotMiddleware_InjectsConfig below for the middleware itself).
func contextWithTrustXFF(trustXFF bool) context.Context {
	cfg := &config.Config{Gateway: config.GatewayConfig{TrustXFF: trustXFF}}
	return context.WithValue(context.Background(), configContextKey{}, cfg)
}

// TestClientIP_IgnoresSpoofedXFFByDefault verifies that with no config in
// context (or TrustXFF: false, the documented default) a client-supplied
// X-Forwarded-For header is ignored entirely — clientIP falls back to
// r.RemoteAddr. This is the core brute-force-protection fix: an attacker
// cannot spoof a fresh IP per request to dodge globalLoginLimiter/apiRateLimiter.
func TestClientIP_IgnoresSpoofedXFFByDefault(t *testing.T) {
	// No config in context at all (mirrors a caller that runs before
	// configSnapshotMiddleware, e.g. the CSRF mismatch reporter).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "203.0.113.9:54321"
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	assert.Equal(t, "203.0.113.9", clientIP(req),
		"without a config snapshot, XFF must be ignored and RemoteAddr (port-stripped) used")

	// Explicit TrustXFF: false in context — the documented default posture.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req2.RemoteAddr = "203.0.113.9:9999"
	req2.Header.Set("X-Forwarded-For", "6.6.6.6")
	req2 = req2.WithContext(contextWithTrustXFF(false))
	assert.Equal(t, "203.0.113.9", clientIP(req2),
		"TrustXFF: false must ignore a spoofed X-Forwarded-For header")
}

// TestClientIP_HonorsXFFWhenTrustXFFConfigured verifies the behind-a-trusted-
// proxy use case still works: when the config snapshot has Gateway.TrustXFF
// true, X-Forwarded-For is honored (first comma-separated entry).
func TestClientIP_HonorsXFFWhenTrustXFFConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:443" // the trusted reverse proxy's address
	req.Header.Set("X-Forwarded-For", "198.51.100.42, 10.0.0.1")
	req = req.WithContext(contextWithTrustXFF(true))

	assert.Equal(t, "198.51.100.42", clientIP(req),
		"TrustXFF: true must honor the first X-Forwarded-For entry (real client, not the proxy)")
}

// TestClientIP_StripsPortFromRemoteAddr verifies the second bug fix: the
// no-XFF fallback path must strip the port from r.RemoteAddr, not return it
// verbatim. Before the fix, a client whose HTTP client opened a fresh TCP
// connection per request (a new ephemeral port each time) would get a fresh
// rate-limit bucket every time even without touching any header.
func TestClientIP_StripsPortFromRemoteAddr(t *testing.T) {
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req1.RemoteAddr = "192.168.1.50:11111"
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req2.RemoteAddr = "192.168.1.50:22222"

	ip1 := clientIP(req1)
	ip2 := clientIP(req2)
	assert.Equal(t, "192.168.1.50", ip1, "port must be stripped from RemoteAddr")
	assert.Equal(t, ip1, ip2,
		"two requests from the same host on different ephemeral ports must resolve to the same client IP")
}

// TestWithRateLimit_SpoofedXFFDoesNotBypassLimitByDefault is the end-to-end
// enforcement proof: two requests from the SAME underlying connection but
// with DIFFERENT spoofed X-Forwarded-For values must land in the SAME
// rate-limit bucket (the attack this whole fix defeats), because
// withRateLimit uses clientIP, and by default (no trusted-proxy config)
// clientIP ignores X-Forwarded-For entirely.
func TestWithRateLimit_SpoofedXFFDoesNotBypassLimitByDefault(t *testing.T) {
	limiter := newAPIRateLimiter(1, time.Minute)
	handlerCalled := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled++
		w.WriteHeader(http.StatusOK)
	})
	wrapped := withRateLimit(limiter, inner)

	// First request: allowed. Spoofed XFF #1.
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req1.RemoteAddr = "203.0.113.77:5000"
	req1.Header.Set("X-Forwarded-For", "1.1.1.1")
	w1 := httptest.NewRecorder()
	wrapped.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code, "first request must be allowed")

	// Second request: same underlying connection (RemoteAddr), but a
	// DIFFERENT spoofed XFF value. Before the fix this created a brand-new
	// rate-limit bucket and would have been allowed (200); after the fix it
	// must be blocked (429) because the real RemoteAddr is what's counted.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req2.RemoteAddr = "203.0.113.77:5001"
	req2.Header.Set("X-Forwarded-For", "2.2.2.2")
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code,
		"a different spoofed X-Forwarded-For value must NOT reset the rate-limit bucket")
	assert.Equal(t, 1, handlerCalled, "inner handler must be called exactly once")
}

// --- configSnapshotMiddleware / configFromContext tests ---

// TestConfigSnapshotMiddleware_InjectsConfig verifies that configSnapshotMiddleware
// stores the current config in the request context under configContextKey.
// Traces to: Milestone 1 — configSnapshotMiddleware (pkg/gateway/auth.go)
func TestConfigSnapshotMiddleware_InjectsConfig(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	var capturedCfg *config.Config
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCfg = configFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := api.configSnapshotMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.NotNil(t, capturedCfg,
		"configSnapshotMiddleware must inject a non-nil config into context")
}

// TestConfigFromContext_ReturnsNilWithoutMiddleware verifies that configFromContext
// returns nil when the middleware has not been applied.
// Traces to: Milestone 1 — configFromContext (pkg/gateway/auth.go)
func TestConfigFromContext_ReturnsNilWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	result := configFromContext(req.Context())
	assert.Nil(t, result,
		"configFromContext must return nil when no config was stored in context")
}

// TestConfigFromContext_ReturnsDifferentConfigThanLive verifies the differentiation
// property: two contexts with different configs return different values.
// Traces to: Milestone 1 — configFromContext (pkg/gateway/auth.go)
func TestConfigFromContext_ReturnsDifferentConfigThanLive(t *testing.T) {
	cfg1 := &config.Config{}
	cfg2 := &config.Config{}
	cfg2.Gateway.Host = "different-host"

	ctx1 := context.WithValue(context.Background(), configContextKey{}, cfg1)
	ctx2 := context.WithValue(context.Background(), configContextKey{}, cfg2)

	result1 := configFromContext(ctx1)
	result2 := configFromContext(ctx2)

	assert.Equal(t, cfg1, result1, "context1 must return cfg1")
	assert.Equal(t, cfg2, result2, "context2 must return cfg2")
	assert.NotEqual(t, result1, result2,
		"different context snapshots must return different configs — proves it's not hardcoded")
}

// TestHandleValidateToken_TriggerReloadNotConfigured documents a known limitation:
// TriggerReload returns "reload not configured" in the test environment because
// AgentLoop.Run() is never called during unit tests.
// This is not a production bug — it's a test environment limitation.
// TestLogin_AfterOnboardingWithoutRestart verifies that after onboarding completes
// (which writes users to disk via safeUpdateConfigJSON), a subsequent login and
// token validation succeeds without a process restart.
//
// Before the A2 fix, safeUpdateConfigJSON only wrote to disk. The in-memory config
// (used by withAuth via GetConfig()) still had no users, so checkBearerAuth fell
// into the "no users configured" branch and returned 401 for every request.
//
// After the fix, safeUpdateConfigJSON calls refreshConfigAndRewireServices so GetConfig()
// returns the config with the newly created admin user immediately.
func TestLogin_AfterOnboardingWithoutRestart(t *testing.T) {
	tmpDir := t.TempDir()
	// Fresh gateway — no users in-memory config.
	// version:1 is required — any other version now fails to load outright
	// (there is no legacy migration path).
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

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

	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	credStore, credErr := func() (*credentials.Store, error) {
		s := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
		if err := credentials.Unlock(s); err != nil {
			return nil, err
		}
		return s, nil
	}()
	require.NoError(t, credErr)

	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	// Step 1: Complete onboarding — writes admin to disk AND refreshes in-memory config.
	// Use a provider body that passes ValidateProviders (model field required).
	onboardBody := `{"provider":{"id":"openai","api_key":"sk-test","model":"gpt-4o"},"admin":{"username":"admin","password":"secret123"}}`
	onboardBody = hermeticOnboardBody(t, onboardBody)
	onboardReq := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(onboardBody))
	onboardReq.Header.Set("Content-Type", "application/json")
	onboardW := httptest.NewRecorder()
	api.HandleCompleteOnboarding(onboardW, onboardReq)
	require.Equal(t, http.StatusOK, onboardW.Code, "onboarding must succeed: %s", onboardW.Body.String())

	// Step 2: Login with the newly created admin credentials.
	loginBody := `{"username":"admin","password":"secret123"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code, "login must succeed after onboarding: %s", loginW.Body.String())
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	token, _ := loginResp["token"].(string)
	require.NotEmpty(t, token, "login must return a non-empty token")

	// Step 3: Use the token via withAuth (falls back to GetConfig() since no
	// configSnapshotMiddleware in unit tests). Before A2 fix this returns 401.
	validateHandler := api.withAuth(api.HandleValidateToken)
	validateReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	validateReq.Header.Set("Authorization", "Bearer "+token)
	validateW := httptest.NewRecorder()
	validateHandler(validateW, validateReq)

	assert.Equal(t, http.StatusOK, validateW.Code,
		"validate must return 200 after onboarding without restart: %s", validateW.Body.String())
	var validateResp map[string]any
	require.NoError(t, json.Unmarshal(validateW.Body.Bytes(), &validateResp))
	assert.Equal(t, "admin", validateResp["username"])
}

func TestHandleValidateToken_TriggerReloadNotConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "testuser", string(hash))

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
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	// First login to get a valid token.
	body := `{"username":"testuser","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleLogin(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &loginResp))
	token, tokenOk := loginResp["token"].(string)
	require.True(t, tokenOk, "login response token must be a string")

	// After login, the in-memory config has no users. Read the updated config
	// from disk and inject user context, simulating what withAuth does after reload.
	diskData, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(diskData, &diskCfg))
	gwMap, gwMapOk := diskCfg["gateway"].(map[string]any)
	require.True(t, gwMapOk, "config.gateway must be an object")
	users, usersOk := gwMap["users"].([]any)
	require.True(t, usersOk, "config.gateway.users must be an array")
	require.Len(t, users, 1)

	testUser := &config.UserConfig{
		Username: "testuser",
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx := context.WithValue(req.Context(), UserContextKey{}, testUser)
	req = req.WithContext(ctx)
	w = httptest.NewRecorder()
	api.HandleValidateToken(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "testuser", resp["username"])
}

// TestHandleChangePassword_InvalidatesExistingToken verifies that after a
// successful password change, the existing token is invalidated:
//  1. token_hash and session_token_hash are cleared in config.json on disk.
//  2. The old bearer token, when presented to HandleValidateToken without an
//     active user context (as withAuth behaves when no hash matches), returns 401.
//
// BDD: Given a user "tknuser" with password "OldTokenPass1",
// When POST /auth/login succeeds (token_hash written to config.json)
// AND POST /auth/change-password with correct current_password succeeds,
// Then: (a) token_hash is empty in config.json on disk,
//
//	(b) session_token_hash is empty in config.json on disk,
//	(c) the old bearer token presented to /auth/validate (no context user)
//	    returns 401 Unauthorized.
//
// This test is designed to FAIL on code where HandleChangePassword did NOT clear
// token_hash / session_token_hash, and PASS on the fixed code that clears them.
func TestHandleChangePassword_InvalidatesExistingToken(t *testing.T) {
	api, tmpDir := newTestRestAPIWithUser(t, "tknuser", "OldTokenPass1")

	// Step 1: Login to obtain a token and write token_hash to config.json.
	loginBody := `{"username":"tknuser","password":"OldTokenPass1"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code, "login must succeed before password change")
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	oldToken, oldTokenOk := loginResp["token"].(string)
	require.True(t, oldTokenOk, "login response token must be a string")
	require.NotEmpty(t, oldToken, "login must return a non-empty token")

	// Confirm token_hash is non-empty on disk after login.
	diskDataBefore, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfgBefore map[string]any
	require.NoError(t, json.Unmarshal(diskDataBefore, &diskCfgBefore))
	gwBefore, gwBeforeOk := diskCfgBefore["gateway"].(map[string]any)
	require.True(t, gwBeforeOk, "config.gateway must be an object")
	usersBefore, usersBeforeOk := gwBefore["users"].([]any)
	require.True(t, usersBeforeOk, "config.gateway.users must be an array")
	require.Len(t, usersBefore, 1)
	userMapBefore, userMapBeforeOk := usersBefore[0].(map[string]any)
	require.True(t, userMapBeforeOk, "config.gateway.users[0] must be an object")
	// SEC-1 / UAT #399: login appends bearer tokens to the "tokens" SET, not the
	// legacy single "token_hash" field. The precondition is that the new token is
	// live on disk, which now means the "tokens" array is non-empty.
	tokensBefore, ok := userMapBefore["tokens"].([]any)
	require.True(t, ok, "tokens array must be present in config.json after login")
	require.NotEmpty(t, tokensBefore,
		"a bearer token must be written to the tokens set on disk after login (precondition)")

	// Step 2: Change password — must clear token_hash and session_token_hash.
	cpBody := `{"current_password":"OldTokenPass1","new_password":"NewTokenPass2"}`
	cpReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(cpBody))
	cpReq.Header.Set("Content-Type", "application/json")
	cpReq = injectUser(cpReq, "tknuser")
	cpW := httptest.NewRecorder()
	api.HandleChangePassword(cpW, cpReq)
	require.Equal(t, http.StatusOK, cpW.Code,
		"change-password must succeed (got %s)", cpW.Body.String())

	// Step 3a: Verify token_hash and session_token_hash are cleared on disk.
	diskDataAfter, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfgAfter map[string]any
	require.NoError(t, json.Unmarshal(diskDataAfter, &diskCfgAfter))
	gwAfter, ok := diskCfgAfter["gateway"].(map[string]any)
	require.True(t, ok, "gateway key must be present in config.json after change-password")
	usersAfter, ok := gwAfter["users"].([]any)
	require.True(t, ok, "users array must be present in config.json after change-password")
	require.Len(t, usersAfter, 1)
	userMapAfter, ok := usersAfter[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", userMapAfter["token_hash"],
		"token_hash must be cleared in config.json after password change — "+
			"old token must be invalidated")
	assert.Equal(t, "", userMapAfter["session_token_hash"],
		"session_token_hash must be cleared in config.json after password change")
	// SEC-1 / UAT #399: the active bearer-token SET must also be emptied, else the
	// old token still verifies via UserConfig.VerifyToken and the password change
	// would not invalidate existing sessions.
	if tokensAfter, ok := userMapAfter["tokens"].([]any); ok {
		assert.Empty(t, tokensAfter,
			"tokens set must be cleared in config.json after password change — "+
				"old bearer tokens must be invalidated")
	}

	// Step 3b: The old token, routed through withAuth (which calls checkBearerAuth
	// and bcrypt-compares against token_hash in the in-memory config), must yield
	// 401 because HandleChangePassword cleared token_hash above.
	//
	// safeUpdateConfigJSON calls refreshConfigAndRewireServices after every write,
	// so GetConfig() already reflects the empty token_hash — no process restart
	// required. This assertion FAILS if HandleChangePassword does NOT clear
	// token_hash (the hash still matches → withAuth injects the user → 200).
	validateHandler := api.withAuth(api.HandleValidateToken)
	validateReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	validateReq.Header.Set("Authorization", "Bearer "+oldToken)
	validateW := httptest.NewRecorder()
	validateHandler(validateW, validateReq)
	assert.Equal(t, http.StatusUnauthorized, validateW.Code,
		"old bearer token must be rejected after password change (token_hash cleared)")
}

// --- triggerReloadAndWait tests (B5 poll loop) ---

// newTestRestAPIForReload builds a minimal restAPI backed by a temp dir and
// returns both the api and the underlying AgentLoop so tests can configure
// SetReloadFunc and drive ClearReloadPending.
func newTestRestAPIForReload(t *testing.T) (*restAPI, *agentLoopWrapper) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
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
	apiObj := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}
	return apiObj, &agentLoopWrapper{al: al}
}

// agentLoopWrapper gives access to reload control methods in tests without
// importing agent.AgentLoop directly (the interface enforces only what we need).
type agentLoopWrapper struct {
	al interface {
		SetReloadFunc(fn func() error)
		MarkReloadPending()
		ClearReloadPending()
	}
}

// TestTriggerReloadAndWait_PollsUntilNotPending verifies that triggerReloadAndWait
// returns nil once IsReloadPending() clears — i.e., the polling loop unblocks
// when a goroutine calls ClearReloadPending after ~50ms.
//
// BDD: Given a reloadFunc that keeps reloadPending=true until ClearReloadPending
// is called by a goroutine, when triggerReloadAndWait is called,
// then it blocks briefly and returns nil once the pending flag clears.
func TestTriggerReloadAndWait_PollsUntilNotPending(t *testing.T) {
	apiObj, wrap := newTestRestAPIForReload(t)

	// The reloadFunc marks the reload pending, mirroring production: the flag is
	// set by the gateway's trigger (services.beginReload, under the mutex that
	// clears it), NOT by TriggerReload. A fake that returned nil without marking
	// would mean "no reload queued", and the poll loop would correctly return at
	// once — leaving this test asserting nothing.
	wrap.al.SetReloadFunc(func() error {
		wrap.al.MarkReloadPending()
		return nil
	})

	go func() {
		time.Sleep(50 * time.Millisecond)
		wrap.al.ClearReloadPending()
	}()

	start := time.Now()
	err := apiObj.triggerReloadAndWait()
	elapsed := time.Since(start)

	require.NoError(t, err, "triggerReloadAndWait must return nil when reload completes")
	// Must have polled for at least 40ms (pending was set), but well under the deadline.
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(40),
		"triggerReloadAndWait must poll until the pending flag clears")
	assert.Less(t, elapsed, reloadWaitTimeout,
		"triggerReloadAndWait must return well before its deadline")
}

// TestTriggerReloadAndWait_AlreadyInProgress_PollsThrough verifies that when
// TriggerReload returns ErrReloadAlreadyInProgress, triggerReloadAndWait falls
// through to the polling loop and returns nil when IsReloadPending() clears.
//
// BDD: Given a reloadFunc that simulates "already in progress", when
// triggerReloadAndWait is called, then it polls until the pending flag is
// cleared and returns nil.
func TestTriggerReloadAndWait_AlreadyInProgress_PollsThrough(t *testing.T) {
	apiObj, wrap := newTestRestAPIForReload(t)

	// reloadFunc returns the "already in progress" sentinel string. TriggerReload
	// sets reloadPending=true before calling it, so the poll loop will see pending=true.
	wrap.al.SetReloadFunc(func() error {
		return fmt.Errorf("reload already in progress")
	})

	// Clear the pending flag from a goroutine after ~50ms.
	go func() {
		time.Sleep(50 * time.Millisecond)
		wrap.al.ClearReloadPending()
	}()

	err := apiObj.triggerReloadAndWait()
	require.NoError(t, err,
		"triggerReloadAndWait must return nil on ErrReloadAlreadyInProgress poll-through")
}

// --- triggerReloadAndWaitOutcome tests (FIX 4) ---

// TestTriggerReloadAndWaitOutcome_ConfirmedReload_ReturnsConfirmedTrue is the
// positive-case proof: when IsReloadPending() clears within the poll window,
// triggerReloadAndWaitOutcome must report confirmed=true.
func TestTriggerReloadAndWaitOutcome_ConfirmedReload_ReturnsConfirmedTrue(t *testing.T) {
	apiObj, wrap := newTestRestAPIForReload(t)

	wrap.al.SetReloadFunc(func() error {
		return nil
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		wrap.al.ClearReloadPending()
	}()

	confirmed, err := apiObj.triggerReloadAndWaitOutcome()
	require.NoError(t, err)
	assert.True(t, confirmed, "a reload confirmed within the poll window must report confirmed=true")
}

// TestTriggerReloadAndWaitOutcome_TimesOutStillPending_ReturnsUnconfirmed is
// the FIX 4 regression test: before this fix, a reload that never confirmed
// within the poll window was indistinguishable from a confirmed one — both
// triggerReloadAndWait return values were nil. Nothing ever calls
// ClearReloadPending here, so IsReloadPending() stays true for the whole
// (shortened, via reloadWaitTimeout) poll window, forcing the timeout
// branch deterministically rather than relying on a slow real 5s wait.
func TestTriggerReloadAndWaitOutcome_TimesOutStillPending_ReturnsUnconfirmed(t *testing.T) {
	apiObj, wrap := newTestRestAPIForReload(t)

	prevDeadline := reloadWaitTimeout
	reloadWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() { reloadWaitTimeout = prevDeadline })

	// The reloadFunc marks the reload pending, mirroring production: the flag
	// is set by the gateway's trigger (services.beginReload), NOT by
	// TriggerReload itself (see TriggerReload's own doc comment) — a fake
	// that returned nil without marking would mean "no reload queued", and
	// the poll loop would correctly return at once, leaving this test
	// asserting nothing (see TestTriggerReloadAndWait_PollsUntilNotPending's
	// identical setup above).
	wrap.al.SetReloadFunc(func() error {
		wrap.al.MarkReloadPending()
		return nil
	})
	// Deliberately never call ClearReloadPending — the pending flag must
	// still be set when the poll loop hits the deadline.

	start := time.Now()
	confirmed, err := apiObj.triggerReloadAndWaitOutcome()
	elapsed := time.Since(start)

	require.NoError(t, err,
		"a timeout must still return a nil error — the 15 existing call sites outside this "+
			"fix's scope that only check err must not start treating a slow reload as a hard failure")
	assert.False(t, confirmed,
		"FIX 4: a reload that never confirmed within the poll window must report confirmed=false, "+
			"not be indistinguishable from a genuinely confirmed reload")
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond,
		"must have actually waited out the poll window, not returned early")

	// The reload really is still marked pending; clear it so nothing in
	// AgentLoop teardown depends on it.
	wrap.al.ClearReloadPending()
}

// TestHandleChangePassword_ReloadTimeout_LogsDistinctWarning is the FIX 4
// regression test for HandleChangePassword's own call site: before this fix,
// a reload that timed out without confirming was completely silent (the old
// `if err := a.triggerReloadAndWait(); err != nil` guard never fires when
// err is nil, which it always is on a timeout) — an operator had no way to
// know the freshly-cleared token set might not be live everywhere yet. This
// proves the new confirmed==false branch logs a distinct message.
func TestHandleChangePassword_ReloadTimeout_LogsDistinctWarning(t *testing.T) {
	api, _ := newTestRestAPIWithUser(t, "cpuser-reload-timeout", "OldPass123")

	prevDeadline := reloadWaitTimeout
	reloadWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() { reloadWaitTimeout = prevDeadline })

	// Wire a reload func that marks the reload pending (mirroring production
	// — see TriggerReload's own doc comment for why TriggerReload itself
	// deliberately does not) and deliberately never clear the pending flag,
	// so triggerReloadAndWaitOutcome (called from inside HandleChangePassword)
	// runs out the (shortened) poll window with confirmed=false.
	api.agentLoop.SetReloadFunc(func() error {
		api.agentLoop.MarkReloadPending()
		return nil
	})
	t.Cleanup(func() { api.agentLoop.ClearReloadPending() })

	logFile := filepath.Join(t.TempDir(), "change-password-reload-timeout.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})
	// HandleChangePassword's new confirmed==false branch uses a bare
	// slog.Warn (matching the existing err!=nil branch right next to it).
	// In production this reaches gateway.log because RunContextWithOptions
	// installs the slog→logger bridge at boot (FIX 1) before any handler
	// can run; this test binary never calls RunContextWithOptions, so the
	// bridge must be installed explicitly here to observe the same real
	// behavior — same pattern as slog_bridge_wiring_test.go.
	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	installSlogBridge()

	body := `{"current_password":"OldPass123","new_password":"NewPass456"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectUser(req, "cpuser-reload-timeout")
	w := httptest.NewRecorder()

	api.HandleChangePassword(w, req)

	// The wire response is unaffected — HandleChangePassword still reports
	// success (gen.OperationResult has no field to carry this distinction;
	// see triggerReloadAndWaitOutcome's doc comment). The password change
	// itself is genuinely unconditional on the reload outcome.
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "did not confirm within the poll window",
		"a reload that timed out unconfirmed must now be logged distinctly, "+
			"not silently indistinguishable from a confirmed reload")
	assert.Contains(t, string(logged), "cpuser-reload-timeout")
}
