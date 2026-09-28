package tools

// web_search_roles_x_test.go — Exa as a provider (US-6, spec "Exa"
// 560–580), the Perplexity citations/temperature shape, and the legacy-path
// guard. Expected values from the spec's Exa table and capability matrix.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// US-6.1: Exa as default with fallback none — POST, Bearer token, named.
func TestExa_DefaultPOSTBearer(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderExa
		c.Exa = config.ExaConfig{Enabled: true, APIKeyRef: envRefExa}
		c.FallbackProvider = config.SearchProviderNone
	}, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: exa (default)") {
		t.Fatalf("expected exa (default), got:\n%s", res.ForLLM)
	}
	if m := f.lastMethod("exa"); m != http.MethodPost {
		t.Fatalf("exa method = %s, want POST", m)
	}
	if got := f.lastAuth("exa"); got != "Bearer wsfx-exa-key" {
		t.Fatalf("exa Authorization = %q, want Bearer wsfx-exa-key", got)
	}
}

// Exa site filters are camelCase on the wire — Tavily's snake_case keys are
// the exact class of silent lie this feature exists to remove.
func TestExa_CamelCaseFilters(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderExa
		c.Exa = config.ExaConfig{Enabled: true, APIKeyRef: envRefExa}
	}, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{"a.example.com"},
		"exclude_domains": []any{"b.example.com"},
	})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	body := f.requestBodyMap(t, "exa")
	inc, _ := body["includeDomains"].([]any)
	exc, _ := body["excludeDomains"].([]any)
	if len(inc) != 1 || inc[0] != "a.example.com" {
		t.Fatalf("includeDomains = %v, want [a.example.com]", inc)
	}
	if len(exc) != 1 || exc[0] != "b.example.com" {
		t.Fatalf("excludeDomains = %v, want [b.example.com]", exc)
	}
	if _, present := body["include_domains"]; present {
		t.Fatal("snake_case include_domains must not appear on the exa wire")
	}
}

// The shipped default base URL applies when the operator left it empty.
func TestExa_DefaultBaseURLWhenEmpty(t *testing.T) {
	p, err := newExaProvider(WebSearchToolOptions{ExaAPIKey: "k", ExaEnabled: true, IngestBoundBytes: 1 << 20}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if p.baseURL != "https://api.exa.ai/search" {
		t.Fatalf("default exa base URL = %q", p.baseURL)
	}
}

// numResults carries the agent's count on the exa wire (wire name flagged as
// a lane decision — spec says read title and URL; the field name is read
// from the current API docs at coding time).
func TestExa_NumResultsCarriesCount(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderExa
		c.Exa = config.ExaConfig{Enabled: true, APIKeyRef: envRefExa}
	}, nil)
	res := f.run(map[string]any{"query": "golang", "count": int64(3)})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "exa")["numResults"].(float64); got != 3 {
		t.Fatalf("exa numResults = %v, want 3", f.requestBodyMap(t, "exa")["numResults"])
	}
}

// US-6.2: the enabled-but-keyless warning walks the FULL keyed catalogue —
// every keyed provider that is enabled without a key is named, none is
// forgotten. The test constructs the all-enabled-no-keys options and asserts
// the returned set equals the keyed catalogue.
func TestExa_KeylessWarningEnumeration(t *testing.T) {
	opts := WebSearchToolOptions{
		PerplexityEnabled:  true,
		BraveEnabled:       true,
		TavilyEnabled:      true,
		GLMSearchEnabled:   true,
		BaiduSearchEnabled: true,
		ExaEnabled:         true,
	}
	got := map[string]bool{}
	for _, m := range enabledButKeylessSearchProviders(opts) {
		got[m.name] = true
	}
	want := map[string]bool{
		"perplexity": true, "brave": true, "tavily": true,
		"glm_search": true, "baidu_search": true, "exa": true,
	}
	if len(got) != len(want) {
		t.Fatalf("keyless list = %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("keyless list missing %q (got %v)", id, got)
		}
	}
}

// Perplexity: temperature 0 on the wire, citations rendered under the prose
// as a source list; missing citations render as "Sources: none returned".
func TestCaps_PerplexityCitationsAndTemperature(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "perplexity")["temperature"].(float64); got != 0 {
		t.Fatalf("temperature = %v, want 0", f.requestBodyMap(t, "perplexity")["temperature"])
	}
	if !strings.Contains(res.ForLLM, "Sources:") || !strings.Contains(res.ForLLM, "https://cite.example.com/one") {
		t.Fatalf("expected sources list with the citation URL, got:\n%s", res.ForLLM)
	}

	f2 := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, nil)
	f2.setHandler("perplexity", jsonBody(`{"choices":[{"message":{"content":"prose only"}}]}`))
	res2 := f2.run(map[string]any{"query": "golang"})
	if res2.IsError {
		t.Fatalf("expected success: %s", res2.ForLLM)
	}
	if !strings.Contains(res2.ForLLM, "Sources: none returned") {
		t.Fatalf("expected none-returned line, got:\n%s", res2.ForLLM)
	}
}

// Legacy guard: with Roles nil the tool behaves exactly as before this
// change — 3-argument schema, the legacy single-line error text — so all
// ~40 existing call sites and tests are untouched.
func TestLegacy_PathUnchanged_WhenRolesNil(t *testing.T) {
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.Roles = nil
	})
	props := propsOf(t, f.tool)
	for name := range props {
		switch name {
		case "query", "count", "range":
		default:
			t.Fatalf("legacy schema must expose only query/count/range, found %q", name)
		}
	}
	if _, ok := props["range"]; !ok {
		t.Fatal("legacy range argument missing")
	}
	dead := deadServerURL(t)
	f2 := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.Roles = nil
		o.TavilyBaseURL = dead
	})
	res := f2.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("legacy tavily failure must stay an error result, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "search failed: ") {
		t.Fatalf("expected legacy error text, got:\n%s", res.ForLLM)
	}
}
