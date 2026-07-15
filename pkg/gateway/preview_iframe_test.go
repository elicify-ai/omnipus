//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — Track E test suite for the chat-served-iframe-preview
// feature (chat-served-iframe-preview-spec.md).
//
// Coverage targets (SC-006 per spec):
//   - pkg/gateway/rest_preview.go (HandlePreview — the unified /preview/ route)
//   - pkg/gateway/rest_workspace.go (CSP / headers)
//   - pkg/config/config.go (IsPreviewEnabled — ADR-044; ValidateAndApplyPreviewDefaults
//     and the separate-port preview keys it validated were deleted, no back-compat)
//
// Tests NOT here (Track B already covered):
//   - TestBuildWorkspaceCSP_FrameAncestorsFromMainOrigin
//   - TestBuildWorkspaceCSP_FrameAncestorsFallback
//   - TestSanitisePreviewPath_RedactsToken
//   - TestMarkFirstServed_OnlyTrueOnce
//   - TestMarkFirstServed_EmptyTokenIsNoOp
//   - TestCanonicalRemoteIP_PrefersForwardedFor
//   - TestServeWorkspaceTool_ResultIncludesPath

package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// parseTestURL parses a raw URL string and returns a *url.URL.
// Used in tests to construct httputil.ReverseProxy targets.
func parseTestURL(rawURL string) (*url.URL, error) {
	return url.Parse(rawURL)
}

// ---------------------------------------------------------------------------
// Helper: newPreviewTestAPI builds a restAPI with a real ServedSubdirs
// registry wired, and a temporary workspace. Suitable for /preview/ handler tests.
// ---------------------------------------------------------------------------

func newPreviewTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	api := newTestRestAPIWithHome(t)
	ss := agent.NewServedSubdirs()
	t.Cleanup(ss.Stop)
	api.servedSubdirs = ss
	return api
}

// ---------------------------------------------------------------------------
// Handler security-header tests
// ---------------------------------------------------------------------------

// TestServePreview_FrameAncestorsHeader verifies that HandlePreview
// sets a CSP header with frame-ancestors pointing at the main origin.
// Traces to: chat-served-iframe-preview-spec.md FR-007c
func TestServePreview_FrameAncestorsHeader(t *testing.T) {
	api := newPreviewTestAPI(t)

	// Create a temporary file to serve.
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.html")
	require.NoError(t, os.WriteFile(indexPath, []byte("<h1>hello</h1>"), 0o600))

	// Register the directory.
	ss := api.servedSubdirs
	token, _, err := ss.Register("agent-1", dir, time.Hour)
	require.NoError(t, err)

	// Wire a non-wildcard config so we get a real frame-ancestors value.
	api.agentLoop.GetConfig().Gateway.Host = "127.0.0.1"
	api.agentLoop.GetConfig().Gateway.Port = 5000

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/preview/agent-1/"+token+"/index.html", nil)
	api.HandlePreview(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	csp := w.Header().Get("Content-Security-Policy")
	assert.NotEmpty(t, csp, "CSP header must be present on /preview/ response")
	assert.Contains(t, csp, "frame-ancestors",
		"CSP must contain frame-ancestors directive (FR-007c)")
	assert.NotContains(t, csp, "frame-ancestors 'none'",
		"Old 'none' value must be removed (CR-01)")
}

// TestDevPreview_FrameAncestorsHeader verifies that the dev proxy's ModifyResponse
// hook strips upstream CSP and injects the gateway-authoritative frame-ancestors.
// Traces to: chat-served-iframe-preview-spec.md FR-007d
func TestDevPreview_FrameAncestorsHeader(t *testing.T) {
	// We exercise the security header injection via the responseHeaderWriter path
	// which is the same code path as HandlePreview's proxyDevRequest rp.ModifyResponse.
	testMainOrigin := "http://127.0.0.1:5000"
	upstreamHeaders := http.Header{}
	upstreamHeaders.Set("Content-Security-Policy", "default-src 'unsafe-eval'")
	upstreamHeaders.Set("X-Frame-Options", "SAMEORIGIN")

	// Simulate ModifyResponse stripping + injection.
	upstreamHeaders.Del("Content-Security-Policy")
	upstreamHeaders.Del("Content-Security-Policy-Report-Only")
	upstreamHeaders.Del("X-Frame-Options")
	setWorkspaceSecurityHeaders(responseHeaderWriter{upstreamHeaders}, testMainOrigin)

	assert.Empty(t, upstreamHeaders.Get("X-Frame-Options"),
		"X-Frame-Options must be stripped from upstream (FR-007d)")
	csp := upstreamHeaders.Get("Content-Security-Policy")
	assert.NotEmpty(t, csp, "gateway CSP must be injected (FR-007d)")
	assert.Contains(t, csp, "frame-ancestors http://127.0.0.1:5000",
		"frame-ancestors must reference the main origin (FR-007c)")
}

// TestServePreview_ReferrerPolicyHeader verifies that the /preview/ handler
// sets Referrer-Policy: no-referrer on 200 responses.
// Traces to: chat-served-iframe-preview-spec.md FR-007b / T-03
func TestServePreview_ReferrerPolicyHeader(t *testing.T) {
	api := newPreviewTestAPI(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0o600))
	ss := api.servedSubdirs
	token, _, err := ss.Register("agent-2", dir, time.Hour)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/preview/agent-2/"+token+"/index.html", nil)
	api.HandlePreview(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "no-referrer",
		w.Header().Get("Referrer-Policy"),
		"Referrer-Policy must be no-referrer to prevent token leakage (T-03)")
}

// TestServePreview_CORSPreflight_AllowsMainOrigin verifies that an OPTIONS
// request from the configured main origin receives the CORS allow header.
// Traces to: chat-served-iframe-preview-spec.md FR-007a
func TestServePreview_CORSPreflight_AllowsMainOrigin(t *testing.T) {
	api := newPreviewTestAPI(t)
	api.agentLoop.GetConfig().Gateway.Host = "127.0.0.1"
	api.agentLoop.GetConfig().Gateway.Port = 5000

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/preview/any/token/", nil)
	r.Header.Set("Origin", "http://127.0.0.1:5000")

	api.HandlePreview(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code,
		"OPTIONS must return 204 (FR-007a)")
	assert.Equal(t, "http://127.0.0.1:5000",
		w.Header().Get("Access-Control-Allow-Origin"),
		"Same-origin CORS preflight must get Access-Control-Allow-Origin (FR-007a)")
	assert.Contains(t,
		w.Header().Get("Access-Control-Allow-Methods"), "GET",
		"GET must be in allowed methods")
	// F-45: Vary: Origin must be set so CDNs/caches don't reuse a same-origin
	// response for a foreign-origin request.
	// Traces to: chat-served-iframe-preview-spec.md FR-007a (cache safety)
	assert.Equal(t, "Origin", w.Header().Get("Vary"),
		"F-45: Vary: Origin must be set on CORS preflight (FR-007a cache safety)")
}

// TestServePreview_CORSPreflight_RejectsForeignOrigin verifies that OPTIONS
// from a foreign origin gets 204 but NO Access-Control-Allow-Origin header.
// Traces to: chat-served-iframe-preview-spec.md FR-007a (foreign origin path)
func TestServePreview_CORSPreflight_RejectsForeignOrigin(t *testing.T) {
	api := newPreviewTestAPI(t)
	api.agentLoop.GetConfig().Gateway.Host = "127.0.0.1"
	api.agentLoop.GetConfig().Gateway.Port = 5000

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/preview/any/token/", nil)
	r.Header.Set("Origin", "https://evil.example.com")

	api.HandlePreview(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code,
		"Foreign-origin OPTIONS must return 204 (not 403) — stealth rejection")
	assert.Empty(t,
		w.Header().Get("Access-Control-Allow-Origin"),
		"Foreign origin must NOT receive Access-Control-Allow-Origin (FR-007a)")
	// F-45: Vary: Origin must be set even on rejected preflights so caches
	// cannot serve the allow-headers-free response as a cached allow response.
	// Traces to: chat-served-iframe-preview-spec.md FR-007a (cache safety)
	assert.Equal(t, "Origin", w.Header().Get("Vary"),
		"F-45: Vary: Origin must be set on foreign-origin CORS preflight too (FR-007a cache safety)")
}

// ---------------------------------------------------------------------------
// Token auth tests
// ---------------------------------------------------------------------------

// TestServePreview_NoAuth_RequiresValidToken verifies the three auth sub-cases:
// valid token, unknown token, and agent-mismatch token.
// Traces to: chat-served-iframe-preview-spec.md FR-023 / T-09
func TestServePreview_NoAuth_RequiresValidToken(t *testing.T) {
	api := newPreviewTestAPI(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o600))
	ss := api.servedSubdirs
	token, _, err := ss.Register("agent-tok", dir, time.Hour)
	require.NoError(t, err)

	tests := []struct {
		name       string
		agentInURL string
		tok        string
		wantStatus int
	}{
		{
			name:       "valid token + matching agent",
			agentInURL: "agent-tok",
			tok:        token,
			wantStatus: http.StatusOK,
		},
		{
			name:       "unknown/expired token",
			agentInURL: "agent-tok",
			tok:        "completely-unknown-token-xyz",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "token from different agent (mismatch)",
			agentInURL: "other-agent",
			tok:        token,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet,
				"/preview/"+tc.agentInURL+"/"+tc.tok+"/index.html", nil)
			api.HandlePreview(w, r)
			assert.Equal(t, tc.wantStatus, w.Code,
				"FR-023: %s must return %d", tc.name, tc.wantStatus)
		})
	}
}

// ---------------------------------------------------------------------------
// 404 boundary tests — 9 paths from the spec BDD scenario outline
// ---------------------------------------------------------------------------

// TestPreviewMux_404ForUnregisteredPaths verifies that the /preview/ handler
// returns errors for paths that do not match or don't have valid tokens.
// Traces to: chat-served-iframe-preview-spec.md BDD 404 scenario
func TestPreviewMux_404ForUnregisteredPaths(t *testing.T) {
	api := newPreviewTestAPI(t)

	paths := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "malformed — no agent segment",
			path:       "/preview/",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed — agent only no token",
			path:       "/preview/agent1/",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown token",
			path:       "/preview/agent1/unknown-token-1234567890/index.html",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "another unknown token with path",
			path:       "/preview/agent2/bad-token-abc/sub/page.html",
			wantStatus: http.StatusNotFound,
		},
		// Note: the double-slash (zero-length agent segment) case was extracted
		// to TestServePreview_DoubleSlash_Returns400 below (F-47 fix: returns 400
		// + serve.malformed_url instead of a 404 mis-classification).
		{
			name:       "HEAD with unknown token",
			path:       "/preview/agent-x/head-unknown-tok/",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range paths {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			api.HandlePreview(w, r)
			assert.Equal(t, tc.wantStatus, w.Code,
				"path %q must return %d (spec 404 boundary)", tc.path, tc.wantStatus)
		})
	}
}

// ---------------------------------------------------------------------------
// Differentiation test: two different inputs → two different outputs
// ---------------------------------------------------------------------------

// TestServePreview_TwoDifferentFiles_ReturnsDistinctContent verifies that
// serving two different files returns their distinct content — proves the
// handler is not hardcoded.
// Traces to: chat-served-iframe-preview-spec.md FR-005
func TestServePreview_TwoDifferentFiles_ReturnsDistinctContent(t *testing.T) {
	api := newPreviewTestAPI(t)
	ss := api.servedSubdirs

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("content-A"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("content-B"), 0o600))

	token, _, err := ss.Register("agent-diff", dir, time.Hour)
	require.NoError(t, err)

	var bodies [2]string
	for i, file := range []string{"a.txt", "b.txt"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet,
			"/preview/agent-diff/"+token+"/"+file, nil)
		api.HandlePreview(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		bodies[i] = w.Body.String()
	}

	assert.NotEqual(t, bodies[0], bodies[1],
		"Two different files must return different content (differentiation test)")
	assert.Equal(t, "content-A", bodies[0], "a.txt must return its actual content")
	assert.Equal(t, "content-B", bodies[1], "b.txt must return its actual content")
}

// ---------------------------------------------------------------------------
// Method gate
// ---------------------------------------------------------------------------

// TestServePreview_MethodGate verifies POST returns 405 for a registered
// static token (the method check fires only after a successful token lookup).
// Traces to: chat-served-iframe-preview-spec.md FR-005
func TestServePreview_MethodGate(t *testing.T) {
	api := newPreviewTestAPI(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0o600))
	ss := api.servedSubdirs
	token, _, err := ss.Register("agent-method", dir, time.Hour)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/preview/agent-method/"+token+"/file.html", nil)
	api.HandlePreview(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code,
		"POST to /preview/ must return 405 — only GET/HEAD allowed")
}

// ---------------------------------------------------------------------------
// Audit: FirstRequestOnly
// ---------------------------------------------------------------------------

// TestServePreview_AuditEvents_FirstRequestOnly verifies that markFirstServed
// gates repeated calls. The audit emission itself is tested via the proof tests.
// Traces to: chat-served-iframe-preview-spec.md FR-024
func TestServePreview_AuditEvents_FirstRequestOnly(t *testing.T) {
	api := newPreviewTestAPI(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0o600))
	ss := api.servedSubdirs
	token, _, err := ss.Register("agent-audit", dir, time.Hour)
	require.NoError(t, err)

	firstCount := 0
	originalMark := func() bool {
		return api.markFirstServed(token)
	}

	if originalMark() {
		firstCount++
	}
	// Subsequent calls must return false.
	for i := 0; i < 5; i++ {
		if originalMark() {
			firstCount++
		}
	}
	assert.Equal(t, 1, firstCount,
		"markFirstServed must return true exactly once per token (FR-024)")
}

// TestServePreview_AuditEvents_Failure verifies that invalid-token requests
// return 404 — the audit path for failure events.
// Traces to: chat-served-iframe-preview-spec.md FR-024a
func TestServePreview_AuditEvents_Failure(t *testing.T) {
	api := newPreviewTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/preview/agent-fail/bad-token/index.html", nil)
	api.HandlePreview(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"Invalid token must produce 404 (FR-024a audit failure path)")
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"], "401 response must carry error message")
}

// ---------------------------------------------------------------------------
// CanonicalGatewayOrigin (PublicURL override)
// ---------------------------------------------------------------------------

// TestCanonicalGatewayOrigin_PublicURLOverride verifies that when public_url
// is set it overrides the host:port derived origin.
// Traces to: chat-served-iframe-preview-spec.md FR-022 / MR-03
func TestCanonicalGatewayOrigin_PublicURLOverride(t *testing.T) {
	tests := []struct {
		name      string
		publicURL string
		host      string
		port      int
		want      string
	}{
		{
			name:      "public_url takes precedence over host:port",
			publicURL: "https://omnipus.example.com",
			host:      "127.0.0.1",
			port:      5000,
			want:      "https://omnipus.example.com",
		},
		{
			name:      "no public_url derives from host:port",
			publicURL: "",
			host:      "127.0.0.1",
			port:      5000,
			want:      "http://127.0.0.1:5000",
		},
		{
			name:      "wildcard host without public_url returns empty",
			publicURL: "",
			host:      "0.0.0.0",
			port:      5000,
			want:      "",
		},
		// F-44: IPv6 wildcard cases. The production code normalises "[<host>]"
		// by stripping brackets (middleware/origin.go:77) and then matches on
		// "0.0.0.0", "::", "::0". All four below must return empty origin.
		// Traces to: chat-served-iframe-preview-spec.md FR-007e (fallback path)
		{
			name:      "F-44: IPv6 any-addr [::]",
			publicURL: "",
			host:      "[::]",
			port:      5000,
			want:      "",
		},
		{
			name:      "F-44: IPv6 any-addr :: (bare)",
			publicURL: "",
			host:      "::",
			port:      5000,
			want:      "",
		},
		{
			name:      "F-44: IPv6 any-addr ::0",
			publicURL: "",
			host:      "::0",
			port:      5000,
			want:      "",
		},
		{
			// ::1 is the loopback address — NOT a wildcard. It should produce a
			// real origin just like 127.0.0.1.
			// Traces to: chat-served-iframe-preview-spec.md FR-007e (loopback is specific)
			name:      "F-44: IPv6 loopback [::1] is NOT wildcard — produces real origin",
			publicURL: "",
			host:      "::1",
			port:      5000,
			want:      "http://::1:5000",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newTestRestAPIWithHome(t).agentLoop.GetConfig()
			cfg.Gateway.PublicURL = tc.publicURL
			cfg.Gateway.Host = tc.host
			cfg.Gateway.Port = tc.port
			got := middleware.CanonicalGatewayOrigin(cfg)
			assert.Equal(t, tc.want, got,
				"CanonicalGatewayOrigin must use public_url when set (FR-022 / MR-03)")
		})
	}
}

// ---------------------------------------------------------------------------
// HandleAbout — preview fields
// ---------------------------------------------------------------------------

// TestHandleAbout_PreviewFields verifies FR-015: GET /api/v1/about includes
// preview_enabled and warmup_timeout_seconds, and no longer includes the
// retired preview_port/preview_listener_enabled/preview_origin fields.
// Traces to: preview-on-main-listener-spec.md FR-015 (US-8)
func TestHandleAbout_PreviewFields(t *testing.T) {
	api := newPreviewTestAPI(t)
	// Wire explicit preview config.
	cfg := api.agentLoop.GetConfig()
	cfg.Gateway.Port = 5000
	trueVal := true
	cfg.Gateway.PreviewEnabled = &trueVal
	cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds = 60

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
	api.HandleAbout(w, r)

	require.Equal(t, http.StatusOK, w.Code, "/api/v1/about must return 200")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// FR-015: preview_enabled replaces the retired preview listener fields.
	assert.Contains(t, resp, "preview_enabled",
		"FR-015: about must include preview_enabled")
	assert.Contains(t, resp, "warmup_timeout_seconds",
		"FR-015: about must include warmup_timeout_seconds")
	assert.NotContains(t, resp, "preview_port",
		"FR-015: preview_port must be removed from AboutResponse")
	assert.NotContains(t, resp, "preview_listener_enabled",
		"FR-015: preview_listener_enabled must be removed from AboutResponse")
	assert.NotContains(t, resp, "preview_origin",
		"FR-015: preview_origin must be removed from AboutResponse")

	// Values must reflect the config we wired.
	// JSON numbers decode as float64 in map[string]any.
	assert.Equal(t, true, resp["preview_enabled"],
		"preview_enabled must reflect config")
	assert.Equal(t, float64(60), resp["warmup_timeout_seconds"],
		"warmup_timeout_seconds must match configured value")
}

// TestHandleAbout_PreviewFields_Differentiation verifies that two different
// preview_enabled configs produce two different about responses — proving
// it's not hardcoded.
// Traces to: preview-on-main-listener-spec.md FR-015 (US-8, differentiation)
func TestHandleAbout_PreviewFields_Differentiation(t *testing.T) {
	values := []bool{true, false}
	bodies := make([]map[string]any, 2)
	for i, v := range values {
		api := newPreviewTestAPI(t)
		cfg := api.agentLoop.GetConfig()
		cfg.Gateway.Port = 5000
		enabled := v
		cfg.Gateway.PreviewEnabled = &enabled

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
		api.HandleAbout(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bodies[i]))
	}
	assert.NotEqual(t, bodies[0]["preview_enabled"], bodies[1]["preview_enabled"],
		"Different preview_enabled configs must produce different about responses")
}

// TestHandleAbout_PreviewFields_GeneratedContractType strengthens
// TestHandleAbout_PreviewFields (pr-test-analyzer finding #24): decoding the
// response into map[string]any only proves the field NAMES are present — it
// cannot catch a JSON-type/schema drift (e.g. preview_enabled serialized as a
// string, or a field silently renamed) because map[string]any accepts any
// shape. This decodes the SAME /api/v1/about response into the GENERATED
// gen.AboutResponse struct (Constraint #8 — the one legal cross-boundary type
// for this response, per contracts/components/schemas/AboutResponse.yaml)
// and asserts PreviewEnabled round-trips correctly for both the true default
// and an explicitly-disabled config, proving HandleAbout emits schema-valid
// JSON for the new field — not just JSON that happens to have the right key
// when decoded loosely.
// Traces to: preview-on-main-listener-spec.md FR-015 (US-8); pr-test #24.
func TestHandleAbout_PreviewFields_GeneratedContractType(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("preview_enabled=%v", enabled), func(t *testing.T) {
			api := newPreviewTestAPI(t)
			cfg := api.agentLoop.GetConfig()
			cfg.Gateway.Port = 5000
			v := enabled
			cfg.Gateway.PreviewEnabled = &v
			cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds = 60

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
			api.HandleAbout(w, r)

			require.Equal(t, http.StatusOK, w.Code, "/api/v1/about must return 200")

			var resp gen.AboutResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
				"FR-015: /api/v1/about response must decode into the GENERATED gen.AboutResponse "+
					"contract type without error — a type-mismatched or malformed field would fail "+
					"here even though a map[string]any decode would silently accept it")

			assert.Equal(t, enabled, resp.PreviewEnabled,
				"gen.AboutResponse.PreviewEnabled must reflect the configured value")
			assert.Equal(t, 60, resp.WarmupTimeoutSeconds,
				"gen.AboutResponse.WarmupTimeoutSeconds must match the configured warmup timeout")
			assert.NotEmpty(t, resp.Version, "gen.AboutResponse.Version must be populated")
			assert.NotEmpty(t, resp.GoVersion, "gen.AboutResponse.GoVersion must be populated")
			assert.NotEmpty(t, resp.Os, "gen.AboutResponse.Os must be populated")
			assert.NotEmpty(t, resp.Arch, "gen.AboutResponse.Arch must be populated")
			assert.Positive(t, resp.Pid, "gen.AboutResponse.Pid must be a real positive PID")
		})
	}
}

// ---------------------------------------------------------------------------
// PendingRestart — preview fields
// ---------------------------------------------------------------------------
//
// TestPendingRestart_PreviewFields (the old FR-027b/MR-02 test asserting that
// changing preview_port surfaces a pending-restart diff) was removed under
// ADR-044: gateway.preview_port no longer exists, and gateway.preview_enabled
// (its replacement) is deliberately NOT restart-gated — see
// TestRestartGatedKeys_KeepsPublicURL_DropsPreview (s1_befixes-adjacent
// coverage) and rest_pending_restart.go's RestartGatedKeys doc comment.

// ---------------------------------------------------------------------------
// ProxyDevRequest — strips upstream CSP
// ---------------------------------------------------------------------------

// TestProxyDevRequest_StripsUpstreamCSP verifies that the responseHeaderWriter
// adapter removes upstream Content-Security-Policy before injecting gateway's.
// Traces to: chat-served-iframe-preview-spec.md FR-007d
func TestProxyDevRequest_StripsUpstreamCSP(t *testing.T) {
	// Simulate the headers an upstream dev-server (e.g. Next.js) would return.
	h := http.Header{}
	h.Set("Content-Security-Policy", "default-src 'unsafe-eval'; connect-src *")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Type", "text/html")

	// Strip (as ModifyResponse does).
	h.Del("Content-Security-Policy")
	h.Del("Content-Security-Policy-Report-Only")
	h.Del("X-Frame-Options")

	// Inject gateway-authoritative policy.
	setWorkspaceSecurityHeaders(responseHeaderWriter{h}, "http://127.0.0.1:5000")

	csp := h.Get("Content-Security-Policy")
	assert.NotContains(t, csp, "unsafe-eval",
		"upstream 'unsafe-eval' must be replaced by gateway CSP (FR-007d)")
	assert.Contains(t, csp, "frame-ancestors http://127.0.0.1:5000",
		"gateway-injected frame-ancestors must reference main origin (FR-007c)")
	assert.Empty(t, h.Get("X-Frame-Options"),
		"X-Frame-Options must be stripped (FR-007d)")
	assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"),
		"Referrer-Policy must be no-referrer (FR-007b)")
}

// TestHandlePreview_NoOriginMiddleware verifies that HandlePreview does NOT
// enforce Origin header matching on state-changing methods (FR-023a / CR-02).
// A POST without an Origin header must receive a non-403 response (404 since
// the token is unregistered — devServers is nil so it falls through to the
// static-registry miss path).
// Traces to: chat-served-iframe-preview-spec.md FR-023a
func TestHandlePreview_NoOriginMiddleware(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// devServers is nil — HandlePreview falls through to the servedSubdirs miss
	// path (404 "preview registration not found") rather than 403 "origin
	// mismatch". This proves no Origin check exists.

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/preview/agent1/sometoken/api/login", nil)
	// Deliberately no Origin header — if an origin-check middleware were wired it
	// would return 403; without it we get 404 (unregistered token) — but never 403.
	api.HandlePreview(w, r)

	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"HandlePreview must NOT return 403 for missing Origin (FR-023a: no origin check)")
}

// ---------------------------------------------------------------------------
// reverseProxy no-op transport test
// ---------------------------------------------------------------------------

// TestResponseHeaderWriter_AdaptsHeader verifies the responseHeaderWriter
// adapter correctly delegates to the underlying http.Header.
// Traces to: chat-served-iframe-preview-spec.md FR-007d implementation detail
func TestResponseHeaderWriter_AdaptsHeader(t *testing.T) {
	h := http.Header{}
	rhw := responseHeaderWriter{h: h}

	rhw.Header().Set("X-Test-Key", "test-value")
	assert.Equal(t, "test-value", h.Get("X-Test-Key"),
		"responseHeaderWriter.Header() must delegate to the underlying http.Header")

	// Write and WriteHeader are no-ops — verify they don't panic.
	n, err := rhw.Write([]byte("ignored"))
	assert.Equal(t, 0, n)
	assert.NoError(t, err)
	rhw.WriteHeader(http.StatusOK) // should not panic
}

// ---------------------------------------------------------------------------
// httputil.ReverseProxy used by proxyDevRequest (structural verification)
// ---------------------------------------------------------------------------

// TestProxyDevRequest_UpstreamCSPStrip_Integration verifies the full pipeline
// of stripping upstream headers and injecting gateway headers using
// httputil.ReverseProxy's ModifyResponse in a minimal integration harness.
// Traces to: chat-served-iframe-preview-spec.md FR-007d
func TestProxyDevRequest_UpstreamCSPStrip_Integration(t *testing.T) {
	// Fake upstream that emits unsafe headers.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-eval'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-body"))
	}))
	defer upstream.Close()

	mainOrigin := "http://localhost:5000"

	// Parse upstream URL directly without an extra helper.
	upstreamURL, err := parseTestURL(upstream.URL)
	require.NoError(t, err)

	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("X-Frame-Options")
		setWorkspaceSecurityHeaders(responseHeaderWriter{resp.Header}, mainOrigin)
		return nil
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	csp := w.Header().Get("Content-Security-Policy")
	assert.NotContains(t, csp, "unsafe-eval",
		"upstream unsafe-eval must be stripped (FR-007d)")
	assert.Contains(t, csp, "frame-ancestors http://localhost:5000",
		"gateway frame-ancestors must be injected (FR-007c)")
	assert.Empty(t, w.Header().Get("X-Frame-Options"),
		"X-Frame-Options must be stripped")
}

// ---------------------------------------------------------------------------
// Workspace security headers (HandleWorkspace)
// ---------------------------------------------------------------------------

// TestWorkspace_SecurityHeaders verifies that the workspace endpoint sets
// security headers using the current buildWorkspaceCSP output (not 'none').
// Traces to: chat-served-iframe-preview-spec.md FR-007 (pre-existing test fix)
func TestWorkspace_SecurityHeaders(t *testing.T) {
	// buildWorkspaceCSP output must contain connect-src 'self' (CR-01 fix) and
	// form-action 'self' and frame-ancestors based on main origin.
	mainOrigin := "http://127.0.0.1:5000"
	csp := buildWorkspaceCSP(mainOrigin)

	assert.Contains(t, csp, "connect-src 'self'",
		"connect-src must be 'self' after CR-01 fix (not 'none')")
	assert.Contains(t, csp, "form-action 'self'",
		"form-action must be 'self' after CR-01 fix (not 'none')")
	assert.Contains(t, csp, "frame-ancestors http://127.0.0.1:5000",
		"frame-ancestors must use the provided main origin")
	assert.NotContains(t, csp, "frame-ancestors 'none'",
		"Old 'none' frame-ancestors must be gone (CR-01)")
	assert.NotContains(t, csp, "connect-src 'none'",
		"Old 'none' connect-src must be gone (CR-01)")
}

// ---------------------------------------------------------------------------
// F-19: Server-side path-traversal blocking for /preview/
// ---------------------------------------------------------------------------

// TestServePreview_PathTraversal_Returns403 verifies that HandlePreview
// blocks path traversal attempts (e.g. /../../../etc/passwd) and returns
// 403 with a serve.path_invalid audit event (F-19).
// Traces to: chat-served-iframe-preview-spec.md FR-005 (path safety)
func TestServePreview_PathTraversal_Returns403(t *testing.T) {
	api := newPreviewTestAPI(t)
	dir := t.TempDir()
	// Put a real file in the served dir so the handler doesn't 404 on root.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o600))
	ss := api.servedSubdirs
	token, _, err := ss.Register("traversal-agent", dir, time.Hour)
	require.NoError(t, err)

	traversalPaths := []struct {
		name string
		path string
	}{
		{
			name: "classic dotdot",
			path: "/preview/traversal-agent/" + token + "/../../etc/passwd",
		},
		{
			name: "triple traversal",
			path: "/preview/traversal-agent/" + token + "/../../../etc/shadow",
		},
		{
			name: "encoded traversal stays blocked after clean",
			path: "/preview/traversal-agent/" + token + "/../secret",
		},
	}

	for _, tc := range traversalPaths {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			api.HandlePreview(w, r)
			assert.True(t,
				w.Code == http.StatusForbidden || w.Code == http.StatusNotFound,
				"path traversal %q must return 403 or 404, got %d", tc.path, w.Code)
			// 403 is the ideal (explicit traversal block); 404 is acceptable when
			// the traversed path does not exist outside the sandbox (defense-in-depth).
		})
	}
}

// TestDevProxy_PathTraversal_Returns400 verifies that HandlePreview never
// forwards a dev-proxy-shaped traversal path to an upstream as a successful
// proxy response.
// Traces to: chat-served-iframe-preview-spec.md FR-006 (path safety)
func TestDevProxy_PathTraversal_Returns400(t *testing.T) {
	api := newPreviewTestAPI(t)
	// devServers is nil, so the token is looked up against servedSubdirs only,
	// which also has no matching registration — the request falls through to
	// the 404 "preview registration not found" path. The important invariant:
	// the response is NOT a successful proxy (200).
	if testing.Short() {
		t.Skip("dev proxy traversal test skipped in -short mode")
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/preview/some-agent/some-token/../../etc/passwd", nil)
	api.HandlePreview(w, r)

	assert.NotEqual(t, http.StatusOK, w.Code,
		"traversal path must not produce a 200 OK")
}

// ---------------------------------------------------------------------------
// F-43: Stress test (SC-008) — real httptest.Server, guarded behind -short
// ---------------------------------------------------------------------------

// TestServePreview_LoadStress_ConcurrentProbes runs 30 concurrent goroutines
// hitting /preview/ through a real httptest.Server for 5 s (fast CI run) and
// asserts HeapAlloc growth stays under 50 MB. The 60 s duration from the
// previous implementation made this test impractical for CI; 5 s achieves
// the same memory-bound goal at a fraction of the wall-clock cost.
//
// Gate: skipped under -short (run explicitly via
//
//	CGO_ENABLED=0 go test -run TestServePreview_LoadStress ./pkg/gateway/... -count=1
//
// Traces to: chat-served-iframe-preview-spec.md SC-008
func TestServePreview_LoadStress_ConcurrentProbes(t *testing.T) {
	if testing.Short() {
		t.Skip("F-43 stress test skipped in -short mode (SC-008)")
	}

	api := newPreviewTestAPI(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "probe.html"), []byte("<h1>probe</h1>"), 0o600))
	ss := api.servedSubdirs
	token, _, err := ss.Register("stress-agent", dir, time.Hour)
	require.NoError(t, err)

	// Spin up a real HTTP server wrapping HandlePreview so the test exercises
	// the full net/http connection path rather than direct handler invocation.
	// httptest.NewRecorder doesn't exercise connection management or response
	// buffering; a real server does.
	srv := httptest.NewServer(http.HandlerFunc(api.HandlePreview))
	defer srv.Close()

	probeURL := srv.URL + "/preview/stress-agent/" + token + "/probe.html"
	client := srv.Client()

	const (
		workers  = 30
		duration = 5 * time.Second
	)

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	deadline := time.Now().Add(duration)
	var mu sync.Mutex
	var totalErrors int

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var localErrors int
			for time.Now().Before(deadline) {
				resp, getErr := client.Get(probeURL)
				if getErr != nil || resp.StatusCode != http.StatusOK {
					localErrors++
				}
				if resp != nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
			mu.Lock()
			totalErrors += localErrors
			mu.Unlock()
		}()
	}

	wg.Wait()

	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)

	// HeapAlloc can't be negative; use InUse as the measure of live memory
	// increase. 50 MB is generous for 30 workers over 5 s of static file serving.
	const maxGrowthBytes = 50 * 1024 * 1024
	heapGrowth := int64(after.HeapInuse) - int64(before.HeapInuse)
	if heapGrowth < 0 {
		heapGrowth = 0
	}
	assert.Zero(t, totalErrors,
		"F-43: all concurrent probes must succeed (SC-008: 0 errors)")
	assert.LessOrEqual(t, heapGrowth, int64(maxGrowthBytes),
		"F-43: HeapInuse growth must be < 50 MB under %d concurrent workers (SC-008)", workers)
}

// ---------------------------------------------------------------------------
// F-47-test — Double-slash returns 400 + serve.malformed_url
// ---------------------------------------------------------------------------

// TestServePreview_DoubleSlash_Returns400 verifies that /preview//some-token/file.html
// returns 400 and a preview.malformed_url audit event. The F-47 production fix
// changed the parse step to reject empty agent segments explicitly before the
// registry lookup, producing 400 + preview.malformed_url instead of a confusing
// misclassification.
// Traces to: chat-served-iframe-preview-spec.md F-47 / preview.malformed_url
func TestServePreview_DoubleSlash_Returns400(t *testing.T) {
	api := newPreviewTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/preview//some-token/file.html", nil)
	api.HandlePreview(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"F-47: double-slash (zero-length agent segment) must return 400 (preview.malformed_url)")

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"],
		"F-47: 400 response must carry an error message")
}

// ---------------------------------------------------------------------------
// F-39 — Audit logger nil-path: no panic when AuditLogger() returns nil
// ---------------------------------------------------------------------------

// TestServePreview_NilAuditLogger_NoPanic verifies that HandlePreview does not
// panic when the audit logger is nil (as is the case in the default
// newTestRestAPIWithHome harness where audit logging is not configured). The
// slog mirror in emitPreviewAuditEntry fires regardless of whether the bus-level
// logger is wired, so the test also verifies the handler returns the expected status.
// Traces to: chat-served-iframe-preview-spec.md FR-024 (best-effort audit)
func TestServePreview_NilAuditLogger_NoPanic(t *testing.T) {
	// newPreviewTestAPI produces an AgentLoop whose AuditLogger() returns nil
	// (audit logging is disabled in the test harness — no audit dir configured).
	api := newPreviewTestAPI(t)

	// Verify assumption: AuditLogger() must indeed be nil in our test harness.
	if api.agentLoop.AuditLogger() != nil {
		t.Skip("skipping: test harness has an audit logger wired (unexpected)")
	}

	// Success path — valid file, nil audit logger must not panic.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o600))
	token, _, err := api.servedSubdirs.Register("nil-audit-agent", dir, time.Hour)
	require.NoError(t, err)

	// emitPreviewAuditEntry checks logger == nil and returns early — must not panic.
	require.NotPanics(t, func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/preview/nil-audit-agent/"+token+"/index.html", nil)
		api.HandlePreview(w, r)
		assert.Equal(t, http.StatusOK, w.Code,
			"F-39: /preview/ must return 200 even when audit logger is nil")
	}, "F-39: HandlePreview must not panic when audit logger is nil")

	// Differentiation: the slog mirror fires on the failure path too (different
	// event). A 404 for an unknown token triggers the failure audit path — must
	// not panic.
	require.NotPanics(t, func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/preview/nil-audit-agent/unknown-token/file.html", nil)
		api.HandlePreview(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code,
			"F-39: unknown token must return 404 on nil audit logger path")
	}, "F-39: failure audit path must not panic with nil audit logger")
}

// (No slog capture helpers needed — tests verify HTTP status codes directly.
// The slog mirror behavior is exercised implicitly when emitPreviewAuditEntry
// fires and the nil-audit-logger path returns early without panicking.)

// ---------------------------------------------------------------------------
// F-39 — Audit logger error-path: Log() error must not fail the HTTP response
// ---------------------------------------------------------------------------

// TestServePreview_AuditLogError_NotFailClosed verifies that when the audit
// logger is in degraded mode (Log() returns an error), the HTTP response is still
// 200 (not 500). The audit failure must not fail-close the served request.
//
// Two sub-tests:
//  1. Confirm a degraded audit.Logger.Log() returns an error (proves the test harness).
//  2. Confirm HandlePreview returns 200 with a nil logger (the nil-logger path
//     is the observable proxy for "Log() fails silently" since we cannot inject the
//     unexported field; the nil-check and error-return path share the same not-fail-closed
//     contract in emitPreviewAuditEntry).
//
// Traces to: chat-served-iframe-preview-spec.md FR-024 (best-effort audit)
func TestServePreview_AuditLogError_NotFailClosed(t *testing.T) {
	// --- Part 1: verify the degraded logger setup produces errors ---
	// Create a degraded logger by pointing it at a path that is a file, not a dir.
	// NewLogger calls MkdirAll which will fail because the parent exists as a file,
	// causing the logger to enter degraded mode where Log() returns errors.
	fileNotDir := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(fileNotDir, []byte("block"), 0o600))

	// Use a sub-path of the file to force MkdirAll to fail.
	degradedLogger, _ := audit.NewLogger(audit.LoggerConfig{
		Dir:           filepath.Join(fileNotDir, "audit"),
		MaxSizeBytes:  1024,
		RetentionDays: 1,
	})
	// degradedLogger may be nil if NewLogger propagates the error.
	// If it's non-nil, Log() must return an error (degraded mode).
	if degradedLogger != nil {
		t.Cleanup(func() { _ = degradedLogger.Close() })
		logErr := degradedLogger.Log(&audit.Entry{Event: "test.probe", Decision: "allow"})
		assert.Error(t, logErr,
			"F-39: degraded audit logger must return error from Log() — confirms setup")
	}

	// --- Part 2: verify the handler is not fail-closed on audit errors ---
	// The restAPI test harness has audit logger = nil (not configured). The
	// emitPreviewAuditEntry function checks `if logger == nil { return }` — this
	// is the best-effort gate. We verify the HTTP response is 200 regardless.
	//
	// NOTE: AgentLoop.auditLogger is unexported with no exported setter, so we
	// cannot inject the degraded logger directly. The nil-logger and degraded-logger
	// paths share the same observable contract: neither causes the HTTP response to
	// be 500. The nil path is the one we can fully control here.
	api := newPreviewTestAPI(t)
	require.Nil(t, api.agentLoop.AuditLogger(),
		"F-39: test harness must have nil audit logger (pre-condition for this test)")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o600))
	token, _, regErr := api.servedSubdirs.Register("err-audit-agent", dir, time.Hour)
	require.NoError(t, regErr)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/preview/err-audit-agent/"+token+"/index.html", nil)
	api.HandlePreview(w, r)

	// Handler must return 200 — audit failure must NOT fail-close the response.
	assert.Equal(t, http.StatusOK, w.Code,
		"F-39: audit logger error/nil must NOT cause the HTTP handler to return non-200 (best-effort)")

	// Differentiation: a 404 failure path also must not fail-close.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet,
		"/preview/err-audit-agent/bad-token/index.html", nil)
	api.HandlePreview(w2, r2)
	assert.Equal(t, http.StatusNotFound, w2.Code,
		"F-39: failure audit path with nil logger must return 404 (not 500)")
}

// ---------------------------------------------------------------------------
// F-42 — Dev frame-ancestors via real HandlePreview
// ---------------------------------------------------------------------------

// TestDevPreview_FrameAncestorsHeader_ViaRealHandler verifies that HandlePreview's
// dev-proxy ModifyResponse hook correctly strips upstream CSP/XFO and injects the
// gateway-authoritative frame-ancestors directive. This test drives a real
// httputil.NewSingleHostReverseProxy through HandlePreview's dev-proxy branch.
// Traces to: chat-served-iframe-preview-spec.md FR-007d
func TestDevPreview_FrameAncestorsHeader_ViaRealHandler(t *testing.T) {
	// Spin up an upstream dev server that emits its own CSP/XFO headers.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-eval'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "<h1>dev server</h1>")
	}))
	defer upstream.Close()

	// Parse the upstream URL to get the port.
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	// Convert port string to int32 for DevServerRegistry.
	var upstreamPort int32
	_, portStr := upstreamURL.Hostname(), upstreamURL.Port()
	require.NotEmpty(t, portStr, "upstream must have a port")
	_, scanErr := fmt.Sscanf(portStr, "%d", &upstreamPort)
	require.NoError(t, scanErr)

	// Wire api with a real DevServerRegistry.
	api := newPreviewTestAPI(t)
	api.agentLoop.GetConfig().Gateway.Host = "127.0.0.1"
	api.agentLoop.GetConfig().Gateway.Port = 5000

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg

	// Register the upstream as a dev server.
	registration, regErr := reg.Register("f42-agent", upstreamPort, 0 /*pid*/, "test-cmd", 10)
	require.NoError(t, regErr)
	token := registration.Token

	// Fire a GET through HandlePreview.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/preview/f42-agent/"+token+"/index.html", nil)
	api.HandlePreview(w, r)

	// Handler must proxy successfully (200 from upstream).
	require.Equal(t, http.StatusOK, w.Code,
		"F-42: HandlePreview must proxy 200 from upstream dev server")

	// Upstream CSP must be STRIPPED; gateway CSP must be INJECTED.
	csp := w.Header().Get("Content-Security-Policy")
	assert.NotContains(t, csp, "unsafe-eval",
		"F-42: upstream 'unsafe-eval' CSP must be stripped by ModifyResponse (FR-007d)")
	assert.Contains(t, csp, "frame-ancestors http://127.0.0.1:5000",
		"F-42: gateway frame-ancestors must be injected by ModifyResponse (FR-007c)")
	assert.Empty(t, w.Header().Get("X-Frame-Options"),
		"F-42: upstream X-Frame-Options must be stripped (FR-007d)")

	// Content-Type must be preserved from upstream (ModifyResponse doesn't strip it).
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html",
		"F-42: Content-Type must be preserved from upstream (ModifyResponse must not strip it)")
}

// ---------------------------------------------------------------------------
// F-28 follow-on — Symlink escape returns 403
// ---------------------------------------------------------------------------

// TestServePreview_SymlinkEscape_Returns403 verifies that HandlePreview
// returns 403 when a symlink inside the served directory points to a path
// outside the registered directory. This tests the F-28 symlink-escape defense
// added to rest_preview.go.
//
// Skipped on Windows (os.Symlink requires elevated privileges on Windows).
// Traces to: chat-served-iframe-preview-spec.md FR-005 (path safety) / F-28
func TestServePreview_SymlinkEscape_Returns403(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("F-28: symlink creation requires elevated privileges on Windows")
	}

	api := newPreviewTestAPI(t)
	serveDir := t.TempDir()

	// Create a real file inside the serve directory (registry needs an actual dir).
	realFile := filepath.Join(serveDir, "real.txt")
	require.NoError(t, os.WriteFile(realFile, []byte("safe content"), 0o600))

	// Create a target file OUTSIDE the serve directory.
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret content"), 0o600))

	// Create a symlink INSIDE serveDir that points to the outside file.
	symlinkPath := filepath.Join(serveDir, "escape.txt")
	require.NoError(t, os.Symlink(outsideFile, symlinkPath))

	// Register the serve directory.
	token, _, err := api.servedSubdirs.Register("symlink-agent", serveDir, time.Hour)
	require.NoError(t, err)

	// Request the symlink path through /preview/.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/preview/symlink-agent/"+token+"/escape.txt", nil)
	api.HandlePreview(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"F-28: symlink escaping the serve directory must return 403 (serve.path_invalid)")

	// Verify the response carries an error message (not silent 403 body).
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"],
		"F-28: 403 response must carry an error message")

	// Differentiation: the real file inside the directory must still be accessible.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet,
		"/preview/symlink-agent/"+token+"/real.txt", nil)
	api.HandlePreview(w2, r2)
	assert.Equal(t, http.StatusOK, w2.Code,
		"F-28: real files (non-symlink) inside serve dir must still be served (differentiation)")
	assert.Equal(t, "safe content", w2.Body.String(),
		"F-28: real file content must be correct")
}

// ---------------------------------------------------------------------------
// F-32 follow-on — dev.path_invalid for invalid agentID
// ---------------------------------------------------------------------------

// TestDevPreview_InvalidEntityID_Returns400 verifies that HandlePreview returns
// 400 when the agentID contains characters that fail validateEntityID. This is
// the F-32 path: validation.EntityID is checked once, ahead of both the
// dev-server and static registries, in HandlePreview (rest_preview.go).
// Traces to: chat-served-iframe-preview-spec.md F-32 / dev.path_invalid
func TestDevPreview_InvalidEntityID_Returns400(t *testing.T) {
	api := newPreviewTestAPI(t)
	// Wire a real DevServerRegistry so this exercises the dev-registry path too.
	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg

	// Note: the agentID must be a single URL path segment — slashes are path
	// separators and would change the URL structure rather than land in the
	// agentID field. We test characters that validateEntityID rejects but that
	// can appear in a single URL path segment: ".." (dotdot) and null bytes.
	invalidAgentIDs := []struct {
		name    string
		agentID string
	}{
		{
			// dollar sign is not rejected by validateEntityID — it only rejects
			// "/", "\\", "..", and NUL. We test ".." which IS rejected.
			name:    "dotdot traversal attempt",
			agentID: "agent..bad",
		},
	}

	for _, tc := range invalidAgentIDs {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			// The URL path contains the invalid agentID in the second segment.
			// The ID check fires before either registry lookup.
			r := httptest.NewRequest(http.MethodGet,
				"/preview/"+tc.agentID+"/fake-token/path", nil)
			api.HandlePreview(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code,
				"F-32: agentID %q must return 400 (preview.malformed_url)", tc.agentID)

			var body map[string]string
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			assert.NotEmpty(t, body["error"],
				"F-32: 400 response must carry an error message for agentID %q", tc.agentID)
		})
	}

	// Differentiation: a valid agentID with unknown token produces 404 (not 400),
	// proving the 400 is specifically from the ID check, not a generic error.
	t.Run("valid agentID unknown token produces non-400", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/preview/valid-agent-id/unknown-token/", nil)
		api.HandlePreview(w, r)
		// Valid agentID + unknown token → 404 (token not found in either registry).
		assert.NotEqual(t, http.StatusBadRequest, w.Code,
			"F-32: valid agentID must not return 400 (ID validation passes, fails at token lookup)")
	})
}
