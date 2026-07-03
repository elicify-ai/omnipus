package security_test

// File purpose: unauthenticated-access rejection test (PR-D Axis-7,
// single-user model).
//
// Threat model (single-user model, operator directive, 2026-07): Omnipus is
// now a single-user, self-hosted instance — there is no admin/user role
// axis left to test, because the role/RBAC scaffolding (UserRole,
// RequireAdmin, the seeded second account) has been deleted entirely. The
// only access-control axis that still exists is authenticated vs. not.
//
// This file replaces authz_matrix_test.go, whose entire premise — driving a
// real HTTP gateway with two seeded accounts and asserting they get
// identical treatment across ~15 endpoints — proved the old "neutralize but
// keep role scaffolding" pass. Once the scaffolding itself is gone, that
// two-account comparison is structurally impossible to write, not merely
// redundant.
//
// TestUnauthenticatedAccessRejected boots ONE gateway (single seeded
// account, matching production reality) and asserts that a caller with no
// bearer token and no session cookie is rejected — 401 on read-only GETs;
// 401 or 403 on state-changing POST/PUT, since the CSRF double-submit
// middleware (issue #97) runs before the auth check on state-changing
// methods and can short-circuit an anonymous request with 403 "csrf cookie
// missing" before it ever reaches checkBearerAuth. Either outcome is a hard
// deny. The endpoint/method list below is carried forward verbatim from the
// anon_* rows of the deleted authz_matrix_test.go.
//
// Plan reference: temporal-puzzling-melody.md §6 PR-D.

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// unauthCase is one row: an unauthenticated request to a protected endpoint,
// and the set of HTTP statuses that count as "rejected". This test has
// exactly one axis (authenticated vs. not) — there is no role field.
type unauthCase struct {
	name   string
	method string
	path   string
	// body is sent with Content-Type: application/json when non-empty.
	body string
	// want lists the HTTP statuses that count as a correct rejection. GETs
	// hard-expect exactly 401. State-changing POST/PUT rows accept 401 OR
	// 403 because of the CSRF-vs-auth middleware-order ambiguity described
	// above.
	want []int
}

// unauthCases carries forward the exact anon_* endpoint/method list from the
// deleted authz_matrix_test.go.
func unauthCases() []unauthCase {
	return []unauthCase{
		// ---- Read surface ----
		{name: "get_agents", method: http.MethodGet, path: "/api/v1/agents", want: []int{http.StatusUnauthorized}},
		{name: "get_config", method: http.MethodGet, path: "/api/v1/config", want: []int{http.StatusUnauthorized}},
		{name: "get_status", method: http.MethodGet, path: "/api/v1/status", want: []int{http.StatusUnauthorized}},
		{name: "get_tasks", method: http.MethodGet, path: "/api/v1/tasks", want: []int{http.StatusUnauthorized}},
		{name: "get_tools", method: http.MethodGet, path: "/api/v1/tools", want: []int{http.StatusUnauthorized}},
		{name: "get_sessions", method: http.MethodGet, path: "/api/v1/sessions", want: []int{http.StatusUnauthorized}},
		{
			name:   "get_sandbox_status",
			method: http.MethodGet,
			path:   "/api/v1/security/sandbox-status",
			want:   []int{http.StatusUnauthorized},
		},
		{
			name:   "get_tool_policies",
			method: http.MethodGet,
			path:   "/api/v1/security/tool-policies",
			want:   []int{http.StatusUnauthorized},
		},
		{
			name:   "get_rate_limits",
			method: http.MethodGet,
			path:   "/api/v1/security/rate-limits",
			want:   []int{http.StatusUnauthorized},
		},
		{name: "get_audit_log", method: http.MethodGet, path: "/api/v1/audit-log", want: []int{http.StatusUnauthorized}},
		{
			name:   "get_credentials",
			method: http.MethodGet,
			path:   "/api/v1/credentials",
			want:   []int{http.StatusUnauthorized},
		},

		// ---- Write surface: anon on state-changing routes. CSRF
		// middleware fires before auth and returns 403 "csrf cookie
		// missing" when the __Host-csrf cookie is absent (issue #97); auth
		// middleware would return 401. The exact code depends on
		// middleware order — either is a hard deny.
		{
			name:   "post_agents",
			method: http.MethodPost,
			path:   "/api/v1/agents",
			body:   `{"name":"a1","model":"scripted-model","soul":"test-soul"}`,
			want:   []int{http.StatusUnauthorized, http.StatusForbidden},
		},
		{
			name:   "post_sessions",
			method: http.MethodPost,
			path:   "/api/v1/sessions",
			body:   `{"agent_id":"omnipus-system","type":"chat"}`,
			want:   []int{http.StatusUnauthorized, http.StatusForbidden},
		},
		{
			name:   "put_config",
			method: http.MethodPut,
			path:   "/api/v1/config",
			body:   `{"agents":{"defaults":{}}}`,
			want:   []int{http.StatusUnauthorized, http.StatusForbidden},
		},
		{
			name:   "put_tool_policies",
			method: http.MethodPut,
			path:   "/api/v1/security/tool-policies",
			body:   `{"tool_policies":{}}`,
			want:   []int{http.StatusUnauthorized, http.StatusForbidden},
		},
	}
}

// TestUnauthenticatedAccessRejected boots a single gateway (one seeded
// account — the only account topology that exists now that the RBAC
// scaffolding is deleted) and asserts that a caller presenting no bearer
// token and no session cookie is rejected on every endpoint in
// unauthCases.
func TestUnauthenticatedAccessRejected(t *testing.T) {
	gw := testutil.StartTestGateway(t,
		testutil.WithBearerAuth(),
		testutil.WithAgents([]config.AgentConfig{
			{ID: "omnipus-system", Type: config.AgentTypeSystem, Description: "system agent"},
		}),
	)

	cases := unauthCases()
	require.Len(t, cases, 15, "endpoint list must match the 15 anon_* rows carried forward from authz_matrix_test.go")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reqBody io.Reader
			if tc.body != "" {
				reqBody = bytes.NewReader([]byte(tc.body))
			}
			req, err := http.NewRequest(tc.method, gw.URL+tc.path, reqBody)
			require.NoError(t, err, "build request")
			req.Header.Set("Origin", gw.URL)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			// Deliberately no Authorization header and no CSRF cookie/header
			// — this is the unauthenticated caller under test.

			resp, err := gw.HTTPClient.Do(req)
			require.NoError(t, err, "do request")
			defer resp.Body.Close()

			raw, _ := io.ReadAll(resp.Body)

			ok := false
			for _, want := range tc.want {
				if resp.StatusCode == want {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("%s %s: got status %d, want one of %v. Body: %s",
					tc.method, tc.path, resp.StatusCode, tc.want, string(raw))
			}
		})
	}
}
