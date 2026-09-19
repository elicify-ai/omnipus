// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// signin_provider_test.go — the seam's own acceptance tests (ADR-0010 WP2
// phase 2), plus the platform-trust-anchor tests that never belonged to
// rest_platform_auth.go's product logic in the first place: they pin THIS
// package's own PUT /api/v1/config blocklist (blocked_paths.go) and restart
// gate (rest_pending_restart.go) against the platform_auth config keys, not
// anything the moved provider code does. Both stayed here when
// rest_platform_auth_test.go moved to editions/platform/provider_test.go.
//
// What editions/platform/provider_test.go proves instead (against a fake
// Host, no pkg/gateway access): the PKCE/discovery/JWT-verification/
// replay-protection/handoff logic the provider itself owns. What this file
// proves: that a hook dropping a provider's wrapper or limiter goes red when
// driven through the REAL production route registration
// (registerAdditionalEndpoints) — the property no editions/platform-side test
// can observe, because registerAdditionalEndpoints is private to this
// package and a provider in a separate Go module cannot reach it. The
// fakeSignInProvider below stands in for editions/platform.Provider for that
// one purpose; it is not a copy of the real provider's business logic.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// setSignInProviderForTest installs p (which may be nil) as the registered
// provider for the duration of the test, restoring the previous value on
// cleanup. Package-private: only this package's own seam tests may reach
// into the process-global registration slot RegisterSignInProvider guards in
// production.
func setSignInProviderForTest(t *testing.T, p SignInProvider) {
	t.Helper()
	prev := signInProvider
	signInProvider = p
	t.Cleanup(func() { signInProvider = prev })
}

// ── RegisterSignInProvider itself ───────────────────────────────────────────

func TestRegisterSignInProvider_NilPanics(t *testing.T) {
	setSignInProviderForTest(t, nil)
	assert.Panics(t, func() { RegisterSignInProvider(nil) },
		"registering a nil provider must panic, not silently leave platform mode unconfigured")
}

func TestRegisterSignInProvider_TwicePanics(t *testing.T) {
	setSignInProviderForTest(t, nil)
	RegisterSignInProvider(&fakeSignInProvider{})
	assert.Panics(t, func() { RegisterSignInProvider(&fakeSignInProvider{}) },
		"a second registration must panic — ADR-0010 decision 3 forbids a runtime auth-mode/provider switch")
}

// ── deliverable 2: no provider registered ───────────────────────────────────

// TestPlatformAuthMode_NoProviderRegistered_ServesOnlyStart503 is WP2
// deliverable 2's acceptance proof: platform mode with no SignInProvider
// registered serves NO sign-in routes except PlatformAuthStartPath, which
// answers 503 rather than 404 — a signed-out SPA gets one stable, honest
// answer instead of a status it cannot tell apart from "not built yet".
func TestPlatformAuthMode_NoProviderRegistered_ServesOnlyStart503(t *testing.T) {
	setSignInProviderForTest(t, nil)
	withEdition(t, config.EditionHosted)
	api := newAuthModeTestAPI(t)
	mux := authModeRealMux(t, api)

	startReq := httptest.NewRequest(http.MethodPost, PlatformAuthStartPath, strings.NewReader(`{}`))
	startReq.Header.Set("Content-Type", "application/json")
	startW := httptest.NewRecorder()
	mux.ServeHTTP(startW, startReq)
	assert.Equal(t, http.StatusServiceUnavailable, startW.Code,
		"platform mode with no registered provider must answer 503 on the start route, not 404 and not 200")
	assert.Contains(t, startW.Body.String(), "no sign-in provider is registered")

	for _, path := range []string{
		"/api/v1/auth/session",
		"/api/v1/auth/platform/claim",
		"/auth/callback",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code,
				"%s must not be registered at all when no provider is registered", path)
		})
	}
}

// ── the route-table composition itself ──────────────────────────────────────

// fakeSignInProvider is a minimal stand-in for editions/platform.Provider,
// used ONLY to prove the seam — that a registered provider's declared wrap
// kind, CSRF exemption and rate limiter survive platformAuthMode's
// composition and the real route registration. It deliberately builds its
// limiters the same way (Host.NewRateLimiter / NewReadSizedRateLimiter, once,
// cached) and to the same documented ceilings editions/platform.Provider
// uses, so the rate-limit test below is meaningful evidence that a hook
// dropping a wrapper or limiter would go red — not just a syntax check of an
// unrelated toy.
type fakeSignInProvider struct {
	once    sync.Once
	limiter RateLimiter
	calls   int
	callsMu sync.Mutex
}

func (f *fakeSignInProvider) Name() string { return "fake" }

func (f *fakeSignInProvider) Routes(h Host) []ProviderRoute {
	f.once.Do(func() { f.limiter = h.NewRateLimiter(10, time.Minute) })
	ok := func(w http.ResponseWriter, r *http.Request) {
		f.callsMu.Lock()
		f.calls++
		f.callsMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
	return []ProviderRoute{
		{Path: PlatformAuthStartPath, Handler: f.limiter.Wrap(ok), CSRFExempt: true},
		{Path: "/api/v1/auth/session", Handler: ok},
		{Path: "/api/v1/auth/platform/claim", Handler: ok},
		{Path: "/auth/callback", Handler: ok, Wrap: ProviderRouteBare},
	}
}

// TestPlatformAuthMode_MapsProviderRouteWrapAndCSRFExempt pins the
// composition logic directly: each ProviderRoute's declared Wrap becomes the
// matching authRouteWrap, a provider route never carries auth_mode.go's OWN
// limiter field (rate limiting is the provider's own responsibility per WP2
// deliverable 1), and only a route the provider marked CSRFExempt appears in
// the mode's csrfExemptPaths.
func TestPlatformAuthMode_MapsProviderRouteWrapAndCSRFExempt(t *testing.T) {
	api := newAuthModeTestAPI(t)
	setSignInProviderForTest(t, &fakeSignInProvider{})

	mode := platformAuthMode(api)
	require.Len(t, mode.authRoutes, 4)

	byPath := make(map[string]authRoute, len(mode.authRoutes))
	for _, rt := range mode.authRoutes {
		byPath[rt.path] = rt
	}

	assert.Equal(t, authWrapOptionalAuth, byPath[PlatformAuthStartPath].wrap)
	assert.Equal(t, authWrapOptionalAuth, byPath["/api/v1/auth/session"].wrap)
	assert.Equal(t, authWrapOptionalAuth, byPath["/api/v1/auth/platform/claim"].wrap)
	assert.Equal(t, authWrapBare, byPath["/auth/callback"].wrap,
		"the callback route must carry NO auth wrapper — ProviderRouteBare")

	for path, rt := range byPath {
		assert.Nil(t, rt.limiter, "%s must not carry an auth_mode.go-owned limiter", path)
	}

	assert.Equal(t, []string{PlatformAuthStartPath}, mode.csrfExemptPaths,
		"only the route the provider declared CSRFExempt must appear in the mode's exempt set")
}

// TestPlatformAuthMode_ProviderLimiterSurvivesRealRegistration is the proof
// WP2 deliverable 1 names: "a hook that drops a wrapper or limiter goes red".
// It drives the REAL production route registration
// (registerAdditionalEndpoints, via testMuxRegistrar — the same pattern
// authModeRealMux and the pre-move platformAuthRealMux used) with a
// registered provider whose /start route is rate-limited on ITS OWN limiter,
// built via Host.NewRateLimiter. A fresh spoofed X-Forwarded-For per attempt
// must not buy extra attempts (gateway.trust_xff is false, the posture every
// desktop/hosted instance ships with) — the same regression class
// TestPlatformAuthStart_SpoofedXFFCannotBuyExtraAttempts pinned before the
// move.
//
// MUTATION ORACLE: delete `p.Routes(h)`'s call into f.limiter.Wrap(ok) in
// fakeSignInProvider (i.e. register `ok` unwrapped) and this test fails —
// every one of the 11 attempts returns 200.
func TestPlatformAuthMode_ProviderLimiterSurvivesRealRegistration(t *testing.T) {
	withEdition(t, config.EditionHosted)
	api := newAuthModeTestAPI(t)
	fp := &fakeSignInProvider{}
	setSignInProviderForTest(t, fp)

	cfg := api.agentLoop.GetConfig()
	require.False(t, cfg.Gateway.TrustXFF,
		"this test asserts the DEFAULT posture — trust_xff must be false here")

	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	serve := func(req *http.Request, sourceIP, spoofedXFF string) *httptest.ResponseRecorder {
		req.RemoteAddr = sourceIP + ":54321"
		if spoofedXFF != "" {
			req.Header.Set("X-Forwarded-For", spoofedXFF)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req.WithContext(withConfigSnapshot(req.Context(), cfg)))
		return w
	}
	newStart := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, PlatformAuthStartPath, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		return req
	}

	const sourceIP = "203.0.113.201"
	for i := 1; i <= 10; i++ {
		w := serve(newStart(), sourceIP, fmt.Sprintf("10.9.9.%d", i))
		require.Equal(t, http.StatusOK, w.Code,
			"attempt %d is inside the provider's documented ceiling and must succeed", i)
	}
	w := serve(newStart(), sourceIP, "10.9.9.99")
	assert.Equal(t, http.StatusTooManyRequests, w.Code,
		"the 11th call must be refused by the provider's OWN limiter, surviving real registration")
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), "rate limit exceeded")
}

// ── the platform_auth trust anchor's three gates ────────────────────────────
//
// These three never tested the moved provider code — they test THIS
// package's own PUT /api/v1/config blocklist (blocked_paths.go) and restart
// gate (rest_pending_restart.go) against the security.platform_auth.* config
// keys, which stay in the engine (pkg/config/platform_auth.go, WP2's own
// text: "the engine must read the trust anchor to verify sessions"). They
// moved here, unchanged, when rest_platform_auth_test.go moved to
// editions/platform.

// TestTrustAnchor_IsBlockedFromTheConfigEndpoint — ADR-0005 E3 in one line: an
// agent that can write the trust anchor points it at a key it controls and
// mints itself a session. The generic PUT /api/v1/config surface must refuse
// it at the leaf, at the intermediate, at the root, and as a dot-path literal
// — the four shapes a write can arrive in.
func TestTrustAnchor_IsBlockedFromTheConfigEndpoint(t *testing.T) {
	bodies := map[string]map[string]any{
		"leaf": {"security": map[string]any{"platform_auth": map[string]any{
			"keys": []any{map[string]any{"kid": "evil", "alg": "EdDSA", "public_key": "AAAA"}},
		}}},
		"intermediate": {"security": map[string]any{"platform_auth": map[string]any{"issuer": "https://evil.example"}}},
		"root":         {"security": map[string]any{}},
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			got, blocked := matchBlockedPath(body, blockedPaths)
			assert.True(t, blocked, "the trust anchor must be refused in this shape too")
			assert.Equal(t, "security", got, "the whole security subtree is blocked, not one leaf")
		})
	}
}

// TestTrustAnchor_DotPathLiteralDeeperThanABlockedEntry pins a real limit of
// matchBlockedPath, and the separate reason the anchor is safe anyway.
func TestTrustAnchor_DotPathLiteralDeeperThanABlockedEntry(t *testing.T) {
	body := map[string]any{"security.platform_auth.keys": []any{
		map[string]any{"kid": "evil", "alg": "EdDSA", "public_key": "AAAA"},
	}}
	_, blocked := matchBlockedPath(body, blockedPaths)
	require.False(t, blocked,
		"documenting the walker's actual behaviour: a dot-path literal deeper than a blocked "+
			"entry is not matched by the ancestor rule")

	// And the reason that is harmless: it lands nowhere.
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	var cfg config.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Empty(t, cfg.Security.PlatformAuth.Keys,
		"a dotted key must not reach config.Security — if this ever fails, the walker's gap "+
			"above has become a way to rewrite the trust anchor")
}

// TestTrustAnchor_IsRestartGated — who vouches for the people signing in is a
// boot decision. A setting that appears to change while the running process
// still enforces the old value is its own class of bug, and on an auth anchor
// it is the dangerous kind.
func TestTrustAnchor_IsRestartGated(t *testing.T) {
	gated := make(map[config.ConfigKey]bool, len(RestartGatedKeys))
	for _, k := range RestartGatedKeys {
		gated[k] = true
	}
	for _, k := range []config.ConfigKey{
		config.SecurityPlatformAuthIssuer,
		config.SecurityPlatformAuthClientID,
		config.SecurityPlatformAuthInstanceID,
		config.SecurityPlatformAuthKeys,
	} {
		assert.True(t, gated[k], "%s must be restart-gated", k)
	}
}

// ── proof the wire-contract schemas stay wired (WP2a) ───────────────────────

// TestSignInProviderSchemas_AreWired proves the two schema names
// editions/platform.Provider decodes against (PlatformAuthStartRequest,
// PlatformAuthClaimRequest — WP2a: the platform endpoints' contracts stay in
// engine/omnipus/contracts/) are actually compiled and reachable through
// Host.DecodeAndValidate. A schema name that was renamed or removed on this
// side without a matching update on the provider's side would 500 "inbound
// schema unavailable" here the instant gateway.validate_inbound is turned on
// — this test is what catches that before it ships.
func TestSignInProviderSchemas_AreWired(t *testing.T) {
	api := newTestRestAPIWithValidation(t)
	h := api.signInHost()

	for _, schema := range []string{"PlatformAuthStartRequest", "PlatformAuthClaimRequest"} {
		t.Run(schema, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			var dst map[string]any
			ok := h.DecodeAndValidate(w, req, schema, &dst)
			assert.False(t, ok, "an empty object must fail schema validation, not pass it")
			assert.NotEqual(t, http.StatusInternalServerError, w.Code,
				"schema %q must be compiled and wired — a 500 here means the schema file is missing", schema)
		})
	}
}
