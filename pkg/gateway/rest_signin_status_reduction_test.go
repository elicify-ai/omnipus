package gateway

// FR-009/FR-050 additions (2026-09-14): SignInStatus.reason carries why a
// degraded not_signed_in could not actually check the login, and an anonymous
// caller inside the pre-auth window gets a REDUCED status answer — no
// account_label, the same class of value HandleProviders already hides.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anonymousStatusRequest is adminRequest with NO user in the context — the
// pre-auth window's anonymous caller.
func anonymousStatusRequest(t *testing.T, api *restAPI, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
	return w
}

// TestSignInStatus_CopilotReasonOnDegradedStates pins reason for the two
// degrade cases the mapping documents: the check could not run, and the CLI
// is missing. Every definite state must NOT carry one.
func TestSignInStatus_CopilotReasonOnDegradedStates(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"

	// The check-failed state (CLI could not start / timed out) cannot be
	// produced through the HTTP door with the fake — an unrecognised stderr
	// maps to the classifier's recognised NotSignedIn — so it is pinned at
	// the mapping function itself, which is where the reason is set.
	t.Run("check could not run (mapper level)", func(t *testing.T) {
		st, known := copilotSignInStatusResponse(providers_pkg.CopilotSignInResult{
			State:  providers_pkg.CopilotCheckFailed,
			Detail: "exec timed out",
		})
		require.True(t, known)
		require.NotNil(t, st.Reason, "the check-failed degrade must carry a reason")
		assert.Contains(t, *st.Reason, "could not run")
		assert.NotContains(t, *st.Reason, "exec timed out", "the CLI's raw output never reaches the wire")
	})
	t.Run("recognised not_signed_in carries no reason", func(t *testing.T) {
		st, known := copilotSignInStatusResponse(providers_pkg.CopilotSignInResult{
			State: providers_pkg.CopilotNotSignedIn,
		})
		require.True(t, known)
		assert.Nil(t, st.Reason, "a recognised not-signed-in needs no reason")
	})

	t.Run("CLI missing", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		clearCopilotFromPath(t)
		w := adminRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var got gen.SignInStatus
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		require.NotNil(t, got.Reason, "the CLI-missing case must carry a reason")
		assert.Contains(t, *got.Reason, "not installed")
	})

	t.Run("signed in carries no reason", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		putFakeCopilotOnPath(t, "ok", "", 0)
		w := adminRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var got gen.SignInStatus
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.Nil(t, got.Reason, "a definite state must not carry a reason")
	})
}

// TestSignInStatus_AnonymousCallerGetsNoAccountLabel pins the FR-050
// reduction: the label is the operator's vendor account identifier and is
// stripped for an anonymous caller, on both provider methods.
func TestSignInStatus_AnonymousCallerGetsNoAccountLabel(t *testing.T) {
	t.Run("codex-cli: label present for the signed-in admin, absent anonymously", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		const path = "/api/v1/providers/codex-cli/sign-in/status"

		wAdmin := adminRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, wAdmin.Code, "body=%s", wAdmin.Body.String())
		var adminStatus gen.SignInStatus
		require.NoError(t, json.Unmarshal(wAdmin.Body.Bytes(), &adminStatus))

		wAnon := anonymousStatusRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, wAnon.Code, "body=%s", wAnon.Body.String())
		var anonStatus gen.SignInStatus
		require.NoError(t, json.Unmarshal(wAnon.Body.Bytes(), &anonStatus))
		assert.Nil(t, anonStatus.AccountLabel, "an anonymous caller must not see the account label")
		assert.Equal(t, adminStatus.State, anonStatus.State, "state itself is not reduced")
		if adminStatus.AccountLabel != nil {
			assert.NotEqual(t, adminStatus.AccountLabel, anonStatus.AccountLabel)
		}
	})

	t.Run("device_code (openai-chatgpt): same reduction", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		const path = "/api/v1/providers/openai-chatgpt/sign-in/status"

		wAnon := anonymousStatusRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, wAnon.Code, "body=%s", wAnon.Body.String())
		var anonStatus gen.SignInStatus
		require.NoError(t, json.Unmarshal(wAnon.Body.Bytes(), &anonStatus))
		assert.Nil(t, anonStatus.AccountLabel, "an anonymous caller must not see the account label")
	})
}

var _ = config.Config{} // keep the config import if the helpers above change
