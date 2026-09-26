// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED tests for ADR-094 preview isolation, order 10
// (TestPreviewCredentialFilter, FR-020/DS-4, S-3.1–S-3.5; spec:
// docs/internal/specs/adsr-094-preview-isolation-spec.md).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: the dev-proxy request direction filters
//     credentials — Cookie pairs named exactly omnipus-session / csrf /
//     __Host-csrf dropped (case-sensitive), everything else forwarded (header
//     omitted when nothing remains); Authorization deleted ONLY for a Bearer
//     the gateway's own validator accepts (user tokens, CLI token, legacy env
//     token — NOT the ADR-004 credential store); the response direction keeps
//     neutralizeReservedSetCookies; the SAME filter serves both deployment
//     modes (one code path).
//   - Specification source: FR-020, DS-4 rows 1–12, S-3.1–S-3.5, A-3
//     (round-2 MAJ-003). Every expected forwarded string is the DS-4
//     "Forwarded" column, not observed output.
//   - Unit boundary: REAL production chain (buildProductionMiddlewareChain)
//     around a real mux with registerPreviewEndpoints, driven by a real
//     httptest.Server; the upstream is a REAL httptest dev server whose
//     handler records exactly what arrived — the Director (the unit under
//     test) is real, nothing mocked.
//   - RED shape: today's Director (rest_preview.go::proxyDevRequest, FR-013)
//     strips the ENTIRE Cookie AND Authorization headers — the OPPOSITE
//     failure of FR-020's name-scoped filtering. RED rows are the
//     over-strip: rows 1, 2, 4 (the app's own cookies must forward
//     post-GREEN), rows 6, 7, 10 (non-gateway Authorization must forward).
//     Rows 3, 5, 11, 12 pass today (whole-header deletion) and are pins
//     ACROSS the change: post-GREEN the name-scoped filter must keep
//     deleting exactly the reserved names and the gateway-accepted Bearer.
//   - Known gaps (documented): (1) DS-4 row 10's zero-bcrypt validator spy —
//     there is no filter yet to instrument, so the call-count seam does not
//     exist; the forwarding half of row 10 is pinned and CHECK re-runs the
//     spy against GREEN's filter (the same defer-to-CHECK pattern as
//     M-3…M-9). (2) The Mode 1 label-host mirror of the filter REDs at the
//     missing isolated_url mint today; post-GREEN it proves the one-code-path
//     claim by driving the same request under the label Host.
//   - Mutations: M-5 (revert the filter to forward-all) must flip rows 1, 3,
//     5, 11, 12 red again post-GREEN.

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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// harness — Mode 2 dev-proxy over the real production chain
// ---------------------------------------------------------------------------

// piRedSeenHeaders records exactly what the upstream received.
type piRedSeenHeaders struct {
	mu     sync.Mutex
	cookie string
	auth   string
}

// piRedSeen reads the recorded headers.
func (s *piRedSeenHeaders) piRedSeen() (cookie, auth string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookie, s.auth
}

// piRedFilterHarness: real chain, real mux, real dev-proxy, real upstream spy.
type piRedFilterHarness struct {
	api  *restAPI
	srv  *httptest.Server
	seen *piRedSeenHeaders

	// setRespCookies installs the upstream's outgoing Set-Cookie values for
	// the NEXT request (response-direction rows).
	setRespCookies func([]string)

	devToken   string // the dev registration's token (the /preview/ credential)
	port       string
	sessionTok string // legacy-shaped (no id, no dot) minted session plaintext
}

func piRedNewFilterHarness(t *testing.T) *piRedFilterHarness {
	t.Helper()
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	api, _ := newPreviewRouteTestAPI(t)
	h := &piRedFilterHarness{api: api, seen: &piRedSeenHeaders{}}
	cfg := api.agentLoop.GetConfig()

	// A minted session plaintext (legacy shape: no omnipus_ prefix, no dot)
	// wired as the user's SessionTokenHash.
	plaintext, hash, err := middleware.MintSessionToken()
	require.NoError(t, err)
	h.sessionTok = plaintext
	cfg.Gateway.Users = []config.UserConfig{{
		Username:         "pi-red-filter-user",
		SessionTokenHash: config.BcryptHash(hash),
	}}

	var (
		respMu      sync.Mutex
		respCookies []string
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, auth := r.Header.Get("Cookie"), r.Header.Get("Authorization")
		h.seen.mu.Lock()
		h.seen.cookie = cookie
		h.seen.auth = auth
		h.seen.mu.Unlock()

		// Echo what arrived into response headers so the test can read it
		// from the proxy's response as well.
		w.Header().Set("X-Seen-Cookie", cookie)
		if auth != "" {
			w.Header().Set("X-Seen-Auth", auth)
		} else {
			w.Header().Del("X-Seen-Auth")
		}

		// Response direction: send the test-installed Set-Cookie list back.
		respMu.Lock()
		out := respCookies
		respMu.Unlock()
		for _, c := range out {
			w.Header().Add("Set-Cookie", c)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	t.Cleanup(upstream.Close)

	h.setRespCookies = func(cs []string) {
		respMu.Lock()
		respCookies = cs
		respMu.Unlock()
	}

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devEntry, regErr := reg.Register("pi-red-filter-agent", upstreamPort(t, upstream.URL), 0 /*pid*/, "npm run dev", 10)
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

// piRedProxyGet drives a GET through the Mode 2 dev proxy
// (/preview/<agent>/<token>/<sub>), setting exactly the Cookie and
// Authorization headers given (empty string = header absent).
func (h *piRedFilterHarness) piRedProxyGet(
	t *testing.T, token, cookieVal, authVal string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		h.srv.URL+"/preview/pi-red-filter-agent/"+token+"/app.js", nil)
	require.NoError(t, err)
	if cookieVal != "" {
		req.Header.Set("Cookie", cookieVal)
	}
	if authVal != "" {
		req.Header.Set("Authorization", authVal)
	}
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// piRedMintUserToken mints an ID-tagged gateway user token
// ("omnipus_<id>_<body>", entry hash over the body — TokenSecret's split) or
// a legacy one (no id: hash over the whole raw).
func piRedMintUserToken(id, body string) (string, config.TokenEntry) {
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

// piRedFilterMintIsolated mints through the serve_web tool (static mode) and
// returns the parsed result payload — the Mode 1 mirror's label source.
func piRedFilterMintIsolated(
	t *testing.T, h *piRedFilterHarness, agentID string,
) map[string]any {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index.html"), []byte("<h1>x</h1>"), 0o644))

	tool := tools.NewWebServeTool(
		dir,
		agentID,
		func() *config.Config { return h.api.agentLoop.GetConfig() },
		h.api.servedSubdirs,
		nil, // static mode
		tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
		nil, nil, 60, 86400,
	)
	result := tool.Execute(tools.WithAgentID(context.Background(), agentID),
		map[string]any{"path": "."})
	require.False(t, result.IsError, "web_serve must succeed: %s", result.ForLLM)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed))
	return parsed
}

// ---------------------------------------------------------------------------
// order 10 — DS-4 cookie rows (request direction) + response direction
// ---------------------------------------------------------------------------

// TestPreviewCredentialFilter drives the DS-4 cookie table through the Mode 2
// dev proxy. RED rows fail as LEAKS today (the filter does not exist);
// forward rows are pins.
func TestPreviewCredentialFilter(t *testing.T) {
	h := piRedNewFilterHarness(t)

	t.Run("row1_mixed_reserved_and_app_cookies", func(t *testing.T) {
		cookieIn := "omnipus-session=s1; myapp_session=y; csrf=c1; __Host-csrf=h1; CSRF=lower-mismatch"
		h.piRedProxyGet(t, h.devToken, cookieIn, "")
		cookieOut, _ := h.seen.piRedSeen()

		require.Equal(t, "myapp_session=y; CSRF=lower-mismatch", cookieOut,
			"RED (FR-020, DS-4 row 1): only myapp_session=y; CSRF=lower-mismatch may forward — "+
				"the reserved names (case-sensitive) must be dropped; today the Director strips "+
				"the ENTIRE Cookie header (FR-013 strip-all), so even the app's own cookies never arrive")
	})

	t.Run("row2_app_only_unchanged", func(t *testing.T) {
		cookieIn := "myapp_session=y"
		h.piRedProxyGet(t, h.devToken, cookieIn, "")
		cookieOut, _ := h.seen.piRedSeen()
		assert.Equal(t, cookieIn, cookieOut,
			"RED (FR-020, DS-4 row 2): the app's own cookie must forward unchanged — today the "+
				"Director strips the ENTIRE Cookie header (FR-013 strip-all)")
	})

	t.Run("row3_reserved_only_header_omitted", func(t *testing.T) {
		h.piRedProxyGet(t, h.devToken, "omnipus-session=s1", "")
		cookieOut, _ := h.seen.piRedSeen()
		require.Empty(t, cookieOut,
			"DS-4 row 3 (pin across the change): with only reserved pairs the Cookie header is "+
				"absent today (strip-all) and must stay absent post-GREEN (omitted when nothing remains)")
	})

	t.Run("row4_duplicate_app_cookies_forward", func(t *testing.T) {
		cookieIn := "myapp_session=a; myapp_session=b"
		h.piRedProxyGet(t, h.devToken, cookieIn, "")
		cookieOut, _ := h.seen.piRedSeen()
		assert.Equal(t, cookieIn, cookieOut,
			"RED (FR-020, DS-4 row 4): duplicates of non-reserved names forward unchanged — today "+
				"the Director strips the ENTIRE Cookie header (FR-013 strip-all)")
	})

	t.Run("row8_upstream_reserved_setcookie_neutralized", func(t *testing.T) {
		h.setRespCookies([]string{"omnipus-session=upstream-val"})
		resp := h.piRedProxyGet(t, h.devToken, "", "")
		sc := resp.Header.Values("Set-Cookie")
		assert.NotContains(t, sc, "omnipus-session=upstream-val",
			"DS-4 row 8 (pin): upstream Set-Cookie for a reserved name stays neutralized (anti-fixation)")
	})

	t.Run("row9_upstream_app_setcookie_forwards", func(t *testing.T) {
		h.setRespCookies([]string{"myapp_session=appval"})
		resp := h.piRedProxyGet(t, h.devToken, "", "")
		sc := resp.Header.Values("Set-Cookie")
		assert.Contains(t, sc, "myapp_session=appval",
			"DS-4 row 9 (pin): the app's own Set-Cookie forwards (its login sticks)")
	})
}

// ---------------------------------------------------------------------------
// order 10 — Bearer-deletion matrix (DS-4 rows 5/10–12)
// ---------------------------------------------------------------------------

// TestPreviewCredentialFilter_BearerMatrix covers DS-4 rows 5, 6, 10, 11, 12
// against resolveBearerIdentity's exact semantics (A-3, round-2 MAJ-003).
func TestPreviewCredentialFilter_BearerMatrix(t *testing.T) {
	h := piRedNewFilterHarness(t)
	cfg := h.api.agentLoop.GetConfig()

	row5Raw, row5Entry := piRedMintUserToken("row5", strings.Repeat("b", 43))
	row11Raw, row11Entry := piRedMintUserToken("", strings.Repeat("l", 43))

	t.Run("row5_id_tagged_deleted", func(t *testing.T) {
		cfg.Gateway.Users[0].Tokens = []config.TokenEntry{row5Entry}
		h.piRedProxyGet(t, h.devToken, "", "Bearer "+row5Raw)
		_, authOut := h.seen.piRedSeen()
		require.Empty(t, authOut,
			"RED (FR-020, DS-4 row 5, A-3/MAJ-003): a Bearer the gateway's own validator accepts "+
				"(id-tagged user token) must be deleted — today it forwards to the upstream")
	})

	t.Run("row6_foreign_bearer_forwarded", func(t *testing.T) {
		cfg.Gateway.Users[0].Tokens = []config.TokenEntry{row5Entry}
		h.piRedProxyGet(t, h.devToken, "", "Bearer some-other-token")
		_, authOut := h.seen.piRedSeen()
		assert.Equal(t, "Bearer some-other-token", authOut,
			"RED (FR-020, DS-4 row 6): a Bearer the gateway validator does NOT accept forwards "+
				"unchanged — today the Director deletes the ENTIRE Authorization header")
	})

	t.Run("row7_basic_auth_forwarded", func(t *testing.T) {
		h.piRedProxyGet(t, h.devToken, "", "Basic dXNlcjpwYXNz")
		_, authOut := h.seen.piRedSeen()
		assert.Equal(t, "Basic dXNlcjpwYXNz", authOut,
			"RED (FR-020, DS-4 row 7): non-Bearer Authorization forwards unchanged — today the "+
				"Director deletes the ENTIRE Authorization header")
	})

	t.Run("row10_jwt_forwarded", func(t *testing.T) {
		cfg.Gateway.Users[0].Tokens = []config.TokenEntry{row5Entry}
		jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhIn0.c2ln"
		h.piRedProxyGet(t, h.devToken, "", "Bearer "+jwt)
		_, authOut := h.seen.piRedSeen()
		// Forwarding half is the RED row today: the Director deletes the
		// ENTIRE Authorization header. The ZERO-bcrypt-compare half needs a
		// validator spy (VerifyToken/VerifyCLIToken call count) — there is no
		// filter yet to instrument, so the seam does not exist; CHECK runs
		// the spy against GREEN's filter (same defer-to-CHECK as M-3…M-9).
		assert.Equal(t, "Bearer "+jwt, authOut,
			"RED (FR-020, DS-4 row 10): a non-gateway JWT forwards post-GREEN — today the "+
				"Director deletes the ENTIRE Authorization header. (The zero-compare half is "+
				"CHECK's spy run, post-GREEN.)")
	})

	t.Run("row11_legacy_user_token_deleted", func(t *testing.T) {
		cfg.Gateway.Users[0].Tokens = []config.TokenEntry{row11Entry}
		h.piRedProxyGet(t, h.devToken, "", "Bearer "+row11Raw)
		_, authOut := h.seen.piRedSeen()
		require.Empty(t, authOut,
			"DS-4 row 11 (pin across the change): a legacy user token (no id prefix) the validator "+
				"accepts is deleted today (strip-all) and must STAY deleted post-GREEN (bounded scan)")
	})

	t.Run("row12_env_token_deleted", func(t *testing.T) {
		t.Setenv("OMNIPUS_BEARER_TOKEN", "pi-red-env-token-value")
		h.piRedProxyGet(t, h.devToken, "", "Bearer pi-red-env-token-value")
		_, authOut := h.seen.piRedSeen()
		require.Empty(t, authOut,
			"DS-4 row 12 (pin across the change): the legacy env token is deleted today (strip-all) "+
				"and must STAY deleted post-GREEN (resolveBearerIdentity's third source)")
	})
}

// ---------------------------------------------------------------------------
// order 10 — one code path, both modes (S-3.5)
// ---------------------------------------------------------------------------

// TestPreviewCredentialFilter_OneCodePath drives the same request under the
// Mode 1 label Host and asserts the SAME filtered set reaches the upstream
// (FR-020: "one code path, no mode-conditional behaviour"). RED today at the
// missing mint; post-GREEN it drives the request and asserts equality with
// Mode 2's row-1 forwarded set.
func TestPreviewCredentialFilter_OneCodePath(t *testing.T) {
	h := piRedNewFilterHarness(t)
	cfg := h.api.agentLoop.GetConfig()
	// Mode 1 canonical origin on the real listener port (DS-3 row 1).
	cfg.Gateway.PublicURL = "http://localhost:" + h.port

	parsed := piRedFilterMintIsolated(t, h, "pi-red-filter-agent")
	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-001/FR-022): no isolated_url mint exists yet — the Mode 1 mirror of the "+
			"credential filter is RED until the dual-URL contract lands; post-GREEN this test "+
			"drives the SAME request under the label Host and asserts the same filtered set")
	_ = isoRaw

	// Post-GREEN (kept compilable, reached only after the mint exists):
	// drive the row-1 request under the label Host.
	labelURL, _ := isoRaw.(string)
	require.NotEmpty(t, labelURL)
	host := strings.TrimPrefix(labelURL, "http://")
	host = strings.TrimSuffix(host, "/")

	cookieIn := "omnipus-session=s1; myapp_session=y; csrf=c1; __Host-csrf=h1; CSRF=lower-mismatch"
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/anything", nil)
	require.NoError(t, err)
	req.Host = host
	req.Header.Set("Cookie", cookieIn)
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	cookieOut, _ := h.seen.piRedSeen()
	assert.Equal(t, "myapp_session=y; CSRF=lower-mismatch", cookieOut,
		"S-3.5: the Mode 1 label-host direction applies the SAME filter (one code path)")
}
