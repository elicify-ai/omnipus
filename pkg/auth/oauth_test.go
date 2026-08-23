package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ADR-068 FR-002a: the tests for the deleted OpenAI device-code / browser
// login flow (authorize-URL building, code exchange, device-code parsing, the
// OpenAI client config) were deleted with the code, not skipped. What remains
// exercises the token-response parser and the store-held refresh path.

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
