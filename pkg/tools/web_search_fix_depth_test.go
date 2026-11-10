package tools

// web_search_fix_depth_test.go — gate round 1 finding K2: the operator's
// configured depth is a CEILING on agent-requested depth for GLM
// (content_size) and Perplexity (search_context_size), exactly as it already
// is for Tavily (ADR-096 D20; spec "Depth": "depth may ask for less and is
// clamped if it asks for more, with a note in the result").
//
// Expectations derive from the spec, not the code:
//   - GLM axis (spec depth table): medium → medium, high → high (low is
//     refused at pre-flight — that is K3, not covered here); the operator's
//     tools.web.glm_search.content_size (absent → medium) is the ceiling.
//   - Perplexity axis: low < medium < high; a SET operator
//     search_context_size is the ceiling; an ABSENT one never clamps and
//     sends the field only when the operator set it or the agent passed
//     depth (D11).
//   - A clamp adds a note to the result naming the operator value (Tavily's
//     existing sentence is the in-repo note pattern).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// GLM: the operator set content_size medium; the agent asks for high.
// The wire must carry the ceiling, not the agent's ask, and the result must
// carry the clamp note.
func TestFixK2_GLMHighClampedToOperatorMedium(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, func(o *WebSearchToolOptions) {
		o.GLMContentSize = "medium"
	})
	res := f.run(map[string]any{"query": "golang", "depth": "high"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "glm")["content_size"].(string); got != "medium" {
		t.Fatalf("glm content_size = %q, want medium (operator ceiling)", got)
	}
	if !strings.Contains(res.ForLLM, "clamped") {
		t.Fatalf("expected clamp note, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `"medium"`) {
		t.Fatalf("clamp note must name the operator value, got:\n%s", res.ForLLM)
	}
}

// GLM within the ceiling: agent medium under a high ceiling sends medium and
// no note. (Regression cover for the ceiling comparison direction.)
func TestFixK2_GLMWithinCeilingNoNote(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, func(o *WebSearchToolOptions) {
		o.GLMContentSize = "high"
	})
	res := f.run(map[string]any{"query": "golang", "depth": "medium"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "glm")["content_size"].(string); got != "medium" {
		t.Fatalf("glm content_size = %q, want medium (within ceiling)", got)
	}
	if strings.Contains(res.ForLLM, "clamped") {
		t.Fatalf("no clamp note expected within ceiling, got:\n%s", res.ForLLM)
	}
}

// GLM omitted depth sends the operator's content_size (the ceiling value
// itself).
func TestFixK2_GLMOmitUsesOperatorCeiling(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, func(o *WebSearchToolOptions) {
		o.GLMContentSize = "high"
	})
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	if got, _ := f.requestBodyMap(t, "glm")["content_size"].(string); got != "high" {
		t.Fatalf("glm content_size = %q, want high (operator ceiling, omitted depth)", got)
	}
}

// Perplexity: the operator set search_context_size low; the agent asks for
// high. The wire must carry low, and the result must carry the clamp note.
func TestFixK2_PerplexityHighClampedToOperatorLow(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, func(o *WebSearchToolOptions) {
		o.PerplexityContextSize = "low"
	})
	res := f.run(map[string]any{"query": "golang", "depth": "high"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	opts, _ := f.requestBodyMap(t, "perplexity")["web_search_options"].(map[string]any)
	if got, _ := opts["search_context_size"].(string); got != "low" {
		t.Fatalf("perplexity search_context_size = %v, want low (operator ceiling)", opts["search_context_size"])
	}
	if !strings.Contains(res.ForLLM, "clamped") {
		t.Fatalf("expected clamp note, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `"low"`) {
		t.Fatalf("clamp note must name the operator value, got:\n%s", res.ForLLM)
	}
}

// Perplexity within the ceiling: agent medium under a high ceiling sends
// medium and no note.
func TestFixK2_PerplexityWithinCeilingNoNote(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, func(o *WebSearchToolOptions) {
		o.PerplexityContextSize = "high"
	})
	res := f.run(map[string]any{"query": "golang", "depth": "medium"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	opts, _ := f.requestBodyMap(t, "perplexity")["web_search_options"].(map[string]any)
	if got, _ := opts["search_context_size"].(string); got != "medium" {
		t.Fatalf("perplexity search_context_size = %v, want medium (within ceiling)", opts["search_context_size"])
	}
	if strings.Contains(res.ForLLM, "clamped") {
		t.Fatalf("no clamp note expected within ceiling, got:\n%s", res.ForLLM)
	}
}

// Perplexity exactly at the ceiling: agent medium under a medium ceiling
// sends medium and no note.
func TestFixK2_PerplexityEqualCeilingNoNote(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, func(o *WebSearchToolOptions) {
		o.PerplexityContextSize = "medium"
	})
	res := f.run(map[string]any{"query": "golang", "depth": "medium"})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	opts, _ := f.requestBodyMap(t, "perplexity")["web_search_options"].(map[string]any)
	if got, _ := opts["search_context_size"].(string); got != "medium" {
		t.Fatalf("perplexity search_context_size = %v, want medium (at ceiling)", opts["search_context_size"])
	}
	if strings.Contains(res.ForLLM, "clamped") {
		t.Fatalf("no clamp note expected at ceiling, got:\n%s", res.ForLLM)
	}
}
