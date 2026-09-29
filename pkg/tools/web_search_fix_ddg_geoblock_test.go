package tools

// web_search_fix_ddg_geoblock_test.go — RED pack for issue #1056 F-1: a
// DuckDuckGo call that fails with a network/TLS-class error returns a raw
// transport error ("network: request failed: Get \"https://html.
// duckduckgo.com/html/?q=test\": remote error: tls: handshake failure")
// with no hint that the cause is regional blocking, not an outage.
//
// Oracle: issue #1056 F-1's own citation — DuckDuckGo's Wikipedia-listed
// area served is "Worldwide, except for North Korea, Indonesia and China"
// (Reuters confirmed the Indonesia block 2024-08-02). The issue's suggested
// message names exactly those three. This test does not pin GREEN's exact
// sentence (the issue itself says "the exact list should be
// maintained/verified") — it asserts the substantive content: the failure
// text must say the provider is blocked/unavailable in some countries and
// must name all three documented ones.
//
// A live TLS handshake failure cannot be reproduced deterministically
// against an httptest server. `deadServerURL` (this package's existing
// fixture helper, used by other web_search_fix_*_test.go files as "the
// deterministic network-class failure") produces a connection error whose
// message matches classifySearchFailure's "request failed" substring rule
// exactly like a TLS handshake failure does (DuckDuckGoSearchProvider.
// SearchWithCaps wraps both as "request failed: %w") — both land in
// classNetwork, the same hop class the issue's repro shows. That is the
// class this test exercises; it is not a claim about TLS specifically.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestFixF1_DuckDuckGoNetworkFailureNamesBlockedRegions: DuckDuckGo is the
// resolved default with no fallback configured, so its network-class
// failure ends the call (R1/R2 — "The default only. A failure is a
// failure"). The resulting error text must name that DuckDuckGo is blocked
// in some countries and must list North Korea, Indonesia and mainland
// China (issue #1056 F-1's own citations) — not a bare transport error.
func TestFixF1_DuckDuckGoNetworkFailureNamesBlockedRegions(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderDuckDuckGo
		c.FallbackProvider = config.SearchProviderNone
	}, func(o *WebSearchToolOptions) {
		o.DuckDuckGoBaseURL = deadServerURL(t) // network-class failure, deterministic
	})

	res := f.run(map[string]any{"query": "test"})
	if !res.IsError {
		t.Fatalf("expected the DuckDuckGo network failure to end the call (no fallback configured), got: %s", res.ForLLM)
	}

	lower := strings.ToLower(res.ForLLM)
	if !strings.Contains(lower, "blocked") && !strings.Contains(lower, "unavailable") {
		t.Fatalf(
			"a DuckDuckGo network-class failure must carry a helpful note that it may be regionally "+
				"blocked (issue #1056 F-1), got: %s", res.ForLLM)
	}
	for _, region := range []string{"north korea", "indonesia", "mainland china"} {
		if !strings.Contains(lower, region) {
			t.Fatalf("blocked-region note must name %q (issue #1056 F-1's documented exclusions), got: %s", region, res.ForLLM)
		}
	}
}

// TestFixF1_DuckDuckGoNamedProviderNetworkFailureNamesBlockedRegions covers
// the issue's exact repro shape: the agent named `provider="duckduckgo"`
// directly (not merely resolved as the default) and it is the only usable
// provider, so its hard-fail tail fires (D6: naming the resolved default is
// treated exactly as omitting the argument — same code path, exercised via
// the explicit-name entry point to prove the note fires there too).
func TestFixF1_DuckDuckGoNamedProviderNetworkFailureNamesBlockedRegions(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderDuckDuckGo
		c.FallbackProvider = config.SearchProviderNone
	}, func(o *WebSearchToolOptions) {
		o.DuckDuckGoBaseURL = deadServerURL(t)
	})

	res := f.run(map[string]any{"query": "test", "provider": "duckduckgo"})
	if !res.IsError {
		t.Fatalf("expected the named DuckDuckGo network failure to end the call, got: %s", res.ForLLM)
	}

	lower := strings.ToLower(res.ForLLM)
	if !strings.Contains(lower, "blocked") && !strings.Contains(lower, "unavailable") {
		t.Fatalf("named-provider DuckDuckGo network failure must carry the blocked-region note, got: %s", res.ForLLM)
	}
	for _, region := range []string{"north korea", "indonesia", "mainland china"} {
		if !strings.Contains(lower, region) {
			t.Fatalf("blocked-region note must name %q, got: %s", region, res.ForLLM)
		}
	}
}

// TestFixF1_DuckDuckGoNonNetworkFailureOmitsBlockedRegionNote: founder
// decision D-B (plan.md) scopes the note to explain a network/region block
// specifically — "the note explains a network/region block to the agent; it
// must not appear for other failures". A DuckDuckGo failure that is NOT
// network-class must not carry it. DuckDuckGoSearchProvider.SearchWithCaps
// (pkg/tools/web_search.go) classifies any non-200 HTTP response as
// classBadResponse — never classNetwork, regardless of the status code
// (auth-shaped 401 included) — so a swapped-in 401 response is a
// deterministic, reproducible non-network-class failure, exercising exactly
// the same no-fallback-configured / failureLine path the network-class
// tests above use (only the failure class differs). Wrongly showing the
// geo-block note here would mislead the agent into diagnosing a config/auth
// problem as regional blocking.
func TestFixF1_DuckDuckGoNonNetworkFailureOmitsBlockedRegionNote(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderDuckDuckGo
		c.FallbackProvider = config.SearchProviderNone
	}, nil)
	f.setHandler("ddg", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // non-network class: classBadResponse, never classNetwork
	})

	res := f.run(map[string]any{"query": "test"})
	if !res.IsError {
		t.Fatalf("expected the DuckDuckGo non-200 response to end the call (no fallback configured), "+
			"got ForLLM=%q ForUser=%q", res.ForLLM, res.ForUser)
	}

	assertNoBlockedRegionNote(t, res, "a non-network (HTTP status) DuckDuckGo failure")
}

// TestFixF1_DuckDuckGoSuccessOmitsBlockedRegionNote: a successful DuckDuckGo
// search must never carry the region-block note either — D-B/#1056 F-1 scope
// the note to a specific failure, not a standing DuckDuckGo disclaimer that
// would appear on every call regardless of outcome.
func TestFixF1_DuckDuckGoSuccessOmitsBlockedRegionNote(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderDuckDuckGo
		c.FallbackProvider = config.SearchProviderNone
	}, nil) // canonical DDG fixture handler already returns a well-formed, successful result

	res := f.run(map[string]any{"query": "test"})
	if res.IsError {
		t.Fatalf("expected the canonical DuckDuckGo fixture handler to succeed, got: %s", res.ForLLM)
	}

	assertNoBlockedRegionNote(t, res, "a successful DuckDuckGo search")
}

// assertNoBlockedRegionNote asserts neither ForLLM nor ForUser carries the
// F-1 blocked-region note's "blocked" wording or any of its three named
// regions (issue #1056 F-1's own citations: North Korea, Indonesia,
// mainland China).
func assertNoBlockedRegionNote(t *testing.T, res *ToolResult, scenario string) {
	t.Helper()
	for _, field := range []struct {
		name string
		text string
	}{{"ForLLM", res.ForLLM}, {"ForUser", res.ForUser}} {
		lower := strings.ToLower(field.text)
		if strings.Contains(lower, "blocked") {
			t.Fatalf("%s must not carry the blocked-region note wording in %s, got: %s", scenario, field.name, field.text)
		}
		for _, region := range []string{"north korea", "indonesia", "mainland china"} {
			if strings.Contains(lower, region) {
				t.Fatalf("%s must not name %q in %s (the note is network-class only, D-B/plan.md), got: %s",
					scenario, region, field.name, field.text)
			}
		}
	}
}
