package auth

import (
	"encoding/base64"
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

// OAuthProviderConfig describes a vendor OAuth token endpoint — device-code
// login plus refresh-token exchange (ADR-068 §8b amendment, T068-32).
//
// The OpenAI device-code flow this file used to hold was deleted by T068-03
// (ADR-068 FR-002a) on the premise that borrowing the shared Codex client id
// was an unsanctioned trick. That reading was withdrawn 2026-08-23: the
// shared Codex login is the form OpenAI itself endorses and every peer agent
// (OpenClaw, OpenCode, Hermes) ships, so the flow is restored here — Omnipus
// requests and polls its own device-code session and stores the result in
// its OWN encrypted credential store (pkg/credentials, never the vendor's
// `~/.codex/auth.json`, never config.json). Reading that file stays
// available separately as a read-only IMPORT (FR-047,
// pkg/providers/codex_cli_credentials.go) — it is no longer the only source.
type OAuthProviderConfig struct {
	Issuer   string
	ClientID string
	Scopes   string
	Port     int
}

// OpenAIOAuthConfig returns the Codex device-code endpoints and the shared
// Codex CLI client id every peer agent uses for this same sanctioned flow
// (ADR-068 §8b decision 1).
func OpenAIOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Issuer:   "https://auth.openai.com",
		ClientID: "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:   "openid profile email offline_access",
		Port:     1455,
	}
}

// Environment variables that configure xAI's device-code OAuth (ADR-068
// FR-049). There is deliberately no code constant carrying an xAI client id:
// xAI sign-in ships only once Omnipus has its own registration with xAI —
// until then XAIOAuthConfig returns ErrNotConfigured and the xai provider
// row stays key-only.
const (
	EnvXAIOAuthClientID = "OMNIPUS_XAI_CLIENT_ID"
	EnvXAIOAuthIssuer   = "OMNIPUS_XAI_OAUTH_ISSUER"
	EnvXAIOAuthScopes   = "OMNIPUS_XAI_OAUTH_SCOPES"
)

// ErrNotConfigured is returned by XAIOAuthConfig when OMNIPUS_XAI_CLIENT_ID
// is unset.
var ErrNotConfigured = fmt.Errorf("xai OAuth client id not configured (set %s)", EnvXAIOAuthClientID)

// XAIOAuthConfig reads xAI's device-code OAuth configuration from the
// environment. Returns ErrNotConfigured when no client id has been set —
// xAI sign-in is configuration, not a code release (ADR-068 §8b decision 3).
func XAIOAuthConfig() (OAuthProviderConfig, error) {
	clientID := strings.TrimSpace(os.Getenv(EnvXAIOAuthClientID))
	if clientID == "" {
		return OAuthProviderConfig{}, ErrNotConfigured
	}
	issuer := strings.TrimSpace(os.Getenv(EnvXAIOAuthIssuer))
	if issuer == "" {
		issuer = "https://accounts.x.ai"
	}
	scopes := strings.TrimSpace(os.Getenv(EnvXAIOAuthScopes))
	if scopes == "" {
		scopes = "openid profile email offline_access"
	}
	return OAuthProviderConfig{
		Issuer:   issuer,
		ClientID: clientID,
		Scopes:   scopes,
	}, nil
}

// DeviceCodeInfo holds what the SPA needs to show the operator to complete a
// device-code sign-in (ADR-068 FR-008/FR-044): the link to open, the code to
// enter, and how often to poll.
type DeviceCodeInfo struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	VerifyURL    string `json:"verify_url"`
	Interval     int    `json:"interval"`
}

type deviceCodeResponse struct {
	DeviceAuthID string
	UserCode     string
	Interval     int
}

// RequestDeviceCode requests a device code from the OAuth provider. Returns
// the info needed for the operator to authenticate on any device — the
// vendor's own device code and any PKCE verifier never leave this call.
func RequestDeviceCode(cfg OAuthProviderConfig) (*DeviceCodeInfo, error) {
	reqBody, err := json.Marshal(map[string]string{
		"client_id": cfg.ClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding device code request: %w", err)
	}

	resp, err := http.Post(
		cfg.Issuer+"/api/accounts/deviceauth/usercode",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
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

// DeviceCodePollOutcome describes the state of an in-flight device-code
// session when PollDeviceCodeOnce did not return a credential (ADR-068
// FR-044).
type DeviceCodePollOutcome string

const (
	// DeviceCodePollPending: the operator has not approved the session yet.
	DeviceCodePollPending DeviceCodePollOutcome = "pending"
	// DeviceCodePollSlowDown: the vendor asked the caller to poll less often
	// (OAuth device-flow `slow_down`, RFC 8628 §3.5). Still pending — the
	// caller should widen its own interval (FR-045).
	DeviceCodePollSlowDown DeviceCodePollOutcome = "slow_down"
	// DeviceCodePollDenied: the operator explicitly declined on the vendor's
	// page. Terminal — stop polling.
	DeviceCodePollDenied DeviceCodePollOutcome = "denied"
	// DeviceCodePollExpired: the vendor reports the device-code session
	// itself has expired. Terminal — stop polling.
	DeviceCodePollExpired DeviceCodePollOutcome = "expired"
)

// PollDeviceCodeOnce makes a single poll attempt to check whether the
// operator has approved the device-code session. providerID is stamped onto
// the resulting AuthCredential.Provider (this flow is shared by openai and,
// once configured, xai — FR-049). Returns (credential, "", nil) on success;
// (nil, outcome, nil) while the session is still open or has reached a
// terminal negative state (see DeviceCodePollOutcome); (nil, "", err) on a
// transport or response-parsing failure.
func PollDeviceCodeOnce(cfg OAuthProviderConfig, providerID, deviceAuthID, userCode string) (*AuthCredential, DeviceCodePollOutcome, error) {
	return pollDeviceCode(cfg, providerID, deviceAuthID, userCode)
}

func pollDeviceCode(cfg OAuthProviderConfig, providerID, deviceAuthID, userCode string) (*AuthCredential, DeviceCodePollOutcome, error) {
	reqBody, err := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encoding device poll request: %w", err)
	}

	resp, err := http.Post(
		cfg.Issuer+"/api/accounts/deviceauth/token",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, "", err
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
		// Distinguish pending / slow_down / denied / expired from the error body.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		bodyStr := strings.ToLower(strings.TrimSpace(string(body)))
		switch {
		case strings.Contains(bodyStr, "access_denied"):
			return nil, DeviceCodePollDenied, nil
		case strings.Contains(bodyStr, "expired_token"), strings.Contains(bodyStr, "device_auth_expired"):
			return nil, DeviceCodePollExpired, nil
		case strings.Contains(bodyStr, "slow_down"):
			return nil, DeviceCodePollSlowDown, nil
		default:
			return nil, DeviceCodePollPending, nil
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading device token response: %w", err)
	}

	var tokenResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeChallenge     string `json:"code_challenge"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if unmarshalErr := json.Unmarshal(body, &tokenResp); unmarshalErr != nil {
		return nil, "", unmarshalErr
	}

	redirectURI := cfg.Issuer + "/deviceauth/callback"
	cred, err := ExchangeCodeForTokens(cfg, providerID, tokenResp.AuthorizationCode, tokenResp.CodeVerifier, redirectURI)
	if err != nil {
		return nil, "", err
	}
	return cred, "", nil
}

// ExchangeCodeForTokens exchanges an authorization code (with its PKCE
// verifier) for an access + refresh token pair. Used internally by the
// device-code flow above, whose poll response carries both the
// authorization_code and the code_verifier the vendor generated on our
// behalf — Omnipus never generates or holds a PKCE pair for this flow.
// providerID is stamped onto the resulting AuthCredential.Provider.
func ExchangeCodeForTokens(cfg OAuthProviderConfig, providerID, code, codeVerifier, redirectURI string) (*AuthCredential, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {codeVerifier},
	}
	tokenURL := cfg.Issuer + "/oauth/token"

	// #nosec G107 -- tokenURL is derived from cfg.Issuer, which callers pass
	// from fixed literal configuration (OpenAIOAuthConfig / XAIOAuthConfig) —
	// never request-derived.
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("exchanging code for tokens: %w", err)
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
		return nil, fmt.Errorf("reading token exchange response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	return parseTokenResponse(body, providerID)
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
	tokenURL := cfg.Issuer + "/oauth/token"

	// #nosec G107 -- tokenURL is derived from cfg.Issuer, which callers pass
	// from fixed literal configuration — never request-derived.
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
