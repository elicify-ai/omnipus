package routing

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestResolveRoute_MiaIsDefaultAfterSeed verifies that after seeding core agents
// via coreagent.SeedConfig, the route resolver picks Mia as the default agent.
//
// BDD: Given a config seeded with all 5 core agents (Mia has Default=true),
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
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Default: true, Enabled: &enabled},
		{ID: "jim", Enabled: &enabled},
		{ID: "ava", Enabled: &enabled},
	}
	cfg := testConfig(agents, nil)
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
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Default: true, Enabled: &enabled},
		{ID: "jim", Enabled: &enabled},
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
		t.Errorf("AgentID = %q, want 'jim' (channel binding overrides global default)", route.AgentID)
	}
	if route.MatchedBy != "binding.channel" {
		t.Errorf("MatchedBy = %q, want 'binding.channel'", route.MatchedBy)
	}
}

// TestResolveRoute_NoDefaultFallsToFirstEnabledAgent verifies that when no agent
// has Default=true, the resolver picks the first enabled agent instead of
// the DefaultAgentID constant ("main").
//
// BDD: Given agents alpha (enabled) and beta (enabled), neither is default,
//
//	When ResolveRoute is called,
//	Then the resolved agent is "alpha" (first enabled),
//	And the old "main" constant is NOT returned.
func TestResolveRoute_NoDefaultFallsToFirstEnabledAgent(t *testing.T) {
	enabled := true
	agents := []config.AgentConfig{
		{ID: "alpha", Enabled: &enabled},
		{ID: "beta", Enabled: &enabled},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "cli"})
	if route.AgentID != "alpha" {
		t.Errorf("AgentID = %q, want 'alpha' (first enabled agent when no default set)", route.AgentID)
	}
	if route.AgentID == DefaultAgentID {
		t.Errorf("AgentID must NOT be %q — fallback must be the first enabled agent, not the constant", DefaultAgentID)
	}
}

// TestResolveRoute_AllDisabledFallsToDefaultConstant verifies that when all
// agents are disabled, the resolver falls back to the DefaultAgentID constant.
//
// BDD: Given agents alpha and beta, both explicitly disabled,
//
//	When ResolveRoute is called,
//	Then the resolved agent is DefaultAgentID ("main") as a last resort.
func TestResolveRoute_AllDisabledFallsToDefaultConstant(t *testing.T) {
	disabled := false
	agents := []config.AgentConfig{
		{ID: "alpha", Enabled: &disabled},
		{ID: "beta", Enabled: &disabled},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "cli"})
	if route.AgentID != DefaultAgentID {
		t.Errorf("AgentID = %q, want %q (all-disabled must fall back to DefaultAgentID)", route.AgentID, DefaultAgentID)
	}
}

// TestResolveRoute_DisabledDefaultSkipsToFirstEnabled verifies that when the
// agent marked Default=true is explicitly disabled, the resolver falls back to
// the first enabled agent rather than returning the disabled default or the
// DefaultAgentID constant.
//
// BDD: Given "mia" is Default=true but Enabled=false, and "jim" is Enabled=true,
//
//	When ResolveRoute is called,
//	Then the resolved agent is "jim" (first enabled non-default),
//	And NOT "mia" (disabled default must be skipped).
//
// Traces to: sprint/258-jun-2026 — routing fallback picks first ENABLED agent.
//
// Previously: PRODUCT BUG in resolveDefaultAgentID (route.go:254) returned a disabled
// agent when it had Default=true because the loop only checked `a.Default` and NOT
// `a.IsActive()`. Fixed by backend-lead: resolveDefaultAgentID now skips disabled
// agents in the Default=true pass and falls through to the first-enabled-agent fallback.
// This test was written to catch the bug and now confirms the fix is stable. Keep it.
func TestResolveRoute_DisabledDefaultSkipsToFirstEnabled(t *testing.T) {
	disabled := false
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Default: true, Enabled: &disabled},
		{ID: "jim", Enabled: &enabled},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID == "mia" {
		t.Errorf(
			"PRODUCT BUG: AgentID = %q (disabled default was returned) — "+
				"resolveDefaultAgentID must skip disabled agents even when Default=true. "+
				"Fix: add a.IsActive() check in pkg/routing/route.go:resolveDefaultAgentID.",
			route.AgentID)
	}
	// "jim" is the only enabled agent — must be selected.
	if route.AgentID != "jim" {
		t.Errorf("AgentID = %q, want 'jim' (only enabled agent when default is disabled)", route.AgentID)
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
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Enabled: &enabled}, // no Default=true
		{ID: "jim", Enabled: &enabled},
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
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Default: true, Enabled: &enabled},
		{ID: "jim", Enabled: &enabled},
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
