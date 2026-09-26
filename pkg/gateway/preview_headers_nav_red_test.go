// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED tests for ADR-094 preview isolation, orders 14, 15
// and 16 (FR-012, FR-014, FR-010, FR-011, DS-7, S-2.1, S-2.3, S-2.8, S-2.13;
// spec: docs/internal/specs/adr-094-preview-isolation-spec.md).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: Mode 2 preview responses carry the spec's CSP
//     template byte-identically (static tripwire oracle); preflights under
//     the token prefix answer exactly GET, HEAD, OPTIONS with no
//     allow-headers wildcard and successful responses carry ACAO:* / no
//     ACAC; main-Host /api/v1 document navigations are rejected before the
//     handler except the named GET-only exemptions; /preview/ refuses
//     service-worker requests.
//   - Specification source: the CSP template block (spec "Mode 2 response
//     headers"), FR-012, FR-010's exemption list verbatim, FR-011, DS-7,
//     S-2.8, S-2.13. Expected values are transcribed from the spec, never
//     observed output.
//   - Unit boundary: REAL production chain (buildProductionMiddlewareChain),
//     real mux with test spy handlers per route family, real dev registry +
//     httptest upstream with a hit counter. Guards are asserted by
//     REACHABILITY only (spy/upstream counters) — the spec leaves the
//     refusal status to the implementer (A-1).
//   - RED shape: today no Fetch-Metadata/Service-Worker guard exists (the
//     handler and the upstream are reached), the proxy emits no CSP and no
//     ACAO (ModifyResponse deletes CSP headers), so the guard, CSP and
//     preflight rows RED; the exemption rows are pins (reachable today, must
//     stay reachable). Mode 1 header-set rows RED at the missing mint.
//   - Known gaps (documented): DS-7 rows 2–3 (percent-encoding of a prefix
//     carrying reserved chars, agent id "; drop") are builder-unit cases —
//     the builder does not exist pre-GREEN and the registry mints only
//     registry-safe tokens; they land with GREEN's builder unit tests.
//     Mode 1 proxied CSP uses the same fixture-binding note as
//     preview_redirect_rule_red_test.go (order 13).
//   - Mutations (post-GREEN, CHECK): widening the exemption list (e.g.
//     /api/v1/uploads/) flips row uploads_not_exempt; dropping the ACAO
//     override flips the ACAO row; template drift flips the tripwire.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// piRedGuardAgent is this file's dev registration agent id.
const piRedGuardAgent = "pi-red-guard-agent"

// piRedGuardHarness: real chain, spies per route family, controllable dev
// upstream with a hit counter.
type piRedGuardHarness struct {
	api          *restAPI
	srv          *httptest.Server
	port         string
	devToken     string
	upstreamHits atomic.Int32

	mu      sync.Mutex
	upHdrs  map[string]string
	session string
	spies   map[string]*atomic.Int32
}

func piRedNewGuardHarness(t *testing.T) *piRedGuardHarness {
	t.Helper()
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	api, _ := newPreviewRouteTestAPI(t)
	h := &piRedGuardHarness{api: api, spies: map[string]*atomic.Int32{}}
	cfg := api.agentLoop.GetConfig()

	plaintext, hash, err := middleware.MintSessionToken()
	require.NoError(t, err)
	h.session = plaintext
	cfg.Gateway.Users = []config.UserConfig{{
		Username:         "pi-red-guard-user",
		SessionTokenHash: config.BcryptHash(hash),
	}}

	spy := func(name string) *atomic.Int32 {
		c := &atomic.Int32{}
		h.spies[name] = c
		return c
	}
	agentsSpy := spy("agents")
	spy("library")
	spy("media-ws")
	spy("media")
	spy("uploads")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.upstreamHits.Add(1)
		h.mu.Lock()
		hdrs := h.upHdrs
		h.mu.Unlock()
		for k, v := range hdrs {
			w.Header().Set(k, v)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devEntry, regErr := reg.Register(piRedGuardAgent, upstreamPort(t, upstream.URL), 0, "npm run dev", 10)
	require.NoError(t, regErr)
	h.devToken = devEntry.Token

	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		agentsSpy.Add(1)
		api.withAuth(api.HandleAgents)(w, r)
	})
	mainMux.HandleFunc("/api/v1/library/", func(w http.ResponseWriter, r *http.Request) {
		h.spies["library"].Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mainMux.HandleFunc("/api/v1/media/workspace/", func(w http.ResponseWriter, r *http.Request) {
		h.spies["media-ws"].Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mainMux.HandleFunc("/api/v1/media/", func(w http.ResponseWriter, r *http.Request) {
		h.spies["media"].Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mainMux.HandleFunc("/api/v1/uploads/", func(w http.ResponseWriter, r *http.Request) {
		h.spies["uploads"].Add(1)
		w.WriteHeader(http.StatusOK)
	})
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})

	chain := buildProductionMiddlewareChain(api, mainMux)
	h.srv = httptest.NewServer(chain)
	t.Cleanup(h.srv.Close)
	h.port = fmt.Sprintf("%d", upstreamPort(t, h.srv.URL))

	cfg.Gateway.PublicURL = "http://localhost:" + h.port
	cfg.Gateway.PreviewEnabled = boolPtr(true)

	return h
}

// piRedGuardUpstreamEmit installs the upstream's next response headers.
func (h *piRedGuardHarness) piRedGuardUpstreamEmit(t *testing.T, hdrs map[string]string) {
	t.Helper()
	h.mu.Lock()
	h.upHdrs = hdrs
	h.mu.Unlock()
}

// piRedGuardGet issues a request with explicit Host and headers; the response
// is returned without following redirects.
func (h *piRedGuardHarness) piRedGuardGet(
	t *testing.T, host, path, method string, hdrs map[string]string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, nil)
	require.NoError(t, err)
	if host != "" {
		req.Host = host
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// piRedMode2CSPTemplate transcribes the spec's CSP template verbatim (the
// static tripwire oracle): ${ORIGIN} = canonical origin, ${PREFIX} =
// /preview/{agent}/{token}/ percent-encoded, ${ORIGIN-WS} = the ws:// form.
func piRedMode2CSPTemplate(origin, wsOrigin, prefix string) string {
	return "default-src 'none';\n" +
		"script-src " + origin + prefix + " 'unsafe-inline' 'unsafe-eval';\n" +
		"style-src " + origin + prefix + ";\n" +
		"img-src " + origin + prefix + " data:;\n" +
		"font-src " + origin + prefix + " data:;\n" +
		"media-src " + origin + prefix + ";\n" +
		"connect-src " + origin + prefix + " " + wsOrigin + prefix + ";\n" +
		"form-action " + origin + prefix + ";\n" +
		"worker-src " + origin + prefix + " blob:;\n" +
		"base-uri 'none';\n" +
		"object-src 'none';\n" +
		"frame-ancestors 'none';\n"
}

// piRedMintGuardLabel mints a Mode 1 label through the serve_web tool.
func piRedMintGuardLabel(t *testing.T, h *piRedGuardHarness) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index.html"), []byte("x"), 0o644))
	tool := tools.NewWebServeTool(
		dir, piRedGuardAgent,
		func() *config.Config { return h.api.agentLoop.GetConfig() },
		h.api.servedSubdirs, nil,
		tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
		nil, nil, 60, 86400)
	result := tool.Execute(tools.WithAgentID(context.Background(), piRedGuardAgent),
		map[string]any{"path": "."})
	require.False(t, result.IsError, "web_serve must succeed: %s", result.ForLLM)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed))
	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-001/FR-022): no isolated_url mint exists yet — the Mode 1 surface cannot be driven")
	u := strings.TrimPrefix(isoRaw.(string), "http://")
	u = strings.TrimSuffix(u, "/")
	parts := strings.SplitN(u, ".", 2)
	require.Len(t, parts, 2)
	return parts[0]
}

// ---------------------------------------------------------------------------
// order 14 — CSP header sets (FR-014, DS-7 row 1)
// ---------------------------------------------------------------------------

// TestPreviewCSPHeaderSet: the Mode 2 proxied CSP equals the spec template
// byte-for-byte (static tripwire), and Mode 1 responses carry
// frame-ancestors 'none' with no source directives and no sandbox, static
// and proxied. RED today: the proxy emits NO CSP header at all
// (ModifyResponse deletes upstream CSP headers), and Mode 1 has no surface.
func TestPreviewCSPHeaderSet(t *testing.T) {
	h := piRedNewGuardHarness(t)
	origin := "http://localhost:" + h.port
	prefix := "/preview/" + piRedGuardAgent + "/" + h.devToken

	t.Run("mode2_proxied_csp_byte_identical", func(t *testing.T) {
		h.piRedGuardUpstreamEmit(t, map[string]string{
			"Content-Security-Policy": "default-src *; script-src http://evil.example 'unsafe-inline'",
		})
		resp := h.piRedGuardGet(t, "", prefix+"/app.js", http.MethodGet, nil)
		want := piRedMode2CSPTemplate(origin, "ws://localhost:"+h.port, prefix)
		assert.Equal(t, want, resp.Header.Get("Content-Security-Policy"),
			"RED (FR-014, DS-7 row 1): the Mode 2 proxied CSP must be byte-identical to the "+
				"spec template (the static tripwire oracle) — today the proxy emits no CSP at all "+
				"and the upstream's CSP would leak through")
	})

	t.Run("mode1_static_header_set", func(t *testing.T) {
		h2 := piRedNewGuardHarness(t)
		label := piRedMintGuardLabel(t, h2)
		resp := h2.piRedGuardGet(t, label+".localhost:"+h2.port, "/", http.MethodGet, nil)
		csp := resp.Header.Get("Content-Security-Policy")
		assert.Contains(t, csp, "frame-ancestors 'none'",
			"RED (FR-014): Mode 1 responses carry frame-ancestors 'none'")
		for _, directive := range []string{
			"default-src", "script-src", "style-src", "img-src", "font-src",
			"media-src", "connect-src", "form-action", "worker-src",
		} {
			assert.NotContains(t, csp, directive,
				"FR-014: Mode 1 carries no source directives")
		}
		assert.NotContains(t, csp, "sandbox",
			"FR-014: no sandbox directive exists anywhere in the web_serve model")
	})

	t.Run("mode1_proxied_header_set", func(t *testing.T) {
		// Fixture note (mechanism-neutral, A-2): the drive binds the label host
		// to the harness's dev upstream via the registry; if GREEN resolves
		// label→upstream differently the FIXTURE adapts, the assertions stand.
		h.upstreamHits.Store(0)
		label := piRedMintGuardLabel(t, h)
		resp := h.piRedGuardGet(t, label+".localhost:"+h.port, "/", http.MethodGet, nil)
		require.Equal(t, int32(1), h.upstreamHits.Load(),
			"the label-host request must reach the dev upstream (fixture binding)")
		csp := resp.Header.Get("Content-Security-Policy")
		assert.Contains(t, csp, "frame-ancestors 'none'",
			"RED (FR-014): Mode 1 proxied responses carry frame-ancestors 'none'")
		assert.NotContains(t, csp, "sandbox", "FR-014: no sandbox directive")
	})
}

// ---------------------------------------------------------------------------
// order 15 — CORS preflight pin (FR-012, DS-7 row 4, S-2.8)
// ---------------------------------------------------------------------------

// TestPreviewCORSPreflightPin: preflights under the token prefix answer
// exactly GET, HEAD, OPTIONS with NO allow-headers wildcard, and successful
// proxied responses carry ACAO:* with the upstream's ACAO/ACAC deleted.
func TestPreviewCORSPreflightPin(t *testing.T) {
	h := piRedNewGuardHarness(t)
	prefix := "/preview/" + piRedGuardAgent + "/" + h.devToken

	t.Run("preflight_methods_exact_no_wildcard_headers", func(t *testing.T) {
		resp := h.piRedGuardGet(t, "", prefix+"/x", http.MethodOptions,
			map[string]string{
				"Origin":                        "http://crossorigin.example",
				"Access-Control-Request-Method": "GET",
			})
		assert.Equal(t, "GET, HEAD, OPTIONS", resp.Header.Get("Access-Control-Allow-Methods"),
			"RED (FR-012, DS-7 row 4): the preflight answers exactly GET, HEAD, OPTIONS — "+
				"no CORS answer exists under the prefix today")
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Headers"),
			"FR-012 (round-1 MIN-005): NO allow-headers header at all — a wildcard would "+
				"permit cross-origin non-simple writes to the dev upstream")
	})

	t.Run("proxied_acao_star_upstream_acao_deleted", func(t *testing.T) {
		h.piRedGuardUpstreamEmit(t, map[string]string{
			"Access-Control-Allow-Origin":      "http://evil.example",
			"Access-Control-Allow-Credentials": "true",
		})
		resp := h.piRedGuardGet(t, "", prefix+"/app.js", http.MethodGet, nil)
		assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"),
			"RED (FR-012): every successful response under the token prefix carries ACAO:* — "+
				"the upstream's ACAO must be deleted before the override")
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"),
			"FR-012: no ACAC — reads only, no allow-credentials")
	})
}

// ---------------------------------------------------------------------------
// order 16 — Fetch-Metadata / Service-Worker navigation guard
//            (FR-010, FR-011, S-2.13, round-2 MIN-002)
// ---------------------------------------------------------------------------

// TestPreviewNavigationGuard: a main-Host /api/v1 navigation
// (Sec-Fetch-Dest: document) is rejected BEFORE the API handler except the
// named GET-only exemption list; /preview/ refuses service-worker requests.
// Every guard assertion is REACHABILITY ONLY (route spies / upstream hit
// counter) — the spec leaves the refusal status to the implementer (A-1):
// "the requirement is that the request never reaches the handler".
func TestPreviewNavigationGuard(t *testing.T) {
	h := piRedNewGuardHarness(t)
	prefix := "/preview/" + piRedGuardAgent + "/" + h.devToken
	doc := map[string]string{"Sec-Fetch-Dest": "document"}

	t.Run("guard_blocks_document_nav_on_api", func(t *testing.T) {
		// With a valid session the ONLY thing that may stop this request is
		// the guard — today nothing does and the handler runs.
		hdrs := map[string]string{
			"Sec-Fetch-Dest": "document",
			"Cookie":         "omnipus-session=" + h.session,
		}
		resp := h.piRedGuardGet(t, "", "/api/v1/agents", http.MethodGet, hdrs)
		require.Equal(t, int32(0), h.spies["agents"].Load(),
			"RED (FR-010, S-2.13): a main-Host document navigation to /api/v1 reached the API "+
				"handler (status %d) — no Fetch-Metadata guard exists before the handler yet",
			resp.StatusCode)
	})

	t.Run("exempt_library_download_get_reachable", func(t *testing.T) {
		// FR-010 exemption 1, GET-only: /api/v1/library/{workspaceId}/download.
		resp := h.piRedGuardGet(t, "", "/api/v1/library/ws-123/download", http.MethodGet, doc)
		_ = resp
		assert.GreaterOrEqual(t, h.spies["library"].Load(), int32(1),
			"FR-010 (pin): the library download route stays reachable for a document navigation")
	})

	t.Run("exempt_media_workspace_get_reachable", func(t *testing.T) {
		// FR-010 exemption 2: /api/v1/media/workspace/...
		h.piRedGuardGet(t, "", "/api/v1/media/workspace/w1/img.png", http.MethodGet, doc)
		assert.GreaterOrEqual(t, h.spies["media-ws"].Load(), int32(1),
			"FR-010 (pin): the workspace media route stays reachable for a document navigation")
	})

	t.Run("exempt_media_get_reachable", func(t *testing.T) {
		// FR-010 exemption 3: /api/v1/media/...
		h.piRedGuardGet(t, "", "/api/v1/media/att-1", http.MethodGet, doc)
		assert.GreaterOrEqual(t, h.spies["media"].Load(), int32(1),
			"FR-010 (pin): the media route stays reachable for a document navigation")
	})

	t.Run("exemptions_are_get_only", func(t *testing.T) {
		// A document navigation POSTing to an exempted address is NOT exempt.
		// The CSRF pair (double-submit cookie + header) gets the POST past
		// CSRFMiddleware so the ONLY thing left to stop it post-GREEN is the
		// guard; today the route spy fires — the RED.
		hdrs := map[string]string{
			"Sec-Fetch-Dest": "document",
			"Cookie":         "csrf=pi-red-csrf-pair",
			"X-Csrf-Token":   "pi-red-csrf-pair",
		}
		h.piRedGuardGet(t, "", "/api/v1/library/ws-123/download", http.MethodPost, hdrs)
		require.Equal(t, int32(0), h.spies["library"].Load(),
			"RED (FR-010, Q3): the exemption is GET-only — a document-navigation POST to an "+
				"exempted address must be rejected before the handler; today the handler ran")
	})

	t.Run("uploads_not_exempt", func(t *testing.T) {
		// FR-010: /api/v1/uploads/{session_id}/{filename} is deliberately NOT
		// exempt — no SPA navigation targets it.
		h.piRedGuardGet(t, "", "/api/v1/uploads/s1/f.txt", http.MethodGet, doc)
		require.Equal(t, int32(0), h.spies["uploads"].Load(),
			"RED (FR-010): a document navigation to the uploads route must be rejected before "+
				"the handler — today the handler ran")
	})

	t.Run("preview_service_worker_script_refused", func(t *testing.T) {
		h.upstreamHits.Store(0)
		h.piRedGuardGet(t, "", prefix+"/sw.js", http.MethodGet,
			map[string]string{"Service-Worker": "script"})
		require.Equal(t, int32(0), h.upstreamHits.Load(),
			"RED (FR-011): a /preview/ request carrying Service-Worker: script must be refused "+
				"— today it reaches the dev upstream, and a service worker would outlive the page")
	})

	t.Run("preview_sec_fetch_serviceworker_refused", func(t *testing.T) {
		h.upstreamHits.Store(0)
		h.piRedGuardGet(t, "", prefix+"/sw.js", http.MethodGet,
			map[string]string{"Sec-Fetch-Dest": "serviceworker"})
		require.Equal(t, int32(0), h.upstreamHits.Load(),
			"RED (FR-011): a /preview/ request carrying Sec-Fetch-Dest: serviceworker must be "+
				"refused — today it reaches the dev upstream")
	})
}
