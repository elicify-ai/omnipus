package tools

// web_search_roles_us4_test.go — US-4 (P0): the agent's provider pick is
// honoured, and unusable picks get an honest refusal (spec "Agent-chosen
// provider", lines 266–285).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// The agent's pick is honoured: brave chosen -> brave runs, nobody else, and
// the result names it as (chosen).
func TestChosen_Honoured(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	res := f.run(map[string]any{"query": "golang", "provider": "brave"})
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: brave (chosen)") {
		t.Fatalf("expected brave (chosen), got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("tavily"); h != 0 {
		t.Fatalf("tavily hits = %d, want 0", h)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// US-4.2: a named unusable provider is a refusal with the usable ids listed.
func TestChosen_UnusableRefusal(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.Exa = config.ExaConfig{Enabled: false, APIKeyRef: envRefMissingBrav}
	}, func(o *WebSearchToolOptions) {
		o.ExaEnabled = false
	})
	res := f.run(map[string]any{"query": "golang", "provider": "exa"})
	if !res.IsError {
		t.Fatalf("expected refusal, got: %s", res.ForLLM)
	}
	if !strings.HasPrefix(res.ForLLM, "search failed\n- exa: not usable\nUsable providers: tavily, duckduckgo") {
		t.Fatalf("expected 3-line refusal, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("tavily"); h != 0 {
		t.Fatalf("tavily hits = %d, want 0", h)
	}
}

// Round-2 D6 correction: naming the resolved default behaves EXACTLY as
// omitting the argument — the operator's fallback stays live.
func TestChosen_NamedDefaultEqualsOmitted(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{"query": "golang", "provider": "tavily"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: duckduckgo (fallback)") {
		t.Fatalf("expected duckduckgo (fallback), got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 1 {
		t.Fatalf("duckduckgo hits = %d, want 1", h)
	}
}

// Naming a usable provider with that provider's own capability and failing ->
// hard fail, NO hop, and the message explains the capability reason and an alternative.
func TestChosen_CapabilityFailHardFails_NoHop(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{"query": "golang", "provider": "tavily", "include_domains": []any{"example.com"}})
	if !res.IsError {
		t.Fatalf("expected hard fail, got: %s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0 (no hop)", h)
	}
	if !strings.Contains(res.ForLLM, "- tavily (chosen): network:") {
		t.Fatalf("expected tavily (chosen) line, got:\n%s", res.ForLLM)
	}
	lower := strings.ToLower(res.ForLLM)
	if !strings.Contains(lower, "capability") {
		t.Fatalf("expected the provider-specific capability reason for no hop (ADR-096 D6/AC-5), got:\n%s", res.ForLLM)
	}
	if strings.Contains(lower, "no fallback is configured") || strings.Contains(lower, "no fallback configured") {
		t.Fatalf("duckduckgo is configured and eligible, so the message must not claim there is no fallback, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(lower, "duckduckgo") {
		t.Fatalf("expected duckduckgo as an alternative provider (ADR-096 D6), got:\n%s", res.ForLLM)
	}
}

// A plain query on a named provider hops on a hop-class failure.
func TestChosen_PlainFailHops(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	f.setHandler("brave", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	res := f.run(map[string]any{"query": "golang", "provider": "brave"})
	if res.IsError {
		t.Fatalf("expected hop success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: duckduckgo (fallback)") {
		t.Fatalf("expected duckduckgo (fallback), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "brave (chosen) failed") {
		t.Fatalf("expected note naming brave (chosen), got:\n%s", res.ForLLM)
	}
}

// A named id that is not in the catalogue at all is the same refusal shape.
func TestChosen_UnknownIDRefusal(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	res := f.run(map[string]any{"query": "golang", "provider": "not-a-provider"})
	if !res.IsError {
		t.Fatalf("expected refusal, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- not-a-provider: not usable") {
		t.Fatalf("expected unknown-id line, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Usable providers: tavily, duckduckgo") {
		t.Fatalf("expected usable list, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("tavily"); h != 0 {
		t.Fatalf("tavily hits = %d, want 0", h)
	}
}

// Perplexity's prose answer is that provider's own capability: naming it and
// failing is a hard fail, no hop (spec agent-chosen table, capability row).
func TestChosen_PerplexityProseIsCapability_HardFail(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderDuckDuckGo
		c.FallbackProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, nil)
	f.setHandler("perplexity", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	res := f.run(map[string]any{"query": "golang", "provider": "perplexity"})
	if !res.IsError {
		t.Fatalf("expected hard fail, got: %s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0 (named, no hop)", h)
	}
	if !strings.Contains(res.ForLLM, "- perplexity (chosen): upstream:") {
		t.Fatalf("expected perplexity (chosen) line, got:\n%s", res.ForLLM)
	}
}
