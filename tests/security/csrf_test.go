package security_test

// File purpose: CSRF protection tests for state-changing POST endpoints
// (PR-D Axis-7, completed as PR-H #97).
//
// Hard-asserts the CSRF double-submit cookie gate:
//
//   - Every state-changing request (POST/PUT/PATCH/DELETE) on a non-exempt
//     /api/v1/* path MUST carry both:
//     1. the __Host-csrf cookie
//     2. an X-CSRF-Token header with the same value
//
//   - Missing cookie → 403 "csrf cookie missing"
//   - Missing header → 403 "csrf header missing"
//   - Mismatched cookie/header → 403 "csrf token mismatch"
//   - Matching pair → request reaches the handler (2xx/4xx per handler logic)
//
// A cross-origin JavaScript attacker cannot read the cookie (same-origin
// policy applies to cookies bound to our host), so they cannot forge a
// valid header. The hostile Origin leg of the test is preserved to catch
// a regression in the CORS reflection gate that would let a browser READ
// the 403 body (which would be a secondary leak, not a CSRF bypass).
//
// History:
//   - Original PR-D version documented the gap and asserted only that the
//     server did not 5xx.
//   - PR-H (#97) landed the double-submit middleware and flipped these
//     assertions to hard rejections + hard acceptance on the happy path.
//
// Plan reference: temporal-puzzling-melody.md §1 (CSRF/Origin decision).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// csrfTarget is a state-changing endpoint to probe.
type csrfTarget struct {
	method string
	path   string
	// body is sent on each request; empty body means no Content-Type and no payload.
	body string
	// expectAuthedOK is the status code (or code range) returned when the
	// request is sent with a valid bearer token AND a matching CSRF
	// cookie+header pair. The handler may still 4xx for semantic reasons
	// (e.g., agent_id not found), so tests accept a range.
	expectAuthedOK []int
	// Whether an empty body with no Content-Type is acceptable (for endpoints
	// that only look at headers).
	needsJSONBody bool
}

func csrfTargets() []csrfTarget {
	return []csrfTarget{
		{
			// /api/v1/agents exercises the generic state-changing route — it
			// is NOT in the CSRF exempt list, so it is the canonical probe.
			method: http.MethodPost,
			path:   "/api/v1/agents",
			// `type` is required since the ADR-034 discriminated-union create
			// contract — without it the request 400s before the CSRF layer's
			// success-path assertion can be exercised.
			body:           `{"type":"Main","name":"csrf-test-agent","model":"scripted-model","soul":"test-soul"}`,
			needsJSONBody:  true,
			expectAuthedOK: []int{http.StatusOK, http.StatusCreated},
		},
		{
			method:         http.MethodPut,
			path:           "/api/v1/security/tool-policies",
			body:           `{"tool_policies":{}}`,
			needsJSONBody:  true,
			expectAuthedOK: []int{http.StatusOK, http.StatusBadRequest},
		},
		{
			method:         http.MethodPost,
			path:           "/api/v1/sessions",
			body:           `{"agent_id":"omnipus-system","type":"chat"}`,
			needsJSONBody:  true,
			expectAuthedOK: []int{http.StatusOK, http.StatusCreated, http.StatusBadRequest},
		},
		{
			// /api/v1/config accepts PUT for updates.
			method:         http.MethodPut,
			path:           "/api/v1/config",
			body:           `{"agents":{"defaults":{}}}`,
			needsJSONBody:  true,
			expectAuthedOK: []int{http.StatusOK, http.StatusBadRequest},
		},
	}
}

// TestCSRFProtection hard-asserts the double-submit cookie gate on every
// state-changing endpoint in csrfTargets. The three sub-tests match the
// failure modes of pkg/gateway/middleware/csrf.go.
//
// Authorization: Bearer callers are EXEMPT from the CSRF gate — browsers
// cannot auto-send the Authorization header cross-origin, so Bearer traffic
// is not a CSRF target. These tests therefore omit the Bearer token from the
// attack-scenario sub-tests to exercise the cookie-auth code path, which is
// what the CSRF gate actually guards.
func TestCSRFProtection(t *testing.T) {
	// Deliberately NOT testutil.WithBearerAuth(): this test seeds its admin by
	// driving the real onboarding wizard below, and POST /onboarding/complete
	// now refuses on any instance that already has an authentication authority
	// — a configured user OR OMNIPUS_BEARER_TOKEN — whatever the onboarding
	// flag says (see onboardingWindowGate, pkg/gateway/rest_onboarding.go).
	// WithBearerAuth sets that env token, so onboarding here would 409.
	//
	// Do NOT "fix" a future 409 by re-adding WithBearerAuth or by clearing the
	// env var around the onboarding call: both would paper over the control
	// that stops an anonymous caller minting an admin on an authenticated
	// instance. Starting fresh is also the honest production sequence — a real
	// deployment onboards first and sets OMNIPUS_BEARER_TOKEN afterwards.
	//
	// Nothing this test asserts is weakened by the change. Pre-onboarding the
	// harness sets dev_mode_bypass (no users, no env token), but the instant
	// onboarding seeds gateway.users, checkBearerAuth requires a token that
	// matches a user — so auth is genuinely enforced for every sub-test that
	// depends on it. Verified by mutation: replacing adminToken with a bogus
	// value makes every happy-path sub-test fail with 401 "invalid Bearer
	// token". The two attack sub-tests send no Authorization header at all and
	// are answered by the CSRF middleware, which runs BEFORE auth.
	gw := testutil.StartTestGateway(t)

	// Use a fixed CSRF token for the happy-path test. The middleware only
	// compares cookie to header; any value works so long as both legs match.
	const csrfToken = "csrf-test-fixture-value-42"

	// Onboard a real password-backed admin so the happy-path sub-test can clear
	// the re-auth gate on sensitive PUTs (e.g. /security/tool-policies, Spec-6
	// FR-12.2). The synthetic env-token admin (WithBearerAuth) has no password
	// hash and so cannot mint a consent token via POST /api/v1/auth/reauth.
	// Onboarding seeds gateway.users, which disables the legacy env-token bearer
	// (gw.Token()) — checkBearerAuth then requires a token in the user list — so
	// the happy path authenticates with this onboarded admin token instead.
	// The two ATTACK sub-tests below send NO bearer (modeling cross-origin CSRF),
	// so they are unaffected by the user-list seeding.
	const adminPassword = "securepass123"
	adminToken := onboardCSRFAdmin(t, gw, adminPassword)

	for _, tgt := range csrfTargets() {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(tgt.path)

		// ------------------------------------------------------------
		// 1. Cross-origin + NO X-CSRF-Token header + NO Bearer →
		//    hard-expect 403 (the realistic browser-CSRF attacker scenario).
		// ------------------------------------------------------------
		t.Run(name+"_no_csrf_header_rejected", func(t *testing.T) {
			body := bytes.NewReader([]byte(tgt.body))
			req, err := http.NewRequest(tgt.method, gw.URL+tgt.path, body)
			require.NoError(t, err)
			req.Header.Set("Origin", "https://evil.example.com")
			if tgt.needsJSONBody {
				req.Header.Set("Content-Type", "application/json")
			}
			// Intentionally omit Authorization header. In a real CSRF attack
			// the victim's browser cannot auto-send Authorization cross-origin,
			// only auto-send cookies — that's the scenario we're modeling.
			// Also deliberately omit the X-CSRF-Token header AND the cookie.
			resp, err := gw.HTTPClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// Hard expectation: 403 Forbidden from the CSRF gate. Without
			// Bearer auth the request would 401 at the auth layer — but the
			// CSRF middleware runs BEFORE auth in the middleware chain, so a
			// state-changing request with no cookie short-circuits to 403.
			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"CSRF gate must reject %s %s with no cookie/header as 403",
				tgt.method, tgt.path)

			// Error body must mention CSRF (so the error is actionable).
			raw, _ := io.ReadAll(resp.Body)
			var body403 map[string]string
			require.NoError(t, json.Unmarshal(raw, &body403),
				"403 body must be JSON — got %q", string(raw))
			assert.Contains(t, strings.ToLower(body403["error"]), "csrf",
				"403 body must name the CSRF gate so callers can diagnose: %q", body403["error"])
		})

		// ------------------------------------------------------------
		// 2. Wrong cookie/header pair → hard-expect 403.
		// ------------------------------------------------------------
		t.Run(name+"_wrong_csrf_token_rejected", func(t *testing.T) {
			body := bytes.NewReader([]byte(tgt.body))
			req, err := http.NewRequest(tgt.method, gw.URL+tgt.path, body)
			require.NoError(t, err)
			req.Header.Set("Origin", "https://evil.example.com")
			if tgt.needsJSONBody {
				req.Header.Set("Content-Type", "application/json")
			}
			// Same as above — no Bearer; the attack is cross-origin browser CSRF.
			// Cookie and header disagree — the classic "forged header"
			// attack shape. Attacker can set arbitrary headers but cannot
			// write __Host- cookies from a different origin.
			req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: "server-seeded-value"})
			req.Header.Set("X-Csrf-Token", "attacker-guess")
			resp, err := gw.HTTPClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"CSRF gate must reject mismatched cookie/header as 403")

			raw, _ := io.ReadAll(resp.Body)
			var body403 map[string]string
			require.NoError(t, json.Unmarshal(raw, &body403))
			// Specifically "mismatch" so we know we reached the compare step,
			// not the cookie-missing short-circuit.
			assert.Equal(t, "csrf token mismatch", body403["error"],
				"403 body must read 'csrf token mismatch' on a forged-header attempt")
		})

		// ------------------------------------------------------------
		// 3. Correct cookie + matching header → request reaches handler.
		// ------------------------------------------------------------
		t.Run(name+"_correct_csrf_token_passes", func(t *testing.T) {
			body := bytes.NewReader([]byte(tgt.body))
			req, err := http.NewRequest(tgt.method, gw.URL+tgt.path, body)
			require.NoError(t, err)
			req.Header.Set("Origin", gw.URL)
			if tgt.needsJSONBody {
				req.Header.Set("Content-Type", "application/json")
			}
			// Authenticate with the onboarded admin (not gw.Token() — the env
			// token is dead once gateway.users is seeded by onboarding).
			req.Header.Set("Authorization", "Bearer "+adminToken)
			req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: csrfToken})
			req.Header.Set("X-Csrf-Token", csrfToken)
			// Re-auth gate (Spec-6 FR-12.2): sensitive PUTs (e.g. tool-policies)
			// also require a single-use consent token AFTER the CSRF + admin
			// checks. Mint one so the request reaches the handler — what this
			// sub-test actually asserts is that the CSRF compare path lets a
			// matching cookie+header through (NotEqual 403), not that re-auth is
			// absent. Without the token the handler's re-auth gate would 403 and
			// mask a genuine CSRF regression.
			if isReAuthGatedPUT(tgt.method, tgt.path) {
				rt := mintReAuthToken(t, gw.HTTPClient, gw.URL, gw.URL, adminToken, adminPassword)
				req.Header.Set(reAuthHeader, rt)
			}
			resp, err := gw.HTTPClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// The request must NOT be rejected at the CSRF gate. The handler
			// itself may still 4xx (missing field, agent not found, etc.);
			// the test accepts the per-target whitelist.
			require.NotEqual(t, http.StatusForbidden, resp.StatusCode,
				"CSRF gate must let through matching cookie+header "+
					"(%s %s returned 403 — regression in the compare path)",
				tgt.method, tgt.path)

			accepted := false
			for _, want := range tgt.expectAuthedOK {
				if resp.StatusCode == want {
					accepted = true
					break
				}
			}
			if !accepted {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("handler returned unexpected status %d (want one of %v) for %s %s. Body: %s",
					resp.StatusCode, tgt.expectAuthedOK, tgt.method, tgt.path, truncate(string(raw), 200))
			}
		})
	}
}

// TestCSRFCORSReflectionGate verifies the CORS reflection gate, which is
// the second layer of CSRF defense. Even if the primary cookie+header
// check missed something, a browser JS attacker can only READ the response
// body if the server reflects their Origin in Access-Control-Allow-Origin.
// This test ensures hostile origins are never reflected.
func TestCSRFCORSReflectionGate(t *testing.T) {
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	hostileOrigins := []string{
		"https://evil.example.com",
		"http://attacker.localhost",
		"null",
		"data:,",
		"file://",
	}
	for _, origin := range hostileOrigins {
		t.Run("hostile_origin_"+sanitizeName(origin), func(t *testing.T) {
			// Use an auth-required GET endpoint — simplest way to exercise
			// CORS reflection without touching state.
			req, err := http.NewRequest(http.MethodGet, gw.URL+"/api/v1/agents", nil)
			require.NoError(t, err)
			req.Header.Set("Origin", origin)
			if gw.Token() != "" {
				req.Header.Set("Authorization", "Bearer "+gw.Token())
			}
			resp, err := gw.HTTPClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			acao := resp.Header.Get("Access-Control-Allow-Origin")
			assert.NotEqual(t, origin, acao,
				"CORS must NOT reflect hostile origin %q (got ACAO=%q) — this "+
					"would let browser JS read the response body",
				origin, acao)
			// It is acceptable for ACAO to be empty or to be gw.URL.
			if acao != "" && acao != gw.URL {
				t.Fatalf("CORS reflected unexpected origin %q (req.Origin=%q) — "+
					"expected empty or %q",
					acao, origin, gw.URL)
			}
		})
	}

	// Positive control: same-origin requests should get ACAO reflected.
	t.Run("same_origin_reflected", func(t *testing.T) {
		req, err := gw.NewRequest(http.MethodGet, "/api/v1/agents", nil)
		require.NoError(t, err)
		resp, err := gw.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		acao := resp.Header.Get("Access-Control-Allow-Origin")
		assert.Equal(t, gw.URL, acao,
			"same-origin requests MUST get the gateway URL echoed in ACAO")
	})
}

// sanitizeName produces a t.Run-safe name from an arbitrary string.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 32 {
			break
		}
	}
	if b.Len() == 0 {
		return "empty"
	}
	return b.String()
}
