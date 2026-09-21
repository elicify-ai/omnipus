// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Issue #800 (Bedrock region contract): the region is a per-provider-row
// setting, saved and echoed by PUT/GET /api/v1/providers/{id} — never
// hard-wired to the catalog's default. These tests exercise the REST layer
// only (persistence + wire echo); runtime resolution precedence and
// model-id resolution are covered in pkg/providers.

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// bedrockCatalogDocJSON renders a minimal 2.0.0 document carrying one
// amazon-bedrock row shaped exactly per the Bedrock region contract:
// `regions: [{id, group}]` on the provider, `inference_profiles` on a model.
func bedrockCatalogDocJSON() []byte {
	updated := time.Now().UTC().Format(time.RFC3339)
	return []byte(fmt.Sprintf(`{
		"schema_version": "2.0.0",
		"version": "v2026.9.21",
		"updated_at": %q,
		"source": "test-fixture",
		"default_resize_limits": { "long_edge_px": 7680, "max_bytes": 10485760 },
		"providers": [
			{
				"id": "amazon-bedrock",
				"name": "Amazon Bedrock",
				"company": "Amazon Bedrock",
				"api": "https://bedrock-runtime.us-east-1.amazonaws.com",
				"protocol": "bedrock",
				"region": "us-east-1",
				"regions": [
					{"id": "us-east-1", "group": "us"},
					{"id": "eu-central-1", "group": "eu"}
				],
				"tier": "standard",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "anthropic.claude-sonnet-4-5-20250929-v1:0", "name": "Claude Sonnet 4.5",
					 "tool_call": true, "context_window": 200000, "max_output_tokens": 64000,
					 "input_modalities": ["text"], "status": "active", "inference_profiles": ["us", "eu"]}
				]
			}
		]
	}`, updated))
}

func bedrockCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.NewCatalog(bedrockCatalogDocJSON())
	require.NoError(t, err, "the bedrock region test fixture must parse")
	return c
}

func TestRestProviders_PUT_PersistsAndEchoesRegion(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = bedrockCatalog(t)
	// Pre-seed an already-configured row (api_key_ref present) so this PUT
	// can omit api_key — a body with no api_key field skips the billable
	// upstream key-validation probe entirely (mirrors "row 6" in
	// TestRestProviders_PUT_Unknown_CloudIAM_Custom), keeping this test
	// hermetic: no real network call to AWS.
	seedProviderConfig(t, api, map[string]any{
		"provider": "amazon-bedrock", "model": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"api_key_ref": "T800_BEDROCK_KEY_SEED",
	})

	w := doPutProvider(t, api, "amazon-bedrock",
		`{"model":"anthropic.claude-sonnet-4-5-20250929-v1:0","region":"eu-central-1"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.NotNil(t, got.Region, "PUT response must echo the region it just saved")
	assert.Equal(t, "eu-central-1", *got.Region)

	persisted := persistedProviderRow(t, api, "amazon-bedrock")
	assert.Equal(t, "eu-central-1", persisted["region"], "region must be persisted verbatim in config.json")
}

func TestRestProviders_GET_EchoesPersistedRegion(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = bedrockCatalog(t)
	seedProviderConfig(t, api, map[string]any{
		"provider": "amazon-bedrock", "model": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"api_key_ref": "T800_BEDROCK_KEY", "region": "eu-central-1",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	api.HandleProviders(w, isolateRateLimit(t, r))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Region, "GET must echo the row's persisted region")
	assert.Equal(t, "eu-central-1", *got[0].Region)
}

func TestRestProviders_PUT_OmittedRegion_LeavesPersistedValueAlone(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = bedrockCatalog(t)
	seedProviderConfig(t, api, map[string]any{
		"provider": "amazon-bedrock", "model": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"api_key_ref": "T800_BEDROCK_KEY_2", "region": "eu-central-1",
	})

	// A model-only edit that never mentions region must not clobber it —
	// mirrors applyProviderIdentity's existing rule for api_base/protocol.
	w := doPutProvider(t, api, "amazon-bedrock", `{"model":"anthropic.claude-sonnet-4-5-20250929-v1:0"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	persisted := persistedProviderRow(t, api, "amazon-bedrock")
	assert.Equal(t, "eu-central-1", persisted["region"], "an omitted region field must not erase the persisted value")
}
