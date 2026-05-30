package security_test

// File purpose: authorization matrix tests for state-changing REST endpoints (PR-D Axis-7).
//
// Threat model: the REST API must enforce three levels of access:
//   - anonymous (no bearer token) → reject with 401
//   - user (valid token, UserRoleUser) → allow only read surface + own resources
//   - admin (valid token, UserRoleAdmin) → allow everything
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
			// User role now correctly returns 403 admin-required.
			name:   "user_get_rate_limits",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/security/rate-limits", ""},
			expect: matrixExpect{status: http.StatusForbidden},
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
			name:   "user_get_audit_log",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/audit-log", ""},
			expect: matrixExpect{status: http.StatusForbidden, bodyContains: "admin required"},
			note:   "admin-only: user must receive 403 (Issue #98)",
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
				`{"name":"a1","model":"scripted-model"}`,
			},
			wantOneOf: []int{http.StatusUnauthorized, http.StatusForbidden},
			note:      "anon on state-changing route: CSRF (403) or auth (401) is a hard deny — middleware-order dependent",
		},
		{
			// GAP: user can create agents — this is arguably admin-only but current behavior allows it.
			name: "user_post_agents",
			req: matrixRequest{
				roleUser, http.MethodPost, "/api/v1/agents",
				`{"name":"authz-user-a","model":"scripted-model"}`,
			},
			expect: matrixExpect{status: http.StatusCreated},
			note:   "GAP: user can create agents (should be admin-only per Issue #98?)",
		},
		{
			name: "admin_post_agents",
			req: matrixRequest{
				roleAdmin, http.MethodPost, "/api/v1/agents",
				`{"name":"authz-admin-a","model":"scripted-model"}`,
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
			name:   "user_put_config",
			req:    matrixRequest{roleUser, http.MethodPut, "/api/v1/config", `{"agents":{"defaults":{}}}`},
			expect: matrixExpect{status: http.StatusForbidden, bodyContains: "admin required"},
			note:   "admin-only: user must be rejected with 403 (Issue #98)",
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
			name: "user_put_tool_policies",
			req: matrixRequest{
				roleUser, http.MethodPut, "/api/v1/security/tool-policies",
				`{"tool_policies":{}}`,
			},
			expect: matrixExpect{status: http.StatusForbidden, bodyContains: "admin required"},
			note:   "admin-only: user must be rejected with 403 (Issue #98)",
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
			name:   "user_get_credentials",
			req:    matrixRequest{roleUser, http.MethodGet, "/api/v1/credentials", ""},
			expect: matrixExpect{status: http.StatusForbidden, bodyContains: "admin required"},
			note:   "admin-only: user must be rejected with 403 (Issue #98)",
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

	// Sanity check: the seeded config has both roles.
	cfg := findTestConfig(t, gw.ConfigPath())
	mustHaveRole(t, cfg, "admin")
	mustHaveRole(t, cfg, "user")

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
