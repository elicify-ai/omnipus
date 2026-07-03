package security_test

// File purpose: authorization matrix tests for state-changing REST endpoints (PR-D Axis-7).
//
// Threat model (updated — single-user model, operator directive, 2026-07):
// "Omnipus is now a single-user, self-hosted instance — there are no admin
// and normal users anymore." config.LoadConfig's load-time self-heal
// (config.normalizeAdminOnlyRoles) normalizes EVERY Gateway.Users entry to
// admin and rewrites config.json in the same pass, so no authenticated
// caller can ever reach request handling with a non-admin role — the RBAC
// scaffolding (UserRole, RequireAdmin, the "user" role literal itself)
// stays intact in the code but has NO PRACTICAL EFFECT. The matrix now
// enforces two levels of access:
//   - anonymous (no bearer token) → reject with 401
//   - any authenticated caller (valid token — the requested role is
//     irrelevant; config.LoadConfig always normalizes it to admin) →
//     allow everything an admin can do
//
// roleUser rows are DELIBERATELY KEPT (not deleted): gatewayWithRBAC still
// seeds a second account ("secuser") with a requested role="user" and its
// own real password (rbacUserPassword), and the matrix drives it through
// the identical endpoints as roleAdmin — including the requireReAuth
// consent gate on sensitive PUTs (Spec-6 FR-12.2) — to prove the
// normalization holds end-to-end over real HTTP, not just against a
// hand-asserted role literal. Any row that used to assert
// roleUser -> 403 "admin required" is deliberately flipped (not deleted) to
// assert the SAME outcome as the sibling roleAdmin row, since both accounts
// are now indistinguishable at the authorization layer.
//
// The matrix is populated from a manual reading of pkg/gateway/rest.go's
// registerAdditionalEndpoints() list (≥30 rows per task spec). The matrix
// asserts actual current behavior, not aspirational behavior — any cell that
// does not match current behavior is a REAL RBAC GAP the test will flag.
//
// Plan reference: temporal-puzzling-melody.md §6 PR-D.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// authzMatrix returns the full ≥30 row matrix using the new matrixRequest/matrixExpect
// split types (F22). The cases reflect TODAY'S behavior. Endpoints with known RBAC
// gaps are flagged with "GAP:" notes.
//
// F17: every row uses a single expect.status. The ONLY exception is wantOneOf on
// anonymous state-changing rows where the middleware order (CSRF=403 vs auth=401) is
// non-deterministic — those rows use wantOneOf and document the reason explicitly.
func authzMatrix() []matrixCase {
	return []matrixCase{
		// ---- Read surface: all three roles ----
		{
			name:   "anon_get_agents",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/agents", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
			note:   "anon must be rejected",
		},
		{
			name:   "user_get_agents",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/agents", ""},
			expect: matrixExpect{status: http.StatusOK},
			note:   "user may read agent list",
		},
		{
			name:   "admin_get_agents",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/agents", ""},
			expect: matrixExpect{status: http.StatusOK},
		},

		{
			name:   "anon_get_config",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/config", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			name:   "user_get_config",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/config", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_config",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/config", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_status",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/status", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			name:   "user_get_status",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/status", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_status",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/status", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_tasks",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/tasks", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			name:   "user_get_tasks",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/tasks", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_tasks",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/tasks", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_tools",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/tools", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			name:   "user_get_tools",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/tools", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_tools",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/tools", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_sessions",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/sessions", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			name:   "user_get_sessions",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/sessions", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_sessions",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/sessions", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_sandbox_status",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/security/sandbox-status", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			name:   "user_get_sandbox_status",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/security/sandbox-status", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_sandbox_status",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/security/sandbox-status", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_tool_policies",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/security/tool-policies", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			name:   "user_get_tool_policies",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/security/tool-policies", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_tool_policies",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/security/tool-policies", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_rate_limits",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/security/rate-limits", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			// v0.2-#155 item 7 (commit 35d10df) flipped /api/v1/security/rate-limits
			// from withAuth to adminWrap because the GET response carries the live
			// daily-cost meter and current cap config — admin-sensitive observability.
			// Single-user model (operator directive, 2026-07): secuser's requested
			// role="user" is normalized to admin by config.LoadConfig's load-time
			// self-heal before it ever reaches request handling, so the RequireAdmin
			// gate this row used to hit (403) now passes. Deliberately flipped (not
			// deleted) to prove the new outcome against the real config-loading
			// choke point rather than a hand-asserted role literal.
			name:   "user_get_rate_limits",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/security/rate-limits", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_rate_limits",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/security/rate-limits", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "anon_get_audit_log",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/audit-log", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			// Single-user model (operator directive, 2026-07): secuser's requested
			// role="user" is normalized to admin by config.LoadConfig's load-time
			// self-heal before it ever reaches request handling, so the admin-only
			// 403 this row used to assert (Issue #98) no longer occurs. Deliberately
			// flipped (not deleted) to prove the new outcome against the real
			// config-loading choke point.
			name:   "user_get_audit_log",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/audit-log", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_audit_log",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/audit-log", ""},
			expect: matrixExpect{status: http.StatusOK},
		},

		// ---- Write surface: createAgent (POST) ----
		// Anon on state-changing routes: CSRF middleware fires before auth and returns
		// 403 "csrf cookie missing" when the __Host-csrf cookie is absent (issue #97).
		// Auth middleware would return 401. The exact code depends on middleware order.
		// F17 exception: wantOneOf is documented here because the behavior is
		// middleware-order dependent — not because we are accepting any of three codes.
		{
			name: "anon_post_agents",
			req: matrixRequest{
				roleAnon, http.MethodPost, "/api/v1/agents",
				`{"name":"a1","model":"scripted-model","soul":"test-soul"}`,
			},
			wantOneOf: []int{http.StatusUnauthorized, http.StatusForbidden},
			note:      "anon on state-changing route: CSRF (403) or auth (401) is a hard deny — middleware-order dependent",
		},
		{
			// GAP: user can create agents — this is arguably admin-only but current behavior allows it.
			name: "user_post_agents",
			req: matrixRequest{
				roleUser, http.MethodPost, "/api/v1/agents",
				`{"name":"authz-user-a","model":"scripted-model","soul":"test-soul"}`,
			},
			expect: matrixExpect{status: http.StatusCreated},
			note:   "GAP: user can create agents (should be admin-only per Issue #98?)",
		},
		{
			name: "admin_post_agents",
			req: matrixRequest{
				roleAdmin, http.MethodPost, "/api/v1/agents",
				`{"name":"authz-admin-a","model":"scripted-model","soul":"test-soul"}`,
			},
			expect: matrixExpect{status: http.StatusCreated},
		},

		// ---- Write surface: sessions POST ----
		{
			name: "anon_post_sessions",
			req: matrixRequest{
				roleAnon, http.MethodPost, "/api/v1/sessions",
				`{"agent_id":"omnipus-system","type":"chat"}`,
			},
			wantOneOf: []int{http.StatusUnauthorized, http.StatusForbidden},
			note:      "anon on state-changing route: CSRF (403) or auth (401) — middleware-order dependent",
		},
		{
			// The omnipus-system agent exists (hardcoded core agent) so this should succeed.
			name: "user_post_sessions",
			req: matrixRequest{
				roleUser, http.MethodPost, "/api/v1/sessions",
				`{"agent_id":"omnipus-system","type":"chat"}`,
			},
			expect: matrixExpect{status: http.StatusCreated},
			note:   "omnipus-system is a core agent — always present",
		},
		{
			name: "admin_post_sessions",
			req: matrixRequest{
				roleAdmin, http.MethodPost, "/api/v1/sessions",
				`{"agent_id":"omnipus-system","type":"chat"}`,
			},
			expect: matrixExpect{status: http.StatusCreated},
			note:   "omnipus-system is a core agent — always present",
		},

		// ---- Config PUT (admin-only) ----
		{
			name:      "anon_put_config",
			req:       matrixRequest{roleAnon, http.MethodPut, "/api/v1/config", `{"agents":{"defaults":{}}}`},
			wantOneOf: []int{http.StatusUnauthorized, http.StatusForbidden},
			note:      "anon on state-changing route: CSRF (403) or auth (401) — middleware-order dependent",
		},
		{
			// Single-user model (operator directive, 2026-07): secuser's requested
			// role="user" is normalized to admin by config.LoadConfig's load-time
			// self-heal before it ever reaches request handling, so the admin-only
			// 403 this row used to assert (Issue #98) no longer occurs. Deliberately
			// flipped (not deleted) to prove the new outcome against the real
			// config-loading choke point.
			name:   "user_put_config",
			req:    matrixRequest{roleUser, http.MethodPut, "/api/v1/config", `{"agents":{"defaults":{}}}`},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			// Admin PUT config: the body is a valid partial config so this should succeed.
			name:   "admin_put_config",
			req:    matrixRequest{roleAdmin, http.MethodPut, "/api/v1/config", `{"agents":{"defaults":{}}}`},
			expect: matrixExpect{status: http.StatusOK},
		},

		// ---- tool-policies PUT (admin-only) ----
		{
			name: "anon_put_tool_policies",
			req: matrixRequest{
				roleAnon, http.MethodPut, "/api/v1/security/tool-policies",
				`{"tool_policies":{}}`,
			},
			wantOneOf: []int{http.StatusUnauthorized, http.StatusForbidden},
			note:      "anon on state-changing route: CSRF (403) or auth (401) — middleware-order dependent",
		},
		{
			// Single-user model (operator directive, 2026-07): secuser's requested
			// role="user" is normalized to admin by config.LoadConfig's load-time
			// self-heal, so the admin-only 403 this row used to assert (Issue #98)
			// no longer occurs at the RequireAdmin gate. This PUT is ALSO guarded
			// by the separate requireReAuth consent gate (Spec-6 FR-12.2) — the
			// test loop below mints a real re-auth token for secuser (its own
			// password, rbacUserPassword) exactly as it does for the admin row, so
			// this row proves secuser clears BOTH gates end-to-end, not just the
			// role check.
			name: "user_put_tool_policies",
			req: matrixRequest{
				roleUser, http.MethodPut, "/api/v1/security/tool-policies",
				`{"tool_policies":{}}`,
			},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name: "admin_put_tool_policies",
			req: matrixRequest{
				roleAdmin, http.MethodPut, "/api/v1/security/tool-policies",
				`{"tool_policies":{}}`,
			},
			expect: matrixExpect{status: http.StatusOK},
		},

		// ---- Credentials (admin-only) ----
		{
			name:   "anon_get_credentials",
			req:    matrixRequest{roleAnon, http.MethodGet, "/api/v1/credentials", ""},
			expect: matrixExpect{status: http.StatusUnauthorized},
		},
		{
			// Single-user model (operator directive, 2026-07): secuser's requested
			// role="user" is normalized to admin by config.LoadConfig's load-time
			// self-heal before it ever reaches request handling, so the admin-only
			// 403 this row used to assert (Issue #98) no longer occurs. Deliberately
			// flipped (not deleted) to prove the new outcome against the real
			// config-loading choke point.
			name:   "user_get_credentials",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/credentials", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
		{
			name:   "admin_get_credentials",
			req:    matrixRequest{roleAdmin, http.MethodGet, "/api/v1/credentials", ""},
			expect: matrixExpect{status: http.StatusOK},
		},
	}
}

func TestAuthorizationMatrix(t *testing.T) {
	gw, adminToken, userToken, csrfToken := gatewayWithRBAC(t)

	// Sanity check: the seeded config has an admin. mustHaveRole(t, cfg,
	// "user") used to hold here (secadmin=admin, secuser=user); it no longer
	// can. Single-user model (operator directive, 2026-07): config.LoadConfig's
	// load-time self-heal (config.normalizeAdminOnlyRoles) normalizes EVERY
	// Gateway.Users entry to admin and rewrites config.json in the same pass,
	// so there is no longer a way for a Gateway.Users entry to retain a
	// non-admin role after a reload. gatewayWithRBAC still seeds "secuser"
	// with a REQUESTED role="user" specifically so this check can prove — at
	// the real config-loading choke point, not a hand-asserted literal — that
	// the requested role never survives to disk.
	cfg := findTestConfig(t, gw.ConfigPath())
	mustHaveRole(t, cfg, config.UserRoleAdmin)
	var secuserFound bool
	for _, u := range cfg.Gateway.Users {
		if u.Username != "secuser" {
			continue
		}
		secuserFound = true
		require.Equal(t, config.UserRoleAdmin, u.Role,
			"single-user model: secuser was seeded with role=\"user\" but "+
				"config.LoadConfig must normalize it to admin on reload")
	}
	require.True(t, secuserFound, "secuser must be present in Gateway.Users after SeedUser")

	matrix := authzMatrix()
	require.GreaterOrEqual(t, len(matrix), 30,
		"matrix must have at least 30 rows per task spec (got %d)", len(matrix))

	for i, tc := range matrix {
		name := matrixCaseName(i, tc)
		t.Run(name, func(t *testing.T) {
			var token string
			switch tc.req.role {
			case roleAnon:
				token = ""
			case roleUser:
				token = userToken
			case roleAdmin:
				token = adminToken
			}

			var reqBody io.Reader
			if tc.req.body != "" {
				reqBody = bytes.NewReader([]byte(tc.req.body))
			}
			req, err := http.NewRequest(tc.req.method, gw.URL+tc.req.path, reqBody)
			if err != nil {
				t.Fatalf("build req: %v", err)
			}
			req.Header.Set("Origin", gw.URL)
			if tc.req.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
				// Authenticated callers attach the CSRF cookie + header on
				// state-changing methods so the CSRF middleware does not
				// short-circuit the request before auth runs (issue #97).
				// Anon rows deliberately omit both to exercise the CSRF gate.
				switch tc.req.method {
				case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
					req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: csrfToken})
					req.Header.Set("X-Csrf-Token", csrfToken)
				}
				// Re-auth gate (Spec-6 FR-12.2): several sensitive admin PUT routes
				// (e.g. /security/tool-policies) require a single-use consent token
				// AFTER the admin-role check (RequireAdmin → requireReAuth). Anon
				// rows fail earlier (CSRF/401), so only authenticated rows reach —
				// and must clear — the re-auth gate. Single-user model (operator
				// directive, 2026-07): secuser's requested role="user" is normalized
				// to admin the same as the onboarded admin
				// (config.normalizeAdminOnlyRoles), so it reaches this gate too and
				// needs its own consent token, minted with its own real password
				// (rbacUserPassword) via the same POST /api/v1/auth/reauth round
				// trip the admin row uses.
				if isReAuthGatedPUT(tc.req.method, tc.req.path) {
					reauthPassword := rbacAdminPassword
					if tc.req.role == roleUser {
						reauthPassword = rbacUserPassword
					}
					rt := mintReAuthToken(t, gw.HTTPClient, gw.URL, gw.URL, token, reauthPassword)
					req.Header.Set(reAuthHeader, rt)
				}
			}
			resp, err := gw.HTTPClient.Do(req)
			if err != nil {
				t.Fatalf("do req: %v", err)
			}
			defer resp.Body.Close()

			raw, _ := io.ReadAll(resp.Body)
			rawStr := string(raw)

			if tc.note != "" {
				t.Logf("note: %s (role=%s %s %s -> %d)",
					tc.note, tc.req.role, tc.req.method, tc.req.path, resp.StatusCode)
			}

			// Assert status — either exact (F17) or documented wantOneOf.
			if len(tc.wantOneOf) > 0 {
				// Middleware-order-dependent rows (anon + state-changing).
				// These are the ONLY rows allowed to have multiple acceptable codes.
				// Every other row must have a single expect.status (F17).
				ok := false
				for _, want := range tc.wantOneOf {
					if resp.StatusCode == want {
						ok = true
						break
					}
				}
				if !ok {
					note := tc.note
					if note == "" {
						note = "(no note)"
					}
					t.Fatalf(
						"role=%s %s %s: got status %d, want one of %v "+
							"(middleware-order dependent). Body: %s. Note: %s",
						tc.req.role, tc.req.method, tc.req.path, resp.StatusCode,
						tc.wantOneOf, truncate(rawStr, 200), note,
					)
				}
			} else {
				// Single exact status (the normal path for all non-ambiguous rows).
				require.Equal(t, tc.expect.status, resp.StatusCode,
					"role=%s %s %s: unexpected status. Body: %s. Note: %s",
					tc.req.role, tc.req.method, tc.req.path,
					truncate(rawStr, 200), tc.note)
			}

			// Body substring assertion for admin-enforced 403 responses (Issue #98).
			if tc.expect.bodyContains != "" {
				assert.Contains(t, rawStr, tc.expect.bodyContains,
					"role=%s %s %s: response body must contain %q",
					tc.req.role, tc.req.method, tc.req.path, tc.expect.bodyContains)
			}
			assert.Less(t, resp.StatusCode, 500,
				"server must not 5xx for any matrix row (role=%s %s %s)",
				tc.req.role, tc.req.method, tc.req.path)
		})
	}
}

// findTestConfig reads the on-disk config.json and parses enough of it to let
// mustHaveRole() verify that both an admin and a user role are present.
func findTestConfig(t *testing.T, path string) *config.Config {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read config at %s", path)

	// The JSON on disk uses a nested "gateway.users" array; the Config struct
	// deserializes it directly.
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}
	return &cfg
}

// matrixCaseName produces a readable t.Run name from a matrixCase.
// Uses tc.name if set (preferred — explicitly named in F22 refactor),
// falling back to derived name for backward compat.
func matrixCaseName(i int, tc matrixCase) string {
	if tc.name != "" {
		return tc.name
	}
	path := strings.NewReplacer("/", "_").Replace(strings.TrimPrefix(tc.req.path, "/api/v1/"))
	return string(tc.req.role) + "_" + strings.ToLower(tc.req.method) + "_" + path + "_" + itoa3(i)
}
