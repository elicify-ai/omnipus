package routing

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveRoute_SystemAgentExcludedFromDefaultFallback verifies ADR-049 D3 /
// FR-078: a type:system agent (the Judge) is IsChatTarget()==false, so it is
// never the resolved default — neither via the first-enabled fallback nor even
// when it is (via tampered config) marked Default=true.
func TestResolveRoute_SystemAgentExcludedFromDefaultFallback(t *testing.T) {
	t.Run("first-enabled fallback skips the System Agent", func(t *testing.T) {
		// No agent marked Default; the Judge sorts first but must be skipped.
		agents := []config.AgentConfig{
			{ID: "judge", Type: config.AgentTypeSystem, Locked: true},
			{ID: "mia", Type: config.AgentTypeCore},
		}
		cfg := testConfig(agents, nil)
		route := NewRouteResolver(cfg).ResolveRoute(RouteInput{Channel: "telegram"})
		if route.AgentID != "mia" {
			t.Errorf("AgentID = %q, want %q (Judge must be skipped as a non-chat-target)", route.AgentID, "mia")
		}
	})

	t.Run("a Default-flagged System Agent is still not resolved", func(t *testing.T) {
		// Tamper: the Judge is (illegally) marked Default; resolveDefaultAgentID
		// gates on IsChatTarget so it still falls through to a real chat target.
		agents := []config.AgentConfig{
			{ID: "judge", Type: config.AgentTypeSystem, Default: true, Locked: true},
			{ID: "mia", Type: config.AgentTypeCore},
		}
		cfg := testConfig(agents, nil)
		route := NewRouteResolver(cfg).ResolveRoute(RouteInput{Channel: "telegram"})
		if route.AgentID == "judge" {
			t.Errorf("AgentID = %q; a System Agent must never be resolved as the default", route.AgentID)
		}
		if route.AgentID != "mia" {
			t.Errorf("AgentID = %q, want %q", route.AgentID, "mia")
		}
	})
}

// TestResolveRoute_BindingToSystemAgentFallsBack verifies pickAgentID degrades a
// binding that resolves to a System Agent back to the default (IsChatTarget()==
// false), exactly as it does for a worker.
func TestResolveRoute_BindingToSystemAgentFallsBack(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCore, Default: true},
		{ID: "judge", Type: config.AgentTypeSystem, Locked: true},
	}
	// A channel-wildcard binding pointing at the Judge.
	bindings := []config.AgentBinding{
		{AgentID: "judge", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	route := NewRouteResolver(cfg).ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (binding to a System Agent must fall back to default)", route.AgentID, "mia")
	}
}
