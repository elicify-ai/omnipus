// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Issue #800 (Bedrock region contract): onboarding's api_key variant
// (OnboardingProviderApiKey.region) must decode into the same
// onboardingProviderChoice.Region field that mutateConfigForCompletion
// persists as the provider row's `region` — mirroring the existing
// `endpoint` field's decode-then-persist path exactly.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeOnboardingCompleteBody_DecodesRegion(t *testing.T) {
	body := `{"provider":{"auth_method":"api_key","id":"amazon-bedrock","api_key":"sk-test",` +
		`"model":"anthropic.claude-sonnet-4-5-20250929-v1:0","region":"eu-central-1"},` +
		`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	w := httptest.NewRecorder()

	_, choice, ok := decodeOnboardingCompleteBody(w, r, false)
	require.True(t, ok, "body=%s", w.Body.String())
	assert.Equal(t, "amazon-bedrock", choice.ID)
	assert.Equal(t, "eu-central-1", choice.Region, "the region field must decode into onboardingProviderChoice.Region")
}

func TestDecodeOnboardingCompleteBody_RegionOmitted_LeavesItEmpty(t *testing.T) {
	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},` +
		`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	w := httptest.NewRecorder()

	_, choice, ok := decodeOnboardingCompleteBody(w, r, false)
	require.True(t, ok, "body=%s", w.Body.String())
	assert.Equal(t, "", choice.Region, "an omitted region field must decode as empty, never a stray default")
}
