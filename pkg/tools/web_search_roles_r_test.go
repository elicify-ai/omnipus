package tools

// web_search_roles_r_test.go — the Resolution table (spec "Resolution",
// lines 220–265) and US-2 naming. Expected values derive from the R-table
// rows and the spec's result-text blocks, never from observed output.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// R1: default usable + fallback "none" -> the default runs, nobody else.
// US-1 acceptance 2.
func TestR1_ExplicitNone_DefaultUsable_CallsDefaultOnly(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderNone
	}, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: tavily (default)") {
		t.Fatalf("expected tavily (default), got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo called %d times, want 0", h)
	}
}

// R2: fallback "none", the default fails -> one error naming the default
// only. US-1 acceptance 3.
func TestR2_ExplicitNone_DefaultNetworkFail_NamesDefaultOnly(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderNone
	}, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.HasPrefix(res.ForLLM, "search failed\n- tavily (default): network:") {
		t.Fatalf("expected default-only failure line, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo called %d times, want 0", h)
	}
}

// R3/R6 hop: fallback absent, DuckDuckGo usable, default != DuckDuckGo ->
// auto DuckDuckGo fallback. Default fails network -> the fallback answers,
// the note names the default and its class. US-2 acceptance 1.
func TestR3_DefaultFails_AutoFallbackAnswers(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: duckduckgo (fallback)") {
		t.Fatalf("expected duckduckgo (fallback), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Note: tavily (default) failed and was not used for these results. tavily: network:") {
		t.Fatalf("expected hop note naming tavily, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 1 {
		t.Fatalf("duckduckgo hits = %d, want 1", h)
	}
}

// R1/R3: the same canonical config with a healthy default -> no hop.
func TestR3_DefaultSucceeds_FallbackNotCalled(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: tavily (default)") {
		t.Fatalf("expected tavily (default), got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// R4: explicit usable fallback (brave) -> the hop goes to brave, not to the
// auto DuckDuckGo.
func TestR4_ExplicitUsableFallbackHops(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: brave (fallback)") {
		t.Fatalf("expected brave (fallback), got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// R4b: the fallback names a known but currently unusable provider -> it is
// listed as "not called" with its reason. Reason mapping (lane decision,
// vocabulary from spec line ~397): enabled=false -> "switched off";
// enabled=true with an empty key -> "no API key".
func TestR4b_FallbackKnownButUnusable_ListedAsNotCalled(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderExa
		c.Exa = config.ExaConfig{Enabled: false, APIKeyRef: envRefMissingBrav}
	}, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- tavily (default): network:") {
		t.Fatalf("expected tavily failure line, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- exa (fallback): not called: switched off") {
		t.Fatalf("expected exa not-called line (switched off), got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// R5: fallback == default -> that provider runs at most once. US-1
// acceptance 4.
func TestR5_FallbackSameAsDefault_CalledAtMostOnce(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.FallbackProvider = config.SearchProviderTavily
	}, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
	})
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if n := strings.Count(res.ForLLM, "\n- "); n != 1 {
		t.Fatalf("expected exactly 1 failure line, got %d:\n%s", n, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- tavily (default): network:") {
		t.Fatalf("expected tavily line, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// R6 flagship (US-1.5): default unusable (no key), the operator's explicit
// DuckDuckGo fallback usable -> DuckDuckGo runs and the note says the
// default was not called because it has no API key. (K6, gate round 1: the
// fallback is now EXPLICIT — with an absent fallback, R3 requires a usable
// default, so an unusable default + absent fallback is R7 nobody-runs, per
// the spec's R-table. US-1.5's "resolved fallback is a usable DuckDuckGo"
// is the explicit R4 pick this test now configures.)
func TestR6_DefaultUnusable_ExplicitFallbackRuns(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.Tavily.APIKeyRef = envRefMissingTav
		c.FallbackProvider = config.SearchProviderDuckDuckGo
	}, func(o *WebSearchToolOptions) {
		o.TavilyAPIKeys = nil
	})
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: duckduckgo (fallback)") {
		t.Fatalf("expected duckduckgo (fallback), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Note: tavily (default) was not called: no API key.") {
		t.Fatalf("expected not-called note, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("tavily"); h != 0 {
		t.Fatalf("tavily hits = %d, want 0", h)
	}
}

// R7: both roles unusable -> nobody is called; both are named with their
// reasons; DuckDuckGo is NOT called and NOT named (it is not the fallback).
func TestR7_BothRolesUnusable_NobodyCalled(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.Tavily.APIKeyRef = envRefMissingTav
		c.FallbackProvider = config.SearchProviderExa
		c.Exa = config.ExaConfig{Enabled: false, APIKeyRef: envRefMissingBrav}
	}, func(o *WebSearchToolOptions) {
		o.TavilyAPIKeys = nil
	})
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- tavily (default): not called: no API key") {
		t.Fatalf("expected tavily not-called line, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- exa (fallback): not called: switched off") {
		t.Fatalf("expected exa not-called line, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
	if strings.Contains(res.ForLLM, "duckduckgo") {
		t.Fatalf("duckduckgo must not be named when it is not a role:\n%s", res.ForLLM)
	}
}

// R8: fallback absent, DuckDuckGo disabled -> the default is used, no other
// provider is invented. US-1.3 shape with tavily as the default.
func TestR8_NoFallbackInvented_WhenDDGDisabled(t *testing.T) {
	dead := deadServerURL(t)
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DuckDuckGo.Enabled = false
	}, func(o *WebSearchToolOptions) {
		o.TavilyBaseURL = dead
		o.DuckDuckGoEnabled = false
	})
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- tavily (default): network:") {
		t.Fatalf("expected tavily-only failure, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// R9: an unknown default id is treated as not usable; with the operator's
// explicit DuckDuckGo fallback, R6 runs it and the error names the unknown
// id. (K6, gate round 1: with an ABSENT fallback the auto-DDG arm requires
// a usable default — R3's third conjunct — so the absent-fallback form of
// this scenario is R7 nobody-runs; R9's "the error names the unknown id"
// needs a fallback that actually resolves.)
func TestR9_UnknownDefault_ExplicitFallbackRuns_NamesUnknownID(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = "not-a-provider"
		c.FallbackProvider = config.SearchProviderDuckDuckGo
	}, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: duckduckgo (fallback)") {
		t.Fatalf("expected duckduckgo (fallback), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "was not called: unknown id") {
		t.Fatalf("expected unknown-id note, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 1 {
		t.Fatalf("duckduckgo hits = %d, want 1", h)
	}
}

// Empty is final: HTTP 200 + zero results -> success naming the provider, no
// hop. US-2 acceptance 3.
func TestEmptyIsFinal_NoHop(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", jsonBody(`{"results":[]}`))
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tavily") {
		t.Fatalf("expected tavily named, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}
