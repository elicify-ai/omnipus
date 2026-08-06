// D18: GET /api/v1/providers/model-capabilities — the SPA warn-and-proceed
// pre-send check needs a flat {id, modalities}[] list from the backend's
// in-repo capability catalog (pkg/providers/capabilities), because model
// vision capability is not knowable client-side at all. This file proves
// both the nil-catalog degrade-gracefully path (empty list, never a 500)
// and the populated-catalog path (real modalities on the wire).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/capabilities"
)

// TestHandleProviders_ModelCapabilitiesGET_EmptyWhenAgentLoopNil verifies the
// D18 endpoint degrades gracefully (200 + empty array, never a 500) when the
// restAPI has no AgentLoop wired at all — a genuine degraded-boot shape
// already handled elsewhere in this package (see rest_preview_audit.go's
// `a == nil || a.agentLoop == nil` guard). NewAgentLoop itself always wires
// the embedded-seed catalog (loop.go ~:645), so this is the only realistic
// path that reaches a nil catalog in the handler.
func TestHandleProviders_ModelCapabilitiesGET_EmptyWhenAgentLoopNil(t *testing.T) {
	api := &restAPI{allowedOrigin: "http://localhost:3000"}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/model-capabilities", nil)
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var caps []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &caps))
	assert.Empty(t, caps, "no AgentLoop wired must degrade to an empty list, never a 500")
}

// TestHandleProviders_ModelCapabilitiesGET_NilAgentLoopLogsWarn is the
// 7-reviewer-gate Finding 4 regression test. The nil-AgentLoop degraded-boot
// shape above returns [] on the wire — byte-identical to "the catalog
// legitimately has no entries". The SPA's modelLacksImageCapability treats
// an absent entry as "assume it can" (FR-026's intentional optimistic
// default), so a degraded boot silently produced a blanket all-clear (no
// vision warnings for anyone, ever) with NO signal anywhere that anything
// was wrong. The fix keeps the wire contract exactly as-is (still [], never
// a 500 — this is an advisory endpoint) but logs a WARN server-side so the
// degraded state is at least observable in the gateway log.
//
// Reuses cookieAuthLogRecorder (cookie_auth_observability_test.go, same
// package) rather than declaring a second slog-capturing test double.
func TestHandleProviders_ModelCapabilitiesGET_NilAgentLoopLogsWarn(t *testing.T) {
	recorder := installCookieAuthLogRecorder(t)

	api := &restAPI{allowedOrigin: "http://localhost:3000"}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/model-capabilities", nil)
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code, "the wire contract must not change: still 200 + [], never a 500")

	assert.True(t, recorder.contains("model-capabilities"),
		"a nil AgentLoop (degraded boot) must log a WARN naming the endpoint, so the degraded state is "+
			"observable server-side instead of silently producing a blanket vision-warning all-clear")
}

// TestHandleProviders_ModelCapabilitiesGET_PopulatedCatalogDoesNotWarn is the
// negative companion: the normal, healthy-boot path (AgentLoop wired, real
// catalog) must NOT emit the degraded-boot WARN — it would be a false
// positive that trains operators to ignore the log.
func TestHandleProviders_ModelCapabilitiesGET_PopulatedCatalogDoesNotWarn(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	require.NotNil(
		t, api.agentLoop.GetCapabilityCatalog(),
		"precondition: NewAgentLoop wires the embedded seed by default",
	)

	recorder := installCookieAuthLogRecorder(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/model-capabilities", nil)
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.False(t, recorder.contains("model-capabilities"),
		"a healthy boot with a real AgentLoop/catalog must never emit the degraded-boot WARN")
}

// TestHandleProviders_ModelCapabilitiesGET_DefaultsToEmbeddedSeed verifies
// that a normally-constructed AgentLoop (NewAgentLoop always wires the
// embedded-seed catalog, loop.go ~:645) already serves real per-model
// modalities on this endpoint with zero extra wiring.
func TestHandleProviders_ModelCapabilitiesGET_DefaultsToEmbeddedSeed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	require.NotNil(
		t, api.agentLoop.GetCapabilityCatalog(),
		"precondition: NewAgentLoop wires the embedded seed by default",
	)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/model-capabilities", nil)
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var caps []struct {
		ID         string   `json:"id"`
		Modalities []string `json:"modalities"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &caps))
	assert.NotEmpty(t, caps, "embedded seed catalog must yield at least one entry")
}

// TestHandleProviders_ModelCapabilitiesGET_ReturnsCatalogEntries verifies
// that once a capability catalog is wired (mirroring gateway boot's
// SetCapabilityCatalog call), the endpoint returns the real per-model
// modalities on the wire — this is what the SPA's modelLacksImageCapability
// decision reads.
func TestHandleProviders_ModelCapabilitiesGET_ReturnsCatalogEntries(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	seed := []byte(`{
		"version": "test-1",
		"schema_version": "1",
		"updated_at": "2026-01-01T00:00:00Z",
		"source": "test",
		"default_resize_budget": {"long_edge_px": 4096, "max_bytes": 10485760},
		"models": [
			{"id": "vision-model", "provider": "test", "input_modalities": ["text", "image"]},
			{"id": "glm-5.2", "provider": "z-ai", "input_modalities": ["text"]}
		]
	}`)
	catalog, err := capabilities.NewCatalog(seed, nil, nil, nil)
	require.NoError(t, err)
	api.agentLoop.SetCapabilityCatalog(catalog)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/model-capabilities", nil)
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var caps []struct {
		ID         string   `json:"id"`
		Modalities []string `json:"modalities"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &caps))
	require.Len(t, caps, 2)

	byID := map[string][]string{}
	for _, c := range caps {
		byID[c.ID] = c.Modalities
	}
	assert.ElementsMatch(t, []string{"text", "image"}, byID["vision-model"])
	assert.ElementsMatch(t, []string{"text"}, byID["glm-5.2"])
	assert.NotContains(t, byID["glm-5.2"], "image", "text-only model must not report image support")
}

// TestHandleProviders_ModelCapabilitiesGET_DropsUnrepresentableModalities is the
// regression test for a silent failure introduced alongside the endpoint itself
// and caught in review.
//
// The INTERNAL Modality type is deliberately open: modality.go accepts any
// non-empty unknown value so an operator can seed a modality ahead of runtime
// support, and TestParseSeed_AcceptsUnknownModalities pins that as intended.
// The WIRE enum (ModelCapabilities.yaml) is closed to
// text|image|pdf|audio|video. The handler originally cast straight from one to
// the other, so an operator-seeded "3d" landed on the wire as an out-of-enum
// value.
//
// Why that was severe rather than cosmetic: the SPA validates this response
// with z.array(z.enum(...)), which fails the ENTIRE array on a single bad
// element — and both consumers (browserAnnotate.ts, attachment-adapter.ts)
// treated a failed capability fetch as best-effort, non-blocking (by
// design — see the 7-reviewer-gate follow-on fix that upgraded their
// console.debug to console.warn and distinguished a schema-validation
// failure from an ordinary network failure). So ONE forward-compat
// modality anywhere in the catalog silently disabled the vision pre-send
// warning for EVERY model, with no user-visible signal.
//
// Correct behavior: drop values the wire cannot represent, keep the rest. A
// model advertising ["text","3d"] reports ["text"] — which is exactly right for
// this endpoint's advisory purpose, since it still lacks "image" and the
// warning still fires.
func TestHandleProviders_ModelCapabilitiesGET_DropsUnrepresentableModalities(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	seed := []byte(`{
		"version": "test-1",
		"schema_version": "1",
		"updated_at": "2026-01-01T00:00:00Z",
		"source": "test",
		"default_resize_budget": {"long_edge_px": 4096, "max_bytes": 10485760},
		"models": [
			{"id": "future-model", "provider": "test", "input_modalities": ["text", "3d", "image"]},
			{"id": "exotic-text", "provider": "test", "input_modalities": ["text", "hologram"]}
		]
	}`)
	catalog, err := capabilities.NewCatalog(seed, nil, nil, nil)
	require.NoError(t, err)
	api.agentLoop.SetCapabilityCatalog(catalog)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/model-capabilities", nil)
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var caps []struct {
		ID         string   `json:"id"`
		Modalities []string `json:"modalities"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &caps))

	byID := map[string][]string{}
	for _, c := range caps {
		byID[c.ID] = c.Modalities
	}

	// The out-of-enum value is dropped; the representable ones survive.
	assert.ElementsMatch(t, []string{"text", "image"}, byID["future-model"],
		"unknown modality must be dropped, not emitted, and must not take the valid ones down with it")

	// A model whose ONLY other modality is unrepresentable still appears, and
	// still reports the modalities that ARE representable.
	//
	// Note: an "all modalities unrepresentable" case is unreachable by
	// construction — the catalog validator rejects any model whose
	// input_modalities omits "text" ("every model supports text"), and "text"
	// is always representable. So the emitted list can never be empty here;
	// asserting that it could be would be testing a state the domain forbids.
	entry, ok := byID["exotic-text"]
	require.True(t, ok, "a model with an unrepresentable modality must still be listed")
	assert.ElementsMatch(t, []string{"text"}, entry,
		"the unknown modality is dropped; the representable one survives")

	// Belt-and-braces: no response value may fall outside the wire enum, since
	// a single one poisons the client's whole-array validation.
	allowed := map[string]bool{"text": true, "image": true, "pdf": true, "audio": true, "video": true}
	for id, mods := range byID {
		for _, m := range mods {
			assert.True(t, allowed[m], "model %q emitted out-of-enum modality %q", id, m)
		}
	}
}
