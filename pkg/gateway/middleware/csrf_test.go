//go:build !cgo

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
)

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

func TestCSRF_DefaultExempt(t *testing.T) {
	// Default exempt list (no options passed) includes the cookie-issuer
	// endpoints (onboarding, login) and operational health endpoints
	// (mounted on health-server mux; exempt here for defense-in-depth in
	// case of future remount).
	h := buildMW()
	for _, path := range []string{
		"/api/v1/onboarding/complete",
		"/api/v1/auth/login",
		"/health",
		"/ready",
		"/reload",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code, "exempt path %s must bypass CSRF gate", path)
			assert.Equal(t, "next-ran", rec.Body.String())
		})
	}
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
	// custom. Both the default /api/v1/auth/login AND the custom /extra are
	// exempt.
	h := buildMW(WithDefaultExempts(), WithExemptPath("/extra"))

	for _, p := range []string{
		"/api/v1/auth/login",
		"/api/v1/onboarding/complete",
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

func TestCSRF_NilOptionIgnored(t *testing.T) {
	// Passing a nil Option must be safely ignored, not panic. This lets
	// callers conditionally apply options via a ternary without branching.
	var noOpt Option
	h := buildMW(noOpt)

	// Default behavior still in effect: onboarding is exempt.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", nil)
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
