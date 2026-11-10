// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// withDeviceCodeVendor swaps oauthConfigFor so openai-chatgpt/xai point at an
// httptest.Server instead of the real vendor, and restores it on cleanup.
func withDeviceCodeVendor(t *testing.T, server *httptest.Server) {
	t.Helper()
	prev := oauthConfigFor
	oauthConfigFor = func(providerID string) (auth.OAuthProviderConfig, error) {
		switch providerID {
		case "openai-chatgpt", "xai":
			return auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "test-client"}, nil
		default:
			return prev(providerID)
		}
	}
	t.Cleanup(func() { oauthConfigFor = prev })
}

// deviceCodeVendorMux builds the two endpoints RequestDeviceCode/pollDeviceCode
// need. pollState controls what /deviceauth/token answers on each call.
func deviceCodeVendorMux(t *testing.T, pollState *string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"device_auth_id": "vendor_das_1", "user_code": "WDJB-MJHT", "interval": 1}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		switch *pollState {
		case "pending":
			http.Error(w, `{"error":"authorization_pending"}`, http.StatusBadRequest)
		case "signed_in":
			resp := map[string]any{"authorization_code": "code1", "code_verifier": "verif1"}
			require.NoError(t, json.NewEncoder(w).Encode(resp))
		case "slow_down":
			// RFC 8628 §3.5 back-off — still pending, poll less often.
			http.Error(w, `{"error":"slow_down"}`, http.StatusBadRequest)
		default:
			http.Error(w, "unexpected state", http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"access_token":  "vendor-access-token",
			"refresh_token": "vendor-refresh-token",
			"expires_in":    3600,
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	})
	return mux
}

// doJSON drives api.HandleProviders directly (bypassing the real HTTP
// middleware chain), so it injects the config snapshot configSnapshotMiddleware
// would normally set — RequireNotBypass (inside requireAdminAuthz) fails
// closed with 503 "disabled while dev_mode_bypass is active" when it finds
// no snapshot in context, exactly the same as a genuinely bypass-active
// request, so a bare httptest.NewRequest here would misreport every sign-in
// route as bypass-blocked.
func doJSON(t *testing.T, api *restAPI, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reader)
	cfg := api.agentLoop.GetConfig()
	r = r.WithContext(context.WithValue(r.Context(), configContextKey{}, cfg))
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, r))
	return w
}

func doJSONWithUser(t *testing.T, api *restAPI, method, path string, body any, username string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reader)
	cfg := api.agentLoop.GetConfig()
	ctx := context.WithValue(r.Context(), configContextKey{}, cfg)
	if username != "" {
		ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: username})
	}
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, r))
	return w
}

func TestSignInStart_DeviceCode(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	state := "pending"
	server := httptest.NewServer(deviceCodeVendorMux(t, &state))
	defer server.Close()
	withDeviceCodeVendor(t, server)

	w := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp gen.SignInStartResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, gen.SignInStartResponseMethodDeviceCode, resp.Method)
	assert.Equal(t, "WDJB-MJHT", resp.UserCode)
	assert.NotEmpty(t, resp.DeviceAuthId)
	assert.NotContains(t, resp.DeviceAuthId, "vendor_das_1",
		"the vendor's own device_auth_id must never leave the gateway (FR-008)")
	assert.GreaterOrEqual(t, resp.IntervalSeconds, 1)
	assert.False(t, resp.ExpiresAt.IsZero())
}

func TestSignIn_RefusedForKeyOnly400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := doJSON(t, api, http.MethodPost, "/api/v1/providers/openrouter/sign-in", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "provider does not support sign-in", body["error"])
}

func TestSignInStart_XAI_NoClientID400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	t.Setenv(auth.EnvXAIOAuthClientID, "")
	require.NoError(t, os.Unsetenv(auth.EnvXAIOAuthClientID))

	w := doJSON(t, api, http.MethodPost, "/api/v1/providers/xai/sign-in", nil)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "provider does not support sign-in", body["error"])
}

func TestSignInPoll_PendingThenSignedIn(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	state := "pending"
	server := httptest.NewServer(deviceCodeVendorMux(t, &state))
	defer server.Close()
	withDeviceCodeVendor(t, server)

	startW := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	require.Equal(t, http.StatusOK, startW.Code)
	var startResp gen.SignInStartResponse
	require.NoError(t, json.Unmarshal(startW.Body.Bytes(), &startResp))
	pollBody := gen.SignInPollRequest{DeviceAuthId: startResp.DeviceAuthId}

	pollW1 := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll", pollBody)
	require.Equal(t, http.StatusOK, pollW1.Code, pollW1.Body.String())
	var pollResp1 gen.SignInPollResponse
	require.NoError(t, json.Unmarshal(pollW1.Body.Bytes(), &pollResp1))
	assert.Equal(t, gen.SignInPollResponseStatePending, pollResp1.State)

	state = "signed_in"
	pollW2 := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll", pollBody)
	require.Equal(t, http.StatusOK, pollW2.Code, pollW2.Body.String())
	var pollResp2 gen.SignInPollResponse
	require.NoError(t, json.Unmarshal(pollW2.Body.Bytes(), &pollResp2))
	assert.Equal(t, gen.SignInPollResponseStateSignedIn, pollResp2.State)

	// The credential must actually be stored before the response (FR-044).
	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	raw, getErr := store.Get(credentials.OAuthEntryName("openai"))
	require.NoError(t, getErr)
	assert.Contains(t, raw, "vendor-access-token")
	assert.Contains(t, raw, "vendor-refresh-token")

	// Single-use: a third poll on the same (now-resolved) handle 404s.
	pollW3 := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll", pollBody)
	assert.Equal(t, http.StatusNotFound, pollW3.Code)
}

func TestSignInPoll_Expired404(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	body := gen.SignInPollRequest{DeviceAuthId: "das_never_existed"}
	w := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll", body)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSignInStatus_Store(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// This test pins STORAGE semantics, and its label assertions need an
	// authenticated caller: an anonymous caller inside the FR-050 pre-auth
	// window gets a REDUCED answer with no account_label. doJSON sets no
	// user, so wrap.
	doJSONAsAdmin := func(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		return doJSONWithUser(t, api, method, path, body, "admin")
	}

	// Nothing stored -> not_signed_in.
	w1 := doJSONAsAdmin(t, http.MethodGet, "/api/v1/providers/openai-chatgpt/sign-in/status", nil)
	require.Equal(t, http.StatusOK, w1.Code)
	var status1 gen.SignInStatus
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &status1))
	assert.Equal(t, gen.SignInStatusStateNotSignedIn, status1.State)

	// A fresh, unexpired stored credential -> signed_in with account_label.
	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	freshBlob, err := json.Marshal(map[string]any{
		"access_token": "tok1", "account_id": "acc_1",
		"expires_at": "2099-01-01T00:00:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(freshBlob)))

	w2 := doJSONAsAdmin(t, http.MethodGet, "/api/v1/providers/openai-chatgpt/sign-in/status", nil)
	require.Equal(t, http.StatusOK, w2.Code)
	var status2 gen.SignInStatus
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &status2))
	assert.Equal(t, gen.SignInStatusStateSignedIn, status2.State)
	require.NotNil(t, status2.AccountLabel)
	assert.Equal(t, "acc_1", *status2.AccountLabel)

	// Expired with no refresh token -> expired.
	expiredBlob, err := json.Marshal(map[string]any{
		"access_token": "tok2", "account_id": "acc_1",
		"expires_at": "2000-01-01T00:00:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(expiredBlob)))

	w3 := doJSONAsAdmin(t, http.MethodGet, "/api/v1/providers/openai-chatgpt/sign-in/status", nil)
	require.Equal(t, http.StatusOK, w3.Code)
	var status3 gen.SignInStatus
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &status3))
	assert.Equal(t, gen.SignInStatusStateExpired, status3.State)
}

func TestSignOut_DeletesOAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	blob, err := json.Marshal(map[string]any{"access_token": "tok", "account_id": "acc"})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(blob)))

	w := doJSON(t, api, http.MethodDelete, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result gen.OperationResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.True(t, result.Success)

	_, getErr := store.Get(credentials.OAuthEntryName("openai"))
	require.Error(t, getErr, "the entry must actually be gone")

	statusW := doJSON(t, api, http.MethodGet, "/api/v1/providers/openai-chatgpt/sign-in/status", nil)
	var status gen.SignInStatus
	require.NoError(t, json.Unmarshal(statusW.Body.Bytes(), &status))
	assert.Equal(t, gen.SignInStatusStateNotSignedIn, status.State)
}

func TestSignOut_NotFoundIsSuccess(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := doJSON(t, api, http.MethodDelete, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result gen.OperationResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.True(t, result.Success, "FR-048: deleting an absent entry is still success")
}

// TestSignIn_PreAuthOnlyDuringOnboarding — FR-050. newTestRestAPIWithHome's
// onboarding.NewManager(tmpDir) starts NOT complete, so the request above
// (no UserContextKey in context) already exercises the pre-auth-allowed
// path; this test explicitly marks onboarding complete and asserts the
// same unauthenticated request is now refused.
func TestSignIn_PreAuthOnlyDuringOnboarding(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	require.False(t, api.onboardingMgr.IsComplete(), "precondition: fresh manager starts incomplete")

	// Pre-auth: reachable while onboarding is incomplete. The subject id is
	// a REMOVED provider id (WU-24), which makes the admission distinction
	// sharp without any vendor seam: inside the window the request is
	// ADMITTED past authentication and the handler itself refuses it (400),
	// while the same call after onboarding never reaches the handler (401).
	w1 := doJSON(t, api, http.MethodPost, "/api/v1/providers/"+"codex"+"-cli"+"/sign-in", nil)
	assert.Equal(t, http.StatusBadRequest, w1.Code,
		"pre-auth window admits the request; the handler refuses the removed id: %s", w1.Body.String())

	require.NoError(t, api.onboardingMgr.CompleteOnboarding())
	require.True(t, api.onboardingMgr.IsComplete())

	w2 := doJSON(t, api, http.MethodPost, "/api/v1/providers/"+"codex"+"-cli"+"/sign-in", nil)
	assert.Equal(t, http.StatusUnauthorized, w2.Code,
		"FR-050: once onboarding is complete, sign-in requires an authenticated session")
}
