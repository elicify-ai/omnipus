//go:build !cgo

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
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/tools"
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
			name:     "trustXFF=true multi-hop XFF first",
			xff:      "203.0.113.1, 10.0.0.5",
			ra:       "10.0.0.1:1234",
			trustXFF: true,
			want:     "203.0.113.1",
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
func TestWebServeTool_StaticResultIncludesPath(t *testing.T) {
	dir := t.TempDir()
	ss := agent.NewServedSubdirs()
	t.Cleanup(ss.Stop)

	tool := tools.NewWebServeTool(
		dir,                     // workspace
		"fr008-agent",           // agentID
		"http://127.0.0.1:5001", // gatewayPreviewBaseURL
		ss,                      // served
		nil,                     // devReg — not needed for static mode
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
