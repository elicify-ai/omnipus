// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_probe_bedrock_region_test.go — orchestrator review round 2, D1.
//
// Observed against a real gateway via Playwright (onboarding step 4, Amazon
// Bedrock, AWS region eu-central-1, dummy key, model
// anthropic.claude-opus-4-6-v1): POST /api/v1/onboarding/probe-provider sent
// exactly {"id":"amazon-bedrock","auth":"api_key","api_key":"...",
// "model":"anthropic.claude-opus-4-6-v1"} — no region at all — so the key
// check ran against us-east-1, never the region the operator picked. By
// contrast POST /onboarding/complete already sent provider.region correctly
// (rest_onboarding_bedrock_region_test.go). Both probe paths — the
// onboarding probe AND the PUT-triggered save-time key check — shared the
// same gap: neither ever read a region at all when resolving the base URL
// or the model id to probe.
//
// These tests use the bedrockRuntimeBaseOverride test seam (rest.go) so a
// local httptest.Server can observe which region a probe resolved to via
// the request PATH, since an httptest server's own host cannot be a
// per-region AWS DNS name the way bedrock-runtime.<region>.amazonaws.com is.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// bedrockConverseOKBody is a minimal successful Converse response.
const bedrockConverseOKBody = `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`

// TestHandleOnboardingProbeProvider_Bedrock_HonoursRequestRegion is the D1
// onboarding-probe regression: a request that names an AWS region must
// probe THAT region's endpoint, with the model id carrying its
// cross-region inference profile group prefix — and must still echo the
// operator's OWN model id on probed_model, never the AWS-facing rewritten
// one (onboarding.tsx's canFinish gate compares probed_model to the
// operator's raw pick verbatim).
func TestHandleOnboardingProbeProvider_Bedrock_HonoursRequestRegion(t *testing.T) {
	api := newOnboardingTestAPI(t, t.TempDir(), nil)

	var gotPath string
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bedrockConverseOKBody))
	}))
	defer runtimeSrv.Close()
	api.bedrockRuntimeBaseOverride = runtimeSrv.URL

	body := `{"id":"amazon-bedrock","auth":"api_key","api_key":"sk-test",` +
		`"model":"anthropic.claude-opus-4-6-v1","region":"eu-central-1"}`
	w := postProbe(t, api, body)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var result gen.ProbeProviderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.True(t, result.Success, "body=%s", w.Body.String())
	require.NotNil(t, result.ProbedModel)
	assert.Equal(t, "anthropic.claude-opus-4-6-v1", *result.ProbedModel,
		"probed_model must echo the operator's own pick, never the AWS-facing group-prefixed id")

	require.NotEmpty(t, gotPath, "the fake runtime server was never hit at all")
	assert.True(t, strings.HasPrefix(gotPath, "/eu-central-1/"),
		"the probe must hit the eu-central-1 endpoint the operator picked, not the catalog's us-east-1 default; got path=%s", gotPath)
	assert.Contains(t, gotPath, "/model/eu.anthropic.claude-opus-4-6-v1/converse",
		"the model actually sent to AWS must carry the eu. cross-region inference profile prefix; got path=%s", gotPath)
}

// TestHandleOnboardingProbeProvider_Bedrock_NoRegion_DefaultsToCatalog proves
// the fix is additive: omitting region still probes the catalog's own
// default region, exactly as before this fix.
func TestHandleOnboardingProbeProvider_Bedrock_NoRegion_DefaultsToCatalog(t *testing.T) {
	api := newOnboardingTestAPI(t, t.TempDir(), nil)

	var gotPath string
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bedrockConverseOKBody))
	}))
	defer runtimeSrv.Close()
	api.bedrockRuntimeBaseOverride = runtimeSrv.URL

	body := `{"id":"amazon-bedrock","auth":"api_key","api_key":"sk-test",` +
		`"model":"anthropic.claude-opus-4-6-v1"}`
	w := postProbe(t, api, body)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NotEmpty(t, gotPath)
	assert.True(t, strings.HasPrefix(gotPath, "/us-east-1/"),
		"an omitted region must still fall back to the catalog's own default region; got path=%s", gotPath)
	// us-east-1's group is "us", and this model's catalog inference_profiles
	// includes "us" (embedded snapshot) — so the bare id is STILL rewritten,
	// just with the "us." prefix instead of "eu.".
	assert.Contains(t, gotPath, "/model/us.anthropic.claude-opus-4-6-v1/converse", "got path=%s", gotPath)
}

// TestProviderPutValidateKey_Bedrock_HonoursPersistedRegion is D1's OTHER
// half — the coordinator's explicit ask to also check "the Settings →
// Providers 'test key' / probe path": there is no separate test-key
// endpoint; Settings validates the key as a side effect of PUT
// /providers/{id} whenever api_key is present (providerPutValidateKey).
// This PUT changes ONLY the key — no region field — so the probe must fall
// back to the row's own PERSISTED region (eu-central-1, seeded below), not
// the catalog's us-east-1 default.
func TestProviderPutValidateKey_Bedrock_HonoursPersistedRegion(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = bedrockCatalog(t)
	seedProviderConfig(t, api, map[string]any{
		"provider": "amazon-bedrock", "model": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"api_key_ref": "T800_D1_PUT_SEED", "region": "eu-central-1",
	})

	var gotPath string
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bedrockConverseOKBody))
	}))
	defer runtimeSrv.Close()
	api.bedrockRuntimeBaseOverride = runtimeSrv.URL

	w := doPutProvider(t, api, "amazon-bedrock",
		`{"model":"anthropic.claude-sonnet-4-5-20250929-v1:0","api_key":"sk-new-key"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NotEmpty(t, gotPath, "the save-time key validation never reached the fake runtime server")
	assert.True(t, strings.HasPrefix(gotPath, "/eu-central-1/"),
		"a region-only-persisted row's key check must still probe its OWN region, not the catalog default; got path=%s", gotPath)
	assert.Contains(t, gotPath, "/model/eu.anthropic.claude-sonnet-4-5-20250929-v1:0/converse", "got path=%s", gotPath)
}
