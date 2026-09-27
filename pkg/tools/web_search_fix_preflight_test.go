package tools

// web_search_fix_preflight_test.go — gate round 1 finding K3: GLM depth:low
// is refused on EVERY path that can reach GLM (FR-015; spec "Depth": "if the
// agent sets depth and that provider would run, refuse the same way as a
// site filter. Name the usable providers that honour depth. Do not hop.").
// The default path's pre-flight refuses GLM low today (pinned by
// TestDepth_GLM_MappingAndLowRefusal); the chosen, not-usable-default and
// hop-fallback paths reach GLM's request anyway. Expectations derive from
// the spec's refusal shape: "- <id> (<role>): rejected: depth is not
// supported", and no request fires (fixture hit counts stay 0).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// The chosen path: the agent names GLM with depth low. Refused before any
// request, in the spec's refusal shape.
func TestFixK3_ChosenGLMLowRefusedBeforeAnyRequest(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, nil)
	res := f.run(map[string]any{"query": "golang", "provider": "glm", "depth": "low"})
	if !res.IsError {
		t.Fatalf("expected GLM low refusal on the chosen path, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- glm (chosen): rejected: depth is not supported") {
		t.Fatalf("want the spec refusal line, got:\n%s", res.ForLLM)
	}
	// The tool-level pre-flight refusal names the usable depth providers
	// (spec: "Name the usable providers that honour depth"); a refusal that
	// reaches the provider guard instead does not.
	if !strings.Contains(res.ForLLM, "Providers that support depth:") {
		t.Fatalf("want the providers list from the pre-flight refusal, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("glm"); h != 0 {
		t.Fatalf("glm hits = %d, want 0 (refusal precedes any request)", h)
	}
}

// The not-usable-default path: the default cannot run, GLM is the fallback,
// the agent asked depth low. GLM must not run.
func TestFixK3_FallbackGLMLowRefusedBeforeAnyRequest(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefMissingBrav}
		c.FallbackProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, nil)
	res := f.run(map[string]any{"query": "golang", "depth": "low"})
	if !res.IsError {
		t.Fatalf("expected GLM low refusal on the fallback path, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- glm (fallback): rejected: depth is not supported") {
		t.Fatalf("want the spec refusal line for the fallback role, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Providers that support depth:") {
		t.Fatalf("want the providers list from the pre-flight refusal, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("glm"); h != 0 {
		t.Fatalf("glm hits = %d, want 0 (refusal precedes any request)", h)
	}
}

// The hop path: the default (Tavily) fails hop-class after the agent asked
// depth low; the resolved fallback is GLM. GLM must not be requested.
func TestFixK3_HopPathGLMLowRefusedNoRequest(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, nil)
	f.setHandler("tavily", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	res := f.run(map[string]any{"query": "golang", "depth": "low"})
	if !res.IsError {
		t.Fatalf("expected the call to fail without GLM, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- glm (fallback): rejected: depth is not supported") {
		t.Fatalf("want GLM's refusal line on the hop path, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("glm"); h != 0 {
		t.Fatalf("glm hits = %d, want 0 (no request on the hop path)", h)
	}
	if h := f.hitsOf("tavily"); h != 1 {
		t.Fatalf("tavily hits = %d, want 1", h)
	}
}
