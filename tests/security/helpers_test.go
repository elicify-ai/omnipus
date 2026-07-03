package security_test

// File purpose: shared helpers for PR-D security tests.
//
// These helpers sit alongside (and deliberately do NOT modify) D1's
// ssrf_matrix_test.go / sandbox_enforcement tests and D3's workflow +
// security_payloads. They are local to the security_test package so that if
// D3 later lifts them into pkg/testutil we can delete this file.
//
// matrixRequest / matrixExpect / matrixCase types live here (F22 — split from
// authz_matrix_test.go to separate input shape from assertion shape, reducing
// the ambiguity that came from the single 7-field struct that conflated both).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// authRole enumerates the three principal classes we test.
// Moved to helpers_test.go so it is available to both authz_matrix_test.go
// and any future matrix-style tests (F22 refactor).
type authRole string

const (
	roleAnon  authRole = "anonymous"
	roleUser  authRole = "user"
	roleAdmin authRole = "admin"
)

// rbacAdminPassword is the plaintext password gatewayWithRBAC onboards the
// admin user (secadmin) with. Exposed as a const so tests that must clear the
// re-auth gate on a sensitive admin PUT can mint a consent token via
// mintReAuthToken (POST /api/v1/auth/reauth re-verifies this password).
const rbacAdminPassword = "securepass123"

// rbacUserPassword is the plaintext password gatewayWithRBAC sets on the
// non-admin account (secuser). Single-user model (operator directive,
// 2026-07): config.LoadConfig's load-time self-heal
// (config.normalizeAdminOnlyRoles) normalizes EVERY Gateway.Users entry to
// admin, so secuser reaches every RequireAdmin gate exactly like the
// onboarded admin — including the sensitive PUTs guarded by the separate
// requireReAuth consent gate (Spec-6 FR-12.2). A real password lets
// authz_matrix_test.go mint a genuine re-auth token for secuser via POST
// /api/v1/auth/reauth, the same round trip rbacAdminPassword drives for the
// admin account, so the matrix can prove secuser completes a re-auth-gated
// PUT end-to-end rather than stalling at an orthogonal gate.
const rbacUserPassword = "userpass456"

// matrixRequest describes the HTTP request to issue (who sends it, what to send).
// F22: split from the monolithic matrixCase struct to separate request from assertion.
type matrixRequest struct {
	role   authRole
	method string
	path   string
	// body is sent with Content-Type: application/json when non-empty.
	body string
}

// matrixExpect describes the exact assertion to make.
// F17: each row uses a single status, not a slice. The only exception is
// wantOneOf on matrixCase for middleware-order-dependent anon rows (documented).
type matrixExpect struct {
	// status is the EXACT expected HTTP status code.
	status int
	// bodyContains, if non-empty, is asserted as a substring of the response body.
	bodyContains string
}

// matrixCase is one row in the authorization matrix.
// F22: the old 7-field struct has been split into req (input) + expect (assertion).
type matrixCase struct {
	name   string
	req    matrixRequest
	expect matrixExpect
	// note is logged on every run for documentation / debugging.
	note string
	// wantOneOf overrides expect.status when the precise code is middleware-order
	// dependent (CSRF=403 vs auth=401 for anonymous state-changing requests).
	// Only use this when there is a documented reason. Do NOT use it to paper
	// over missing assertions — every non-anon row must have a single expect.status.
	wantOneOf []int
}

// gatewayWithAdmin boots a gateway with DevModeBypass OFF and a single admin
// user wired into config.Gateway.Users. Returns the gateway plus the admin's
// plaintext bearer token. Unlike the bare StartTestGateway(WithBearerAuth())
// path (which relies on OMNIPUS_BEARER_TOKEN fallback = admin), this exercises
// the RBAC code path — a role is attached to the user, which matters for
// authz_matrix_test.go.
//
// Returns the gateway, admin plaintext token, a seeded "user" role plaintext
// token for the non-admin account, and the CSRF cookie value issued by the
// onboarding response. The CSRF value is the same for both user and admin
// (it's just the onboarding-seeded cookie — the bearer token is what
// distinguishes identity at the auth layer). Callers wanting to exercise the
// CSRF gate on state-changing requests must set both:
//
//	Cookie: __Host-csrf=<csrfToken>
//	X-CSRF-Token: <csrfToken>
//
// Tests that deliberately probe CSRF (e.g., csrf_test.go) set these manually;
// the authz matrix test sets them for every authenticated POST/PUT/DELETE.
//
// Single-user model (operator directive, 2026-07): the second ("secuser")
// account is still seeded with a REQUESTED role of "user", but
// config.LoadConfig's load-time self-heal (config.normalizeAdminOnlyRoles)
// normalizes it to admin on every reload — there is no longer a way for a
// Gateway.Users entry to retain a non-admin role. secuser also gets a real
// password (rbacUserPassword, not just the returned bearer token) so callers
// can drive it through the same requireReAuth consent round trip
// (POST /api/v1/auth/reauth) the admin account uses.
func gatewayWithRBAC(t *testing.T) (gw *testutil.TestGateway, adminToken, userToken, csrfToken string) {
	t.Helper()

	// Pre-seed the config with two users BEFORE the gateway boots so the
	// legacy OMNIPUS_BEARER_TOKEN env-var fallback path is never taken.
	adminPlain := "admin-token-" + randSuffix()
	userPlain := "user-token-" + randSuffix()

	adminHash, err := bcrypt.GenerateFromPassword([]byte(adminPlain), bcrypt.MinCost)
	require.NoError(t, err)
	userHash, err := bcrypt.GenerateFromPassword([]byte(userPlain), bcrypt.MinCost)
	require.NoError(t, err)
	// secuser also gets a real password hash (not just a bearer token) so the
	// single-user-model matrix rows can mint a genuine re-auth consent token
	// for it via POST /api/v1/auth/reauth, mirroring the admin round trip.
	userPasswordHash, err := bcrypt.GenerateFromPassword([]byte(rbacUserPassword), bcrypt.MinCost)
	require.NoError(t, err)

	// We cannot inject users via a testutil Option, so seed them by writing
	// a full config.json before boot, then start the gateway pointed at it.
	// The simplest route: call StartTestGateway first, immediately stop it,
	// rewrite config.json with users, then start a second gateway. But that
	// re-binds ports. Instead we leverage the fact that the harness lets
	// callers pre-create the temp dir by passing nothing special — we piggy-back
	// by booting first with bearerAuth=false, then onboarding via REST.
	//
	// Strategy: boot with DevModeBypass=true (no auth), POST /onboarding/complete
	// to register the admin, then write the second (non-admin) user directly
	// into config.json via the config endpoint using the admin token.
	// Seed the system agent so POST /sessions with agent_id=omnipus-system
	// resolves (otherwise the handler returns 400 "agent not found", masking
	// whether RBAC / CSRF fired as intended).
	gw = testutil.StartTestGateway(t, testutil.WithAgents([]config.AgentConfig{
		{ID: "omnipus-system", Type: config.AgentTypeSystem, Description: "system agent"},
	}))

	// Onboard to create the admin user with a role.
	body := map[string]any{
		"provider": map[string]any{
			"id":      "openai",
			"api_key": "sk-test-security-matrix-" + randSuffix(),
			"model":   "gpt-4o",
		},
		"admin": map[string]any{
			"username": "secadmin",
			"password": rbacAdminPassword,
		},
	}
	buf, _ := json.Marshal(body)
	req, err := gw.NewRequest(http.MethodPost, "/api/v1/onboarding/complete",
		bytes.NewReader(buf))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"onboarding must succeed to seed admin user")
	var onboardResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&onboardResp))
	require.NotEmpty(t, onboardResp.Token)
	adminToken = onboardResp.Token

	// Capture whichever CSRF cookie the onboarding handler issued. Bug 3
	// made the cookie flavor request-aware: __Host-csrf when the request
	// arrived over TLS (Secure:true, __Host- prefix), otherwise the
	// un-prefixed `csrf` fallback (Secure:false) so the browser will
	// accept it on plain HTTP. httptest.NewServer binds plain HTTP so the
	// fallback is what the handler emits in this harness.
	for _, c := range resp.Cookies() {
		if c.Name == "__Host-csrf" || c.Name == "csrf" {
			csrfToken = c.Value
			break
		}
	}
	require.NotEmpty(t, csrfToken,
		"onboarding response must set either __Host-csrf (TLS) or csrf (plain HTTP) cookie")

	// Add a second (non-admin) user via gw.SeedUser — this writes the user via
	// the same read-modify-write + /reload path the gateway uses, eliminating
	// the hand-rolled config-rewrite dance.
	// beforeWrite disables DevModeBypass so that RequireNotBypass does not block
	// admin security endpoints during the actual test run.
	_ = adminHash // already have usable adminToken from onboarding response
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer seedCancel()
	require.NoError(t,
		gw.SeedUser(seedCtx, config.UserConfig{
			Username:     "secuser",
			PasswordHash: string(userPasswordHash),
			TokenHash:    config.BcryptHash(userHash),
			Role:         config.UserRoleUser,
		}, func(m map[string]any) {
			gwSec := m["gateway"].(map[string]any)
			gwSec["dev_mode_bypass"] = false
		}),
		"SeedUser must succeed to add non-admin user",
	)

	// Poll with the non-admin user's plaintext token to confirm reload has
	// propagated through the gateway's auth middleware.
	// A 401 means the config swap is not yet complete; any other status means
	// the new user list is live. Timeout: 5 s, 100 ms interval.
	deadline := time.Now().Add(5 * time.Second)
	for {
		probeReq, probeErr := http.NewRequest(http.MethodGet, gw.URL+"/api/v1/agents", nil)
		require.NoError(t, probeErr, "failed to build probe request")
		probeReq.Header.Set("Origin", gw.URL)
		probeReq.Header.Set("Authorization", "Bearer "+userPlain)

		probeResp, probeErr := gw.HTTPClient.Do(probeReq)
		if probeErr == nil {
			status := probeResp.StatusCode
			_ = probeResp.Body.Close()
			// 200 or any non-401 means the user was recognized.
			if status != http.StatusUnauthorized {
				break
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf(
				"gatewayWithRBAC: non-admin user token was not recognized within 5s after SeedUser; "+
					"last probe status: %d — reload may have failed or taken too long",
				http.StatusUnauthorized,
			)
		}
		time.Sleep(100 * time.Millisecond)
	}

	return gw, adminToken, userPlain, csrfToken
}

// randSuffix returns a short timestamp-based suffix suitable for making test
// identifiers unique across parallel runs. It is NOT cryptographic.
func randSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// onboardCSRFAdmin onboards a single password-backed admin into a freshly
// booted gateway and returns the admin's bearer token. Used by the CSRF happy
// path, which needs a real password user to mint a re-auth consent token (the
// synthetic env-token admin from WithBearerAuth has no password hash). Seeding
// gateway.users via onboarding disables the legacy env-token bearer, so callers
// must authenticate with the returned token thereafter.
func onboardCSRFAdmin(t *testing.T, gw *testutil.TestGateway, password string) (adminToken string) {
	t.Helper()
	onboardBody := map[string]any{
		"provider": map[string]any{
			"id":      "openai",
			"api_key": "sk-test-csrf-" + randSuffix(),
			"model":   "gpt-4o",
		},
		"admin": map[string]any{
			"username": "csrfadmin",
			"password": password,
		},
	}
	buf, err := json.Marshal(onboardBody)
	require.NoError(t, err)
	req, err := gw.NewRequest(http.MethodPost, "/api/v1/onboarding/complete",
		bytes.NewReader(buf))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"onboarding must succeed to seed the CSRF admin (body=%s)", string(raw))
	var onboardResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(raw, &onboardResp))
	require.NotEmpty(t, onboardResp.Token, "onboarding must return an admin token")

	// Poll until the seeded admin token authenticates — onboarding hot-reloads
	// gateway.users asynchronously, and until that lands the env-token path is
	// still live and the new token may be briefly unrecognized.
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, perr := http.NewRequest(http.MethodGet, gw.URL+"/api/v1/agents", nil)
		require.NoError(t, perr)
		probe.Header.Set("Origin", gw.URL)
		probe.Header.Set("Authorization", "Bearer "+onboardResp.Token)
		presp, perr := gw.HTTPClient.Do(probe)
		if perr == nil {
			status := presp.StatusCode
			_ = presp.Body.Close()
			if status == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("onboardCSRFAdmin: seeded admin token not recognized within 5s — reload may have failed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return onboardResp.Token
}

// reAuthHeader mirrors pkg/gateway's unexported const of the same name — the
// header the caller replays a single-use re-auth consent token in (Spec-6
// FR-12.2). The re-auth gate (requireReAuth) sits on several sensitive PUT
// routes (/security/tool-policies, /performance, /security/sandbox-config,
// /providers/{id}, /agents/{id}/tools) and rejects an otherwise-authorized
// request that lacks this token with 403. Re-declared here (not imported)
// because tests/security is an external package.
const reAuthHeader = "X-Reauth-Token"

// mintReAuthToken performs the real HTTP consent round-trip:
// POST /api/v1/auth/reauth {"password": <password>} with the caller's bearer
// token, returning the single-use consent token the handler mints. Attach it
// to the very next sensitive PUT via the X-Reauth-Token header.
//
// This is the external-package counterpart to pkg/gateway's withReAuthAdmin
// helper: those handler-level tests mint straight from the in-memory store;
// these tests drive the gateway over HTTP, so the token MUST be obtained via
// the password-verifying /auth/reauth endpoint. The bearer token's user must
// therefore have a real password hash (an onboarded admin) — the synthetic
// env-token admin has none and cannot mint. The token is single-use, so call
// this once per gated request.
func mintReAuthToken(t *testing.T, client *http.Client, baseURL, origin, bearer, password string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"password": password})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/reauth",
		bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := client.Do(req)
	require.NoError(t, err, "POST /api/v1/auth/reauth must not error")
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"re-auth must succeed to mint a consent token (body=%s)", string(raw))
	var rresp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(raw, &rresp),
		"re-auth response must be JSON with a token (body=%s)", string(raw))
	require.NotEmpty(t, rresp.Token, "re-auth must mint a non-empty consent token")
	return rresp.Token
}

// isReAuthGatedPUT reports whether (method, path) is a sensitive PUT guarded by
// requireReAuth (Spec-6 FR-12.2). A test issuing one of these as an authorized
// admin must attach a freshly minted X-Reauth-Token or it will 403 at the
// re-auth gate before reaching the handler logic the test means to exercise.
// Matched by suffix/substring because IDs vary (/providers/{id},
// /agents/{id}/tools).
func isReAuthGatedPUT(method, path string) bool {
	if method != http.MethodPut {
		return false
	}
	switch {
	case strings.HasSuffix(path, "/security/tool-policies"),
		strings.HasSuffix(path, "/performance"),
		strings.HasSuffix(path, "/security/sandbox-config"),
		strings.HasSuffix(path, "/tools"): // /agents/{id}/tools
		return true
	case strings.Contains(path, "/providers/"): // PUT /providers/{id}
		return true
	}
	return false
}

// testCSRFToken is the fixed value used by non-browser test clients that
// just need to satisfy the CSRF double-submit compare (issue #97). The
// middleware only verifies that cookie == header, not that either matches
// a server-side secret — a server-issued cookie prevents cross-origin
// forgery because attackers cannot read it, not because the server
// remembers it. Same-origin test callers can therefore pick any value,
// provided they send it on both sides.
const testCSRFToken = "test-csrf-any-value"

// withCSRF attaches the test CSRF cookie and header to a state-changing
// request so it passes the CSRF middleware. Pure convenience over the
// three-line "AddCookie + Header.Set + ..." idiom.
func withCSRF(req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: testCSRFToken})
	req.Header.Set("X-Csrf-Token", testCSRFToken)
	return req
}

// mustHaveRole asserts that the config currently contains a user with the
// given role. Used as a sanity check before running authz tests.
func mustHaveRole(t *testing.T, cfg *config.Config, role config.UserRole) {
	t.Helper()
	for _, u := range cfg.Gateway.Users {
		if u.Role == role {
			return
		}
	}
	t.Fatalf("expected config.Gateway.Users to contain a user with role %q, got %+v",
		role, cfg.Gateway.Users)
}
