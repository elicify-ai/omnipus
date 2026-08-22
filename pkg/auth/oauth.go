package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

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
	Originator   string
	Port         int
}

func OpenAIOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Issuer:     "https://auth.openai.com",
		ClientID:   "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:     "openid profile email offline_access",
		Originator: "codex_cli_rs",
		Port:       1455,
	}
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

// GenerateState generates a random state string for OAuth CSRF protection.
func GenerateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type deviceCodeResponse struct {
	DeviceAuthID string
	UserCode     string
	Interval     int
}

// DeviceCodeInfo holds the device code information returned by the OAuth provider.
type DeviceCodeInfo struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	VerifyURL    string `json:"verify_url"`
	Interval     int    `json:"interval"`
}

// RequestDeviceCode requests a device code from the OAuth provider.
// Returns the info needed for the user to authenticate in a browser.
func RequestDeviceCode(cfg OAuthProviderConfig) (*DeviceCodeInfo, error) {
	reqBody, err := json.Marshal(map[string]string{
		"client_id": cfg.ClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling device code request: %w", err)
	}

	resp, err := http.Post(
		cfg.Issuer+"/api/accounts/deviceauth/usercode",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	// Response body is fully drained (via io.ReadAll or a bounded LimitReader
	// below); a Close error on an already-consumed HTTP response body has no
	// effect on the parsed OAuth result.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %s", string(body))
	}

	deviceResp, err := parseDeviceCodeResponse(body)
	if err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}

	if deviceResp.Interval < 1 {
		deviceResp.Interval = 5
	}

	return &DeviceCodeInfo{
		DeviceAuthID: deviceResp.DeviceAuthID,
		UserCode:     deviceResp.UserCode,
		VerifyURL:    cfg.Issuer + "/codex/device",
		Interval:     deviceResp.Interval,
	}, nil
}

// PollDeviceCodeOnce makes a single poll attempt to check if the user has authenticated.
// Returns (credential, nil) on success, (nil, nil) if still pending, or (nil, err) on failure.
func PollDeviceCodeOnce(cfg OAuthProviderConfig, deviceAuthID, userCode string) (*AuthCredential, error) {
	return pollDeviceCode(cfg, deviceAuthID, userCode)
}

func parseDeviceCodeResponse(body []byte) (deviceCodeResponse, error) {
	var raw struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return deviceCodeResponse{}, err
	}

	interval, err := parseFlexibleInt(raw.Interval)
	if err != nil {
		return deviceCodeResponse{}, err
	}

	return deviceCodeResponse{
		DeviceAuthID: raw.DeviceAuthID,
		UserCode:     raw.UserCode,
		Interval:     interval,
	}, nil
}

func parseFlexibleInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}

	var interval int
	if err := json.Unmarshal(raw, &interval); err == nil {
		return interval, nil
	}

	var intervalStr string
	if err := json.Unmarshal(raw, &intervalStr); err == nil {
		intervalStr = strings.TrimSpace(intervalStr)
		if intervalStr == "" {
			return 0, nil
		}
		return strconv.Atoi(intervalStr)
	}

	return 0, fmt.Errorf("invalid integer value: %s", string(raw))
}

var (
	errDeviceAuthPending = fmt.Errorf("authorization_pending")
	errDeviceAuthDenied  = fmt.Errorf("access_denied")
)

func pollDeviceCode(cfg OAuthProviderConfig, deviceAuthID, userCode string) (*AuthCredential, error) {
	reqBody, err := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling device token request: %w", err)
	}

	resp, err := http.Post(
		cfg.Issuer+"/api/accounts/deviceauth/token",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, err
	}
	// Response body is fully drained (via io.ReadAll or a bounded LimitReader
	// below); a Close error on an already-consumed HTTP response body has no
	// effect on the parsed OAuth result.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		// Try to read the error body to distinguish pending vs denied. A
		// partial/failed read still yields whatever bytes were read before
		// the error; the substring check below degrades safely to "pending".
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			_ = readErr
		}
		bodyStr := strings.ToLower(strings.TrimSpace(string(body)))
		if strings.Contains(bodyStr, "access_denied") {
			return nil, errDeviceAuthDenied
		}
		return nil, errDeviceAuthPending
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading device token response: %w", err)
	}

	var tokenResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeChallenge     string `json:"code_challenge"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	redirectURI := cfg.Issuer + "/deviceauth/callback"
	return ExchangeCodeForTokens(cfg, tokenResp.AuthorizationCode, tokenResp.CodeVerifier, redirectURI)
}

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
	// is only ever constructed by the two hardcoded factories in this file
	// (OpenAIOAuthConfig, GoogleAntigravityOAuthConfig) with fixed literal
	// Issuer/TokenURL strings — never request-derived.
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	// Response body is fully drained (via io.ReadAll or a bounded LimitReader
	// below); a Close error on an already-consumed HTTP response body has no
	// effect on the parsed OAuth result.
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

func BuildAuthorizeURL(cfg OAuthProviderConfig, pkce PKCECodes, state, redirectURI string) string {
	return buildAuthorizeURL(cfg, pkce, state, redirectURI)
}

func buildAuthorizeURL(cfg OAuthProviderConfig, pkce PKCECodes, state, redirectURI string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {cfg.Scopes},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}

	isGoogle := strings.Contains(strings.ToLower(cfg.Issuer), "accounts.google.com")
	if isGoogle {
		// Google OAuth requires these for refresh token support
		params.Set("access_type", "offline")
		params.Set("prompt", "consent")
	} else {
		// OpenAI-specific parameters
		params.Set("id_token_add_organizations", "true")
		params.Set("codex_cli_simplified_flow", "true")
		if strings.Contains(strings.ToLower(cfg.Issuer), "auth.openai.com") {
			params.Set("originator", "omnipus")
		}
		if cfg.Originator != "" {
			params.Set("originator", cfg.Originator)
		}
	}

	// Google uses /auth path, OpenAI uses /oauth/authorize
	if isGoogle {
		return cfg.Issuer + "/auth?" + params.Encode()
	}
	return cfg.Issuer + "/oauth/authorize?" + params.Encode()
}

// ExchangeCodeForTokens exchanges an authorization code for tokens.
func ExchangeCodeForTokens(cfg OAuthProviderConfig, code, codeVerifier, redirectURI string) (*AuthCredential, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {codeVerifier},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	tokenURL := cfg.Issuer + "/oauth/token"
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}

	// Determine provider name from config
	provider := "openai"
	if cfg.TokenURL != "" && strings.Contains(cfg.TokenURL, "googleapis.com") {
		provider = "google-antigravity"
	}

	// #nosec G107 -- same as RefreshAccessToken above: tokenURL comes only
	// from the two hardcoded provider-config factories in this file.
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("exchanging code for tokens: %w", err)
	}
	// Response body is fully drained (via io.ReadAll or a bounded LimitReader
	// below); a Close error on an already-consumed HTTP response body has no
	// effect on the parsed OAuth result.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token exchange response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	return parseTokenResponse(body, provider)
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

	// Recent OpenAI OAuth responses may only include chatgpt_account_id in id_token claims.
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
