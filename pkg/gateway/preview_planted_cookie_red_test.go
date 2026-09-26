// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED tests for ADR-094 preview isolation, order 11
// (FR-015/DS-5, S-4.1–S-4.3; spec:
// docs/internal/specs/adr-094-preview-isolation-spec.md).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: on a main-Host request whose Cookie header
//     carries more than one occurrence of a reserved gateway cookie name
//     (exact: omnipus-session / csrf / __Host-csrf), the gateway runs the
//     detector OUTSIDE CSRFMiddleware and before any credential read, marks
//     the credential read failed, emits the founder-corrected clear set, and
//     answers state-changing requests with the typed planted_cookie_cleared
//     error. Preview-Host requests are out of scope (MIN-007).
//   - Specification source: FR-015 (verbatim algorithm), DS-5 rows 1–9,
//     S-4.1–S-4.3, Q4 round-2 CRIT-001. The expected clear set below is
//     GENERATED from the FR-015 algorithm written out in the test — never
//     from observed output.
//   - Unit boundary: REAL production chain (CSRF outermost over
//     configSnapshot over the mux) around the real registrations; a real
//     httptest.Server. The detector/clearer is post-GREEN code — every
//     detection row REDs today at its absence.
//   - RED shape: today nothing detects planted duplicates (the request dies
//     at CSRF or at the session read with no clear lines). Post-GREEN the
//     detector sits in the production chain; GREEN must extend
//     buildProductionMiddlewareChain (preview_csrf_realmux_test.go) to match
//     gateway.go's wrap order so these tests drive the same chain.
//   - Known gaps: the SPA retry surfaces (orders 27/28) are frontend RED's
//     scope; S-4.4 is E2E (frontend). The 2x2x(depth+1)-1 bound is implied
//     by exact set-equality.
//   - Mutations: M-6 (remove the detector) must flip every detection row
//     red post-GREEN.

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// piRedPlantedHarness: main-Host chain with a wired session user.
type piRedPlantedHarness struct {
	api        *restAPI
	srv        *httptest.Server
	port       string
	sessionTok string
}

func piRedNewPlantedHarness(t *testing.T) *piRedPlantedHarness {
	t.Helper()
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	api, _ := newPreviewRouteTestAPI(t)
	h := &piRedPlantedHarness{api: api}
	cfg := api.agentLoop.GetConfig()

	plaintext, hash, err := middleware.MintSessionToken()
	require.NoError(t, err)
	h.sessionTok = plaintext
	cfg.Gateway.Users = []config.UserConfig{{
		Username:         "pi-red-planted-user",
		SessionTokenHash: config.BcryptHash(hash),
	}}

	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		api.withAuth(api.HandleAgents)(w, r)
	})
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})

	chain := buildProductionMiddlewareChain(api, mainMux)
	h.srv = httptest.NewServer(chain)
	t.Cleanup(h.srv.Close)
	h.port = fmt.Sprintf("%d", upstreamPort(t, h.srv.URL))

	// Mode 1 canonical origin on the listener port (for the MIN-007 row).
	cfg.Gateway.PublicURL = "http://localhost:" + h.port
	cfg.Gateway.PreviewEnabled = boolPtr(true)

	return h
}

// piRedDoPlanted issues a main-Host request with an explicit Cookie header
// value (raw header control, so duplicate names are exactly as intended).
func (h *piRedPlantedHarness) piRedDoPlanted(
	t *testing.T, method, path, cookieHeader string, extraHeader, extraValue string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, nil)
	require.NoError(t, err)
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	if extraHeader != "" {
		req.Header.Set(extraHeader, extraValue)
	}
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// ---------------------------------------------------------------------------
// FR-015 clear-set oracle generator
// ---------------------------------------------------------------------------

// piRedClearSetExpectation enumerates the FR-015 clear set for a request
// path: for every /-boundary prefix P (full path and every ancestor through
// "/"), at both P and P+"/": the Domain=localhost form at every such path
// INCLUDING "/", the host-only form at every such path EXCEPT "/".
func piRedClearSetExpectation(path string) (domainForm, hostOnlyForm []string) {
	prefixes := piRedPathPrefixes(path)
	for _, p := range prefixes {
		domainForm = append(domainForm, p, p+"/")
		if p != "/" {
			hostOnlyForm = append(hostOnlyForm, p, p+"/")
		}
	}
	return domainForm, hostOnlyForm
}

// piRedPathPrefixes returns the full path and every /-boundary ancestor
// through "/". For "/api/v1/agents": /api/v1/agents, /api/v1, /api, /.
func piRedPathPrefixes(path string) []string {
	var prefixes []string
	seen := map[string]bool{}
	p := path
	for {
		if !seen[p] {
			seen[p] = true
			prefixes = append(prefixes, p)
		}
		if p == "/" {
			break
		}
		i := strings.LastIndexByte(p, '/')
		if i <= 0 {
			if !seen["/"] {
				seen["/"] = true
				prefixes = append(prefixes, "/")
			}
			break
		}
		p = p[:i]
	}
	return prefixes
}

// piRedAssertClearSet verifies the FR-015 clear set EXACTLY: every expected
// (form, path) line present, no extra lines, each line empty-valued with
// Max-Age=0 and a past Expires. secureOn forces Secure on every line (DS-5
// row 4: __Host-csrf mirrors its issuance posture — Secure on plain HTTP).
func piRedAssertClearSet(
	t *testing.T, resp *http.Response, name, reqPath string, secureOn bool,
) int {
	t.Helper()
	domainForm, hostOnlyForm := piRedClearSetExpectation(reqPath)

	want := map[string]int{} // "domain|<path>" / "hostonly|<path>" -> count
	for _, p := range domainForm {
		want["domain|"+p]++
	}
	for _, p := range hostOnlyForm {
		want["hostonly|"+p]++
	}

	got := map[string]int{}
	for _, raw := range resp.Header.Values("Set-Cookie") {
		c, err := http.ParseSetCookie(raw)
		if err != nil || c.Name != name {
			continue
		}
		form := "hostonly"
		if c.Domain != "" {
			form = "domain"
			assert.Equal(t, "localhost", c.Domain,
				"the Domain form is exactly Domain=localhost")
		}
		got[form+"|"+c.Path]++

		assert.Equal(t, "", c.Value, "clear lines are empty-valued (%s)", raw)
		assert.True(t, strings.Contains(strings.ToLower(raw), "max-age=0"),
			"clear lines carry Max-Age=0 (%s)", raw)
		if exp := c.Expires; !exp.IsZero() {
			assert.True(t, exp.Before(time.Now().Add(-time.Second)),
				"clear lines carry a PAST Expires (%s)", raw)
		} else {
			assert.Fail(t, "clear lines must carry an Expires attribute", "%s", raw)
		}
		if secureOn {
			assert.True(t, c.Secure, "__Host-csrf clear lines mirror issuance: Secure on (%s)", raw)
		} else {
			assert.False(t, c.Secure,
				"Secure mirrors the plain-HTTP request posture: absent (%s)", raw)
		}
	}

	assert.Equal(t, want, got,
		"FR-015 clear set for %s on %s must match the founder algorithm exactly "+
			"(Domain form incl. /, host-only excl. /, both slash forms)", name, reqPath)

	// The genuine cookie is host-only at exactly Path=/ — never in the set.
	assert.NotContains(t, got, "hostonly|/",
		"the genuine host-only Path=/ cookie must NEVER be in the clear set (CRIT-001)")
	return len(got)
}

// ---------------------------------------------------------------------------
// order 11 — the FR-015 clear-set algorithm (set-equality per depth)
// ---------------------------------------------------------------------------

// TestPreviewPlantedCookieClearSet drives a POST with a planted
// omnipus-session duplicate and asserts the response's clear set EXACTLY
// equals the FR-015 enumeration at three depths. RED today: no clear lines
// exist at all (the detector does not exist).
func TestPreviewPlantedCookieClearSet(t *testing.T) {
	h := piRedNewPlantedHarness(t)

	plant := "omnipus-session=real; omnipus-session=planted"

	t.Run("depth3_api_v1_agents", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodPost, "/api/v1/agents", plant, "", "")
		// 2 x 2 x (3+1) - 1 = 15 lines.
		n := piRedAssertClearSet(t, resp, "omnipus-session", "/api/v1/agents", false)
		require.Equal(t, 15, n,
			"RED (FR-015, bound 2x2x(depth+1)-1): expected exactly 15 clear lines for a depth-3 "+
				"path — the detector/clearer does not exist yet")
	})

	t.Run("depth1_agents", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodPost, "/agents", plant, "", "")
		// 2 x 2 x (1+1) - 1 = 7 lines.
		n := piRedAssertClearSet(t, resp, "omnipus-session", "/agents", false)
		require.Equal(t, 7, n, "FR-015 bound at depth 1")
	})

	t.Run("depth0_root", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodPost, "/", plant, "", "")
		// 2 x 2 x (0+1) - 1 = 3 lines (Domain at / and //, host-only at //).
		n := piRedAssertClearSet(t, resp, "omnipus-session", "/", false)
		require.Equal(t, 3, n, "FR-015 bound at the root path")
	})

	t.Run("host_csrf_secure_on", func(t *testing.T) {
		// DS-5 row 4: __Host-csrf clear lines mirror the ISSUANCE posture —
		// Secure on even on plain HTTP (the name never legitimately exists
		// without Secure).
		resp := h.piRedDoPlanted(t, http.MethodPost, "/api/v1/agents",
			"__Host-csrf=one; __Host-csrf=two", "", "")
		piRedAssertClearSet(t, resp, "__Host-csrf", "/api/v1/agents", true)
		n := 0
		for _, raw := range resp.Header.Values("Set-Cookie") {
			if c, err := http.ParseSetCookie(raw); err == nil && c.Name == "__Host-csrf" {
				n++
			}
		}
		require.Equal(t, 15, n, "RED (FR-015, DS-5 row 4): the __Host-csrf clear set "+
			"follows the same enumeration with Secure on — no detector exists yet")
	})
}

// ---------------------------------------------------------------------------
// order 11 — DS-5 detection rows (detector outside CSRFMiddleware)
// ---------------------------------------------------------------------------

// TestPreviewPlantedCookieDetection drives the DS-5 rows. RED today: the
// detector does not exist — a GET dies at the session read with no clear
// lines, and a POST dies at CSRFMiddleware's generic 403 with a code-less
// envelope (FR-015 step 1's named failure mode, round-2 MAJ-002).
func TestPreviewPlantedCookieDetection(t *testing.T) {
	h := piRedNewPlantedHarness(t)

	t.Run("row1_get_dup_session_401_with_clear_lines", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/agents",
			"omnipus-session=real; omnipus-session=planted", "", "")
		// DS-5 row 1: GET -> 401 with clear lines. The 401 itself is pinned
		// (the credential read is marked failed both today and post-GREEN);
		// the RED is the missing clear set.
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
			"DS-5 row 1: the GET answers 401")
		n := piRedAssertClearSet(t, resp, "omnipus-session", "/api/v1/agents", false)
		require.Equal(t, 15, n,
			"RED (FR-015, DS-5 row 1): the 401 carries no clear lines today — the detector and "+
				"the founder-corrected clear set do not exist yet; expected exactly 15")
	})

	t.Run("row2_post_dup_session_typed_error", func(t *testing.T) {
		// DS-5 row 2: state-changing -> standard JSON envelope, code
		// planted_cookie_cleared, message "Omnipus cleared cookies set by a
		// preview — please retry" (Q4), plus the clear lines.
		resp := h.piRedDoPlanted(t, http.MethodPost, "/api/v1/agents",
			"omnipus-session=real; omnipus-session=planted", "", "")
		body := piRedBody(t, resp)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &parsed),
			"the planted response must be the standard JSON error envelope")
		require.Equal(t, "planted_cookie_cleared", parsed["code"],
			"RED (FR-015, DS-5 row 2): today the POST dies at CSRFMiddleware's generic 403 with "+
				"a code-less envelope (got %q) — the detector does not run outside CSRFMiddleware yet", body)
		assert.Contains(t, body, "Omnipus cleared cookies set by a preview — please retry",
			"the human message is verbatim from FR-015 (Q4)")
		piRedAssertClearSet(t, resp, "omnipus-session", "/api/v1/agents", false)
	})

	t.Run("row3_post_dup_csrf_typed_error_not_generic_403", func(t *testing.T) {
		// DS-5 row 3: the planted name is csrf itself. The response must be the
		// planted envelope — NOT CSRFMiddleware's generic mismatch — proving
		// the detector runs BEFORE CSRFMiddleware (round-2 MAJ-002).
		resp := h.piRedDoPlanted(t, http.MethodPost, "/api/v1/agents",
			"csrf=one; csrf=two", "", "")
		body := piRedBody(t, resp)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &parsed))
		require.Equal(t, "planted_cookie_cleared", parsed["code"],
			"RED (FR-015, DS-5 row 3): today this is CSRFMiddleware's generic mismatch (got %q) — "+
				"behind CSRF, csrfCookieValue validates the longer-path plant first and the request "+
				"dies with no recovery; the detector is not outside CSRFMiddleware yet", body)
	})

	t.Run("row4_post_dup_host_csrf_typed_error", func(t *testing.T) {
		// DS-5 row 4: __Host-csrf duplicates — same typed error; its clear
		// lines' Secure posture is asserted in TestPreviewPlantedCookieClearSet
		// (host_csrf_secure_on).
		resp := h.piRedDoPlanted(t, http.MethodPost, "/api/v1/agents",
			"__Host-csrf=one; __Host-csrf=two", "", "")
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(piRedBody(t, resp)), &parsed))
		require.Equal(t, "planted_cookie_cleared", parsed["code"],
			"RED (FR-015, DS-5 row 4): __Host-csrf duplicates must hit the planted detector, "+
				"not the generic CSRF 403 — no detector exists yet")
	})

	t.Run("row5_non_reserved_dup_not_intercepted", func(t *testing.T) {
		// DS-5 row 5: duplicates of a NON-reserved name never trigger the
		// detector — the valid session still authenticates. PIN across the
		// change (holds today: nothing intercepts anything).
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/agents",
			"omnipus-session="+h.sessionTok+"; myapp_session=a; myapp_session=b", "", "")
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"DS-5 row 5 (pin): non-reserved duplicates are not intercepted; the genuine session works")
		assert.NotContains(t, piRedBody(t, resp), "planted_cookie_cleared",
			"no planted interception for non-reserved names")
	})

	t.Run("row6_single_occurrence_not_intercepted", func(t *testing.T) {
		// DS-5 row 6: exactly ONE occurrence of a reserved name is the genuine
		// shape — never intercepted. PIN across the change.
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/agents",
			"omnipus-session="+h.sessionTok, "", "")
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"DS-5 row 6 (pin): a single occurrence is never intercepted")
		assert.NotContains(t, piRedBody(t, resp), "planted_cookie_cleared",
			"no planted interception for a single occurrence")
	})
}

// piRedPlantedMintIsolated mints through the serve_web tool (static mode)
// against the planted harness and returns the Mode 1 label — the MIN-007
// row's preview-Host source.
func piRedPlantedMintIsolated(
	t *testing.T, h *piRedPlantedHarness, agentID string,
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

// TestPreviewPlantedCookiePreviewHostScope pins MIN-007: a preview-Host
// request NEVER triggers the planted detector — "a previewed app's own
// csrf-named cookie never triggers this". RED today at the missing mint (no
// isolated_url exists yet, so no real preview Host can be driven).
func TestPreviewPlantedCookiePreviewHostScope(t *testing.T) {
	h := piRedNewPlantedHarness(t)

	parsed := piRedPlantedMintIsolated(t, h, "pi-red-planted-agent")
	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-001/FR-022): no isolated_url mint exists yet — the MIN-007 preview-Host scope "+
			"row cannot drive a real label host until the dual-URL contract lands")

	host := strings.TrimPrefix(isoRaw.(string), "http://")
	host = strings.TrimSuffix(host, "/")

	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/api/v1/agents", nil)
	require.NoError(t, err)
	req.Host = host // preview Host: the dispatch must bypass the main chain
	req.Header.Set("Cookie", "csrf=one; csrf=two")
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body := piRedBody(t, resp)
	assert.NotContains(t, body, "planted_cookie_cleared",
		"MIN-007: the planted detector never fires on a preview-Host request")
	assert.NotContains(t, body, "Omnipus cleared cookies set by a preview",
		"MIN-007: no planted clear lines on a preview-Host request")
}
