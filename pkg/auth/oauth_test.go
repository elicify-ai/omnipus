package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// ADR-068 §8b (2026-08-23 amendment): the OpenAI device-code flow deleted by
// T068-03 (FR-002a) is restored here — the shared Codex client id is the
// form OpenAI itself endorses, per peer agents. Tests below cover the
// restored RequestDeviceCode / PollDeviceCodeOnce / ExchangeCodeForTokens
// trio plus the token-response parser and the store-held refresh path. The
// browser/PKCE-authorize-URL flow (BuildAuthorizeURL, GenerateState,
// GeneratePKCE) is NOT restored — the device flow never builds an authorize
// URL itself and the Headless rule (ADR-068 §8b) forbids a localhost
// callback, so those symbols would have no caller.

func makeJWTForClaims(t *testing.T, claims map[string]any) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}

func TestParseTokenResponse(t *testing.T) {
	resp := map[string]any{
		"access_token":  "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      "test-id-token",
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccessToken != "test-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "test-access-token")
	}
	if cred.RefreshToken != "test-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", cred.RefreshToken, "test-refresh-token")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
	if cred.AuthMethod != "oauth" {
		t.Errorf("AuthMethod = %q, want %q", cred.AuthMethod, "oauth")
	}
	if cred.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should not be zero")
	}
}

func TestParseTokenResponseExtractsAccountIDFromIDToken(t *testing.T) {
	idToken := makeJWTForClaims(t, map[string]any{"chatgpt_account_id": "acc-id-from-id-token"})
	resp := map[string]any{
		"access_token":  "opaque-access-token",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      idToken,
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}
	if cred.AccountID != "acc-id-from-id-token" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-id-from-id-token")
	}
}

func TestExtractAccountIDFromOrganizationsFallback(t *testing.T) {
	token := makeJWTForClaims(t, map[string]any{
		"organizations": []any{
			map[string]any{"id": "org_from_orgs"},
		},
	})

	if got := extractAccountID(token); got != "org_from_orgs" {
		t.Errorf("extractAccountID() = %q, want %q", got, "org_from_orgs")
	}
}

func TestParseTokenResponseNoAccessToken(t *testing.T) {
	body := []byte(`{"refresh_token": "test"}`)
	_, err := parseTokenResponse(body, "openai")
	if err == nil {
		t.Error("expected error for missing access_token")
	}
}

func TestParseTokenResponseAccountIDFromIDToken(t *testing.T) {
	idToken := makeJWTWithAccountID("acc-from-id")
	resp := map[string]any{
		"access_token":  "not-a-jwt",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      idToken,
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccountID != "acc-from-id" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-from-id")
	}
}

func makeJWTWithAccountID(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`),
	)
	return header + "." + payload + ".sig"
}

func TestRefreshAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		if ignoredErr := r.ParseForm(); ignoredErr != nil {
			_ = ignoredErr
		}
		if r.FormValue("grant_type") != "refresh_token" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"access_token":  "refreshed-access-token",
			"refresh_token": "refreshed-refresh-token",
			"expires_in":    3600,
		}
		if ignoredErr := json.NewEncoder(w).Encode(resp); ignoredErr != nil {
			_ = ignoredErr
		}
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
	}

	cred := &AuthCredential{
		AccessToken:  "old-token",
		RefreshToken: "old-refresh-token",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}

	if refreshed.AccessToken != "refreshed-access-token" {
		t.Errorf("AccessToken = %q, want %q", refreshed.AccessToken, "refreshed-access-token")
	}
	if refreshed.RefreshToken != "refreshed-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "refreshed-refresh-token")
	}
}

func TestRefreshAccessTokenNoRefreshToken(t *testing.T) {
	cfg := OAuthProviderConfig{Issuer: "http://127.0.0.1:0", ClientID: "test-client"}
	cred := &AuthCredential{
		AccessToken: "old-token",
		Provider:    "openai",
		AuthMethod:  "oauth",
	}

	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Error("expected error for missing refresh token")
	}
}

func TestRefreshAccessTokenPreservesRefreshAndAccountID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"access_token": "new-access-token-only",
			"expires_in":   3600,
		}
		if ignoredErr := json.NewEncoder(w).Encode(resp); ignoredErr != nil {
			_ = ignoredErr
		}
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "test-client"}
	cred := &AuthCredential{
		AccessToken:  "old-access",
		RefreshToken: "existing-refresh",
		AccountID:    "acc_existing",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}
	if refreshed.RefreshToken != "existing-refresh" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "existing-refresh")
	}
	if refreshed.AccountID != "acc_existing" {
		t.Errorf("AccountID = %q, want %q", refreshed.AccountID, "acc_existing")
	}
}

func TestOpenAIOAuthConfig(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	if cfg.Issuer != "https://auth.openai.com" {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, "https://auth.openai.com")
	}
	if cfg.ClientID == "" {
		t.Error("ClientID is empty")
	}
	if cfg.Scopes == "" {
		t.Error("Scopes is empty")
	}
	// The Port field (and its 1455 value) was vestigial from the DELETED
	// loopback-callback flow — nothing outside this assertion ever read it,
	// and it documented a localhost listener ADR-068 forbids. Both are gone;
	// the assertion that replaces it pins the fields the device-code flow
	// actually uses.
}

func TestXAIOAuthConfig_Unset(t *testing.T) {
	t.Setenv(EnvXAIOAuthClientID, "")
	t.Setenv(EnvXAIOAuthIssuer, "")
	t.Setenv(EnvXAIOAuthScopes, "")
	// Explicitly unset rather than rely on t.Setenv("", "") semantics.
	if err := os.Unsetenv(EnvXAIOAuthClientID); err != nil {
		t.Fatalf("Unsetenv: %v", err)
	}

	_, err := XAIOAuthConfig()
	if err == nil {
		t.Fatal("expected ErrNotConfigured, got nil")
	}
	if err.Error() == "" {
		t.Error("error message is empty")
	}
}

func TestXAIOAuthConfig_Set(t *testing.T) {
	t.Setenv(EnvXAIOAuthClientID, "test-xai-client")
	t.Setenv(EnvXAIOAuthIssuer, "https://accounts.example.com")

	cfg, err := XAIOAuthConfig()
	if err != nil {
		t.Fatalf("XAIOAuthConfig() error: %v", err)
	}
	if cfg.ClientID != "test-xai-client" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "test-xai-client")
	}
	if cfg.Issuer != "https://accounts.example.com" {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, "https://accounts.example.com")
	}
	if cfg.Scopes == "" {
		t.Error("Scopes defaults should not be empty")
	}
}

func TestXAIOAuthConfig_DefaultIssuer(t *testing.T) {
	t.Setenv(EnvXAIOAuthClientID, "test-xai-client")
	if err := os.Unsetenv(EnvXAIOAuthIssuer); err != nil {
		t.Fatalf("Unsetenv: %v", err)
	}

	cfg, err := XAIOAuthConfig()
	if err != nil {
		t.Fatalf("XAIOAuthConfig() error: %v", err)
	}
	if cfg.Issuer != "https://accounts.x.ai" {
		t.Errorf("Issuer = %q, want default %q", cfg.Issuer, "https://accounts.x.ai")
	}
}

func TestRequestDeviceCode_Parses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/deviceauth/usercode" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"device_auth_id": "das_test123",
			"user_code":      "WDJB-MJHT",
			"interval":       5,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "test-client"}
	info, err := RequestDeviceCode(cfg)
	if err != nil {
		t.Fatalf("RequestDeviceCode() error: %v", err)
	}
	if info.DeviceAuthID != "das_test123" {
		t.Errorf("DeviceAuthID = %q, want %q", info.DeviceAuthID, "das_test123")
	}
	if info.UserCode != "WDJB-MJHT" {
		t.Errorf("UserCode = %q, want %q", info.UserCode, "WDJB-MJHT")
	}
	if info.Interval != 5 {
		t.Errorf("Interval = %d, want 5", info.Interval)
	}
	if info.VerifyURL != server.URL+"/codex/device" {
		t.Errorf("VerifyURL = %q, want %q", info.VerifyURL, server.URL+"/codex/device")
	}
}

func TestRequestDeviceCode_DefaultInterval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"device_auth_id": "das_x", "user_code": "ABCD-1234"}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	info, err := RequestDeviceCode(OAuthProviderConfig{Issuer: server.URL, ClientID: "c"})
	if err != nil {
		t.Fatalf("RequestDeviceCode() error: %v", err)
	}
	if info.Interval != 5 {
		t.Errorf("Interval = %d, want default 5", info.Interval)
	}
}

func TestRequestDeviceCode_UpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := RequestDeviceCode(OAuthProviderConfig{Issuer: server.URL, ClientID: "c"})
	if err == nil {
		t.Fatal("expected error on upstream failure")
	}
}

func TestParseDeviceCodeResponseIntervalAsNumber(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":5}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}
	if resp.DeviceAuthID != "abc" {
		t.Errorf("DeviceAuthID = %q, want %q", resp.DeviceAuthID, "abc")
	}
	if resp.UserCode != "DEF-1234" {
		t.Errorf("UserCode = %q, want %q", resp.UserCode, "DEF-1234")
	}
	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseIntervalAsString(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"5"}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}
	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseInvalidInterval(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"abc"}`)

	if _, err := parseDeviceCodeResponse(body); err == nil {
		t.Fatal("expected error for invalid interval")
	}
}

// pollServer builds an httptest server backing both the device-poll and
// token-exchange endpoints, so PollDeviceCodeOnce's full success path can be
// exercised without a real vendor.
func pollServer(t *testing.T, pollStatus int, pollBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(pollStatus)
		if _, err := w.Write([]byte(pollBody)); err != nil {
			t.Errorf("write: %v", err)
		}
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"access_token":  "device-flow-access-token",
			"refresh_token": "device-flow-refresh-token",
			"expires_in":    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	})
	return httptest.NewServer(mux)
}

func TestPollDeviceCodeOnce_Success(t *testing.T) {
	body := `{"authorization_code":"code123","code_challenge":"chal","code_verifier":"verif"}`
	server := pollServer(t, http.StatusOK, body)
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	cred, outcome, err := PollDeviceCodeOnce(cfg, "openai", "das_1", "USER-CODE")
	if err != nil {
		t.Fatalf("PollDeviceCodeOnce() error: %v", err)
	}
	if outcome != "" {
		t.Errorf("outcome = %q, want empty on success", outcome)
	}
	if cred == nil {
		t.Fatal("expected a credential on success")
	}
	if cred.AccessToken != "device-flow-access-token" {
		t.Errorf("AccessToken = %q, want device-flow-access-token", cred.AccessToken)
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
}

func TestPollDeviceCodeOnce_Pending(t *testing.T) {
	server := pollServer(t, http.StatusBadRequest, `{"error":"authorization_pending"}`)
	defer server.Close()

	cred, outcome, err := PollDeviceCodeOnce(OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}, "openai", "das_1", "CODE")
	if err != nil {
		t.Fatalf("PollDeviceCodeOnce() error: %v", err)
	}
	if cred != nil {
		t.Error("expected no credential while pending")
	}
	if outcome != DeviceCodePollPending {
		t.Errorf("outcome = %q, want %q", outcome, DeviceCodePollPending)
	}
}

func TestPollDeviceCodeOnce_SlowDown(t *testing.T) {
	server := pollServer(t, http.StatusBadRequest, `{"error":"slow_down"}`)
	defer server.Close()

	cred, outcome, err := PollDeviceCodeOnce(OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}, "openai", "das_1", "CODE")
	if err != nil {
		t.Fatalf("PollDeviceCodeOnce() error: %v", err)
	}
	if cred != nil {
		t.Error("expected no credential on slow_down")
	}
	if outcome != DeviceCodePollSlowDown {
		t.Errorf("outcome = %q, want %q", outcome, DeviceCodePollSlowDown)
	}
}

func TestPollDeviceCodeOnce_Denied(t *testing.T) {
	server := pollServer(t, http.StatusBadRequest, `{"error":"access_denied"}`)
	defer server.Close()

	cred, outcome, err := PollDeviceCodeOnce(OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}, "openai", "das_1", "CODE")
	if err != nil {
		t.Fatalf("PollDeviceCodeOnce() error: %v", err)
	}
	if cred != nil {
		t.Error("expected no credential on denial")
	}
	if outcome != DeviceCodePollDenied {
		t.Errorf("outcome = %q, want %q", outcome, DeviceCodePollDenied)
	}
}

func TestPollDeviceCodeOnce_Expired(t *testing.T) {
	server := pollServer(t, http.StatusBadRequest, `{"error":"expired_token"}`)
	defer server.Close()

	cred, outcome, err := PollDeviceCodeOnce(OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}, "openai", "das_1", "CODE")
	if err != nil {
		t.Fatalf("PollDeviceCodeOnce() error: %v", err)
	}
	if cred != nil {
		t.Error("expected no credential when expired")
	}
	if outcome != DeviceCodePollExpired {
		t.Errorf("outcome = %q, want %q", outcome, DeviceCodePollExpired)
	}
}

func TestExchangeCodeForTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.FormValue("grant_type") != "authorization_code" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"expires_in":    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "test-client"}
	cred, err := ExchangeCodeForTokens(cfg, "openai", "test-code", "test-verifier", "http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatalf("ExchangeCodeForTokens() error: %v", err)
	}
	if cred.AccessToken != "mock-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "mock-access-token")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
}
