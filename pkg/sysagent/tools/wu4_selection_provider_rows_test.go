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

// T1 is a coverage addition, not a demonstrated production defect. FR-064/065
// applies to every affected provider row, even though the first/representative
// row matches and the caller supplies the actual current revision token.
func TestWU4Selection_ProviderRejectsLaterRowRecipientMismatchAtCurrentRevision(t *testing.T) {
	for _, dimension := range []string{"endpoint", "protocol"} {
		t.Run(dimension, func(t *testing.T) {
			f, candidate, oldRefs := selectionProviderRows(t, dimension)
			before := f.snapshot(t)
			token := wu3SharedRow(t, f.d, "provider", "deepseek")["revision"]
			require.NotEmpty(t, token)
			require.NotEqual(t, "absent", token)
			tool := systools.NewProviderConfigureTool(f.d)
			result := tool.Execute(context.Background(), map[string]any{
				"name": "deepseek", "api_key_ref": candidate, "expected_revision": token,
			})
			f.unchanged(t, before)
			wu3Error(t, result, "INVALID_REFERENCE")
			wu4bRefusal(t, result, "exact recipient")
			assert.Equal(t, oldRefs[0], f.cfg.Providers[0].APIKeyRef, "matching first row must not be partially wired")
			assert.Equal(t, oldRefs[1], f.cfg.Providers[1].APIKeyRef, "mismatching later row must retain its own original selection")
			assert.Zero(t, f.saves)
			assert.Zero(t, f.reloads)
			assert.Zero(t, f.listings, "recipient refusal happens before any provider probe")
			assert.Zero(t, f.reconciles)
			assert.Equal(t, token, wu3SharedRow(t, f.d, "provider", "deepseek")["revision"], "not a stale-token rejection")
			assert.NotContains(t, result.ForLLM, "FAKE-SELECTION-PROVIDER-CANDIDATE")
		})
	}
}

func TestWU4Selection_ProviderHomogeneousTwoRowsSelectAndProbeAtCurrentRevision(t *testing.T) {
	f, candidate, oldRefs := selectionProviderRows(t, "homogeneous")
	f.d.ListProviderModels = func(ctx context.Context, protocol catalog.Protocol, endpoint, value string, _ providers.URLChecker) ([]string, error) {
		f.unlocked(t)
		f.listings++
		require.NoError(t, ctx.Err())
		assert.Equal(t, "openai-compatible", string(protocol))
		assert.Equal(t, "https://rows-a.selection.invalid/v1", endpoint, "the saved A destination, not a catalog fallback")
		assert.Equal(t, "FAKE-SELECTION-PROVIDER-CANDIDATE", value)
		return []string{"selection-model-a", "selection-model-b"}, nil
	}
	before := f.snapshot(t)
	token := wu3SharedRow(t, f.d, "provider", "deepseek")["revision"]
	result := systools.NewProviderConfigureTool(f.d).Execute(context.Background(), map[string]any{
		"name": "deepseek", "api_key_ref": candidate, "expected_revision": token,
	})
	wu3Success(t, result)
	body := wu3Outcome(t, result, "working")
	assert.Equal(t, candidate, body["api_key_ref"])
	require.Len(t, f.cfg.Providers, 2)
	for _, row := range f.cfg.Providers {
		assert.Equal(t, candidate, row.APIKeyRef)
		assert.Equal(t, "https://rows-a.selection.invalid/v1", row.APIBase)
		assert.Equal(t, "openai-compatible", row.Protocol)
	}
	after := f.snapshot(t)
	assert.Equal(t, before.credentials, after.credentials, "selection never republishes values, metadata or receipts")
	assert.Equal(t, 1, f.saves)
	assert.Equal(t, 1, f.reloads)
	assert.Equal(t, 1, f.listings)
	assert.Zero(t, f.reconciles)
	var disk config.Config
	require.NoError(t, json.Unmarshal(after.config, &disk))
	require.Len(t, disk.Providers, 2)
	assert.Equal(t, candidate, disk.Providers[0].APIKeyRef)
	assert.Equal(t, candidate, disk.Providers[1].APIKeyRef)
	for i, ref := range oldRefs {
		value, err := f.d.CredStore.Get(ref)
		require.NoError(t, err)
		assert.Equal(t, []string{"FAKE-SELECTION-OLD-A", "FAKE-SELECTION-OLD-B"}[i], value)
	}
	assert.NotEqual(t, token, body["revision"])
	assert.NotContains(t, result.ForLLM, "FAKE-SELECTION-PROVIDER-CANDIDATE")
}

func selectionProviderRows(t *testing.T, dimension string) (*wu4bFixture, string, []string) {
	t.Helper()
	f := newWU4BFixture(t)
	// Literal recipient descriptors are the spec-derived oracle. Do not use
	// the production recipient constructor/comparator to derive the negatives.
	// DeepSeek has both catalog-supported protocols, so the protocol-negative
	// row remains valid and reaches recipient comparison rather than failing
	// earlier during endpoint resolution.
	a := credentials.CardRecipient{Kind: "provider_key", Provider: "deepseek", Endpoint: "https://rows-a.selection.invalid/v1", Protocol: "openai-compatible", Field: "api_key"}
	b := a
	switch dimension {
	case "endpoint":
		b.Endpoint = "https://rows-b.selection.invalid/v1"
	case "protocol":
		b.Protocol = "anthropic"
	case "homogeneous":
	default:
		t.Fatalf("unknown provider-row case %q", dimension)
	}
	oldA := f.bound(t, a, "FAKE-SELECTION-OLD-A")
	oldB := f.bound(t, b, "FAKE-SELECTION-OLD-B")
	candidate := f.bound(t, a, "FAKE-SELECTION-PROVIDER-CANDIDATE")
	f.cfg.Providers = []*config.ModelConfig{
		{Provider: "deepseek", Model: "selection-model-a", APIBase: a.Endpoint, Protocol: a.Protocol, APIKeyRef: oldA},
		{Provider: "deepseek", Model: "selection-model-b", APIBase: b.Endpoint, Protocol: b.Protocol, APIKeyRef: oldB},
	}
	for i, recipient := range []credentials.CardRecipient{a, b} {
		effective, err := providers.ResolveEndpoint(f.cfg.Providers[i])
		require.NoError(t, err, "fixture row %d must be a legal runtime destination", i)
		require.Equal(t, recipient.Endpoint, effective.API)
		require.Equal(t, recipient.Protocol, string(effective.Protocol))
	}
	f.persist(t)
	return f, candidate, []string{oldA, oldB}
}
