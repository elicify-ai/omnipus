package tools

// web_search_fix_calltime_test.go — gate round 1 finding K1: call-time keys
// (ADR-096 D4a, AC-16, spec test 43).
//
// Oracle: docs/internal/architecture/ADR-096-web-search-provider-model.md
// D4a ("Usability is checked at call time. A key can appear after unlock…
// The tool holds a resolver, not baked key strings") and AC-16 ("A key that
// appears after the tool was built makes that provider callable without
// re-registration"); the D16 reason vocabulary ("no API key") for the empty
// key set, which is final — never a hop-class failure with a %!w(<nil>)
// wrapper.
//
// The fixture (web_search_roles_fixture_test.go) runs the REAL tool and REAL
// providers; only the network edge is an httptest server per provider.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Refs that are never set in the environment: a provider configured against
// one of these is unusable at tool construction (and stays unusable until
// the test sets the value).
const (
	k1RefMissingTavily = "K1_TAVILY_KEY_MISSING"
	k1RefMissingGLM    = "K1_GLM_KEY_MISSING"
)

// TestFixK1_LateKeyMakesDefaultCallable_SameToolInstance is spec test 43 /
// AC-16: the tool is built with Tavily enabled but unusable (its ref does
// not resolve, and no key reached the tool any other way); once the key
// appears, the SAME tool instance must serve the next call through Tavily —
// no re-registration, no rebuild.
func TestFixK1_LateKeyMakesDefaultCallable_SameToolInstance(t *testing.T) {
	f := newRolesSearchFixture(t,
		func(cfg *config.WebToolsConfig) {
			cfg.Tavily = config.TavilyConfig{Enabled: true, APIKeyRef: k1RefMissingTavily}
			cfg.FallbackProvider = config.SearchProviderNone
		},
		func(opts *WebSearchToolOptions) {
			opts.TavilyAPIKeys = nil // built unusable: no key reached the tool
		},
	)

	res := f.run(map[string]any{"query": "late key"})
	if !strings.Contains(res.ForLLM, "- tavily (default): not called: no API key") {
		t.Fatalf("call 1: want the R7 not-called line, got:\n%s", res.ForLLM)
	}
	if hits := f.hitsOf("tavily"); hits != 0 {
		t.Fatalf("call 1: tavily hit %d times, want 0", hits)
	}

	t.Setenv(k1RefMissingTavily, "late-key")

	res2 := f.run(map[string]any{"query": "late key"})
	if strings.Contains(res2.ForLLM, "search failed") {
		t.Fatalf("call 2: the late key must make Tavily callable on the same tool, got:\n%s", res2.ForLLM)
	}
	body := f.requestBodyMap(t, "tavily")
	if got := body["api_key"]; got != "late-key" {
		t.Fatalf("call 2: tavily api_key on the wire = %v, want %q (read at call time)", got, "late-key")
	}
	if !strings.Contains(res2.ForLLM, "Search provider: tavily (default)") {
		t.Fatalf("call 2: want Tavily to answer as the default, got:\n%s", res2.ForLLM)
	}
}

// TestFixK1_LateKeyDefaultAnswersNotAutoDDG is AC-16 under the R3 shape:
// with the fallback absent, a default whose key arrives late must answer
// ITSELF on the next call. On the snapshot construction the empty key pool
// was misread as a hop-class failure, so the auto DuckDuckGo fallback
// answered while the default carried the key.
func TestFixK1_LateKeyDefaultAnswersNotAutoDDG(t *testing.T) {
	f := newRolesSearchFixture(t,
		func(cfg *config.WebToolsConfig) {
			cfg.Tavily = config.TavilyConfig{Enabled: true, APIKeyRef: k1RefMissingTavily}
			// The operator's explicit DuckDuckGo fallback (R4) answers while
			// Tavily is unusable (R6). K6, gate round 1: with an ABSENT
			// fallback the auto-DuckDuckGo arm needs a usable default (R3's
			// third conjunct), so an unusable default would be R7
			// nobody-runs — not the scenario this test is about.
			cfg.FallbackProvider = config.SearchProviderDuckDuckGo
		},
		func(opts *WebSearchToolOptions) {
			opts.TavilyAPIKeys = nil
		},
	)

	res := f.run(map[string]any{"query": "q"})
	if !strings.Contains(res.ForLLM, "via DuckDuckGo") {
		t.Fatalf("call 1: want the auto fallback to answer the unusable default, got:\n%s", res.ForLLM)
	}
	if hits := f.hitsOf("ddg"); hits != 1 {
		t.Fatalf("call 1: ddg hit %d times, want 1", hits)
	}

	t.Setenv(k1RefMissingTavily, "late-key")

	res2 := f.run(map[string]any{"query": "q"})
	if !strings.Contains(res2.ForLLM, "(via Tavily)") {
		t.Fatalf("call 2: want Tavily itself to answer, got:\n%s", res2.ForLLM)
	}
	if hits := f.hitsOf("ddg"); hits != 1 {
		t.Fatalf("call 2: ddg hit %d times, want 1 (no hop once the default is callable)", hits)
	}
}

// TestFixK1_LateKeyGLMNamedCallCarriesLiveKey extends D4a to the single-key
// providers: a named GLM call after the key appears carries the live key in
// its Authorization header, and before the key appears GLM is refused, not
// called.
func TestFixK1_LateKeyGLMNamedCallCarriesLiveKey(t *testing.T) {
	f := newRolesSearchFixture(t,
		func(cfg *config.WebToolsConfig) {
			cfg.DefaultProvider = config.SearchProviderDuckDuckGo
			cfg.FallbackProvider = config.SearchProviderNone
			cfg.GLMSearch = config.GLMSearchConfig{Enabled: true, APIKeyRef: k1RefMissingGLM}
		},
		func(opts *WebSearchToolOptions) {
			opts.GLMSearchAPIKey = "" // built unusable
		},
	)

	res := f.run(map[string]any{"query": "q", "provider": "glm"})
	if !strings.Contains(res.ForLLM, "- glm: not usable") {
		t.Fatalf("call 1: want the honest chosen-provider refusal, got:\n%s", res.ForLLM)
	}
	if hits := f.hitsOf("glm"); hits != 0 {
		t.Fatalf("call 1: glm hit %d times, want 0", hits)
	}

	t.Setenv(k1RefMissingGLM, "late-glm-key")

	res2 := f.run(map[string]any{"query": "q", "provider": "glm"})
	if got := f.lastAuth("glm"); got != "Bearer late-glm-key" {
		t.Fatalf("call 2: glm Authorization = %q, want %q (read at call time)", got, "Bearer late-glm-key")
	}
	if !strings.Contains(res2.ForLLM, "Search provider: glm (chosen)") {
		t.Fatalf("call 2: want GLM to answer as chosen, got:\n%s", res2.ForLLM)
	}
}

// TestFixK1_EmptyKeySetIsNotUsable_NotHopClass pins K1's second half: an
// empty key set is "not usable" (the D16 vocabulary, a final class), never
// the "all api keys failed, last error: %!w(<nil>)" wrapper — a nil-wrapped
// error that classified as a hop and read as a network failure.
func TestFixK1_EmptyKeySetIsNotUsable_NotHopClass(t *testing.T) {
	deadBase := "http://127.0.0.1:1" // port 1: connection refused, no network
	client := &http.Client{Timeout: 2 * time.Second}
	cases := []struct {
		name string
		call func() (string, error)
	}{
		{"tavily", func() (string, error) {
			p := &TavilySearchProvider{keyPool: NewAPIKeyPool(nil), baseURL: deadBase, client: client}
			return p.SearchWithCaps(context.Background(), searchRequest{query: "q"})
		}},
		{"perplexity", func() (string, error) {
			p := &PerplexitySearchProvider{keyPool: NewAPIKeyPool(nil), baseURL: deadBase, client: client}
			return p.SearchWithCaps(context.Background(), searchRequest{query: "q"})
		}},
		{"brave", func() (string, error) {
			p := &BraveSearchProvider{keyPool: NewAPIKeyPool(nil), baseURL: deadBase, client: client}
			return p.SearchWithCaps(context.Background(), searchRequest{query: "q"})
		}},
		{"glm", func() (string, error) {
			p := &GLMSearchProvider{baseURL: deadBase, client: client}
			return p.SearchWithCaps(context.Background(), searchRequest{query: "q"})
		}},
		{"baidu", func() (string, error) {
			p := &BaiduSearchProvider{baseURL: deadBase, client: client}
			return p.SearchWithCaps(context.Background(), searchRequest{query: "q"})
		}},
		{"exa", func() (string, error) {
			p := &ExaSearchProvider{baseURL: deadBase, client: client}
			return p.SearchWithCaps(context.Background(), searchRequest{query: "q"})
		}},
	}
	for _, tc := range cases {
		_, err := tc.call()
		var spErr *searchProviderError
		if !errors.As(err, &spErr) {
			t.Fatalf("%s: empty key set must be a searchProviderError (not usable), got %T: %v", tc.name, err, err)
		}
		if spErr.class != classNotUsable {
			t.Fatalf("%s: empty key set class = %q, want %q", tc.name, spErr.class, classNotUsable)
		}
		if spErr.msg != "no API key" {
			t.Fatalf("%s: empty key set message = %q, want the D16 reason %q", tc.name, spErr.msg, "no API key")
		}
		if hopClass(spErr.class) {
			t.Fatalf("%s: an empty key set must never be a hop class", tc.name)
		}
	}
}
