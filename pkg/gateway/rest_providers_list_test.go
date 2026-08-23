// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-068 T068-04 — GET /api/v1/providers returns configured rows only.
//
// The seeded `cfg.Providers` templates (pkg/config/defaults.go) used to be
// echoed as permanent `status: disconnected` rows, one per unconfigured
// catalog entry. Resolution #16 removes them: a row the operator never
// created is not theirs to manage. Spec: adr-068-providers-ux-spec.md
// FR-011a, FR-043, TDD row 15a, SC-014 (second clause).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// seedTemplateProviders installs the real fresh-install seed templates from
// config.DefaultConfig() into the in-memory config, then appends the given
// operator-configured rows. Templates carry no `provider` identity and no
// credential ref — exactly the shape pkg/config/defaults.go ships.
func seedTemplateProviders(t *testing.T, api *restAPI, configured ...*config.ModelConfig) {
	t.Helper()
	templates := config.DefaultConfig().Providers
	require.GreaterOrEqual(t, len(templates), 10,
		"the fresh-install seed must ship at least ten templates for SC-014's second clause to mean anything")
	for _, tpl := range templates {
		require.Empty(t, tpl.Provider, "seed template %q must carry no provider identity", tpl.ModelName)
		require.Empty(t, tpl.APIKeyRef, "seed template %q must carry no credential ref", tpl.ModelName)
	}
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = cfg.Providers[:0]
	cfg.Providers = append(cfg.Providers, templates...)
	cfg.Providers = append(cfg.Providers, configured...)
}

// TestListProviders_ConfiguredOnly — TDD row 15a / SC-014 second clause.
func TestListProviders_ConfiguredOnly(t *testing.T) {
	t.Run("ten seed templates + one configured → exactly one row", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		// A configured, connected provider with NO known upstream base keeps the
		// GET hermetic (no live /models fetch). The spec scenario names
		// openrouter; the id is immaterial to the template-row assertion.
		const ref = "T068_04_MYGW_API_KEY"
		t.Setenv(ref, "sk-configured")
		seedTemplateProviders(t, api, &config.ModelConfig{
			ModelName: "mygw",
			Provider:  "mygw",
			Model:     "mygw/llama",
			APIKeyRef: ref,
			Models:    []string{"mygw/llama"},
		})

		provs := getProviders(t, api)
		require.Len(t, provs, 1, "only the configured provider may be echoed; got %+v", provs)
		assert.Equal(t, "mygw", provs[0].Id)
		assert.Equal(t, gen.ProviderStatusConnected, provs[0].Status)
		for _, p := range provs {
			assert.NotEqual(t, gen.ProviderStatusDisconnected, p.Status,
				"no template row with status: disconnected may exist for an unconfigured entry")
		}
	})

	t.Run("templates alone → empty list, never a placeholder row", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seedTemplateProviders(t, api)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		api.HandleProviders(w, r)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		// Provider.yaml requires an array: the body must be `[]`, not `null`
		// and not the retired `{"id":"default"}` filler row.
		assert.Equal(t, "[]", trimJSON(w.Body.Bytes()), "body=%s", w.Body.String())
	})

	t.Run("configured row without a key is still a row (disconnected), template is not", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			ModelName: "mygw", Provider: "mygw", Model: "mygw/llama",
			Models: []string{"mygw/llama"},
		})
		provs := getProviders(t, api)
		require.Len(t, provs, 1, "got %+v", provs)
		assert.Equal(t, "mygw", provs[0].Id)
		assert.Equal(t, gen.ProviderStatusDisconnected, provs[0].Status)
		assert.Equal(t, []string{"mygw/llama"}, provs[0].Models)
	})

	t.Run("no models from any source → models is [] not the template alias", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			ModelName: "mygw-alias", Provider: "mygw", Model: "mygw/llama",
		})
		provs := getProviders(t, api)
		require.Len(t, provs, 1, "got %+v", provs)
		require.NotNil(t, provs[0].Models, "Provider.yaml requires models:array")
		assert.Empty(t, provs[0].Models,
			"the 'final fallback: configured default model alias' fill is removed (resolution #16)")
	})

	t.Run("configured row the catalog does not know → unknown-provider (FR-043)", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		fixture, err := os.ReadFile("../providers/catalog/testdata/providers_catalog_2.0.0_fixture.json")
		require.NoError(t, err)
		cat, err := catalog.NewCatalog(fixture)
		require.NoError(t, err)
		api.providerCatalog = cat

		const ref = "T068_04_NOPE_API_KEY"
		t.Setenv(ref, "sk-whatever")
		seedTemplateProviders(t, api,
			&config.ModelConfig{ModelName: "nope", Provider: "nope", Model: "nope/x", APIKeyRef: ref,
				Models: []string{"nope/x"}},
			&config.ModelConfig{ModelName: "mygw", Provider: "zai", Model: "zai/glm-5.2",
				Models: []string{"glm-5.2"}},
		)
		provs := getProviders(t, api)
		require.Len(t, provs, 2, "got %+v", provs)
		byID := map[string]gen.Provider{}
		for _, p := range provs {
			byID[p.Id] = p
		}
		nope := byID["nope"]
		assert.Equal(t, gen.ProviderStatusUnknownProvider, nope.Status)
		require.NotNil(t, nope.Error)
		assert.Equal(t, `unknown provider "nope"`, *nope.Error,
			"generic text parameterised by the operator's own id, nothing else")
		require.NotNil(t, nope.Models)
		assert.Empty(t, nope.Models, "S67 Q4: models is [] for an unknown-provider row")
		assert.NotEqual(t, gen.ProviderStatusUnknownProvider, byID["zai"].Status,
			"a catalog id must not be classified unknown")
	})

	t.Run("DELETE on a template id is not found", func(t *testing.T) {
		// Until T068-09 lands the DELETE branch, the dispatcher has no DELETE
		// case and answers 405; T068-09 tightens this to 404. Either way a
		// template id is never a deletable row.
		api := newTestRestAPIWithHome(t)
		seedTemplateProviders(t, api)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/providers/groq", nil)
		api.HandleProviders(w, r)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, w.Code,
			"body=%s", w.Body.String())
	})
}

// trimJSON compacts a JSON body for exact comparison.
func trimJSON(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	out, _ := json.Marshal(v)
	return string(out)
}
