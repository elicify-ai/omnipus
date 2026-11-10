// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — planted-cookie recovery on GET (review finding F5,
// backend half; spec docs/internal/specs/adr-094-preview-isolation-spec.md
// FR-015 and the typed-error bullet: "For planted duplicates on GETs the
// gateway answers 401 with the clear lines attached", and the SPA keys its
// one Retry toast on the machine code planted_cookie_cleared).
//
// Test plan (elicify-test-writing):
//
//   - Behaviour under test: an authenticated GET whose Cookie header carries a
//     duplicated reserved name answers 401 with the generated ErrorResponse
//     envelope, code planted_cookie_cleared and the verbatim human message,
//     plus the clear lines — the response src/lib/api/http.ts::request
//     recognises. A clean retry with the genuine cookie then succeeds.
//   - Oracle: FR-015 (Q4 message, verbatim) and the spec's GET row (401 +
//     clear lines); the code is the one the SPA transport matches on.
//   - Unit boundary: the REAL production chain (planted-cookie guard, CSRF,
//     auth) around a real authenticated route (/api/v1/agents via withAuth)
//     with a real minted session.
//   - Cases: duplicated omnipus-session with a GENUINE first value (the
//     credential read must still fail closed); clean retry; a GET without a
//     duplicate is untouched — for an authenticated route (/api/v1/agents)
//     and the optional-auth boot read (/api/v1/state), where a top-level
//     navigation keeps the anonymous pass-through.

package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlantedCookieGET_TypedRecoveryResponse(t *testing.T) {
	h := piRedNewPlantedHarness(t)

	t.Run("planted_get_401_typed_envelope", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/agents",
			"omnipus-session="+h.sessionTok+"; omnipus-session=planted", "", "")
		t.Cleanup(func() { _ = resp.Body.Close() })
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
			"FR-015 GET row: a planted duplicate on a GET answers 401 (credential read fails closed)")
		assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body), "the 401 must be the JSON error envelope: %s", raw)
		assert.Equal(t, "planted_cookie_cleared", body["code"],
			"F5: the GET 401 must carry the machine code the SPA Retry toast keys on (got %s)", raw)
		assert.Equal(t, "Omnipus cleared cookies set by a preview — please retry", body["error"],
			"FR-015 (Q4): the human message is verbatim in the generated envelope's error field")
		piRedAssertClearSet(t, resp, "omnipus-session", "/api/v1/agents", false)
	})

	t.Run("clean_retry_succeeds", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/agents",
			"omnipus-session="+h.sessionTok, "", "")
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"the user's Retry (genuine cookie only) is authenticated again")
	})

	t.Run("no_duplicate_no_typed_code", func(t *testing.T) {
		pcgAssertNotPlanted(t, h, "/api/v1/agents")
	})
}

// pcgAssertNotPlanted: an ordinary invalid session is not a planted event.
func pcgAssertNotPlanted(t *testing.T, h *piRedPlantedHarness, path string) {
	t.Helper()
	resp := h.piRedDoPlanted(t, http.MethodGet, path, "omnipus-session=stale-not-a-session", "", "")
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "planted_cookie_cleared",
		"an ordinary invalid session is not a planted-cookie event")
}

// pcgNewStateHarness is piRedNewPlantedHarness plus the optional-auth
// /api/v1/state route, registered exactly as rest.go registers it.
func pcgNewStateHarness(t *testing.T) *piRedPlantedHarness {
	t.Helper()
	h := piRedNewPlantedHarness(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/state", h.api.withOptionalAuth(h.api.HandleState))
	srv := httptest.NewServer(buildProductionMiddlewareChain(h.api, mux))
	t.Cleanup(srv.Close)
	return &piRedPlantedHarness{api: h.api, srv: srv, port: h.port, sessionTok: h.sessionTok}
}

// TestPlantedCookieGET_OptionalAuthState: the SPA's boot read GET
// /api/v1/state is optional-auth; a planted duplicate must produce the same
// typed 401 (not an anonymous "signed out" state the route guard turns into a
// /login redirect with no Retry), while a browser navigation keeps the
// anonymous pass-through.
func TestPlantedCookieGET_OptionalAuthState(t *testing.T) {
	h := pcgNewStateHarness(t)
	planted := "omnipus-session=" + h.sessionTok + "; omnipus-session=planted"

	t.Run("planted_fetch_typed_401", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/state", planted, "Sec-Fetch-Mode", "cors")
		t.Cleanup(func() { _ = resp.Body.Close() })
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
			"F5: a planted GET /api/v1/state answers 401, not an anonymous state (got %s)", raw)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		assert.Equal(t, "planted_cookie_cleared", body["code"])
		assert.Equal(t, "Omnipus cleared cookies set by a preview — please retry", body["error"])
		piRedAssertClearSet(t, resp, "omnipus-session", "/api/v1/state", false)
	})

	t.Run("planted_navigation_not_typed", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/state", planted, "Sec-Fetch-Mode", "navigate")
		t.Cleanup(func() { _ = resp.Body.Close() })
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "planted_cookie_cleared",
			"a top-level navigation keeps the optional-auth anonymous pass-through")
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("clean_retry_succeeds", func(t *testing.T) {
		resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/state", "omnipus-session="+h.sessionTok, "", "")
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("no_duplicate_no_typed_code", func(t *testing.T) {
		pcgAssertNotPlanted(t, h, "/api/v1/state")
	})
}
