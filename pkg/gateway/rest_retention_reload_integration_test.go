// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// TestRetentionSweepE2E_SessionCookieSurvivesBypassFlipAndReload is a full,
// real-gateway (testutil.StartTestGateway) reproduction of
// tests/e2e/retention.spec.ts's session_past_retention_threshold_is_swept,
// used to root-cause the deterministic 401 the Playwright E2E harness
// observes on POST /api/v1/security/retention/sweep.
//
// Sequence (mirrors the E2E test + its harness exactly):
//  1. POST /api/v1/onboarding/complete — mirrors runci.sh's curl onboarding.
//  2. POST /api/v1/auth/login — mirrors global-setup.ts's real login, which
//     is what actually mints the omnipus-session cookie every spec's
//     storageState carries (onboarding's own cookie is discarded by the
//     curl call in runci.sh and never reaches Playwright).
//  3. Read config.json, JSON-round-trip it in a generic map, flip
//     gateway.dev_mode_bypass to false, write it back — byte-for-byte the
//     same technique retention.spec.ts uses via fs.readFileSync +
//     JSON.parse/JSON.stringify + fs.writeFileSync.
//  4. POST /reload — the exact health-server endpoint the E2E test drives.
//  5. POST /api/v1/security/retention/sweep carrying ONLY the cookie from
//     step 2 (no Authorization header — matches ADR-044, where the SPA has
//     no JS-visible bearer token and authHeaders() in the E2E test never
//     finds one either).
import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

func TestRetentionSweepE2E_SessionCookieSurvivesBypassFlipAndReload(t *testing.T) {
	gw := testutil.StartTestGateway(t, testutil.WithAllowEmpty())

	// Step 1: onboard admin.
	onboardBody := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"admin123"}}`
	onboardBody = hermeticOnboardBody(t, onboardBody)
	onboardReq, err := gw.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(onboardBody))
	require.NoError(t, err)
	onboardReq.Header.Set("Content-Type", "application/json")
	onboardResp, err := gw.Do(onboardReq)
	require.NoError(t, err)
	defer onboardResp.Body.Close()
	require.Equal(t, http.StatusOK, onboardResp.StatusCode, "onboarding must succeed")

	// Step 2: login — the actual cookie-minting call global-setup.ts makes.
	loginBody := `{"username":"admin","password":"admin123"}`
	loginReq, err := gw.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	require.NoError(t, err)
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := gw.Do(loginReq)
	require.NoError(t, err)
	defer loginResp.Body.Close()
	require.Equal(t, http.StatusOK, loginResp.StatusCode, "login must succeed")

	var sessionCookie, csrfCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		switch c.Name {
		case middleware.SessionCookieName:
			sessionCookie = c
		case middleware.CSRFCookieName, middleware.CSRFCookieNameHTTP:
			csrfCookie = c
		}
	}
	require.NotNil(t, sessionCookie, "login must issue an omnipus-session cookie")
	require.NotEmpty(t, sessionCookie.Value)
	require.NotNil(
		t,
		csrfCookie,
		"login must issue a CSRF cookie (needed to pass the CSRF gate on cookie-only requests)",
	)
	require.NotEmpty(t, csrfCookie.Value)

	// Step 3: mimic retention.spec.ts's raw config.json read -> JSON.parse ->
	// flip gateway.dev_mode_bypass -> JSON.stringify -> write back.
	raw, err := os.ReadFile(gw.ConfigPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	gwMap, ok := m["gateway"].(map[string]any)
	require.True(t, ok, "gateway key must exist")
	gwMap["dev_mode_bypass"] = false
	m["gateway"] = gwMap
	rewritten, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(gw.ConfigPath(), rewritten, 0o600))

	// Step 4: POST /reload — the exact call the E2E test makes.
	reloadReq, err := gw.NewRequest(http.MethodPost, "/reload", nil)
	require.NoError(t, err)
	reloadResp, err := gw.Do(reloadReq)
	require.NoError(t, err)
	defer reloadResp.Body.Close()
	require.Equal(t, http.StatusOK, reloadResp.StatusCode, "POST /reload must succeed")

	// Same grace period the E2E test waits for async reload propagation.
	time.Sleep(500 * time.Millisecond)

	// Sanity: dev_mode_bypass really did flip and the CSRF gate is satisfied —
	// a request with a VALID CSRF cookie+header but NO session cookie and NO
	// bearer must now 401 (checkBearerAuth's cookie-fallback-failed branch),
	// proving the reload actually applied and we're really exercising
	// checkBearerAuth, not just being blocked earlier by CSRF.
	noAuthReq, err := gw.NewRequest(http.MethodPost, "/api/v1/security/retention/sweep", nil)
	require.NoError(t, err)
	noAuthReq.Header.Del("Authorization")
	noAuthReq.AddCookie(csrfCookie)
	noAuthReq.Header.Set("X-Csrf-Token", csrfCookie.Value)
	noAuthResp, err := gw.Do(noAuthReq)
	require.NoError(t, err)
	defer noAuthResp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, noAuthResp.StatusCode,
		"sanity: a request with no session cookie/bearer must 401 once dev_mode_bypass is really off")

	// Step 5: the cookie minted in step 2, carried alone (no Authorization
	// header — matching what tests/e2e/retention.spec.ts's authHeaders() +
	// page.request.post() actually sends under ADR-044), plus the matching
	// CSRF cookie+header (which the E2E test's authHeaders() ALSO attaches
	// when present — see retention.spec.ts:88-89), must still authenticate
	// the sweep request after the reload.
	sweepReq, err := gw.NewRequest(http.MethodPost, "/api/v1/security/retention/sweep", nil)
	require.NoError(t, err)
	sweepReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionCookie.Value})
	sweepReq.AddCookie(csrfCookie)
	sweepReq.Header.Set("X-Csrf-Token", csrfCookie.Value)
	sweepResp, err := gw.Do(sweepReq)
	require.NoError(t, err)
	defer sweepResp.Body.Close()

	require.NotEqual(t, http.StatusUnauthorized, sweepResp.StatusCode,
		"the pre-reload session cookie must still authenticate the sweep request after POST /reload")
}
