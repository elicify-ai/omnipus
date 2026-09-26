// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED tests for ADR-094 preview isolation, orders 12 and 13
// (FR-013, DS-2, DS-2b, S-5.1–S-5.5; spec:
// docs/internal/specs/adr-094-preview-isolation-spec.md).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: the Mode 2 dev proxy applies the 4-step Location
//     rule (resolve → emit alias+in-prefix → re-root root-relative → else 502
//     no Location), deletes Location on non-redirect statuses, leaves 304
//     untouched; Mode 1 passes every Location through unchanged.
//   - Specification source: FR-013 (verbatim 4-step), DS-2 rows 1–11,
//     DS-2b rows 1–3, S-5.1–S-5.5. Expected verdicts are the DS-2/DS-2b
//     Verdict columns, never observed output.
//   - Unit boundary: REAL production chain (buildProductionMiddlewareChain)
//     around a real mux with registerPreviewEndpoints, a REAL httptest
//     upstream the test programmes per row (status + Location), driven by a
//     real httptest.Server; the Director and ModifyResponse are real.
//   - RED shape: today's proxy passes every Location through untouched
//     (ModifyResponse touches only CSP headers and reserved Set-Cookies) —
//     the 8 verdict rows (502 / re-root / deletion) RED at that; the 5
//     pass-through rows are pins ACROSS the change. Order 13 (Mode 1) REDs
//     at the dev mint — on darwin the tier3 Linux gate answers first
//     ("Tier 3 dev servers are Linux only"); green-able on Linux CI (the
//     same platform note as preview_host_dispatch_red_test.go).
//   - Known gaps: none at this layer; the alias set (rows 10–11) is asserted
//     behaviorally through both loopback alias forms.
//   - Mutations: M-x (post-GREEN, CHECK): re-root to the wrong prefix, skip
//     the resolve step (raw dot-segments), or apply the rule on Mode 1 must
//     flip the corresponding rows red.

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
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// piRedRedirectAgent is the dev registration's agent id for this file.
const piRedRedirectAgent = "pi-red-redirect-agent"

// piRedRedirectHarness: real chain, real dev registry entry, and an upstream
// the test programmes per row (status + Location).
type piRedRedirectHarness struct {
	api      *restAPI
	srv      *httptest.Server
	port     string
	devToken string

	mu           sync.Mutex
	upStatus     int
	upLoc        string
	upstreamHits atomic.Int32
}

func piRedNewRedirectHarness(t *testing.T) *piRedRedirectHarness {
	t.Helper()
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	api, _ := newPreviewRouteTestAPI(t)
	h := &piRedRedirectHarness{api: api}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		status, loc := h.upStatus, h.upLoc
		h.mu.Unlock()
		if loc != "" {
			w.Header().Set("Location", loc)
		}
		h.upstreamHits.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devEntry, regErr := reg.Register(piRedRedirectAgent, upstreamPort(t, upstream.URL), 0, "npm run dev", 10)
	require.NoError(t, regErr)
	h.devToken = devEntry.Token

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	chain := buildProductionMiddlewareChain(api, mainMux)
	h.srv = httptest.NewServer(chain)
	t.Cleanup(h.srv.Close)
	h.port = fmt.Sprintf("%d", upstreamPort(t, h.srv.URL))

	return h
}

// piRedUpstreamEmit programmes the upstream's next response.
func (h *piRedRedirectHarness) piRedUpstreamEmit(t *testing.T, status int, location string) {
	t.Helper()
	h.mu.Lock()
	h.upStatus, h.upLoc = status, location
	h.mu.Unlock()
}

// piRedProxyGet drives a GET through the Mode 2 dev proxy and returns the
// client-visible response.
func (h *piRedRedirectHarness) piRedProxyGet(t *testing.T, sub string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		h.srv.URL+"/preview/"+piRedRedirectAgent+"/"+h.devToken+"/"+sub, nil)
	require.NoError(t, err)
	// Do NOT follow redirects: the test asserts the FIRST response's status
	// and Location exactly (following would chase the 302 back upstream).
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// ---------------------------------------------------------------------------
// order 12 — the DS-2 table through the Mode 2 dev proxy
// ---------------------------------------------------------------------------

// TestPreviewRedirectRule drives every DS-2 row through the real dev proxy.
// RED rows (2, 3, 4, 5, 6, 7, 8, 9 + the non-redirect deletion): today the
// Location passes through untouched. Pins (1, 10, 11, 304): pass-through is
// already correct today and must stay correct post-GREEN.
func TestPreviewRedirectRule(t *testing.T) {
	h := piRedNewRedirectHarness(t)

	// The alias-origin prefix used by rows 1, 5, 6, 10, 11.
	prefix := "/preview/" + piRedRedirectAgent + "/" + h.devToken

	emit := func(t *testing.T, name, rawLoc string, wantLoc string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			h.piRedUpstreamEmit(t, http.StatusFound, rawLoc)
			resp := h.piRedProxyGet(t, "page")
			require.Equal(t, http.StatusFound, resp.StatusCode,
				"DS-2: an in-prefix emit keeps the upstream redirect status")
			assert.Equal(t, wantLoc, resp.Header.Get("Location"),
				"DS-2: the emitted Location must match the Verdict column")
		})
	}
	fiftyTwo := func(t *testing.T, name, rawLoc, redMsg string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			h.piRedUpstreamEmit(t, http.StatusFound, rawLoc)
			resp := h.piRedProxyGet(t, "page")
			require.Equal(t, http.StatusBadGateway, resp.StatusCode,
				"RED (FR-013, DS-2): %s — today the upstream %d passes through with the Location intact", redMsg, http.StatusFound)
			assert.Empty(t, resp.Header.Get("Location"),
				"DS-2: the 502 carries NO Location")
		})
	}

	emit(t, "row1_in_prefix_emit_unchanged", prefix+"/next", prefix+"/next")

	fiftyTwo(t, "row2_out_of_prefix_502", "/api/v1/config",
		"an alias-origin out-of-prefix target must 502 with no Location")
	fiftyTwo(t, "row3_protocol_relative_502", "//localhost:"+h.port+"/api/v1/config",
		"a protocol-relative out-of-prefix target must 502")
	fiftyTwo(t, "row4_loopback_alias_out_502", "http://127.0.0.1:"+h.port+"/api/v1/config",
		"an absolute loopback-alias out-of-prefix target must 502 (the Director does not rewrite Host)")
	fiftyTwo(t, "row6_dot_segments_502", prefix+"/../../etc",
		"dot-segments normalise out of the prefix and must 502")
	fiftyTwo(t, "row7_pct_encoded_502", "%2e%2e%2fapi",
		"percent-encoded dot-segments normalise out and must 502")
	fiftyTwo(t, "row8_foreign_origin_502", "http://evil.example/",
		"a foreign origin must 502")
	fiftyTwo(t, "row9_data_uri_502", "data:text/html,x",
		"a non-http(s) resource must 502")

	t.Run("row5_root_relative_reroot", func(t *testing.T) {
		h.piRedUpstreamEmit(t, http.StatusFound, "/next")
		resp := h.piRedProxyGet(t, "page")
		require.Equal(t, http.StatusFound, resp.StatusCode,
			"DS-2 row 5: the re-rooted redirect keeps the upstream status")
		assert.Equal(t, prefix+"/next", resp.Header.Get("Location"),
			"RED (FR-013, DS-2 row 5): a raw root-relative Location must be re-rooted under the "+
				"prefix and re-checked — today it passes through un-re-rooted")
	})

	emit(t, "row10_loopback_alias_in_prefix_emit", "http://127.0.0.1:"+h.port+prefix+"/next", "http://127.0.0.1:"+h.port+prefix+"/next")

	emit(t, "row11_canonical_abs_in_prefix_emit", "http://localhost:"+h.port+prefix+"/next", "http://localhost:"+h.port+prefix+"/next")

	t.Run("non_redirect_status_deletes_location", func(t *testing.T) {
		h.piRedUpstreamEmit(t, http.StatusOK, prefix+"/elsewhere")
		resp := h.piRedProxyGet(t, "page")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Location"),
			"RED (FR-013, S-5.5): a stray Location on a non-redirect status must be deleted — "+
				"today it passes through")
	})

	t.Run("status_304_untouched", func(t *testing.T) {
		h.piRedUpstreamEmit(t, http.StatusNotModified, prefix+"/conditional")
		resp := h.piRedProxyGet(t, "page")
		require.Equal(t, http.StatusNotModified, resp.StatusCode)
		assert.Equal(t, prefix+"/conditional", resp.Header.Get("Location"),
			"DS-2 / S-5.5 (pin): a 304's Location is untouched — not 502'd, not deleted")
	})
}

// ---------------------------------------------------------------------------
// order 13 — DS-2b: Mode 1 passes every Location through unchanged
// ---------------------------------------------------------------------------

// piRedRedirectLabel mints through the serve_web tool (static mode) and
// returns the Mode 1 label — the only label source that exists.
func piRedRedirectLabel(t *testing.T, h *piRedRedirectHarness) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index.html"), []byte("x"), 0o644))

	tool := tools.NewWebServeTool(
		dir,
		piRedRedirectAgent,
		func() *config.Config { return h.api.agentLoop.GetConfig() },
		h.api.servedSubdirs,
		nil, // static mode — no dev spawn on this platform
		tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
		nil, nil, 60, 86400,
	)
	result := tool.Execute(tools.WithAgentID(context.Background(), piRedRedirectAgent),
		map[string]any{"path": "."})
	require.False(t, result.IsError, "web_serve must succeed: %s", result.ForLLM)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed))
	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-001/FR-022): no isolated_url mint exists yet — the Mode 1 surface of the "+
			"redirect rule cannot be driven pre-GREEN; post-GREEN this test binds the label host "+
			"to the harness's dev upstream and asserts the three DS-2b rows pass through unchanged")
	labelURL, _ := isoRaw.(string)
	u := strings.TrimPrefix(labelURL, "http://")
	u = strings.TrimSuffix(u, "/")
	parts := strings.SplitN(u, ".", 2)
	require.Len(t, parts, 2, "isolated_url must be <label>.localhost[:port], got %q", labelURL)
	return parts[0]
}

// TestPreviewRedirectRule_Mode1Passthrough pins DS-2b: under a label Host
// every Location is emitted unchanged — the Mode 2 out-of-prefix 502 and the
// non-redirect deletion do NOT apply (the app owns its host's navigation,
// US-5 AC4). RED today at the missing mint. Fixture note (mechanism-neutral,
// A-2): the drive binds the label host to the harness's registered dev
// upstream and asserts the upstream was REACHED — if GREEN's label→upstream
// resolution differs, the FIXTURE adapts; the DS-2b assertions stand.
func TestPreviewRedirectRule_Mode1Passthrough(t *testing.T) {
	h := piRedNewRedirectHarness(t)
	label := piRedRedirectLabel(t, h)
	labelHost := label + ".localhost:" + h.port

	cases := []struct {
		name string
		loc  string // raw upstream Location; %s = listener port
	}{
		{name: "ds2b_row1_root_relative", loc: "/login"},
		{name: "ds2b_row2_absolute_out_of_prefix", loc: "http://127.0.0.1:%s/api/v1/config"},
		{name: "ds2b_row3_foreign_https", loc: "https://auth.example.com/oauth"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h.upstreamHits.Store(0)
			want := fmt.Sprintf(tc.loc, h.port)
			h.piRedUpstreamEmit(t, http.StatusFound, want)

			req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/", nil)
			require.NoError(t, err)
			req.Host = labelHost
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			require.Equal(t, int32(1), h.upstreamHits.Load(),
				"the label-host request must reach the app upstream (fixture binding)")
			require.Equal(t, http.StatusFound, resp.StatusCode,
				"DS-2b: Mode 1 does NOT apply the out-of-prefix 502")
			assert.Equal(t, want, resp.Header.Get("Location"),
				"DS-2b: the Location is emitted unchanged under a label Host")
		})
	}
}
