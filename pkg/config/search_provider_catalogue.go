// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "slices"

// search_provider_catalogue.go — THE single web-search provider catalogue
// (ADR-096 D15, spec FR-035, AC-20). One list of provider ids, config
// section names, display names, credential refs, keyed-or-keyless, capability
// flags and PRE-ADR chain positions, with the per-provider config accessors.
// Every consumer derives from it (loops, not hand-written lists): the tool's
// resolver and enabled-but-keyless warning (pkg/tools), the Settings
// catalogue and the save path's id→section mapping (pkg/gateway), the
// credential-injection enumeration (pkg/credentials), and the roles
// migration (this package). Adding a provider here and nothing else makes it
// storable, injectable, selectable, warnable and listable — pinned by the
// catalogue walk tests.

// SearchProviderDef is one catalogue entry: the provider's identity,
// requirements, capabilities, and the accessors binding it to its
// WebToolsConfig section.
type SearchProviderDef struct {
	// ID is the catalogue id — the role-key vocabulary (default_provider /
	// fallback_provider) and the contract's IntegrationProviderId enum.
	ID string
	// Section is the tools.web sub-object key carrying the provider's
	// configuration. Ids and sections differ only for GLM and Baidu
	// (glm_search / baidu_search) — the mapping the Settings save already
	// used.
	Section string
	// DisplayName is the Settings list name.
	DisplayName string
	// Keyed reports whether the provider needs an API key — a vault entry
	// named by its api_key_ref, injected into the process environment by
	// credentials.InjectFromConfig.
	Keyed bool
	// RequiresBaseURL reports whether the provider is usable only with a
	// non-empty base URL (SearXNG — the keyless-with-prerequisite row of
	// ADR-096's Definitions table).
	RequiresBaseURL bool
	// CredRef is the shipped credential-store entry name a Settings save
	// writes (and the provider's api_key_ref then carries); empty for
	// keyless providers.
	CredRef string
	// HonoursDepth and HonoursSiteFilters are the ADR-096 D9/D12 capability
	// matrix: depth on Tavily, Perplexity and GLM; site filters on Tavily,
	// Perplexity and Exa.
	HonoursDepth       bool
	HonoursSiteFilters bool
	// PreADRChainPos is the provider's 1-based position in the PRE-ADR
	// selection chain (Perplexity > Brave > SearXNG > Tavily > DuckDuckGo >
	// Baidu > GLM) that the roles migration mirrors verbatim (D11); 0 for
	// providers absent from that chain (Exa). The migration is a backward
	// fix: it records who the OLD chain would have picked, so new providers
	// correctly never enter it.
	PreADRChainPos int

	// Accessors bind the def to its WebToolsConfig section. They are the
	// ONE place the per-provider config fields are named; every derivation
	// goes through them.
	Enabled    func(*WebToolsConfig) bool
	SetEnabled func(w *WebToolsConfig, on bool)
	APIKeyRef  func(*WebToolsConfig) string
	APIKey     func(*WebToolsConfig) string
	// BaseURL is wired only for RequiresBaseURL providers.
	BaseURL func(*WebToolsConfig) string
}

// SearchProviderCatalogue is the ONE catalogue, in the operator-facing
// display order (the legacy chain order, with Exa appended). Production code
// only reads it; tests may append entries — spec test 48 adds a provider and
// nothing else, then demands storable / injectable / selectable / warnable /
// listable — restoring the slice afterwards.
var SearchProviderCatalogue = []SearchProviderDef{
	{
		ID: SearchProviderPerplexity, Section: "perplexity", DisplayName: "Perplexity",
		Keyed: true, CredRef: "PERPLEXITY_API_KEY",
		HonoursDepth: true, HonoursSiteFilters: true,
		PreADRChainPos: 1,
		Enabled:        func(w *WebToolsConfig) bool { return w.Perplexity.Enabled },
		SetEnabled:     func(w *WebToolsConfig, on bool) { w.Perplexity.Enabled = on },
		APIKeyRef:      func(w *WebToolsConfig) string { return w.Perplexity.APIKeyRef },
		APIKey:         func(w *WebToolsConfig) string { return w.Perplexity.APIKey() },
	},
	{
		ID: SearchProviderBrave, Section: "brave", DisplayName: "Brave Search",
		Keyed: true, CredRef: "BRAVE_API_KEY",
		PreADRChainPos: 2,
		Enabled:        func(w *WebToolsConfig) bool { return w.Brave.Enabled },
		SetEnabled:     func(w *WebToolsConfig, on bool) { w.Brave.Enabled = on },
		APIKeyRef:      func(w *WebToolsConfig) string { return w.Brave.APIKeyRef },
		APIKey:         func(w *WebToolsConfig) string { return w.Brave.APIKey() },
	},
	{
		ID: SearchProviderSearXNG, Section: "searxng", DisplayName: "SearXNG",
		RequiresBaseURL: true,
		PreADRChainPos:  3,
		Enabled:         func(w *WebToolsConfig) bool { return w.SearXNG.Enabled },
		SetEnabled:      func(w *WebToolsConfig, on bool) { w.SearXNG.Enabled = on },
		BaseURL:         func(w *WebToolsConfig) string { return w.SearXNG.BaseURL },
	},
	{
		ID: SearchProviderTavily, Section: "tavily", DisplayName: "Tavily",
		Keyed: true, CredRef: "TAVILY_API_KEY",
		HonoursDepth: true, HonoursSiteFilters: true,
		PreADRChainPos: 4,
		Enabled:        func(w *WebToolsConfig) bool { return w.Tavily.Enabled },
		SetEnabled:     func(w *WebToolsConfig, on bool) { w.Tavily.Enabled = on },
		APIKeyRef:      func(w *WebToolsConfig) string { return w.Tavily.APIKeyRef },
		APIKey:         func(w *WebToolsConfig) string { return w.Tavily.APIKey() },
	},
	{
		ID: SearchProviderDuckDuckGo, Section: "duckduckgo", DisplayName: "DuckDuckGo",
		PreADRChainPos: 5,
		Enabled:        func(w *WebToolsConfig) bool { return w.DuckDuckGo.Enabled },
		SetEnabled:     func(w *WebToolsConfig, on bool) { w.DuckDuckGo.Enabled = on },
	},
	{
		ID: SearchProviderBaidu, Section: "baidu_search", DisplayName: "Baidu Search",
		Keyed: true, CredRef: "BAIDU_API_KEY",
		PreADRChainPos: 6,
		Enabled:        func(w *WebToolsConfig) bool { return w.BaiduSearch.Enabled },
		SetEnabled:     func(w *WebToolsConfig, on bool) { w.BaiduSearch.Enabled = on },
		APIKeyRef:      func(w *WebToolsConfig) string { return w.BaiduSearch.APIKeyRef },
		APIKey:         func(w *WebToolsConfig) string { return w.BaiduSearch.APIKey() },
	},
	{
		ID: SearchProviderGLM, Section: "glm_search", DisplayName: "GLM Search",
		Keyed: true, CredRef: "GLM_API_KEY",
		HonoursDepth: true,
		// GLM's depth axis has no low value and its site-filter wire shape
		// is unverified — D9 refuses site filters on GLM entirely.
		HonoursSiteFilters: false,
		PreADRChainPos:     7,
		Enabled:            func(w *WebToolsConfig) bool { return w.GLMSearch.Enabled },
		SetEnabled:         func(w *WebToolsConfig, on bool) { w.GLMSearch.Enabled = on },
		APIKeyRef:          func(w *WebToolsConfig) string { return w.GLMSearch.APIKeyRef },
		APIKey:             func(w *WebToolsConfig) string { return w.GLMSearch.APIKey() },
	},
	{
		ID: SearchProviderExa, Section: "exa", DisplayName: "Exa",
		Keyed: true, CredRef: "EXA_API_KEY",
		HonoursSiteFilters: true,
		PreADRChainPos:     0, // not in the PRE-ADR chain — it did not exist
		Enabled:            func(w *WebToolsConfig) bool { return w.Exa.Enabled },
		SetEnabled:         func(w *WebToolsConfig, on bool) { w.Exa.Enabled = on },
		APIKeyRef:          func(w *WebToolsConfig) string { return w.Exa.APIKeyRef },
		APIKey:             func(w *WebToolsConfig) string { return w.Exa.APIKey() },
	},
}

// SearchProviderDefByID returns the catalogue def for id, or false for an
// unknown id (R9: never usable, never a boot failure).
func SearchProviderDefByID(id string) (SearchProviderDef, bool) {
	for i := range SearchProviderCatalogue {
		if SearchProviderCatalogue[i].ID == id {
			return SearchProviderCatalogue[i], true
		}
	}
	return SearchProviderDef{}, false
}

// preADRChainDefs returns the catalogue defs that sit on the PRE-ADR
// selection chain, in that chain's order — the order the roles migration
// must mirror (D11) and the tie-break order its step 9 uses.
func preADRChainDefs() []SearchProviderDef {
	sorted := make([]SearchProviderDef, 0, len(SearchProviderCatalogue))
	for _, def := range SearchProviderCatalogue {
		if def.PreADRChainPos > 0 {
			sorted = append(sorted, def)
		}
	}
	slices.SortStableFunc(sorted, func(a, b SearchProviderDef) int {
		return a.PreADRChainPos - b.PreADRChainPos
	})
	return sorted
}
