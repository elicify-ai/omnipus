package routing

// bound_instance_test.go — ADR-029 WS-A routing tests for bound channel instances.
// Covers TDD plan items #5, #6, #7, #8, DS-3 drift states, and regression proofs.

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// testConfigWithChannels builds a *config.Config with agents, bindings, and a
// channels map, all of which are used for bound-instance routing tests.
func testConfigWithChannels(
	agents []config.AgentConfig,
	bindings []config.AgentBinding,
	channels map[string]config.ChannelInstanceConfig,
) *config.Config {
	cfg := testConfig(agents, bindings)
	cfg.Channels = channels
	return cfg
}

// ── TDD #5: additive field zero-value safety ─────────────────────────────────

// TestRouteInput_BoundInstance_ZeroValue confirms the BoundInstance field
// defaults to false so existing call sites are unaffected (no silent behavior
// change on existing routes).
func TestRouteInput_BoundInstance_ZeroValue(t *testing.T) {
	var ri RouteInput
	if ri.BoundInstance {
		t.Error("BoundInstance default must be false (zero-value); existing call sites must not be affected")
	}
}

// TestResolvedRoute_DropField_ZeroValue confirms the Drop field defaults to
// false so existing routes never accidentally drop.
func TestResolvedRoute_DropField_ZeroValue(t *testing.T) {
	var rr ResolvedRoute
	if rr.Drop {
		t.Error("Drop default must be false (zero-value); existing routes must not accidentally drop")
	}
}

// ── TDD #6: bound instance routes to member agent ────────────────────────────

// TestResolveRoute_BoundInstance_RoutesToMember verifies US-4 AC-1:
// an ordinary inbound on a workspace-bound instance routes to the configured
// member agent via Priority-0 identity (MatchedBy=="identity.agent").
func TestResolveRoute_BoundInstance_RoutesToMember(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	}
	channels := map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
	}
	cfg := testConfigWithChannels(agents, nil, channels)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "whatsapp",
		InstanceID:    "whatsapp.eu",
		Identity:      &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		BoundInstance: true,
	})

	if route.Drop {
		t.Fatalf("route.Drop is true for a valid bound agent; must not drop")
	}
	if route.AgentID != "ray" {
		t.Errorf("AgentID = %q, want %q", route.AgentID, "ray")
	}
	if route.MatchedBy != "identity.agent" {
		t.Errorf("MatchedBy = %q, want %q", route.MatchedBy, "identity.agent")
	}
}

// ── TDD #7: bound-drift drops for DS-3 states 2,3,4 ─────────────────────────

// TestResolveRoute_BoundDrift_DeletedAgent covers DS-3 row 2: the bound agent
// does not exist in the agent list (deleted) → Drop=true, GetDefaultAgent never used.
func TestResolveRoute_BoundDrift_DeletedAgent(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		// "ray" is intentionally absent (deleted)
	}
	cfg := testConfigWithChannels(agents, nil, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "whatsapp",
		InstanceID:    "whatsapp.eu",
		Identity:      &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		BoundInstance: true,
	})

	if !route.Drop {
		t.Fatalf("route.Drop is false for a deleted bound agent; must be true (drift drop)")
	}
	if route.MatchedBy != "bound.drift.drop" {
		t.Errorf("MatchedBy = %q, want %q", route.MatchedBy, "bound.drift.drop")
	}
	// AgentID must be empty — NOT the default agent.
	if route.AgentID == "mia" {
		t.Error("drift drop must NOT route to the global default agent")
	}
}

// TestResolveRoute_BoundDrift_WorkerAgent covers DS-3 row 4: the bound agent
// exists but is a worker (not a chat target) → Drop=true.
func TestResolveRoute_BoundDrift_WorkerAgent(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray", Type: config.AgentTypeWorker},
	}
	cfg := testConfigWithChannels(agents, nil, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "whatsapp",
		InstanceID:    "whatsapp.eu",
		Identity:      &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		BoundInstance: true,
	})

	if !route.Drop {
		t.Fatalf("route.Drop is false for a worker bound agent; must be true (drift drop)")
	}
	if route.MatchedBy != "bound.drift.drop" {
		t.Errorf("MatchedBy = %q, want %q", route.MatchedBy, "bound.drift.drop")
	}
	if route.AgentID == "mia" {
		t.Error("drift drop must NOT route to the global default agent")
	}
}

// TestResolveRoute_BoundDrift_NotBound_WorkerFallsToDefault covers a non-bound
// route: a worker as identity on an UNBOUND instance must fall through to the
// default (existing pickAgentID behavior), NOT drop.
func TestResolveRoute_BoundDrift_NotBound_WorkerFallsToDefault(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray", Type: config.AgentTypeWorker},
	}
	cfg := testConfigWithChannels(agents, nil, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "whatsapp",
		InstanceID:    "whatsapp", // bare type — unbound
		Identity:      &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		BoundInstance: false, // key: not bound
	})

	// Unbound path: pickAgentID sees a worker and falls back to default (existing behavior).
	if route.Drop {
		t.Fatal("unbound route with worker identity must NOT drop; it should fall back to default via pickAgentID")
	}
	if route.AgentID != "mia" {
		t.Errorf("unbound worker identity should fall to default 'mia', got %q", route.AgentID)
	}
}

// ── TDD #8: stale member still routes (DS-3 row 5) ───────────────────────────

// TestResolveRoute_StaleMember_StillRoutes covers DS-3 row 5: the agent is
// removed from CoreTeam but still exists in the agent list as a valid chat
// target → still routes (stale but functional), no drop.
// The drift guard checks agent existence + chat-target status, NOT CoreTeam membership.
func TestResolveRoute_StaleMember_StillRoutes(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"}, // exists, enabled, non-worker — just not in CoreTeam anymore
	}
	cfg := testConfigWithChannels(agents, nil, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "whatsapp",
		InstanceID:    "whatsapp.eu",
		Identity:      &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		BoundInstance: true,
	})

	if route.Drop {
		t.Fatalf("stale member (removed from CoreTeam but still existing) must NOT drop; got Drop=true")
	}
	if route.AgentID != "ray" {
		t.Errorf("stale member should still route to 'ray', got %q", route.AgentID)
	}
	if route.MatchedBy != "identity.agent" {
		t.Errorf("MatchedBy = %q, want %q", route.MatchedBy, "identity.agent")
	}
}

// ── DS-3 row 1: in-team member routes normally ───────────────────────────────

// TestResolveRoute_BoundInstance_InTeam_Routes covers DS-3 row 1: the agent is
// present, in CoreTeam, enabled, non-worker → routes normally to the member.
func TestResolveRoute_BoundInstance_InTeam_Routes(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	}
	cfg := testConfigWithChannels(agents, nil, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "whatsapp",
		InstanceID:    "whatsapp.eu",
		Identity:      &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		BoundInstance: true,
	})

	if route.Drop {
		t.Fatal("in-team member must NOT drop")
	}
	if route.AgentID != "ray" {
		t.Errorf("AgentID = %q, want %q", route.AgentID, "ray")
	}
}

// ── Unbound regression: default cascade unchanged ────────────────────────────

// TestResolveRoute_Unbound_DefaultCascadeUnchanged is a regression test (TDD #11):
// an unbound instance (BoundInstance=false, no Identity) with no explicit agent_id
// and no handoff pin resolves via the existing default cascade, never drops.
func TestResolveRoute_Unbound_DefaultCascadeUnchanged(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia", Default: true},
	}
	cfg := testConfigWithChannels(agents, nil, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "telegram",
		InstanceID:    "telegram", // bare type
		BoundInstance: false,
		Identity:      nil,
	})

	if route.Drop {
		t.Fatal("unbound instance must not drop; should use existing default cascade")
	}
	if route.AgentID != "mia" {
		t.Errorf("unbound default cascade: AgentID = %q, want %q", route.AgentID, "mia")
	}
	if route.MatchedBy != "default" {
		t.Errorf("unbound default cascade: MatchedBy = %q, want %q", route.MatchedBy, "default")
	}
}

// ── Priority order preserved: explicit binding still wins over default ───────

// TestResolveRoute_ExplicitBinding_WinsOverBoundInstance verifies that
// an explicit peer binding takes precedence over the default cascade even
// when BoundInstance is false (regression for existing Priority-1..6 behavior).
func TestResolveRoute_ExplicitBinding_WinsOverBoundInstance(t *testing.T) {
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
				Peer:      &config.PeerMatch{Kind: "direct", ID: "user42"},
			},
		},
	}
	cfg := testConfigWithChannels(agents, bindings, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:       "telegram",
		Peer:          &RoutePeer{Kind: "direct", ID: "user42"},
		BoundInstance: false,
	})

	if route.AgentID != "support" {
		t.Errorf("explicit peer binding: AgentID = %q, want %q", route.AgentID, "support")
	}
	if route.MatchedBy != "binding.peer" {
		t.Errorf("explicit peer binding: MatchedBy = %q, want %q", route.MatchedBy, "binding.peer")
	}
}
