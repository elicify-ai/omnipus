// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — fix3 (gate round 3) focused tests for the #798 ADR-094
// gate findings A1 / A2 / A3 / A8:
//
//   - A1 (BLOCKER): a Mode 2 STATIC preview response must carry the Mode 2
//     CSP template (FR-014/S-2.13), not the workspace CSP — today it carries
//     buildWorkspaceCSP (connect-src 'self'), so a static preview page can
//     fetch/form-POST /api/v1 with the session cookie.
//   - A2 (MAJOR): the Mode 2 redirect rule must resolve the Location (dot
//     segments normalised, percent-encoded prefix in ONE comparison form)
//     BEFORE the prefix check — an in-prefix "feat/../next" redirect and a
//     percent-encoded agent id must emit, not 502 (DS-2 rows 1/6, FR-013).
//     The reserved-root refusal (DS-2 row 2) is pinned as-is.
//   - A3 (MAJOR): the FR-011 service-worker refusal is a main-Host control
//     (FR-028) — a Mode 1 label request must be SERVED, not 403'd.
//   - A8 (MAJOR): gatewayOwnsBearer's return-value matrix is pinned at unit
//     level (FR-020/DS-4 rows 5–12). The zero-bcrypt cost property is not
//     black-box observable without a validator spy seam; CHECK audits it.
//
// Unit boundary: REAL production chain (buildProductionMiddlewareChain) over
// registerPreviewEndpoints; a real httptest upstream the test programmes per
// row; the static surface minted through the real serve_web tool. Expected
// values are transcribed from the spec/finding text, never observed output.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// fix3Harness: real chain, real registries, a programmable upstream, and the
// harness's own canonical origin. The upstream only answers requests in the
// dev-proxy tests; the static tests never reach it.
type fix3Harness struct {
	api  *restAPI
	srv  *httptest.Server
	port string

	devRegistry  *sandbox.DevServerRegistry
	upstreamPort int32

	mu        sync.Mutex
	upStatus  int
	upLoc     string
	upstream_ atomic.Int32
}

func newFix3Harness(t *testing.T) *fix3Harness {
	t.Helper()
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	api, _ := newPreviewRouteTestAPI(t)
	h := &fix3Harness{api: api, upStatus: http.StatusOK}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.upstream_.Add(1)
		h.mu.Lock()
		status, loc := h.upStatus, h.upLoc
		h.mu.Unlock()
		if loc != "" {
			w.Header().Set("Location", loc)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	h.devRegistry = reg
	h.upstreamPort = upstreamPort(t, upstream.URL)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	chain := buildProductionMiddlewareChain(api, mainMux)
	h.srv = httptest.NewServer(chain)
	t.Cleanup(h.srv.Close)
	h.port = fmt.Sprintf("%d", upstreamPort(t, h.srv.URL))

	h.api.agentLoop.GetConfig().Gateway.PublicURL = "http://localhost:" + h.port
	h.api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)

	return h
}

// fix3RegisterDev registers the harness upstream under agentID and returns
// the dev registration token.
func (h *fix3Harness) fix3RegisterDev(t *testing.T, agentID string) string {
	t.Helper()
	entry, err := h.devRegistry.Register(agentID, h.upstreamPort, 0, "npm run dev", 10)
	require.NoError(t, err)
	return entry.Token
}

// fix3MintStatic mints a static registration through the real serve_web tool
// and returns the parsed result payload (path/url/isolated_url).
func (h *fix3Harness) fix3MintStatic(t *testing.T, agentID string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644))

	tool := tools.NewWebServeTool(
		dir, agentID,
		func() *config.Config { return h.api.agentLoop.GetConfig() },
		h.api.servedSubdirs,
		nil, // static mode — no dev spawn, platform-independent
		tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
		nil, nil, 60, 86400,
	)
	result := tool.Execute(tools.WithAgentID(context.Background(), agentID),
		map[string]any{"path": "."})
	require.False(t, result.IsError, "web_serve must succeed: %s", result.ForLLM)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed))
	out := map[string]string{}
	for _, k := range []string{"path", "url", "isolated_url"} {
		if v, ok := parsed[k].(string); ok {
			out[k] = v
		}
	}
	require.NotEmpty(t, out["path"], "static mint must carry the /preview/ path")
	return out
}

// fix3Get issues a GET against the harness server with an explicit Host and
// headers; redirects are not followed.
func (h *fix3Harness) fix3Get(
	t *testing.T, host, path string, hdrs map[string]string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
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

// fix3UpstreamEmit programmes the upstream's next response.
func (h *fix3Harness) fix3UpstreamEmit(t *testing.T, status int, location string) {
	t.Helper()
	h.mu.Lock()
	h.upStatus, h.upLoc = status, location
	h.mu.Unlock()
}

// fix3Mode2CSPTemplate transcribes the spec's Mode 2 CSP template verbatim
// (the static tripwire oracle; spec "Mode 2 response headers" / ADR-094
// §2.3): ${ORIGIN}, ${PREFIX} percent-encoded, ${ORIGIN-WS}.
func fix3Mode2CSPTemplate(origin, wsOrigin, prefix string) string {
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

// fix3MintToken mints a gateway bearer token fixture: an id-tagged
// "omnipus_<id>_<body>" (entry hash over the body) or a legacy bare string
// (no id, no dot — hash over the whole raw).
func fix3MintToken(id, body string) (string, config.TokenEntry) {
	hash, err := bcrypt.GenerateFromPassword([]byte(body), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	if id != "" {
		return "omnipus_" + id + "_" + body,
			config.TokenEntry{ID: id, Hash: config.BcryptHash(hash)}
	}
	return body, config.TokenEntry{Hash: config.BcryptHash(hash)}
}

// ---------------------------------------------------------------------------
// A1 — Mode 2 static responses carry the Mode 2 CSP template
// ---------------------------------------------------------------------------

// TestPreviewFix3_StaticMode2CSP: a static preview opened at
// /preview/{agent}/{token}/ on the main host must carry the byte-stable Mode
// 2 CSP template (FR-014/S-2.13) — confined connect-src/form-action to
// origin+prefix, frame-ancestors 'none' — not the workspace CSP. RED today
// (A1): the static path keeps buildWorkspaceCSP (connect-src 'self'), which
// lets the previewed page fetch/POST /api/v1 with the session cookie.
func TestPreviewFix3_StaticMode2CSP(t *testing.T) {
	h := newFix3Harness(t)
	mint := h.fix3MintStatic(t, "fix3-static-agent")
	prefix := strings.TrimSuffix(mint["path"], "/")

	resp := h.fix3Get(t, "", prefix+"/", nil)
	csp := resp.Header.Get("Content-Security-Policy")

	want := fix3Mode2CSPTemplate("http://localhost:"+h.port, "ws://localhost:"+h.port, prefix)
	wantWire := strings.TrimSpace(strings.ReplaceAll(want, "\n", " "))
	assert.Equal(t, wantWire, csp,
		"RED (A1, FR-014/S-2.13): the Mode 2 STATIC preview CSP must be the byte-stable "+
			"Mode 2 template — today it is the workspace CSP (connect-src 'self'), so the "+
			"previewed page can reach /api/v1 with the session cookie")
	assert.Contains(t, csp, "frame-ancestors 'none'",
		"A1 (FR-014): the Mode 2 template carries frame-ancestors 'none'")
}

// TestPreviewFix3_Mode1StaticCSPPin: under a label Host the static response
// keeps the Mode 1 CSP — frame-ancestors 'none' and NO source directives
// (FR-014; pin across the A1 change).
func TestPreviewFix3_Mode1StaticCSPPin(t *testing.T) {
	h := newFix3Harness(t)
	mint := h.fix3MintStatic(t, "fix3-static-agent")
	require.NotEmpty(t, mint["isolated_url"], "a loopback canonical origin mints the Mode 1 URL")
	// isolated_url is already http://<label>.localhost[:port]/ — the Host is
	// that authority, not a second ".localhost" suffix.
	u := strings.TrimPrefix(strings.TrimSuffix(mint["isolated_url"], "/"), "http://")
	parts := strings.SplitN(u, ".", 2)
	require.Len(t, parts, 2, "isolated_url must be <label>.localhost[:port]")

	resp := h.fix3Get(t, u, "/", nil)
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "frame-ancestors 'none'", "FR-014: Mode 1 frame-ancestors")
	assert.NotContains(t, csp, "default-src", "FR-014: Mode 1 carries no source directives")
}

// ---------------------------------------------------------------------------
// A2 — redirect rule: resolve + normalise BEFORE the prefix check
// ---------------------------------------------------------------------------

// TestPreviewFix3_RedirectInPrefixDotSegments: an upstream redirect to
// "<prefix>/feat/../next" normalises INSIDE the prefix and must be EMITTED
// RESOLVED (DS-2 rows 1/6, FR-013, CR3; DS-2's "Resolved origin/path"
// column — what is checked is what is emitted). Test-oracle update,
// backend-lead fix-round-4 2026-09-28: round 3 pinned the raw emit; the
// round-4 gate finding and squad decision replace it with the resolved
// emit (qa-lead's RED pack preview_fix3_red_test.go pins the same value).
func TestPreviewFix3_RedirectInPrefixDotSegments(t *testing.T) {
	h := newFix3Harness(t)
	token := h.fix3RegisterDev(t, "fix3-redirect-agent")
	prefix := "/preview/fix3-redirect-agent/" + token

	h.fix3UpstreamEmit(t, http.StatusFound, prefix+"/feat/../next")
	resp := h.fix3Get(t, "", prefix+"/page", nil)

	require.Equal(t, http.StatusFound, resp.StatusCode,
		"A2 (DS-2 row 1): an in-prefix redirect keeps the upstream status")
	assert.Equal(t, prefix+"/next", resp.Header.Get("Location"),
		"A2/CR3 (FR-013): dot segments that normalise back INSIDE the prefix must "+
			"emit RESOLVED — the emitted Location is the normalised path the prefix "+
			"check cleared, never the raw dot-segment form")
}

// TestPreviewFix3_RedirectPercentEncodedAgent: an agent id that must be
// percent-encoded on the wire (EntityID admits spaces and semicolons) — a
// Location naming the ESCAPED prefix must emit; comparing the escaped prefix
// against the decoded path 502s it today (CR3's second half).
func TestPreviewFix3_RedirectPercentEncodedAgent(t *testing.T) {
	h := newFix3Harness(t)
	const agentID = "fix3 sp;ace" // EntityID-legal: space + semicolon, no / \ ..
	token := h.fix3RegisterDev(t, agentID)
	esc := url.PathEscape(agentID) // what the prefix builder does
	prefix := "/preview/" + esc + "/" + token

	h.fix3UpstreamEmit(t, http.StatusFound, prefix+"/dashboard")
	resp := h.fix3Get(t, "", "/preview/"+esc+"/"+token+"/page", nil)

	require.Equal(t, http.StatusFound, resp.StatusCode,
		"A2 (DS-2 row 1): the escaped-prefix Location is in-prefix and emits")
	assert.Equal(t, prefix+"/dashboard", resp.Header.Get("Location"),
		"RED (A2/CR3): the prefix must be compared in ONE consistent form — today the "+
			"escaped prefix is compared against the decoded path and the redirect 502s")
}

// TestPreviewFix3_RedirectReservedRootPin: DS-2 row 2 — a root-relative
// redirect into a gateway-reserved namespace 502s with no Location (pin
// across the A2 rewrite).
func TestPreviewFix3_RedirectReservedRootPin(t *testing.T) {
	h := newFix3Harness(t)
	token := h.fix3RegisterDev(t, "fix3-redirect-agent")
	prefix := "/preview/fix3-redirect-agent/" + token

	h.fix3UpstreamEmit(t, http.StatusFound, "/api/v1/config")
	resp := h.fix3Get(t, "", prefix+"/page", nil)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode,
		"DS-2 row 2 (pin): a reserved-root root-relative redirect 502s")
	assert.Empty(t, resp.Header.Get("Location"),
		"DS-2 row 2 (pin): the 502 carries no Location")
}

// ---------------------------------------------------------------------------
// A3 — the service-worker refusal is a main-Host control (FR-028)
// ---------------------------------------------------------------------------

// TestPreviewFix3_Mode1ServiceWorkerServed: a Mode 1 label request carrying
// Service-Worker: script must be SERVED — FR-011 applies to main-host
// /preview/ requests only (FR-028); the label host cannot see gateway
// cookies. RED today: the refusal runs before the label branch.
func TestPreviewFix3_Mode1ServiceWorkerServed(t *testing.T) {
	h := newFix3Harness(t)
	mint := h.fix3MintStatic(t, "fix3-static-agent")
	require.NotEmpty(t, mint["isolated_url"])
	host := strings.TrimPrefix(strings.TrimSuffix(mint["isolated_url"], "/"), "http://")

	resp := h.fix3Get(t, host, "/", map[string]string{"Service-Worker": "script"})
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"RED (A3, FR-011 per FR-028): a Mode 1 label request must not be 403'd by the "+
			"main-host service-worker guard — the app owns its host")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "x", string(body), "A3: the Mode 1 app is actually served")
}

// TestPreviewFix3_Mode2ServiceWorkerStillRefused: the main-host /preview/
// refusal stands (FR-011; pin across the A3 reorder).
func TestPreviewFix3_Mode2ServiceWorkerStillRefused(t *testing.T) {
	h := newFix3Harness(t)
	mint := h.fix3MintStatic(t, "fix3-static-agent")
	prefix := strings.TrimSuffix(mint["path"], "/")

	for _, hdrs := range []map[string]string{
		{"Service-Worker": "script"},
		{"Sec-Fetch-Dest": "serviceworker"},
	} {
		resp := h.fix3Get(t, "", prefix+"/sw.js", hdrs)
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"FR-011 (pin): a main-host /preview/ service-worker request stays refused")
	}
}

// ---------------------------------------------------------------------------
// A8 — gatewayOwnsBearer return-value matrix (cost property: see CHECK)
// ---------------------------------------------------------------------------

// TestPreviewFix3_OwnsBearerShapePins pins gatewayOwnsBearer's verdict matrix
// (FR-020/DS-4 rows 5–7/10–12) at unit level: gateway-accepted shapes stay
// true across the A8 pre-filter; JWT/foreign/Basic stay false. The
// zero-bcrypt-compare property (DS-4 row 10) is a COST property — not
// black-box observable here; CHECK's validator spy audits it.
func TestPreviewFix3_OwnsBearerShapePins(t *testing.T) {
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	cfg := &config.Config{Gateway: config.GatewayConfig{
		Users: []config.UserConfig{{Username: "fix3-user"}},
	}}

	row5Raw, row5Entry := fix3MintToken("row5", strings.Repeat("b", 43))
	cfg.Gateway.Users[0].Tokens = []config.TokenEntry{row5Entry}

	t.Run("row5_id_tagged_accepted", func(t *testing.T) {
		assert.True(t, gatewayOwnsBearer(cfg, "Bearer "+row5Raw),
			"DS-4 row 5: an id-tagged gateway user token is the gateway's")
	})
	t.Run("row10_jwt_not_gateway_owned", func(t *testing.T) {
		jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhIn0.c2ln"
		assert.False(t, gatewayOwnsBearer(cfg, "Bearer "+jwt),
			"DS-4 row 10: a non-gateway JWT is not the gateway's (and must forward with "+
				"ZERO bcrypt compares — cost half audited by CHECK's spy)")
	})
	t.Run("row6_foreign_not_gateway_owned", func(t *testing.T) {
		assert.False(t, gatewayOwnsBearer(cfg, "Bearer some-other-token"),
			"DS-4 row 6: a foreign bearer is not the gateway's")
	})
	t.Run("row7_basic_not_bearer", func(t *testing.T) {
		assert.False(t, gatewayOwnsBearer(cfg, "Basic dXNlcjpwYXNz"),
			"DS-4 row 7: non-Bearer Authorization is not a bearer")
	})
	t.Run("row11_legacy_user_token_accepted", func(t *testing.T) {
		row11Raw, row11Entry := fix3MintToken("", strings.Repeat("l", 43))
		cfg.Gateway.Users[0].Tokens = []config.TokenEntry{row11Entry}
		assert.True(t, gatewayOwnsBearer(cfg, "Bearer "+row11Raw),
			"DS-4 row 11: a legacy (no id, no dot) gateway user token is the gateway's")
	})
	t.Run("row12_env_token_accepted", func(t *testing.T) {
		t.Setenv("OMNIPUS_BEARER_TOKEN", "fix3-env-token-value")
		assert.True(t, gatewayOwnsBearer(cfg, "Bearer fix3-env-token-value"),
			"DS-4 row 12: the legacy env token is the gateway's (constant-time compare)")
	})
}
