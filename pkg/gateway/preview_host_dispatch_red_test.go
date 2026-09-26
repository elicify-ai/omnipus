// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED tests for ADR-094 preview isolation, orders 2, 3, 4,
// 5, 8 and DS-3's fall-through rows 11–12 of order 9 (spec:
// docs/internal/specs/adr-094-preview-isolation-spec.md).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: a request whose Host is <label>.localhost:<canonical-port>
//     reaches a preview-host mux mounting exactly one handler — preview
//     serving; no gateway API/auth handler is reachable under a preview Host;
//     the CSRF gate skips preview-Host requests; hot-flip retires Mode 1; the
//     Host is lower-cased before the registry lookup; grammar-invalid label
//     hosts never dispatch; on a non-Mode-1 deployment every label-class URL
//     404s via the empty registry.
//   - Specification source: FR-006/FR-007/FR-028, S-2.2, S-2.5–S-2.7,
//     S-2.9–S-2.11, F-1/F-2, DS-1, DS-3, DS-6 row 15. Expected values are the
//     spec's, never observed output.
//   - Unit boundary: REAL production chain — buildProductionMiddlewareChain
//     (CSRF outermost over configSnapshot over the mux; the exact gateway.go
//     wrap order, see preview_csrf_realmux_test.go) around a real mux
//     registered exactly like production (registerPreviewEndpoints +
//     api.withAuth(api.HandleAgents)), driven by a real httptest.Server.
//     Spies are counters wrapping the handlers AT the production paths, so
//     "spy did not run" proves "no gateway handler ran" (S-2.6's provably
//     not invoked). Labels come from the one contract-fixed mint surface
//     (the serve_web tool's isolated_url, FR-022) or, where the claim is the
//     pre-dispatch hole itself, from a grammar-valid notional label.
//   - Mutations: M-4 (drop the Host lower-casing) must flip order 5's
//     upper-case rows red. M-1/M-2 are order 25's instruments (their own
//     file); M-6/M-7 target other files.
//   - Known gaps (documented): the per-label rate-limit clause of FR-028 is
//     order 19's dedicated row (preview_lifecycle_rate_red_test.go). The
//     dev-mode clause of order 3 needs the tool's dev path, which is
//     Linux-gated (Tier3UnsupportedMessage on darwin) — that subtest is RED
//     at its named Linux-gate line on this host and green-able on Linux CI.

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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// piRedDispatchHarness wires the real production middleware chain around a
// real mux with production-identical registrations, plus invocation spies at
// the gateway handler paths.
type piRedDispatchHarness struct {
	api *restAPI
	srv *httptest.Server

	// apiSpyHits / authSpyHits count gateway-handler invocations at the
	// production paths — the S-2.6 "provably not invoked" instrument.
	apiSpyHits  *atomic.Int32
	authSpyHits *atomic.Int32

	port       string // the real listener port == the canonical origin's port
	sessionTok string // plaintext session cookie value for the wired user
}

// piRedNewDispatchHarness builds the harness. Canonical origin is
// http://localhost:<listener-port> (DS-3 row 1's Mode 1 shape); a session
// user is wired so a session cookie authenticates (the wave3 bearer-auth
// test's exact wiring).
func piRedNewDispatchHarness(t *testing.T) *piRedDispatchHarness {
	t.Helper()

	// The env token would authenticate requests without any cookie and make
	// the spy assertions ambiguous — pin it off like wave3 does.
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	api, ss := newPreviewRouteTestAPI(t)
	h := &piRedDispatchHarness{
		api:         api,
		apiSpyHits:  &atomic.Int32{},
		authSpyHits: &atomic.Int32{},
	}
	cfg := api.agentLoop.GetConfig()

	// Session user wired on the LIVE config (bcrypt of a fresh token) — the
	// browser session-cookie auth path (auth.go's ResolveUserFromCookie
	// fallback inside checkBearerAuth).
	plaintext, hash, err := middleware.MintSessionToken()
	require.NoError(t, err)
	h.sessionTok = plaintext
	cfg.Gateway.Users = []config.UserConfig{{
		Username:         "pi-red-user",
		SessionTokenHash: config.BcryptHash(hash),
	}}

	mainMux := http.NewServeMux()

	// Production registration shape: "/api/v1/agents" → withAuth(HandleAgents)
	// on the main mux. The spy wraps the REAL handler so "spy==0" proves the
	// gateway agent-list handler never ran.
	mainMux.HandleFunc("/api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		h.apiSpyHits.Add(1)
		api.withAuth(api.HandleAgents)(w, r)
	})

	// "/auth/session" spy — records any gateway auth-handler invocation.
	mainMux.HandleFunc("/auth/session", func(w http.ResponseWriter, r *http.Request) {
		h.authSpyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authenticated":true}`))
	})

	// Preview endpoints exactly as production registers them.
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})

	chain := buildProductionMiddlewareChain(api, mainMux)
	h.srv = httptest.NewServer(chain)
	t.Cleanup(h.srv.Close)

	h.port = fmt.Sprintf("%d", upstreamPort(t, h.srv.URL))

	// Mode 1 canonical origin on the real listener port (DS-3 row 1).
	cfg.Gateway.PublicURL = "http://localhost:" + h.port
	cfg.Gateway.PreviewEnabled = boolPtr(true)

	// The harness mints through its own tool instances against this store.
	_ = ss

	return h
}

// piRedDo issues a request against the harness server with an explicit Host
// header (the label-host instrument) and an optional session cookie.
func piRedDo(
	t *testing.T, h *piRedDispatchHarness, method, hostHeader, path string, withSession bool,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, nil)
	require.NoError(t, err)
	req.Host = hostHeader
	if withSession {
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: h.sessionTok})
	}
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// piRedBody drains a response body to a string.
func piRedBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// piRedLabelHost builds a Host header for a label (port appended by callers).
func piRedLabelHost(label string) string { return label + ".localhost" }

// piRedIsolatedURL runs the serve_web tool in the given mode against the
// harness's canonical origin and returns the parsed result payload.
func piRedIsolatedURL(
	t *testing.T, h *piRedDispatchHarness, agentID, inputCommand string, inputPort int32,
) map[string]any {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index.html"),
		[]byte("<h1>pi-red label host</h1>"),
		0o644,
	))

	var devReg *sandbox.DevServerRegistry
	if inputCommand != "" {
		reg := sandbox.NewDevServerRegistry()
		t.Cleanup(reg.Close)
		h.api.devServers = reg
		devReg = reg
	}

	tool := tools.NewWebServeTool(
		dir,
		agentID,
		func() *config.Config { return h.api.agentLoop.GetConfig() },
		h.api.servedSubdirs,
		devReg, // nil → static mode
		tools.WebServeDevConfig{
			Tier3Commands: []string{"python3"},
			PortRange:     [2]int32{18000, 18999},
			MaxConcurrent: 2,
		},
		nil, // egressProxy
		nil, // auditLogger
		60,
		86400,
	)

	input := map[string]any{"path": "."}
	if inputCommand != "" {
		input["command"] = inputCommand
		input["port"] = inputPort
	}
	result := tool.Execute(tools.WithAgentID(context.Background(), agentID), input)
	require.False(t, result.IsError, "web_serve must succeed: %s", result.ForLLM)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed),
		"web_serve result must be valid JSON")
	return parsed
}

// piRedMintStaticLabel mints a live static label through the serve_web tool
// (the one contract-fixed mint surface, FR-022) and returns it.
//
// RED today: the mint carries no isolated_url, so this helper's require
// names the missing dual-URL contract as the RED point for every live-label
// subtest.
func piRedMintStaticLabel(t *testing.T, h *piRedDispatchHarness, agentID string) string {
	t.Helper()
	parsed := piRedIsolatedURL(t, h, agentID, "", 0)

	fallback, _ := parsed["url"].(string)
	require.NotEmpty(t, fallback, "the /preview/ fallback url must stay present (FR-022)")

	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-001/FR-022): no isolated_url mint exists yet — the live-label surface of "+
			"this test is RED until the dual-URL contract lands")
	labelURL, _ := isoRaw.(string)
	require.NotEmpty(t, labelURL)

	// http://<label>.localhost:<port>/ → the label.
	u := strings.TrimPrefix(labelURL, "http://")
	u = strings.TrimSuffix(u, "/")
	parts := strings.SplitN(u, ".", 2)
	require.Len(t, parts, 2,
		"isolated_url must be <label>.localhost[:port], got %q", labelURL)
	return parts[0]
}

// piRedSpawnDevUpstream drives the DEV path for real: registers a tier3
// fixture command (python3 http.server) and calls Execute's dev variant so a
// REAL spawned upstream backs the dev registration. On darwin the tool's
// Linux gate returns an error result before any spawn — subtests using this
// helper carry their RED at that named line and are green-able on Linux CI.
func piRedSpawnDevUpstream(t *testing.T, h *piRedDispatchHarness, agentID string) string {
	t.Helper()
	const devPort = int32(18042)
	parsed := piRedIsolatedURL(t, h, agentID,
		fmt.Sprintf("python3 -m http.server %d", devPort), devPort)

	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-001 dev variant): the dev-mode web_serve result carries no isolated_url")
	labelURL, _ := isoRaw.(string)
	u := strings.TrimPrefix(labelURL, "http://")
	u = strings.TrimSuffix(u, "/")
	parts := strings.SplitN(u, ".", 2)
	require.Len(t, parts, 2,
		"dev isolated_url must be <label>.localhost[:port], got %q", labelURL)
	return parts[0]
}

// ---------------------------------------------------------------------------
// order 2 — THE canonical RED gate
// ---------------------------------------------------------------------------

// TestPreviewHostDispatch_REDAPIReachableToday proves the #798 Host-dispatch
// hole server-side: TODAY, with a live session cookie, GET
// http://<label>.localhost:<port>/api/v1/agents returns the gateway's agent
// list (200 + the handler spy fired). Post-GREEN the same request must 404
// via the preview mux (unknown label) and the gateway handler spy must stay
// at zero (FR-006/FR-007, S-2.2, S-2.5).
//
// Says nothing about CORS — isAllowedOrigin/wsCheckOrigin already refuse
// label origins today; that is order 25's pin (round-2 MAJ-004).
func TestPreviewHostDispatch_REDAPIReachableToday(t *testing.T) {
	h := piRedNewDispatchHarness(t)

	host := piRedLabelHost("pi-red-notional") + ":" + h.port
	resp := piRedDo(t, h, http.MethodGet, host, "/api/v1/agents", true)
	body := piRedBody(t, resp)

	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"RED (FR-006/FR-007, S-2.2, S-2.5): with a session cookie, GET /api/v1/agents under a "+
			"<label>.localhost Host returned %d — the gateway's agent list (the #798 Host-dispatch "+
			"hole). Post-GREEN the preview mux must answer 404 (no gateway handler reachable "+
			"under a preview Host)", resp.StatusCode)

	assert.Equal(t, int32(0), h.apiSpyHits.Load(),
		"S-2.6: the gateway agent-list handler must never run under a label Host")
	assert.NotContains(t, body, "pi-red-user",
		"the response must not carry gateway-authenticated content")
}

// ---------------------------------------------------------------------------
// order 3 — no gateway handler under a preview Host
// ---------------------------------------------------------------------------

// TestPreviewHostDispatch_NoGatewayHandler covers S-2.6: static mode —
// /api/v1/agents and /auth/session under a LIVE label Host answer a
// file-server 404 and the gateway API/auth handler spies never fire; dev
// mode — the label host serves the upstream app while the gateway spies
// stay at zero.
func TestPreviewHostDispatch_NoGatewayHandler(t *testing.T) {
	t.Run("static_live_label_api_and_auth_unreachable", func(t *testing.T) {
		h := piRedNewDispatchHarness(t)
		label := piRedMintStaticLabel(t, h, "pi-red-agent")
		host := piRedLabelHost(label) + ":" + h.port

		for _, path := range []string{"/api/v1/agents", "/auth/session"} {
			resp := piRedDo(t, h, http.MethodGet, host, path, true)
			body := piRedBody(t, resp)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode,
				"static mode: %s under a live label Host must be a file-server 404", path)
			assert.NotContains(t, body, "authenticated",
				"the auth spy's JSON must never surface under a label Host")
		}
		assert.Equal(t, int32(0), h.apiSpyHits.Load(),
			"S-2.6: the gateway agent-list handler is provably not invoked under a live label Host")
		assert.Equal(t, int32(0), h.authSpyHits.Load(),
			"S-2.6: the gateway auth handler is provably not invoked under a live label Host")
	})

	t.Run("dev_mode_label_host_serves_upstream_not_gateway", func(t *testing.T) {
		h := piRedNewDispatchHarness(t)
		label := piRedSpawnDevUpstream(t, h, "pi-red-dev-agent")
		host := piRedLabelHost(label) + ":" + h.port

		// The upstream app is reachable through the label host.
		resp := piRedDo(t, h, http.MethodGet, host, "/index.html", false)
		body := piRedBody(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"dev mode: the label host must serve the spawned upstream app")
		assert.Contains(t, body, "pi-red label host",
			"the response must be the upstream's content, forwarded by the dev proxy")

		// Gateway handlers stay unreachable under the label host.
		for _, path := range []string{"/api/v1/agents", "/auth/session"} {
			gwResp := piRedDo(t, h, http.MethodGet, host, path, true)
			gwBody := piRedBody(t, gwResp)
			assert.NotContains(t, gwBody, "authenticated",
				"dev mode: %s under a label Host must never surface gateway auth content", path)
		}
		assert.Equal(t, int32(0), h.apiSpyHits.Load(),
			"S-2.6: dev mode — the gateway agent-list handler is provably not invoked")
		assert.Equal(t, int32(0), h.authSpyHits.Load(),
			"S-2.6: dev mode — the gateway auth handler is provably not invoked")
	})
}

// ---------------------------------------------------------------------------
// order 4 — dispatch ordering (FR-028)
// ---------------------------------------------------------------------------

// TestPreviewHostDispatch_Ordering covers S-2.7 / FR-028: the CSRF gate does
// not run on preview-Host requests; the preview-host mux sees a live config
// snapshot (hot-flip). The per-label rate-limit clause is order 19's
// dedicated row (preview_lifecycle_rate_red_test.go).
func TestPreviewHostDispatch_Ordering(t *testing.T) {
	t.Run("csrf_gate_skips_preview_host_post", func(t *testing.T) {
		h := piRedNewDispatchHarness(t)
		label := piRedMintStaticLabel(t, h, "pi-red-agent")
		host := piRedLabelHost(label) + ":" + h.port

		// POST with a valid session cookie and NO X-Csrf-Token header under
		// the label Host: the CSRF gate must not refuse it (FR-028 — the
		// exemption is scoped by dispatch to the preview mux, not by the
		// /preview/ path prefix).
		resp := piRedDo(t, h, http.MethodPost, host, "/index.html", true)
		body := piRedBody(t, resp)

		assert.NotEqual(t, http.StatusForbidden, resp.StatusCode,
			"FR-028: a POST under a preview Host must not be CSRF-refused (got %d)",
			resp.StatusCode)
		assert.NotContains(t, body, "csrf",
			"the CSRF middleware's refusal body must never surface for preview-Host requests")
	})

	t.Run("hot_flip_retires_mode1_without_restart", func(t *testing.T) {
		h := piRedNewDispatchHarness(t)
		label := piRedMintStaticLabel(t, h, "pi-red-agent")
		host := piRedLabelHost(label) + ":" + h.port

		// Before the flip the label host serves the registration.
		resp := piRedDo(t, h, http.MethodGet, host, "/index.html", false)
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"RED (FR-022): no live label exists yet — the dual-URL mint is missing")

		// Hot-flip: preview_enabled=false retires Mode 1 without restart
		// (the mux sees a live config snapshot, S-2.7).
		h.api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(false)

		resp2 := piRedDo(t, h, http.MethodGet, host, "/index.html", false)
		assert.Equal(t, http.StatusNotFound, resp2.StatusCode,
			"S-2.7: after the hot-flip the label host must 404 with no restart")
	})
}

// ---------------------------------------------------------------------------
// order 5 — Host lower-casing before the registry lookup (F-2), plus DS-1's
// incoming-validation rows
// ---------------------------------------------------------------------------

// TestPreviewHostMux_LowerCaseLookup covers S-2.9 / F-2: an upper-case Host
// routes to the same registration as the lower-case label, and
// grammar-INVALID label hosts never dispatch to a registration (DS-1's
// incoming rows — pins: today nothing dispatches at all, and post-GREEN
// they must keep not dispatching).
func TestPreviewHostMux_LowerCaseLookup(t *testing.T) {
	h := piRedNewDispatchHarness(t)
	label := piRedMintStaticLabel(t, h, "pi-red-agent")

	// Upper-case Host rows (M-4's instrument): same registration, same file.
	for _, host := range []string{
		piRedLabelHost(strings.ToUpper(label)) + ":" + h.port,               // ALL-CAPS
		piRedLabelHost(strings.ToUpper(label[:1])+label[1:]) + ":" + h.port, // Title-cased
	} {
		resp := piRedDo(t, h, http.MethodGet, host, "/index.html", false)
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"RED (S-2.9/F-2): Host %q must route to the same registration as %q — the mux's "+
				"lower-casing (and the label dispatch itself) is not implemented",
			host, label+".localhost")
	}

	// DS-1 incoming-validation rows: underscore, leading hyphen, over-max
	// length — these must NEVER dispatch to a registration.
	for _, bad := range []string{"has_underscore", "-lead", strings.Repeat("a", 64)} {
		host := piRedLabelHost(bad) + ":" + h.port
		resp := piRedDo(t, h, http.MethodGet, host, "/index.html", false)
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"DS-1: grammar-invalid label host %q must not serve a registration", host)
		assert.Equal(t, int32(0), h.apiSpyHits.Load()+h.authSpyHits.Load(),
			"DS-1: a grammar-invalid label Host must not reach a gateway handler either")
	}
}

// ---------------------------------------------------------------------------
// order 8 — non-Mode-1 deployment: empty registry 404s (F-1)
// ---------------------------------------------------------------------------

// TestPreviewSSRF_EmptyRegistry404 covers S-2.10 / FR-007: on a non-Mode-1
// deployment (https-loopback canonical origin) EVERY label-class URL 404s
// via the empty label registry, and no gateway handler produced it.
func TestPreviewSSRF_EmptyRegistry404(t *testing.T) {
	h := piRedNewDispatchHarness(t)

	// Non-Mode-1 deployment: https-loopback canonical origin (DS-3 row 2).
	h.api.agentLoop.GetConfig().Gateway.PublicURL = "https://localhost:" + h.port

	host := piRedLabelHost("pi-red-notional") + ":" + h.port
	resp := piRedDo(t, h, http.MethodGet, host, "/api/v1/agents", true)
	piRedBody(t, resp)

	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"RED (S-2.10/FR-007): on a non-Mode-1 deployment a label-class URL must 404 via the "+
			"empty registry — today it reaches the main mux instead (got %d)", resp.StatusCode)
	assert.Equal(t, int32(0), h.apiSpyHits.Load(),
		"S-2.10: no gateway handler may produce the response")
}

// ---------------------------------------------------------------------------
// order 9's DS-3 fall-through rows 11–12
// ---------------------------------------------------------------------------

// TestCanonicalOriginFallThrough covers DS-3 rows 11–12 / S-2.11: a label
// Host on the WRONG port (or portless against an explicit-port origin) does
// not claim the preview mux — it falls through to the main mux exactly as
// today, and a state-changing fall-through request stays fully CSRF-gated
// (FR-028: the CSRF exemption applies iff the request is dispatched to the
// preview-host mux).
func TestCanonicalOriginFallThrough(t *testing.T) {
	h := piRedNewDispatchHarness(t)
	label := piRedMintStaticLabel(t, h, "pi-red-agent")

	t.Run("wrong_port_falls_through", func(t *testing.T) {
		// DS-3 row 11: Host <label>.localhost:9999 vs canonical listener port.
		host := piRedLabelHost(label) + ":9999"
		resp := piRedDo(t, h, http.MethodGet, host, "/index.html", false)
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"DS-3 row 11: a wrong-port label Host must not reach the registration")
	})

	t.Run("portless_host_falls_through", func(t *testing.T) {
		// DS-3 row 12: portless Host vs explicit-port canonical origin.
		host := piRedLabelHost(label)
		resp := piRedDo(t, h, http.MethodGet, host, "/index.html", false)
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"DS-3 row 12: a portless label Host must not reach the registration")
	})

	t.Run("fall_through_stays_csrf_gated", func(t *testing.T) {
		// S-2.11 (FR-028, round-2 MIN-003): CSRF is skipped IFF the request
		// is dispatched to the preview-host mux.
		host := piRedLabelHost(label) + ":9999"
		resp := piRedDo(t, h, http.MethodPost, host, "/api/v1/agents", true)
		body := piRedBody(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode,
			"S-2.11: a state-changing request with a fall-through Host and no X-Csrf-Token "+
				"IS CSRF-refused")
		assert.Contains(t, body, "csrf",
			"the fall-through refusal must be the CSRF middleware's, not something else")
	})
}
