package routing

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveRoute_IdentityAgent_OverridesBindings covers Spec-2 US-5 point 1:
// an inbound message on a channel instance whose identity is {kind:agent,id:X}
// is routed AS agent X, overriding every binding (including a peer binding to a
// different agent).
func TestResolveRoute_IdentityAgent_OverridesBindings(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "sales", Default: true},
		{ID: "support"},
		{ID: "concierge"},
	}
	// A peer binding to "support" would win normally — identity must beat it.
	bindings := []config.AgentBinding{
		{
			AgentID: "support",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
				Peer:      &config.PeerMatch{Kind: "direct", ID: "user123"},
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		InstanceID: "telegram",
		Peer:       &RoutePeer{Kind: "direct", ID: "user123"},
		Identity:   &config.ChannelIdentity{Kind: "agent", ID: "concierge"},
	})

	if route.AgentID != "concierge" {
		t.Errorf("AgentID = %q, want 'concierge' (identity must override the peer binding)", route.AgentID)
	}
	if route.MatchedBy != "identity.agent" {
		t.Errorf("MatchedBy = %q, want 'identity.agent'", route.MatchedBy)
	}
}

// TestResolveRoute_IdentityUser_FallsThroughToDefault covers Spec-2 US-5 point 2:
// a {kind:user} identity does NOT short-circuit — the message is attributed to
// the user and routed via the normal cascade, ending at the default agent when
// no binding matches.
func TestResolveRoute_IdentityUser_FallsThroughToDefault(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "jim"},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		InstanceID: "telegram",
		Peer:       &RoutePeer{Kind: "direct", ID: "user1"},
		Identity:   &config.ChannelIdentity{Kind: "user"},
	})

	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want 'mia' (user identity routes via default)", route.AgentID)
	}
	if route.MatchedBy != "default" {
		t.Errorf("MatchedBy = %q, want 'default'", route.MatchedBy)
	}
}

// TestResolveRoute_IdentityUser_StillHonorsBindings confirms a user-kind identity
// leaves the binding cascade intact — a matching peer binding still wins.
func TestResolveRoute_IdentityUser_StillHonorsBindings(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "support"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "support",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
				Peer:      &config.PeerMatch{Kind: "direct", ID: "vip"},
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		InstanceID: "telegram",
		Peer:       &RoutePeer{Kind: "direct", ID: "vip"},
		Identity:   &config.ChannelIdentity{Kind: "user"},
	})

	if route.AgentID != "support" {
		t.Errorf("AgentID = %q, want 'support' (binding wins under user identity)", route.AgentID)
	}
	if route.MatchedBy != "binding.peer" {
		t.Errorf("MatchedBy = %q, want 'binding.peer'", route.MatchedBy)
	}
}

// TestResolveRoute_NilIdentity_NoChange guards backward compatibility: a nil
// Identity leaves routing exactly as before (default when no binding matches).
func TestResolveRoute_NilIdentity_NoChange(t *testing.T) {
	agents := []config.AgentConfig{{ID: "mia", Default: true}}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		InstanceID: "telegram",
		Peer:       &RoutePeer{Kind: "direct", ID: "u"},
		Identity:   nil,
	})

	if route.MatchedBy != "default" {
		t.Errorf("MatchedBy = %q, want 'default' (nil identity must not change routing)", route.MatchedBy)
	}
}

// TestResolveRoute_IdentityAgent_UnknownFallsBackToDefault confirms that an
// identity pointing at a non-existent agent does not drop the message — pickAgentID
// falls back to the default agent (and logs a warning).
func TestResolveRoute_IdentityAgent_UnknownFallsBackToDefault(t *testing.T) {
	agents := []config.AgentConfig{{ID: "mia", Default: true}}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		InstanceID: "telegram",
		Identity:   &config.ChannelIdentity{Kind: "agent", ID: "ghost"},
	})

	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want 'mia' (unknown identity agent falls back to default)", route.AgentID)
	}
	// MatchedBy still reports identity.agent — the route was selected by identity,
	// even though the target resolved to the default.
	if route.MatchedBy != "identity.agent" {
		t.Errorf("MatchedBy = %q, want 'identity.agent'", route.MatchedBy)
	}
}

// TestResolveRoute_IdentityAgent_EmptyIDIgnored confirms an agent-kind identity
// with an empty ID is treated as "no identity" and falls through the cascade.
func TestResolveRoute_IdentityAgent_EmptyIDIgnored(t *testing.T) {
	agents := []config.AgentConfig{{ID: "mia", Default: true}}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		InstanceID: "telegram",
		Identity:   &config.ChannelIdentity{Kind: "agent", ID: "  "},
	})

	if route.MatchedBy != "default" {
		t.Errorf("MatchedBy = %q, want 'default' (agent identity with empty id is ignored)", route.MatchedBy)
	}
}
