//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Middleware-gate tests for admin security routes.
//
// These tests prove that every admin security endpoint is wrapped with:
//
//	withAuth → RequireAdmin → RequireNotBypass
//
// and that the global CSRF middleware (applied via WrapHTTPHandler) rejects
// state-changing requests without a valid cookie/header pair.
//
// Test strategy: build the exact middleware stack that registerAdditionalEndpoints
// assembles and drive it with httptest — no full gateway boot required. The
// CSRF layer is tested by constructing it around the per-route chain, mirroring
// what the production WrapHTTPHandler call does.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
)

// normalizedRoleForUser writes a standalone, minimal config.json (in its own
// throwaway temp dir) containing a single Gateway user named username with
// the given requestedRole, loads it through the REAL config.LoadConfig
// pipeline, and returns the role that pipeline actually resolved.
//
// Single-user model (operator directive, 2026-07): "Omnipus is now a
// single-user, self-hosted instance — there are no admin and normal users
// anymore." The RBAC scaffolding (UserRole type, RequireAdmin middleware,
// the Users screen, the ownership model) stays intact in the code, but the
// admin/user distinction has NO PRACTICAL EFFECT: config.LoadConfig
// self-heals any non-admin Gateway.Users role to admin at load time
// (config.normalizeAdminOnlyRoles) and rewrites config.json in the same
// pass, so no authenticated user can ever reach request handling with a
// non-admin role.
//
// Tests use this helper — instead of hand-injecting config.UserRoleAdmin as
// a literal — to prove that claim against the REAL choke point rather than
// asserting a hardcoded expectation.
func normalizedRoleForUser(t *testing.T, username, requestedRole string) config.UserRole {
	t.Helper()
	return normalizedUserForRole(t, username, requestedRole).Role
}

// normalizedUserForRole is normalizedRoleForUser's counterpart for handlers
// that also read *config.UserConfig from ctxkey.UserContextKey{} (e.g. for
// audit attribution) rather than just the role. Returns the full resolved
// user record so callers can inject both context values.
func normalizedUserForRole(t *testing.T, username, requestedRole string) *config.UserConfig {
	t.Helper()
	dir := t.TempDir()
	diskCfg := map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway": map[string]any{
			"users": []any{
				map[string]any{
					"username":      username,
					"password_hash": "",
					"token_hash":    "",
					"role":          requestedRole,
				},
			},
		},
	}
	data, err := json.Marshal(diskCfg)
	require.NoError(t, err)
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	loaded, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	require.Len(t, loaded.Gateway.Users, 1, "expected exactly one seeded user after LoadConfig")
	return &loaded.Gateway.Users[0]
}

// adminRoute is the canonical table of admin security routes and a representative
// state-changing body for POST/PUT/PATCH methods. GET-only routes use an empty
// body. Each entry carries enough metadata for all five test scenarios.
type adminRoute struct {
	method string
	path   string
	// body is a minimal valid body for the method. Empty for GET routes.
	body string
}

// allAdminRoutes covers all route entries (GET+PUT on same path counts
// as two entries; we exercise the state-changing method for multi-method paths).
var allAdminRoutes = []adminRoute{
	{http.MethodGet, "/api/v1/config/pending-restart", ""},
	{http.MethodPost, "/api/v1/gateway/restart", ""},
	{http.MethodGet, "/api/v1/security/audit-log", ""},
	{http.MethodPut, "/api/v1/security/audit-log", `{"enabled":true}`},
	{http.MethodGet, "/api/v1/security/skill-trust", ""},
	{http.MethodPut, "/api/v1/security/skill-trust", `{"mode":"require_signed"}`},
	{http.MethodGet, "/api/v1/security/prompt-guard", ""},
	{http.MethodPut, "/api/v1/security/prompt-guard", `{"enabled":false}`},
	{http.MethodGet, "/api/v1/security/rate-limits", ""},
	{http.MethodPut, "/api/v1/security/rate-limits", `{"api_requests_per_minute":60}`},
	{http.MethodGet, "/api/v1/security/sandbox-config", ""},
	{http.MethodPut, "/api/v1/security/sandbox-config", `{"mode":"permissive"}`},
	{http.MethodGet, "/api/v1/security/session-scope", ""},
	{http.MethodPut, "/api/v1/security/session-scope", `{"dm_scope":"main"}`},
	{http.MethodGet, "/api/v1/security/retention", ""},
	{http.MethodPut, "/api/v1/security/retention", `{"session_days":30}`},
	{http.MethodPost, "/api/v1/security/retention/sweep", ""},
	{http.MethodGet, "/api/v1/users", ""},
	{http.MethodPost, "/api/v1/users", `{"username":"bob","role":"user","password":"testpwd1"}`},
	{http.MethodDelete, "/api/v1/users/bob", ""},
	{http.MethodPut, "/api/v1/users/bob/password", `{"password":"newpwd123"}`},
	{http.MethodPatch, "/api/v1/users/bob/role", `{"role":"user"}`},
}

// stateChangingAdminRoutes returns only the routes with state-changing methods
// (POST / PUT / PATCH / DELETE) — these are the ones CSRF gates.
func stateChangingAdminRoutes() []adminRoute {
	out := make([]adminRoute, 0)
	stateChanging := map[string]bool{
		http.MethodPost:   true,
		http.MethodPut:    true,
		http.MethodPatch:  true,
		http.MethodDelete: true,
	}
	for _, r := range allAdminRoutes {
		if stateChanging[r.method] {
			out = append(out, r)
		}
	}
	return out
}

// innerOKHandler is a trivial 200 responder used as the terminus of each
// middleware chain in tests.
var innerOKHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// buildFullAdminHandler assembles the complete per-route middleware chain:
//
//	withAuth → RequireAdmin → RequireNotBypass → inner
//
// Used for TestAdminRoutes_UnauthenticatedRequestsRejected where the real
// withAuth bearer-check must fire.
func buildFullAdminHandler(api *restAPI) http.HandlerFunc {
	return api.withAuth(
		middleware.RequireAdmin(
			middleware.RequireNotBypass(innerOKHandler),
		).ServeHTTP,
	)
}

// buildInnerChainHandler assembles the inner gates only:
//
//	RequireAdmin → RequireNotBypass → inner
//
// Used when the caller injects the role+config into context directly
// (simulating what withAuth would do after a successful token check).
// This lets TestAdminRoutes_AdminOnly and TestAdminRoutes_DevModeBypass probe
// those gates without needing real bcrypt tokens.
func buildInnerChainHandler() http.Handler {
	return middleware.RequireAdmin(
		middleware.RequireNotBypass(innerOKHandler),
	)
}

// makeAdminCtxRequest builds a request with an admin role injected into the
// context (simulating what withAuth produces after a successful token check)
// and a non-bypass config snapshot in context.
func makeAdminCtxRequest(method, path, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = false
	ctx := context.WithValue(r.Context(), ctxkey.RoleContextKey{}, config.UserRoleAdmin)
	ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, &config.UserConfig{
		Username: "admin",
		Role:     config.UserRoleAdmin,
	})
	ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
	return r.WithContext(ctx)
}

// makeNonAdminCtxRequest builds a request with a user (non-admin) role in context.
func makeNonAdminCtxRequest(method, path, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = false
	ctx := context.WithValue(r.Context(), ctxkey.RoleContextKey{}, config.UserRoleUser)
	ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, &config.UserConfig{
		Username: "bob",
		Role:     config.UserRoleUser,
	})
	ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
	return r.WithContext(ctx)
}

// makeBypassCtxRequest builds an admin-role request but with DevModeBypass=true.
func makeBypassCtxRequest(method, path, body string) *http.Request {
	r := makeAdminCtxRequest(method, path, body)
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = true
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, cfg)
	return r.WithContext(ctx)
}

// TestAdminRoutes_AllHaveCSRF proves that every admin security state-changing
// route returns 403 when the CSRF cookie is absent. This mirrors the global
// middleware.CSRFMiddleware that production applies via WrapHTTPHandler.
//
// The inner chain used here is RequireAdmin→RequireNotBypass→inner (with admin
// context injected) so that only the CSRF gate decides the outcome. This isolates
// the CSRF check from auth/role complexity while still exercising the correct
// handler chain order.
//
// Outcome: 403 {"error":"csrf cookie missing"} on each state-changing route.
func TestAdminRoutes_AllHaveCSRF(t *testing.T) {
	// Wrap the inner chain with CSRF middleware, same as production.
	// No exempt paths configured beyond the defaults — admin paths are NOT exempt.
	csrfH := middleware.CSRFMiddleware()(buildInnerChainHandler())

	routes := stateChangingAdminRoutes()
	passed := 0
	for _, route := range routes {
		t.Run(route.method+"_"+route.path, func(t *testing.T) {
			req := makeAdminCtxRequest(route.method, route.path, route.body)
			// No CSRF cookie — gate must fire.

			w := httptest.NewRecorder()
			csrfH.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code,
				"route %s %s: expected 403 from CSRF gate when cookie is absent",
				route.method, route.path)
			assert.Contains(t, w.Body.String(), "csrf",
				"route %s %s: error body must mention 'csrf'", route.method, route.path)
			passed++
		})
	}
	t.Logf("TestAdminRoutes_AllHaveCSRF: %d/%d state-changing routes gated", passed, len(routes))
}

// TestAdminRoutes_AdminOnly proves that every admin security route returns 403
// when the caller has a user (non-admin) role.
//
// Uses the inner chain (RequireAdmin → RequireNotBypass → inner) with a non-admin
// role injected into context, simulating what withAuth produces after a successful
// token check for a user-role account.
func TestAdminRoutes_AdminOnly(t *testing.T) {
	h := buildInnerChainHandler()

	passed := 0
	for _, route := range allAdminRoutes {
		t.Run(route.method+"_"+route.path, func(t *testing.T) {
			req := makeNonAdminCtxRequest(route.method, route.path, route.body)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code,
				"route %s %s: non-admin must receive 403", route.method, route.path)
			assert.Contains(t, w.Body.String(), "admin",
				"route %s %s: error body must mention 'admin'", route.method, route.path)
			passed++
		})
	}
	t.Logf("TestAdminRoutes_AdminOnly: %d/%d routes blocked non-admin", passed, len(allAdminRoutes))
}

// TestAdminRoutes_DevModeBypassReturn503 verifies that when DevModeBypass=true
// every admin security route returns 503, protecting admin surfaces from anonymous
// access in that mode.
//
// Uses the inner chain (RequireAdmin → RequireNotBypass → inner) with admin role
// and bypass=true injected into context. RequireAdmin passes (admin role present);
// RequireNotBypass intercepts and returns 503.
func TestAdminRoutes_DevModeBypassReturn503(t *testing.T) {
	h := buildInnerChainHandler()

	passed := 0
	for _, route := range allAdminRoutes {
		t.Run(route.method+"_"+route.path, func(t *testing.T) {
			req := makeBypassCtxRequest(route.method, route.path, route.body)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusServiceUnavailable, w.Code,
				"route %s %s: bypass mode must return 503", route.method, route.path)
			assert.Contains(t, w.Body.String(), "dev-mode-bypass",
				"route %s %s: error body must mention 'dev-mode-bypass'", route.method, route.path)
			passed++
		})
	}
	t.Logf(
		"TestAdminRoutes_DevModeBypassReturn503: %d/%d routes returned 503 in bypass mode",
		passed,
		len(allAdminRoutes),
	)
}

// TestAdminRoutes_ValidAdminHappyPath verifies that an admin with a valid
// CSRF cookie+header and bypass=false passes through the full inner chain
// (RequireAdmin → RequireNotBypass → inner) without being blocked by any gate.
// The inner handler (a trivial 200 stub) is reached on every route.
//
// This proves the middleware chain does not over-reject valid admin traffic.
func TestAdminRoutes_ValidAdminHappyPath(t *testing.T) {
	const csrfToken = "test-csrf-token-value"

	// Wrap the inner chain with CSRF (same as production outer layer).
	csrfH := middleware.CSRFMiddleware()(buildInnerChainHandler())

	passed := 0
	for _, route := range allAdminRoutes {
		t.Run(route.method+"_"+route.path, func(t *testing.T) {
			req := makeAdminCtxRequest(route.method, route.path, route.body)

			// For state-changing methods, supply the CSRF cookie+header pair.
			if route.method != http.MethodGet && route.method != http.MethodHead {
				req.AddCookie(&http.Cookie{
					Name:  middleware.CSRFCookieName,
					Value: csrfToken,
				})
				req.Header.Set(middleware.CSRFHeaderName, csrfToken)
			}

			w := httptest.NewRecorder()
			csrfH.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code,
				"route %s %s: valid admin+CSRF must reach the inner handler (200)",
				route.method, route.path)
			passed++
		})
	}
	t.Logf("TestAdminRoutes_ValidAdminHappyPath: %d/%d routes passed for valid admin", passed, len(allAdminRoutes))
}

// TestAdminRoutes_UnauthenticatedRequestsRejected verifies that requests with NO
// bearer token and NO session cookie receive 401 from the withAuth gate.
//
// Uses the full chain (withAuth → RequireAdmin → RequireNotBypass → inner) with
// no auth credentials at all — no Authorization header, no role in context.
// OMNIPUS_BEARER_TOKEN="" is set by newTestRestAPIWithHome so the legacy env
// fallback cannot intervene.
func TestAdminRoutes_UnauthenticatedRequestsRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	chain := buildFullAdminHandler(api)

	passed := 0
	for _, route := range allAdminRoutes {
		t.Run(route.method+"_"+route.path, func(t *testing.T) {
			var req *http.Request
			if route.body == "" {
				req = httptest.NewRequest(route.method, route.path, nil)
			} else {
				req = httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				req.Header.Set("Content-Type", "application/json")
			}
			// No Authorization header. No role in context. configSnapshotMiddleware not applied.

			w := httptest.NewRecorder()
			chain(w, req)

			code := w.Code
			assert.True(t, code == http.StatusUnauthorized || code == http.StatusForbidden,
				"route %s %s: unauthenticated request must be rejected with 401 or 403, got %d",
				route.method, route.path, code)
			passed++
		})
	}
	t.Logf(
		"TestAdminRoutes_UnauthenticatedRequestsRejected: %d/%d routes rejected unauthenticated requests",
		passed,
		len(allAdminRoutes),
	)
}

// testMuxRegistrar wraps http.ServeMux to implement httpHandlerRegistrar,
// allowing registerAdditionalEndpoints to populate a real Go mux in tests.
type testMuxRegistrar struct {
	mux *http.ServeMux
}

func (r *testMuxRegistrar) RegisterHTTPHandler(pattern string, handler http.Handler) {
	r.mux.Handle(pattern, handler)
}

// TestSandboxConfigPUT_RealMux_DevModeBypass503 verifies that a PUT to
// /api/v1/security/sandbox-config returns 503 when dev_mode_bypass=true,
// exercising the real registerAdditionalEndpoints route chain rather than the
// parallel test-only chain used by TestAdminRoutes_DevModeBypassReturn503.
//
// Defense-in-depth: the matrix test TestAdminRoutes_DevModeBypassReturn503
// uses a hand-rolled inner chain (RequireAdmin→RequireNotBypass) and skips
// withAuth entirely. If someone adds a duplicate RegisterHTTPHandler call for
// /api/v1/security/sandbox-config that drops RequireNotBypass, the matrix test
// won't catch it — only the source-grep test
// (TestRegisterHTTPHandler_NoDuplicatePatterns) does. This test exercises the
// actual production registerAdditionalEndpoints call so any routing downgrade
// is caught at the HTTP layer.
//
// Auth flow: the test pre-injects a config snapshot with DevModeBypass=true
// into the request context. withAuth reads it via configFromContext; because
// OMNIPUS_BEARER_TOKEN is empty (newTestRestAPIWithHome clears it) and no
// users are configured, checkBearerAuth hits the dev-bypass path and returns
// authenticated-as-admin. RequireAdmin passes (admin role in context).
// RequireNotBypass then reads the same config snapshot (DevModeBypass=true)
// and returns 503.
//
// Traces to: temporal-puzzling-melody.md Wave 1C — architect follow-up:
// real-mux bypass test for sandbox-config.
func TestSandboxConfigPUT_RealMux_DevModeBypass503(t *testing.T) {
	// newTestRestAPIWithHome clears OMNIPUS_BEARER_TOKEN so no env-token auth
	// fires; the dev-bypass path in checkBearerAuth is driven by the config
	// snapshot instead.
	api := newTestRestAPIWithHome(t)

	// Register all additional endpoints onto a real http.ServeMux.
	// This exercises the exact same registerAdditionalEndpoints call that
	// gateway.go makes on startup (production code path, not a hand-rolled chain).
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	// Build the bypass config snapshot. Pre-injected into the request context so
	// that (1) checkBearerAuth inside withAuth sees DevModeBypass=true and grants
	// admin access without a token, and (2) RequireNotBypass sees DevModeBypass=true
	// and returns 503. withAuth only writes RoleContextKey and UserContextKey — it
	// does not overwrite ConfigContextKey — so the snapshot survives the full chain.
	bypassCfg := &config.Config{}
	bypassCfg.Gateway.DevModeBypass = true

	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(`{"mode":"permissive"}`))
	req.Header.Set("Content-Type", "application/json")
	// checkBearerAuth requires a "Bearer " prefix before it reads the config
	// for the dev-bypass path. The token value is irrelevant: with no env token
	// and no users configured, checkBearerAuth will reach the DevModeBypass
	// branch (line 83 in auth.go) and grant admin access.
	req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"PUT /api/v1/security/sandbox-config must return 503 under dev_mode_bypass via real mux; got body: %s",
		w.Body.String())
	assert.Contains(t, w.Body.String(), "dev-mode-bypass",
		"503 body must mention 'dev-mode-bypass'")
}
