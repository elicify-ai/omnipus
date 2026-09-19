// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// withEdition pins config.Edition for the duration of the test, restoring the
// prior value on cleanup — the same save/restore pattern
// pkg/config/edition_test.go uses. config.EditionAuthMode() (and therefore
// defaultExemptPaths()) derives from this package-level var, so every mode
// composition test in this file must set it explicitly rather than rely on
// whatever the previous test left behind.
func withEdition(t *testing.T, e string) {
	t.Helper()
	prev := config.Edition
	config.Edition = e
	t.Cleanup(func() { config.Edition = prev })
}

// nextHandler is a trivial handler that records a success-marker in the
// response so tests can confirm whether the middleware let the request
// through.
var nextHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "next-ran")
})

// buildMW wraps nextHandler with CSRFMiddleware built from the given options.
func buildMW(opts ...Option) http.Handler {
	return CSRFMiddleware(opts...)(nextHandler)
}

func TestCSRF_SafeMethodsPassThrough(t *testing.T) {
	h := buildMW()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/agents", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code, "safe method %s must bypass CSRF gate", method)
			assert.Equal(t, "next-ran", rec.Body.String())
		})
	}
}

func TestCSRF_MissingCookie(t *testing.T) {
	h := buildMW()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader([]byte(`{}`)))
	// No cookie, no header.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "csrf cookie missing", body["error"])
	assert.NotContains(t, rec.Body.String(), "next-ran", "next handler must not run on rejected CSRF")
}

func TestCSRF_MissingHeader(t *testing.T) {
	h := buildMW()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc123"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "csrf header missing", body["error"])
}

func TestCSRF_Mismatch(t *testing.T) {
	var reportedRoute, reportedIP string
	var reportCalls int
	h := buildMW(
		WithReporter(func(r *http.Request, sourceIP, route string) {
			reportCalls++
			reportedIP = sourceIP
			reportedRoute = route
		}),
		WithClientIPFunc(func(r *http.Request) string { return "203.0.113.9" }),
	)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "correct-token"})
	req.Header.Set(CSRFHeaderName, "wrong-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "csrf token mismatch", body["error"])
	assert.Equal(t, 1, reportCalls, "reporter must fire exactly once on mismatch")
	assert.Equal(t, "203.0.113.9", reportedIP)
	assert.Equal(t, "/api/v1/config", reportedRoute)
}

func TestCSRF_MatchPassesThrough(t *testing.T) {
	h := buildMW()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "match-me"})
	req.Header.Set(CSRFHeaderName, "match-me")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "next-ran", rec.Body.String())
}

// TestCSRF_DefaultExempt pins the default exempt list (no options passed) for
// BOTH auth modes (ADR-0010 WP2): local mode (core edition) restores
// upstream's own bootstrap set — /api/v1/auth/login plus the two onboarding
// routes, since local mode runs onboarding BEFORE any session exists;
// platform mode (desktop, hosted) has NO auth route exempt by default — the
// registered sign-in provider declares its cookie-issuing start route and the
// gateway passes it in with WithExemptPaths at boot (auth_mode.go's
// csrfExemptPaths; pinned against this middleware by
// TestAuthMode_CsrfExemptPaths_MatchTheMiddlewareDefault), with onboarding
// deliberately NOT exempt (TestCSRF_OnboardingCompleteIsNotExempt and
// TestCSRF_ProbeProviderIsNotExempt pin that negative separately). Both
// modes carry the operational health endpoints (mounted on health-server
// mux; exempt here for defense-in-depth in case of future remount).
func TestCSRF_DefaultExempt(t *testing.T) {
	cases := map[string]struct {
		edition string
		exempt  []string
	}{
		"local mode (core edition)": {
			edition: config.EditionCore,
			exempt: []string{
				"/api/v1/onboarding/complete",
				"/api/v1/onboarding/probe-provider",
				"/api/v1/auth/login",
				"/health",
				"/ready",
				"/reload",
			},
		},
		"platform mode (hosted edition)": {
			edition: config.EditionHosted,
			exempt: []string{
				"/health",
				"/ready",
				"/reload",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withEdition(t, tc.edition)
			h := buildMW()
			// The sign-in start route is the provider's to declare, never a
			// middleware default — in either mode it is gated until the
			// gateway passes it in with WithExemptPaths.
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/platform/start", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusForbidden, rec.Code, "provider start route must not be a middleware default")
			for _, path := range tc.exempt {
				t.Run(path, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)

					assert.Equal(t, http.StatusOK, rec.Code, "exempt path %s must bypass CSRF gate", path)
					assert.Equal(t, "next-ran", rec.Body.String())
				})
			}
		})
	}
}

// TestCSRF_ProbeProviderIsNotExempt pins the removal of
// /api/v1/onboarding/probe-provider from the default exempt set.
//
// It was exempt on the rationale "called on fresh install, no cookie exists".
// Under ADR-0008 the probe is a withAuth route reached only from inside the
// wizard, so the caller always holds the session cookie sign-in issued — and
// it issues no CSRF cookie of its own, so it never met this set's invariant
// either. While the exemption stood, an attacker page could make the victim's
// browser POST a probe with the session cookie and no CSRF token, driving an
// outbound request from the gateway to an attacker-chosen api_base.
//
// This is a platform-mode-only property (local mode's onboarding runs
// pre-auth and DOES exempt this route — TestCSRF_DefaultExempt's local-mode
// case pins that), so mode is pinned explicitly here rather than relying on
// whatever a previous test left in config.Edition.
func TestCSRF_ProbeProviderIsNotExempt(t *testing.T) {
	withEdition(t, config.EditionHosted)
	h := buildMW()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
		bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"the provider probe must be CSRF-gated like any other state-changing route")
	assert.NotEqual(t, "next-ran", rec.Body.String(),
		"the handler must not be reached without a CSRF cookie and header")
}

func TestCSRF_WithExemptPath_DropsDefaults(t *testing.T) {
	// When a caller supplies WithExemptPath without WithDefaultExempts, the
	// default set is intentionally dropped. The onboarding endpoint is no
	// longer exempt; it needs cookie+header.
	h := buildMW(WithExemptPath("/custom"))

	// Onboarding is now gated.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"WithExemptPath without WithDefaultExempts must drop the default set")

	// Custom exempt path passes through.
	req = httptest.NewRequest(http.MethodPost, "/custom", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCSRF_WithExemptPaths_Multiple(t *testing.T) {
	// WithExemptPaths(...) is a bulk variant equivalent to calling
	// WithExemptPath once per arg.
	h := buildMW(WithExemptPaths("/one", "/two", "/three"))

	for _, p := range []string{"/one", "/two", "/three"} {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "path %s must be exempt", p)
	}

	// A path not in the custom set is still gated.
	req := httptest.NewRequest(http.MethodPost, "/four", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRF_WithDefaultExempts_AndCustom(t *testing.T) {
	// Combining WithDefaultExempts with WithExemptPath yields defaults UNION
	// custom. Both the default /health AND the custom /extra are exempt.
	// Pinned to platform mode — TestCSRF_DefaultExempt already covers both
	// modes' defaults on their own; this test's subject is the UNION
	// behavior, not re-deriving the mode table. /extra stands in for the
	// provider start route the gateway passes in the same way at boot.
	withEdition(t, config.EditionHosted)
	h := buildMW(WithDefaultExempts(), WithExemptPath("/extra"))

	for _, p := range []string{
		"/health",
		"/extra",
	} {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "path %s must be exempt", p)
	}
}

func TestCSRF_OptionsDeepCopy_NoPostConstructionMutation(t *testing.T) {
	// Build a slice, hand it to the constructor, then mutate the slice. The
	// middleware's behavior must be unaffected — the constructor must deep-copy
	// the paths into its private map.
	paths := make([]string, 0, 3)
	paths = append(paths, "/a", "/b")
	h := buildMW(WithExemptPaths(paths...))

	// Mutate the caller's slice AFTER construction.
	paths[0] = "/hijacked"
	paths = append(paths, "/c")
	_ = paths

	// /a must still be exempt; /hijacked must still be gated.
	req := httptest.NewRequest(http.MethodPost, "/a", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "original exempt path must survive caller mutation")

	req = httptest.NewRequest(http.MethodPost, "/hijacked", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code, "post-construction slice mutation must not leak into middleware")
}

func TestCSRF_WithReporter_NilIsSafe(t *testing.T) {
	// Passing a nil reporter option must not crash; the middleware simply
	// rejects mismatches without invoking a callback.
	h := buildMW(WithReporter(nil))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/abc", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "a"})
	req.Header.Set(CSRFHeaderName, "b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRF_NoReporterOnMismatchIsSafe(t *testing.T) {
	// With no WithReporter option at all, a mismatch still returns 403 and
	// doesn't panic.
	h := buildMW()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/abc", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "a"})
	req.Header.Set(CSRFHeaderName, "b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRF_WithClientIPFunc_FallbackRemoteAddr(t *testing.T) {
	// When no WithClientIPFunc is supplied, the mismatch reporter gets
	// r.RemoteAddr as the source IP.
	var seenIP string
	h := buildMW(WithReporter(func(r *http.Request, sourceIP, route string) {
		seenIP = sourceIP
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", nil)
	req.RemoteAddr = "192.0.2.7:51234"
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "x"})
	req.Header.Set(CSRFHeaderName, "y")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "192.0.2.7:51234", seenIP, "fallback must be r.RemoteAddr")
}

// TestCSRF_OnboardingCompleteIsNotExempt pins the removal of an exemption that
// had outlived its reason.
//
// It was exempt because it "issues the cookie so the SPA's first post-onboarding
// request can pass the gate". Under ADR-0008 it issues no cookie — the session
// and __Host-csrf are minted at platform sign-in, which happens BEFORE the
// wizard — and the route is withAuth. So the exemption bought nothing and cost
// this: an attacker page could make a signed-in victim's browser complete their
// onboarding with an attacker-chosen provider and API key, writing that key into
// the victim's encrypted credential store.
//
// Platform-mode-only, like TestCSRF_ProbeProviderIsNotExempt — local mode's
// onboarding runs pre-auth and DOES exempt this route.
func TestCSRF_OnboardingCompleteIsNotExempt(t *testing.T) {
	withEdition(t, config.EditionHosted)
	h := buildMW()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"a state-changing POST with no CSRF cookie or header must be refused")
}

func TestCSRF_NilOptionIgnored(t *testing.T) {
	// Passing a nil Option must be safely ignored, not panic. This lets
	// callers conditionally apply options via a ternary without branching.
	// Mode pinned to platform for the same reason as the tests above — the
	// subject here is nil-Option handling, not the mode table.
	withEdition(t, config.EditionHosted)
	var noOpt Option
	h := buildMW(noOpt)

	// Default behavior still in effect: the operational health route is exempt.
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestIssueCSRFCookie_Attributes(t *testing.T) {
	rec := httptest.NewRecorder()
	// Use a request with TLS set to exercise the secure-cookie path (__Host-csrf).
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.TLS = &tls.ConnectionState{} // non-nil TLS triggers the __Host- cookie branch
	require.NoError(t, IssueCSRFCookie(rec, req))

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "exactly one cookie must be set")
	c := cookies[0]

	assert.Equal(t, CSRFCookieName, c.Name, "cookie must be __Host-csrf")
	assert.Equal(t, "/", c.Path, "__Host- requires Path=/")
	assert.Empty(t, c.Domain, "__Host- requires no Domain attribute")
	assert.True(t, c.Secure, "__Host- requires Secure")
	assert.False(t, c.HttpOnly, "SPA must be able to read the cookie via document.cookie")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite, "must be SameSite=Strict for CSRF protection")
	// 32 random bytes base64-url-encoded = 43 chars (no padding).
	assert.Len(t, c.Value, 43, "token must be 32 bytes base64-url-encoded without padding")
}

func TestIssueCSRFCookie_TokenIsUnique(t *testing.T) {
	// Sanity check: two successive calls produce distinct tokens. Not a
	// real entropy test (that belongs in a fuzz run), but it catches the
	// common bug of accidentally returning a constant.
	seen := map[string]bool{}
	tlsReq := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	for i := 0; i < 16; i++ {
		rec := httptest.NewRecorder()
		require.NoError(t, IssueCSRFCookie(rec, tlsReq))
		c := rec.Result().Cookies()[0]
		assert.False(t, seen[c.Value], "token collision on iteration %d: %q", i, c.Value)
		seen[c.Value] = true
	}
}

func TestIssueCSRFCookie_HeaderIsParseable(t *testing.T) {
	rec := httptest.NewRecorder()
	tlsReq := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	require.NoError(t, IssueCSRFCookie(rec, tlsReq))
	setCookie := rec.Header().Get("Set-Cookie")
	require.NotEmpty(t, setCookie)

	// Must include all required attributes literally.
	assert.True(t, strings.HasPrefix(setCookie, CSRFCookieName+"="),
		"Set-Cookie must start with __Host-csrf=")
	assert.Contains(t, setCookie, "Path=/")
	assert.Contains(t, setCookie, "Secure")
	assert.Contains(t, setCookie, "SameSite=Strict")
	assert.NotContains(t, setCookie, "HttpOnly",
		"HttpOnly must not be set — SPA needs to read the cookie")
	assert.NotContains(t, setCookie, "Domain=",
		"__Host- prefix forbids Domain attribute")
}

func TestCSRF_ErrorBody_JSONEncoded(t *testing.T) {
	// The error body is JSON-encoded via encoding/json (not fmt.Fprintf),
	// so Content-Type is application/json and the payload is a valid JSON
	// object with an "error" field.
	h := buildMW()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "csrf cookie missing", body["error"])
}

// --- Bug 3 coverage: Bearer bypass + plain-HTTP cookie downgrade ---

// TestCSRFMiddleware_BearerBypass verifies that a state-changing request
// carrying an Authorization: Bearer header skips the double-submit check
// entirely. Browsers cannot auto-send an Authorization header cross-origin,
// so Bearer-authenticated callers are not a CSRF target — requiring them
// to juggle the cookie is pure friction and breaks plain-HTTP deployments
// where the Secure __Host-csrf cookie cannot install.
func TestCSRFMiddleware_BearerBypass(t *testing.T) {
	reached := false
	h := CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/doctor", nil)
	req.Header.Set("Authorization", "Bearer sk-test-token")
	// Intentionally no cookie, no X-Csrf-Token header — the whole point is
	// that Bearer callers don't need them.

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"Bearer-authenticated state-changing request must pass through without a CSRF cookie")
	assert.True(t, reached, "the inner handler must actually run")
}

// TestCSRFMiddleware_BearerMustHavePrefix confirms that only the "Bearer "
// prefix triggers the bypass — a stray Authorization: Basic or malformed
// header still goes through the normal CSRF check.
func TestCSRFMiddleware_BearerMustHavePrefix(t *testing.T) {
	h := CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must NOT run when Bearer prefix is absent")
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/doctor", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"non-Bearer Authorization must fall through to the cookie check and 403 without one")
}

// TestCSRFMiddleware_PlainHTTPCookieAccepted verifies that the middleware
// accepts the un-prefixed `csrf` cookie issued over plain HTTP, not only
// the TLS-only __Host-csrf cookie. The two flavors are interchangeable
// as far as the gate is concerned — the gate cares about "cookie value
// matches header value", not which name carries the value.
func TestCSRFMiddleware_PlainHTTPCookieAccepted(t *testing.T) {
	reached := false
	h := CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	const token = "plain-http-token-value"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/doctor", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieNameHTTP, Value: token})
	req.Header.Set(CSRFHeaderName, token)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"middleware must accept the plain-HTTP `csrf` cookie when it matches the header")
	assert.True(t, reached, "the inner handler must actually run")
}

// TestIssueCSRFCookie_PlainHTTPUsesFallbackName verifies that when the
// request arrives without TLS (r.TLS == nil and no X-Forwarded-Proto=https),
// IssueCSRFCookie emits the un-prefixed `csrf` cookie with Secure=false so
// the browser will actually store it on an HTTP origin.
func TestIssueCSRFCookie_PlainHTTPUsesFallbackName(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/onboarding/complete", nil)
	// req.TLS is nil because it's an http:// URL.
	rec := httptest.NewRecorder()
	require.NoError(t, IssueCSRFCookie(rec, req))

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, CSRFCookieNameHTTP, c.Name,
		"on plain HTTP the fallback `csrf` cookie must be issued instead of __Host-csrf")
	assert.False(t, c.Secure,
		"fallback cookie must have Secure=false so the browser actually stores it on HTTP")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite,
		"SameSite=Strict must survive the HTTP downgrade — it's the real CSRF defense")
	assert.Equal(t, "/", c.Path)
}

// TestIssueCSRFCookie_HTTPSUsesHostPrefix verifies the secure branch still
// emits the __Host-csrf cookie with Secure=true when r.TLS is non-nil.
func TestIssueCSRFCookie_HTTPSUsesHostPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/onboarding/complete", nil)
	req.TLS = &tls.ConnectionState{} // simulate TLS-connected request
	rec := httptest.NewRecorder()
	require.NoError(t, IssueCSRFCookie(rec, req))

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, CSRFCookieName, c.Name, "HTTPS branch must still emit __Host-csrf")
	assert.True(t, c.Secure, "__Host- prefix requires Secure=true")
}

// TestIssueCSRFCookie_ForwardedProtoHonored — a reverse proxy terminating
// TLS and forwarding X-Forwarded-Proto=https must route to the secure
// branch, so the cookie survives the Strict-Transport-Security dance.
func TestIssueCSRFCookie_ForwardedProtoHonored(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/onboarding/complete", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	require.NoError(t, IssueCSRFCookie(rec, req))

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, CSRFCookieName, cookies[0].Name,
		"X-Forwarded-Proto=https from a terminating proxy must pick the __Host- branch")
}

// --- preview-on-main-listener (v5 spec, FR-012 + FR-019) coverage ---

// TestIssueCSRFCookie_MaxAge verifies FR-019: both cookie flavors (secure
// __Host-csrf and the plain-HTTP fallback) carry MaxAge=86400 (24h),
// matching the session cookie's lifetime (SessionCookieMaxAge). Before this
// change the cookie had no MaxAge at all and died on browser close, which
// is shorter than the 24h session — the CSRF cookie could expire out from
// under a still-valid session.
func TestIssueCSRFCookie_MaxAge(t *testing.T) {
	t.Run("secure __Host-csrf", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		req.TLS = &tls.ConnectionState{}
		rec := httptest.NewRecorder()
		require.NoError(t, IssueCSRFCookie(rec, req))

		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(
			t,
			SessionCookieMaxAge,
			cookies[0].MaxAge,
			"MaxAge must be 86400 (24h), matching the session cookie",
		)
	})

	t.Run("plain-HTTP fallback csrf", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		rec := httptest.NewRecorder()
		require.NoError(t, IssueCSRFCookie(rec, req))

		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, SessionCookieMaxAge, cookies[0].MaxAge, "fallback cookie MaxAge must also be 86400 (24h)")
	})
}

// TestCSRF_PreviewPrefixExempt_AllMethods verifies FR-012 / US-7: any method
// to a tokenized /preview/<agent>/<token>/... path passes through the CSRF
// gate WITHOUT a cookie or header, because an exact-path exemption can
// never match a tokenized URL.
func TestCSRF_PreviewPrefixExempt_AllMethods(t *testing.T) {
	h := buildMW()
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/preview/my-agent/tok123abc/x", bytes.NewReader([]byte(`{}`)))
			// Deliberately no CSRF cookie, no X-Csrf-Token header.
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code,
				"%s /preview/<token>/... must bypass CSRF without a cookie/header", method)
			assert.Equal(t, "next-ran", rec.Body.String())
		})
	}
}

// TestCSRF_PreviewPrefixDoesNotExemptAPI verifies the /preview/ prefix
// exemption is scoped exactly — it must never bleed into /api/v1/*, which
// keeps full CSRF enforcement (FR-012 non-behavior: "MUST NOT weaken
// CSRF/origin on /api/v1/*").
func TestCSRF_PreviewPrefixDoesNotExemptAPI(t *testing.T) {
	h := buildMW()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/foo", bytes.NewReader([]byte(`{}`)))
	// No cookie, no header.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "/api/v1/* must still require CSRF")
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "csrf cookie missing", body["error"])
}

// TestCSRF_SafeMethodRemintsWhenCookieMissing verifies FR-019: a GET request
// with no CSRF cookie causes the middleware to mint one via IssueCSRFCookie,
// with MaxAge=86400. This closes the "returning user" lockout: a browser
// reopened after the CSRF cookie expired (or a fresh profile) can never
// otherwise acquire a first cookie without an explicit issuer endpoint.
func TestCSRF_SafeMethodRemintsWhenCookieMissing(t *testing.T) {
	h := buildMW()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	// No X-Forwarded-Proto / TLS → plain-HTTP fallback cookie flavor.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "a GET lacking the CSRF cookie must get one re-minted")
	assert.Equal(t, CSRFCookieNameHTTP, cookies[0].Name)
	assert.Equal(t, SessionCookieMaxAge, cookies[0].MaxAge, "re-minted cookie must carry MaxAge=86400")
	assert.NotEmpty(t, cookies[0].Value)
}

// TestCSRF_SafeMethodDoesNotRemintWhenCookiePresent verifies the re-mint
// only fires when the cookie is ACTUALLY missing — a safe request that
// already carries a valid cookie must not get a second, different one
// minted underneath it (that would needlessly invalidate any header the
// SPA already cached for this cookie value).
func TestCSRF_SafeMethodDoesNotRemintWhenCookiePresent(t *testing.T) {
	h := buildMW()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieNameHTTP, Value: "existing-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Result().Cookies(), "must not re-mint when a CSRF cookie is already present")
}

// TestCSRF_StateChangingMethodDoesNotRemint verifies FR-019's negative case:
// a state-changing request (POST) that lacks the CSRF cookie must NOT get
// one re-minted — it must still 403. Re-minting on a state-changing method
// would let a request silently acquire a fresh token to pass its own
// check, defeating the double-submit invariant.
func TestCSRF_StateChangingMethodDoesNotRemint(t *testing.T) {
	h := buildMW()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader([]byte(`{}`)))
	// No cookie, no header.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "POST lacking the CSRF cookie must still 403")
	assert.Empty(t, rec.Result().Cookies(), "a rejected state-changing request must not have a cookie re-minted")
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "csrf cookie missing", body["error"])
}

// TestCSRFMiddleware_BearerBypass_StillIntactAfterPreviewChanges re-asserts
// (alongside the pre-existing TestCSRFMiddleware_BearerBypass) that the
// Authorization: Bearer skip path is unaffected by the /preview/ prefix
// exemption and safe-method re-mint added by this change — a Bearer-
// authenticated state-changing request to a NON-preview, NON-exempt path
// still bypasses the cookie/header check entirely, and (being a
// state-changing method) must not have anything re-minted either.
func TestCSRFMiddleware_BearerBypass_StillIntactAfterPreviewChanges(t *testing.T) {
	reached := false
	h := CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/abc", nil)
	req.Header.Set("Authorization", "Bearer sk-test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "Bearer bypass must still work after the preview/re-mint changes")
	assert.True(t, reached)
	assert.Empty(t, rec.Result().Cookies(), "Bearer path is state-changing; it must not get a cookie re-minted")
}

// --- CR-2 coverage (7-reviewer gate, pass 2): anonymous pre-login re-mint ---

// TestCSRF_SafeMethodRemint_AnonymousPreLogin_NoSessionRequired verifies
// CR-2 / FR-019: an anonymous, PRE-LOGIN safe-method GET — no
// __Host-csrf/csrf cookie, no omnipus-session cookie, no Authorization
// header, nothing at all identifying the caller — still gets a fresh,
// server-random CSRF cookie minted in the response. This is distinct from
// TestCSRF_SafeMethodRemintsWhenCookieMissing (which only proves the
// re-mint fires and carries the right MaxAge): this test additionally
// proves (1) the minted value is genuinely random, not a fixed/hardcoded
// placeholder — two independent anonymous requests must mint two DIFFERENT
// tokens — and (2) the re-mint never sets or otherwise depends on a session
// cookie: CSRFMiddleware has no session store dependency at all, so an
// entirely unauthenticated visitor can acquire a CSRF cookie before ever
// logging in, without that act establishing a session.
func TestCSRF_SafeMethodRemint_AnonymousPreLogin_NoSessionRequired(t *testing.T) {
	h := buildMW()

	// A brand-new, anonymous browser hitting the SPA for the very first
	// time: zero cookies, zero auth headers.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "an anonymous safe-method GET must never be rejected")
	assert.Equal(t, "next-ran", rec.Body.String(), "the request must reach the next handler unblocked")

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "exactly one cookie (the re-minted CSRF cookie) must be set")
	minted := cookies[0]

	assert.Contains(t, []string{CSRFCookieName, CSRFCookieNameHTTP}, minted.Name,
		"minted cookie must be one of the two CSRF cookie flavors")
	assert.NotEmpty(t, minted.Value, "minted token must be non-empty (server-random), not a placeholder")
	assert.Len(t, minted.Value, 43,
		"minted token must be the real 32-byte random value base64url-encoded, not a stub/constant")

	// Differentiation: a SECOND, independent anonymous request must mint a
	// DIFFERENT token. A hardcoded "random" value would make this fail while
	// still passing every assertion above.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	cookies2 := rec2.Result().Cookies()
	require.Len(t, cookies2, 1)
	assert.NotEqual(t, minted.Value, cookies2[0].Value,
		"two independent anonymous requests must mint two distinct tokens, proving the value is "+
			"actually server-random and not a fixed constant")

	// No session-establishing cookie of any kind is set by this middleware.
	// CSRFMiddleware has no dependency on (and no knowledge of) session
	// state — the re-mint is purely "does this safe request carry a CSRF
	// cookie", independent of whether the caller is logged in.
	for _, c := range cookies {
		assert.NotContains(t, strings.ToLower(c.Name), "session",
			"the CSRF re-mint must never set a session cookie — minting a CSRF token is not a "+
				"login/session-establishing action")
	}
}
