package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// testMasterKey is a deterministic hex master key used only in tests.
const testMasterKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// newOnboardingTestAPI creates a restAPI wired with an unlocked credential store
// for onboarding tests that submit api_key values (SEC-23: no plaintext fallback).
func newOnboardingTestAPI(t *testing.T, tmpDir string, al *agent.AgentLoop) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	if err := credentials.Unlock(credStore); err != nil {
		t.Fatalf("unlock credential store: %v", err)
	}
	return &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}
}

// --- provider key-validation test scaffolding ---

// startFakeProviderUpstream starts a loopback stand-in for an OpenAI-compatible
// provider that accepts ANY api key: GET /models returns a one-model catalog and
// POST /chat/completions returns a minimal completion, so providers.ValidateKey
// classifies the key as `valid`.
//
// Every onboarding test that expects to reach the end of HandleCompleteOnboarding
// needs one. Since the handler now probes the submitted api_key (see the
// "Provider API-key validation" block in rest_onboarding.go), a test posting
// {"id":"openai","auth":"api_key","api_key":"sk-test"} with no api_base override would otherwise
// make a live call to api.openai.com on every CI run — and get a real 401, i.e.
// the tests would both depend on the public internet AND fail once they reached
// it. Pointing provider.endpoint at this server keeps them hermetic.
//
// The server binds 127.0.0.1; onboarding tests leave api.ssrfChecker nil, so the
// SSRF gate (which blocks loopback by default) does not skip the probe.
func startFakeProviderUpstream(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// withProviderEndpoint returns body with provider.endpoint set to endpoint. It is
// a pure string transform (no *testing.T) so it is safe to call from the
// goroutines in the concurrency tests.
func withProviderEndpoint(body, endpoint string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		panic("withProviderEndpoint: body is not valid JSON: " + err.Error())
	}
	prov, ok := m["provider"].(map[string]any)
	if !ok {
		panic("withProviderEndpoint: body has no provider object")
	}
	prov["endpoint"] = endpoint
	out, err := json.Marshal(m)
	if err != nil {
		panic("withProviderEndpoint: re-marshal failed: " + err.Error())
	}
	return string(out)
}

// hermeticOnboardBody is the one-call form: start a stand-in provider and point
// the request body at it.
func hermeticOnboardBody(t *testing.T, body string) string {
	t.Helper()
	return withProviderEndpoint(body, startFakeProviderUpstream(t))
}

// --- HandleCompleteOnboarding tests ---

// TestHandleCompleteOnboarding_Success verifies that POST /api/v1/onboarding/complete
// with valid provider and admin credentials returns 200 with a token.
// BDD: Given a fresh install (onboarding not complete),
// When POST /api/v1/onboarding/complete {"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}} is called,
// Then 200 with {"token":"<token>","role":"admin","username":"admin"}.
func TestHandleCompleteOnboarding_Success(t *testing.T) {
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

	// Verify onboarding is not complete yet
	require.False(t, api.onboardingMgr.IsComplete(), "onboarding should not be complete initially")

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"], "token must be non-empty")
	assert.Equal(t, "admin", resp["username"])
}

// TestHandleCompleteOnboarding_AlreadyComplete verifies that POST /api/v1/onboarding/complete
// returns 409 Conflict when onboarding is already complete.
// BDD: Given onboarding is already complete,
// When POST /api/v1/onboarding/complete is called again,
// Then 409 Conflict with {"error":"onboarding already complete"}.
func TestHandleCompleteOnboarding_AlreadyComplete(t *testing.T) {
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
	onboardingMgr := onboarding.NewManager(tmpDir)
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboardingMgr,
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	// Mark onboarding as complete
	require.NoError(t, onboardingMgr.CompleteOnboarding())

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "onboarding already complete", resp["error"])
}

// TestHandleCompleteOnboarding_MissingAPIKey verifies that POST /api/v1/onboarding/complete
// with empty provider.api_key returns 400.
// BDD: Given provider.api_key is empty,
// When POST /api/v1/onboarding/complete is called,
// Then 400 with {"error":"provider.api_key is required"}.
func TestHandleCompleteOnboarding_MissingAPIKey(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":""},"admin":{"username":"admin","password":"secret123"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "provider.api_key is required", resp["error"])
}

// TestHandleCompleteOnboarding_MissingProviderID verifies that POST /api/v1/onboarding/complete
// with empty provider.id returns 400.
// BDD: Given provider.id is empty,
// When POST /api/v1/onboarding/complete is called,
// Then 400 with {"error":"provider.id is required"}.
func TestHandleCompleteOnboarding_MissingProviderID(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	body := `{"provider":{"auth_method":"api_key","id":"","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "provider.id is required", resp["error"])
}

// TestHandleCompleteOnboarding_MissingAdminUsername verifies that POST /api/v1/onboarding/complete
// with empty admin.username returns 400.
// BDD: Given admin.username is empty,
// When POST /api/v1/onboarding/complete is called,
// Then 400 with {"error":"admin.username is required"}.
func TestHandleCompleteOnboarding_MissingAdminUsername(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"","password":"secret123"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin.username is required", resp["error"])
}

// TestHandleCompleteOnboarding_MissingAdminPassword verifies that POST /api/v1/onboarding/complete
// with empty admin.password returns 400.
// BDD: Given admin.password is empty,
// When POST /api/v1/onboarding/complete is called,
// Then 400 with {"error":"admin.password is required"}.
func TestHandleCompleteOnboarding_MissingAdminPassword(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":""}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin.password is required", resp["error"])
}

// TestHandleCompleteOnboarding_RejectsInvalidUsername verifies that POST /api/v1/onboarding/complete
// rejects usernames that fail usernameRE validation with 400.
// BDD: Given an admin.username that violates the username constraints,
// When POST /api/v1/onboarding/complete is called,
// Then 400 with an error containing "username".
func TestHandleCompleteOnboarding_RejectsInvalidUsername(t *testing.T) {
	invalidCases := []struct {
		name     string
		username string
	}{
		{"too short (single char)", "a"},
		{"starts with dot", ".admin"},
		{"contains space", "admin user"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHomeAuth(t)

			body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"` + tc.username + `","password":"secret123"}}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			api.HandleCompleteOnboarding(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code, "username %q should be rejected", tc.username)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			errMsg, _ := resp["error"].(string)
			assert.Contains(t, errMsg, "username", "error should mention username for input %q", tc.username)
		})
	}

	// Positive case: a valid 2-char username must NOT be rejected by username validation.
	// It may fail for other reasons (e.g. provider validation) but must not return usernameInvalidMsg.
	t.Run("valid 2-char username passes username check", func(t *testing.T) {
		api := newTestRestAPIWithHomeAuth(t)

		body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"ab","password":"secret123"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.HandleCompleteOnboarding(w, req)

		if w.Code == http.StatusBadRequest {
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			errMsg, _ := resp["error"].(string)
			assert.NotEqual(t, usernameInvalidMsg, errMsg,
				"valid username 'ab' must not be rejected by username validation")
		}
	})
}

// TestHandleCompleteOnboarding_WeakPassword verifies that POST /api/v1/onboarding/complete
// with a password shorter than 8 characters returns 400.
// BDD: Given admin.password is "short" (less than 8 characters),
// When POST /api/v1/onboarding/complete is called,
// Then 400 with {"error":"admin.password must be at least 8 characters"}.
func TestHandleCompleteOnboarding_WeakPassword(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"short"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin.password must be at least 8 characters", resp["error"])
}

// TestHandleCompleteOnboarding_MethodNotAllowed verifies that GET /api/v1/onboarding/complete
// returns 405.
// BDD: Given a GET request to /onboarding/complete,
// When the request is processed,
// Then 405 Method Not Allowed is returned.
func TestHandleCompleteOnboarding_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHomeAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding/complete", nil)
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- Integration: onboarding -> login -> validate ---

// TestHandleCompleteOnboarding_ThenLogin verifies the full onboarding flow:
// 1. Complete onboarding (returns token)
// 2. Login with the admin credentials (returns another token)
// 3. Validate the login token (returns user info).
// BDD: Given a fresh install,
// When the onboarding flow completes and login is attempted with the admin credentials,
// Then login succeeds and the returned token validates successfully.
func TestHandleCompleteOnboarding_ThenLogin(t *testing.T) {
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

	// Step 1: Complete onboarding
	onboardingBody := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	onboardingBody = hermeticOnboardBody(t, onboardingBody)
	onboardingReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/onboarding/complete",
		strings.NewReader(onboardingBody),
	)
	onboardingReq.Header.Set("Content-Type", "application/json")
	onboardingW := httptest.NewRecorder()
	api.HandleCompleteOnboarding(onboardingW, onboardingReq)
	require.Equal(t, http.StatusOK, onboardingW.Code)
	var onboardingResp map[string]any
	require.NoError(t, json.Unmarshal(onboardingW.Body.Bytes(), &onboardingResp))
	onboardingToken, onboardingTokenOk := onboardingResp["token"].(string)
	require.True(t, onboardingTokenOk, "onboarding response token must be a string")
	assert.NotEmpty(t, onboardingToken, "onboarding must return a token")

	// Step 2: Login with the admin credentials
	loginBody := `{"username":"admin","password":"secret123"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	api.HandleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code)
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	loginToken, loginTokenOk := loginResp["token"].(string)
	require.True(t, loginTokenOk, "login response token must be a string")
	assert.NotEmpty(t, loginToken, "login must return a token")

	// Step 3: Validate the login token.
	// HandleValidateToken expects UserContextKey set by withAuth middleware.
	// In unit tests we inject it manually, simulating what withAuth does.
	adminUser := &config.UserConfig{Username: "admin"}
	validateReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	validateReq.Header.Set("Authorization", "Bearer "+loginToken)
	validateReq = validateReq.WithContext(context.WithValue(validateReq.Context(), UserContextKey{}, adminUser))
	validateW := httptest.NewRecorder()
	api.HandleValidateToken(validateW, validateReq)

	assert.Equal(t, http.StatusOK, validateW.Code)
	var validateResp map[string]any
	require.NoError(t, json.Unmarshal(validateW.Body.Bytes(), &validateResp))
	assert.Equal(t, "admin", validateResp["username"])

	// Onboarding token should also be valid (same user)
	validateReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	validateReq2.Header.Set("Authorization", "Bearer "+onboardingToken)
	validateReq2 = validateReq2.WithContext(context.WithValue(validateReq2.Context(), UserContextKey{}, adminUser))
	validateW2 := httptest.NewRecorder()
	api.HandleValidateToken(validateW2, validateReq2)
	assert.Equal(t, http.StatusOK, validateW2.Code)
}

// TestHandleCompleteOnboarding_PersistsAdmin verifies that the admin user created
// during onboarding persists in config.json and can be used to login after restart.
// BDD: Given onboarding completes and creates admin user,
// When config.json is read directly,
// Then it contains the admin user with a password_hash and token_hash.
func TestHandleCompleteOnboarding_PersistsAdmin(t *testing.T) {
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

	// Complete onboarding
	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleCompleteOnboarding(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Read config.json directly and verify admin user is persisted
	configData, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	var configMap map[string]any
	require.NoError(t, json.Unmarshal(configData, &configMap))

	gateway, ok := configMap["gateway"].(map[string]any)
	require.True(t, ok, "config must have gateway key")
	users, ok := gateway["users"].([]any)
	require.True(t, ok, "gateway must have users array")
	require.Len(t, users, 1, "must have exactly 1 user")

	user, ok := users[0].(map[string]any)
	require.True(t, ok, "user must be a map")
	assert.Equal(t, "admin", user["username"])
	assert.NotEmpty(t, user["password_hash"], "password_hash must be set")
	// SEC-1 / UAT #399: onboarding now issues the admin's bearer token into the
	// token SET, not the legacy single token_hash.
	tokens, ok := user["tokens"].([]any)
	require.True(t, ok, "tokens set must be written by onboarding")
	require.Len(t, tokens, 1, "onboarding issues exactly one bearer token")
	entry, entryOk := tokens[0].(map[string]any)
	require.True(t, entryOk, "tokens[0] must be an object")
	assert.NotEmpty(t, entry["hash"], "token entry hash must be set")
	assert.NotEmpty(t, entry["id"], "token entry id must be set")
}

// TestHandleCompleteOnboarding_WritesActualModelAsAlias verifies the fix for
// the "agents show 'openrouter' instead of the selected model" bug.
//
// The provider entry created during onboarding must use the actual model
// string as its model_name (the alias that agents.defaults.model_name
// references), not the provider ID. Otherwise the Agent Profile UI shows
// a generic name (e.g. "openrouter") instead of the model the user picked
// (e.g. "z-ai/glm-5v-turbo"), and subsequent onboardings with a different
// model from the same provider would stomp on the existing entry.
//
// BDD: Given a fresh install,
// When POST /api/v1/onboarding/complete with {provider.id=openrouter, provider.model=z-ai/glm-5v-turbo},
// Then config.providers contains an entry with model_name="z-ai/glm-5v-turbo"
//
//	AND config.agents.defaults.model_name == "z-ai/glm-5v-turbo"
//	AND the provider entry keeps provider="openrouter" for API routing.
func TestHandleCompleteOnboarding_WritesActualModelAsAlias(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	body := `{"provider":{"auth_method":"api_key","id":"openrouter","api_key":"sk-or-v1-test","model":"z-ai/glm-5v-turbo"},` +
		`"admin":{"username":"admin","password":"secret123"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleCompleteOnboarding(w, req)
	require.Equal(t, http.StatusOK, w.Code, "onboarding must succeed (body=%s)", w.Body.String())

	configData, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	var configMap map[string]any
	require.NoError(t, json.Unmarshal(configData, &configMap))

	// 1. agents.defaults.model_name is the actual model, not the provider ID.
	agents, ok := configMap["agents"].(map[string]any)
	require.True(t, ok)
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "z-ai/glm-5v-turbo", defaults["model_name"],
		"agents.defaults.model_name must be the actual model the user selected, not the provider ID")

	// 2. The new provider entry uses the actual model as model_name,
	//    keeps provider=openrouter for API routing, and stores the api_key_ref.
	providers, ok := configMap["providers"].([]any)
	require.True(t, ok)
	var match map[string]any
	for _, p := range providers {
		entry, _ := p.(map[string]any)
		if entry["provider"] == "openrouter" && entry["model"] == "z-ai/glm-5v-turbo" {
			match = entry
			break
		}
	}
	require.NotNil(t, match, "provider entry for (openrouter, z-ai/glm-5v-turbo) must exist")
	assert.Equal(t, "z-ai/glm-5v-turbo", match["model_name"],
		"provider.model_name must mirror the model, matching the alias convention used by seeded entries")
	assert.Equal(t, "openrouter", match["provider"])
	assert.Equal(t, "z-ai/glm-5v-turbo", match["model"])
	assert.Equal(t, "openrouter_API_KEY", match["api_key_ref"])
	// No plaintext api_key leaked to config.json
	_, hasPlaintext := match["api_key"]
	assert.False(t, hasPlaintext, "api_key must not appear in config.json (credentials in encrypted store)")
}

// TestHandleCompleteOnboarding_SecondModelSameProviderCreatesNewEntry verifies
// that if an operator runs onboarding twice against the same provider but with
// different models, a distinct provider entry is created each time — instead
// of the second onboarding overwriting the first (the old behavior when the
// dedup key was the provider ID alias).
//
// In practice users don't re-run onboarding; the same invariant guards the
// Settings → Providers UI that adds a second model from the same provider.
func TestHandleCompleteOnboarding_SecondModelSameProviderCreatesNewEntry(t *testing.T) {
	tmpDir := t.TempDir()
	// Pre-populate config with one openrouter entry to simulate a prior onboarding.
	existing := []byte(`{"version":1,"agents":{"defaults":{"model_name":"z-ai/glm-5v-turbo"},"list":[]},` +
		`"providers":[{"model_name":"z-ai/glm-5v-turbo","model":"z-ai/glm-5v-turbo",` +
		`"provider":"openrouter","api_key_ref":"openrouter_API_KEY"}]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", existing, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	body := `{"provider":{"auth_method":"api_key","id":"openrouter","api_key":"sk-or-v1-test","model":"anthropic/claude-sonnet-4.6"},` +
		`"admin":{"username":"admin","password":"secret123"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleCompleteOnboarding(w, req)
	require.Equal(t, http.StatusOK, w.Code, "onboarding must succeed (body=%s)", w.Body.String())

	configData, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	var configMap map[string]any
	require.NoError(t, json.Unmarshal(configData, &configMap))

	// Both provider entries must survive — distinct models, same provider, shared api_key_ref.
	providers, _ := configMap["providers"].([]any)
	var hasGLM, hasClaude bool
	for _, p := range providers {
		e, _ := p.(map[string]any)
		if e["model"] == "z-ai/glm-5v-turbo" && e["provider"] == "openrouter" {
			hasGLM = true
		}
		if e["model"] == "anthropic/claude-sonnet-4.6" && e["provider"] == "openrouter" {
			hasClaude = true
			assert.Equal(t, "anthropic/claude-sonnet-4.6", e["model_name"])
		}
	}
	assert.True(t, hasGLM, "original provider entry must not be stomped")
	assert.True(t, hasClaude, "new provider entry must be created for the second model")
}

// --- Concurrency tests ---

// TestHandleCompleteOnboarding_Concurrent verifies that concurrent calls to
// HandleCompleteOnboarding do not corrupt state — at most one succeeds,
// the rest get 409 Conflict or success without data corruption.
// BDD: Given multiple concurrent POST /api/v1/onboarding/complete requests,
// When all are handled simultaneously,
// Then each either succeeds (200) or gets Conflict (409), and config.json
// is not corrupted (has exactly one admin user).
func TestHandleCompleteOnboarding_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	// Set up a credential store so the onboarding can persist API keys (SEC-23).
	masterKey := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	t.Setenv("OMNIPUS_MASTER_KEY", masterKey)
	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))

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
	onboardingMgr := onboarding.NewManager(tmpDir)
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboardingMgr,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	// One shared stand-in provider for all n goroutines: startFakeProviderUpstream
	// registers a t.Cleanup, which must not be called from inside a goroutine.
	upstream := startFakeProviderUpstream(t)

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
			body = withProviderEndpoint(body, upstream)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			api.HandleCompleteOnboarding(w, req)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	// At least one should succeed
	has200 := false
	for _, code := range codes {
		if code == http.StatusOK {
			has200 = true
		}
		// Other valid responses: 409 Conflict (onboarding already complete)
	}
	assert.True(t, has200, "at least one concurrent request must succeed with 200")

	// After all concurrent requests, config.json should have exactly 1 user (no corruption)
	configData, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	var configMap map[string]any
	require.NoError(t, json.Unmarshal(configData, &configMap))

	gateway, gatewayOk := configMap["gateway"].(map[string]any)
	require.True(t, gatewayOk, "config.gateway must be an object")
	users, usersOk := gateway["users"].([]any)
	require.True(t, usersOk, "config.gateway.users must be an array")
	assert.Len(t, users, 1, "config.json must have exactly 1 admin user after concurrent calls (no duplication)")
}

// TestHandleCompleteOnboarding_ConcurrentDifferentUsers verifies that when
// concurrent requests try to create different usernames, only one succeeds
// (the one that acquires the lock first) and the others get 409 or 500.
func TestHandleCompleteOnboarding_ConcurrentDifferentUsers(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	// Set up a credential store so the onboarding can persist API keys (SEC-23).
	masterKey := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	t.Setenv("OMNIPUS_MASTER_KEY", masterKey)
	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))

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
		credStore:     credStore,
	}

	// One shared stand-in provider for all n goroutines: startFakeProviderUpstream
	// registers a t.Cleanup, which must not be called from inside a goroutine.
	upstream := startFakeProviderUpstream(t)

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test-` + string(
				rune('0'+idx),
			) + `"},"admin":{"username":"admin` + string(
				rune('0'+idx),
			) + `","password":"secret123"}}`
			body = withProviderEndpoint(body, upstream)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			api.HandleCompleteOnboarding(w, req)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	// At least one must succeed
	hasSuccess := false
	for _, code := range codes {
		if code == http.StatusOK {
			hasSuccess = true
			break
		}
	}
	assert.True(t, hasSuccess, "at least one concurrent request must succeed")

	// Config should not be corrupted — should have exactly 1 user
	configData, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	var configMap map[string]any
	require.NoError(t, json.Unmarshal(configData, &configMap))

	gateway, gatewayOk := configMap["gateway"].(map[string]any)
	require.True(t, gatewayOk, "config.gateway must be an object")
	usersRaw := gateway["users"]
	if usersRaw == nil {
		t.Fatal("gateway.users should not be nil")
	}
	users, usersOk := usersRaw.([]any)
	require.True(t, usersOk, "config.gateway.users must be an array")
	assert.Len(t, users, 1, "config.json must have exactly 1 user after concurrent calls")
}

// --- HandleOnboardingProbeProvider tests ---

// probeProviderWithUpstream points the probe at a stub httptest server that
// mimics the OpenAI /v1/models shape. Used to avoid hitting real provider APIs.
func probeProviderWithUpstream(t *testing.T, upstream string, body string, api *restAPI) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	_ = upstream
	api.HandleOnboardingProbeProvider(w, req)
	return w
}

// TestHandleOnboardingProbeProvider_SuccessWithModels probes a stub upstream
// and asserts the handler returns the model list without persisting anything.
func TestHandleOnboardingProbeProvider_SuccessWithModels(t *testing.T) {
	// Stub /v1/models endpoint.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)
	require.False(t, api.onboardingMgr.IsComplete(),
		"onboarding must not be complete for the probe to work")

	body := `{"id":"openai","auth":"api_key","api_key":"test-key","api_base":"` + upstream.URL + `"}`
	w := probeProviderWithUpstream(t, upstream.URL, body, api)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	models, _ := resp["models"].([]any)
	assert.ElementsMatch(t, []any{"gpt-3.5-turbo", "gpt-4"}, models,
		"probe must return the upstream model list sorted alphabetically")

	// Nothing persisted: config.json has no providers array entry, creds store is empty.
	cfgData, err := os.ReadFile(tmpDir + "/config.json")
	if err == nil {
		assert.NotContains(t, string(cfgData), "test-key",
			"probe must not persist the api_key to config.json")
	}
}

// TestHandleOnboardingProbeProvider_UpstreamUnauthorized verifies that a 401
// from the upstream is surfaced as success=false with an error message,
// matching the existing POST /providers/{id}/test contract.
func TestHandleOnboardingProbeProvider_UpstreamUnauthorized(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)

	body := `{"id":"openai","auth":"api_key","api_key":"bad-key","api_base":"` + upstream.URL + `"}`
	w := probeProviderWithUpstream(t, upstream.URL, body, api)

	assert.Equal(t, http.StatusOK, w.Code,
		"upstream failure still returns HTTP 200 with success=false (same shape as /providers/{id}/test)")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
	errMsg, errMsgOk := resp["error"].(string)
	require.True(t, errMsgOk, "response error field must be a string")
	assert.Contains(t, errMsg, "401")
}

// TestHandleOnboardingProbeProvider_AlreadyComplete verifies that once
// onboarding is marked complete, the endpoint returns HTTP 409 to steer
// admins to the normal provider-management flow.
func TestHandleOnboardingProbeProvider_AlreadyComplete(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	api := newOnboardingTestAPI(t, tmpDir, nil)

	// Complete onboarding by writing the state marker directly — avoids
	// running the full /onboarding/complete handler in this narrow test.
	commit, err := api.onboardingMgr.ReserveComplete()
	require.NoError(t, err)
	require.NoError(t, commit())
	require.True(t, api.onboardingMgr.IsComplete())

	body := `{"id":"openai","auth":"api_key","api_key":"any","api_base":"http://127.0.0.1:1/"}`
	w := probeProviderWithUpstream(t, "", body, api)

	assert.Equal(t, http.StatusConflict, w.Code,
		"probe-provider must return 409 once onboarding is complete")
	assert.Contains(t, w.Body.String(), "onboarding already complete")
}

// TestHandleOnboardingProbeProvider_MissingFields exercises the request-body
// validation branches — empty id, empty api_key, and an unknown provider
// without api_base override must all return 400.
func TestHandleOnboardingProbeProvider_MissingFields(t *testing.T) {
	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)

	cases := []struct {
		name string
		body string
		want string // substring of error
	}{
		{"empty_id", `{"auth":"api_key","api_key":"k","api_base":"http://x/"}`, "id is required"},
		{"empty_api_key", `{"id":"openai","auth":"api_key","api_base":"http://x/"}`, "api_key is required"},
		{"unknown_provider_no_api_base", `{"id":"notaprovider","auth":"api_key","api_key":"k"}`, "unknown provider"},
		{"malformed_json", `{not-json`, "invalid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := probeProviderWithUpstream(t, "", tc.body, api)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

// TestHandleCompleteOnboarding_BadRequest_ReleasesReservation verifies that
// when HandleCompleteOnboarding returns 400 (bad request body — validation
// failure before the config write), the onboarding reservation is released
// so that a subsequent valid attempt can succeed.
//
// BDD: Given a fresh install (onboarding not complete),
// When POST /api/v1/onboarding/complete with a missing admin.username (400 path),
// Then: (a) HTTP 400 is returned, AND
//
//	(b) the onboarding manager is not in the "reserved" state so a second
//	    valid POST succeeds with 200 (not 409 Conflict).
//
// This test is designed to FAIL on code where the defer guard was absent
// (reservation held permanently after a 400) and PASS on the fixed code
// (defer releases reservation on every non-committed return path).
func TestHandleCompleteOnboarding_BadRequest_ReleasesReservation(t *testing.T) {
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

	// Step 1: Send a bad request — admin.username is empty, which triggers a 400
	// before the config write. The reservation must be released in the defer.
	badBody := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"","password":"secret123"}}`
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(badBody))
	badReq.Header.Set("Content-Type", "application/json")
	badW := httptest.NewRecorder()

	api.HandleCompleteOnboarding(badW, badReq)

	require.Equal(t, http.StatusBadRequest, badW.Code,
		"bad request with empty username must return 400")

	// Step 2: Verify the reservation was released by confirming IsComplete is still
	// false and a second valid request succeeds with 200 (not 409).
	// If the reservation were still held, ReserveComplete would return
	// ErrAlreadyComplete and the handler would return 409.
	require.False(t, api.onboardingMgr.IsComplete(),
		"onboarding must NOT be complete after a 400 response")

	goodBody := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`
	goodBody = hermeticOnboardBody(t, goodBody)
	goodReq := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(goodBody))
	goodReq.Header.Set("Content-Type", "application/json")
	goodW := httptest.NewRecorder()

	api.HandleCompleteOnboarding(goodW, goodReq)

	require.Equal(t, http.StatusOK, goodW.Code,
		"second valid onboarding request must succeed after reservation released (got %s)", goodW.Body.String())
}

// TestHandleOnboardingProbeProvider_WrongMethod ensures non-POST verbs are rejected.
func TestHandleOnboardingProbeProvider_WrongMethod(t *testing.T) {
	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)

	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/v1/onboarding/probe-provider", nil)
		w := httptest.NewRecorder()
		api.HandleOnboardingProbeProvider(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code,
			"verb %s must be rejected", m)
	}
	// silence unused context import if minimized tests drop it
	_ = context.Background
}

// --- HandleOnboardingProbeProvider auth-gap tests ---

// TestHandleOnboardingProbeProvider_PublicModelsBadKey is the primary regression
// test for the OpenRouter auth gap (SC-003): GET /models returns 200 with a model
// list (the provider's /models endpoint is public) but POST /chat/completions returns
// 401 (the key is invalid). The probe must respond success=false with a curated
// user-facing message and a validation.outcome of "invalid_key".
//
// BDD: Given a mock upstream where GET /models → 200 but POST /chat/completions → 401,
// When POST /api/v1/onboarding/probe-provider {"id":"openai","auth":"api_key","api_key":"bad-key"} is called,
// Then the response is HTTP 200 with success=false, validation.outcome="invalid_key",
// and the error does NOT contain the raw upstream body or the API key (SEC-16).
func TestHandleOnboardingProbeProvider_PublicModelsBadKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			// Public /models — returns 200 regardless of auth (the OpenRouter pattern).
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"openrouter/auto"},{"id":"openai/gpt-4"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			// Auth gate — rejects the bad key.
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)
	require.False(t, api.onboardingMgr.IsComplete())

	body := `{"id":"openai","auth":"api_key","api_key":"bad-key","api_base":"` + upstream.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleOnboardingProbeProvider(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// SC-003: probe must fail when /chat/completions returns 401.
	assert.Equal(t, false, resp["success"],
		"probe must fail when /chat/completions returns 401")
	// The error field carries the curated FR-7 message (SEC-16: no raw body, no key).
	errStr, _ := resp["error"].(string)
	assert.Contains(t, errStr, "rejected",
		"error must state the key was rejected (curated message)")
	assert.NotContains(t, errStr, "bad-key",
		"SEC-16: error must not contain the API key")
	assert.NotContains(t, errStr, "Invalid API key",
		"SEC-16: error must not echo the raw upstream body")
	// validation field must carry outcome=invalid_key (R-B / FR-013).
	validationMap, _ := resp["validation"].(map[string]any)
	require.NotNil(t, validationMap, "validation object must be present")
	assert.Equal(t, "invalid_key", validationMap["outcome"],
		"validation.outcome must be invalid_key for a 401")
}

// TestHandleOnboardingProbeProvider_PublicModelsGoodKey verifies the happy path:
// GET /models → 200 and POST /chat/completions → 200 → success=true with models.
//
// BDD: Given a mock upstream where both GET /models and POST /chat/completions → 200,
// When POST /api/v1/onboarding/probe-provider {"id":"openai","auth":"api_key","api_key":"good-key"} is called,
// Then the response is HTTP 200 with {"success":true,"models":[...]}.
func TestHandleOnboardingProbeProvider_PublicModelsGoodKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"openrouter/auto"},{"id":"openai/gpt-4"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)
	require.False(t, api.onboardingMgr.IsComplete())

	body := `{"id":"openai","auth":"api_key","api_key":"good-key","api_base":"` + upstream.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleOnboardingProbeProvider(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	models, _ := resp["models"].([]any)
	assert.NotEmpty(t, models, "models list must be returned on success")
}

// --- SEC-24 SSRF tests (fix #1) ---

// captureSlog redirects the default slog logger to an in-memory buffer for the
// duration of the test and returns the buffer plus a restore func. Used to assert
// that an observable WARN was emitted on a skip/inconclusive branch.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestHandleOnboardingProbeProvider_SSRFBlocksInternalEndpoint proves the SSRF
// gate: a probe whose caller-supplied `endpoint` points at an internal/loopback
// address is rejected with success=false "endpoint not allowed" BEFORE any
// outbound call — closing the pre-onboarding, unauthenticated SSRF hole.
//
// BDD: Given an SSRF checker with no internal allowlist,
// When POST /onboarding/probe-provider has api_base=http://127.0.0.1:<port>/,
// Then HTTP 200 with {"success":false,"error":"endpoint not allowed"} and the
//
//	upstream is never contacted.
func TestHandleOnboardingProbeProvider_SSRFBlocksInternalEndpoint(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++ // must remain 0 — the SSRF gate blocks before any request
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"}]}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)
	// Real SSRF checker, no internal allowlist → loopback (httptest binds 127.0.0.1) is blocked.
	api.ssrfChecker = security.NewSSRFChecker(nil)
	require.False(t, api.onboardingMgr.IsComplete())

	body := `{"id":"openai","auth":"api_key","api_key":"test-key","api_base":"` + upstream.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleOnboardingProbeProvider(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"], "internal endpoint must be rejected")
	assert.Equal(t, "endpoint not allowed", resp["error"])
	assert.Zero(t, hits, "upstream must never be contacted when SSRF blocks the endpoint")
}

// TestHandleOnboardingProbeProvider_SSRFAllowsAllowlistedLoopback verifies the
// gate does not over-block: when 127.0.0.1 is explicitly allowlisted the probe
// proceeds and reaches the (loopback) upstream, returning the model list.
func TestHandleOnboardingProbeProvider_SSRFAllowsAllowlistedLoopback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)
	// Allowlist loopback so the legitimate httptest target is permitted.
	api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1", "::1"})

	body := `{"id":"openai","auth":"api_key","api_key":"test-key","api_base":"` + upstream.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleOnboardingProbeProvider(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"], "allowlisted loopback must be permitted")
}

// --- /providers/{id}/test tests (fixes #3, #4, #5) ---

// newProviderTestAPI builds a restAPI wired with an unlocked credential store and a
// config.json on disk, suitable for exercising POST /providers/{id}/test. The
// onboarding manager is left incomplete so the endpoint serves without auth.
func newProviderTestAPI(t *testing.T, tmpDir, configJSON string) *restAPI {
	t.Helper()
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(configJSON), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return newOnboardingTestAPI(t, tmpDir, al)
}

// TestProviderTest_HonorsConfiguredAPIBase proves fix #3: POST /providers/{id}/test
// probes the provider entry's configured `api_base`, NOT the vendor default. The
// test points api_base at a local httptest server and asserts the auth probe hit
// THAT server (the openai vendor default api.openai.com is never contacted).
//
// BDD: Given a provider entry with api_base=http://127.0.0.1:<port> and a plaintext key,
// When POST /api/v1/providers/openai/test is called,
// Then the /chat/completions probe hits the configured base and success=true.
func TestProviderTest_HonorsConfiguredAPIBase(t *testing.T) {
	var probedPath string
	var probedHost bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probedHost = true
		probedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	// Provider entry: openai protocol, plaintext api_keys, and a CONFIGURED api_base
	// pointing at the local server (a regional / self-hosted gateway override).
	cfgJSON := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[` +
		`{"provider":"openai","model":"gpt-4","model_name":"gpt-4",` +
		`"api_keys":["sk-test"],"api_base":"` + upstream.URL + `"}]}`
	api := newProviderTestAPI(t, tmpDir, cfgJSON)
	// Allowlist loopback so the SSRF gate permits the legitimate test server.
	api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1", "::1"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/test", nil)
	w := httptest.NewRecorder()
	api.HandleProviders(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"], "probe against configured api_base must succeed (body=%s)", w.Body.String())
	assert.True(t, probedHost, "the auth probe must hit the CONFIGURED api_base, not the vendor default")
	assert.True(t, strings.HasSuffix(probedPath, "/chat/completions"),
		"the probe must POST to /chat/completions on the configured base, got %q", probedPath)
}

// TestProviderTest_ConfiguredAPIBaseRejectsBadKey proves the configured-base probe
// actually validates the key: an api_base server that returns 401 yields success=false.
func TestProviderTest_ConfiguredAPIBaseRejectsBadKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	cfgJSON := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[` +
		`{"provider":"openai","model":"gpt-4","model_name":"gpt-4",` +
		`"api_keys":["sk-bad"],"api_base":"` + upstream.URL + `"}]}`
	api := newProviderTestAPI(t, tmpDir, cfgJSON)
	api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1", "::1"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/test", nil)
	w := httptest.NewRecorder()
	api.HandleProviders(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"], "401 from the configured base must reject")
	errStr, _ := resp["error"].(string)
	// The centralized validator returns a curated message (SEC-16: raw status codes
	// must not appear in client-facing responses; only the classified outcome does).
	assert.Contains(t, errStr, "rejected",
		"error must state the key was rejected (curated FR-7 message)")
	assert.NotContains(t, errStr, "401",
		"SEC-16: raw HTTP status code must not appear in client-facing error")
	assert.NotContains(t, errStr, "sk-bad", "client error must never echo the API key")
}

// TestProviderTest_SSRFBlocksInternalAPIBase proves fix #1 on the /test path: a
// configured api_base pointing at an internal address is blocked before any
// outbound call when SSRF is active and the address is not allowlisted.
func TestProviderTest_SSRFBlocksInternalAPIBase(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	cfgJSON := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[` +
		`{"provider":"openai","model":"gpt-4","model_name":"gpt-4",` +
		`"api_keys":["sk-test"],"api_base":"` + upstream.URL + `"}]}`
	api := newProviderTestAPI(t, tmpDir, cfgJSON)
	// No allowlist → loopback api_base is blocked.
	api.ssrfChecker = security.NewSSRFChecker(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/test", nil)
	w := httptest.NewRecorder()
	api.HandleProviders(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
	assert.Equal(t, "endpoint not allowed", resp["error"])
	assert.Zero(t, hits, "SSRF must block before any outbound call to the internal api_base")
}

// TestProviderTest_CredentialVaultUnreadable proves fix #5: when the provider
// entry references an api_key_ref that the credential vault cannot resolve, the
// response is a DISTINCT "vault could not be read" message — not the misleading
// "no API key configured".
//
// BDD: Given a provider entry with api_key_ref but a LOCKED credential store,
// When POST /api/v1/providers/openai/test is called,
// Then success=false with an error mentioning the credential vault could not be read.
func TestProviderTest_CredentialVaultUnreadable(t *testing.T) {
	tmpDir := t.TempDir()
	cfgJSON := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[` +
		`{"provider":"openai","model":"gpt-4","model_name":"gpt-4",` +
		`"api_key_ref":"openai_API_KEY"}]}`
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(cfgJSON), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// Build the API WITHOUT an unlocked credential store: a LOCKED store makes
	// resolveCredentialRef fail, exercising the present-but-unresolvable branch.
	lockedStore := credentials.NewStore(tmpDir + "/credentials.json")
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     lockedStore,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/test", nil)
	w := httptest.NewRecorder()
	api.HandleProviders(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
	errStr, _ := resp["error"].(string)
	assert.Contains(t, errStr, "credential vault could not be read",
		"unresolvable ref must surface the vault-read error, not 'no API key configured'")
	assert.NotContains(t, errStr, "no API key configured")
}

// TestRefreshProviderModels_CredentialVaultUnreadable proves the
// whole-codebase-review Backend-High finding's sibling gap in
// refreshProviderModels: when the provider entry references an api_key_ref
// that the credential vault cannot resolve, POST
// /api/v1/providers/{id}/refresh-models must surface the DISTINCT "vault
// could not be read" message — not the misleading "no API key configured" —
// mirroring TestProviderTest_CredentialVaultUnreadable above (fix #5's
// pattern, replicated here per finding #2).
//
// BDD: Given a provider entry with api_key_ref but a LOCKED credential store,
// When POST /api/v1/providers/openai/refresh-models is called,
// Then 422 with an error mentioning the credential vault could not be read.
func TestRefreshProviderModels_CredentialVaultUnreadable(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
		Providers: []*config.ModelConfig{
			{ModelName: "gpt-4", Provider: "openai", Model: "gpt-4", APIKeyRef: "openai_API_KEY"},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// Build the API WITHOUT an unlocked credential store: a LOCKED store makes
	// resolveCredentialRef fail, exercising the present-but-unresolvable branch.
	lockedStore := credentials.NewStore(tmpDir + "/credentials.json")
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     lockedStore,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/refresh-models", nil)
	w := httptest.NewRecorder()
	api.refreshProviderModels(w, req, "openai")

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errStr, _ := resp["error"].(string)
	assert.Contains(t, errStr, "credential vault could not be read",
		"unresolvable ref must surface the vault-read error, not 'no API key configured'")
	assert.NotContains(t, errStr, "no API key configured")
}

// TestProviderTest_CredentialRefNotFound proves the fix for the credential-resolution
// conflation finding: a resolveCredentialRef failure caused by the ref simply not
// existing in the store (*credentials.NotFoundError — e.g. a stale/deleted credential
// or a hand-edited config with a typo'd ref) must NOT surface the "vault could not be
// read — unlock and retry" message, since unlocking changes nothing when the ref
// genuinely isn't there. It must instead surface a distinct "no longer exists /
// re-enter" message. This is the counterpart to TestProviderTest_CredentialVaultUnreadable
// above, which covers the locked-store (transient, unlock-and-retry-IS-correct) case.
//
// BDD: Given a provider entry with api_key_ref pointing at a name absent from an
// UNLOCKED credential store,
// When POST /api/v1/providers/openai/test is called,
// Then success=false with an error saying the ref no longer exists — never "unlock and
// retry".
func TestProviderTest_CredentialRefNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfgJSON := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[` +
		`{"provider":"openai","model":"gpt-4","model_name":"gpt-4",` +
		`"api_key_ref":"openai_API_KEY"}]}`
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(cfgJSON), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// Build the API with an UNLOCKED store that has never had "openai_API_KEY"
	// written to it — resolveCredentialRef must fail with *credentials.NotFoundError,
	// NOT ErrStoreLocked, exercising the ref-genuinely-absent branch.
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	unlockedStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(unlockedStore))
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     unlockedStore,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/test", nil)
	w := httptest.NewRecorder()
	api.HandleProviders(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
	errStr, _ := resp["error"].(string)
	assert.Contains(t, errStr, "no longer exists",
		"a ref absent from an unlocked store must surface the not-found message")
	assert.NotContains(t, errStr, "unlock and retry",
		"unlock-and-retry is WRONG advice when the ref simply doesn't exist in the store")
	assert.NotContains(t, errStr, "no API key configured")
}

// TestRefreshProviderModels_CredentialRefNotFound is the refresh-models counterpart to
// TestProviderTest_CredentialRefNotFound: a ref absent from an UNLOCKED store must
// surface the "no longer exists / re-enter" message, never "unlock and retry".
//
// BDD: Given a provider entry with api_key_ref pointing at a name absent from an
// UNLOCKED credential store,
// When POST /api/v1/providers/openai/refresh-models is called,
// Then 422 with an error saying the ref no longer exists — never "unlock and retry".
func TestRefreshProviderModels_CredentialRefNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
		Providers: []*config.ModelConfig{
			{ModelName: "gpt-4", Provider: "openai", Model: "gpt-4", APIKeyRef: "openai_API_KEY"},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// Build the API with an UNLOCKED store that has never had "openai_API_KEY"
	// written to it — resolveCredentialRef must fail with *credentials.NotFoundError,
	// NOT ErrStoreLocked, exercising the ref-genuinely-absent branch.
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	unlockedStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(unlockedStore))
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     unlockedStore,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/refresh-models", nil)
	w := httptest.NewRecorder()
	api.refreshProviderModels(w, req, "openai")

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errStr, _ := resp["error"].(string)
	assert.Contains(t, errStr, "no longer exists",
		"a ref absent from an unlocked store must surface the not-found message")
	assert.NotContains(t, errStr, "unlock and retry",
		"unlock-and-retry is WRONG advice when the ref simply doesn't exist in the store")
	assert.NotContains(t, errStr, "no API key configured")
}

// TestHandleOnboardingProbeProvider_EmptyModelsWarns proves fix #4: when the
// upstream /models list is empty the probe still returns success=true (the catalog
// is simply empty) but the skip is observable via a WARN — instead of a silent pass
// that would defeat the auth-verification intent for empty-catalog providers.
func TestHandleOnboardingProbeProvider_EmptyModelsWarns(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`)) // empty model list
	}))
	defer upstream.Close()

	logBuf := captureSlog(t)

	tmpDir := t.TempDir()
	api := newOnboardingTestAPI(t, tmpDir, nil)
	api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1", "::1"})

	body := `{"id":"openai","auth":"api_key","api_key":"test-key","api_base":"` + upstream.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleOnboardingProbeProvider(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// Empty catalog is not a hard failure — success stays true (don't block onboarding).
	assert.Equal(t, true, resp["success"], "empty model list must not hard-fail the probe")
	// But the skip must be observable.
	assert.Contains(t, logBuf.String(), "provider returned no models",
		"an empty model list must emit an observable WARN that the key was not verified")
}

// --- provider API-key validation at first-run setup ---
//
// Regression coverage for the bug where HandleCompleteOnboarding checked only
// that provider.api_key was non-empty and then stored it: a typo'd or revoked
// key produced a 200, a "welcome" screen, and an install whose agent could not
// answer a single message. The provider EDIT path (PUT /api/v1/providers/{id})
// had probed the key all along — the strictest moment in the product was the
// only one with no check.
//
// The three tests below pin the whole accept/reject policy, which is deliberately
// asymmetric: only "the provider told us this key is wrong" rejects. See the
// ACCEPT/REJECT POLICY note in rest_onboarding.go for why anything else blocking
// would be worse than the bug being fixed.

// TestHandleCompleteOnboarding_ValidKeyAccepted pins the happy path: a key the
// provider accepts completes onboarding and is persisted.
//
// BDD: Given a provider whose /chat/completions accepts the submitted key,
// When POST /api/v1/onboarding/complete is called,
// Then 200, no warning, and the key is in the encrypted credential store.
func TestHandleCompleteOnboarding_ValidKeyAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	// A provider that answers the probe successfully — i.e. says the key is good.
	upstream := startFakeProviderUpstream(t)
	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-good-key"},"admin":{"username":"admin","password":"secret123"}}`,
		upstream,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	require.Equal(t, http.StatusOK, w.Code, "a key the provider accepts must complete onboarding (body=%s)", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"])
	assert.Nil(t, resp["warning"], "a valid key must not produce a warning")

	stored, err := api.credStore.Get("openai_API_KEY")
	require.NoError(t, err, "the accepted key must be in the credential store")
	assert.Equal(t, "sk-good-key", stored)
}

// TestHandleCompleteOnboarding_InvalidKeyRejectedAndNotStored is the core
// regression test: a key the provider rejects must not complete onboarding, and
// must not be written anywhere.
//
// BDD: Given a provider whose /chat/completions returns 401 invalid_api_key,
// When POST /api/v1/onboarding/complete is called,
// Then 400 with a message naming the provider, the credential store does NOT
// gain the entry, onboarding is NOT marked complete, and no admin user is
// written to config.json — so the operator can retry with a corrected key.
func TestHandleCompleteOnboarding_InvalidKeyRejectedAndNotStored(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	// Stand-in provider that rejects the credential the way a real
	// OpenAI-compatible endpoint does.
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			// The catalog is deliberately served WITHOUT auth (OpenRouter really
			// does this) — proving the check does not rely on /models to detect
			// a bad key.
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`))
	}))
	defer upstreamSrv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-typo"},"admin":{"username":"admin","password":"secret123"}}`,
		upstreamSrv.URL,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a key the provider rejects must not complete onboarding (body=%s)", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errMsg, _ := resp["error"].(string)
	assert.Contains(t, errMsg, "rejected", "the message must say the key was rejected, got %q", errMsg)
	assert.Contains(t, errMsg, "OpenAI", "the message must name the provider, got %q", errMsg)
	assert.NotContains(t, errMsg, "sk-typo", "SEC-16: the message must never echo the key")

	// The rejected key must not exist anywhere.
	_, credErr := api.credStore.Get("openai_API_KEY")
	require.Error(t, credErr, "a rejected key must NOT be written to the credential store")

	assert.False(t, api.onboardingMgr.IsComplete(),
		"a rejected key must leave onboarding retryable, not complete")

	cfgData, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(cfgData), "sk-typo", "the rejected key must not reach config.json")
	assert.NotContains(t, string(cfgData), "password_hash",
		"no admin user may be created when the provider key is rejected")
}

// TestHandleCompleteOnboarding_UnreachableProviderStillCompletes pins the other
// half of the policy, and the reason it is not simply "validate everything":
// onboarding is the ONLY door into the product, so a provider we cannot reach
// right now must NOT be treated as a bad key. If it were, a flaky network would
// make Omnipus uninstallable — a worse failure than the bug this check fixes.
//
// BDD: Given the provider endpoint refuses connections (network down / DNS
// failure / provider outage, all classified providers.OutcomeUnreachable),
// When POST /api/v1/onboarding/complete is called,
// Then 200, the key is stored as entered, and the response carries a warning
// saying the key could not be checked — never an "invalid key" rejection.
func TestHandleCompleteOnboarding_UnreachableProviderStillCompletes(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	// A server that is started (so we get a real, currently-free port) and then
	// immediately closed: every probe gets connection-refused, which is exactly
	// what classify() maps to OutcomeUnreachable.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-cannot-check"},"admin":{"username":"admin","password":"secret123"}}`,
		deadURL,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"an unreachable provider must NOT block first-run setup (body=%s)", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"], "onboarding must still issue a token")

	warning, _ := resp["warning"].(string)
	require.NotEmpty(t, warning, "the operator must be told the key could not be checked")
	assert.Contains(t, warning, "Couldn't reach",
		"the warning must say the provider was unreachable, not that the key is bad, got %q", warning)

	stored, err := api.credStore.Get("openai_API_KEY")
	require.NoError(t, err, "an unchecked key must still be stored as entered")
	assert.Equal(t, "sk-cannot-check", stored)

	assert.True(t, api.onboardingMgr.IsComplete(), "onboarding must be marked complete")
}

// --- coverage for the two branches that diverge from PUT (review finding D5) ---
//
// newOnboardingTestAPI deliberately leaves ssrfChecker nil (see its own comment),
// so none of the tests above ever exercise probeSkipReason != "". The two tests
// below wire a real *security.SSRFChecker so both skip branches — "no endpoint
// resolved" (a protocol like azure with no vendor default and no operator
// override) and "ssrf blocked" (a resolved base the SSRF gate refuses) — are
// pinned, along with the provider_key_validation_skipped audit event and the
// D4 fix that both branches now set a user-visible warning instead of
// completing onboarding with zero signal that the key was never checked.

// TestHandleCompleteOnboarding_NoEndpointResolvedStillCompletesWithWarning covers
// the "no endpoint resolved" skip branch: azure is a known protocol (see
// providers.IsKnownProtocol) with no vendor default base (providers.GetDefaultAPIBase
// falls to its `default: return ""` case for it) and the request supplies no
// endpoint override either, so probeBase resolves to "". There is nothing to
// probe — no outbound call is possible, hermetic by construction.
func TestHandleCompleteOnboarding_NoEndpointResolvedStillCompletesWithWarning(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, MaxSizeBytes: 1 << 20, RetentionDays: 1})
	require.NoError(t, err, "audit logger must initialize")
	t.Cleanup(func() { _ = auditLogger.Close() })
	api.auditor = auditLogger

	body := `{"provider":{"auth_method":"api_key","id":"azure","api_key":"sk-azure-key"},"admin":{"username":"admin","password":"secret123"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"a provider with no resolvable endpoint must NOT block first-run setup (body=%s)", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"], "onboarding must still issue a token")

	warning, _ := resp["warning"].(string)
	require.NotEmpty(t, warning,
		"D4: a skipped probe must still warn the operator — otherwise it is indistinguishable from a verified key")
	assert.Contains(t, warning, "endpoint",
		"the warning must explain that no endpoint was available to check, got %q", warning)

	stored, err := api.credStore.Get("azure_API_KEY")
	require.NoError(t, err, "an unchecked key must still be stored as entered")
	assert.Equal(t, "sk-azure-key", stored)
	assert.True(t, api.onboardingMgr.IsComplete(), "onboarding must be marked complete")

	_ = auditLogger.Close()
	entries := readAuditLog(t, auditDir)
	var found bool
	for _, line := range entries {
		if line["event"] == "provider_key_validation_skipped" {
			found = true
			details, _ := line["details"].(map[string]any)
			assert.Equal(t, "azure", details["provider"])
			assert.Equal(t, "no endpoint resolved", details["reason"])
			assert.Equal(t, "onboarding", details["source"])
		}
	}
	assert.True(t, found, "provider_key_validation_skipped audit event must be present with reason=%q",
		"no endpoint resolved")
}

// TestHandleCompleteOnboarding_SSRFBlockedProbeStillCompletesWithWarning covers
// the "ssrf blocked" skip branch AND, simultaneously, the real GetDefaultAPIBase
// (no operator-supplied endpoint) resolution path that review finding D5 flagged
// as having zero coverage once every other test in this file started injecting
// `endpoint` via hermeticOnboardBody: the request below supplies NO endpoint at
// all, so probeBase comes from providers.GetDefaultAPIBase("litellm"), which is
// itself a loopback address (http://localhost:4000/v1 — see the ollama/litellm
// case list in factory_provider.go, the exact "operators the divergence was
// designed to protect" the D4 review comment names). A real, unallowlisted
// *security.SSRFChecker blocks it before any outbound call — hermetic by
// construction, and it proves the vendor-default branch resolves to a real,
// loopback-shaped base rather than the empty string.
func TestHandleCompleteOnboarding_SSRFBlockedProbeStillCompletesWithWarning(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)
	// Real SSRF checker, no loopback allowlist — blocks localhost:4000 by default.
	api.ssrfChecker = security.NewSSRFChecker(nil)

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, MaxSizeBytes: 1 << 20, RetentionDays: 1})
	require.NoError(t, err, "audit logger must initialize")
	t.Cleanup(func() { _ = auditLogger.Close() })
	api.auditor = auditLogger

	// No `endpoint` field at all — probeBase must come from GetDefaultAPIBase.
	body := `{"provider":{"auth_method":"api_key","id":"litellm","api_key":"sk-litellm-key"},"admin":{"username":"admin","password":"secret123"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"an SSRF-blocked probe must NOT block first-run setup (body=%s)", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"], "onboarding must still issue a token")

	warning, _ := resp["warning"].(string)
	require.NotEmpty(t, warning,
		"D4: an SSRF-skipped probe must still warn the operator — otherwise it is indistinguishable from a verified key")
	assert.Contains(t, warning, "endpoint",
		"the warning must explain the endpoint could not be checked, got %q", warning)

	stored, err := api.credStore.Get("litellm_API_KEY")
	require.NoError(t, err, "an unchecked key must still be stored as entered")
	assert.Equal(t, "sk-litellm-key", stored)
	assert.True(t, api.onboardingMgr.IsComplete(), "onboarding must be marked complete")

	_ = auditLogger.Close()
	entries := readAuditLog(t, auditDir)
	var found bool
	for _, line := range entries {
		if line["event"] == "provider_key_validation_skipped" {
			found = true
			details, _ := line["details"].(map[string]any)
			assert.Equal(t, "litellm", details["provider"])
			assert.Equal(t, "ssrf blocked", details["reason"])
			assert.Equal(t, "onboarding", details["source"])
		}
	}
	assert.True(t, found, "provider_key_validation_skipped audit event must be present with reason=%q",
		"ssrf blocked")
}

// TestHandleCompleteOnboarding_UsesLiveCatalogForProbeModel is the regression
// test for review finding D6: HandleCompleteOnboarding previously called
// providers.ValidateKey without a Catalog, so providers.pickProbeModel returned
// providers.probeModelDefaults' hardcoded slug for openrouter
// ("meta-llama/llama-3.1-8b-instruct") immediately — WITHOUT ever calling
// providers.FetchModels — whenever that slug existed in the rules table,
// regardless of whether it still exists on the provider's live catalog.
//
// This stand-in simulates that slug having been retired: /models does not list
// it, but does list a different chat-capable model. If the handler still probes
// the hardcoded slug, /chat/completions 404s and classify() maps that to
// Unreachable — a false "couldn't check" warning for a perfectly good key. With
// the live catalog wired through, pickProbeModel falls back to the first
// chat-capable catalog entry instead, the probe succeeds, and onboarding
// completes with NO warning (OutcomeValid).
func TestHandleCompleteOnboarding_UsesLiveCatalogForProbeModel(t *testing.T) {
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	const retiredDefault = "meta-llama/llama-3.1-8b-instruct"
	const liveModel = "some-vendor/still-live-chat-model"

	var probedModels []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			// The rules-table default is deliberately ABSENT — simulating a
			// retired slug — alongside one still-live chat-capable model.
			_, _ = w.Write([]byte(`{"data":[{"id":"` + liveModel + `"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			var payload struct {
				Model string `json:"model"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &payload)
			probedModels = append(probedModels, payload.Model)
			if payload.Model == retiredDefault {
				// The real OpenRouter behavior for an unknown/retired slug.
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"model not found","code":404}}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openrouter","api_key":"sk-or-v1-still-good"},"admin":{"username":"admin","password":"secret123"}}`,
		upstream.URL,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"a good key must still complete onboarding even when the hardcoded default slug is retired (body=%s)", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp["warning"],
		"D6: with the live catalog wired through, the probe must use a model that is actually live — no warning expected")

	require.NotEmpty(t, probedModels, "the probe must have hit /chat/completions at least once")
	assert.NotContains(t, probedModels, retiredDefault,
		"D6: the handler must not probe the retired rules-table slug when a live catalog is available; probed=%v", probedModels)
	assert.Contains(t, probedModels, liveModel,
		"D6: the handler must fall back to a live catalog entry; probed=%v", probedModels)
}
