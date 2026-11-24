// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_settings_s1_route_guard_test.go — Settings redesign #1519, part 8, RED.
//
// Security finding S1 proof (sec report 2026-11-20, "credential deletion lacks
// the development-bypass guard"). The report's instrument gap: the earlier
// test calls the handler directly with an injected user and "does not exercise
// actual route bypass exclusion, CSRF or deletion auditing". This test drives
// the REAL registered route table — registerAdditionalEndpoints on a real
// ServeMux inside the production middleware chain, with a real login (real
// session cookie) and the real CSRF pair.
//
// Oracle (spec + report, never the fix code):
//   - FR-056: DELETE /credentials/{key} "keeps withAuth, the existing admin
//     and bypass guards". The existing bypass guard is RequireNotBypass, whose
//     documented behaviour on every other high-blast-radius route is 503 while
//     dev_mode_bypass is on (rest.go adminWrap routes; the report names
//     middleware/bypass_gate.go::RequireNotBypass as "the actual bypass
//     blocker").
//   - C-5 B7: no X-Reauth-Token any more; a signed-in delete needs no password.
//   - Report S1 QA ask: "exercise the registered route with bypass on/off and
//     anonymous/authenticated clients, asserting that a refused request leaves
//     the vault entry intact."
//
// RED today (landing): the credentials route is registered with bare withAuth
// (rest.go::registerSettingsAndAccountRoutes), so bypass-ON callers reach
// deleteCredential as the synthetic _dev_bypass identity and are refused only
// by the retired requireReAuth password gate — the very gate FR-056 removes.
// Once that gate goes (the approved server change), nothing but the missing
// bypass guard stands between a no-account caller and the vault.
//
// Expected against work/settings-1519-be (fix e7d663217b): all four cases pass.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

const (
	s1DoomedKey      = "S1_DOOMED_KEY"
	s1DoomedSecret   = "s1-vault-secret-value"
	s1PersonUser     = "s1-person"
	s1PersonPassword = "S1-Route-Guard-Passphrase-1"
)

// s1CredRouteFixture is the real-route harness: person login, real cookies,
// the production middleware chain around the real registered routes, and an
// unlocked credential vault holding the doomed entry.
type s1CredRouteFixture struct {
	api     *restAPI
	handler http.Handler
	cookies []*http.Cookie
}

func newS1CredRouteFixture(t *testing.T) *s1CredRouteFixture {
	t.Helper()
	withEdition(t, config.EditionCore) // local mode: requireReAuth performs the real token check
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	api, home := newTestRestAPIWithUser(t, s1PersonUser, s1PersonPassword)
	credStore := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(credStore), "unlock the proof vault")
	api.credStore = credStore
	require.NoError(t, credStore.Set(s1DoomedKey, s1DoomedSecret), "seed the doomed vault entry")

	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	f := &s1CredRouteFixture{api: api, handler: buildProductionMiddlewareChain(api, mux)}

	login := httptest.NewRecorder()
	loginBody := `{"username":"` + s1PersonUser + `","password":"` + s1PersonPassword + `"}`
	api.HandleLogin(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody)))
	require.Equal(t, http.StatusOK, login.Code, "real person login: %s", login.Body.String())
	f.cookies = login.Result().Cookies()
	return f
}

// csrfCookie returns the login-issued CSRF cookie (the pair an anonymous
// direct client can legitimately obtain — the report: "the caller can satisfy
// the independent CSRF cookie/header requirement; it is not an authentication
// barrier").
func (f *s1CredRouteFixture) csrfCookie(t *testing.T) *http.Cookie {
	t.Helper()
	for _, c := range f.cookies {
		if c.Name == middleware.CSRFCookieName || c.Name == middleware.CSRFCookieNameHTTP {
			return c
		}
	}
	t.Fatal("fixture: the login response must seed a CSRF cookie")
	return nil
}

// deleteCredential sends DELETE /api/v1/credentials/{key} through the real
// registered route. withSession attaches the person's session cookie; without
// it the caller is anonymous (only the CSRF pair is presented).
func (f *s1CredRouteFixture) deleteCredential(t *testing.T, key string, withSession bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/"+key, nil)
	csrf := f.csrfCookie(t)
	req.AddCookie(csrf)
	req.Header.Set(middleware.CSRFHeaderName, csrf.Value)
	if withSession {
		for _, c := range f.cookies {
			if c.Name == middleware.SessionCookieName {
				req.AddCookie(c)
			}
		}
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

// requireVaultIntact proves the refused request left the vault entry intact.
func (f *s1CredRouteFixture) requireVaultIntact(t *testing.T) {
	t.Helper()
	val, err := f.api.credStore.Get(s1DoomedKey)
	require.NoError(t, err, "the vault entry must still be readable after the refused delete")
	require.Equal(t, s1DoomedSecret, val,
		"the vault entry must be intact — a refused delete must not have removed it")
}

func TestDeleteCredentialRoute_BypassGuardMatrix(t *testing.T) {
	t.Run("bypass on, anonymous caller: the bypass guard refuses and the vault stays intact", func(t *testing.T) {
		f := newS1CredRouteFixture(t)
		f.api.agentLoop.GetConfig().Gateway.DevModeBypass = true

		// No session, no bearer: an anonymous direct client holding only the
		// CSRF pair it obtained from a safe request (sec report S1 scenario).
		w := f.deleteCredential(t, s1DoomedKey, false)

		require.Equal(t, http.StatusServiceUnavailable, w.Code,
			"RED (S1, FR-056 'keeps the existing bypass guards'): with dev_mode_bypass on, an anonymous "+
				"caller reaches DELETE /credentials — the registered route carries no RequireNotBypass, so the "+
				"only refusal left is the retired requireReAuth password gate the spec removes. The high-blast-"+
				"radius routes answer 503 from the bypass guard; credential deletion must too. body=%s",
			w.Body.String())
		require.Contains(t, w.Body.String(), "dev_mode_bypass",
			"the refusal must come from the bypass guard itself, not from an accidental gate; body=%s", w.Body.String())
		f.requireVaultIntact(t)
	})

	t.Run("bypass on, signed-in session: still refused, vault intact", func(t *testing.T) {
		f := newS1CredRouteFixture(t)
		f.api.agentLoop.GetConfig().Gateway.DevModeBypass = true

		// The session cookie is presented, but the bypass short-circuit
		// precedes cookie auth: every caller under an open bypass is the
		// synthetic identity, and the bypass guard refuses all of them —
		// the same posture as every other adminWrap route.
		w := f.deleteCredential(t, s1DoomedKey, true)

		require.Equal(t, http.StatusServiceUnavailable, w.Code,
			"RED (S1): with dev_mode_bypass on, a signed-in session must not reach credential deletion either — "+
				"the existing bypass guard refuses everyone while bypass is on; body=%s", w.Body.String())
		require.Contains(t, w.Body.String(), "dev_mode_bypass",
			"the refusal must name the bypass posture; body=%s", w.Body.String())
		f.requireVaultIntact(t)
	})

	t.Run("bypass off, anonymous caller: 401, vault intact (preserved)", func(t *testing.T) {
		f := newS1CredRouteFixture(t)

		w := f.deleteCredential(t, s1DoomedKey, false)

		require.Equal(t, http.StatusUnauthorized, w.Code,
			"with bypass off an anonymous direct client stays unauthenticated (preserved behaviour); body=%s",
			w.Body.String())
		f.requireVaultIntact(t)
	})

	t.Run("bypass off, signed-in person without a password token: delete succeeds (FR-056)", func(t *testing.T) {
		f := newS1CredRouteFixture(t)

		// C-5 B7 / FR-056: the signed-in delete asks one confirm on the
		// screen and needs NO re-auth consent token on the server.
		w := f.deleteCredential(t, s1DoomedKey, true)

		require.Equal(t, http.StatusOK, w.Code,
			"RED (FR-056/B7, through the real registered route): a signed-in delete without a consent token is "+
				"still 403 — deleteCredential still calls requireReAuth; the spec moves the consent to the screen's "+
				"confirm and drops only the server gate. body=%s", w.Body.String())
		require.Contains(t, w.Body.String(), `"status":"removed"`,
			"the delete must actually commit; body=%s", w.Body.String())
		val, err := f.api.credStore.Get(s1DoomedKey)
		require.Error(t, err, "the credential must be gone from the vault after the successful delete")
		require.Empty(t, val, "no value may survive for the deleted key")
	})
}
