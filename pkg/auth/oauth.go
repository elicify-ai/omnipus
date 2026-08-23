package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// OAuthProviderConfig describes a vendor OAuth token endpoint used for
// store-held credential refresh.
//
// ADR-068 FR-002a: the OpenAI device-code / browser-login flow that used to
// live in this file (device-code request + poll, authorize-URL building,
// code-for-token exchange, the OpenAI client config) is deleted. No code path
// in Omnipus starts a vendor OAuth or device-code login for OpenAI; OpenAI
// sign-in is the vendor's own CLI (`codex login`) whose credential file is
// read, never written (FR-007). What remains here is the refresh path still
// used by the Google Cloud Code Assist provider.
type OAuthProviderConfig struct {
	Issuer   string
	ClientID string
	// ClientSecret is required for Google OAuth (confidential client). Field
	// name matches gosec's secret-name pattern by design: it holds the
	// actual OAuth client secret, sourced from an env var (see
	// GoogleAntigravityOAuthConfig), never a literal; never serialized to
	// any gateway/API response (grep confirms no pkg/gateway reference to
	// OAuthProviderConfig).
	// #nosec G117 -- see comment above
	ClientSecret string
	TokenURL     string // Override token endpoint (Google uses a different URL than issuer)
	Scopes       string
	Port         int
}

// GoogleAntigravityOAuthConfig returns the OAuth configuration for Google Cloud Code Assist (Antigravity).
// Client credentials are the same ones used by OpenCode/pi-ai for Cloud Code Assist access.
func GoogleAntigravityOAuthConfig() OAuthProviderConfig {
	// Google OAuth credentials must be configured via environment variables.
	clientID := os.Getenv("OMNIPUS_GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("OMNIPUS_GOOGLE_CLIENT_SECRET")
	// #nosec G101 -- ClientID/ClientSecret below are the clientID/clientSecret
	// locals read from os.Getenv two lines up, not hardcoded literals; gosec's
	// pattern match fires on the struct field names alone.
	return OAuthProviderConfig{
		Issuer:       "https://accounts.google.com/o/oauth2/v2",
		TokenURL:     "https://oauth2.googleapis.com/token",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs",
		Port:         51121,
	}
}

// RefreshAccessToken exchanges a store-held refresh token for a fresh access
// token at the provider's token endpoint.
func RefreshAccessToken(cred *AuthCredential, cfg OAuthProviderConfig) (*AuthCredential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	data := url.Values{
		"client_id":     {cfg.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
		"scope":         {"openid profile email"},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	tokenURL := cfg.Issuer + "/oauth/token"
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}

	// #nosec G107 -- tokenURL is derived from cfg.Issuer/cfg.TokenURL, and cfg
	// is only ever constructed by the hardcoded factory in this file
	// (GoogleAntigravityOAuthConfig) with fixed literal Issuer/TokenURL
	// strings — never request-derived.
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	// Response body is fully drained via io.ReadAll below; a Close error on an
	// already-consumed HTTP response body has no effect on the parsed result.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %s", string(body))
	}

	refreshed, err := parseTokenResponse(body, cred.Provider)
	if err != nil {
		return nil, err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = cred.RefreshToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = cred.AccountID
	}
	if cred.Email != "" && refreshed.Email == "" {
		refreshed.Email = cred.Email
	}
	if cred.ProjectID != "" && refreshed.ProjectID == "" {
		refreshed.ProjectID = cred.ProjectID
	}
	return refreshed, nil
}

func parseTokenResponse(body []byte, provider string) (*AuthCredential, error) {
	// tokenResp is a local, anonymous struct used only to json.Unmarshal the
	// OAuth provider's own token-endpoint response (incoming deserialization,
	// never marshaled back out); it is not part of any package API and never
	// crosses the gateway/API boundary. Each field below is annotated
	// individually since gosec's G117 fires per struct field.
	var tokenResp struct {
		AccessToken  string `json:"access_token"`  // #nosec G117 -- incoming-only, see comment above
		RefreshToken string `json:"refresh_token"` // #nosec G117 -- incoming-only, see comment above
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}

	var expiresAt time.Time
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	cred := &AuthCredential{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    expiresAt,
		Provider:     provider,
		AuthMethod:   "oauth",
	}

	if id := extractAccountID(tokenResp.IDToken); id != "" {
		cred.AccountID = id
	} else if id := extractAccountID(tokenResp.AccessToken); id != "" {
		cred.AccountID = id
	}

	return cred, nil
}

func extractAccountID(token string) string {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return ""
	}

	if accountID, ok := claims["chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID
	}

	if accountID, ok := claims["https://api.openai.com/auth.chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID
	}

	if authClaim, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if accountID, ok := authClaim["chatgpt_account_id"].(string); ok && accountID != "" {
			return accountID
		}
	}

	if orgs, ok := claims["organizations"].([]any); ok {
		for _, org := range orgs {
			if orgMap, ok := org.(map[string]any); ok {
				if accountID, ok := orgMap["id"].(string); ok && accountID != "" {
					return accountID
				}
			}
		}
	}

	return ""
}

func parseJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("token is not a JWT")
	}

	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64URLDecode(payload)
	if err != nil {
		return nil, err
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, err
	}

	return claims, nil
}

func base64URLDecode(s string) ([]byte, error) {
	s = strings.NewReplacer("-", "+", "_", "/").Replace(s)
	return base64.StdEncoding.DecodeString(s)
}
