//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — closes PTA-2 (7-reviewer gate, pass 2, feat/preview-on-main-listener):
// no existing test wires the ACTUAL PRODUCTION middleware chain (CSRFMiddleware +
// configSnapshotMiddleware) around a REAL mux populated by registerPreviewEndpoints
// and proves the /preview/ CSRF-exemption boundary end to end.
//
// preview_disabled_test.go's TestPreviewServedOnMainMux and
// rest_preview_web_serve_e2e_test.go's TestServeWebEndToEnd both build a real
// mux via registerPreviewEndpoints + testMuxRegistrar and drive it with a real
// httptest.Server/client — but neither wraps the mux in ANY middleware, so
// neither can prove the CSRF exemption for /preview/ is real (as opposed to
// merely documented) or that /api/v1/* on the SAME wired mux still enforces
// CSRF. middleware/csrf_test.go's TestCSRF_PreviewPrefixExempt_AllMethods
// proves the exemption in isolation (a bare CSRFMiddleware wrapping a stub
// handler, not the actual registerPreviewEndpoints route table).
//
// Per the lead's design note: RequireMatchingOriginOnStateChanging (the
// Origin middleware) is intentionally NOT wired in production and must be
// treated as dead code for this test — it is not part of the chain built
// here. The only production cross-origin defense wired around the mux is
// the double-submit CSRFMiddleware (+ configSnapshotMiddleware, which
// CSRFMiddleware's Reporter path does not require but production always
// installs alongside it — see gateway.go:2191-2252).
//
// buildProductionMiddlewareChain below reproduces gateway.go's exact wrap
// order. gateway.go calls channels.Manager.WrapHTTPHandler twice:
//
//  1. WrapHTTPHandler(api.configSnapshotMiddleware)  // gateway.go:2193
//  2. WrapHTTPHandler(csrfMW)                         // gateway.go:2250
//
// WrapHTTPHandler's semantics (pkg/channels/manager.go:761) are
// `m.httpServer.Handler = middleware(m.httpServer.Handler)` — each call
// wraps the CURRENT handler, so the handler installed LAST ends up
// OUTERMOST. The resulting request order is exactly what gateway.go's own
// comment (line 2205) states: "CSRF check → configSnapshot injection → mux
// dispatch → auth check in handler".
package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// buildProductionMiddlewareChain wires the SAME production middleware chain
// gateway.go installs around the main mux (see the file-level doc comment for
// the exact wrap-order citation). It deliberately does NOT include
// middleware.RequireMatchingOriginOnStateChanging — that helper is not wired
// in production (lead decision, CR-1/ARCH-1) and including it here would
// test a chain that does not exist in the running gateway.
func buildProductionMiddlewareChain(api *restAPI, mux http.Handler) http.Handler {
	csrfMW := middleware.CSRFMiddleware()
	// Inner-first: configSnapshotMiddleware wraps mux, THEN csrfMW wraps
	// that — matching WrapHTTPHandler(configSnapshotMiddleware) being called
	// before WrapHTTPHandler(csrfMW) in gateway.go.
	return csrfMW(api.configSnapshotMiddleware(mux))
}

// newWiredPreviewRealMux builds a real *http.ServeMux populated by the
// ACTUAL production registerPreviewEndpoints call (not a hand-rolled route),
// plus a trivial state-changing /api/v1/... handler so test (b) below has a
// non-preview mutation route to exercise on the SAME mux, and a catch-all
// "/" 404 responder (mirrors TestPreviewServedOnMainMux's registration
// order), then wraps the whole thing in the production middleware chain and
// serves it from a real httptest.Server.
func newWiredPreviewRealMux(t *testing.T) (*restAPI, *httptest.Server) {
	t.Helper()
	api, _ := newPreviewRouteTestAPI(t)
	// Pin PreviewEnabled explicitly (true) so this test's outcome never
	// depends on IsPreviewEnabled's nil-receiver default polarity — a
	// separate, in-flight finding (TDA-1) — one way or the other.
	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})

	// A trivial state-changing /api/v1/... handler, reachable on the exact
	// same wired mux as /preview/, so test (b) proves the CSRF gate is
	// genuinely active for non-preview routes rather than merely absent
	// from this minimal test mux.
	mainMux.HandleFunc("/api/v1/test-state-changing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("state-changed"))
	})
	mainMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	chain := buildProductionMiddlewareChain(api, mainMux)
	srv := httptest.NewServer(chain)
	t.Cleanup(srv.Close)
	return api, srv
}

// TestPreviewCSRFRealMux_StateChangingPreviewProxiesWithoutCSRF verifies
// PTA-2(a): a state-changing (POST) request to a REAL, registered
// /preview/<agent>/<token>/... dev-server proxy passes through the wired
// production middleware chain WITHOUT any CSRF cookie or X-Csrf-Token
// header — proving the /preview/ prefix exemption is genuinely active in
// the real chain, not just documented — and actually reaches HandlePreview,
// which reverse-proxies it to the upstream dev server (a 201 from the real
// upstream, not a coincidental 200 from some other handler).
func TestPreviewCSRFRealMux_StateChangingPreviewProxiesWithoutCSRF(t *testing.T) {
	api, srv := newWiredPreviewRealMux(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method, "upstream dev server must see the proxied POST")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("dev-server got the POST"))
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devReg, err := reg.Register("csrf-realmux-agent", upstreamPort(t, upstream.URL), 0 /*pid*/, "npm run dev", 10)
	require.NoError(t, err)

	// Deliberately no CSRF cookie, no X-Csrf-Token header, no Authorization
	// header at all.
	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/preview/csrf-realmux-agent/"+devReg.Token+"/submit", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.NotEqual(t, http.StatusForbidden, resp.StatusCode,
		"a POST to /preview/ on the wired real mux must NOT be CSRF-rejected (403) — the /preview/ "+
			"prefix is exempt from the double-submit check")
	assert.Equal(t, http.StatusCreated, resp.StatusCode,
		"the POST must actually reach HandlePreview and be reverse-proxied to the upstream dev "+
			"server (proving it passed the wired CSRF gate for real, not just avoided a 403 by luck)")
}

// TestPreviewCSRFRealMux_StateChangingPreviewUnknownTokenNotCSRFBlocked is
// the negative-token companion to the proxying test above: a state-changing
// POST to /preview/ with a token that is NOT registered anywhere still
// passes through the CSRF gate (no cookie/header supplied) and is rejected
// by HandlePreview's OWN token-lookup logic (404), never by the CSRF
// middleware (403). This distinguishes "the CSRF gate let it through, and
// something downstream legitimately said no" from "the CSRF gate itself
// blocked it" — the two would otherwise be indistinguishable if the only
// preview fixture available always resolved to a registered token.
func TestPreviewCSRFRealMux_StateChangingPreviewUnknownTokenNotCSRFBlocked(t *testing.T) {
	_, srv := newWiredPreviewRealMux(t)

	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/preview/some-agent/completely-unknown-token/submit", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.NotEqual(t, http.StatusForbidden, resp.StatusCode,
		"an unknown-token POST to /preview/ must not be CSRF-rejected — the exemption applies "+
			"unconditionally by path prefix, before any token lookup")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"HandlePreview's own token lookup (not the CSRF gate) must produce the 404 for an unknown token")
}

// TestPreviewCSRFRealMux_NonPreviewAPIRouteStillRequiresCSRF verifies
// PTA-2(b): on the EXACT SAME wired real mux (same CSRFMiddleware instance,
// same registerPreviewEndpoints registration), a state-changing request to
// a NON-preview /api/v1/... path WITHOUT a CSRF cookie/header IS rejected
// with 403 by the CSRF middleware. This proves the /preview/ exemption seen
// above is scoped narrowly to the /preview/ prefix — it is not, for
// example, a global bypass that accidentally disabled CSRF enforcement
// mux-wide — and that the middleware chain built here is genuinely active
// rather than a no-op wrapper.
func TestPreviewCSRFRealMux_NonPreviewAPIRouteStillRequiresCSRF(t *testing.T) {
	_, srv := newWiredPreviewRealMux(t)

	resp, err := http.Post(srv.URL+"/api/v1/test-state-changing", "application/json", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"/api/v1/* on the SAME wired mux as /preview/ must still enforce CSRF — the exemption "+
			"must not bleed outside the /preview/ prefix")
	body := readBody(t, resp)
	assert.Contains(t, body, "csrf", "403 body must indicate a CSRF rejection, not some other 403")
}

// TestPreviewCSRFRealMux_NonPreviewAPIRoutePassesWithValidCSRF is the
// positive control for the test above: the SAME /api/v1/... route, on the
// SAME wired mux, DOES succeed when a matching CSRF cookie+header pair is
// supplied. Without this control, a mux-registration bug that made
// /api/v1/test-state-changing 403 unconditionally (regardless of CSRF
// state) would pass the negative test above for the wrong reason — this
// differentiates "CSRF specifically rejected the missing-token case" from
// "this route always 403s".
func TestPreviewCSRFRealMux_NonPreviewAPIRoutePassesWithValidCSRF(t *testing.T) {
	_, srv := newWiredPreviewRealMux(t)

	const token = "realmux-csrf-token-value"
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/test-state-changing", nil)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieNameHTTP, Value: token})
	req.Header.Set(middleware.CSRFHeaderName, token)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a matching CSRF cookie+header pair on the wired real mux must reach the actual handler")
	assert.Equal(t, "state-changed", readBody(t, resp))
}

// readBody reads and returns resp.Body as a string, failing the test on a
// read error. Small helper to keep the assertions above terse.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// TestPreviewCSRFRealMux_GETPreviewAlsoBypassesCSRF is a sanity companion
// proving the exemption is not method-specific in some accidental way — a
// safe-method GET to /preview/ also bypasses CSRF (as it always would, since
// GET is never gated to begin with), confirming the static-file registration
// path used by other preview tests in this package is reachable through the
// SAME wired chain used above for the dev-proxy (POST) path.
func TestPreviewCSRFRealMux_GETPreviewAlsoBypassesCSRF(t *testing.T) {
	api, srv := newWiredPreviewRealMux(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "index.html"), []byte("<h1>realmux get</h1>"), 0o644))

	token, _, err := api.servedSubdirs.Register("csrf-realmux-get-agent", workDir, time.Hour)
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/preview/csrf-realmux-get-agent/" + token + "/index.html")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"GET /preview/ on the wired real mux must reach HandlePreview and serve the file")
	assert.Contains(t, readBody(t, resp), "realmux get")
}
