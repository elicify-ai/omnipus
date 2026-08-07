// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T2.23 → ADR-044 (preview-on-main-listener v5): under the OLD design,
// /preview/ lived on a separate listener wired once at boot, so a runtime
// toggle of preview_listener_enabled could never take effect without a
// restart (see git history for the old BLOCKED test this file used to hold,
// tracked under issue #155). ADR-044 replaces that with a single main-mux
// registration plus a LIVE gateway.preview_enabled check inside HandlePreview
// itself (FR-006). This file now proves the hot-flip actually works: no
// restart, no re-registration, and — critically — a running dev server is
// NOT force-killed when preview is disabled (US-4 AS-4).
//
// Traces to: preview-on-main-listener-spec.md FR-006, FR-007, US-4, S5, S7.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestPreviewToggleHotReload verifies FR-006/FR-007 (US-4, S5, S7): toggling
// gateway.preview_enabled at runtime flips /preview/'s behavior on the very
// next request — no restart, no mux re-registration — and disabling it does
// NOT tear down an already-running dev server (it stays registered and would
// still be reachable the instant preview is re-enabled).
func TestPreviewToggleHotReload(t *testing.T) {
	api, _ := newPreviewRouteTestAPI(t)

	// Register a real dev-server-backed token so we can prove the underlying
	// registration survives the disable step untouched.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("dev server alive"))
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devReg, err := reg.Register("hotflip-agent", upstreamPort(t, upstream.URL), 0 /*pid*/, "npm run dev", 10)
	require.NoError(t, err)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	srv := httptest.NewServer(mainMux)
	t.Cleanup(srv.Close)

	path := "/preview/hotflip-agent/" + devReg.Token + "/"

	// Step 1: enabled (default) — the mux/registration is set up exactly
	// ONCE, before any of the toggling below.
	resp1, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	_ = resp1.Body.Close()
	require.Equal(t, http.StatusOK, resp1.StatusCode,
		"step 1: preview must serve 200 while enabled (no code change since registration)")

	// Step 2: disable live. No re-registration, no restart — just flip the
	// config field the same way a PUT /api/v1/config/gateway would.
	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(false)

	resp2, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode,
		"step 2: /preview/ must 404 immediately after preview_enabled=false — no restart required")

	// Step 2b: the dev-server registration itself must survive the disable
	// untouched — disabling preview_enabled must NOT force-kill running dev
	// servers (US-4 AS-4). Lookup still finds it in the registry.
	stillRegistered := reg.Lookup(devReg.Token)
	require.NotNil(t, stillRegistered,
		"step 2b: disabling preview_enabled must not unregister/kill the running dev server")
	assert.Equal(t, "hotflip-agent", stillRegistered.AgentID)

	// Step 2c: the upstream dev server process (simulated by the httptest
	// server) is still reachable directly — proving it was never killed, only
	// made unreachable THROUGH the gateway's /preview/ gate.
	directResp, err := http.Get(upstream.URL)
	require.NoError(t, err)
	_ = directResp.Body.Close()
	assert.Equal(t, http.StatusOK, directResp.StatusCode,
		"step 2c: the dev server process itself must still be running (not force-killed)")

	// Step 3: re-enable live. The SAME registration (never re-created) must
	// serve immediately — proving the toggle is genuinely live, not a
	// one-way trip requiring a fresh registration/restart.
	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)

	resp3, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	_ = resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode,
		"step 3: /preview/ must serve again immediately after preview_enabled=true — no restart, same registration")
}

// TestPreviewToggleHotReload_StaticToken mirrors the above for a static-file
// (Tier 1) registration rather than a dev-server (Tier 3) one, so the
// hot-flip contract is proven for both registry types HandlePreview consults.
func TestPreviewToggleHotReload_StaticToken(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "index.html"), []byte("static hotflip"), 0o644))
	token, _, err := ss.Register("hotflip-static-agent", workDir, time.Hour)
	require.NoError(t, err)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	srv := httptest.NewServer(mainMux)
	t.Cleanup(srv.Close)

	path := "/preview/hotflip-static-agent/" + token + "/index.html"

	resp1, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	_ = resp1.Body.Close()
	require.Equal(t, http.StatusOK, resp1.StatusCode)

	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(false)
	resp2, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode,
		"static-mode /preview/ must also 404 immediately when disabled")

	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)
	resp3, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	_ = resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode,
		"static-mode /preview/ must serve again immediately when re-enabled")
}
