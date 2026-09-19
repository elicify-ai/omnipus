package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postProbe drives POST /api/v1/onboarding/probe-provider through the PRODUCTION
// route table, exactly as postComplete does for the completion route.
func (e *onboardAuthEnv) postProbe(t *testing.T, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	e.api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.77:54321"
	if authenticated {
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: platformSessionToken})
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req.WithContext(withConfigSnapshot(req.Context(), e.api.agentLoop.GetConfig())))
	return w
}

// TestOnboardingProbe_SignedInUser_IsNotRefused is the regression guard for a
// defect that made onboarding IMPOSSIBLE to finish.
//
// Onboarding step 2 cannot enable Finish until the provider probe passes
// (FR-029: probedModel must equal the selected model). Under platform login the
// wizard is only ever reached by a SIGNED-IN user — and the probe carried the
// pre-auth window gate, one of whose three signals is hasAuthenticationAuthority:
// "a configured user exists". Signing in creates exactly such a user. So the one
// caller the endpoint now has was the one caller it refused, and the wizard
// dead-ended on a step whose only exit is a request that always 409s.
//
// The window gate is right for what it was written to protect (a route that
// MINTS authority). The probe mints nothing: it validates a key the caller
// already typed. What it must still refuse is use after onboarding is finished.
func TestOnboardingProbe_SignedInUser_IsNotRefused(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)

	w := env.postProbe(t, `{"id":"openai","auth_method":"api_key","api_key":"sk-test","model":"gpt-4o"}`, true)

	assert.NotEqual(t, http.StatusConflict, w.Code,
		"a signed-in user in the wizard must not be refused by the pre-auth window gate — "+
			"that gate trips on the very session the wizard now requires, so Finish could never enable; body=%s",
		w.Body.String())
	assert.NotEqual(t, http.StatusUnauthorized, w.Code,
		"the caller holds a valid session; body=%s", w.Body.String())
}

// TestOnboardingProbe_Unauthenticated_IsRefused — the probe carries a caller-supplied
// API key to an upstream provider. With onboarding behind sign-in there is no longer
// any legitimate anonymous caller, so it must not be an open outbound-request oracle.
func TestOnboardingProbe_Unauthenticated_IsRefused(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)

	w := env.postProbe(t, `{"id":"openai","auth_method":"api_key","api_key":"sk-test","model":"gpt-4o"}`, false)

	require.NotEqual(t, http.StatusOK, w.Code,
		"an anonymous caller must never reach the upstream probe; body=%s", w.Body.String())
}

// TestOnboardingProbe_AfterOnboardingComplete_IsRefused — the endpoint's surviving
// purpose. Once setup is done, provider changes go through the normal settings
// flow, which is audited and re-auth free. This is what the old gate got right.
func TestOnboardingProbe_AfterOnboardingComplete_IsRefused(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)

	require.Equal(t, http.StatusOK, env.postComplete(t, "203.0.113.13", env.completeBody(), true).Code)
	require.True(t, env.api.onboardingMgr.IsComplete())

	w := env.postProbe(t, `{"id":"openai","auth_method":"api_key","api_key":"sk-test","model":"gpt-4o"}`, true)

	assert.Equal(t, http.StatusConflict, w.Code,
		"after onboarding the probe must refuse; body=%s", w.Body.String())
}
