package tools

// web_search_fix_fallback_message_test.go — RED pack for issue #1056 F-3
// ("naming a provider disables the fallback" — the message-honesty half).
//
// Oracle: ADR-096 D6/AC-5. D6's rule table (pkg/tools/web_search.go::
// executeChosen) already implements THREE distinct reasons a named
// provider's hop does not happen:
//   (a) the call used that provider's own capability (D6 row 1: "Hard fail.
//       No hop. A substitute cannot deliver what was asked for")
//   (b) the call was plain, but no fallback is configured at all (fallback
//       role resolves to "" — R2/absent-with-no-auto)
//   (c) the call was plain, a fallback IS configured and eligible, so it
//       hops per D6 row 2 ("Hop, with D16's honest report") — this is the
//       CORRECT, already-shipped behaviour; nothing to fix here.
//
// The bug: today's code (executeChosen, the "Hard fail: capability use, no
// fallback, or final class" branch) renders ONE byte-identical tail for (a)
// and (b):
//
//	"This call named %s, so the fallback was not tried. Omit provider to use
//	the default and the fallback, or set provider to one of: %s."
//
// That sentence is actively false for (b): it reads "the fallback was not
// tried" as if a fallback exists and was skipped, when in fact none is
// configured. It is imprecise for (a): it never says the reason was the
// agent's own capability request. AC-5 requires provider-fault honesty
// generally (D16's whole point); D6's row-by-row table is the spec source
// for what GREEN's message must distinguish. Exact wording is GREEN's
// choice — these tests assert content, not a fixed string, per the elicify-
// test-writing rule against over-pinning: (a) must name the capability
// reason, (b) must plainly say no fallback is configured and must NOT use
// the misleading "fallback was not tried" phrasing that implies a fallback
// exists, and the two messages must not be textually identical in their
// reason clause.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestFixF3_CapabilityHardFailMessageNamesCapabilityReason: a named provider
// that fails after using its own capability (include_domains on Perplexity,
// per D6 row 1) with a fallback CONFIGURED AND ELIGIBLE (duckduckgo) must
// still hard-fail (D6 forbids hopping a capability use) — but the message
// must say the fallback was skipped BECAUSE the call used a provider-
// specific capability, not the generic "was not tried" wording that gives
// no reason at all.
func TestFixF3_CapabilityHardFailMessageNamesCapabilityReason(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderTavily
		c.FallbackProvider = config.SearchProviderDuckDuckGo // configured AND eligible
		c.Perplexity = config.PerplexityConfig{Enabled: true, APIKeyRef: envRefPerplexity}
	}, nil)
	f.setHandler("perplexity", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // upstream: hop-class
	})
	res := f.run(map[string]any{
		"query":           "golang",
		"provider":        "perplexity",
		"include_domains": []any{"example.com"},
	})
	if !res.IsError {
		t.Fatalf("D6 row 1: a capability use must hard-fail, not hop, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "(fallback)") {
		t.Fatalf("a capability use must never hop even with an eligible fallback configured, got: %s", res.ForLLM)
	}
	lower := strings.ToLower(res.ForLLM)
	if !strings.Contains(lower, "capability") {
		t.Fatalf("message must name the capability reason the fallback was skipped (AC-5/D6), got: %s", res.ForLLM)
	}
	if strings.Contains(lower, "no fallback is configured") || strings.Contains(lower, "no fallback configured") {
		t.Fatalf("a capability hard-fail must not claim no fallback is configured — one is, and it is eligible: %s", res.ForLLM)
	}
}

// TestFixF3_NoFallbackConfiguredMessagePlainlySaysSo: a named provider that
// fails on a PLAIN query (no capability) with NO fallback configured at all
// (fallback_provider: "none") must say plainly that no fallback is
// configured — not the generic "so the fallback was not tried" wording
// that implies a fallback exists but was skipped. This is today's message
// verbatim (pkg/tools/web_search.go::executeChosen's hard-fail tail), which
// this test proves is wrong for this case.
func TestFixF3_NoFallbackConfiguredMessagePlainlySaysSo(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderTavily
		c.FallbackProvider = config.SearchProviderNone // no fallback at all
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	f.setHandler("brave", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // upstream: hop-class
	})
	res := f.run(map[string]any{"query": "golang", "provider": "brave"}) // plain query, no capability
	if !res.IsError {
		t.Fatalf("expected the named-provider call to fail with no fallback to hop to, got: %s", res.ForLLM)
	}
	lower := strings.ToLower(res.ForLLM)
	if strings.Contains(lower, "so the fallback was not tried") {
		t.Fatalf(
			"with fallback_provider=none, \"the fallback was not tried\" falsely implies a fallback exists — "+
				"the message must plainly say none is configured, got: %s", res.ForLLM)
	}
	if !strings.Contains(lower, "no fallback") || !strings.Contains(lower, "configured") {
		t.Fatalf("message must plainly state that no fallback is configured (AC-5/D6), got: %s", res.ForLLM)
	}
}

// TestFixF3_ConfiguredEligibleFallbackHopsWithNoHardFailTail pins the
// CORRECT, already-shipped case (c): a plain named-provider call with a
// fallback that IS configured and eligible hops (D6 row 2) — it must not
// produce any hard-fail tail message at all. This test is expected to be
// GREEN against today's code (pkg/tools/web_search.go::executeChosen's
// `entries.usable[entries.fallbackID]` hop branch already does this); it is
// included as a locked characterization of the message-honesty angle the
// spec (D6/AC-5) requires, not as a new failing RED test. Flagged per the
// qa-lead brief: this is a "lock the correct behaviour" test, not a red one.
func TestFixF3_ConfiguredEligibleFallbackHopsWithNoHardFailTail(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderTavily
		c.FallbackProvider = config.SearchProviderDuckDuckGo // configured AND eligible
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	f.setHandler("brave", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // upstream: hop-class
	})
	res := f.run(map[string]any{"query": "golang", "provider": "brave"}) // plain query, no capability
	if res.IsError {
		t.Fatalf("D6 row 2: a plain named-provider failure with an eligible fallback must hop, got error: %s", res.ForLLM)
	}
	lower := strings.ToLower(res.ForLLM)
	if strings.Contains(lower, "so the fallback was not tried") || strings.Contains(lower, "omit provider to use the default") {
		t.Fatalf("a successful hop must not carry any hard-fail tail text, got: %s", res.ForLLM)
	}
	if !strings.Contains(lower, "duckduckgo") {
		t.Fatalf("a successful hop must name the fallback that answered, got: %s", res.ForLLM)
	}
}
