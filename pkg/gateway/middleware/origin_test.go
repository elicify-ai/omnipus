// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// buildOriginCfg creates a minimal config with the given gateway host and port
// for use in origin-check middleware tests.
func buildOriginCfg(host string, port int) *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			Host: host,
			Port: port,
		},
	}
}

// passThroughHandler is a sentinel handler that records whether it was called.
type passThroughHandler struct {
	called bool
}

func (h *passThroughHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

// TestOrigin_GetBypasses verifies that GET requests are not subject to the
// Origin check even when no Origin header is present (safe method by RFC 7231).
func TestOrigin_GetBypasses(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/dev/agent/token/", nil)
	// No Origin header.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET without Origin: expected 200, got %d", rec.Code)
	}
	if !inner.called {
		t.Error("expected inner handler to be called for GET")
	}
}

// TestOrigin_PostMissingOriginRejected verifies that POST with no Origin
// header is rejected with 403.
func TestOrigin_PostMissingOriginRejected(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/dev/agent/token/api/data", nil)
	// No Origin header.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without Origin: expected 403, got %d", rec.Code)
	}
	if inner.called {
		t.Error("inner handler must not be called when origin check fails")
	}
}

// TestOrigin_PostMismatchedOriginRejected verifies that POST with a mismatched
// Origin header is rejected with 403.
func TestOrigin_PostMismatchedOriginRejected(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/dev/agent/token/api/data", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with mismatched Origin: expected 403, got %d", rec.Code)
	}
	if inner.called {
		t.Error("inner handler must not be called when origin mismatches")
	}
}

// TestOrigin_PostMatchingOriginPasses verifies that POST with the correct
// Origin passes through to the inner handler.
func TestOrigin_PostMatchingOriginPasses(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/dev/agent/token/api/data", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("POST with matching Origin: expected 200, got %d", rec.Code)
	}
	if !inner.called {
		t.Error("expected inner handler to be called for matching Origin")
	}
}

// TestOrigin_PutMatchingOriginPasses verifies that PUT with the correct
// Origin passes through.
func TestOrigin_PutMatchingOriginPasses(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("omnipus.example.com", 443)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPut, "/dev/agent/token/resource", nil)
	req.Header.Set("Origin", "https://omnipus.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("PUT with matching Origin: expected 200, got %d", rec.Code)
	}
	if !inner.called {
		t.Error("expected inner handler to be called")
	}
}

// TestOrigin_HeadBypasses verifies that HEAD (safe method) bypasses the check.
func TestOrigin_HeadBypasses(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodHead, "/dev/agent/token/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("HEAD without Origin: expected 200, got %d", rec.Code)
	}
	if !inner.called {
		t.Error("HEAD should bypass origin check")
	}
}

// TestOrigin_NilConfigRejectsStateChanging verifies that when getCfg returns
// nil, state-changing requests are rejected (fail-closed semantics).
func TestOrigin_NilConfigRejectsStateChanging(t *testing.T) {
	t.Parallel()
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return nil }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/dev/agent/token/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("nil config: expected 403, got %d", rec.Code)
	}
}

// TestOrigin_DeleteMismatchedRejected verifies DELETE with mismatched Origin
// is rejected.
func TestOrigin_DeleteMismatchedRejected(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodDelete, "/dev/agent/token/resource", nil)
	req.Header.Set("Origin", "http://attacker.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("DELETE with mismatched Origin: expected 403, got %d", rec.Code)
	}
}

// TestOrigin_PreviewPrefixExempt_NoOriginHeader verifies that a state-changing
// request under /preview/ is NOT rejected even with no Origin header at all —
// the prefix exemption is unconditional, not merely a relaxed match (ADR-044 /
// FR-012 / US-7 AS-3).
func TestOrigin_PreviewPrefixExempt_NoOriginHeader(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/preview/agent-1/some-token/submit", nil)
	// Deliberately no Origin header — a previewed app's own form POST would
	// not carry the SPA's origin.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("POST /preview/ with no Origin: expected 200 (exempt), got %d", rec.Code)
	}
	if !inner.called {
		t.Error("expected inner handler to be called for /preview/ prefix regardless of Origin")
	}
}

// TestOrigin_PreviewPrefixExempt_ForeignOrigin verifies that a /preview/
// request with a foreign Origin header still passes — the prefix exemption
// does not fall back to origin matching (ADR-044 / FR-012).
func TestOrigin_PreviewPrefixExempt_ForeignOrigin(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodDelete, "/preview/agent-1/some-token/resource", nil)
	req.Header.Set("Origin", "http://127.0.0.1:9999") // the previewed dev server's own loopback port
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("DELETE /preview/ with foreign Origin: expected 200 (exempt), got %d", rec.Code)
	}
	if !inner.called {
		t.Error("expected inner handler to be called for /preview/ prefix regardless of Origin")
	}
}

// TestOrigin_PreviewPrefixExempt_NilConfig verifies the exemption fires even
// when getCfg returns nil — the check for /preview/ happens before the
// fail-closed nil-config branch, so a boot-time config wiring gap cannot
// accidentally 403 a legitimate previewed-app request.
func TestOrigin_PreviewPrefixExempt_NilConfig(t *testing.T) {
	t.Parallel()
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return nil }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/preview/agent-1/some-token/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("POST /preview/ with nil config: expected 200 (exempt), got %d", rec.Code)
	}
	if !inner.called {
		t.Error("expected inner handler to be called for /preview/ prefix even with nil config")
	}
}

// TestOrigin_NonPreviewPathStillEnforced is a differentiation guard: a path
// that merely CONTAINS "preview" but isn't under the exact /preview/ prefix
// must still be origin-checked normally.
func TestOrigin_NonPreviewPathStillEnforced(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("localhost", 3000)
	inner := &passThroughHandler{}
	mw := RequireMatchingOriginOnStateChanging(func() *config.Config { return cfg }, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/preview-settings", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /api/v1/preview-settings with no Origin: expected 403 (not exempt), got %d", rec.Code)
	}
	if inner.called {
		t.Error("inner handler must not be called — this path is not under the /preview/ prefix")
	}
}

// TestCanonicalGatewayOrigin_HostPort verifies the origin string derivation for
// a non-standard port.
func TestCanonicalGatewayOrigin_HostPort(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("myhost", 8080)
	got := canonicalGatewayOrigin(cfg)
	want := "http://myhost:8080"
	if got != want {
		t.Errorf("canonicalGatewayOrigin = %q, want %q", got, want)
	}
}

// TestCanonicalGatewayOrigin_HTTPSPort verifies that port 443 produces https
// without the port number.
func TestCanonicalGatewayOrigin_HTTPSPort(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("secure.example.com", 443)
	got := canonicalGatewayOrigin(cfg)
	want := "https://secure.example.com"
	if got != want {
		t.Errorf("canonicalGatewayOrigin = %q, want %q", got, want)
	}
}

// TestCanonicalGatewayOrigin_FullURL verifies parsing when Host is already a
// full URL.
func TestCanonicalGatewayOrigin_FullURL(t *testing.T) {
	t.Parallel()
	cfg := buildOriginCfg("https://gateway.example.com", 443)
	got := canonicalGatewayOrigin(cfg)
	want := "https://gateway.example.com"
	if got != want {
		t.Errorf("canonicalGatewayOrigin = %q, want %q", got, want)
	}
}
