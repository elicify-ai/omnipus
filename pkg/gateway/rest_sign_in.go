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
//
// Every field is guarded by restAPI.signInMu — see the CONCURRENCY CONTRACT
// above putDeviceSession. The struct is deliberately copyable (no pointers,
// no embedded lock) so readers can be handed a snapshot instead of the live
// record.
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

// minSignInIntervalSeconds and maxSignInIntervalSeconds bound
// interval_seconds on every wire response that carries it
// (SignInStartResponseDeviceCode, SignInPollResponse) — see
// contracts/components/schemas/SignInStartResponseDeviceCode.yaml and
// SignInPollResponse.yaml, both `minimum: 1` / `maximum: 30`. The SPA's
// generated Zod schema enforces the same bound and throws on a value outside
// it, so every producer (session start, the poll path's slow_down widening,
// and its own-computed fallback when the session already vanished) MUST
// clamp through clampSignInIntervalSeconds rather than emit the vendor's or
// an arithmetically-widened value unclamped.
const (
	minSignInIntervalSeconds = 1
	maxSignInIntervalSeconds = 30
)

// clampSignInIntervalSeconds bounds v to
// [minSignInIntervalSeconds, maxSignInIntervalSeconds] — the contract's
// declared range for interval_seconds. Every producer of that field in this
// file MUST route through this helper: neither a vendor-advertised interval
// (which can exceed 30, e.g. a 60s interval on session start) nor a
// repeatedly slow_down-widened one (5 -> 10 -> 20 -> 40 after three
// back-offs) may reach the wire unclamped.
func clampSignInIntervalSeconds(v int) int {
	if v < minSignInIntervalSeconds {
		return minSignInIntervalSeconds
	}
	if v > maxSignInIntervalSeconds {
		return maxSignInIntervalSeconds
	}
	return v
}

// --- device-code session store -------------------------------------------
//
// CONCURRENCY CONTRACT — one lock, and no pointer ever escapes it.
//
// a.signInMu is the SINGLE mutex guarding both the signInSessions map AND
// every field of every deviceCodeSession it holds. No *deviceCodeSession is
// ever handed to a caller: readers get a value COPY, and every mutation goes
// through a helper below that does its work inside the critical section.
//
// This is not a style preference. The previous shape handed the LIVE map out
// of an already-released lock (deviceSessions()) and deviceCodeStatus ranged
// over it unlocked; a concurrent put/delete during that range is
// "fatal error: concurrent map iteration and map write" — Go's UNRECOVERABLE
// runtime fatal, which kills the whole gateway process, not just the
// request. It also wrote sess.resolved and sess.intervalSeconds after
// getDeviceSession had released the lock, a plain data race on the same
// fields a concurrent status read was reading. Both GET .../sign-in/status
// and POST .../sign-in are reachable UNAUTHENTICATED while onboarding is
// incomplete (FR-050), so this was a pre-auth remote crash.
//
// LOCK ORDER: signInMu is a LEAF. No other lock (a.configMu, the credential
// store's, the audit logger's) may be taken while it is held, and no helper
// below performs I/O — no vendor call, no store read, no audit write — under
// it. Every such call in the handlers happens between helper invocations,
// never inside one.
//
// The map is lazily initialized on first write, so a bare `restAPI{}` test
// literal that never touches a sign-in route need not know the field exists.

// putDeviceSession stores a COPY of sess under handle, and opportunistically
// sweeps sessions that can no longer produce a non-terminal outcome (see
// sweepDeviceSessionsLocked) so an abandoned dialog does not leak an entry
// for the life of the process.
func (a *restAPI) putDeviceSession(handle string, sess deviceCodeSession) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if a.signInSessions == nil {
		a.signInSessions = make(map[string]*deviceCodeSession)
	}
	a.sweepDeviceSessionsLocked(time.Now())
	stored := sess
	a.signInSessions[handle] = &stored
}

// getDeviceSession returns a COPY of the session under handle. Callers get a
// consistent snapshot they may read freely; they may not write it back —
// mutations go through resolveDeviceSession/widenDeviceSessionInterval.
func (a *restAPI) getDeviceSession(handle string) (deviceCodeSession, bool) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	sess, ok := a.signInSessions[handle]
	if !ok {
		return deviceCodeSession{}, false
	}
	return *sess, true
}

// resolveDeviceSession marks the session terminal AND removes it in one
// critical section — FR-044's single-use guard: signed_in / expired / denied
// is returned exactly once for a handle, and a concurrent status read can
// never observe the half-applied state (resolved set, entry still present)
// that two separate lock acquisitions would expose.
func (a *restAPI) resolveDeviceSession(handle string) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if sess, ok := a.signInSessions[handle]; ok {
		sess.resolved = true
	}
	delete(a.signInSessions, handle)
}

// widenDeviceSessionInterval applies the vendor's slow_down back-off to the
// stored session under the lock and returns the new floor, so subsequent
// polls (and a concurrent GET status) see it too. ok is false when the
// session is already gone — the caller then reports the widened value it
// computed for this response only.
func (a *restAPI) widenDeviceSessionInterval(handle string) (widened int, ok bool) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	sess, found := a.signInSessions[handle]
	if !found {
		return 0, false
	}
	widened = sess.intervalSeconds * 2
	if widened <= sess.intervalSeconds {
		widened = sess.intervalSeconds + 5
	}
	widened = clampSignInIntervalSeconds(widened)
	sess.intervalSeconds = widened
	return widened, true
}

// pendingDeviceSessionExists reports whether an OPEN (unresolved, unexpired)
// device-code session exists for providerID. The iteration happens INSIDE
// the critical section — that is the whole point of this method existing
// instead of a map accessor: the caller never holds a reference it could
// range over while another request writes the map.
func (a *restAPI) pendingDeviceSessionExists(providerID string) bool {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	now := time.Now()
	a.sweepDeviceSessionsLocked(now)
	for _, sess := range a.signInSessions {
		if sess.providerID == providerID && !sess.resolved && now.Before(sess.expiresAt) {
			return true
		}
	}
	return false
}

// sweepDeviceSessionsLocked drops every session that can no longer produce a
// non-terminal outcome: already resolved, or past its own expiresAt (which
// putDeviceSession caps at deviceCodeSessionMaxTTL). Without it a dialog the
// operator opened and closed leaked its entry until process exit — the poll
// path removes an entry only on a TERMINAL outcome that a client actually
// asked for. Deleting from a map being ranged over is defined behaviour in
// Go: an entry removed before it is reached is simply not produced.
//
// The caller MUST hold a.signInMu.
func (a *restAPI) sweepDeviceSessionsLocked(now time.Time) {
	for handle, sess := range a.signInSessions {
		if sess.resolved || !now.Before(sess.expiresAt) {
			delete(a.signInSessions, handle)
		}
	}
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

// signInRefreshOAuthConfig resolves the OAuth config for any sign-in path
// that may end up REFRESHING — i.e. any path that constructs a
// providers.NewStoreOAuthTokenSource — and bounds it at
// providers.MaxOAuthRefreshLockHold.
//
// The bound is not about this request's own latency. The refresh runs while
// holding the process-wide per-vendor lock that live agent turns and DELETE
// .../sign-in queue behind, so whoever holds it longest sets the stall
// everyone inherits. The agent path bounded itself at 20s explicitly, "so a
// hung vendor costs one turn"; the status poll did not, and quietly ran on
// the auth package's 30s interactive default — which made the agent's
// tighter bound not a ceiling at all, because an agent turn could be queued
// behind a status poll holding the same mutex for 30s. Both now read one
// constant, declared next to the lock it governs.
//
// oauthConfigFor stays the vendor-endpoint seam tests swap; this wrapper is
// deliberately separate so a test that points the gateway at an httptest
// server does not have to remember to restate the bound.
func signInRefreshOAuthConfig(providerID string) (auth.OAuthProviderConfig, error) {
	cfg, err := oauthConfigFor(providerID)
	if err != nil {
		return auth.OAuthProviderConfig{}, err
	}
	cfg.Timeout = providers_pkg.MaxOAuthRefreshLockHold
	return cfg, nil
}

// newStoreOAuthTokenSource is the token-source constructor the sign-in status
// path uses. A package-level var (mirroring oauthConfigFor just above) purely
// so a test can observe the config this file actually hands to it — the
// lock-hold bound is only meaningful if it reaches the token source, and a
// client-side deadline is not something the fake vendor on the other end can
// see.
var newStoreOAuthTokenSource = providers_pkg.NewStoreOAuthTokenSource

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
	interval = clampSignInIntervalSeconds(interval)
	now := time.Now()
	expiresAt := now.Add(deviceCodeSessionMaxTTL)
	a.putDeviceSession(handle, deviceCodeSession{
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
		a.resolveDeviceSession(req.DeviceAuthId)
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
		if setErr := providers_pkg.WriteStoreOAuthCredential(providerID, store, providers_pkg.OAuthCredential{
			AccessToken:  cred.AccessToken,
			RefreshToken: cred.RefreshToken,
			AccountID:    cred.AccountID,
			ExpiresAt:    cred.ExpiresAt,
		}); setErr != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to store credential")
			return
		}
		a.reRegisterOAuthSensitiveValues(store)
		if a.auditor != nil {
			if logErr := a.auditor.Log(&audit.Entry{
				Event:    "provider.signed_in",
				Decision: audit.DecisionAllow,
				User:     auditActor(r),
				Details: map[string]any{
					"provider":  providerID,
					"source_ip": a.clientIPWithLiveFallback(r),
				},
			}); logErr != nil {
				slogWarnAuditFailed("provider.signed_in", logErr)
			}
		}
		a.resolveDeviceSession(req.DeviceAuthId)
		jsonOK(w, gen.SignInPollResponse{State: gen.SignInPollResponseStateSignedIn})
		return
	}

	switch outcome {
	case auth.DeviceCodePollSlowDown:
		// Widen the session's own interval so subsequent polls (and a
		// concurrent GET status, were it to read it) see the new floor too.
		// The widening happens under signInMu; if the session vanished in
		// the meantime the widened value still governs THIS response.
		newInterval, stored := a.widenDeviceSessionInterval(req.DeviceAuthId)
		if !stored {
			newInterval = sess.intervalSeconds * 2
			if newInterval <= sess.intervalSeconds {
				newInterval = sess.intervalSeconds + 5
			}
			newInterval = clampSignInIntervalSeconds(newInterval)
		}
		jsonOK(w, gen.SignInPollResponse{
			State:           gen.SignInPollResponseStatePending,
			IntervalSeconds: &newInterval,
		})
	case auth.DeviceCodePollDenied:
		a.resolveDeviceSession(req.DeviceAuthId)
		jsonOK(w, gen.SignInPollResponse{State: gen.SignInPollResponseStateDenied})
	case auth.DeviceCodePollExpired:
		a.resolveDeviceSession(req.DeviceAuthId)
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
	// Any open, unresolved device-code session for this provider reports
	// pending. The scan runs inside signInMu — see the concurrency contract
	// above pendingDeviceSessionExists.
	if a.pendingDeviceSessionExists(providerID) {
		return gen.SignInStatus{State: gen.SignInStatusStatePending}
	}

	oauthCfg, cfgErr := signInRefreshOAuthConfig(providerID)
	if cfgErr != nil {
		return gen.SignInStatus{State: gen.SignInStatusStateNotSignedIn}
	}
	tokenSource := newStoreOAuthTokenSource(providerID, store, oauthCfg)
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

// cheapSignInRowStatus answers "is this configured sign_in row signed in"
// using ONLY cheap, local checks — no vendor call — for GET /providers'
// list render (ADR-068 T068-14 gap fix) and the PUT /providers/{id}
// sign_in response. device_code providers (openai-chatgpt/xai) are
// answered by peeking the stored "<vendor>_OAUTH" credential with no
// refresh attempt and no network I/O (providers_pkg.PeekStoreOAuthCred, in
// contrast to deviceCodeStatus/NewStoreOAuthTokenSource which may refresh
// against the vendor — that cost belongs to the explicit sign-in status
// poll, never a list render). cli_login providers reuse cliLoginStatus,
// which itself only reads a local file for codex-cli and is a constant
// not_signed_in for any other cli_login id — github-copilot included,
// deliberately: the real Copilot check runs the vendor CLI and spends a
// PREMIUM request against the operator's subscription (see
// rest_signin_copilot.go's handleCopilotSignInStatus doc comment); that
// cost belongs exclusively to the operator's explicit "Check sign-in"
// action. known is false whenever this cheap path has nothing more to say
// than the row's key-derived default (not signed in / unsupported
// provider) — callers must leave the row's existing status untouched.
func (a *restAPI) cheapSignInRowStatus(providerID string) (state gen.ProviderStatus, accountLabel string, known bool) {
	method, ok := a.signInMethodFor(providerID)
	if !ok {
		return "", "", false
	}
	if method == "cli_login" {
		st := cliLoginStatus(providerID)
		label := ""
		if st.AccountLabel != nil {
			label = *st.AccountLabel
		}
		switch st.State {
		case gen.SignInStatusStateSignedIn:
			return gen.ProviderStatusSignedIn, label, true
		case gen.SignInStatusStateExpired:
			return gen.ProviderStatusExpired, label, true
		default:
			return "", "", false
		}
	}
	// device_code: peek the stored credential only — no refresh, no vendor call.
	store, err := a.resolveSignInCredStore()
	if err != nil {
		return "", "", false
	}
	cred, err := providers_pkg.PeekStoreOAuthCred(providerID, store)
	if err != nil || cred == nil {
		return "", "", false
	}
	if !cred.ExpiresAt.IsZero() && time.Now().After(cred.ExpiresAt) {
		return gen.ProviderStatusExpired, cred.AccountID, true
	}
	return gen.ProviderStatusSignedIn, cred.AccountID, true
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
	token, accountID, mtimeExpiry, err := providers_pkg.ReadCodexCliCredentials()
	if err != nil {
		jsonErr(w, http.StatusNotFound, "no codex login found")
		return
	}
	store, storeErr := a.resolveSignInCredStore()
	if storeErr != nil {
		jsonErr(w, http.StatusServiceUnavailable, "credential store unavailable")
		return
	}
	// Prefer the JWT's own exp claim; fall back to
	// ReadCodexCliCredentials' auth.json-mtime+1h estimate (its documented
	// contract — see that function's doc comment) rather than leaving
	// expiresAt at its zero value. A zero ExpiresAt makes
	// needsOAuthRefresh treat the imported credential as "never needs
	// refresh" by design, so a non-JWT token would be reported signed-in
	// forever and only surface as a raw 401 from CodexProvider.Chat once it
	// actually expired — never routed through ErrProviderNeedsSignIn. If
	// somehow neither source produces a usable expiry, reject the import
	// outright rather than silently persisting an unbounded credential.
	expiresAt := mtimeExpiry
	if exp, ok := jwtUnverifiedExpiry(token); ok {
		expiresAt = exp
	}
	if expiresAt.IsZero() {
		jsonErr(w, http.StatusUnprocessableEntity, "could not establish an expiry for the imported credential")
		return
	}
	if setErr := providers_pkg.WriteStoreOAuthCredential("openai-chatgpt", store, providers_pkg.OAuthCredential{
		// FR-047: no refresh token is imported — this session ends at exp.
		AccessToken: token,
		AccountID:   accountID,
		ExpiresAt:   expiresAt,
	}); setErr != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to store credential")
		return
	}
	a.reRegisterOAuthSensitiveValues(store)
	if a.auditor != nil {
		if logErr := a.auditor.Log(&audit.Entry{
			Event:    "provider.signed_in",
			Decision: audit.DecisionAllow,
			User:     auditActor(r),
			Details: map[string]any{
				"provider":  "openai-chatgpt",
				"via":       "codex_cli_import",
				"source_ip": a.clientIPWithLiveFallback(r),
			},
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
	// O7: only a row that can actually SOURCE a device-code sign-in for its
	// vendor (providers_pkg.OAuthEntryOwner) may remove that vendor's
	// stored grant. providers_pkg.OAuthVendorID is many-to-one (openai AND
	// openai-chatgpt both resolve to vendor "openai"), so without this
	// gate, DELETE /providers/openai/sign-in — called against a row that
	// never signs in at all — destroyed a still-configured
	// openai-chatgpt row's live ChatGPT grant. A non-owning id has nothing
	// of its own to revoke, so this is a no-op success, not a failure.
	if providers_pkg.OAuthEntryOwner(providerID) {
		// providers_pkg.DeleteStoreOAuthCred, not a bare store.Delete: the
		// delete has to be ordered against an in-flight refresh exchange, or
		// sign-out is not a revocation. A refresh that started before the
		// operator clicked Sign out used to complete AFTER the delete and write
		// a fresh access+refresh pair straight back — the UI said "not signed
		// in", the audit log below recorded provider.signed_out, and the grant
		// the operator believed they had destroyed was live again with nothing
		// surfacing it. See that function's threat note. It can block for as
		// long as one bounded vendor exchange; that is the price of the ordering.
		if delErr := providers_pkg.DeleteStoreOAuthCred(providerID, store); delErr != nil {
			var notFound *credentials.NotFoundError
			if !errors.As(delErr, &notFound) {
				jsonErr(w, http.StatusInternalServerError, "failed to sign out")
				return
			}
			// NotFound = success (FR-048).
		}
	}
	a.reRegisterOAuthSensitiveValues(store)
	if a.auditor != nil {
		if logErr := a.auditor.Log(&audit.Entry{
			Event:    "provider.signed_out",
			Decision: audit.DecisionAllow,
			User:     auditActor(r),
			Details: map[string]any{
				"provider":  providerID,
				"source_ip": a.clientIPWithLiveFallback(r),
			},
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
