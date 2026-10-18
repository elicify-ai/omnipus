package systools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T3 / D1-A endpoint exception / FR-064/065: actual endpoint-only card ->
// actual Submit -> configure_provider, not a forged binding or catalog URL
// masquerading as an arbitrary endpoint journey. This is baseline coverage,
// not a claim that current validation has a vulnerability.
func TestWU4Selection_EndpointOnlyCardSelectsSameNormalizedDestination(t *testing.T) {
	for _, endpoint := range []string{
		selectionCustomEndpoint,
		"https://CUSTOM.Selection.Invalid/tenant/v1",
		"https://custom.selection.invalid/tenant/v1/",
	} {
		t.Run(endpoint, func(t *testing.T) {
			want := credentials.CardRecipient{Kind: "provider_key", Endpoint: selectionCustomEndpoint, Field: "api_key"}
			f, ref, oldRef := selectionIssueEndpointCard(t, "https://CUSTOM.Selection.Invalid/tenant/v1/", want, "openai", "openai-compatible")
			before := f.snapshot(t)
			selectionObserveEndpointProbe(t, f, selectionCustomEndpoint, "openai-compatible")
			token := wu3SharedRow(t, f.d, "provider", "openai")["revision"]
			require.NotEmpty(t, token)
			require.NotEqual(t, "absent", token)
			result := systools.NewProviderConfigureTool(f.d).Execute(context.Background(), map[string]any{
				"name": "openai", "api_base": endpoint, "api_key_ref": ref, "expected_revision": token,
			})
			wu3Success(t, result)
			body := wu3Outcome(t, result, "working")
			assert.Equal(t, ref, body["api_key_ref"])
			assert.Equal(t, selectionCustomEndpoint, body["api_base"])
			assert.Equal(t, "openai-compatible", body["protocol"])
			assert.Equal(t, ref, f.cfg.Providers[0].APIKeyRef)
			assert.Equal(t, selectionCustomEndpoint, f.cfg.Providers[0].APIBase)
			after := f.snapshot(t)
			assert.Equal(t, before.credentials, after.credentials, "selection cannot rewrite card values, bindings or receipt")
			assert.Equal(t, 1, f.saves)
			assert.Equal(t, 1, f.reloads)
			assert.Equal(t, 1, f.listings)
			assert.Zero(t, f.reconciles)
			var disk config.Config
			require.NoError(t, json.Unmarshal(after.config, &disk))
			require.Len(t, disk.Providers, 1)
			assert.Equal(t, ref, disk.Providers[0].APIKeyRef)
			assert.Equal(t, selectionCustomEndpoint, disk.Providers[0].APIBase)
			oldValue, err := f.d.CredStore.Get(oldRef)
			require.NoError(t, err)
			assert.Equal(t, "FAKE-SELECTION-ENDPOINT-OLD-VALUE", oldValue)
			assert.NotContains(t, result.ForLLM, selectionEndpointValue)
		})
	}
}

func TestWU4Selection_EndpointOnlyCardRefusesChangedHostSchemeAndPathAtomically(t *testing.T) {
	for _, endpoint := range []string{
		"https://another.selection.invalid/tenant/v1",
		"http://custom.selection.invalid/tenant/v1",
		"https://custom.selection.invalid/another/v1",
	} {
		t.Run(endpoint, func(t *testing.T) {
			want := credentials.CardRecipient{Kind: "provider_key", Endpoint: selectionCustomEndpoint, Field: "api_key"}
			f, ref, oldRef := selectionIssueEndpointCard(t, selectionCustomEndpoint, want, "openai", "openai-compatible")
			before := f.snapshot(t)
			token := wu3SharedRow(t, f.d, "provider", "openai")["revision"]
			result := systools.NewProviderConfigureTool(f.d).Execute(context.Background(), map[string]any{
				"name": "openai", "api_base": endpoint, "api_key_ref": ref, "expected_revision": token,
			})
			f.unchanged(t, before)
			wu3Error(t, result, "INVALID_REFERENCE")
			wu4bRefusal(t, result, "exact recipient")
			assert.Equal(t, oldRef, f.cfg.Providers[0].APIKeyRef)
			assert.Equal(t, selectionCustomEndpoint, f.cfg.Providers[0].APIBase)
			assert.Zero(t, f.saves)
			assert.Zero(t, f.reloads)
			assert.Zero(t, f.listings)
			assert.Zero(t, f.reconciles)
		})
	}
}

func TestWU4Selection_CatalogEndpointCardKeepsProviderAndProtocolIdentityStrict(t *testing.T) {
	for _, dimension := range []string{"matching", "provider", "protocol"} {
		t.Run(dimension, func(t *testing.T) {
			const endpoint = "https://api.deepseek.com"
			want := credentials.CardRecipient{Kind: "provider_key", Provider: "deepseek", Protocol: "openai-compatible", Endpoint: endpoint, Field: "api_key"}
			provider, protocol := "deepseek", "openai-compatible"
			if dimension == "provider" {
				provider = "openai"
			}
			if dimension == "protocol" {
				protocol = "anthropic" // legal DeepSeek secondary protocol, not a malformed config row
			}
			f, ref, oldRef := selectionIssueEndpointCard(t, endpoint, want, provider, protocol)
			effective, err := providers.ResolveEndpoint(f.cfg.Providers[0])
			require.NoError(t, err, "negative fixture must reach recipient comparison")
			require.Equal(t, endpoint, effective.API)
			require.Equal(t, protocol, string(effective.Protocol))
			before := f.snapshot(t)
			selectionObserveEndpointProbe(t, f, endpoint, protocol)
			token := wu3SharedRow(t, f.d, "provider", provider)["revision"]
			result := systools.NewProviderConfigureTool(f.d).Execute(context.Background(), map[string]any{
				"name": provider, "api_base": endpoint, "api_key_ref": ref, "expected_revision": token,
			})
			if dimension == "matching" {
				wu3Success(t, result)
				wu3Outcome(t, result, "working")
				assert.Equal(t, ref, f.cfg.Providers[0].APIKeyRef)
				assert.Equal(t, 1, f.saves)
				assert.Equal(t, 1, f.reloads)
				assert.Equal(t, 1, f.listings)
				assert.Equal(t, before.credentials, f.snapshot(t).credentials)
			} else {
				f.unchanged(t, before)
				wu3Error(t, result, "INVALID_REFERENCE")
				wu4bRefusal(t, result, "exact recipient")
				assert.Equal(t, oldRef, f.cfg.Providers[0].APIKeyRef)
				assert.Zero(t, f.saves)
				assert.Zero(t, f.reloads)
				assert.Zero(t, f.listings)
			}
		})
	}
}

func selectionObserveEndpointProbe(t *testing.T, f *wu4bFixture, wantEndpoint, wantProtocol string) {
	t.Helper()
	f.d.ListProviderModels = func(ctx context.Context, protocol catalog.Protocol, endpoint, value string, _ providers.URLChecker) ([]string, error) {
		f.unlocked(t)
		f.listings++
		require.NoError(t, ctx.Err())
		assert.Equal(t, wantProtocol, string(protocol))
		assert.Equal(t, wantEndpoint, endpoint)
		assert.Equal(t, selectionEndpointValue, value)
		return []string{"endpoint-fixture-model"}, nil
	}
}
