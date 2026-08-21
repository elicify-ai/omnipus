package routing

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveRoute_MiaIsDefaultAfterSeed verifies that after seeding core agents
// via coreagent.SeedConfig, the route resolver picks Mia as the default agent.
//
// BDD: Given a config seeded with all 4 base core agents (Mia has Default=true),
//
//	When ResolveRoute is called with no bindings,
//	Then the resolved agent is "mia" (matched via Default=true).
//
// Note: this test constructs the config directly rather than calling coreagent.SeedConfig.
// pkg/routing is a low-level config-level resolver with no agent-lifecycle dependency;
// importing pkg/coreagent here would pull in all seed logic (prompts, SOUL.md writes,
// etc.) for what is an isolated unit test of the resolver. The end-to-end seed path
// (coreagent.SeedConfig → Default=true on mia → resolver returns "mia") is covered in
// pkg/coreagent (TestSeedConfig_MiaIsDefault). Here we test the resolver logic only.
func TestResolveRoute_MiaIsDefault(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "jim"},
		{ID: "ava"},
	}
	cfg := testConfig(agents, nil)
	// The per-entity Default flag is NOT consulted by either default-agent
	// resolver (ADR-054 D6.4) — the singleton is. This test passed before only
	// because "mia" happened to be FIRST in slice order, which the last-resort
	// rung used to honour; it now sorts, so "ava" would win. Setting the
	// singleton makes the test assert the rule it always claimed to.
	cfg.Agents.Defaults.DefaultAgentID = "mia"
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (mia is marked default)", route.AgentID, "mia")
	}
	if route.MatchedBy != "default" {
		t.Errorf("MatchedBy = %q, want 'default'", route.MatchedBy)
	}
}

// TestResolveRoute_ChannelBindingOverridesDefault verifies that a channel-wildcard
// binding takes priority over the global default agent.
//
// BDD: Given mia is Default=true, and a channel-wildcard binding telegram → "jim",
//
//	When ResolveRoute for channel=telegram with no peer/guild/team,
//	Then the resolved agent is "jim" (binding.channel takes priority).
func TestResolveRoute_ChannelBindingOverridesDefault(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "jim"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "jim",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	// Same as the sibling default tests: the per-entity Default flag does not
	// drive resolution (ADR-054 D6.4) — the singleton does. Without it "jim"
	// would win on the sorted last-resort rung.
	cfg.Agents.Defaults.DefaultAgentID = "mia"
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user1"},
	})
	if route.AgentID != "jim" {
		t.Errorf("AgentID = %q, want 'jim' (channel binding overrides global default)", route.AgentID)
	}
	if route.MatchedBy != "binding.channel" {
		t.Errorf("MatchedBy = %q, want 'binding.channel'", route.MatchedBy)
	}
}

// TestResolveRoute_NoDefaultFallsToFirstAgent verifies that when no agent
// has Default=true, the resolver picks the first chat-target agent. The old
// "main" sentinel constant (routing.DefaultAgentID) is deleted entirely — there
// is no longer a hardcoded name it could fall back to instead.
//
// BDD: Given agents alpha and beta, neither is default,
//
//	When ResolveRoute is called,
//	Then the resolved agent is "alpha" (first in list),
//	And it is NOT the literal "main" (the retired sentinel's name).
func TestResolveRoute_NoDefaultFallsToFirstAgent(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta"},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "cli"})
	if route.AgentID != "alpha" {
		t.Errorf("AgentID = %q, want 'alpha' (first agent when no default set)", route.AgentID)
	}
	if route.AgentID == "main" {
		t.Errorf(
			"AgentID = %q — fallback must be the first available agent, not the retired 'main' sentinel",
			route.AgentID,
		)
	}
}

// TestResolveRoute_ChannelBindingWinsWithNoGlobalDefault verifies that a channel binding
// resolves to the bound agent even when NO global default agent is set (Default=false for all).
//
// BDD: Given no agent has Default=true, and a binding telegram → "jim",
//
//	When ResolveRoute for channel=telegram,
//	Then the resolved agent is "jim" (channel binding takes precedence over fallback).
//
// Traces to: sprint/258-jun-2026 — routing precedence, channel binding over no-default.
func TestResolveRoute_ChannelBindingWinsWithNoGlobalDefault(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia"}, // no Default=true
		{ID: "jim"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "jim",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user1"},
	})
	if route.AgentID != "jim" {
		t.Errorf("AgentID = %q, want 'jim' (channel binding must win even without a global default)", route.AgentID)
	}
	if route.MatchedBy != "binding.channel" {
		t.Errorf("MatchedBy = %q, want 'binding.channel'", route.MatchedBy)
	}
}

// TestResolveRoute_NonExistentAgentInBindingFallsToDefault verifies that when a
// channel binding references an agent ID that does not exist in the agent list,
// pickAgentID logs a warning and falls back to resolveDefaultAgentID.
//
// BDD: Given a binding telegram → "ghost-agent" (non-existent), and "mia" is Default=true,
//
//	When ResolveRoute for channel=telegram,
//	Then the resolved agent is "mia" (non-existent binding → fallback to default).
//
// Traces to: sprint/258-jun-2026 — routing fallback, non-existent agent in binding.
func TestResolveRoute_NonExistentAgentInBindingFallsToDefault(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "jim"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "ghost-agent", // does not exist in agents list
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	// The per-entity Default flag does not drive resolution (ADR-054 D6.4)
	// — the singleton does. Without it "jim" would win on the sorted
	// last-resort rung, which is what this test used to rely on.
	cfg.Agents.Defaults.DefaultAgentID = "mia"
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user1"},
	})
	// pickAgentID should warn and fall back to the default agent (mia).
	if route.AgentID != "mia" {
		t.Errorf(
			"AgentID = %q, want 'mia' — binding references non-existent 'ghost-agent', "+
				"must fall back to default agent via pickAgentID → resolveDefaultAgentID",
			route.AgentID)
	}
}

// TestResolveDefaultAgentID_EmptyAgentList directly pins
// route.go::resolveDefaultAgentID's first branch: an empty cfg.Agents.List
// has no sentinel left to invent, so it must return "" — never a hardcoded
// agent name (the retired "main" sentinel included). Called directly (not
// via ResolveRoute) since resolveDefaultAgentID is unexported but this test
// file lives in the same package.
func TestResolveDefaultAgentID_EmptyAgentList(t *testing.T) {
	cfg := testConfig(nil, nil)
	r := NewRouteResolver(cfg)

	got := r.resolveDefaultAgentID()
	if got != "" {
		t.Errorf("resolveDefaultAgentID() = %q, want empty string for an empty agent list", got)
	}
}

// TestResolveDefaultAgentID_NoChatTargetAgent directly pins
// route.go::resolveDefaultAgentID's second branch: a non-empty agent list
// containing ONLY workers (no chat-target agent) also has nothing to fall
// back to and must return "".
func TestResolveDefaultAgentID_NoChatTargetAgent(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "w1", Type: config.AgentTypeWorker},
		{ID: "w2", Type: config.AgentTypeWorker},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	got := r.resolveDefaultAgentID()
	if got != "" {
		t.Errorf("resolveDefaultAgentID() = %q, want empty string when no agent is a chat target", got)
	}
}

// TestResolveDefaultAgentID_Differentiation is an anti-shortcut check: two
// different configs (different override, different first chat-target agent)
// must produce two different resolved IDs. This catches a resolveDefaultAgentID
// that was hollowed out to always return the same hardcoded string.
func TestResolveDefaultAgentID_Differentiation(t *testing.T) {
	cfgA := testConfig([]config.AgentConfig{{ID: "alpha"}, {ID: "beta"}}, nil)
	cfgA.Agents.Defaults.DefaultAgentID = "beta"
	gotA := NewRouteResolver(cfgA).resolveDefaultAgentID()
	if gotA != "beta" {
		t.Fatalf("resolveDefaultAgentID() = %q, want 'beta' (configured override)", gotA)
	}

	// No override: the last resort is the lexicographically-FIRST chat target,
	// so "delta" wins over "gamma" despite being listed second. Sorting (rather
	// than slice order) is what makes this resolver agree with
	// agent.AgentRegistry.GetDefaultAgent — see ADR-064 §7.
	cfgB := testConfig([]config.AgentConfig{{ID: "gamma"}, {ID: "delta"}}, nil)
	gotB := NewRouteResolver(cfgB).resolveDefaultAgentID()
	if gotB != "delta" {
		t.Fatalf("resolveDefaultAgentID() = %q, want 'delta' (lexicographically first chat-target, no override)", gotB)
	}

	if gotA == gotB {
		t.Fatalf("two different configs both resolved to %q — resolveDefaultAgentID may be hardcoded", gotA)
	}
}
