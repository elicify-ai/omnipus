// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Run-time provider clients on a hosted tenant: the transport a turn uses is
// built by CreateProviderFromConfig, and it never consulted any SSRF checker.
// A row pointing at loopback (by IP literal, or by a NAME that resolves to
// loopback at connect time) let a turn reach the tenant machine's own
// services. These tests count requests on a loopback canary.

func withEditionForGuardTest(t *testing.T, e string) {
	t.Helper()
	t.Cleanup(config.SetEdition(e))
}

func startGuardCanary(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],` +
			`"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func chatOnce(t *testing.T, mc *config.ModelConfig) error {
	t.Helper()
	p, model, err := CreateProviderFromConfig(mc)
	require.NoError(t, err, "construction itself is not the guard; the dial is")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = p.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil, model, nil)
	return err
}

// TestRuntimeClient_Hosted_RefusesLoopbackEndpoints: on hosted, a custom row
// at loopback never reaches the canary, for both HTTP protocol families and
// for a host name (localhost) that only resolves to loopback at dial time.
func TestRuntimeClient_Hosted_RefusesLoopbackEndpoints(t *testing.T) {
	withEditionForGuardTest(t, config.EditionHosted)
	srv, hits := startGuardCanary(t)
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	cases := []struct{ name, protocol, base string }{
		{"openai-compatible by IP", "openai-compatible", srv.URL + "/v1"},
		{"openai-compatible by name", "openai-compatible", byName + "/v1"},
		{"anthropic by IP", "anthropic", srv.URL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GUARD_TEST_KEY", "sk-test")
			mc := &config.ModelConfig{
				Provider: "planted-custom", Model: "m1", Custom: true,
				Protocol: tc.protocol, APIBase: tc.base, APIKeyRef: "GUARD_TEST_KEY",
			}
			err := chatOnce(t, mc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "SSRF", "the refusal must say why")
		})
	}
	assert.Equal(t, int64(0), hits.Load(), "no run-time client may reach loopback on hosted")
}

// TestRuntimeClient_Core_StillReachesLocalEndpoint is the control: outside
// hosted, a local endpoint is a supported setup (Ollama, LM Studio) and the
// client reaches it.
func TestRuntimeClient_Core_StillReachesLocalEndpoint(t *testing.T) {
	withEditionForGuardTest(t, config.EditionCore)
	srv, hits := startGuardCanary(t)
	t.Setenv("GUARD_TEST_KEY", "sk-test")
	mc := &config.ModelConfig{
		Provider: "local-custom", Model: "m1", Custom: true,
		Protocol: "openai-compatible", APIBase: srv.URL + "/v1", APIKeyRef: "GUARD_TEST_KEY",
	}
	_ = chatOnce(t, mc)
	assert.Positive(t, hits.Load())
}

// plantedRefusalText is the person-facing refusal for a stored api_base on
// a catalog id that the hosted edition will not use. Written from the
// product voice rules: what happened, the cause seen, one thing to do.
const plantedRefusalText = "The request to OpenRouter was not sent: its saved address is not OpenRouter's own. " +
	"Save OpenRouter again in the provider settings to reset the address."

// TestResolveEndpoint_Hosted_RefusesPlantedCatalogBase: a catalog id carrying
// an api_base the operator's allow list does not name (a row planted before
// the fix, possibly at a PUBLIC host) is refused on hosted with an error the
// person can act on, so no client is built for it and neither the key nor a
// turn goes anywhere. Core keeps the documented override.
func TestResolveEndpoint_Hosted_RefusesPlantedCatalogBase(t *testing.T) {
	planted := &config.ModelConfig{Provider: "openrouter", Model: "x", APIBase: "https://attacker.example/v1"}

	withEditionForGuardTest(t, config.EditionHosted)
	_, err := ResolveEndpoint(planted)
	require.Error(t, err, "hosted: a planted catalog base is refused, not resolved")
	var pe *PlantedEndpointError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "openrouter", pe.ProviderID)
	assert.Equal(t, plantedRefusalText, err.Error())

	withEditionForGuardTest(t, config.EditionCore)
	ep, err := ResolveEndpoint(planted)
	require.NoError(t, err)
	assert.Equal(t, "https://attacker.example/v1", ep.API, "core: the documented override still applies")
}

// TestCreateProvider_Hosted_RefusesPlantedPublicCatalogBase: the run-time
// client builder refuses the same row, so a turn never gets a client aimed at
// the planted host. A public host cannot be served by a local canary, so the
// proof is that no provider is built at all.
func TestCreateProvider_Hosted_RefusesPlantedPublicCatalogBase(t *testing.T) {
	withEditionForGuardTest(t, config.EditionHosted)
	t.Setenv("GUARD_TEST_KEY", "sk-owner")
	planted := &config.ModelConfig{
		Provider: "openrouter", Model: "x", APIBase: "https://attacker.example/v1", APIKeyRef: "GUARD_TEST_KEY",
	}

	p, _, err := CreateProviderFromConfig(planted)

	require.Error(t, err)
	assert.Nil(t, p, "no client may exist for a planted catalog base")
	var pe *PlantedEndpointError
	assert.ErrorAs(t, err, &pe)
}

// TestCreateProvider_PlantedBaseRefusalScope: what the refusal does NOT touch.
// A custom row keeps its own base on hosted, an allow-listed override on a
// catalog id is accepted on hosted, and core accepts any override.
func TestCreateProvider_PlantedBaseRefusalScope(t *testing.T) {
	t.Setenv("GUARD_TEST_KEY", "sk-owner")
	t.Cleanup(func() { common.SetEndpointAllowlist(nil) })

	withEditionForGuardTest(t, config.EditionHosted)
	custom := &config.ModelConfig{
		Provider: "my-gateway", Model: "m1", Custom: true, Protocol: "openai-compatible",
		APIBase: "https://gateway.example/v1", APIKeyRef: "GUARD_TEST_KEY",
	}
	ep, err := ResolveEndpoint(custom)
	require.NoError(t, err, "hosted: a custom row is not a catalog id")
	assert.Equal(t, "https://gateway.example/v1", ep.API)
	_, _, err = CreateProviderFromConfig(custom)
	require.NoError(t, err)

	common.SetEndpointAllowlist([]string{"mock.internal"})
	allowed := &config.ModelConfig{
		Provider: "openrouter", Model: "x", APIBase: "http://mock.internal:9000/v1", APIKeyRef: "GUARD_TEST_KEY",
	}
	ep, err = ResolveEndpoint(allowed)
	require.NoError(t, err, "hosted: the operator allow list names this host")
	assert.Equal(t, "http://mock.internal:9000/v1", ep.API)
	common.SetEndpointAllowlist(nil)

	withEditionForGuardTest(t, config.EditionCore)
	planted := &config.ModelConfig{
		Provider: "openrouter", Model: "x", APIBase: "https://attacker.example/v1", APIKeyRef: "GUARD_TEST_KEY",
	}
	_, _, err = CreateProviderFromConfig(planted)
	require.NoError(t, err, "core: an override on a catalog id is a supported setup")
}

// TestRuntimeClient_Hosted_AnthropicStreamRefusesLoopback: the anthropic
// provider streams through its own HTTP client, separate from the one Chat
// uses. On hosted that client must be guarded too.
func TestRuntimeClient_Hosted_AnthropicStreamRefusesLoopback(t *testing.T) {
	withEditionForGuardTest(t, config.EditionHosted)
	srv, hits := startGuardCanary(t)
	t.Setenv("GUARD_TEST_KEY", "sk-test")
	mc := &config.ModelConfig{
		Provider: "planted-custom", Model: "m1", Custom: true,
		Protocol: "anthropic", APIBase: srv.URL, APIKeyRef: "GUARD_TEST_KEY",
	}
	p, model, err := CreateProviderFromConfig(mc)
	require.NoError(t, err)
	sp, ok := p.(StreamingProvider)
	require.True(t, ok, "the anthropic provider streams")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = sp.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, model, nil, func(string) {}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSRF", "the refusal must say why")
	assert.Equal(t, int64(0), hits.Load(), "the stream client may not reach loopback on hosted")
}
