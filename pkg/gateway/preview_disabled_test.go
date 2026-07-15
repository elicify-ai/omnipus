//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-044 (preview-on-main-listener v5) replaced the separate preview
// listener with a single main-mux registration (FR-001/FR-002/FR-003).
// This file used to cover T2.22 ("preview listener disabled ⇒ /preview/
// absent from the main mux"); that contract no longer exists. The new
// contract: /preview/ is ALWAYS registered on the main mux, and
// gateway.preview_enabled gates it LIVE, per request, inside HandlePreview
// itself — disabling it 404s the request rather than un-registering the
// route (US-2, US-4, FR-003, FR-006).

package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestNoSeparatePreviewListener verifies FR-003 (US-2 AS-1,2): there is no
// second listener/mux/server backing /preview/ anymore — it shares
// channels.Manager's single httpServer/mux, the same as every other route.
func TestNoSeparatePreviewListener(t *testing.T) {
	// Struct-shape proof: channels.Manager must not declare a previewMux or
	// previewServer field. reflect.Type.Field walks field NAMES regardless of
	// export status, so this fails loudly if either field is reintroduced.
	mgrType := reflect.TypeOf(channels.Manager{})
	for i := 0; i < mgrType.NumField(); i++ {
		name := mgrType.Field(i).Name
		if name == "previewMux" || name == "previewServer" {
			t.Errorf("channels.Manager must not declare a %q field (FR-003: no separate preview listener)", name)
		}
	}

	// Method-shape proof: the separate-listener API surface must be gone too.
	mgrPtrType := reflect.TypeOf(&channels.Manager{})
	for _, method := range []string{"SetupPreviewServer", "RegisterPreviewHandler", "WrapPreviewHandler"} {
		if _, ok := mgrPtrType.MethodByName(method); ok {
			t.Errorf("channels.Manager must not have a %s method (FR-003: no separate preview listener)", method)
		}
	}
}

// upstreamPort extracts the numeric port from an httptest.Server URL for use
// with sandbox.DevServerRegistry.Register (which stores a bare int32 port).
func upstreamPort(t *testing.T, rawURL string) int32 {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	var port int32
	_, scanErr := fmt.Sscanf(u.Port(), "%d", &port)
	require.NoError(t, scanErr)
	return port
}

// TestPreviewServedOnMainMux verifies FR-001/FR-002 (US-1 AS-3, S4): /preview/
// is registered on the MAIN mux via httpHandlerRegistrar — the exact same
// registration interface every other route uses (registerAdditionalEndpoints,
// etc.) — and BOTH GET (static-file registration) and POST (dev-server
// registration, reverse-proxied) reach HandlePreview. A catch-all SPA/API
// handler is registered alongside it, matching production's registration
// order, so a 200/201 here is provably HandlePreview and not an accidental
// catch-all hit.
func TestPreviewServedOnMainMux(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	mainMux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mainMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SPA"))
	})

	srv := httptest.NewServer(mainMux)
	t.Cleanup(srv.Close)

	// --- GET: static-file registration ---
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "index.html"), []byte("<h1>main-mux get</h1>"), 0o644))
	getToken, _, err := ss.Register("mainmux-agent", workDir, time.Hour)
	require.NoError(t, err)

	getResp, err := http.Get(srv.URL + "/preview/mainmux-agent/" + getToken + "/index.html")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	assert.Equal(t, http.StatusOK, getResp.StatusCode,
		"GET /preview/ on the main mux must reach HandlePreview (FR-001/FR-002), not the SPA/API catch-all")

	// --- POST: dev-server registration (reverse proxy) ---
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method, "upstream dev server must see the proxied POST")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("dev-server got the POST"))
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devReg, err := reg.Register("mainmux-agent", upstreamPort(t, upstream.URL), 0 /*pid*/, "npm run dev", 10)
	require.NoError(t, err)

	postResp, err := http.Post(srv.URL+"/preview/mainmux-agent/"+devReg.Token+"/submit", "application/json", nil)
	require.NoError(t, err)
	defer func() { _ = postResp.Body.Close() }()
	assert.Equal(t, http.StatusCreated, postResp.StatusCode,
		"POST /preview/ on the main mux must reverse-proxy to the dev server (FR-001/FR-002)")
}

// TestUnknownTokenReturns404 verifies S22 (traces FR-001): an unknown/expired
// token on the main-mux-registered /preview/ route 404s — distinct from the
// preview-disabled 404 (see TestPreviewDisabledReturns404), which fires
// before any token lookup happens at all.
func TestUnknownTokenReturns404(t *testing.T) {
	api, _ := newPreviewRouteTestAPI(t)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	srv := httptest.NewServer(mainMux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/preview/some-agent/completely-unknown-token/index.html")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"unknown token on the main-mux /preview/ route must 404 (S22)")
}

// TestPreviewDisabledReturns404 verifies FR-006 / US-4 AS-1,2 (S5): when
// gateway.preview_enabled is false, ANY /preview/ request 404s — including a
// request bearing a token that IS validly registered, proving the gate fires
// before token lookup (not merely coincidentally matching an already-404
// unknown-token case).
func TestPreviewDisabledReturns404(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "index.html"), []byte("hi"), 0o644))
	token, _, err := ss.Register("disabled-agent", workDir, time.Hour)
	require.NoError(t, err)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	srv := httptest.NewServer(mainMux)
	t.Cleanup(srv.Close)

	path := "/preview/disabled-agent/" + token + "/index.html"

	// Sanity: enabled (default nil == true) serves the file.
	enabledResp, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	_ = enabledResp.Body.Close()
	require.Equal(t, http.StatusOK, enabledResp.StatusCode,
		"precondition: the token must serve successfully while preview is enabled")

	// Disable live — no restart, no re-registration.
	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(false)

	disabledResp, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	defer func() { _ = disabledResp.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, disabledResp.StatusCode,
		"a VALID token must still 404 while gateway.preview_enabled=false (FR-006)")
}
