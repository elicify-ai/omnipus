package tools

// web_search_roles_depth_test.go — Depth (spec 528–558) and the capability
// body shapes (Perplexity context size, GLM content size). Expected values
// from the depth mapping table and the D20 clamp rule.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// The depth mapping table, on the wire: agent depth -> Tavily search_depth.
func TestDepth_TavilyMapping(t *testing.T) {
	cases := []struct {
		agent string
		want  string
	}{
		{"low", "fast"},
		{"medium", "basic"},
		{"high", "advanced"},
	}
	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			f := newRolesSearchFixture(t, nil, nil)
			res := f.run(map[string]any{"query": "golang", "depth": tc.agent})
			if res.IsError {
				t.Fatalf("depth %s: %s", tc.agent, res.ForLLM)
			}
			body := f.requestBodyMap(t, "tavily")
			if got, _ := body["search_depth"].(string); got != tc.want {
				t.Fatalf("depth %s: search_depth = %q, want %q", tc.agent, got, tc.want)
			}
		})
	}
}

// A new-install Tavily default (basic) is what an omitted depth sends; a
// migrated object (advanced) sends advanced. US-5 independent test.
func TestDepth_TavilyOmitUsesOperatorDefault(t *testing.T) {
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilySearchDepth = "basic"
	})
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "tavily")["search_depth"].(string); got != "basic" {
		t.Fatalf("omitted depth: search_depth = %q, want basic", got)
	}

	f2 := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilySearchDepth = "advanced"
	})
	res2 := f2.run(map[string]any{"query": "golang"})
	if res2.IsError {
		t.Fatalf("expected success: %s", res2.ForLLM)
	}
	if got, _ := f2.requestBodyMap(t, "tavily")["search_depth"].(string); got != "advanced" {
		t.Fatalf("migrated omit: search_depth = %q, want advanced", got)
	}
}

// D20 clamp: the operator's depth is a ceiling. high clamped to basic, with
// a note in the result.
func TestDepth_ClampHighToOperatorBasic(t *testing.T) {
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilySearchDepth = "basic"
	})
	res := f.run(map[string]any{"query": "golang", "depth": "high"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "tavily")["search_depth"].(string); got != "basic" {
		t.Fatalf("clamped search_depth = %q, want basic", got)
	}
	if !strings.Contains(res.ForLLM, "clamped") {
		t.Fatalf("expected clamp note, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "basic") {
		t.Fatalf("clamp note must name the operator value, got:\n%s", res.ForLLM)
	}
}

// ultra-fast is operator-only: passed through when the agent omits depth,
// and a clamp ceiling when the agent asks for high.
func TestDepth_UltraFastOperatorOnly(t *testing.T) {
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilySearchDepth = "ultra-fast"
	})
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "tavily")["search_depth"].(string); got != "ultra-fast" {
		t.Fatalf("operator ultra-fast passthrough = %q, want ultra-fast", got)
	}

	f2 := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilySearchDepth = "ultra-fast"
	})
	res2 := f2.run(map[string]any{"query": "golang", "depth": "high"})
	if res2.IsError {
		t.Fatalf("expected success: %s", res2.ForLLM)
	}
	if got, _ := f2.requestBodyMap(t, "tavily")["search_depth"].(string); got != "ultra-fast" {
		t.Fatalf("high clamped to ultra-fast = %q, want ultra-fast", got)
	}
}

// GLM: content_size is the depth axis (medium/high only); agent low refuses
// before any request.
func TestDepth_GLM_MappingAndLowRefusal(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "glm")["content_size"].(string); got != "medium" {
		t.Fatalf("glm content_size = %q, want medium", got)
	}

	f2 := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, func(o *WebSearchToolOptions) {
		// D20: the operator's configured depth is a ceiling, "not just a
		// default" — the shipped medium ceiling clamps the agent's high to
		// medium (pinned by TestFixK2_GLMHighClampedToOperatorMedium). The
		// depth-table mapping high→high needs the ceiling RAISED: here the
		// operator sets high, so the agent's high passes through unclamped.
		o.GLMContentSize = "high"
	})
	res2 := f2.run(map[string]any{"query": "golang", "depth": "high"})
	if res2.IsError {
		t.Fatalf("expected success: %s", res2.ForLLM)
	}
	if got, _ := f2.requestBodyMap(t, "glm")["content_size"].(string); got != "high" {
		t.Fatalf("glm high content_size = %q, want high", got)
	}

	f3 := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, nil)
	res3 := f3.run(map[string]any{"query": "golang", "depth": "low"})
	if !res3.IsError {
		t.Fatalf("expected refusal for glm low, got: %s", res3.ForLLM)
	}
	if h := f3.hitsOf("glm"); h != 0 {
		t.Fatalf("glm hits = %d, want 0", h)
	}
}

// Perplexity: web_search_options.search_context_size is sent ONLY when the
// operator set it or the agent passed depth (sending a default would change
// the bill, D11).
func TestDepth_PerplexityContextSizeGating(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if _, present := f.requestBodyMap(t, "perplexity")["web_search_options"]; present {
		t.Fatal("web_search_options must be absent when operator unset and agent omitted depth")
	}

	f2 := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, func(o *WebSearchToolOptions) {
		o.PerplexityContextSize = "low"
	})
	res2 := f2.run(map[string]any{"query": "golang"})
	if res2.IsError {
		t.Fatalf("expected success: %s", res2.ForLLM)
	}
	opts, _ := f2.requestBodyMap(t, "perplexity")["web_search_options"].(map[string]any)
	if got, _ := opts["search_context_size"].(string); got != "low" {
		t.Fatalf("operator context size = %v, want low", opts["search_context_size"])
	}

	f3 := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, nil)
	res3 := f3.run(map[string]any{"query": "golang", "depth": "high"})
	if res3.IsError {
		t.Fatalf("expected success: %s", res3.ForLLM)
	}
	opts3, _ := f3.requestBodyMap(t, "perplexity")["web_search_options"].(map[string]any)
	if got, _ := opts3["search_context_size"].(string); got != "high" {
		t.Fatalf("agent depth high -> context size = %v, want high", opts3["search_context_size"])
	}
}

// Depth on a provider that cannot honour it is refused before any request,
// naming the usable providers that can.
func TestDepth_IncapableDefaultRefusal(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	res := f.run(map[string]any{"query": "golang", "depth": "high"})
	if !res.IsError {
		t.Fatalf("expected refusal, got: %s", res.ForLLM)
	}
	if !strings.HasPrefix(res.ForLLM, "search failed\n- brave (default): rejected: depth is not supported\nProviders that support depth: tavily") {
		t.Fatalf("expected depth refusal, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("brave"); h != 0 {
		t.Fatalf("brave hits = %d, want 0", h)
	}
}
