package gateway

// rest_integrations_catalogue_test.go — spec test 48's "listable" half and
// the contract-enum sync test (FR-035: the contract's id enum derives from
// the single searchProviderCatalogue, so adding a provider to the catalogue
// and nothing else must be listable, and an enum that drifts from the
// catalogue fails here rather than shipping).
//
// Oracle: docs/internal/specs/web-search-provider-model-spec.md (FR-035,
// AC-20); contracts/components/schemas/IntegrationProvider.yaml.

import (
	"slices"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestIntegrationProviderEnumMatchesCatalogue asserts the generated
// IntegrationProviderId enum equals the single catalogue's id set plus the
// voice ids (voice rows share the schema; they are outside the web-search
// catalogue). This is the FR-035 "including the contract's id enum" clause:
// drift between contracts/ and the catalogue fails CI instead of shipping.
func TestIntegrationProviderEnumMatchesCatalogue(t *testing.T) {
	enumSet := map[gen.IntegrationProviderId]bool{
		gen.IntegrationProviderIdBrave:      true,
		gen.IntegrationProviderIdTavily:     true,
		gen.IntegrationProviderIdPerplexity: true,
		gen.IntegrationProviderIdDuckduckgo: true,
		gen.IntegrationProviderIdSearxng:    true,
		gen.IntegrationProviderIdGlm:        true,
		gen.IntegrationProviderIdBaidu:      true,
		gen.IntegrationProviderIdExa:        true,
		gen.IntegrationProviderIdElevenlabs: true,
		gen.IntegrationProviderIdGroq:       true,
		gen.IntegrationProviderIdAudioModel: true,
	}

	expected := map[string]bool{}
	for _, def := range config.SearchProviderCatalogue {
		expected[def.ID] = true
	}
	for _, d := range voiceIntegrationDefs {
		expected[d.id] = true
	}

	for id := range expected {
		if !enumSet[gen.IntegrationProviderId(id)] {
			t.Errorf("catalogue/voice id %q has no enum constant — regenerate contracts (make gen-contracts)", id)
		}
	}
	if len(expected) != len(enumSet) {
		t.Fatalf("enum has %d values, catalogue+voice has %d — drift between the contract enum and the catalogue",
			len(enumSet), len(expected))
	}
}

// TestCatalogueWalk_AppendedProviderIsListable is spec test 48's listable
// half: a provider appended to the catalogue — and nothing else — appears in
// the Integrations list with its own row, and its config section is
// storable through the save path's ref mapping.
func TestCatalogueWalk_AppendedProviderIsListable(t *testing.T) {
	saved := config.SearchProviderCatalogue
	grown := make([]config.SearchProviderDef, 0, len(saved)+1)
	grown = append(grown, saved...)
	grown = append(grown, config.SearchProviderDef{
		ID:          "testprov",
		Section:     "testprov",
		DisplayName: "Test Provider",
		Keyed:       true,
		CredRef:     "TESTPROV_API_KEY",
		Enabled:     func(w *config.WebToolsConfig) bool { return w.Exa.Enabled },
		SetEnabled:  func(w *config.WebToolsConfig, on bool) { w.Exa.Enabled = on },
		APIKeyRef:   func(*config.WebToolsConfig) string { return "TESTPROV_API_KEY" },
		APIKey:      func(w *config.WebToolsConfig) string { return w.Exa.APIKey() },
	})
	config.SearchProviderCatalogue = grown
	t.Cleanup(func() { config.SearchProviderCatalogue = saved })

	api, user, _ := newRolesTestAPI(t)
	reauthFor(t, api, user)
	resp := getIntegrationList(t, api, user)

	var row *gen.IntegrationProvider
	for i := range resp.Search {
		if resp.Search[i].Id == "testprov" {
			row = &resp.Search[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("appended provider has no search row in the list — the catalogue is not the list's source (rows: %d)", len(resp.Search))
	}
	if row.DisplayName != "Test Provider" || !row.RequiresKey {
		t.Fatalf("row = %+v, want display name Test Provider and requires_key true", *row)
	}

	// Storable: the save path's id→section mapping derives from the same
	// catalogue, so the appended provider's api_key_ref is writable.
	section, ok := searchRefSectionByID("testprov")
	if !ok || section != "testprov" {
		t.Fatalf("searchRefSectionByID(testprov) = %q, %v — the save path cannot store the appended provider", section, ok)
	}
}
