package agent

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Fix round 6 (issue #1340, founder decision 2026-10-18): an agent pinned to
// openrouter with a vendor-prefixed model id ("deepseek/<model>") must not
// carry a phantom fallback (deepseek, <model>) produced by re-resolving the
// pinned primary through the default path. Expected values come from the
// directive: the only candidate is the pinned pair; a fallback-eligible
// primary failure surfaces as that primary failure, never as
// model_route_unavailable naming "deepseek"; a vendor prefix that is not a
// provider at all ("qa-1340/...") leaves no unknown-fallback candidate; and a
// REAL declared fallback is still kept.

const routing1340PhantomModel = "deepseek/deepseek-v4.1-flash-1017"

// routing1340PhantomConfig mirrors the hands-on install: one OpenRouter row
// whose own model differs from the agent's pinned model.
func routing1340PhantomConfig(apiBase string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"},
		}},
		Providers: []*config.ModelConfig{{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash", APIBase: apiBase}},
	}
}

func TestRouting1340_PinnedVendorModelHasNoPhantomFallback(t *testing.T) {
	for _, tc := range []struct{ name, model string }{
		{"catalog_vendor_prefix", routing1340PhantomModel},
		{"non_provider_prefix", "qa-1340/no-such-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := routing1340PhantomConfig("https://openrouter.invalid/api/v1")
			candidates := resolveAgentCandidatesWithPrimaryProvider(cfg, "openrouter", tc.model, "openrouter", nil, nil)
			want := []providers.FallbackCandidate{{Provider: "openrouter", Model: tc.model}}
			if !reflect.DeepEqual(candidates, want) {
				t.Fatalf("candidates = %+v, want only the pinned pair %+v", candidates, want)
			}
			build := buildProviderPool(cfg, candidates, "routing1340-phantom")
			if len(build.failures) != 0 {
				t.Errorf("pool build recorded route failures for a pinned, configured pair: %v", build.failures)
			}
			if build.primaryUnknown {
				t.Error("configured OpenRouter primary was marked unknown")
			}
		})
	}
}

func TestRouting1340_PinnedVendorModelKeepsRealDeclaredFallback(t *testing.T) {
	cfg := routing1340PhantomConfig("https://openrouter.invalid/api/v1")
	fallbacks := []config.FallbackModel{{Provider: "missing", Model: "fallback"}}
	candidates := resolveAgentCandidatesWithPrimaryProvider(cfg, "openrouter", routing1340PhantomModel, "openrouter", fallbacks, nil)
	want := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: routing1340PhantomModel},
		{Provider: "missing", Model: "fallback"},
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("candidates = %+v, want pinned primary then the declared fallback %+v", candidates, want)
	}
}

// Fix round 7 (R6-1): a configured fallback that happens to equal the pair the
// pinned primary's vendor prefix would re-resolve to is a REAL fallback and
// must be kept, in order, behind the pinned primary.
func TestRouting1340_PinnedVendorModelKeepsCollidingDeclaredFallback(t *testing.T) {
	cfg := routing1340PhantomConfig("https://openrouter.invalid/api/v1")
	fallbacks := []config.FallbackModel{{Provider: "deepseek", Model: "deepseek-v4.1-flash-1017"}}
	candidates := resolveAgentCandidatesWithPrimaryProvider(cfg, "openrouter", routing1340PhantomModel, "openrouter", fallbacks, nil)
	want := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: routing1340PhantomModel},
		{Provider: "deepseek", Model: "deepseek-v4.1-flash-1017"},
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("candidates = %+v, want pinned primary then the configured (deepseek, model) fallback %+v", candidates, want)
	}
}

// The real turn path: resolver → pool build (as published at construction) →
// selection → provider call. The OpenRouter edge answers HTTP 503, a
// fallback-eligible failure.
func TestRouting1340_PinnedVendorModelSurfacesPrimaryFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream overloaded","code":503}}`)
	}))
	defer server.Close()
	cfg := routing1340PhantomConfig(server.URL + "/v1")
	candidates := resolveAgentCandidatesWithPrimaryProvider(cfg, "openrouter", routing1340PhantomModel, "openrouter", nil, nil)
	build := buildProviderPool(cfg, candidates, "routing1340-phantom")
	a := &AgentInstance{ID: "routing1340-phantom", Model: routing1340PhantomModel, Provider: &routing1340DefaultClient{}, Candidates: candidates}
	a.storeProviderPoolBuild(build)
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	if err == nil {
		t.Fatal("a 503 primary produced no error")
	}
	if requests.Load() == 0 {
		t.Fatal("the pinned OpenRouter primary was never called")
	}
	var route *providers.ModelRouteUnavailableError
	if errors.As(err, &route) {
		t.Fatalf("primary failure was replaced by a route refusal for (%s, %s): %v", route.Provider, route.Model, err)
	}
	llm := TranslateTurnError(err)
	if llm.Code == CodeModelRouteUnavailable {
		t.Errorf("translated code=%q, want the primary's own failure, not model_route_unavailable", llm.Code)
	}
	if strings.Contains(strings.ToLower(llm.Message), "deepseek cannot serve") || llm.Provider == "deepseek" {
		t.Errorf("user-facing failure names the phantom provider: %+v", llm)
	}
	pe := errorToProviderError(err)
	if pe == nil || pe.Status != http.StatusServiceUnavailable || pe.Provider != "openrouter" || pe.Model != routing1340PhantomModel {
		t.Errorf("surfaced provider facts=%+v, want openrouter / %s / HTTP 503", pe, routing1340PhantomModel)
	}
}
