package routing

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func testConfig(agents []config.AgentConfig, bindings []config.AgentBinding) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         "/tmp/omnipus-test",
				DefaultModel: config.DefaultModel{Model: "gpt-4"},
			},
			List: agents,
		},
		Bindings: bindings,
		Session: config.SessionConfig{
			DMScope: "per-peer",
		},
	}
}

// TestResolveRoute_DefaultAgent_NoBindings pins the post-sentinel-removal
// contract: with an empty agent list, resolveDefaultAgentID has no sentinel
// left to invent and returns "" (WARN-logged) — it must NOT resolve to a
// hardcoded agent name. Traces to route.go::resolveDefaultAgentID's "No
// agents are configured" branch.
func TestResolveRoute_DefaultAgent_NoBindings(t *testing.T) {
	cfg := testConfig(nil, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user1"},
	})

	if route.AgentID != "" {
		t.Errorf("AgentID = %q, want empty string (no agents configured, no sentinel to fall back to)", route.AgentID)
	}
	if route.Drop {
		t.Errorf("Drop = true, want false — an empty default is a routable empty ID, not a drop")
	}
	if route.MatchedBy != "default" {
		t.Errorf("MatchedBy = %q, want 'default'", route.MatchedBy)
	}
}

func TestResolveRoute_PeerBinding(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "sales", Default: true},
		{ID: "support"},
	}
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
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user123"},
	})

	if route.AgentID != "support" {
		t.Errorf("AgentID = %q, want 'support'", route.AgentID)
	}
	if route.MatchedBy != "binding.peer" {
		t.Errorf("MatchedBy = %q, want 'binding.peer'", route.MatchedBy)
	}
}

func TestResolveRoute_GuildBinding(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "general", Default: true},
		{ID: "gaming"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "gaming",
			Match: config.BindingMatch{
				Channel:   "discord",
				AccountID: "*",
				GuildID:   "guild-abc",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "discord",
		GuildID: "guild-abc",
		Peer:    &RoutePeer{Kind: "channel", ID: "ch1"},
	})

	if route.AgentID != "gaming" {
		t.Errorf("AgentID = %q, want 'gaming'", route.AgentID)
	}
	if route.MatchedBy != "binding.guild" {
		t.Errorf("MatchedBy = %q, want 'binding.guild'", route.MatchedBy)
	}
}

func TestResolveRoute_TeamBinding(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "general", Default: true},
		{ID: "work"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "work",
			Match: config.BindingMatch{
				Channel:   "slack",
				AccountID: "*",
				TeamID:    "T12345",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "slack",
		TeamID:  "T12345",
		Peer:    &RoutePeer{Kind: "channel", ID: "C001"},
	})

	if route.AgentID != "work" {
		t.Errorf("AgentID = %q, want 'work'", route.AgentID)
	}
	if route.MatchedBy != "binding.team" {
		t.Errorf("MatchedBy = %q, want 'binding.team'", route.MatchedBy)
	}
}

func TestResolveRoute_AccountBinding(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "default-agent", Default: true},
		{ID: "premium"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "premium",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "bot2",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:   "telegram",
		AccountID: "bot2",
		Peer:      &RoutePeer{Kind: "direct", ID: "user1"},
	})

	if route.AgentID != "premium" {
		t.Errorf("AgentID = %q, want 'premium'", route.AgentID)
	}
	if route.MatchedBy != "binding.account" {
		t.Errorf("MatchedBy = %q, want 'binding.account'", route.MatchedBy)
	}
}

func TestResolveRoute_ChannelWildcard(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "main", Default: true},
		{ID: "telegram-bot"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "telegram-bot",
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

	if route.AgentID != "telegram-bot" {
		t.Errorf("AgentID = %q, want 'telegram-bot'", route.AgentID)
	}
	if route.MatchedBy != "binding.channel" {
		t.Errorf("MatchedBy = %q, want 'binding.channel'", route.MatchedBy)
	}
}

func TestResolveRoute_PriorityOrder_PeerBeatsGuild(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "general", Default: true},
		{ID: "vip"},
		{ID: "gaming"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "vip",
			Match: config.BindingMatch{
				Channel:   "discord",
				AccountID: "*",
				Peer:      &config.PeerMatch{Kind: "direct", ID: "user-vip"},
			},
		},
		{
			AgentID: "gaming",
			Match: config.BindingMatch{
				Channel:   "discord",
				AccountID: "*",
				GuildID:   "guild-1",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "discord",
		GuildID: "guild-1",
		Peer:    &RoutePeer{Kind: "direct", ID: "user-vip"},
	})

	if route.AgentID != "vip" {
		t.Errorf("AgentID = %q, want 'vip' (peer should beat guild)", route.AgentID)
	}
	if route.MatchedBy != "binding.peer" {
		t.Errorf("MatchedBy = %q, want 'binding.peer'", route.MatchedBy)
	}
}

func TestResolveRoute_InvalidAgentFallsToDefault(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "main", Default: true},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "nonexistent",
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
	})

	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main' (invalid agent should fall to default)", route.AgentID)
	}
}

// TestResolveRoute_SkippedAgentFailsClosed pins ADR-054 D7/§9: a binding
// naming an agent whose entity record EXISTS but failed to load (recorded in
// cfg.SkippedAgentIDs by the agent registry after agentstore.Store.List)
// must fail closed — Drop the route — rather than falling back to the
// default agent. Re-routing a binding that was deliberately pointed at a
// specific, possibly more-restrictive agent to the default is a privilege
// change, not availability graceful degradation (unlike
// TestResolveRoute_InvalidAgentFallsToDefault's "never existed" case, which
// keeps the pre-ADR-054 WARN + default-fallback behavior unchanged).
func TestResolveRoute_SkippedAgentFailsClosed(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "main", Default: true},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "restricted-worker",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	cfg.SkippedAgentIDs = []string{"restricted-worker"}
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "telegram",
	})

	if !route.Drop {
		t.Fatalf("Drop = false, want true — a binding naming a skipped agent must fail closed, not re-route to %q", route.AgentID)
	}
	if route.AgentID != "" {
		t.Errorf("AgentID = %q, want empty on a dropped route", route.AgentID)
	}
	if route.MatchedBy != "binding.channel" {
		t.Errorf("MatchedBy = %q, want the binding rule that matched (binding.channel)", route.MatchedBy)
	}
}

// TestResolveRoute_SkippedAgentDoesNotAffectUnrelatedBindings proves the
// skipped-ID check is scoped to the specific requested agent, not a global
// kill switch: a DIFFERENT binding naming a healthy, loaded agent must still
// route normally even while some OTHER agent is recorded as skipped.
func TestResolveRoute_SkippedAgentDoesNotAffectUnrelatedBindings(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "main", Default: true},
		{ID: "healthy-worker"},
	}
	bindings := []config.AgentBinding{
		{
			AgentID: "healthy-worker",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
			},
		},
	}
	cfg := testConfig(agents, bindings)
	cfg.SkippedAgentIDs = []string{"some-other-broken-agent"}
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "telegram",
	})

	if route.Drop {
		t.Fatal("Drop = true, want false — an unrelated skipped agent must not affect this binding's own healthy target")
	}
	if route.AgentID != "healthy-worker" {
		t.Errorf("AgentID = %q, want 'healthy-worker'", route.AgentID)
	}
}

// TestResolveRoute_DefaultAgentSelection verifies the ADR-054 D6.4 ladder:
// the settings singleton (Agents.Defaults.DefaultAgentID) selects the default
// agent, NOT the per-entity AgentConfig.Default field (which "beta" sets here
// deliberately, to prove it is now inert for resolution — see registry.go's
// GetDefaultAgent and this file's resolveDefaultAgentID doc comments for why
// the field was demoted: two concurrent per-entity writes could each set
// Default=true with no shared lock, so the single "the default" pointer had
// to move to a settings scalar that cannot have two winners).
func TestResolveRoute_DefaultAgentSelection(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta", Default: true},
		{ID: "gamma"},
	}
	cfg := testConfig(agents, nil)
	cfg.Agents.Defaults.DefaultAgentID = "beta"
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "cli",
	})

	if route.AgentID != "beta" {
		t.Errorf("AgentID = %q, want 'beta' (config.Agents.Defaults.DefaultAgentID)", route.AgentID)
	}
}

// TestResolveRoute_DefaultAgentSelection_EntityFlagIsInert is the negative
// counterpart: AgentConfig.Default=true alone (no settings override) must NOT
// select "beta" — the fallback ladder picks the first chat-target agent in
// list order instead ("alpha"). Regression guard for D6.4's core invariant.
func TestResolveRoute_DefaultAgentSelection_EntityFlagIsInert(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta", Default: true},
		{ID: "gamma"},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "cli",
	})

	if route.AgentID != "alpha" {
		t.Errorf("AgentID = %q, want 'alpha' (AgentConfig.Default must be inert without the settings override)", route.AgentID)
	}
}

func TestResolveRoute_NoDefaultFallsBackToFirstEnabledAgent(t *testing.T) {
	// When custom agents exist but none is marked default, routing must fall
	// back to the first enabled agent (sprint/258 hardening). Previously the
	// fallback was the "main" constant which usually didn't exist, causing
	// messages to be silently dropped. The new behavior picks the first
	// enabled agent so messages always route somewhere meaningful.
	agents := []config.AgentConfig{
		{ID: "alpha"}, // Enabled==nil → active
		{ID: "beta"},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel: "cli",
	})

	// "alpha" is the first enabled agent — it must be chosen over DefaultAgentID.
	if route.AgentID != "alpha" {
		t.Errorf("AgentID = %q, want 'alpha' (first enabled agent when no default set)", route.AgentID)
	}
}
