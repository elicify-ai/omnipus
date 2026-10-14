package routing

// Routing after the default agent's retirement (omnipus-system-agent FR-121,
// FR-133): a channel message routes only through its instance's workspace
// binding to the named main agent; every other case drops, with no default,
// channel-wildcard or binding fallback. Supersedes the 7-priority cascade
// tests (route_test, routing_default_test, identity_route_test,
// worker_routing_test, route_judge_test, bound_instance_test).

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
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
		Session:  config.SessionConfig{DMScope: "per-peer"},
	}
}

func boundInput(agentID string) RouteInput {
	return RouteInput{
		Channel: "whatsapp", InstanceID: "whatsapp.eu",
		Identity:      &config.ChannelIdentity{Kind: "agent", ID: agentID},
		BoundInstance: true,
		Peer:          &RoutePeer{Kind: "direct", ID: "+15550001"},
	}
}

func TestResolveRoute_BoundInstanceRoutesToItsMainAgent(t *testing.T) {
	cfg := testConfig([]config.AgentConfig{{ID: "mia"}, {ID: "ray"}}, nil)
	route := NewRouteResolver(cfg).ResolveRoute(boundInput("ray"))
	assert.False(t, route.Drop)
	assert.Equal(t, "ray", route.AgentID)
	assert.Equal(t, MatchedByMainAgent, route.MatchedBy)
	assert.Contains(t, route.SessionKey, "ray", "the session key belongs to the main agent")
	assert.NotEmpty(t, route.MainSessionKey)
}

func TestResolveRoute_BoundMainAgentThatCannotAnswerDrops(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "mia"},
		{ID: "helper", Type: config.AgentTypeWorker},
		{ID: "judge", Type: config.AgentTypeSystem},
		{ID: "broken"},
	}
	for _, row := range []struct {
		name, agent, matched string
		skipped              []string
	}{
		{"deleted", "ghost", MatchedByMissingAgent, nil},
		{"record_failed_to_load", "broken", MatchedByMissingAgent, []string{"broken"}},
		{"worker", "helper", MatchedByNotChatTarget, nil},
		{"system_agent", "judge", MatchedByNotChatTarget, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			cfg := testConfig(agents, nil)
			cfg.SkippedAgentIDs = row.skipped
			route := NewRouteResolver(cfg).ResolveRoute(boundInput(row.agent))
			assert.True(t, route.Drop, "FR-121: never fall back to another agent")
			assert.Equal(t, row.matched, route.MatchedBy)
			assert.Empty(t, route.AgentID)
			assert.Empty(t, route.SessionKey)
		})
	}
}

// An unbound instance drops even when legacy bindings would once have routed
// it (peer, account, channel wildcard) — FR-133 retires priorities 6 and 7,
// and priorities 1–5 return only at instance scope (US-16, landing 2).
func TestResolveRoute_UnboundDropsWhateverLegacyBindingsSay(t *testing.T) {
	agents := []config.AgentConfig{{ID: "mia"}, {ID: "ray"}}
	bindings := []config.AgentBinding{
		{AgentID: "ray", Match: config.BindingMatch{Channel: "whatsapp", Peer: &config.PeerMatch{Kind: "direct", ID: "+15550001"}}},
		{AgentID: "ray", Match: config.BindingMatch{Channel: "whatsapp", AccountID: "acct"}},
		{AgentID: "mia", Match: config.BindingMatch{Channel: "whatsapp", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	for _, in := range []RouteInput{
		{Channel: "whatsapp", Peer: &RoutePeer{Kind: "direct", ID: "+15550001"}},
		{Channel: "whatsapp", AccountID: "acct"},
		{Channel: "whatsapp", Identity: &config.ChannelIdentity{Kind: "agent", ID: "ray"}},
		{Channel: "whatsapp", Identity: &config.ChannelIdentity{Kind: "user"}, BoundInstance: true},
		{Channel: "whatsapp", Identity: &config.ChannelIdentity{Kind: "agent", ID: "  "}, BoundInstance: true},
	} {
		route := NewRouteResolver(cfg).ResolveRoute(in)
		assert.True(t, route.Drop, "input %+v must drop", in)
		assert.Equal(t, MatchedByUnbound, route.MatchedBy)
		assert.Empty(t, route.AgentID)
	}
}

func TestResolveRoute_SkippedAgentDoesNotAffectAnotherBinding(t *testing.T) {
	cfg := testConfig([]config.AgentConfig{{ID: "mia"}, {ID: "ray"}}, nil)
	cfg.SkippedAgentIDs = []string{"some-other-broken-agent"}
	route := NewRouteResolver(cfg).ResolveRoute(boundInput("ray"))
	assert.False(t, route.Drop)
	assert.Equal(t, "ray", route.AgentID)
}

func TestRouteZeroValues_NeverRouteOrDropByAccident(t *testing.T) {
	var in RouteInput
	assert.False(t, in.BoundInstance)
	var out ResolvedRoute
	assert.False(t, out.Drop)
	route := NewRouteResolver(testConfig(nil, nil)).ResolveRoute(in)
	assert.True(t, route.Drop, "a zero-value input is unbound and drops")
}
