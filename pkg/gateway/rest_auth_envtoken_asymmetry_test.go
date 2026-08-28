// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The OMNIPUS_BEARER_TOKEN asymmetry, found by browser UAT on 2026-08-27.
//
// checkBearerAuth (auth.go) refuses the legacy env-token fallback once
// bearerAccountsConfigured is true — deliberately, so a stale env token cannot
// override a real account. requestPrincipalAuthenticated (rest_auth.go) did a
// bare constant-time compare against the same env var with no such guard, and
// `omnipus gateway` mints Gateway.CLIToken at every startup, so
// bearerAccountsConfigured is true on every install past first boot.
//
// Observed live, post-onboarding, with ONE stale token:
//
//	GET  /api/v1/providers                        -> 200
//	GET  /api/v1/providers/catalog                -> 200
//	POST /api/v1/providers/openai-chatgpt/sign-in -> 200, minting a real
//	                                                 device code at the vendor
//	GET  /api/v1/agents, /config, /workspaces …   -> 401
//
// A token the rest of the product correctly rejects still authenticated on
// exactly the routes the FR-050 gate protects, including the one with a real
// outbound vendor side effect.
//
// Every test here drives the PRODUCTION route table — registerAdditionalEndpoints
// through a real *http.ServeMux (testMuxRegistrar, routes_admin_test.go) — so a
// regression in either the registration or the gate is caught.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// ── scaffolding ─────────────────────────────────────────────────────────────

// uniqueTestSourceIP hands every request in this file its own source address.
//
// The sign-in and anonymous-provider-list rate limiters (signInStartLimiter,
// providerListAnonLimiter et al., rest_auth.go) are PROCESS-GLOBAL and keyed
// by client IP, and every httptest.NewRequest otherwise arrives from the same
// default 192.0.2.1. A new test that exercises a rate-limited route therefore
// spends budget the pre-existing tests need, and the package fails on
// whichever test happens to run past the ceiling — not on the one that spent
// it. Caught exactly that way on the CI worker: with these tests added,
// TestSignIn_CopilotDispatchDoesNotLeakToOtherProviders saw 429 instead of
// 200 (signInStartLimiter is 10/minute).
//
// TEST-NET-3 (203.0.113.0/24, RFC 5737) is reserved for documentation and is
// never routable, so it cannot collide with a real address.
var testSourceIPCounter atomic.Int64

func uniqueTestSourceIP() string {
	return fmt.Sprintf("203.0.113.%d:1234", testSourceIPCounter.Add(1)%254+1)
}

// viaRealMux issues one request through the production route table with the
// config snapshot configSnapshotMiddleware would have installed. An empty
// authHeader sends no Authorization header at all.
func viaRealMux(t *testing.T, api *restAPI, method, path, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = uniqueTestSourceIP()
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req.WithContext(ctx))
	return w
}

// seedCLITokenAccount installs the machine credential `omnipus gateway` mints
// at every startup (clitoken.EnsureCLIToken). This is the exact condition that
// makes bearerAccountsConfigured true on a real install — the reason the
// asymmetry was reachable in production rather than only in theory.
func seedCLITokenAccount(t *testing.T, api *restAPI) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Gateway.CLIToken = &config.TokenEntry{
		ID:   "clitok",
		Hash: config.BcryptHash("$2a$10$abcdefghijklmnopqrstuvABCDEFGHIJKLMNOPQRSTUVWXYZ012345"),
	}
	t.Cleanup(func() { cfg.Gateway.CLIToken = nil })
}

// countingDeviceCodeVendor points the device_code sign-in seam at a local
// server that COUNTS device-code requests, so "no device code was minted" is
// an observation of the vendor, not an inference from a status code.
func countingDeviceCodeVendor(t *testing.T) *atomic.Int64 {
	t.Helper()
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		resp := map[string]any{"device_auth_id": "vendor_das_1", "user_code": "WDJB-MJHT", "interval": 1}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	withDeviceCodeVendor(t, server)
	return &hits
}

// ── 1. accounts configured + stale env token → refused ──────────────────────

// TestEnvBearerToken_RefusedOnceAccountBasedAuthExists is the regression test
// for the UAT finding. With a CLI token minted (every install past first boot)
// the env fallback is dead on the withAuth path already; these FR-050-gated
// routes must agree.
func TestEnvBearerToken_RefusedOnceAccountBasedAuthExists(t *testing.T) {
	newOnboardedInstance := func(t *testing.T) *restAPI {
		t.Helper()
		api := newTestRestAPIWithHome(t)
		completeOnboarding(t, api)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "stale-env-token")
		seedCLITokenAccount(t, api)
		return api
	}

	t.Run("GET /providers is 401 and leaks no inventory", func(t *testing.T) {
		api := newOnboardedInstance(t)

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "Bearer stale-env-token")

		require.Equal(t, http.StatusUnauthorized, w.Code,
			"a token every withAuth route rejects must not authenticate here; body=%s", w.Body.String())
		assert.NotContains(t, w.Body.String(), `"providers"`,
			"a refused caller must not receive the provider inventory")
	})

	t.Run("POST /providers/openai-chatgpt/sign-in is 401 and mints no device code", func(t *testing.T) {
		api := newOnboardedInstance(t)
		vendorHits := countingDeviceCodeVendor(t)

		w := viaRealMux(t, api, http.MethodPost,
			"/api/v1/providers/openai-chatgpt/sign-in", "Bearer stale-env-token")

		require.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
		assert.Zero(t, vendorHits.Load(),
			"the outbound device-code request must never leave the process")
		api.signInMu.Lock()
		sessions := len(api.signInSessions)
		api.signInMu.Unlock()
		assert.Zero(t, sessions, "no device-code session may be opened for a refused caller")
	})

	t.Run("POST /providers/codex-cli/sign-in is 401", func(t *testing.T) {
		// The cli_login shape reaches no network at all, so this sub-case
		// distinguishes the gate from any incidental upstream failure: before
		// the fix it answered 200 with the vendor login command.
		api := newOnboardedInstance(t)

		w := viaRealMux(t, api, http.MethodPost,
			"/api/v1/providers/codex-cli/sign-in", "Bearer stale-env-token")

		require.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
		assert.NotContains(t, w.Body.String(), "codex login",
			"a refused caller must not be handed the sign-in instructions")
	})

	t.Run("a logged-in browser is not downgraded to anonymous by a stale env token", func(t *testing.T) {
		// withOptionalAuth's env branch short-circuits to `handler(w, r)`
		// with NO identity in context, ahead of the omnipus-session cookie
		// lookup. Un-gated, a stale OMNIPUS_BEARER_TOKEN in the Authorization
		// header therefore erased a genuinely logged-in session: 401 for a
		// user who is signed in. Sharing envBearerTokenAuthenticates with
		// requestPrincipalAuthenticated is what keeps the two paths agreeing
		// about the same token.
		api := newTestRestAPIWithHome(t)
		completeOnboarding(t, api)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "stale-env-token")

		const sessionPlaintext = "session-token-admin-12345678901234567890123"
		hash, err := bcrypt.GenerateFromPassword([]byte(sessionPlaintext), bcrypt.MinCost)
		require.NoError(t, err)
		cfg := api.agentLoop.GetConfig()
		cfg.Gateway.Users = []config.UserConfig{{
			Username:         "admin",
			PasswordHash:     "x",
			SessionTokenHash: config.BcryptHash(hash),
		}}

		mux := http.NewServeMux()
		api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		req.RemoteAddr = uniqueTestSourceIP()
		req.Header.Set("Authorization", "Bearer stale-env-token")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionPlaintext})
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"the cookie identity must win over a stale env token; body=%s", w.Body.String())
	})

	t.Run("a human Gateway.Users account closes it just as a CLI token does", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		completeOnboarding(t, api)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "stale-env-token")
		cfg := api.agentLoop.GetConfig()
		cfg.Gateway.Users = []config.UserConfig{{Username: "admin", PasswordHash: "x"}}

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "Bearer stale-env-token")

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})
}

// ── 2. no accounts configured → the first-boot/CI path is preserved ─────────

// TestEnvBearerToken_StillAuthenticatesWithNoAccountsConfigured pins the case
// OMNIPUS_BEARER_TOKEN exists for: an instance with no account-based auth at
// all. Narrowing the fallback must not delete it.
func TestEnvBearerToken_StillAuthenticatesWithNoAccountsConfigured(t *testing.T) {
	newHeadlessInstance := func(t *testing.T) *restAPI {
		t.Helper()
		api := newTestRestAPIWithHome(t)
		completeOnboarding(t, api)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "headless-ci-token")
		cfg := api.agentLoop.GetConfig()
		require.Empty(t, cfg.Gateway.Users, "precondition: no human account")
		require.Nil(t, cfg.Gateway.CLIToken, "precondition: no CLI token")
		return api
	}

	t.Run("GET /providers is served", func(t *testing.T) {
		api := newHeadlessInstance(t)

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "Bearer headless-ci-token")

		require.Equal(t, http.StatusOK, w.Code,
			"the documented headless deployment mode must keep working; body=%s", w.Body.String())
	})

	t.Run("POST /providers/codex-cli/sign-in is served", func(t *testing.T) {
		api := newHeadlessInstance(t)

		w := viaRealMux(t, api, http.MethodPost,
			"/api/v1/providers/codex-cli/sign-in", "Bearer headless-ci-token")

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var resp gen.SignInStartResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		variant, err := resp.AsSignInStartResponseCliLogin()
		require.NoError(t, err)
		assert.Equal(t, "codex login", variant.Command)
	})

	t.Run("a NON-matching token is still refused", func(t *testing.T) {
		api := newHeadlessInstance(t)

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "Bearer wrong-token")

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})

	t.Run("no credential at all is still refused", func(t *testing.T) {
		api := newHeadlessInstance(t)

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "")

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})
}

// ── 3. the FR-050 pre-auth onboarding window is unchanged ───────────────────

// TestPreAuthOnboardingWindow_UnchangedByEnvTokenNarrowing pins both edges of
// the window the fix must not move: it must not reopen on an instance that has
// an authentication authority, and it must not close on the genuine first run
// (the release blocker fixed in 8015d4c7 — a fresh install whose cli_token
// already exists must still reach the onboarding provider routes).
func TestPreAuthOnboardingWindow_UnchangedByEnvTokenNarrowing(t *testing.T) {
	t.Run("genuine first run: cli_token minted, onboarding incomplete — anonymous GET is 200", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seedCLITokenAccount(t, api)
		require.False(t, api.onboardingMgr.IsComplete(), "precondition: fresh install")

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "")

		require.Equal(t, http.StatusOK, w.Code,
			"the onboarding wizard must still reach the provider routes; body=%s", w.Body.String())
	})

	t.Run("onboarding complete — anonymous GET is 401", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		completeOnboarding(t, api)

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "")

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})

	t.Run("onboarding state unknown — anonymous GET is 401 (fails closed)", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.onboardingStateUnknown = true

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "")

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})

	t.Run("OMNIPUS_BEARER_TOKEN is still an authority that closes the window", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "env-token")
		require.False(t, api.onboardingMgr.IsComplete(), "precondition: onboarding incomplete")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		req.RemoteAddr = uniqueTestSourceIP()
		req = req.WithContext(context.WithValue(req.Context(),
			ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))
		assert.False(t, api.preAuthOnboardingWindowOpen(req),
			"an instance somebody CAN authenticate to has no pre-auth window")

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code,
			"an anonymous caller must not slip through the closed window; body=%s", w.Body.String())
	})

	t.Run("the env token itself opens no door once the CLI token exists", func(t *testing.T) {
		// The composite the UAT hit: window closed by the env token's own
		// presence AND the env token refused by the account gate.
		api := newTestRestAPIWithHome(t)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "stale-env-token")
		seedCLITokenAccount(t, api)

		w := viaRealMux(t, api, http.MethodGet, "/api/v1/providers", "Bearer stale-env-token")

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})
}
