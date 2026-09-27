package tools

// web_search_roles_site_test.go — Site filters (spec 479–527): include is a
// requirement (refuse), exclude is a preference (proceed with a note), the
// hostname validator runs before any request, and the fallback is not
// eligible when it cannot honour an include.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// US-5.5: a Tavily default receives include_domains on the wire, snake_case.
func TestSite_TavilyReceivesIncludeDomains(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{"Example.COM", "docs.example.com"},
	})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	got, _ := f.requestBodyMap(t, "tavily")["include_domains"].([]any)
	if len(got) != 2 {
		t.Fatalf("include_domains length = %d, want 2", len(got))
	}
	if got[0] != "example.com" || got[1] != "docs.example.com" {
		t.Fatalf("include_domains = %v, want lowercased/deduped entries", got)
	}
}

// US-5.4: include on Brave (default) is refused before any request, with the
// exact spec text and the usable honouring list.
func TestSite_BraveIncludeRefusal(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{"example.com"},
	})
	if !res.IsError {
		t.Fatalf("expected refusal, got: %s", res.ForLLM)
	}
	want := "search failed\n- brave (default): rejected: site filter is not supported\nProviders that support site filters: tavily"
	if res.ForLLM != want {
		t.Fatalf("got:\n%s\nwant:\n%s", res.ForLLM, want)
	}
	if h := f.hitsOf("brave"); h != 0 {
		t.Fatalf("brave hits = %d, want 0", h)
	}
}

// Exclude is a preference: Brave cannot honour it, the search proceeds and
// the note names the provider and states the exclusion was not applied.
func TestSite_BraveExcludeProceeds(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"exclude_domains": []any{"spam.example.com"},
	})
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Note:") || !strings.Contains(res.ForLLM, "brave") || !strings.Contains(res.ForLLM, "not applied") {
		t.Fatalf("expected exclude note naming brave, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("brave"); h != 1 {
		t.Fatalf("brave hits = %d, want 1", h)
	}
}

// A domain in both lists is excluded: the exclusion wins on the wire.
func TestSite_BothListsExcludeWins(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{"shared.example.com", "keep.example.com"},
		"exclude_domains": []any{"shared.example.com", "other.example.com"},
	})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	body := f.requestBodyMap(t, "tavily")
	inc, _ := body["include_domains"].([]any)
	exc, _ := body["exclude_domains"].([]any)
	if len(inc) != 1 || inc[0] != "keep.example.com" {
		t.Fatalf("include_domains = %v, want only keep.example.com", inc)
	}
	if len(exc) != 2 {
		t.Fatalf("exclude_domains = %v, want 2 entries", exc)
	}
	for _, d := range inc {
		if d == "shared.example.com" {
			t.Fatalf("excluded domain must not remain in include list: %v", inc)
		}
	}
}

// The hop-skip rule: default honours include and fails; the fallback cannot
// honour it -> fallback listed as not called with the exact reason.
func TestSite_FallbackNotEligibleOnInclude(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{"example.com"},
	})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- tavily (default): network:") {
		t.Fatalf("expected tavily failure line, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- duckduckgo (fallback): not called: cannot honour include_domains") {
		t.Fatalf("expected fallback-not-eligible line, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// Count boundary: 10 entries pass and reach the wire; 11 are refused before
// any request.
func TestSite_CountBoundary(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	ten := []any{"a1.example.com", "a2.example.com", "a3.example.com", "a4.example.com", "a5.example.com",
		"a6.example.com", "a7.example.com", "a8.example.com", "a9.example.com", "a10.example.com"}
	res := f.run(map[string]any{"query": "golang", "include_domains": ten})
	if res.IsError {
		t.Fatalf("10 valid entries must pass: %s", res.ForLLM)
	}
	got, _ := f.requestBodyMap(t, "tavily")["include_domains"].([]any)
	if len(got) != 10 {
		t.Fatalf("include_domains length = %d, want 10", len(got))
	}

	f2 := newRolesSearchFixture(t, nil, nil)
	eleven := append(append([]any{}, ten...), "a11.example.com")
	res2 := f2.run(map[string]any{"query": "golang", "include_domains": eleven})
	if !res2.IsError {
		t.Fatalf("expected refusal for 11 entries, got: %s", res2.ForLLM)
	}
	if h := f2.hitsOf("tavily"); h != 0 {
		t.Fatalf("tavily hits = %d, want 0", h)
	}
}

// Validator rejections: wildcard, port, scheme, and an empty entry are all
// refused before any request.
func TestSite_InvalidHostnameRefusal(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{"*.wild.example.com", "bad.example.com:8080", "https://x.example.com", ""},
	})
	if !res.IsError {
		t.Fatalf("expected refusal, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "rejected") {
		t.Fatalf("expected rejected in refusal, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("tavily"); h != 0 {
		t.Fatalf("tavily hits = %d, want 0", h)
	}
}

// GLM refuses site filters while its request shape is unread.
func TestSite_GLMRefusesSiteFilters(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderGLM
		c.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: envRefGLM}
	}, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{"example.com"},
	})
	if !res.IsError {
		t.Fatalf("expected refusal, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "site filter is not supported") {
		t.Fatalf("expected site-filter refusal, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("glm"); h != 0 {
		t.Fatalf("glm hits = %d, want 0", h)
	}
}
