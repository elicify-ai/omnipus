//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// reAuthTestUser is the user the re-auth tests authenticate as.
const (
	reAuthTestUsername = "admin"
	reAuthTestPassword = "correcthorse"
)

// newReAuthTestAPI builds a restAPI with one admin user whose password is
// reAuthTestPassword, plus a request pre-loaded with that user in context.
func newReAuthTestAPI(t *testing.T) (*restAPI, *config.UserConfig) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(reAuthTestPassword), bcrypt.DefaultCost)
	require.NoError(t, err)
	api := newRestAPIWithSingleUser(t, reAuthTestUsername, string(hash))
	user := &config.UserConfig{
		Username:     reAuthTestUsername,
		PasswordHash: string(hash),
	}
	return api, user
}

// doReAuth posts a re-auth request as user and returns the recorder.
func doReAuth(api *restAPI, user *config.UserConfig, password string) *httptest.ResponseRecorder {
	body := `{"password":` + jsonString(password) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reauth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	w := httptest.NewRecorder()
	api.HandleReAuth(w, req)
	return w
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestReAuth_CorrectPassword_MintsToken verifies the consent primitive: a
// correct password yields a verified token (FR-12.2, TDD #7 positive path).
func TestReAuth_CorrectPassword_MintsToken(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	w := doReAuth(api, user, reAuthTestPassword)
	require.Equal(t, http.StatusOK, w.Code, "correct password must return 200")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["verified"])
	assert.NotEmpty(t, resp["token"], "a consent token must be minted")
	assert.EqualValues(t, 300, resp["expires_in"])
}

// TestReAuth_WrongPassword_Rejected verifies a wrong password is rejected 401
// and mints no token (FR-12.2, TDD #7 negative path).
func TestReAuth_WrongPassword_Rejected(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	w := doReAuth(api, user, "wrongpassword")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "wrong password must return 401")
	assert.NotContains(t, w.Body.String(), "token", "no token may be issued on a wrong password")
}

// TestReAuth_Unauthenticated_Rejected verifies the endpoint requires an
// authenticated user in context.
func TestReAuth_Unauthenticated_Rejected(t *testing.T) {
	api, _ := newReAuthTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reauth",
		strings.NewReader(`{"password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleReAuth(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestReAuthToken_SingleUse verifies a minted consent token is consumed on first
// use and rejected on the second (no replay).
func TestReAuthToken_SingleUse(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	w := doReAuth(api, user, reAuthTestPassword)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	token := resp["token"].(string)

	store := api.reauthStoreOrInit()
	assert.True(t, store.consume(token, reAuthTestUsername), "first consume must succeed")
	assert.False(t, store.consume(token, reAuthTestUsername), "second consume must fail (single-use)")
}

// TestReAuthToken_WrongUser_Rejected verifies a token minted for one user cannot
// be consumed under another username.
func TestReAuthToken_WrongUser_Rejected(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	w := doReAuth(api, user, reAuthTestPassword)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	token := resp["token"].(string)
	assert.False(t, api.reauthStoreOrInit().consume(token, "someone-else"))
}

// --- Integrations ---

// putIntegration issues a PUT /integrations/providers/{id} with the given body,
// optional re-auth header, and the test user in context.
func putIntegration(api *restAPI, user *config.UserConfig, id, body, reauthToken string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/integrations/providers/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if reauthToken != "" {
		req.Header.Set(reAuthHeader, reauthToken)
	}
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	w := httptest.NewRecorder()
	api.HandleIntegrationProviders(w, req)
	return w
}

// TestIntegrationProviders_List verifies the GET catalog surfaces both the
// search and voice providers (FR-12.1).
func TestIntegrationProviders_List(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/providers", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	w := httptest.NewRecorder()
	api.HandleIntegrationProviders(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Search []map[string]any `json:"search"`
		Voice  []map[string]any `json:"voice"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Search, "search providers must be listed")
	assert.NotEmpty(t, resp.Voice, "voice providers must be listed")

	// DuckDuckGo is keyless → always configured; it is the default active search.
	var ddg map[string]any
	for _, p := range resp.Search {
		if p["id"] == "duckduckgo" {
			ddg = p
		}
	}
	require.NotNil(t, ddg, "duckduckgo must be present")
	assert.Equal(t, true, ddg["configured"], "keyless provider must be configured")
	assert.Equal(t, false, ddg["requires_key"])
}

// TestIntegrationProviders_List_AllProviders verifies the expanded catalog
// surfaces every implemented search (Brave/Tavily/Perplexity/DuckDuckGo/
// SearXNG/GLM/Baidu) and voice (ElevenLabs/Groq/audio-model) provider
// (FR-12.1). The keyed providers that have no key stored report configured=false.
func TestIntegrationProviders_List_AllProviders(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/providers", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	w := httptest.NewRecorder()
	api.HandleIntegrationProviders(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Search []map[string]any `json:"search"`
		Voice  []map[string]any `json:"voice"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	wantSearch := map[string]bool{
		"brave": true, "tavily": true, "perplexity": true,
		"duckduckgo": true, "searxng": true, "glm": true, "baidu": true,
	}
	gotSearch := map[string]bool{}
	for _, p := range resp.Search {
		gotSearch[p["id"].(string)] = true
	}
	for id := range wantSearch {
		assert.True(t, gotSearch[id], "search provider %q must be in the catalog", id)
	}

	wantVoice := map[string]bool{
		"elevenlabs": true, "groq": true, "audio-model": true,
	}
	gotVoice := map[string]bool{}
	for _, p := range resp.Voice {
		gotVoice[p["id"].(string)] = true
	}
	for id := range wantVoice {
		assert.True(t, gotVoice[id], "voice provider %q must be in the catalog", id)
	}

	// SearXNG is keyless but needs a base_url; with no base_url set it must
	// report configured=false (distinct from DuckDuckGo, which is always on).
	for _, p := range resp.Search {
		if p["id"] == "searxng" {
			assert.Equal(t, false, p["requires_key"])
			assert.Equal(t, false, p["configured"], "searxng without base_url must be unconfigured")
		}
	}
	// audio-model is keyless but needs voice.model_name; unconfigured by default.
	for _, p := range resp.Voice {
		if p["id"] == "audio-model" {
			assert.Equal(t, false, p["requires_key"])
			assert.Equal(t, false, p["configured"], "audio-model without model_name must be unconfigured")
		}
	}
}

// TestIntegrationProviderUpdate_SearXNGActivation_NeedsBaseURL verifies that
// activating SearXNG without a base_url is rejected 400 (keyless but with a
// prerequisite), and that a valid re-auth token is still required first.
func TestIntegrationProviderUpdate_SearXNGActivation_NeedsBaseURL(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	// Re-auth first so the ONLY rejection is the missing base_url.
	rw := doReAuth(api, user, reAuthTestPassword)
	require.Equal(t, http.StatusOK, rw.Code)
	var rresp map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &rresp))
	token := rresp["token"].(string)

	w := putIntegration(api, user, "searxng", `{"kind":"search","active":true}`, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, strings.ToLower(w.Body.String()), "base url")
}

// TestIntegrationProviderUpdate_RequiresReAuth verifies that a sensitive
// integration change WITHOUT a valid re-auth token is rejected with 403 — the
// core negative test for the new consent primitive (FR-12.2). This is NOT the
// RequireNotBypass 503 dev-bypass guard.
func TestIntegrationProviderUpdate_RequiresReAuth(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	// Activate the keyless DuckDuckGo search provider — no API key needed, so
	// the ONLY thing that can reject this request is the missing re-auth token.
	w := putIntegration(api, user, "duckduckgo", `{"kind":"search","active":true}`, "")
	assert.Equal(t, http.StatusForbidden, w.Code,
		"sensitive integration change without a re-auth token must be 403")
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestIntegrationProviderUpdate_WithReAuth_Succeeds verifies the happy path:
// re-auth then a keyless activation succeeds and persists.
func TestIntegrationProviderUpdate_WithReAuth_Succeeds(t *testing.T) {
	api, user := newReAuthTestAPI(t)

	// 1. Re-auth to mint a consent token.
	rw := doReAuth(api, user, reAuthTestPassword)
	require.Equal(t, http.StatusOK, rw.Code)
	var rresp map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &rresp))
	token := rresp["token"].(string)

	// 2. Use it on the sensitive PUT (keyless provider → no credential store needed).
	w := putIntegration(api, user, "duckduckgo", `{"kind":"search","active":true}`, token)
	require.Equal(t, http.StatusOK, w.Code, "valid re-auth must allow the change; body=%s", w.Body.String())

	// The returned catalog must report duckduckgo as the active search provider.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "duckduckgo", resp["active_search"])
}

// TestIntegrationProviderUpdate_KindMismatch_Rejected verifies a body whose kind
// does not match the provider's kind is rejected 400.
func TestIntegrationProviderUpdate_KindMismatch_Rejected(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	w := putIntegration(api, user, "brave", `{"kind":"voice","active":true}`, "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestIntegrationProviderUpdate_UnknownProvider_Rejected verifies an unknown id
// is rejected 400.
func TestIntegrationProviderUpdate_UnknownProvider_Rejected(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	w := putIntegration(api, user, "nonsense", `{"kind":"search","active":true}`, "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- Transcribe ---

// TestTranscribe_NoTranscriber_ServiceUnavailable verifies the composer-mic
// endpoint returns 503 when no transcriber is configured (FR-12.1 degraded path).
func TestTranscribe_NoTranscriber_ServiceUnavailable(t *testing.T) {
	api, user := newReAuthTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", strings.NewReader("x"))
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	w := httptest.NewRecorder()
	api.HandleTranscribe(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"no configured transcriber must yield 503")
}
