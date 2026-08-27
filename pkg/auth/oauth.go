package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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

	// Timeout bounds a single HTTP call made with this config — dial,
	// request, redirects and body read together. Zero means
	// defaultOAuthHTTPTimeout. It exists because the two callers of this
	// package want different bounds: the interactive sign-in flow can afford
	// the default, while the agent-path refresh
	// (providers.NewStoreOAuthTokenSource) runs inside a live turn and
	// bounds itself tighter so a hung vendor stalls one turn rather than the
	// whole token source.
	//
	// There is deliberately no unbounded setting: a zero-timeout
	// http.Client is what this file used to have (http.DefaultClient), and a
	// vendor that accepts the TCP connection and then never answers wedged
	// the caller — and, in the agent path, the refresh mutex — for the
	// process lifetime.
	Timeout time.Duration
}

// OpenAIOAuthConfig returns the Codex device-code endpoints and the shared
// Codex CLI client id every peer agent uses for this same sanctioned flow
// (ADR-068 §8b decision 1).
func OpenAIOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Issuer:   "https://auth.openai.com",
		ClientID: "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:   "openid profile email offline_access",
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

// --- Bounded HTTP transport for every OAuth call ---------------------------
//
// Threat note. Before this block every call in this file went through
// http.Post / http.PostForm on http.DefaultClient, whose Timeout is 0 — no
// dial deadline, no response deadline, no body-read deadline. A vendor (or
// anything on the path able to accept a TCP connection and then stay silent)
// could park an Omnipus goroutine indefinitely. In the agent path that
// goroutine holds the per-vendor refresh mutex
// (providers.NewStoreOAuthTokenSource), so a single hung refresh wedged the
// provider for every later turn until the process restarted. Every call now
// carries BOTH a context deadline and an http.Client.Timeout: the client
// timeout also covers the body read, which a bare context on the request does
// not once the response headers have arrived.

// defaultOAuthHTTPTimeout bounds one OAuth HTTP call when
// OAuthProviderConfig.Timeout is unset. Generous enough for an interactive
// sign-in against a slow vendor, far short of "forever".
//
// A var, not a const, purely so this package's own tests can shrink it; no
// production code assigns to it.
var defaultOAuthHTTPTimeout = 30 * time.Second

// maxOAuthResponseBytes bounds every OAuth response body read. Token
// responses are a few KB at most (an id_token JWT is the largest part), so
// this is orders of magnitude of headroom while still refusing to buffer an
// unbounded stream into memory on the say-so of the remote end.
const maxOAuthResponseBytes = 256 << 10

// maxVendorErrorEcho bounds how much of a vendor's own error body may be
// echoed into an error string. These strings travel: a refresh failure is
// wrapped into providers' providerNeedsSignInError.Cause, reaches the agent
// error classifier, and can surface to the operator. An upstream response is
// attacker-influenced text, so it is truncated and stripped of control
// characters before it is quoted.
const maxVendorErrorEcho = 256

// httpTimeout resolves the bound for one call: the config's own override
// when set, the package default otherwise.
func (c OAuthProviderConfig) httpTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return defaultOAuthHTTPTimeout
}

// doOAuthPost issues one bounded POST and returns the response. The caller
// owns resp.Body. cancel must be called once the body has been read — not
// before, or the body read is aborted.
func doOAuthPost(cfg OAuthProviderConfig, endpoint, contentType string, body []byte) (*http.Response, context.CancelFunc, error) {
	timeout := cfg.httpTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	if err := validateOAuthEndpoint(endpoint); err != nil {
		cancel()
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("Content-Type", contentType)

	// A fresh Client per call is cheap: a nil Transport means
	// http.DefaultTransport, so the connection pool is still shared. Only the
	// timeout is per-call.
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return resp, cancel, nil
}

// validateOAuthEndpoint constrains the URL doOAuthPost will dial.
//
// Every endpoint is built from OAuthProviderConfig.Issuer, which is either a
// compile-time constant (OpenAI) or an operator-set environment variable
// (OMNIPUS_XAI_OAUTH_ISSUER). It is never derived from a request, so this is
// not a user-controlled SSRF sink — but "not request-derived" is an argument
// about today's callers, and a silent typo or a future caller passing an
// attacker-influenced issuer would turn this into one. Validating here makes
// the property structural instead of a claim in a comment, which is also what
// the earlier per-call `#nosec G107` annotations were standing in for before
// the four call sites were consolidated into doOAuthPost.
//
// https is required, with one deliberate exception: a loopback host may use
// http, because the test suite drives these flows against httptest servers and
// an operator may point a local xAI-compatible issuer at 127.0.0.1. This
// mirrors the catalog loader's own URL rule, which likewise permits loopback
// only for local rows.
func validateOAuthEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("oauth endpoint is not a valid URL: %w", err)
	}
	if u.Host == "" {
		return fmt.Errorf("oauth endpoint %q has no host", endpoint)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf(
		"oauth endpoint %q must use https (http is permitted only for a loopback host)",
		endpoint,
	)
}

// isLoopbackHost reports whether a URL hostname refers to the local machine.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// readOAuthBody drains a bounded prefix of an OAuth response body.
func readOAuthBody(resp *http.Response) ([]byte, error) {
	return io.ReadAll(io.LimitReader(resp.Body, maxOAuthResponseBytes))
}

// sanitizeVendorError makes a vendor's error body safe to quote in an error
// string: truncated to maxVendorErrorEcho bytes, forced to valid UTF-8,
// control characters dropped and whitespace collapsed to single spaces.
func sanitizeVendorError(body []byte) string {
	truncated := false
	if len(body) > maxVendorErrorEcho {
		body = body[:maxVendorErrorEcho]
		truncated = true
	}
	// Truncation can split a multi-byte rune; drop any invalid sequence
	// rather than emitting U+FFFD noise into an operator-facing string.
	s := strings.ToValidUTF8(string(body), "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		case !utf8.ValidRune(r):
			return -1
		default:
			return r
		}
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		s = "(empty response body)"
	}
	if truncated {
		s += " [truncated]"
	}
	return s
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

	resp, cancel, err := doOAuthPost(cfg, cfg.Issuer+"/api/accounts/deviceauth/usercode", "application/json", reqBody)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer cancel()
	// Response body is fully drained via readOAuthBody below; a Close error on
	// an already-consumed HTTP response body has no effect on the parsed result.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	body, err := readOAuthBody(resp)
	if err != nil {
		return nil, fmt.Errorf("reading device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %s", sanitizeVendorError(body))
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

	resp, cancel, err := doOAuthPost(cfg, cfg.Issuer+"/api/accounts/deviceauth/token", "application/json", reqBody)
	if err != nil {
		return nil, "", err
	}
	defer cancel()
	// Response body is fully drained via a bounded LimitReader below; a Close
	// error on an already-consumed HTTP response body has no effect on the
	// parsed OAuth result.
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

	body, err := readOAuthBody(resp)
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

	// tokenURL is derived from cfg.Issuer, which callers pass from fixed
	// literal configuration (OpenAIOAuthConfig / XAIOAuthConfig) — never
	// request-derived.
	resp, cancel, err := doOAuthPost(cfg, tokenURL, "application/x-www-form-urlencoded", []byte(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("exchanging code for tokens: %w", err)
	}
	defer cancel()
	// Response body is fully drained via readOAuthBody below; a Close error on
	// an already-consumed HTTP response body has no effect on the parsed result.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	body, err := readOAuthBody(resp)
	if err != nil {
		return nil, fmt.Errorf("reading token exchange response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s", sanitizeVendorError(body))
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

	// tokenURL is derived from cfg.Issuer, which callers pass from fixed
	// literal configuration — never request-derived.
	resp, cancel, err := doOAuthPost(cfg, tokenURL, "application/x-www-form-urlencoded", []byte(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	defer cancel()
	// Response body is fully drained via readOAuthBody below; a Close error on
	// an already-consumed HTTP response body has no effect on the parsed result.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	body, err := readOAuthBody(resp)
	if err != nil {
		return nil, fmt.Errorf("reading token refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %s", sanitizeVendorError(body))
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
