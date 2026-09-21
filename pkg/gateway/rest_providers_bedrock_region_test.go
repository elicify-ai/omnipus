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
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
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

// TestRestProvidersCatalog_GET_RegionsAndInferenceProfilesSurvive is the
// orchestrator-review regression: GET /api/v1/providers/catalog was
// returning amazon-bedrock with `regions` absent, although the parsed
// catalog Document had them and models carried `inference_profiles`.
// pkg/providers/catalog's served_test.go proves the same thing package-
// internally against the raw JSON body; this proves it at the REST layer,
// through the actual handler, unmarshalled into the SAME generated type
// (gen.ProvidersCatalog) the SPA's fetch client validates against — so a
// field-name mismatch between served.go's private marshal shape and the
// contract would fail here even if it silently dropped the value instead.
func TestRestProvidersCatalog_GET_RegionsAndInferenceProfilesSurvive(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = bedrockCatalog(t)

	w := getProvidersCatalog(api, "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got gen.ProvidersCatalog
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Providers, 1)

	bedrock := got.Providers[0]
	assert.Equal(t, "amazon-bedrock", bedrock.Id)
	require.NotNil(t, bedrock.Regions, "CatalogProvider.regions must survive the served GET, not just ParseDocument")
	require.Len(t, *bedrock.Regions, 2)
	assert.Equal(t, "us-east-1", (*bedrock.Regions)[0].Id)
	assert.Equal(t, gen.CatalogProviderRegionsGroup("us"), (*bedrock.Regions)[0].Group)
	assert.Equal(t, "eu-central-1", (*bedrock.Regions)[1].Id)
	assert.Equal(t, gen.CatalogProviderRegionsGroup("eu"), (*bedrock.Regions)[1].Group)

	require.Len(t, bedrock.Models, 1)
	model := bedrock.Models[0]
	require.NotNil(t, model.InferenceProfiles, "CatalogModel.inference_profiles must survive the served GET")
	assert.ElementsMatch(t, []gen.CatalogModelInferenceProfiles{"us", "eu"}, *model.InferenceProfiles)
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

// ── Orchestrator-approved scope addition (issue #800 follow-up): the live
// AWS ListInferenceProfiles refresh, triggered when the Bedrock row's region
// changes, cached on the row (non-secret), and gracefully degraded on 403.

func newBedrockAPIWithCredStore(t *testing.T) (*restAPI, *credentials.Store) {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = bedrockCatalog(t)
	store := credentials.NewStore(filepath.Join(api.homePath, "credentials.json"))
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	require.NoError(t, store.UnlockWithKey(key))
	api.credStore = store
	return api, store
}

func TestRestProviders_PUT_RegionChange_RefreshesLiveInferenceProfileCache(t *testing.T) {
	api, store := newBedrockAPIWithCredStore(t)
	require.NoError(t, store.Set("T800_BEDROCK_LIVE_KEY", "test-bedrock-key"))
	seedProviderConfig(t, api, map[string]any{
		"provider": "amazon-bedrock", "model": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"api_key_ref": "T800_BEDROCK_LIVE_KEY", "region": "us-east-1",
	})

	var gotHost, gotAuth string
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"inferenceProfileSummaries": [
				{
					"inferenceProfileId": "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
					"models": [{"modelArn": "arn:aws:bedrock:eu-central-1::foundation-model/anthropic.claude-sonnet-4-5-20250929-v1:0"}]
				}
			]
		}`))
	}))
	defer controlPlane.Close()
	api.bedrockControlPlaneBaseOverride = controlPlane.URL

	// A region-only change — no api_key in the body — still refreshes the
	// cache using the row's EXISTING stored key.
	w := doPutProvider(t, api, "amazon-bedrock",
		`{"model":"anthropic.claude-sonnet-4-5-20250929-v1:0","region":"eu-central-1"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NotEmpty(t, gotAuth, "the control-plane call must have been made")
	assert.Equal(t, "Bearer test-bedrock-key", gotAuth)
	assert.NotEmpty(t, gotHost)

	persisted := persistedProviderRow(t, api, "amazon-bedrock")
	profiles, ok := persisted["bedrock_inference_profiles"].(map[string]any)
	require.True(t, ok, "bedrock_inference_profiles must be persisted: %#v", persisted)
	assert.Equal(t, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
		profiles["anthropic.claude-sonnet-4-5-20250929-v1:0"])
}

// TestRefreshBedrockInferenceProfilesIfNeeded_KeyOnlyPUT_UsesPersistedRegion
// is orchestrator review round 3 (issue #800): a KEY-ONLY PUT (api_key
// changed, no region field of its own) on a row already persisted with
// region eu-central-1 fell back to row.Region — the CATALOG's us-east-1
// default — and PERSISTED the us-east-1 profiles as that row's live cache.
// A later real turn in eu-central-1 (factory_provider.go's ResolveModelIDLive)
// would then read a us.* profile id for a request actually going to
// eu-central-1 and fail. Required precedence, matching factory_provider.go's
// real-turn resolution exactly: request region -> the row's PERSISTED region
// (p.cfg, same lookup style as resolveBedrockRefreshAPIKey) -> AWS_REGION ->
// catalog default, via bedrock.ResolveRegion (no second copy of that logic).
func TestRefreshBedrockInferenceProfilesIfNeeded_KeyOnlyPUT_UsesPersistedRegion(t *testing.T) {
	api, store := newBedrockAPIWithCredStore(t)
	require.NoError(t, store.Set("T800_R3_KEY", "existing-key"))
	seedProviderConfig(t, api, map[string]any{
		"provider": "amazon-bedrock", "model": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"api_key_ref": "T800_R3_KEY", "region": "eu-central-1",
	})

	// The control-plane override is per-region-suffixed (mirrors
	// bedrockRuntimeBaseOverride's D1 pattern) so this fake server's request
	// PATH reveals which region ListInferenceProfiles actually targeted —
	// an httptest.Server's own host cannot BE a per-region AWS DNS name.
	var gotPath string
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"inferenceProfileSummaries": [
				{
					"inferenceProfileId": "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
					"models": [{"modelArn": "arn:aws:bedrock:eu-central-1::foundation-model/anthropic.claude-sonnet-4-5-20250929-v1:0"}]
				}
			]
		}`))
	}))
	defer controlPlane.Close()
	api.bedrockControlPlaneBaseOverride = controlPlane.URL

	// The PUT's OWN key-validation probe (D1, round 2) must also reach a
	// fake server rather than real AWS — this test is about the SEPARATE
	// live-profile-cache refresh trigger, not the save-time key check.
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bedrockConverseOKBody))
	}))
	defer runtimeSrv.Close()
	api.bedrockRuntimeBaseOverride = runtimeSrv.URL

	// KEY-ONLY PUT: api_key changes, region field is absent entirely.
	w := doPutProvider(t, api, "amazon-bedrock",
		`{"model":"anthropic.claude-sonnet-4-5-20250929-v1:0","api_key":"sk-new-key-r3"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NotEmpty(t, gotPath, "the control-plane call must have been made")
	assert.True(t, strings.HasPrefix(gotPath, "/eu-central-1"),
		"a key-only PUT on a row persisted with eu-central-1 must refresh THAT region, not the catalog's us-east-1 default; got path=%s", gotPath)

	persisted := persistedProviderRow(t, api, "amazon-bedrock")
	profiles, ok := persisted["bedrock_inference_profiles"].(map[string]any)
	require.True(t, ok, "bedrock_inference_profiles must be persisted: %#v", persisted)
	assert.Equal(t, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
		profiles["anthropic.claude-sonnet-4-5-20250929-v1:0"],
		"the persisted cache must hold EU profiles, never the catalog-default region's")
}

func TestRestProviders_PUT_RegionChange_LookupForbidden_DegradesSilently(t *testing.T) {
	api, store := newBedrockAPIWithCredStore(t)
	require.NoError(t, store.Set("T800_BEDROCK_LIVE_KEY_2", "test-bedrock-key"))
	seedProviderConfig(t, api, map[string]any{
		"provider": "amazon-bedrock", "model": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"api_key_ref": "T800_BEDROCK_LIVE_KEY_2", "region": "us-east-1",
	})

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"not authorized"}`))
	}))
	defer controlPlane.Close()
	api.bedrockControlPlaneBaseOverride = controlPlane.URL

	w := doPutProvider(t, api, "amazon-bedrock",
		`{"model":"anthropic.claude-sonnet-4-5-20250929-v1:0","region":"eu-central-1"}`)
	// A 403 from the control plane must never block the save itself.
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	persisted := persistedProviderRow(t, api, "amazon-bedrock")
	assert.Equal(t, "eu-central-1", persisted["region"], "the region change itself must still be saved")
	assert.NotContains(t, persisted, "bedrock_inference_profiles",
		"a failed lookup must not write a (possibly stale/empty) cache entry")
}
