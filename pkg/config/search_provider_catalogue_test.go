package config

// search_provider_catalogue_test.go — ADR-096 D15 / FR-035 / AC-20 / spec
// tests 29 + 48: the single searchProviderCatalogue is the one source of
// provider ids, sections, refs, keyed-ness and capabilities, and every
// consumer derives from it. The walk tests append a synthetic provider and
// must observe it storable/selectable with NO other edit — walking the
// catalogue, never naming ids (the shipped-content pin below is the one
// place ids may appear: it pins the catalogue itself).

import (
	"slices"
	"testing"
)

// TestSearchProviderCatalogue_ShippedContentPinned pins the shipped
// catalogue's content: the seven retained ADR-096 providers, their keyed/keyless
// and capability matrix, and the PRE-ADR legacy chain positions the roles
// migration mirrors after SearXNG's full removal (Exa was never in that chain).
func TestSearchProviderCatalogue_ShippedContentPinned(t *testing.T) {
	if got := len(SearchProviderCatalogue); got != 7 {
		t.Fatalf("catalogue has %d providers, want 7 after SearXNG removal", got)
	}
	ids := catalogueIDsForTest()
	wantOrder := []string{
		SearchProviderPerplexity, SearchProviderBrave,
		SearchProviderTavily, SearchProviderDuckDuckGo, SearchProviderBaidu,
		SearchProviderGLM, SearchProviderExa,
	}
	if !slices.Equal(ids, wantOrder) {
		t.Fatalf("catalogue order = %v, want %v", ids, wantOrder)
	}

	for _, def := range SearchProviderCatalogue {
		if def.ID == "" || def.Section == "" || def.DisplayName == "" {
			t.Errorf("provider %q: id/section/display_name must be set", def.ID)
		}
		if def.Enabled == nil || def.SetEnabled == nil {
			t.Errorf("provider %q: enabled accessors must be wired", def.ID)
		}
		if def.Keyed && (def.APIKey == nil || def.APIKeyRef == nil) {
			t.Errorf("provider %q: keyed providers need key accessors", def.ID)
		}
		if def.RequiresBaseURL && def.BaseURL == nil {
			t.Errorf("provider %q: base-URL providers need a BaseURL accessor", def.ID)
		}
	}

	keyedWant := []string{"perplexity", "brave", "tavily", "baidu", "glm", "exa"}
	var keyedGot []string
	var baseURLWant []string
	for _, def := range SearchProviderCatalogue {
		if def.Keyed {
			keyedGot = append(keyedGot, def.ID)
		}
		if def.RequiresBaseURL {
			baseURLWant = append(baseURLWant, def.ID)
		}
	}
	if !slices.Equal(keyedGot, keyedWant) {
		t.Errorf("keyed set = %v, want %v", keyedGot, keyedWant)
	}
	if len(baseURLWant) != 0 {
		t.Errorf("base-URL-required providers = %v, want none after SearXNG removal", baseURLWant)
	}

	// Capabilities (ADR-096 D9/D12 matrix): depth on Tavily/Perplexity/GLM,
	// site filters on Tavily/Perplexity/Exa.
	depthWant := map[string]bool{SearchProviderTavily: true, SearchProviderPerplexity: true, SearchProviderGLM: true}
	siteWant := map[string]bool{SearchProviderTavily: true, SearchProviderPerplexity: true, SearchProviderExa: true}
	for _, def := range SearchProviderCatalogue {
		if def.HonoursDepth != depthWant[def.ID] {
			t.Errorf("provider %q: HonoursDepth = %v", def.ID, def.HonoursDepth)
		}
		if def.HonoursSiteFilters != siteWant[def.ID] {
			t.Errorf("provider %q: HonoursSiteFilters = %v", def.ID, def.HonoursSiteFilters)
		}
	}

	// The PRE-ADR chain the migration mirrors (D11): keyed subsequence of
	// the legacy chain, Exa absent.
	wantKeyedLegacy := []string{SearchProviderPerplexity, SearchProviderBrave, SearchProviderTavily, SearchProviderBaidu, SearchProviderGLM}
	if got := webRolesKeyedChainIDs(); !slices.Equal(got, wantKeyedLegacy) {
		t.Errorf("legacy keyed chain = %v, want %v", got, wantKeyedLegacy)
	}
	wantLegacy := []string{SearchProviderPerplexity, SearchProviderBrave, SearchProviderTavily, SearchProviderDuckDuckGo, SearchProviderBaidu, SearchProviderGLM}
	w := &DefaultConfig().Tools.Web
	if got := w.preADRChainIDs(); !slices.Equal(got, wantLegacy) {
		t.Errorf("migration legacy chain = %v, want %v", got, wantLegacy)
	}
}

// TestCatalogueWalk_AddedProviderIsStorableAndSelectable is spec test 48's
// config half: adding a provider to the catalogue and NOTHING else makes it
// usable (storable/evaluable) through the one usability test and visible to
// the migration helpers, derived by walking — no id named.
func TestCatalogueWalk_AddedProviderIsStorableAndSelectable(t *testing.T) {
	defer appendTestProvider(t)()

	w := &DefaultConfig().Tools.Web
	w.Exa.Enabled = true
	w.Exa.APIKeyRef = "TESTPROV_API_KEY"
	t.Setenv("TESTPROV_API_KEY", "k")

	if !w.UsableSearchProvider("testprov") {
		t.Fatal("added provider is not usable through the one usability test (spec test 48: storable)")
	}
	if !enabledKeyedWithRef(w, "testprov") {
		t.Fatal("added provider invisible to enabledKeyedWithRef (migration defer predicate)")
	}
	w.Exa.Enabled = false
	if !offKeyedWithResolvingRef(w, "testprov") {
		t.Fatal("added provider invisible to offKeyedWithResolvingRef (migration step 9 predicate)")
	}
	setKeyedEnabled(w, "testprov", true)
	if !w.Exa.Enabled {
		t.Fatal("setKeyedEnabled does not cover the added provider")
	}

	// Selectable: the legacy chain is UNCHANGED by the append (the added
	// provider carries no legacy position — the migration mirrors pre-ADR
	// behaviour, so a test-only provider must not enter it). DDG is switched
	// off here so the assertion cannot pass "by accident" via DDG being
	// usable by default: the only way the answer stays duckduckgo is the
	// final-fallback branch over the LEGACY chain.
	w.DuckDuckGo.Enabled = false
	if got := w.migrationWinnerID(); got != SearchProviderDuckDuckGo {
		t.Fatalf("migration winner = %q after appending a position-less provider; legacy chain must be unchanged", got)
	}
}

// catalogueIDsForTest returns the catalogue ids in declaration order.
func catalogueIDsForTest() []string {
	out := make([]string, 0, len(SearchProviderCatalogue))
	for _, def := range SearchProviderCatalogue {
		out = append(out, def.ID)
	}
	return out
}

// appendTestProvider appends the spec-test-48 synthetic provider — a keyed
// catalogue entry wired to the REAL Exa config fields so tests can toggle
// enabled/key state — and restores the catalogue on test end. The closures
// read real fields (usable/storable paths run the real code); only the
// id/section/ref labels are synthetic.
func appendTestProvider(t *testing.T) func() {
	t.Helper()
	saved := SearchProviderCatalogue
	SearchProviderCatalogue = append(append([]SearchProviderDef{}, saved...), SearchProviderDef{
		ID:          "testprov",
		Section:     "testprov",
		DisplayName: "Test Provider",
		Keyed:       true,
		CredRef:     "TESTPROV_API_KEY",
		Enabled:     func(w *WebToolsConfig) bool { return w.Exa.Enabled },
		SetEnabled:  func(w *WebToolsConfig, on bool) { w.Exa.Enabled = on },
		APIKeyRef:   func(*WebToolsConfig) string { return "TESTPROV_API_KEY" },
		APIKey:      func(w *WebToolsConfig) string { return w.Exa.APIKey() },
	})
	t.Cleanup(func() { SearchProviderCatalogue = saved })
	return func() {}
}
