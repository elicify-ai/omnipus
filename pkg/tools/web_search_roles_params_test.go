package tools

// web_search_roles_params_test.go — D7: the argument list is built from the
// usable set; Description() stays a constant sentence; the rendered
// definition is capped at 2,400 bytes at the maximal configuration
// (spec "Dynamic description and arguments", lines 400–451).

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// propsOf extracts the JSON-schema properties map from Parameters().
func propsOf(t *testing.T, tool *WebSearchTool) map[string]any {
	t.Helper()
	m := tool.Parameters()
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters missing properties map: %T", m["properties"])
	}
	return props
}

// DuckDuckGo-only: query, count, range. No provider, depth, or domain args.
func TestParams_DDGOnly(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.Tavily.Enabled = false
		c.DefaultProvider = config.SearchProviderDuckDuckGo
	}, func(o *WebSearchToolOptions) {
		o.TavilyEnabled = false
	})
	params := propsOf(t, f.tool)
	if _, ok := params["query"]; !ok {
		t.Fatal("query argument missing")
	}
	if _, ok := params["count"]; !ok {
		t.Fatal("count argument missing")
	}
	if _, ok := params["range"]; !ok {
		t.Fatal("range argument missing")
	}
	for _, banned := range []string{"provider", "depth", "include_domains", "exclude_domains"} {
		if _, ok := params[banned]; ok {
			t.Fatalf("%s must not be offered for DDG-only", banned)
		}
	}
}

// Exactly one usable provider that honours depth and site filters: the full
// capability set, and NO provider argument.
func TestParams_TavilyOnly(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DuckDuckGo.Enabled = false
	}, func(o *WebSearchToolOptions) {
		o.DuckDuckGoEnabled = false
	})
	params := propsOf(t, f.tool)
	for _, want := range []string{"query", "count", "range", "depth", "include_domains", "exclude_domains"} {
		if _, ok := params[want]; !ok {
			t.Fatalf("%s argument missing for tavily-only", want)
		}
	}
	if _, ok := params["provider"]; ok {
		t.Fatal("provider argument must not exist when exactly one provider is usable")
	}
}

// Two or more usable, mixed abilities, default honours capabilities: the
// provider enum plus depth and the domain arguments.
func TestParams_MixedDefaultHonours(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	params := propsOf(t, f.tool)
	provObj, ok := params["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider argument missing or wrong type: %T", params["provider"])
	}
	provEnum, ok := provObj["enum"].([]string)
	if !ok {
		t.Fatalf("provider enum missing or wrong type: %T", provObj["enum"])
	}
	if len(provEnum) != 2 {
		t.Fatalf("provider enum length = %d, want 2", len(provEnum))
	}
	for _, want := range []string{"depth", "include_domains", "exclude_domains"} {
		if _, ok := params[want]; !ok {
			t.Fatalf("%s argument missing in mixed config", want)
		}
	}
}

// Capabilities follow the RESOLVED DEFAULT, not "any usable provider": with
// Brave as the default the definition must not offer depth or domain args
// (round-2 correction).
func TestParams_CapabilitiesFollowDefault(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	params := propsOf(t, f.tool)
	if _, ok := params["provider"]; !ok {
		t.Fatal("provider argument missing for mixed config")
	}
	for _, banned := range []string{"depth", "include_domains", "exclude_domains"} {
		if _, ok := params[banned]; ok {
			t.Fatalf("%s must not be offered when the default is brave", banned)
		}
	}
}

// Description() is the same constant sentence for every configuration — the
// round-2 rule that keeps the agent-shown definition and GET /tools
// byte-identical by construction.
func TestParams_DescriptionConstantAcrossConfigs(t *testing.T) {
	f1 := newRolesSearchFixture(t, nil, nil)
	f2 := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	if f1.tool.Description() != f2.tool.Description() {
		t.Fatal("Description() differs across configurations; it must be a constant sentence")
	}
}

// The rendered definition (description + serialised Parameters) stays inside
// the 2,400-byte cap at the MAXIMAL configuration (all eight usable, every
// capability argument present). On overflow the "good for" clauses are
// dropped, never the enum.
func TestParams_SizeCapAtMaximalConfig(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderTavily
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
		c.BaiduSearch = config.BaiduSearchConfig{Enabled: true, APIKeyRef: envRefBaidu}
		c.Exa = config.ExaConfig{Enabled: true, APIKeyRef: envRefExa}
		c.SearXNG = config.SearXNGConfig{Enabled: true, BaseURL: "https://sx.example.com"}
	}, nil)
	rendered := f.tool.Description() + string(mustJSON(t, f.tool.Parameters()))
	if len(rendered) > 2400 {
		t.Fatalf("rendered definition = %d bytes, cap 2400", len(rendered))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal parameters: %v", err)
	}
	return b
}
