// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared scaffolding
// ---------------------------------------------------------------------------

// completeOnboarding marks the instance onboarded through the real manager, so
// the test exercises the same state.json the production gate reads rather than
// a stubbed boolean.
func completeOnboarding(t *testing.T, api *restAPI) {
	t.Helper()
	require.NotNil(t, api.onboardingMgr, "fixture must carry a real onboarding manager")
	require.NoError(t, api.onboardingMgr.CompleteOnboarding())
	require.True(t, api.onboardingMgr.IsComplete(), "onboarding must read back as complete")
}

// signInProviderRow appends a configured sign_in provider row carrying a live
// OAuth credential, so the list branch has something whose account_label it
// could leak.
func signInProviderRow(t *testing.T, api *restAPI, providerID, accountID string) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Name:       providerID,
		Model:      "gpt-5",
		Provider:   providerID,
		AuthMethod: config.AuthMethodSignIn,
	})
	// Written through the same entry-name function the production reader uses
	// (credentials.OAuthEntryName + providers.OAuthVendorID), so the fixture
	// cannot drift from cheapSignInRowStatus's lookup.
	raw, err := json.Marshal(map[string]any{
		"access_token": "at-secret",
		"account_id":   accountID,
		"expires_at":   time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	require.NoError(t, api.credStore.Set(
		credentials.OAuthEntryName(providers.OAuthVendorID(providerID)), string(raw)))
}

// getProviders issues GET /api/v1/providers. When user is non-empty the caller
// is authenticated as that username; when it is empty the caller is anonymous.
func listProvidersAs(t *testing.T, api *restAPI, user, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	if user != "" {
		ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: user})
	}
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
	return w
}

// ---------------------------------------------------------------------------
// C1 — GET /api/v1/providers was unauthenticated forever
// ---------------------------------------------------------------------------

// TestProviderList_RequiresAuthOnceOnboarded is the direct regression test for
// C1. Before the fix the `GET sub == ""` branch of HandleProviders carried no
// authorization gate of any kind: the route is registered withOptionalAuth
// (anonymous callers pass straight through) and, unlike PUT / /test / the five
// FR-050 sign-in routes / DELETE, this branch never checked anything. On a
// fully onboarded production gateway `curl http://host:5000/api/v1/providers`
// with no credentials returned 200 and the whole provider inventory.
//
// contracts/openapi.yaml has always declared `security: [BearerAuth: []]` and a
// 401 response for listProviders, so this is also the code/contract agreement.
func TestProviderList_RequiresAuthOnceOnboarded(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	completeOnboarding(t, api)

	w := listProvidersAs(t, api, "", "")

	require.Equal(t, http.StatusUnauthorized, w.Code,
		"an anonymous list on an onboarded gateway must be 401; body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "authentication required", body["error"])
}

// TestProviderList_AuthenticatedCallerStillListsAfterOnboarding pins the other
// half of the gate: the fix must not lock the Settings screen out of the list
// it re-reads on every provider edit.
func TestProviderList_AuthenticatedCallerStillListsAfterOnboarding(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	completeOnboarding(t, api)

	w := listProvidersAs(t, api, "admin", "")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var rows []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
}

// TestProviderList_AnonymousNeverSeesAccountLabel is the second C1 proof: the
// operator's live vendor account identifier must never appear in a response to
// an unauthenticated caller. Commit 9a7c5ae8 wired cheapSignInRowStatus into
// the list branch and populated AccountLabel from it — Provider.yaml documents
// that field as "account identifier of the signed-in session", i.e. the
// operator's own ChatGPT/xAI identity.
//
// The assertion is made three ways on purpose: the typed field, the raw JSON
// key, and a substring scan of the whole body — so a future change that moves
// the value onto a different field still fails this test.
func TestProviderList_AnonymousNeverSeesAccountLabel(t *testing.T) {
	const secretAccount = "operator@example.com"

	api, _ := newAuthMethodOnboardingAPI(t)
	signInProviderRow(t, api, "openai-chatgpt", secretAccount)

	// Onboarding deliberately left INCOMPLETE and no users configured: this is
	// the FR-050 pre-auth window, the one state in which an anonymous caller
	// still reaches the list at all. Even here the label must not appear.
	w := listProvidersAs(t, api, "", "")
	require.Equal(t, http.StatusOK, w.Code,
		"the pre-auth window must still answer the wizard; body=%s", w.Body.String())

	body := w.Body.String()
	assert.NotContains(t, body, secretAccount,
		"the operator's vendor account id must not appear anywhere in an unauthenticated response")
	assert.NotContains(t, body, "account_label",
		"an unauthenticated response must not carry the account_label key at all")

	var rows []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.NotEmpty(t, rows, "the fixture configured one provider row")
	for _, row := range rows {
		assert.Nil(t, row.AccountLabel, "row %q leaked an account_label", row.Id)
	}

	// Control: the SAME instance and the SAME row does surface the label to an
	// authenticated caller, so the test above is proving redaction rather than
	// an unpopulated fixture.
	authedRows := []gen.Provider{}
	wAuth := listProvidersAs(t, api, "admin", "")
	require.Equal(t, http.StatusOK, wAuth.Code, "body=%s", wAuth.Body.String())
	require.NoError(t, json.Unmarshal(wAuth.Body.Bytes(), &authedRows))
	var sawLabel bool
	for _, row := range authedRows {
		if row.AccountLabel != nil && *row.AccountLabel == secretAccount {
			sawLabel = true
		}
	}
	assert.True(t, sawLabel,
		"an authenticated caller must still get account_label — otherwise the redaction test proves nothing")
}

// TestProviderList_AnonymousNeverSeesDependents pins the other reduction: the
// dependents array names every agent bound to a provider, i.e. the operator's
// roster. Provider.yaml requires the field, so the anonymous answer is the
// empty array (contract-valid), never the real list.
func TestProviderList_AnonymousNeverSeesDependents(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Name: "openai", Model: "gpt-5", Provider: "openai", APIKeyRef: "openai_API_KEY",
	})
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID: "secret-recruiter", Name: "Secret Recruiter",
		Model: &config.AgentModelConfig{Primary: "gpt-5", Provider: "openai"},
	})

	// Authenticated control first: the dependent IS computed for this fixture.
	wAuth := listProvidersAs(t, api, "admin", "")
	require.Equal(t, http.StatusOK, wAuth.Code, "body=%s", wAuth.Body.String())
	assert.Contains(t, wAuth.Body.String(), "secret-recruiter",
		"an authenticated caller must see dependents — otherwise the redaction test proves nothing")

	w := listProvidersAs(t, api, "", "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "secret-recruiter",
		"an unauthenticated response must not enumerate the operator's agents")

	var rows []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.NotNil(t, row.Dependents,
			"Provider.yaml requires dependents: the anonymous answer is [], never a missing key")
		assert.Empty(t, row.Dependents, "row %q leaked dependents to an anonymous caller", row.Id)
	}
}

// TestProviderList_AnonymousIsRateLimited proves the C1 impact statement's
// last clause ("No rate limit applies either") is closed. The list fans out to
// one upstream /models fetch per configured provider, so an unauthenticated
// caller inside the pre-auth window must not be able to drive it without a
// ceiling. A distinct RemoteAddr keeps this test's bucket out of every other
// test's.
func TestProviderList_AnonymousIsRateLimited(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	const attacker = "198.51.100.31:40000"

	var lastCode int
	for i := 0; i < 61; i++ {
		lastCode = listProvidersAs(t, api, "", attacker).Code
		if lastCode == http.StatusTooManyRequests {
			break
		}
	}
	require.Equal(t, http.StatusTooManyRequests, lastCode,
		"an anonymous caller must be rate limited within 61 requests")

	// An AUTHENTICATED caller from the same address is unaffected — the
	// limiter is scoped to the anonymous path only.
	assert.Equal(t, http.StatusOK, listProvidersAs(t, api, "admin", attacker).Code,
		"the anonymous ceiling must not lock out an authenticated caller")
}

// TestProviderList_EnvBearerTokenCallerIsNotLockedOut guards the C1 gate
// against over-reach. withOptionalAuth's legacy OMNIPUS_BEARER_TOKEN branch
// calls the handler with NO UserContextKey on a successful match, so a naive
// "no user in context => 401" gate would refuse the documented headless/CI
// deployment mode (an env token with no Gateway.Users rows) on a route that
// every ordinary withAuth endpoint serves it happily.
//
// The wrong token must still be refused, so this pins both directions.
func TestProviderList_EnvBearerTokenCallerIsNotLockedOut(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	completeOnboarding(t, api)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "headless-ci-token")

	listWithAuthHeader := func(header string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		req = req.WithContext(context.WithValue(req.Context(),
			ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))
		w := httptest.NewRecorder()
		api.HandleProviders(w, isolateRateLimit(t, req))
		return w
	}

	assert.Equal(t, http.StatusOK, listWithAuthHeader("Bearer headless-ci-token").Code,
		"an env-token principal is authenticated and must be served")
	assert.Equal(t, http.StatusUnauthorized, listWithAuthHeader("Bearer wrong-token").Code,
		"a non-matching bearer token must not pass the gate")
	assert.Equal(t, http.StatusUnauthorized, listWithAuthHeader("").Code,
		"no credential at all must still be 401")
}

// ---------------------------------------------------------------------------
// M3 — the FR-050 gate failed OPEN
// ---------------------------------------------------------------------------

// TestPreAuthWindow_ClosedWhenOnboardingStateUnknown is the M3 regression
// test. onboarding.NewManager keeps OnboardingComplete=false on ANY load
// failure and renames an unparseable state.json aside, so a corrupt file on a
// long-onboarded instance silently reopened all five FR-050 sign-in routes —
// DELETE (destroys the OAuth grant) and import (writes the credential store)
// included — unauthenticated, for the whole process lifetime, after one WARN.
//
// The fix samples the file's readability BEFORE the manager consumes it and
// treats "unknown" as closed. The manager here honestly reports incomplete
// (that is the failure mode being modelled); only onboardingStateUnknown
// distinguishes it from a real fresh install.
func TestPreAuthWindow_ClosedWhenOnboardingStateUnknown(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	require.False(t, api.onboardingMgr.IsComplete(),
		"precondition: the manager reports the corrupt-state instance as a fresh install")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	req = req.WithContext(context.WithValue(req.Context(),
		ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))

	assert.True(t, api.preAuthOnboardingWindowOpen(req),
		"precondition: with a readable state the window is open (this is the FR-050 case)")

	api.onboardingStateUnknown = true
	assert.False(t, api.preAuthOnboardingWindowOpen(req),
		"an unknown onboarding state must NOT be treated as a fresh install")

	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, req))
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"with the state unknown, an anonymous provider read must 401; body=%s", w.Body.String())
}

// TestPreAuthWindow_ClosedWhenAnAuthenticationAuthorityExists pins the second
// M3 signal, the one a corrupt state.json cannot erase. FR-050 exists because
// there is no admin account to authenticate as yet; if somebody CAN
// authenticate here, that premise is false whatever state.json says.
func TestPreAuthWindow_ClosedWhenAnAuthenticationAuthorityExists(t *testing.T) {
	t.Run("a configured user closes the window", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		cfg := api.agentLoop.GetConfig()
		cfg.Gateway.Users = []config.UserConfig{{Username: "admin", PasswordHash: "x"}}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))

		assert.False(t, api.preAuthOnboardingWindowOpen(req))
		w := httptest.NewRecorder()
		api.HandleProviders(w, isolateRateLimit(t, req))
		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})

	t.Run("OMNIPUS_BEARER_TOKEN closes the window", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "env-token")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		req = req.WithContext(context.WithValue(req.Context(),
			ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))

		assert.False(t, api.preAuthOnboardingWindowOpen(req))
	})
}

// TestDevModeBypass_GrantsNoAdditionalAccessOnFR050GatedRoutes pins a second
// UAT finding surfaced while fixing the /providers/catalog release blocker
// above (live repro, 2026-08-27): with gateway.dev_mode_bypass=true and
// onboarding COMPLETE, GET /api/v1/providers/catalog returned 200 (it was
// still registered under withAuth at the time — checkBearerAuth's bypass
// short-circuit, auth.go, supplies the synthetic _dev_bypass identity)
// while GET /api/v1/providers returned 401 (already withOptionalAuth +
// requireAuthOutsideOnboarding — bypass supplies no identity on that path;
// hasAuthenticationAuthority's own doc comment says this is deliberate, and
// preAuthOnboardingWindowOpen's window is closed once onboarding is
// complete regardless). The two sibling provider routes disagreed about
// whether the identical anonymous caller was authenticated.
//
// Moving /providers/catalog onto the SAME withOptionalAuth +
// requireAuthOutsideOnboarding shape /providers already used (this file's
// C1 fix, and this commit's catalog fix) resolves that disagreement: both
// routes now fail closed under bypass once onboarding is complete, which is
// what hasAuthenticationAuthority's rationale already required — dev bypass
// must never give an FR-050-gated route MORE access than an anonymous
// caller gets. checkBearerAuth's OWN bypass grant (used by plain withAuth
// routes) is a separate, older, deliberate design for driving the SPA
// pre-login locally (CLAUDE.md "Running the embedded SPA") and is
// deliberately left untouched here — broadening hasAuthenticationAuthority
// to match it would be a security-boundary policy change belonging to a
// dedicated review, not a side effect of this bug fix.
//
// This test does not change that policy; it pins the now-CONSISTENT
// behavior across both routes so a future edit that reintroduces a
// disagreement between them is caught.
func TestDevModeBypass_GrantsNoAdditionalAccessOnFR050GatedRoutes(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = freshCatalog(t)
	completeOnboarding(t, api)

	cfg := api.agentLoop.GetConfig()
	cfg.Gateway.DevModeBypass = true

	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	for _, path := range []string{"/api/v1/providers/catalog", "/api/v1/providers"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req.WithContext(ctx))

			assert.Equal(t, http.StatusUnauthorized, w.Code,
				"dev_mode_bypass must not grant an FR-050-gated route access once "+
					"onboarding is complete and no real identity is configured; body=%s",
				w.Body.String())
		})
	}
}

// ---------------------------------------------------------------------------
// C2 — the Copilot probe was pollable and billed to the operator
// ---------------------------------------------------------------------------

// TestCopilotProbe_SignedInResultIsCachedNotReprobed is the C2 billing proof.
// The probe execs `copilot -p ... --allow-all-tools --no-ask-user`, which
// CopilotSignIn's own doc comment says "costs one premium request when the
// operator is signed in ... and MUST NOT be put on a poll or a page-load
// path"; it nonetheless sat on an FR-050 pre-auth route behind a 60/min
// per-IP ceiling, i.e. 3,600 premium requests/hour billed to the operator.
//
// The fake CLI counts its own invocations, so this asserts the number of
// VENDOR EXECS, not merely the number of 200s.
func TestCopilotProbe_SignedInResultIsCachedNotReprobed(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	api, _ := newAuthMethodOnboardingAPI(t)
	counter := putCountingCopilotOnPath(t, "ok", "", 0)

	for i := 0; i < 20; i++ {
		w := adminRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, w.Code, "call %d body=%s", i, w.Body.String())
		var got gen.SignInStatus
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.Equal(t, gen.SignInStatusStateSignedIn, got.State,
			"call %d must still report the real state", i)
	}

	assert.Equal(t, 1, countInvocations(t, counter),
		"20 status calls against a signed-in operator must spend exactly ONE premium request")
}

// TestCopilotProbe_NotSignedInIsNeverCached pins the correctness half of the
// cache: the transition an operator actually waits on (run `copilot login`,
// click Check sign-in) must never be answered from a stale negative. That is
// also why the cache is safe — a not_signed_in probe spends no premium
// request, so re-running it costs nothing.
func TestCopilotProbe_NotSignedInIsNeverCached(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	api, _ := newAuthMethodOnboardingAPI(t)

	dir := neutralFakeCLIDir(t)
	writeFakeCopilot(t, dir, "", "Error: No authentication information found.", 1)
	t.Setenv("PATH", dir)
	logs := captureSlog(t)

	w := adminRequest(t, api, http.MethodGet, path)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var first gen.SignInStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
	require.Equal(t, gen.SignInStatusStateNotSignedIn, first.State)
	// not_signed_in is also the fallback for a message nobody recognised, so
	// the state alone would pass even if the fake never printed its text.
	require.NotContains(t, logs.String(), unrecognisedCopilotMessageLog,
		"the verified no-credential message must be recognised, not reach the fallback; logs=%s", logs.String())

	// The operator now signs in: the SAME binary path starts reporting success.
	writeFakeCopilot(t, dir, "ok", "", 0)

	w2 := adminRequest(t, api, http.MethodGet, path)
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	var second gen.SignInStatus
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &second))
	assert.Equal(t, gen.SignInStatusStateSignedIn, second.State,
		"the sign-in transition must be visible immediately, never masked by a cached negative")
}

// TestCopilotProbe_ConcurrentCallsAreRefusedNotSpawned is the C2 process-spawn
// proof: up to ~60 concurrent `copilot` children could be alive at once if the
// CLI hung to its 60s timeout. Only one probe may be in flight; the rest are
// refused with 429 rather than queued behind a held request.
func TestCopilotProbe_ConcurrentCallsAreRefusedNotSpawned(t *testing.T) {
	api := &restAPI{}

	require.True(t, api.copilotProbe.acquire(), "the first probe must claim the slot")
	assert.False(t, api.copilotProbe.acquire(),
		"a second concurrent probe must be refused, never spawned alongside the first")

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/providers/github-copilot/sign-in/status", nil)
	w := httptest.NewRecorder()
	api.handleCopilotSignInStatus(w, req)
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"a call arriving while a probe runs must be 429; body=%s", w.Body.String())
	assert.Equal(t, "5", w.Header().Get("Retry-After"),
		"a 429 must tell the caller when to come back")

	api.copilotProbe.release()
	assert.True(t, api.copilotProbe.acquire(), "the slot must be reusable after release")
}

// TestCopilotProbe_ConcurrentHTTPCallsSpawnOneVendorProcess drives the same
// guarantee through the real handler from many goroutines at once, counting
// actual vendor execs.
//
// It used to assert only "at most one vendor process", and never looked at a
// response. That also passes when NO call reached the probe — for example
// when every call was refused by the per-IP rate limiter, which this test
// shared with the rest of the package because it was the one provider test
// not isolated — since 0 <= 1. The shared check below closes that.
func TestCopilotProbe_ConcurrentHTTPCallsSpawnOneVendorProcess(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	counter := putCountingCopilotOnPath(t, "ok", "", 0)
	requireConcurrentCopilotChecksSpawnOneProcess(t, api, counter, 25)
}

// requireConcurrentCopilotChecksSpawnOneProcess sends `calls` concurrent
// Copilot sign-in checks as an admin and requires that the C2 guard, and only
// the C2 guard, shaped every answer:
//
//   - every request carries this test's own rate-limit address, so the per-IP
//     limiter can never be what refused it;
//   - every answer is either signed_in (the one probe, or the C2 cache after
//     it) or the C2 guard's own "already running" 429 — a rate-limiter 429 or
//     anything else fails;
//   - at least one call reached the probe, and exactly ONE vendor process ran.
func requireConcurrentCopilotChecksSpawnOneProcess(t *testing.T, api *restAPI, counter string, calls int) {
	t.Helper()
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	cfg := api.agentLoop.GetConfig()

	// Built up front: isolateRateLimit takes t, which a goroutine must not use
	// to fail the test.
	reqs := make([]*http.Request, calls)
	recorders := make([]*httptest.ResponseRecorder, calls)
	for i := range reqs {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		ctx := context.WithValue(req.Context(), UserContextKey{},
			&config.UserConfig{Username: "admin"})
		ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
		reqs[i] = isolateRateLimit(t, req.WithContext(ctx))
		recorders[i] = httptest.NewRecorder()
	}

	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			api.HandleProviders(recorders[i], reqs[i])
		}(i)
	}
	wg.Wait()

	signedIn, refusedByGuard := 0, 0
	for i, w := range recorders {
		switch w.Code {
		case http.StatusOK:
			var got gen.SignInStatus
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got), "call %d body=%s", i, w.Body.String())
			require.Equal(t, gen.SignInStatusStateSignedIn, got.State, "call %d body=%s", i, w.Body.String())
			signedIn++
		case http.StatusTooManyRequests:
			require.Contains(t, w.Body.String(), "a Copilot sign-in check is already running",
				"call %d was refused by something other than the C2 single-flight guard: body=%s", i, w.Body.String())
			refusedByGuard++
		default:
			require.Failf(t, "unexpected response", "call %d: status %d body=%s", i, w.Code, w.Body.String())
		}
	}
	require.Equal(t, calls, signedIn+refusedByGuard)
	require.GreaterOrEqual(t, signedIn, 1, "at least one concurrent call must have reached the probe")
	assert.Equal(t, 1, countInvocations(t, counter),
		"%d concurrent status calls must spawn exactly ONE vendor process", calls)
}

// writeFakeCopilot writes a `copilot` stand-in into dir. Split out of the
// existing putFakeCopilotOnPath so a test can REPLACE the binary mid-test
// (the sign-in transition) without swapping PATH. The script uses shell
// built-ins only and is self-checked before use — see installFakeCopilot.
func writeFakeCopilot(t *testing.T, dir, stdout, stderr string, exitCode int) {
	t.Helper()
	// Builtins only — dir is the whole PATH (see writeFakeCopilotBinary).
	writeFakeCopilotBinary(t, dir, "", stdout, stderr, exitCode)
}

// putCountingCopilotOnPath installs a `copilot` stand-in that appends one line
// to a tally file per invocation, and returns that file's path. Counting execs
// rather than HTTP 200s is the point: the defect is about how many PREMIUM
// REQUESTS the operator is billed for, not how many responses are returned.
// installFakeCopilot proves the counter works before the test relies on a
// count of zero meaning "never ran".
func putCountingCopilotOnPath(t *testing.T, stdout, stderr string, exitCode int) string {
	t.Helper()
	if !hasBash() {
		t.Skip("fake CLI uses a #!/bin/bash shebang with no Windows equivalent (see #113)")
	}
	dir := neutralFakeCLIDir(t)
	tally := filepath.Join(dir, "invocations")
	require.NotContainsf(t, tally, "'", "tally path %q cannot be single-quoted", tally)
	// `echo` is a bash builtin; the output half is writeFakeCopilotBinary's,
	// builtins only, because dir is the whole PATH.
	writeFakeCopilotBinary(t, dir, "echo x >> '"+tally+"'\n", stdout, stderr, exitCode)
	t.Setenv("PATH", dir)
	return tally
}

// countInvocations reports how many times the counting stand-in ran. A missing
// tally file means zero runs, not a failure.
func countInvocations(t *testing.T, tally string) int {
	t.Helper()
	data, err := os.ReadFile(tally)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(strings.Fields(string(data)))
}

func hasBash() bool {
	_, err := os.Stat("/bin/bash")
	return err == nil
}

// ---------------------------------------------------------------------------
// M5 — pre-auth sign-in probes produced no audit trail
// ---------------------------------------------------------------------------

// TestCopilotProbe_IsAudited proves M5 for the one sign-in emission that lives
// in this team's files. The C2 vendor traffic was previously invisible: the
// probe emitted nothing at all, so an operator billed for thousands of premium
// requests had no record of who drove them or from where.
func TestCopilotProbe_IsAudited(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	api, _ := newAuthMethodOnboardingAPI(t)
	putCountingCopilotOnPath(t, "ok", "", 0)
	auditDir := attachTestAuditor(t, api)

	// Anonymous, inside the FR-050 pre-auth window — the exact shape of the
	// C2 abuse traffic.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "203.0.113.55:5555"
	req = req.WithContext(context.WithValue(req.Context(),
		ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, req))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	entries := readAuditEntries(t, auditDir, EventProviderSignInStatusChecked)
	require.Len(t, entries, 1, "exactly one probe must produce exactly one audit entry")
	details, _ := entries[0]["details"].(map[string]any)
	require.NotNil(t, details, "the entry must carry a details map")
	assert.Equal(t, "github-copilot", details["provider"])
	assert.Equal(t, "203.0.113.55", details["source_ip"],
		"the entry must record where the call came from")
	assert.Equal(t, "signed_in", details["state"])
	assert.Equal(t, false, details["cached"],
		"the first probe actually ran the vendor CLI, so it must not be marked cached")

	// An authenticated probe records the actor. Rebuilt fresh so the cache
	// does not absorb it.
	api2, _ := newAuthMethodOnboardingAPI(t)
	putCountingCopilotOnPath(t, "ok", "", 0)
	auditDir2 := attachTestAuditor(t, api2)
	req2 := httptest.NewRequest(http.MethodGet, path, nil)
	req2.RemoteAddr = "203.0.113.56:5556"
	ctx2 := context.WithValue(req2.Context(), UserContextKey{},
		&config.UserConfig{Username: "admin"})
	ctx2 = context.WithValue(ctx2, ctxkey.ConfigContextKey{}, api2.agentLoop.GetConfig())
	w2 := httptest.NewRecorder()
	api2.HandleProviders(w2, isolateRateLimit(t, req2.WithContext(ctx2)))
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())

	entries2 := readAuditEntries(t, auditDir2, EventProviderSignInStatusChecked)
	require.Len(t, entries2, 1)
	assert.Equal(t, "admin", entries2[0]["user"],
		"an authenticated probe must name the actor that drove it")
}

// ---------------------------------------------------------------------------
// M2 — two FR-050 pre-auth routes had no rate limit
// ---------------------------------------------------------------------------

// TestSignInImportAndSignOut_AreRateLimited is the M2 regression test. Both
// routes called their handlers BARE while start/poll/status were wrapped. Each
// import rewrites the whole encrypted credentials.json and re-registers every
// OAuth value; each sign-out nils the process-wide sensitive-data replacer
// cache, forcing a full reflection walk of Config under a write lock on the
// next scrub.
//
// Distinct RemoteAddrs keep these buckets away from every other test's.
func TestSignInImportAndSignOut_AreRateLimited(t *testing.T) {
	t.Run("import", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		code := hammerProviderRoute(t, api, http.MethodPost,
			"/api/v1/providers/openai-chatgpt/sign-in/import", "198.51.100.41:41000", 12)
		assert.Equal(t, http.StatusTooManyRequests, code,
			"the import route must be rate limited like its FR-050 siblings")
	})

	t.Run("sign-out", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		code := hammerProviderRoute(t, api, http.MethodDelete,
			"/api/v1/providers/openai-chatgpt/sign-in", "198.51.100.42:42000", 12)
		assert.Equal(t, http.StatusTooManyRequests, code,
			"the sign-out route must be rate limited like its FR-050 siblings")
	})
}

// hammerProviderRoute issues up to n anonymous calls from one address and
// returns the first 429's code (or the last code seen).
func hammerProviderRoute(t *testing.T, api *restAPI, method, path, remoteAddr string, n int) int {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	code := 0
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = remoteAddr
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))
		w := httptest.NewRecorder()
		api.HandleProviders(w, isolateRateLimit(t, req))
		code = w.Code
		if code == http.StatusTooManyRequests {
			return code
		}
	}
	return code
}

// ---------------------------------------------------------------------------
// M4 — every sign-in rate limiter was bypassable on trust_xff deployments
// ---------------------------------------------------------------------------

// TestCanonicalRemoteIP_UsesTheTrustedRightmostHop is the M4 regression test.
// canonicalRemoteIP took the LEFTMOST X-Forwarded-For entry when trust_xff was
// on. The nginx idiom this project documents
// (proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for) APPENDS the
// peer to whatever the client sent, so the leftmost entry is a string the
// ATTACKER chose: a fresh value per request yielded a fresh rate-limit bucket,
// defeating signInStatus/Start/Poll and globalLoginLimiter's brute-force
// protection, and poisoning the audit source_ip.
func TestCanonicalRemoteIP_UsesTheTrustedRightmostHop(t *testing.T) {
	cases := []struct {
		name string
		xff  string
		ra   string
		want string
	}{
		{
			// The attack, exactly as nginx delivers it.
			name: "attacker-supplied entry is ignored in favour of the proxy-appended one",
			xff:  "6.6.6.6, 198.51.100.42",
			ra:   "10.0.0.1:443",
			want: "198.51.100.42",
		},
		{
			name: "a whole forged chain still resolves to the appended hop",
			xff:  "1.1.1.1, 2.2.2.2, 3.3.3.3, 198.51.100.42",
			ra:   "10.0.0.1:443",
			want: "198.51.100.42",
		},
		{
			// Caddy's documented header_up X-Forwarded-For {remote_host}
			// REPLACES the header, so its single entry is the real client.
			name: "single entry (Caddy replace idiom) is honoured unchanged",
			xff:  "203.0.113.1",
			ra:   "10.0.0.1:1234",
			want: "203.0.113.1",
		},
		{
			name: "whitespace around the trusted hop is trimmed",
			xff:  "6.6.6.6 ,  203.0.113.1 ",
			ra:   "10.0.0.1:1234",
			want: "203.0.113.1",
		},
		{
			// Fail-closed: a trailing comma must not yield an empty key that
			// every caller would then share.
			name: "an empty trailing entry falls back to RemoteAddr",
			xff:  "6.6.6.6,",
			ra:   "10.0.0.9:1234",
			want: "10.0.0.9",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
			req.RemoteAddr = tc.ra
			req.Header.Set("X-Forwarded-For", tc.xff)
			assert.Equal(t, tc.want, canonicalRemoteIP(req, true))
		})
	}
}

// TestSpoofedXFF_CannotResetASignInLimiterOnTrustXFFDeployments is the
// end-to-end M4 proof: on a trust_xff deployment, a fresh forged
// X-Forwarded-For per request must NOT hand the caller a fresh bucket.
// canonicalRemoteIP is exercised through the real limiter path, so a
// regression that reintroduced leftmost parsing anywhere in the chain fails
// here even if the unit test above were adjusted.
func TestSpoofedXFF_CannotResetASignInLimiterOnTrustXFFDeployments(t *testing.T) {
	limiter := newAPIRateLimiter(2, time.Minute)
	calls := 0
	wrapped := withRateLimit(limiter, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	cfg := &config.Config{Gateway: config.GatewayConfig{TrustXFF: true}}

	var lastCode int
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/x/sign-in", nil)
		req.RemoteAddr = "10.0.0.1:443" // the trusted proxy
		// A DIFFERENT forged left-hand entry every time; nginx appends the
		// real (constant) client address.
		req.Header.Set("X-Forwarded-For",
			"9.9.9."+strconv.Itoa(i)+", 198.51.100.77")
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))
		w := httptest.NewRecorder()
		wrapped(w, req)
		lastCode = w.Code
	}

	assert.Equal(t, http.StatusTooManyRequests, lastCode,
		"a fresh spoofed leftmost X-Forwarded-For must not reset the bucket")
	assert.Equal(t, 2, calls, "only the limiter's allowance may reach the handler")
}

// ---------------------------------------------------------------------------
// audit scaffolding
// ---------------------------------------------------------------------------

// attachTestAuditor wires a real audit logger writing into home/audit so the
// M5 test reads the ACTUAL JSONL the gateway would write, not a stub.
func attachTestAuditor(t *testing.T, api *restAPI) string {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	api.auditor = logger
	return dir
}

// readAuditEntries returns every audit entry with the given event name.
func readAuditEntries(t *testing.T, auditDir, event string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "line=%s", line)
		if entry["event"] == event {
			out = append(out, entry)
		}
	}
	return out
}
