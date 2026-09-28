package credentials

// search_provider_inject_walk_test.go — spec test 29 (Exa's ref is in
// nonChannelRefsFor, asserted by WALKING the provider catalogue, not by
// naming ids) and spec test 48's "injectable" half. Oracle:
// docs/internal/specs/web-search-provider-model-spec.md (FR-035, AC-13,
// AC-20).

import (
	"slices"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestCatalogueWalk_EveryKeyedProviderRefIsInjectable walks the catalogue:
// every keyed provider's credential ref must be returned by the shared
// non-channel enumeration, so InjectFromConfig publishes it into the process
// environment (AC-13). Exa is covered by the walk without being named —
// the exact failure 07a75c104 fixed for the other five providers (the
// configured-but-never-injected Tavily defect).
func TestCatalogueWalk_EveryKeyedProviderRefIsInjectable(t *testing.T) {
	cfg := config.DefaultConfig()
	refs := map[string]bool{}
	for _, def := range config.SearchProviderCatalogue {
		if !def.Keyed {
			continue
		}
		ref := def.CredRef
		if ref == "" {
			t.Fatalf("keyed provider %q has no credential ref", def.ID)
		}
		refs[ref] = true
	}
	if len(refs) == 0 {
		t.Fatal("walk found no keyed providers — the catalogue is empty")
	}

	// Point every keyed provider's config ref at its catalogue ref name, so
	// the enumeration must surface each of them.
	web := &cfg.Tools.Web
	for _, def := range config.SearchProviderCatalogue {
		if !def.Keyed {
			continue
		}
		// The catalogue CredRef is the shipped vault entry name; the config
		// api_key_ref fields are set through the Settings PUT to the same
		// name. Setting them here mirrors a fully configured install.
		setWebSearchAPIKeyRefForTest(web, def.ID, def.CredRef)
	}

	got := map[string]bool{}
	for _, cr := range nonChannelRefsFor(cfg) {
		got[cr.ref] = true
	}
	for ref := range refs {
		if !got[ref] {
			t.Errorf("keyed provider ref %q missing from nonChannelRefsFor — not injectable (AC-13/AC-20)", ref)
		}
	}
}

// TestCatalogueWalk_AppendedProviderIsInjectable is spec test 48's
// injectable half: a provider appended to the catalogue — and nothing else —
// is covered by the shared enumeration with no hand-list edit.
func TestCatalogueWalk_AppendedProviderIsInjectable(t *testing.T) {
	saved := config.SearchProviderCatalogue
	config.SearchProviderCatalogue = append(append([]config.SearchProviderDef{}, saved...), config.SearchProviderDef{
		ID:        "testprov",
		Section:   "testprov",
		Keyed:     true,
		APIKeyRef: func(*config.WebToolsConfig) string { return "TESTPROV_API_KEY" },
	})
	t.Cleanup(func() { config.SearchProviderCatalogue = saved })

	cfg := config.DefaultConfig()
	got := make([]string, 0, len(config.SearchProviderCatalogue))
	for _, cr := range nonChannelRefsFor(cfg) {
		got = append(got, cr.ref)
	}
	if !slices.Contains(got, "TESTPROV_API_KEY") {
		t.Fatalf("appended provider's ref not in nonChannelRefsFor (got %v) — the enumeration is not derived from the catalogue", got)
	}
}

// setWebSearchAPIKeyRefForTest sets the api_key_ref field of the keyed
// provider id on the web tools config. Fixture code — the per-provider
// fields are the substrate, not the enumeration under test.
func setWebSearchAPIKeyRefForTest(web *config.WebToolsConfig, id, ref string) {
	switch id {
	case config.SearchProviderPerplexity:
		web.Perplexity.APIKeyRef = ref
	case config.SearchProviderBrave:
		web.Brave.APIKeyRef = ref
	case config.SearchProviderTavily:
		web.Tavily.APIKeyRef = ref
	case config.SearchProviderGLM:
		web.GLMSearch.APIKeyRef = ref
	case config.SearchProviderBaidu:
		web.BaiduSearch.APIKeyRef = ref
	case config.SearchProviderExa:
		web.Exa.APIKeyRef = ref
	}
}
