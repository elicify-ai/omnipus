package tools

// web_search_fix_chosenhop_test.go — gate round 1 finding K4: the chosen
// path's hop is gated on usability alone; it must pass the SAME fallback
// eligibility gate as the default path — include_domains and the D17a time
// budget — because a named call's hop spends the same fallback budget
// (ADR-096 D17a: "no time budget remaining"; spec failover section).
//
// Expectations derive from the spec's failover vocabulary, not the code: an
// out-of-budget hop ends the call with the chosen provider's failure line
// and "- <fallback> (fallback): not called: no time budget remaining"; the
// fallback is never requested.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// A named plain call that fails hop-class must apply the same budget gate to
// its fallback as the default path does: out of budget, the fallback is not
// requested and the reason is the D17a vocabulary.
func TestFixK4_ChosenHopSkipsOutOfBudgetFallback(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, func(o *WebSearchToolOptions) {
		o.CallBudget = time.Nanosecond
	})
	f.setHandler("ddg", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	res := f.run(map[string]any{"query": "golang", "provider": "duckduckgo"})
	if !res.IsError {
		t.Fatalf("expected the call to fail without the fallback, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- perplexity (fallback): not called: no time budget remaining") {
		t.Fatalf("want the D17a budget skip line, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- duckduckgo (chosen):") {
		t.Fatalf("want the chosen provider's failure line, got:\n%s", res.ForLLM)
	}
	// Nothing runs under an exhausted budget: the budget clamps the first
	// attempt's context to expired, and the fallback gate reads "no time
	// budget remaining" (D17a). Both hit counts stay 0.
	if h := f.hitsOf("perplexity"); h != 0 {
		t.Fatalf("perplexity hits = %d, want 0 (out-of-budget fallback never runs)", h)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("ddg hits = %d, want 0 (exhausted budget expires the attempt context)", h)
	}
}

// Same gate, site-filter shape: a capability use never hops (US-4) even when
// the named provider can honour the capability and fails hop-class. Pinned
// to make sure the budget gate does not weaken the capability rule. (The
// K3 pre-flight refuses a capability the chosen provider cannot honour
// before any request, so here the named provider honours the filter and the
// failure is hop-class, exercising the hop branch itself.)
func TestFixK4_ChosenCapabilityUseStillNeverHops(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderPerplexity
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, nil)
	f.setHandler("perplexity", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	res := f.run(map[string]any{"query": "golang", "provider": "perplexity", "include_domains": []any{"example.com"}})
	if !res.IsError {
		t.Fatalf("expected the capability call to fail without hopping, got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "(fallback)") {
		t.Fatalf("a capability use must never hop — no fallback line may appear, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Omit provider to use the default") {
		t.Fatalf("want the capability hard-fail tail, got:\n%s", res.ForLLM)
	}
}
