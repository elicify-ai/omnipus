package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// type-design T2: the provider token protects the row that list_providers
// describes and configure_provider writes to (the representative row). With
// two rows of different endpoints and references, reordering them changes
// that row, so the token must change and a write planned against the old
// order is refused as CONFLICT, writing nothing.
func TestWU3ProviderTokenCoversTheRepresentativeRow(t *testing.T) {
	d, cfg := wu3Deps(t)
	a := &config.ModelConfig{Provider: "openai", Model: "model-a", APIBase: "https://a.wu3.invalid/v1", APIKeyRef: "ref-a"}
	b := &config.ModelConfig{Provider: "openai", Model: "model-b", APIBase: "https://b.wu3.invalid/v1", APIKeyRef: "ref-b"}
	cfg.Providers = []*config.ModelConfig{a, b}
	ref := systools.IssueProviderReferenceForTest(t, d, "openai", "", "sk-wu3fixstaleplan000000000000000")
	first := wu3SharedRow(t, d, "provider", "openai")
	require.Equal(t, "https://a.wu3.invalid/v1", first["api_base"], "fixture: row A is described first")

	cfg.Providers = []*config.ModelConfig{b, a} // an operator reorders the rows
	second := wu3SharedRow(t, d, "provider", "openai")
	require.Equal(t, "https://b.wu3.invalid/v1", second["api_base"], "fixture: row B is described now")
	assert.NotEqual(t, first["revision"], second["revision"], "a different represented row is a different token")

	res := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{
		"name": "openai", "api_key_ref": ref, "expected_revision": first["revision"],
	})
	wu3Error(t, res, "CONFLICT")
	_, err := d.CredStore.Get("ref-b")
	assert.Error(t, err, "the stale plan wrote no key under the now-represented reference")

	// Control: reordering identical-endpoint model rows keeps the token.
	c := &config.ModelConfig{Provider: "openai", Model: "model-c", APIBase: "https://b.wu3.invalid/v1", APIKeyRef: "ref-b"}
	cfg.Providers = []*config.ModelConfig{b, c, a}
	same := wu3SharedRow(t, d, "provider", "openai")
	assert.Equal(t, second["revision"], same["revision"], "another model row of the same endpoint is not a provider change")
}
