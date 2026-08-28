package security_test

// File purpose: shared helpers for PR-D security tests.
//
// These helpers sit alongside (and deliberately do NOT modify) D1's
// ssrf_matrix_test.go / sandbox_enforcement tests and D3's workflow +
// security_payloads. They are local to the security_test package so that if
// D3 later lifts them into pkg/testutil we can delete this file.
//
// Single-user model (operator directive, 2026-07): the role/RBAC-matrix
// scaffolding (authRole, matrixRequest/matrixExpect/matrixCase,
// gatewayWithRBAC, mustHaveRole) that used to live here was deleted along
// with authz_matrix_test.go — see unauth_access_test.go, which replaces it.
// The re-auth / CSRF helpers below remain in active use by csrf_test.go,
// credential_leakage_test.go, xss_test.go, and supply_chain_test.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// startFakeProviderUpstream starts a loopback stand-in for an OpenAI-compatible
// provider that accepts ANY api key: GET /models returns a one-model catalog and
// POST /chat/completions returns a minimal completion, so
// pkg/providers.ValidateKey classifies the key as `valid`.
//
// HandleCompleteOnboarding (pkg/gateway/rest_onboarding.go) now probes the
// submitted api_key before completing onboarding. Every security test in this
// package that onboards an admin needs to point provider.endpoint at a stand-in
// like this one — otherwise the probe makes a live call to api.openai.com /
// api.anthropic.com, which both fails hermeticity (network-dependent test) and,
// on an egress-restricted runner, silently flips the result from a real 401
// (blocked) to Unreachable (200, proceeds) — a red/green that depends on the
// runner's network rather than the code under test.
//
// This is the tests/security counterpart of pkg/gateway's identically-named
// unexported helper in rest_onboarding_test.go; it cannot be reused directly
// because this file lives in the external security_test package.
func startFakeProviderUpstream(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
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
			"auth_method": "api_key",
			"id":          "openai",
			"api_key":     "sk-test-csrf-" + randSuffix(),
			"model":       "gpt-4o",
			"endpoint":    startFakeProviderUpstream(t),
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
