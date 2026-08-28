// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_provider_preauth_test.go — the three provider-route findings that
// followed the /onboarding/complete authority fix (4335e043 / 0ad0959a):
//
//	O4  POST /api/v1/onboarding/probe-provider   — fail-OPEN window gate.
//	O3  POST /api/v1/providers/{id}/entitlement  — fail-OPEN window gate that
//	                                               should never have had a
//	                                               window at all; no limiter.
//	O5  PUT  /api/v1/providers/{id}              — pre-auth reachable, no
//	    POST /api/v1/providers/{id}/test           limiter of any kind.
//
// All three shared one root cause with the five sign-in routes ADR-068
// FR-050/M3 had already hardened and with /onboarding/complete: the gate was
// `a.onboardingMgr.IsComplete()` and nothing else. onboarding.NewManager
// keeps its fresh-install zero value (OnboardingComplete=false) on ANY load
// failure and renames an unparseable state.json aside, so a truncated file, a
// disk error, a botched chmod or a restored backup silently reopens the route
// to anonymous callers on a long-onboarded instance, for the whole process
// lifetime.
//
// Every test here drives the PRODUCTION route table
// (registerAdditionalEndpoints through a real *http.ServeMux — the exact call
// gateway.go makes at startup) rather than a hand-built handler, so a
// regression in EITHER the registration line or the in-handler gate fails
// them. The one deliberate exception is
// TestPreAuthOnboardingWindowGate_RefusesWhenConfigIsUnreadable, which has to
// construct a restAPI with no readable config at all — a state the mux's own
// withOptionalAuth wrapper would dereference before the gate ever ran.
//
// Each test uses its OWN source IP. Every limiter in this package is a
// process-global, per-IP sliding window shared with every other test in the
// package; a shared address makes tests order-dependent and has already
// dropped a 429 on an unrelated test in this repo once.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// ── shared plumbing ─────────────────────────────────────────────────────────

// serveViaRealMux runs one request through the production route table.
// configSnapshotMiddleware is not in this mux, so the caller's snapshot is
// injected the way that middleware would have.
func serveViaRealMux(
	t *testing.T, api *restAPI, cfg *config.Config, req *http.Request, sourceIP string,
) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	req.RemoteAddr = sourceIP + ":54321"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req.WithContext(withConfigSnapshot(req.Context(), cfg)))
	return w
}

// ── O4: POST /api/v1/onboarding/probe-provider ──────────────────────────────

// newProbeWindowEnv is newOnboardAuthEnv (config.json carrying the named
// users, onboarding deliberately INCOMPLETE, audit logger attached) plus the
// pieces the probe itself needs: the served catalog it admits ids against,
// and an SSRF checker that allowlists loopback so the httptest upstream is
// reachable.
//
// Passing no users produces a genuine fresh install; passing one reproduces
// the divergent state exactly.
func newProbeWindowEnv(t *testing.T, existingUsers ...string) (*onboardAuthEnv, *probeUpstream) {
	t.Helper()
	env := newOnboardAuthEnv(t, existingUsers...)
	cat := probeTestCatalog(t)
	env.api.providerCatalog = cat
	installProbeCatalog(t, cat)
	env.api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1", "::1"})
	return env, startProbeUpstream(t)
}

// probeBody is a well-formed api_key probe pointed at the loopback upstream.
// `openrouter` is a catalog row that offers api_key, and `rec-newest` is one
// of its models, so nothing but the window gate can refuse this request.
func probeBody(up *probeUpstream) string {
	return fmt.Sprintf(
		`{"id":"openrouter","auth":"api_key","api_key":"sk-anonymous","model":"rec-newest","api_base":%q}`,
		up.URL+"/v1")
}

func postProbeViaRealMux(
	t *testing.T, env *onboardAuthEnv, sourceIP, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return serveViaRealMux(t, env.api, env.cfg, req, sourceIP)
}

// TestProbeProvider_RealMux_RefusesWhenAuthenticationAuthorityExists is the
// O4 reproduction. In the divergent state — a real operator in config.json,
// onboarding.completed back to false — the old `IsComplete()`-only gate let
// an anonymous POST straight through to the upstream.
//
// The upstream call count is the assertion that matters. This endpoint is not
// merely informational: with `auth: "sign_in"` it never reads a credential
// from the request at all, it spends one real, billed vendor completion using
// the OPERATOR's own saved login (Codex auth.json, or the Copilot CLI's
// stored token), and on a cli_kind row spawns a subprocess to do it. The
// api_key path exercised here rides the identical gate, so proving zero
// outbound requests proves the money path is shut too.
func TestProbeProvider_RealMux_RefusesWhenAuthenticationAuthorityExists(t *testing.T) {
	env, up := newProbeWindowEnv(t, "realoperator")

	w := postProbeViaRealMux(t, env, "198.51.100.11", probeBody(up))

	require.Equal(t, http.StatusConflict, w.Code,
		"an instance that already has an authentication authority must refuse to probe, "+
			"whatever the onboarding flag says; body=%s", w.Body.String())
	assert.Equal(t, 0, up.requests(),
		"a refused probe must spend NOTHING upstream — this is the finding: "+
			"an anonymous caller burning the operator's billable vendor quota")
	assert.False(t, env.api.onboardingMgr.IsComplete(),
		"the fixture must still be in the divergent state — if this flipped, "+
			"the test proved something other than what it claims")
}

// TestProbeProvider_RealMux_FreshInstallStillSucceeds guards the other
// direction, and is the reason this fix is a gate rather than a 401. A
// genuine first run has NO users and NO state.json, and the onboarding wizard
// cannot get past step 3 without this endpoint: it is the only way to
// validate a provider before an admin exists to authenticate as.
func TestProbeProvider_RealMux_FreshInstallStillSucceeds(t *testing.T) {
	env, up := newProbeWindowEnv(t) // no existing users

	w := postProbeViaRealMux(t, env, "198.51.100.12", probeBody(up))

	require.Equal(t, http.StatusOK, w.Code,
		"a genuine fresh install must still be able to probe a provider; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"],
		"the probe must report the loopback upstream's valid key as valid; body=%s", w.Body.String())
	assert.Equal(t, "rec-newest", resp["probed_model"])
	assert.Positive(t, up.requests(),
		"a permitted probe must actually reach the provider — a gate that answers "+
			"200 without probing would pass every other assertion here")
}

// TestProbeProvider_RealMux_RefusesWhenOnboardingStateIsUnknown pins signal 1.
// An existing-but-unreadable state.json is "unknown", never "fresh install":
// it is indistinguishable from the attack, and refusing is recoverable while
// spending the operator's quota is not. A MISSING state.json — what a real
// first launch looks like — is not "unknown", which is why the test above
// still passes.
func TestProbeProvider_RealMux_RefusesWhenOnboardingStateIsUnknown(t *testing.T) {
	env, up := newProbeWindowEnv(t) // no users at all
	env.api.onboardingStateUnknown = true

	w := postProbeViaRealMux(t, env, "198.51.100.13", probeBody(up))

	require.Equal(t, http.StatusConflict, w.Code,
		"an unreadable onboarding state must close the window, not open it; body=%s",
		w.Body.String())
	assert.Equal(t, 0, up.requests(), "and must spend nothing upstream")
}

// TestProbeProvider_RealMux_RefusesWhenEnvBearerTokenIsTheAuthority pins the
// half of signal 3 that lives outside config.json. OMNIPUS_BEARER_TOKEN is a
// real credential on an instance with no configured users (the documented
// headless deployment mode), so an instance carrying one is not a fresh
// install however empty gateway.users is.
func TestProbeProvider_RealMux_RefusesWhenEnvBearerTokenIsTheAuthority(t *testing.T) {
	env, up := newProbeWindowEnv(t) // no users in config.json
	t.Setenv("OMNIPUS_BEARER_TOKEN", "headless-operator-token")

	w := postProbeViaRealMux(t, env, "198.51.100.14", probeBody(up))

	require.Equal(t, http.StatusConflict, w.Code,
		"an env-token authority closes the window just as a configured user does; body=%s",
		w.Body.String())
	assert.Equal(t, 0, up.requests())
}

// TestProbeProvider_RealMux_AuditsRefusal pins SEC-15/SEC-17 for the refusal.
// The response body deliberately says nothing about WHY (see the test below),
// so the audit log is the only place the reason exists — and without a source
// IP a refused attempt is unattributable.
func TestProbeProvider_RealMux_AuditsRefusal(t *testing.T) {
	env, up := newProbeWindowEnv(t, "realoperator")

	w := postProbeViaRealMux(t, env, "198.51.100.15", probeBody(up))
	require.Equal(t, http.StatusConflict, w.Code)

	var entry map[string]any
	for _, line := range env.auditEvents(t) {
		if line["event"] == audit.EventOnboardingRefused {
			entry = line
			break
		}
	}
	require.NotNil(t, entry, "a refused probe must be audited")
	assert.Equal(t, audit.DecisionDeny, entry["decision"])
	assert.NotEmpty(t, entry["policy_rule"], "SEC-17 requires an explanation on every deny")

	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "the entry must carry details")
	assert.Equal(t, "authentication_authority_exists", details["reason"])
	assert.Equal(t, "198.51.100.15", details["source_ip"])
	assert.Equal(t, "/api/v1/onboarding/probe-provider", details["route"],
		"the route field is what separates this from the /onboarding/complete refusal "+
			"that shares the event name")

	assert.NotContains(t, w.Body.String(), "sk-anonymous",
		"the refusal must not echo the submitted key")
	rendered, err := json.Marshal(entry)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), "sk-anonymous",
		"nor may the audit entry carry it")
}

// TestProbeProvider_RefusalBodyRevealsNothing checks the anti-oracle property
// the single refusal message exists for. GET /api/v1/state still reports
// onboarding_complete=false in the divergent state, so a reason-specific body
// here would be the one signal telling an anonymous caller that this instance
// is in the interesting state.
func TestProbeProvider_RefusalBodyRevealsNothing(t *testing.T) {
	onboarded, upA := newProbeWindowEnv(t)
	require.NoError(t, onboarded.api.onboardingMgr.CompleteOnboarding())
	onboardedBody := postProbeViaRealMux(t, onboarded, "198.51.100.16", probeBody(upA)).Body.String()

	divergent, upB := newProbeWindowEnv(t, "realoperator")
	divergentBody := postProbeViaRealMux(t, divergent, "198.51.100.17", probeBody(upB)).Body.String()

	unknown, upC := newProbeWindowEnv(t)
	unknown.api.onboardingStateUnknown = true
	unknownBody := postProbeViaRealMux(t, unknown, "198.51.100.18", probeBody(upC)).Body.String()

	assert.Equal(t, onboardedBody, divergentBody,
		"the divergent state must be indistinguishable from an ordinary onboarded instance")
	assert.Equal(t, onboardedBody, unknownBody,
		"and so must an unreadable onboarding state; the reason belongs in the audit log")
}

// TestPreAuthOnboardingWindowGate_RefusesWhenConfigIsUnreadable pins the
// fail-closed branch both /onboarding/complete and /onboarding/probe-provider
// inherit from the shared gate. hasAuthenticationAuthority reports a nil
// config snapshot as "no authority", which would OPEN the window; a config we
// cannot read cannot tell us whether users exist, so the answer must be
// refusal.
//
// This one calls the gate directly. It cannot go through the mux: the state
// under test is "no readable config anywhere", and withOptionalAuth
// dereferences a.agentLoop.GetConfig() before the gate would ever run.
func TestPreAuthOnboardingWindowGate_RefusesWhenConfigIsUnreadable(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	// agentLoop nil and no snapshot in context: requestConfigSnapshot returns
	// nil, which is the whole point.
	api := &restAPI{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", nil)
	req.RemoteAddr = "198.51.100.19:54321"
	w := httptest.NewRecorder()

	ok := api.preAuthOnboardingWindowGate(w, req, onboardingProbeRoute, probeWindowClosedMsg)

	assert.False(t, ok, "an unreadable config must not open the pre-auth window")
	assert.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "onboarding already complete",
		"and must use the same anti-oracle refusal body as every other closed reason")
}

// ── O3: POST /api/v1/providers/{id}/entitlement ─────────────────────────────

// newEntitlementAuthEnv is one configured, listable provider row pointed at a
// recording upstream, on an instance whose onboarding is INCOMPLETE — the
// fail-open state the old gate treated as "no authentication needed".
//
// The row's key resolves from an env var ref, matching the existing T067-11
// fixtures.
func newEntitlementAuthEnv(t *testing.T) (*restAPI, *entitlementStub) {
	t.Helper()
	const ref = "PREAUTH_ENTITLEMENT_KEY"
	t.Setenv(ref, "sk-operator-secret")
	stub := newEntitlementStub(t, "gpt-a", "gpt-b")
	api := newEntitlementAPI(t, entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false))
	require.False(t, api.onboardingMgr != nil && api.onboardingMgr.IsComplete(),
		"the fixture must NOT report onboarding complete — that is the state the "+
			"fail-open gate mistook for 'no admin exists yet, let anyone in'")
	return api, stub
}

func postEntitlementViaRealMux(
	t *testing.T, api *restAPI, sourceIP string, decorate func(*http.Request),
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/openai/entitlement", nil)
	if decorate != nil {
		decorate(req)
	}
	return serveViaRealMux(t, api, api.agentLoop.GetConfig(), req, sourceIP)
}

// TestEntitlement_RealMux_RefusesAnonymousCaller is the O3 reproduction.
// Before the fix, `onboardingDone` was false in this state, the 401 branch was
// skipped entirely, and an anonymous POST reached the upstream with the
// operator's own stored key.
//
// The correct posture is the one contracts/openapi.yaml has always declared
// for this operation — `security: BearerAuth`, 401 — with no FR-050 window at
// all. FR-050 exists so onboarding step 3 can reach a provider before an admin
// exists; "Check with my account" operates on a CONFIGURED provider row, and
// nothing is configured until POST /onboarding/complete writes config.json, so
// that premise never applied here.
func TestEntitlement_RealMux_RefusesAnonymousCaller(t *testing.T) {
	api, stub := newEntitlementAuthEnv(t)

	w := postEntitlementViaRealMux(t, api, "198.51.100.21", nil)

	require.Equal(t, http.StatusUnauthorized, w.Code,
		"an anonymous entitlement check must be refused regardless of onboarding state; body=%s",
		w.Body.String())
	assert.Equal(t, 0, stub.calls(),
		"and must not spend the operator's key upstream")
}

// TestEntitlement_RealMux_EnvBearerTokenOperatorStillWorks is the fresh-hazard
// direction for this route. The gate is requestPrincipalAuthenticated, not a
// bare UserContextKey lookup, precisely because the documented headless
// deployment mode (OMNIPUS_BEARER_TOKEN as the only credential, no configured
// users) puts nothing in the context — its operator is genuinely
// authenticated and a context-key check would 401 them.
func TestEntitlement_RealMux_EnvBearerTokenOperatorStillWorks(t *testing.T) {
	api, stub := newEntitlementAuthEnv(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "headless-operator-token")

	w := postEntitlementViaRealMux(t, api, "198.51.100.22", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer headless-operator-token")
	})

	require.Equal(t, http.StatusOK, w.Code,
		"the headless operator's own credential must still work; body=%s", w.Body.String())
	assert.Equal(t, 1, stub.calls(),
		"FR-021 allows exactly one listing call per uncached check")
}

// TestEntitlement_RealMux_SignedInOperatorStillWorks is the SPA direction: a
// resolved session identity in the request context, which is what
// withOptionalAuth installs for a logged-in browser.
func TestEntitlement_RealMux_SignedInOperatorStillWorks(t *testing.T) {
	api, stub := newEntitlementAuthEnv(t)

	w := postEntitlementViaRealMux(t, api, "198.51.100.23", func(r *http.Request) {
		*r = *r.WithContext(context.WithValue(
			r.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"}))
	})

	require.Equal(t, http.StatusOK, w.Code,
		"an authenticated operator must still be able to check entitlement; body=%s",
		w.Body.String())
	assert.Equal(t, 1, stub.calls())
}

// TestEntitlement_RealMux_RateLimited pins the ceiling the contract has
// declared since ADR-067 ("Rate-limited like /providers/{id}/test", plus a
// 429 response) and that no limiter ever enforced.
//
// The requests are AUTHENTICATED, which is not incidental: on this route the
// limiter deliberately runs AFTER the auth gate, so an anonymous flood is
// refused 401 and never consumes the bucket a real operator behind the same
// NAT address is drawing from. Driving this loop anonymously would 401
// forever and never reach the limiter at all — which is exactly the property
// being relied on, and the first version of this test proved it by failing.
func TestEntitlement_RealMux_RateLimited(t *testing.T) {
	api, stub := newEntitlementAuthEnv(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "headless-operator-token")
	const ip = "198.51.100.24"
	authed := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer headless-operator-token")
	}

	for i := range providerEntitlementLimiter.limit {
		w := postEntitlementViaRealMux(t, api, ip, authed)
		require.Equal(t, http.StatusOK, w.Code,
			"request %d must still be inside the window; body=%s", i+1, w.Body.String())
	}

	w := postEntitlementViaRealMux(t, api, ip, authed)
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"request %d must be refused by the limiter; body=%s",
		providerEntitlementLimiter.limit+1, w.Body.String())
	assert.NotEmpty(t, w.Header().Get("Retry-After"),
		"a 429 must tell the caller when to come back")
	assert.Equal(t, 1, stub.calls(),
		"FR-021's process cache must still hold: %d permitted checks, one upstream "+
			"listing call", providerEntitlementLimiter.limit)
}

// TestEntitlement_RealMux_AnonymousFloodDoesNotConsumeTheOperatorsBudget is
// the companion property, stated directly rather than left implicit in the
// test above. It is why this route's limiter sits behind its auth gate while
// /test's sits in front of one.
func TestEntitlement_RealMux_AnonymousFloodDoesNotConsumeTheOperatorsBudget(t *testing.T) {
	api, stub := newEntitlementAuthEnv(t)
	const ip = "198.51.100.25"

	for i := range providerEntitlementLimiter.limit + 5 {
		w := postEntitlementViaRealMux(t, api, ip, nil)
		require.Equal(t, http.StatusUnauthorized, w.Code,
			"anonymous request %d must be refused at the auth gate, never 429; body=%s",
			i+1, w.Body.String())
	}

	// The operator, from the same address, still has their full budget.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "headless-operator-token")
	w := postEntitlementViaRealMux(t, api, ip, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer headless-operator-token")
	})
	require.Equal(t, http.StatusOK, w.Code,
		"the anonymous flood must not have spent the authenticated operator's "+
			"per-IP budget; body=%s", w.Body.String())
	assert.Equal(t, 1, stub.calls())
}

// ── O5: PUT /providers/{id} and POST /providers/{id}/test ───────────────────

// TestProviderPUT_RealMux_RateLimitedAheadOfAllOtherWork pins both the
// existence of the ceiling and its POSITION. Each request below names a
// reserved path segment, which the branch answers 400 long before it does any
// work; the last one flipping to 429 is what proves the limiter runs ahead of
// even that check, and therefore ahead of the outbound ValidateKey call, the
// config.json rewrite and the synchronous full agent-registry rebuild that
// follow it.
func TestProviderPUT_RealMux_RateLimitedAheadOfAllOtherWork(t *testing.T) {
	api, _ := newEntitlementAuthEnv(t)
	const ip = "198.51.100.31"

	// An id past maxProviderIDLen. The branch answers 400 on it immediately,
	// before decoding the body or touching config.json — and, unlike a
	// reserved segment such as "catalog", it still DISPATCHES here (catalog
	// and default-model have their own registered routes that win the mux
	// match, so a PUT to either never reaches this branch at all).
	tooLongID := strings.Repeat("a", maxProviderIDLen+1)

	put := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/providers/"+tooLongID,
			strings.NewReader(`{"api_key":"sk-anonymous"}`))
		req.Header.Set("Content-Type", "application/json")
		return serveViaRealMux(t, api, api.agentLoop.GetConfig(), req, ip)
	}

	for i := range providerConfigWriteLimiter.limit {
		w := put()
		require.Equal(t, http.StatusBadRequest, w.Code,
			"request %d must reach the branch and be refused on its merits, not by the "+
				"limiter; body=%s", i+1, w.Body.String())
	}

	w := put()
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"request %d must be refused by the limiter BEFORE the id-validation check "+
			"that answered every request above; body=%s",
		providerConfigWriteLimiter.limit+1, w.Body.String())
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

// TestProviderPUT_RealMux_FreshInstallStillSucceeds is the other direction for
// O5. FR-050 keeps this route reachable with no credential while onboarding is
// incomplete so the wizard can configure a provider before an admin account
// exists; a limiter that refused the wizard's own first save would brick first
// run.
func TestProviderPUT_RealMux_FreshInstallStillSucceeds(t *testing.T) {
	api, stub := newEntitlementAuthEnv(t)

	body := fmt.Sprintf(`{"api_key":"sk-first-run","model":"gpt-a","api_base":%q}`, stub.srv.URL)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/providers/openai", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := serveViaRealMux(t, api, api.agentLoop.GetConfig(), req, "198.51.100.32")

	require.Equal(t, http.StatusOK, w.Code,
		"an anonymous first-run save must still succeed while the window is open; body=%s",
		w.Body.String())
}

// TestProviderTest_RealMux_RateLimitedAheadOfAllOtherWork is the same shape
// for POST /providers/{id}/test. Unlike the onboarding probe, which carries
// the caller's own key in the body, /test resolves the provider's STORED
// credential and spends one real upstream request with the OPERATOR's key —
// so an unbounded anonymous caller burns quota that is not theirs.
func TestProviderTest_RealMux_RateLimitedAheadOfAllOtherWork(t *testing.T) {
	api, stub := newEntitlementAuthEnv(t)
	const ip = "198.51.100.33"

	test := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/no-such-provider/test", nil)
		return serveViaRealMux(t, api, api.agentLoop.GetConfig(), req, ip)
	}

	for i := range providerTestLimiter.limit {
		w := test()
		require.Equal(t, http.StatusOK, w.Code,
			"request %d must reach the branch (an unconfigured id answers 200 "+
				"success=false), not the limiter; body=%s", i+1, w.Body.String())
	}

	w := test()
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"request %d must be refused by the limiter; body=%s",
		providerTestLimiter.limit+1, w.Body.String())
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
	assert.Equal(t, 0, stub.calls(),
		"an unconfigured provider must never have reached the upstream in the first place")
}

// TestProviderLimiters_DoNotShareABucket pins the deliberate separation. /test
// is reachable anonymously during the onboarding window and entitlement is
// not, so a shared bucket would let an anonymous /test flood exhaust an
// authenticated operator's entitlement budget from the same NAT address.
func TestProviderLimiters_DoNotShareABucket(t *testing.T) {
	limiters := map[string]*apiRateLimiter{
		"providerConfigWriteLimiter": providerConfigWriteLimiter,
		"providerTestLimiter":        providerTestLimiter,
		"providerEntitlementLimiter": providerEntitlementLimiter,
		"providerListAnonLimiter":    providerListAnonLimiter,
	}
	seen := map[*apiRateLimiter]string{}
	for name, l := range limiters {
		require.NotNil(t, l, "%s must exist", name)
		if other, dup := seen[l]; dup {
			t.Fatalf("%s and %s are the same limiter instance and therefore share one "+
				"per-IP budget", name, other)
		}
		seen[l] = name
		assert.Equal(t, 60, l.limit,
			"%s must use the dispatcher's established 60/min ceiling", name)
	}
}
