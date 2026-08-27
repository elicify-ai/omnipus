// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — proof tests for Track B's chat-served-iframe-preview
// changes (chat-served-iframe-preview-spec.md). These prove the new
// behavior works end-to-end. Comprehensive coverage (replay, malformed
// path, scheme mismatch) belongs to qa-lead.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestBuildWorkspaceCSP_FrameAncestorsFromMainOrigin verifies FR-007c:
// when a real main origin is provided, frame-ancestors uses it.
func TestBuildWorkspaceCSP_FrameAncestorsFromMainOrigin(t *testing.T) {
	csp := buildWorkspaceCSP("http://1.2.3.4:5000")
	assert.Contains(t, csp, "frame-ancestors http://1.2.3.4:5000",
		"frame-ancestors must use the provided main origin (FR-007c)")
	assert.Contains(t, csp, "connect-src 'self'",
		"connect-src must be 'self' so hydrated SPAs can fetch own data (FR-007c)")
	assert.Contains(t, csp, "form-action 'self'",
		"form-action must be 'self' to support dev-iframe POSTs (FR-023a)")
	assert.NotContains(t, csp, "frame-ancestors 'none'",
		"old 'none' value must be replaced (CR-01)")
	assert.NotContains(t, csp, "connect-src 'none'",
		"old 'none' value must be replaced (CR-01)")
}

// TestBuildWorkspaceCSP_FrameAncestorsFallback verifies FR-007e:
// when no main origin is available (host=0.0.0.0, no public_url),
// frame-ancestors falls back to '*'. The WARN is emitted once at boot
// (in setupAndStartServices) rather than lazily here — F-8 fix.
func TestBuildWorkspaceCSP_FrameAncestorsFallback(t *testing.T) {
	csp := buildWorkspaceCSP("")
	assert.Contains(t, csp, "frame-ancestors *",
		"empty main origin must fall back to '*' (FR-007e)")
}

// TestSanitisePreviewPath_RedactsToken verifies the audit-payload path
// sanitisation strips the bearer token from logged paths (FR-024 schema).
func TestSanitisePreviewPath_RedactsToken(t *testing.T) {
	cases := []struct {
		name string
		path string
		tok  string
		want string
	}{
		{
			name: "serve path with token",
			path: "/serve/agent-1/abc123token/index.html",
			tok:  "abc123token",
			want: "/serve/agent-1/<redacted>/index.html",
		},
		{
			name: "dev path with token",
			path: "/dev/agent-2/xyz789tok/api/login",
			tok:  "xyz789tok",
			want: "/dev/agent-2/<redacted>/api/login",
		},
		{
			name: "serve path with empty token uses positional redaction",
			path: "/serve/agent-3/sometoken/file.css",
			tok:  "",
			want: "/serve/agent-3/<redacted>/file.css",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitisePreviewPath(tc.path, tc.tok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMarkFirstServed_OnlyTrueOnce verifies FR-024: the first call per
// token returns true, subsequent calls return false. This drives the
// per-token serve.served / dev.proxied audit emission cap.
func TestMarkFirstServed_OnlyTrueOnce(t *testing.T) {
	a := &restAPI{}
	// markFirstServed records into a process-global set; under -count=N the
	// same t.Name() runs N times and would see the prior iteration's entry.
	// Drop the entry on cleanup so each iteration starts fresh.
	token := "test-token-mark-first-served-" + t.Name()
	t.Cleanup(func() {
		firstServedMu.Lock()
		delete(firstServedTokens, token)
		firstServedMu.Unlock()
	})

	first := a.markFirstServed(token)
	second := a.markFirstServed(token)
	third := a.markFirstServed(token)

	assert.True(t, first, "first markFirstServed call must return true")
	assert.False(t, second, "second call on the same token must return false (FR-024)")
	assert.False(t, third, "third call on the same token must return false (FR-024)")
}

// TestMarkFirstServed_EmptyTokenIsNoOp ensures the empty-token guard
// doesn't accidentally pollute the global set.
func TestMarkFirstServed_EmptyTokenIsNoOp(t *testing.T) {
	a := &restAPI{}
	assert.False(t, a.markFirstServed(""), "empty token must return false (no audit emit)")
	assert.False(t, a.markFirstServed(""), "empty token always returns false")
}

// TestCanonicalRemoteIP_PrefersForwardedFor verifies the FR-024 audit
// payload remote_ip canonicalisation. X-Forwarded-For is respected only
// when trustXFF=true (F-14 fix); otherwise r.RemoteAddr is used.
func TestCanonicalRemoteIP_PrefersForwardedFor(t *testing.T) {
	cases := []struct {
		name     string
		xff      string
		ra       string
		trustXFF bool
		want     string
	}{
		// trustXFF=true: XFF header is used.
		{
			name:     "trustXFF=true single XFF",
			xff:      "203.0.113.1",
			ra:       "10.0.0.1:1234",
			trustXFF: true,
			want:     "203.0.113.1",
		},
		{
			// M4: the RIGHTMOST entry is the one the trusted proxy wrote
			// itself. The documented nginx idiom
			// (X-Forwarded-For $proxy_add_x_forwarded_for) APPENDS the peer
			// to whatever the client sent, so this header is what a client
			// claiming to be 203.0.113.1 produces when its real address is
			// 10.0.0.5 — reading the leftmost entry read the client's own
			// string and let it forge its audit remote_ip and its
			// rate-limit bucket at will.
			name:     "trustXFF=true multi-hop uses the proxy-appended hop, not the client's claim",
			xff:      "203.0.113.1, 10.0.0.5",
			ra:       "10.0.0.1:1234",
			trustXFF: true,
			want:     "10.0.0.5",
		},
		{name: "trustXFF=true no XFF strips port", xff: "", ra: "10.0.0.1:1234", trustXFF: true, want: "10.0.0.1"},
		{name: "trustXFF=true no XFF no port", xff: "", ra: "10.0.0.1", trustXFF: true, want: "10.0.0.1"},
		{
			name:     "trustXFF=true XFF whitespace trimmed",
			xff:      " 203.0.113.1 ",
			ra:       "10.0.0.1:1234",
			trustXFF: true,
			want:     "203.0.113.1",
		},
		// trustXFF=false (default): XFF header is ignored — protects bare-IP deployments.
		{
			name:     "trustXFF=false ignores XFF",
			xff:      "203.0.113.1",
			ra:       "10.0.0.1:1234",
			trustXFF: false,
			want:     "10.0.0.1",
		},
		{name: "trustXFF=false strips port", xff: "", ra: "10.0.0.2:5678", trustXFF: false, want: "10.0.0.2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/serve/x/y/", nil)
			req.RemoteAddr = tc.ra
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			got := canonicalRemoteIP(req, tc.trustXFF)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestWebServeTool_StaticResultIncludesPath verifies FR-008: the web_serve
// tool (static mode) result JSON contains path, url, expires_at, and kind
// fields. Rewritten from the deleted TestServeWorkspaceTool_ResultIncludesPath
// to use the unified NewWebServeTool constructor.
//
// ADR-044 (preview-on-main-listener v5): NewWebServeTool's third parameter is
// now a LIVE `getConfig func() *config.Config` accessor (not a boot-frozen
// base-URL string) — the tool builds its URL from
// middleware.CanonicalGatewayOrigin(cfg) on every call and reads
// gateway.preview_enabled live (FR-005/FR-006).
func TestWebServeTool_StaticResultIncludesPath(t *testing.T) {
	dir := t.TempDir()
	ss := agent.NewServedSubdirs()
	t.Cleanup(ss.Stop)

	cfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 5001}}

	tool := tools.NewWebServeTool(
		dir,                                  // workspace
		"fr008-agent",                        // agentID
		func() *config.Config { return cfg }, // getConfig — live accessor (ADR-044)
		ss,                                   // served
		nil,                                  // devReg — not needed for static mode
		tools.WebServeDevConfig{
			PortRange:     [2]int32{18000, 18999},
			MaxConcurrent: 2,
		},
		nil, // egressProxy
		nil, // auditLogger
		60,
		86400,
	)

	ctx := tools.WithAgentID(context.Background(), "fr008-agent")
	result := tool.Execute(ctx, map[string]any{
		"path": ".",
	})

	require.False(t, result.IsError, "Execute must succeed: %s", result.ForLLM)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed),
		"FR-008: tool result must be valid JSON")

	assert.Contains(t, parsed, "kind", "FR-008: result must include kind field")
	assert.Contains(t, parsed, "path", "FR-008: result must include path field")
	assert.Contains(t, parsed, "url", "FR-008: result must preserve url field for replay")
	assert.Contains(t, parsed, "expires_at", "FR-008: result must include expires_at")

	assert.Equal(t, "static", parsed["kind"], "static mode must return kind=static")

	pathVal, _ := parsed["path"].(string)
	assert.True(t, strings.HasPrefix(pathVal, "/preview/"),
		"FR-008: path must start with /preview/, got %q", pathVal)

	urlVal, _ := parsed["url"].(string)
	assert.True(t, strings.HasSuffix(urlVal, pathVal),
		"FR-008: url must end with path (replay-safe reconstruction), url=%q path=%q", urlVal, pathVal)
}

// ---------------------------------------------------------------------------
// FR-013 (ADR-044) — proxy request-cookie strip + response Set-Cookie neutralize
// ---------------------------------------------------------------------------

// TestPreviewProxyStripsRequestCookies verifies FR-013 / S15: the reverse
// proxy strips the operator's ENTIRE Cookie header (not just Authorization)
// before forwarding to the dev server. A previewed app has no legitimate
// need for the gateway's session/CSRF cookies — forwarding them would let a
// compromised dev dependency read them (the read-vector half of the
// session-riding threat the spec's Non-Behaviors section documents as an
// accepted residual only for a directly-opened preview TAB, not the proxy).
func TestPreviewProxyStripsRequestCookies(t *testing.T) {
	var gotCookie, gotAuth string
	var sawCookieHeader bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotAuth = r.Header.Get("Authorization")
		_, sawCookieHeader = r.Header["Cookie"]
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	api := newPreviewTestAPI(t)
	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg

	devReg, err := reg.Register("cookie-strip-agent", upstreamPort(t, upstream.URL), 0 /*pid*/, "npm run dev", 10)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/preview/cookie-strip-agent/"+devReg.Token+"/api/data", nil)
	r.Header.Set("Cookie", "omnipus-session=abc123; csrf=def456; other=value")
	r.Header.Set("Authorization", "Bearer secret-token")
	api.HandlePreview(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.False(t, sawCookieHeader, "FR-013: forwarded request must carry NO Cookie header at all")
	assert.Empty(t, gotCookie, "FR-013: forwarded request must carry no Cookie value")
	assert.Empty(t, gotAuth, "FR-013: forwarded request must carry no Authorization header")
}

// TestPreviewProxyNeutralizesSetCookie verifies FR-013 / S15: a dev-server
// Set-Cookie for a reserved gateway cookie name (omnipus-session, csrf,
// __Host-csrf) is dropped from the response before it reaches the browser —
// a previewed app cannot plant/overwrite the operator's session or CSRF
// cookie (anti-fixation). A previewed app's OWN cookie passes through
// untouched (differentiation — this isn't a blanket Set-Cookie strip).
func TestPreviewProxyNeutralizesSetCookie(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "omnipus-session", Value: "attacker-value", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "attacker-csrf", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "__Host-csrf", Value: "attacker-host-csrf", Path: "/", Secure: true})
		http.SetCookie(w, &http.Cookie{Name: "my-app-cookie", Value: "legit-value", Path: "/"})
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	api := newPreviewTestAPI(t)
	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg

	devReg, err := reg.Register("set-cookie-agent", upstreamPort(t, upstream.URL), 0 /*pid*/, "npm run dev", 10)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/preview/set-cookie-agent/"+devReg.Token+"/", nil)
	api.HandlePreview(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	setCookies := w.Result().Cookies()
	byName := make(map[string]string, len(setCookies))
	for _, c := range setCookies {
		byName[c.Name] = c.Value
	}
	assert.NotContains(t, byName, "omnipus-session",
		"FR-013: reserved cookie omnipus-session must be dropped from the browser response")
	assert.NotContains(t, byName, "csrf",
		"FR-013: reserved cookie csrf must be dropped from the browser response")
	assert.NotContains(t, byName, "__Host-csrf",
		"FR-013: reserved cookie __Host-csrf must be dropped from the browser response")
	assert.Equal(t, "legit-value", byName["my-app-cookie"],
		"differentiation: the previewed app's own cookie must pass through untouched")
}

// TestNeutralizeReservedSetCookies_MalformedValueStillStripped is the regression
// for the silent-failure-hunter CRITICAL: a previewed app that emits a
// reserved-name Set-Cookie whose VALUE contains a byte net/http's strict RFC 6265
// parser rejects (so resp.Cookies() silently DROPS the line) must STILL have that
// line stripped. The old resp.Cookies()-based implementation left it in the raw
// header — invisible to Cookies() but stored by more-lenient real browsers,
// clobbering the operator's real omnipus-session.
func TestNeutralizeReservedSetCookies_MalformedValueStillStripped(t *testing.T) {
	// Reserved cookies whose values carry a raw backslash (0x5C) and bytes above
	// 0x7E — all outside net/http's valid cookie-value grammar, so
	// http.Response.Cookies() omits these lines entirely.
	malformedSession := "omnipus-session=a\\b\x80; Path=/"
	malformedCSRF := "csrf=x\x7f; Path=/"
	legitApp := "my-app-cookie=legit-value; Path=/"

	resp := &http.Response{Header: http.Header{}}
	resp.Header["Set-Cookie"] = []string{malformedSession, malformedCSRF, legitApp}

	// Precondition: net/http's own parser is BLIND to the malformed reserved
	// lines (it returns only the valid app cookie) — the exact gap a
	// parse-based filter would fall into.
	preNames := map[string]bool{}
	for _, c := range resp.Cookies() {
		preNames[c.Name] = true
	}
	require.False(
		t,
		preNames["omnipus-session"],
		"precondition: net/http Cookies() is blind to the malformed reserved omnipus-session line",
	)
	require.False(t, preNames["csrf"], "precondition: net/http Cookies() is blind to the malformed reserved csrf line")

	neutralizeReservedSetCookies(resp)

	for _, line := range resp.Header["Set-Cookie"] {
		name := cookieNameFromSetCookieLine(line)
		assert.NotEqual(t, "omnipus-session", name, "malformed reserved omnipus-session must be stripped: %q", line)
		assert.NotEqual(t, "csrf", name, "malformed reserved csrf must be stripped: %q", line)
	}
	require.Contains(t, resp.Header["Set-Cookie"], legitApp,
		"the previewed app's own (valid) cookie must pass through byte-for-byte")
}
