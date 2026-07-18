//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — integration test closing a coverage gap identified by
// pr-test-analyzer (M3) for the preview-on-main-listener feature (ADR-044):
// no existing test chains the web_serve tool's Execute() output through a
// REAL HTTP request against the gateway's registered /preview/ handler.
//
//   - pkg/tools/web_serve_test.go and rest_preview_proof_test.go's
//     TestWebServeTool_StaticResultIncludesPath only assert on the tool's
//     returned JSON shape (kind/path/url/expires_at) — no HTTP round trip.
//   - preview_disabled_test.go's TestPreviewServedOnMainMux registers
//     directly into ServedSubdirs/DevServerRegistry, bypassing the
//     WebServeTool entirely.
//
// Neither proves the URL the tool actually MINTS is the URL
// registerPreviewEndpoints/HandlePreview actually SERVES — a
// URL-construction-vs-routing mismatch could slip through both suites
// individually while breaking the real feature end to end. This file closes
// that gap.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestServeWebEndToEnd chains the real path for BOTH web_serve modes: it
// registers /preview/ on a REAL mux backed by a REAL httptest.Server (the
// exact registration interface production uses — registerPreviewEndpoints),
// then proves a WebServeTool's minted URL actually resolves through that
// live server.
//
//   - StaticMode_FullExecuteToHTTPChain drives the UNMODIFIED path: builds a
//     real WebServeTool, calls Execute() (static mode — no process spawn, so
//     it's cheap and deterministic), takes the URL IT returns, and issues a
//     real net/http GET against it. This is the literal "call Execute, then
//     HTTP GET the result" chain the M3 gap review asked for.
//   - DevMode_FocusedURLRoutingProof is the FOCUSED equivalent for dev mode
//     (explicitly permitted when the full chain is prohibitive — see its own
//     doc comment for why executeDev's real spawn path is not driven here).
//     It registers a fake dev server (an httptest backend) directly into the
//     SAME DevServerRegistry executeDev uses, builds the URL with the
//     IDENTICAL formula executeDev uses, and proves that URL resolves
//     through the real HandlePreview route and proxies to the fake dev
//     server (status + body).
func TestServeWebEndToEnd(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)
	cfg := api.agentLoop.GetConfig()

	// Register /preview/ on a REAL mux, matching production's registration
	// path exactly (registerAdditionalEndpoints calls registerPreviewEndpoints
	// the same way TestPreviewServedOnMainMux already proves).
	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	mainMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mainMux)
	t.Cleanup(srv.Close)

	// Point the config's canonical gateway origin at the REAL test server's
	// actual address so the tool mints a URL this test can literally dial —
	// this is what makes the test an end-to-end proof rather than two
	// isolated assertions that merely look consistent.
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = int(upstreamPort(t, srv.URL))
	trueVal := true
	cfg.Gateway.PreviewEnabled = &trueVal

	t.Run("StaticMode_FullExecuteToHTTPChain", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(workDir, "index.html"),
			[]byte("<h1>end-to-end preview</h1>"),
			0o644,
		))

		tool := tools.NewWebServeTool(
			workDir,
			"e2e-static-agent",
			func() *config.Config { return cfg }, // live accessor — same cfg pointer HandlePreview reads
			ss,
			nil, // devReg — static mode only
			tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
			nil, // egressProxy
			nil, // auditLogger
			60,
			86400,
		)

		ctx := tools.WithAgentID(context.Background(), "e2e-static-agent")
		result := tool.Execute(ctx, map[string]any{"path": "."})
		require.False(t, result.IsError, "web_serve Execute must succeed: %s", result.ForLLM)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed),
			"web_serve result must be valid JSON")
		mintedURL, _ := parsed["url"].(string)
		require.NotEmpty(t, mintedURL, "web_serve result must include a url")
		require.True(t, strings.HasPrefix(mintedURL, srv.URL),
			"minted URL %q must be rooted at the real test server's origin %q — otherwise the "+
				"URL-construction formula and the live gateway origin have silently diverged",
			mintedURL, srv.URL)

		// The REAL end-to-end proof: dial the URL the tool minted, against the
		// REAL mux HandlePreview is registered on.
		resp, err := http.Get(mintedURL + "index.html")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"a GET against the tool's OWN minted URL must reach HandlePreview and succeed — a "+
				"URL-construction-vs-routing mismatch would 404 here even though the URL format "+
				"and the route registration each look correct in isolation")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "end-to-end preview",
			"the proxied response must contain the actual file content written to the workspace")
	})

	// DevMode_FocusedURLRoutingProof is the FOCUSED equivalent for dev mode —
	// not a literal Execute() call.
	//
	// executeDev's real path (pkg/tools/web_serve.go) spawns an actual OS
	// child process: tier3 allow-list validation → sandbox.SpawnBackgroundChild
	// → a 3s startup grace period → a TCP probe of the bound port — before it
	// ever reaches the URL-construction step this test cares about. Driving
	// that whole path in a unit test would require a real spawnable
	// dev-server binary on PATH and several seconds of sleep per run — the
	// "prohibitive scaffolding" case the M3 gap review anticipated. No
	// existing test in this repo drives executeDev's spawn path either (see
	// pkg/tools/web_serve_test.go: every dev-mode test there either passes a
	// nil devReg or stops at the pre-spawn allow-list/registry-check
	// branches).
	//
	// Instead, this proves the actual defect class M3 flags — a
	// URL-construction-vs-routing mismatch — for the dev-mode path
	// specifically: it registers a fake dev server directly into the SAME
	// DevServerRegistry executeDev uses (mirroring exactly what
	// devReg.ConfirmReservation leaves behind after a real spawn succeeds),
	// builds the URL with the IDENTICAL formula executeDev uses —
	// fmt.Sprintf("/preview/%s/%s/", agentID, token) +
	// middleware.CanonicalGatewayOrigin(cfg), see pkg/tools/web_serve.go's
	// executeDev — and issues a REAL HTTP GET against the REAL main-mux
	// HandlePreview registration, asserting the response proxies through to
	// a real httptest dev-server backend (status + body).
	t.Run("DevMode_FocusedURLRoutingProof", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake dev server response"))
		}))
		t.Cleanup(upstream.Close)

		devRegistry := sandbox.NewDevServerRegistry()
		t.Cleanup(devRegistry.Close)
		api.devServers = devRegistry

		agentID := "e2e-dev-agent"
		devReg, err := devRegistry.Register(agentID, upstreamPort(t, upstream.URL), 0 /*pid*/, "npm run dev", 10)
		require.NoError(t, err)

		// IDENTICAL URL-construction formula to executeDev (web_serve.go).
		path := fmt.Sprintf("/preview/%s/%s/", agentID, devReg.Token)
		mintedURL := middleware.CanonicalGatewayOrigin(cfg) + path

		resp, err := http.Get(mintedURL)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"the dev-mode URL formula must resolve through the real HandlePreview route to the "+
				"fake dev server — a URL-construction-vs-routing mismatch would 404 or misroute here")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "fake dev server response",
			"the proxied response must be the fake dev server's actual body, proving the reverse "+
				"proxy wiring works end to end for the dev-mode URL shape")
	})
}
