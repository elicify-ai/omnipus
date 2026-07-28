//go:build !cgo

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
