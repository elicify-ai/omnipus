// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_sign_in.go implements the ADR-068 §8b (2026-08-23 amendment) sign-in
// routes: POST /providers/{id}/sign-in (FR-008), GET .../sign-in/status
// (FR-009), POST .../sign-in/poll (FR-044), POST
// /providers/openai-chatgpt/sign-in/import (FR-047), and DELETE
// .../sign-in (FR-048, sign out). Two sign-in shapes exist:
//
//   - cli_login (codex-cli, github-copilot): Omnipus never performs or
//     stores the vendor login itself. It reads the vendor CLI's own saved
//     login file read-only and never refreshes it (FR-007).
//   - device_code (openai-chatgpt, and xai once configured): Omnipus
//     requests and polls its own device-code session and stores the
//     resulting tokens in its own encrypted credential store under
//     "<vendor>_OAUTH" — never config.json, never the vendor's own
//     credential file (FR-007/FR-046).
package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

const maxProviderIDLen = 64

// deviceCodeSession is server-side state for one open device-code sign-in
// (FR-044): the vendor's own device_auth_id/user_code (never sent back to
// the client — the client only ever sees our opaque handle), the OAuth
// endpoint config needed to poll and exchange, and the session's own
// server-enforced expiry / single-use guard.
type deviceCodeSession struct {
	providerID         string
	vendorDeviceAuthID string
	vendorUserCode     string
	oauthCfg           auth.OAuthProviderConfig
	createdAt          time.Time
	expiresAt          time.Time
	intervalSeconds    int
	resolved           bool // single-use: true once signed_in/expired/denied has been returned
}

// deviceCodeSessionMaxTTL is FR-044's ceiling on a device-code session's
// server-side lifetime, applied even if the vendor reports a longer one.
const deviceCodeSessionMaxTTL = 15 * time.Minute

// deviceSessions returns the lazily-initialized, mutex-guarded device-code
// session map, creating it on first use. Safe for concurrent callers. A
// bare `restAPI{}` test literal that never touches sign-in routes need not
// know this field exists.
func (a *restAPI) deviceSessions() map[string]*deviceCodeSession {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if a.signInSessions == nil {
		a.signInSessions = make(map[string]*deviceCodeSession)
	}
	return a.signInSessions
}

func (a *restAPI) putDeviceSession(handle string, sess *deviceCodeSession) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if a.signInSessions == nil {
		a.signInSessions = make(map[string]*deviceCodeSession)
	}
	a.signInSessions[handle] = sess
}

func (a *restAPI) getDeviceSession(handle string) (*deviceCodeSession, bool) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	sess, ok := a.signInSessions[handle]
	return sess, ok
}

func (a *restAPI) deleteDeviceSession(handle string) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	delete(a.signInSessions, handle)
}

// newDeviceAuthHandle mints the opaque, gateway-side handle returned to the
// client as device_auth_id (FR-008: "the vendor's device code ... MUST NOT
// leave the gateway"). 24 random bytes, base64url — collision-negligible
// and never guessable.
func newDeviceAuthHandle() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating device auth handle: %w", err)
	}
	return "das_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// signInMethodFor returns "cli_login" | "device_code" for providerID and
// whether sign-in is supported at all. Prefers the ADR-067 catalog's
// auth_methods when a document is loaded; falls back to a small built-in
// set otherwise (the catalog's auth_methods wiring for these specific rows
// is still landing on this branch — GET /providers already tolerates a
// nil/undocumented catalog the same way, see its own comment). xai is
// device_code only once auth.XAIOAuthConfig() actually resolves — with no
// client id configured it is unsupported, never a forward-looking stub.
func (a *restAPI) signInMethodFor(providerID string) (method string, ok bool) {
	if a.providerCatalog != nil && a.providerCatalog.Document() != nil {
		if p, known := a.providerCatalog.Provider(providerID); known {
			supportsSignIn := false
			for _, m := range p.AuthMethods {
				if m == catalog.AuthSignIn {
					supportsSignIn = true
					break
				}
			}
			if !supportsSignIn {
				return "", false
			}
			return builtinSignInMethodShape(providerID), true
		}
	}
	switch providerID {
	case "codex-cli", "github-copilot":
		return "cli_login", true
	case "openai-chatgpt":
		return "device_code", true
	case "xai":
		if _, err := auth.XAIOAuthConfig(); err == nil {
			return "device_code", true
		}
		return "", false
	default:
		return "", false
	}
}

// builtinSignInMethodShape names the sign-in SHAPE (not whether it is
// supported — the caller already established that) for a catalog-known id.
func builtinSignInMethodShape(providerID string) string {
	if providerID == "openai-chatgpt" || providerID == "xai" {
		return "device_code"
	}
	return "cli_login"
}

// cliLoginCommand returns the vendor CLI login command + instructions for a
// cli_login provider (FR-008).
func cliLoginCommand(providerID string) (command, instructions string) {
	switch providerID {
	case "github-copilot":
		return "copilot login", "Run `copilot login` in a terminal, then click Check sign-in."
	default: // codex-cli and any future cli_login row
		return "codex login", "Run `codex login` in a terminal, then click Check sign-in."
	}
}

// oauthConfigFor resolves the auth.OAuthProviderConfig for a device_code
// provider id. A package-level var (mirrors pkg/providers/factory.go's
// getCredential seam) so tests can point openai-chatgpt/xai at an
// httptest.Server instead of the real vendor endpoints.
var oauthConfigFor = func(providerID string) (auth.OAuthProviderConfig, error) {
	switch providerID {
	case "openai-chatgpt":
		return auth.OpenAIOAuthConfig(), nil
	case "xai":
		return auth.XAIOAuthConfig()
	default:
		return auth.OAuthProviderConfig{}, fmt.Errorf("%s: no device-code OAuth config", providerID)
	}
}

// resolveSignInCredStore returns the shared unlocked credential store,
// falling back to opening one directly the same way storeCredential does —
// so sign-in works even in the (test-only) configuration where a.credStore
// was never injected.
func (a *restAPI) resolveSignInCredStore() (*credentials.Store, error) {
	if a.credStore != nil {
		return a.credStore, nil
	}
	store := credentials.NewStore(a.credentialsStorePath())
	if err := credentials.Unlock(store); err != nil {
		return nil, fmt.Errorf("credential store locked: %w", err)
	}
	return store, nil
}

// reRegisterOAuthSensitiveValues recomputes the complete sensitive-value set
// (config-ref-driven bundle plus every stored "<vendor>_OAUTH" entry) and
// re-registers it, so a newly-written or newly-refreshed OAuth token is
// scrubbed from LLM output/logs/audit from this point on — RegisterSensitiveValues'
// contract is "replace with the complete current set", so ANY stored OAuth
// entry (not just the one just touched) must be included or a previously
// signed-in provider's tokens would be silently evicted from the scrubber.
// Best-effort: errors are logged, never returned — a REST response must not
// fail because the housekeeping re-registration hit a transient issue.
func (a *restAPI) reRegisterOAuthSensitiveValues(store *credentials.Store) {
	if a.agentLoop == nil {
		return
	}
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		return
	}
	bundle, _ := credentials.ResolveBundle(cfg, store)
	values := make([]string, 0, len(bundle))
	for _, v := range bundle {
		if v != "" {
			values = append(values, v)
		}
	}
	values = append(values, providers_pkg.CollectOAuthSensitiveValues(store)...)
	cfg.RegisterSensitiveValues(values)
}

// handleProviderSignInStart implements POST /providers/{id}/sign-in (FR-008).
func (a *restAPI) handleProviderSignInStart(w http.ResponseWriter, r *http.Request, providerID string) {
	if err := validateEntityID(providerID); err != nil || len(providerID) > maxProviderIDLen {
		jsonErr(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	method, ok := a.signInMethodFor(providerID)
	if !ok {
		jsonErr(w, http.StatusBadRequest, "provider does not support sign-in")
		return
	}

	if method == "cli_login" {
		command, instructions := cliLoginCommand(providerID)
		resp := gen.SignInStartResponseCliLogin{
			Command:      command,
			Instructions: instructions,
		}
		var wire gen.SignInStartResponse
		if err := wire.FromSignInStartResponseCliLogin(resp); err != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to encode response")
			return
		}
		jsonOK(w, wire)
		return
	}

	// device_code
	oauthCfg, err := oauthConfigFor(providerID)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "provider does not support sign-in")
		return
	}
	info, err := auth.RequestDeviceCode(oauthCfg)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "could not start sign-in with the provider")
		return
	}
	handle, err := newDeviceAuthHandle()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to start sign-in session")
		return
	}
	interval := info.Interval
	if interval < 1 {
		interval = 5
	}
	now := time.Now()
	expiresAt := now.Add(deviceCodeSessionMaxTTL)
	a.putDeviceSession(handle, &deviceCodeSession{
		providerID:         providerID,
		vendorDeviceAuthID: info.DeviceAuthID,
		vendorUserCode:     info.UserCode,
		oauthCfg:           oauthCfg,
		createdAt:          now,
		expiresAt:          expiresAt,
		intervalSeconds:    interval,
	})

	resp := gen.SignInStartResponseDeviceCode{
		VerificationUrl: info.VerifyURL,
		UserCode:        info.UserCode,
		DeviceAuthId:    handle,
		ExpiresAt:       expiresAt,
		IntervalSeconds: interval,
	}
	var wire gen.SignInStartResponse
	if err := wire.FromSignInStartResponseDeviceCode(resp); err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	jsonOK(w, wire)
}

// handleProviderSignInPoll implements POST /providers/{id}/sign-in/poll (FR-044).
func (a *restAPI) handleProviderSignInPoll(w http.ResponseWriter, r *http.Request, providerID string) {
	if err := validateEntityID(providerID); err != nil || len(providerID) > maxProviderIDLen {
		jsonErr(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	var req gen.SignInPollRequest
	if decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); decodeErr != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DeviceAuthId) == "" {
		jsonErr(w, http.StatusBadRequest, "device_auth_id is required")
		return
	}

	sess, found := a.getDeviceSession(req.DeviceAuthId)
	if !found || sess.providerID != providerID {
		jsonErr(w, http.StatusNotFound, "unknown or expired device_auth_id")
		return
	}
	if sess.resolved {
		jsonErr(w, http.StatusNotFound, "unknown or expired device_auth_id")
		return
	}
	if time.Now().After(sess.expiresAt) {
		sess.resolved = true
		a.deleteDeviceSession(req.DeviceAuthId)
		jsonOK(w, gen.SignInPollResponse{State: gen.SignInPollResponseStateExpired})
		return
	}

	cred, outcome, err := auth.PollDeviceCodeOnce(sess.oauthCfg, providers_pkg.OAuthVendorID(providerID), sess.vendorDeviceAuthID, sess.vendorUserCode)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "could not reach the provider to check sign-in status")
		return
	}

	if cred != nil {
		// Success: persist before responding (FR-044).
		store, storeErr := a.resolveSignInCredStore()
		if storeErr != nil {
			jsonErr(w, http.StatusServiceUnavailable, "credential store unavailable")
			return
		}
		entryName := credentials.OAuthEntryName(providers_pkg.OAuthVendorID(providerID))
		blob, marshalErr := json.Marshal(struct {
			AccessToken  string    `json:"access_token"`
			RefreshToken string    `json:"refresh_token,omitempty"`
			AccountID    string    `json:"account_id,omitempty"`
			ExpiresAt    time.Time `json:"expires_at,omitempty"`
		}{
			AccessToken:  cred.AccessToken,
			RefreshToken: cred.RefreshToken,
			AccountID:    cred.AccountID,
			ExpiresAt:    cred.ExpiresAt,
		})
		if marshalErr != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to encode credential")
			return
		}
		if setErr := store.Set(entryName, string(blob)); setErr != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to store credential")
			return
		}
		a.reRegisterOAuthSensitiveValues(store)
		if a.auditor != nil {
			if logErr := a.auditor.Log(&audit.Entry{
				Event:    "provider.signed_in",
				Decision: audit.DecisionAllow,
				Details:  map[string]any{"provider": providerID},
			}); logErr != nil {
				slogWarnAuditFailed("provider.signed_in", logErr)
			}
		}
		sess.resolved = true
		a.deleteDeviceSession(req.DeviceAuthId)
		jsonOK(w, gen.SignInPollResponse{State: gen.SignInPollResponseStateSignedIn})
		return
	}

	switch outcome {
	case auth.DeviceCodePollSlowDown:
		// Widen the session's own interval so subsequent polls (and a
		// concurrent GET status, were it to read it) see the new floor too.
		newInterval := sess.intervalSeconds * 2
		if newInterval <= sess.intervalSeconds {
			newInterval = sess.intervalSeconds + 5
		}
		sess.intervalSeconds = newInterval
		jsonOK(w, gen.SignInPollResponse{
			State:           gen.SignInPollResponseStatePending,
			IntervalSeconds: &newInterval,
		})
	case auth.DeviceCodePollDenied:
		sess.resolved = true
		a.deleteDeviceSession(req.DeviceAuthId)
		jsonOK(w, gen.SignInPollResponse{State: gen.SignInPollResponseStateDenied})
	case auth.DeviceCodePollExpired:
		sess.resolved = true
		a.deleteDeviceSession(req.DeviceAuthId)
		jsonOK(w, gen.SignInPollResponse{State: gen.SignInPollResponseStateExpired})
	default: // pending
		jsonOK(w, gen.SignInPollResponse{State: gen.SignInPollResponseStatePending})
	}
}

// jwtUnverifiedExpiry decodes a JWT's payload segment (no signature
// verification — display/expiry-estimation only, never a trust decision)
// and returns its "exp" claim as a time.Time. ok is false when the token is
// not a 3-part JWT or carries no numeric exp claim.
func jwtUnverifiedExpiry(token string) (t time.Time, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.NewReplacer("-", "+", "_", "/").Replace(payload))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}

// cliLoginStatus computes GET .../sign-in/status for a cli_login provider
// (FR-009). Currently only codex-cli's ~/.codex/auth.json is a real,
// read-only source (github-copilot's CLI-reported status is T068-15's
// scope, not implemented here — falls back to not_signed_in like "no saved
// login").
func cliLoginStatus(providerID string) gen.SignInStatus {
	if providerID != "codex-cli" {
		return gen.SignInStatus{State: gen.SignInStatusStateNotSignedIn}
	}
	token, accountID, mtimeExpiry, err := providers_pkg.ReadCodexCliCredentials()
	if err != nil {
		return gen.SignInStatus{State: gen.SignInStatusStateNotSignedIn}
	}
	state := gen.SignInStatusStateSignedIn
	var expiresAt *time.Time
	if exp, ok := jwtUnverifiedExpiry(token); ok {
		e := exp
		expiresAt = &e
		if time.Now().After(exp) {
			state = gen.SignInStatusStateExpired
		}
	} else {
		e := mtimeExpiry
		expiresAt = &e
		if time.Now().After(mtimeExpiry) {
			state = gen.SignInStatusStateExpired
		}
	}
	resp := gen.SignInStatus{State: state, ExpiresAt: expiresAt}
	if accountID != "" {
		resp.AccountLabel = &accountID
	}
	return resp
}

// deviceCodeStatus computes GET .../sign-in/status for a device_code
// provider (FR-009/FR-046): read from our OWN stored OAuth entry, never the
// vendor. expired means the access token is past exp AND a refresh attempt
// failed or no refresh token is available — so this makes one refresh
// attempt itself when the stored token looks expired, rather than
// mislabeling a token that would actually still refresh fine.
func (a *restAPI) deviceCodeStatus(providerID string) gen.SignInStatus {
	store, err := a.resolveSignInCredStore()
	if err != nil {
		return gen.SignInStatus{State: gen.SignInStatusStateNotSignedIn}
	}
	// Any open, unresolved device-code session for this provider reports pending.
	for _, sess := range a.deviceSessions() {
		if sess.providerID == providerID && !sess.resolved && time.Now().Before(sess.expiresAt) {
			return gen.SignInStatus{State: gen.SignInStatusStatePending}
		}
	}

	oauthCfg, cfgErr := oauthConfigFor(providerID)
	tokenSource := providers_pkg.NewStoreOAuthTokenSource(providerID, store, oauthCfg)
	if cfgErr != nil {
		return gen.SignInStatus{State: gen.SignInStatusStateNotSignedIn}
	}
	accessToken, accountID, srcErr := tokenSource()
	if srcErr != nil {
		if errors.Is(srcErr, providers_pkg.ErrProviderNeedsSignIn) {
			// Distinguish "never signed in" from "signed in, but expired and
			// unrefreshable" by checking whether a stored entry exists at all.
			entryName := credentials.OAuthEntryName(providers_pkg.OAuthVendorID(providerID))
			if _, getErr := store.Get(entryName); getErr == nil {
				return gen.SignInStatus{State: gen.SignInStatusStateExpired}
			}
		}
		return gen.SignInStatus{State: gen.SignInStatusStateNotSignedIn}
	}
	a.reRegisterOAuthSensitiveValues(store) // tokenSource() may have refreshed and persisted a new token
	resp := gen.SignInStatus{State: gen.SignInStatusStateSignedIn}
	if accountID != "" {
		resp.AccountLabel = &accountID
	}
	_ = accessToken // never placed on the wire — status reports state only
	return resp
}

// handleProviderSignInStatus implements GET /providers/{id}/sign-in/status (FR-009).
func (a *restAPI) handleProviderSignInStatus(w http.ResponseWriter, r *http.Request, providerID string) {
	if err := validateEntityID(providerID); err != nil || len(providerID) > maxProviderIDLen {
		jsonErr(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	method, ok := a.signInMethodFor(providerID)
	if !ok {
		jsonErr(w, http.StatusBadRequest, "provider does not support sign-in")
		return
	}
	if method == "cli_login" {
		jsonOK(w, cliLoginStatus(providerID))
		return
	}
	jsonOK(w, a.deviceCodeStatus(providerID))
}

// handleProviderSignInImport implements POST /providers/openai-chatgpt/sign-in/import (FR-047).
func (a *restAPI) handleProviderSignInImport(w http.ResponseWriter, r *http.Request) {
	token, accountID, _, err := providers_pkg.ReadCodexCliCredentials()
	if err != nil {
		jsonErr(w, http.StatusNotFound, "no codex login found")
		return
	}
	store, storeErr := a.resolveSignInCredStore()
	if storeErr != nil {
		jsonErr(w, http.StatusServiceUnavailable, "credential store unavailable")
		return
	}
	entryName := credentials.OAuthEntryName(providers_pkg.OAuthVendorID("openai-chatgpt"))
	var expiresAt time.Time
	if exp, ok := jwtUnverifiedExpiry(token); ok {
		expiresAt = exp
	}
	blob, marshalErr := json.Marshal(struct {
		AccessToken  string    `json:"access_token"`
		RefreshToken string    `json:"refresh_token,omitempty"`
		AccountID    string    `json:"account_id,omitempty"`
		ExpiresAt    time.Time `json:"expires_at,omitempty"`
	}{
		// FR-047: no refresh token is imported — this session ends at exp.
		AccessToken: token,
		AccountID:   accountID,
		ExpiresAt:   expiresAt,
	})
	if marshalErr != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to encode credential")
		return
	}
	if setErr := store.Set(entryName, string(blob)); setErr != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to store credential")
		return
	}
	a.reRegisterOAuthSensitiveValues(store)
	if a.auditor != nil {
		if logErr := a.auditor.Log(&audit.Entry{
			Event:    "provider.signed_in",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"provider": "openai-chatgpt", "via": "codex_cli_import"},
		}); logErr != nil {
			slogWarnAuditFailed("provider.signed_in", logErr)
		}
	}
	jsonOK(w, a.deviceCodeStatus("openai-chatgpt"))
}

// handleProviderSignOut implements DELETE /providers/{id}/sign-in (FR-048).
func (a *restAPI) handleProviderSignOut(w http.ResponseWriter, r *http.Request, providerID string) {
	if err := validateEntityID(providerID); err != nil || len(providerID) > maxProviderIDLen {
		jsonErr(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	store, storeErr := a.resolveSignInCredStore()
	if storeErr != nil {
		jsonErr(w, http.StatusServiceUnavailable, "credential store unavailable")
		return
	}
	entryName := credentials.OAuthEntryName(providers_pkg.OAuthVendorID(providerID))
	if delErr := store.Delete(entryName); delErr != nil {
		var notFound *credentials.NotFoundError
		if !errors.As(delErr, &notFound) {
			jsonErr(w, http.StatusInternalServerError, "failed to sign out")
			return
		}
		// NotFound = success (FR-048).
	}
	a.reRegisterOAuthSensitiveValues(store)
	if a.auditor != nil {
		if logErr := a.auditor.Log(&audit.Entry{
			Event:    "provider.signed_out",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"provider": providerID},
		}); logErr != nil {
			slogWarnAuditFailed("provider.signed_out", logErr)
		}
	}
	jsonOK(w, gen.OperationResult{Success: true})
}

// slogWarnAuditFailed centralizes the "audit write failed" warning so every
// sign-in handler logs the same shape (mirrors the inline pattern used
// throughout rest.go's other audit.Log call sites).
func slogWarnAuditFailed(event string, err error) {
	slog.Warn("audit write failed", "event", event, "error", err)
}
