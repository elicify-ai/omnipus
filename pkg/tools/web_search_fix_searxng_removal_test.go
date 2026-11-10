package tools

// Issue #1056 F-2 / amended ADR-096 D10: retiring SearXNG removes it from
// agent-visible choices and does not turn an unset default into a silent
// DuckDuckGo or SearXNG call. The config-on-disk transition is pinned in
// pkg/config/search_provider_catalogue_fix_searxng_settings_test.go.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestFixF2_RemovedSearXNGNeverAppearsInAgentProviderEnum(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		// A legacy enabled configuration is supplied through the old JSON
		// shape, not through the Go field/type that GREEN will remove. Before
		// removal this provider is usable, so absence from the enum is a
		// genuine test of retirement, not an accidentally disabled fixture.
		if err := json.Unmarshal([]byte(`{"searxng":{"enabled":true,"base_url":"https://searx.example"}}`), c); err != nil {
			t.Fatalf("legacy search configuration fixture: %v", err)
		}
	}, nil)
	props := propsOf(t, f.tool)
	provider, ok := props["provider"].(map[string]any)
	if !ok {
		t.Fatalf("usable Tavily and DuckDuckGo must offer the provider selector, got %T", props["provider"])
	}
	enum, ok := provider["enum"].([]string)
	if !ok {
		t.Fatalf("provider selector has no string enum: %T", provider["enum"])
	}
	if len(enum) < 2 {
		t.Fatalf("provider selector did not include the two usable retained providers: %v", enum)
	}
	for _, id := range enum {
		if id == "searxng" {
			t.Fatalf("retired SearXNG still offered to the agent: %v", enum)
		}
	}
	for _, retained := range []string{"tavily", "duckduckgo"} {
		found := false
		for _, id := range enum {
			if id == retained {
				found = true
			}
		}
		if !found {
			t.Errorf("retained usable provider %s missing from agent enum: %v", retained, enum)
		}
	}
}

func TestFixF2_UnsetDefaultRefusesWithoutInvokingRemovedOrFallbackProvider(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		// The load-time retired-role scrub must produce these role values;
		// the public tool then observes them through its live Roles callback.
		c.DefaultProvider = ""
		c.FallbackProvider = "none"
	}, nil)
	result := f.run(map[string]any{"query": "retired provider must not answer"})
	if !result.IsError || !strings.Contains(result.ForLLM, "search failed") {
		t.Fatalf("unset default must refuse honestly, got IsError=%v: %q", result.IsError, result.ForLLM)
	}
	if strings.Contains(strings.ToLower(result.ForLLM), "searxng") {
		t.Fatalf("result names removed provider although neither current role names it: %q", result.ForLLM)
	}
	for id := range f.hits {
		if got := f.hitsOf(id); got != 0 {
			t.Errorf("unset default called %s %d times; expected no provider calls", id, got)
		}
	}
}
