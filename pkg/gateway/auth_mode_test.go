// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// withEdition pins config.Edition for the duration of the test, restoring the
// prior value on cleanup — the same save/restore pattern
// pkg/config/edition_test.go uses (config.EditionAuthMode(), and therefore
// activeAuthMode, derives from this package-level var). Also used by
// rest_integrations_auth_test.go.
func withEdition(t *testing.T, e string) {
	t.Helper()
	prev := config.Edition
	config.Edition = e
	t.Cleanup(func() { config.Edition = prev })
}

// newAuthModeTestAPI builds a minimal restAPI with no users configured and no
// platform trust anchor — just enough state for registerAdditionalEndpoints
// to run without panicking, regardless of which mode is active. It mirrors
// newPlatformAuthTestAPI's shape (rest_platform_auth_test.go) but has no
// opinion about platform config, since this test drives BOTH modes.
func newAuthModeTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"gateway":{"users":[]}}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}
	// ADR-0010 WP2 phase 2: platform mode's own in-flight/handoff/jti stores
	// moved to editions/platform.Provider as instance fields — a fresh
	// Provider per test isolates them without a package-var reset here. This
	// constructor registers no provider at all; tests that need one call
	// setSignInProviderForTest explicitly (signin_provider_test.go).
	return api
}

// authModeRealMux builds the production route table once, mirroring
// platformAuthRealMux (rest_platform_auth_test.go) without its per-request
// IP/XFF plumbing — this test only cares which paths exist and how their auth
// wrapper answers, not rate-limit behavior (pinned separately, per mode,
// where each mode's own tests already live: TestPlatformAuthLimiters_* and
// the restored TestHandleLogin_RateLimitBlocksAtLimit).
func authModeRealMux(t *testing.T, api *restAPI) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return mux
}

// TestAuthMode_RouteTable_PerMode is the acceptance proof workplan-review-2
// finding N4 names: no test before this one could pin the per-mode
// COMPOSITION itself, because only one mode ever existed in the tree at once.
// It builds the REAL registration (registerAdditionalEndpoints — the same
// function production wiring calls, not a parallel test-only table) under
// each mode and asserts the inverse route sets workplan-review finding 3
// described: platform mode 404s the local password login and requires a
// session for onboarding; local mode is the exact opposite.
func TestAuthMode_RouteTable_PerMode(t *testing.T) {
	t.Run("platform mode", func(t *testing.T) {
		withEdition(t, config.EditionHosted)
		api := newAuthModeTestAPI(t)
		mux := authModeRealMux(t, api)

		loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"x","password":"y"}`))
		loginReq.Header.Set("Content-Type", "application/json")
		loginW := httptest.NewRecorder()
		mux.ServeHTTP(loginW, loginReq)
		assert.Equal(t, http.StatusNotFound, loginW.Code,
			"platform mode must not register the local password login route at all")

		completeReq := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete",
			strings.NewReader(`{}`))
		completeReq.Header.Set("Content-Type", "application/json")
		completeW := httptest.NewRecorder()
		mux.ServeHTTP(completeW, completeReq)
		assert.Equal(t, http.StatusUnauthorized, completeW.Code,
			"platform mode's onboarding runs post-auth: an unauthenticated POST must be 401")
	})

	t.Run("local mode", func(t *testing.T) {
		withEdition(t, config.EditionCore)
		api := newAuthModeTestAPI(t)
		mux := authModeRealMux(t, api)

		loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"x","password":"y"}`))
		loginReq.Header.Set("Content-Type", "application/json")
		loginW := httptest.NewRecorder()
		mux.ServeHTTP(loginW, loginReq)
		assert.NotEqual(t, http.StatusNotFound, loginW.Code,
			"local mode must register the password login route; an unknown user is 401, never 404")

		startReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/platform/start",
			strings.NewReader(`{}`))
		startReq.Header.Set("Content-Type", "application/json")
		startW := httptest.NewRecorder()
		mux.ServeHTTP(startW, startReq)
		assert.Equal(t, http.StatusNotFound, startW.Code,
			"local mode must not register the platform sign-in start route at all")

		// NOTE: this test does not assert that local mode's
		// /api/v1/onboarding/complete accepts an anonymous caller. This
		// package's route table wraps it with withOptionalAuth in local mode
		// (mode.onboardingWrap, above) exactly as upstream does — that half
		// is WP2's. Whether HandleCompleteOnboarding itself (rest_onboarding.go,
		// WP5's file, not touched here) still demands an authenticated owner
		// unconditionally is a WP5 question; asserting on it here would
		// entangle this lane's acceptance test with a handler this lane does
		// not own.
	})
}

// TestAuthMode_UnknownEdition_RegistersNothing pins the fail-closed property
// activeAuthMode's doc comment claims: an edition config.EditionAuthMode()
// cannot derive a mode from yields the empty authMode, and
// registerAdditionalEndpoints must register NEITHER mode's sign-in routes NOR
// the onboarding routes for it — nothing is guessed. (WP1's EditionMisbuild
// already refuses such a binary at boot; this is the route-layer half of the
// same fail-closed posture, defense-in-depth if that boot guard is ever
// bypassed or changed.)
func TestAuthMode_UnknownEdition_RegistersNothing(t *testing.T) {
	withEdition(t, "enterprise") // not core, desktop or hosted
	api := newAuthModeTestAPI(t)
	mux := authModeRealMux(t, api)

	for _, path := range []string{
		"/api/v1/auth/login",
		"/api/v1/auth/platform/start",
		"/api/v1/auth/change-password",
		"/api/v1/auth/reauth",
		"/api/v1/onboarding/complete",
		"/api/v1/onboarding/probe-provider",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code,
				"an unrecognized edition must register NO sign-in or onboarding route; %s must 404", path)
		})
	}

	// /auth/validate and /auth/logout are common to both real modes, so they
	// stay registered even under an unknown edition — there is simply never
	// a session for an unauthenticated caller to validate or revoke, and the
	// existing withAuth gate already answers 401 for that, not 404.
	validateReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	validateW := httptest.NewRecorder()
	mux.ServeHTTP(validateW, validateReq)
	assert.Equal(t, http.StatusUnauthorized, validateW.Code,
		"the common /auth/validate route stays registered regardless of mode")
}

// TestAuthMode_Known distinguishes the two real modes from the empty
// "unknown edition" value — the guard registerSettingsAndAccountRoutes relies
// on to decide whether the onboarding routes may be registered at all.
func TestAuthMode_Known(t *testing.T) {
	api := newAuthModeTestAPI(t)
	assert.True(t, localAuthMode(api).known())
	assert.True(t, platformAuthMode(api).known())
	assert.False(t, authMode{}.known(), "the zero-value mode must report unknown")
}

// TestCorsAllowHeaders_XReauthTokenOnlyInLocalMode pins WP2 deliverable 2's
// CORS half: X-Reauth-Token is contributed by local mode only, because only
// local mode has a re-auth consent primitive for the SPA to replay it on.
func TestCorsAllowHeaders_XReauthTokenOnlyInLocalMode(t *testing.T) {
	// corsAllowHeaders() caches its composed value once per restAPI instance
	// (rest.go's corsAllowHeadersOnce) because in production the edition is a
	// build-time constant that never changes on a live instance. This test
	// therefore builds one fresh api per edition instead of flipping
	// config.Edition on a single shared instance — reusing one instance
	// across editions would just observe the first edition's cached value.
	localAPI := newAuthModeTestAPI(t)
	withEdition(t, config.EditionCore)
	assert.Contains(t, localAPI.corsAllowHeaders(), "X-Reauth-Token",
		"local mode's CORS allow-headers must include X-Reauth-Token")

	platformAPI := newAuthModeTestAPI(t)
	withEdition(t, config.EditionHosted)
	assert.NotContains(t, platformAPI.corsAllowHeaders(), "X-Reauth-Token",
		"platform mode has no re-auth consent primitive; its CORS allow-headers must not advertise the header")

	// Both modes must always carry the shared baseline.
	for _, tc := range []struct {
		edition string
		headers string
	}{
		{config.EditionCore, localAPI.corsAllowHeaders()},
		{config.EditionHosted, platformAPI.corsAllowHeaders()},
	} {
		for _, want := range []string{"Authorization", "Content-Type", "X-Csrf-Token"} {
			assert.Contains(t, tc.headers, want, "edition %s must keep the shared CORS baseline", tc.edition)
		}
	}
}

// TestAuthMode_CsrfExemptPaths_MatchTheMiddlewareDefault cross-checks
// authMode.csrfExemptPaths (this package's documented seam contract) against
// middleware/csrf.go's own independent config.EditionAuthMode() switch — the
// two cannot share a Go value without an import cycle (see csrf.go's
// defaultExemptPaths doc comment), so this test is what keeps them honest.
func TestAuthMode_CsrfExemptPaths_MatchTheMiddlewareDefault(t *testing.T) {
	api := newAuthModeTestAPI(t)

	for _, tc := range []struct {
		name    string
		edition string
		mode    authMode
	}{
		{"local", config.EditionCore, localAuthMode(api)},
		{"platform", config.EditionHosted, platformAuthMode(api)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withEdition(t, tc.edition)
			for _, path := range tc.mode.csrfExemptPaths {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
				w := httptest.NewRecorder()
				// Built the way gateway_boot.go builds it: the defaults plus the
				// active mode's own exempt paths.
				h := middleware.CSRFMiddleware(
					middleware.WithDefaultExempts(),
					middleware.WithExemptPaths(tc.mode.csrfExemptPaths...),
				)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				h.ServeHTTP(w, req)
				assert.Equal(t, http.StatusOK, w.Code,
					"%s: auth_mode.go claims %s is CSRF-exempt, but the boot-composed middleware refused it", tc.name, path)
			}
		})
	}
}
