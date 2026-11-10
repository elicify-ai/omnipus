package routing

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestL1WU1RouteResolver_UnboundAlwaysDropsNeverGlobalOrWildcardFallback(t *testing.T) {
	// FR-121/133 / BDD-13.1 and dataset D3. A legacy wildcard or named
	// identity cannot turn an unbound instance into a workspace binding.
	for _, row := range []struct {
		name     string
		identity *config.ChannelIdentity
		bindings []config.AgentBinding
	}{
		{"no_identity", nil, nil},
		{"legacy_named", &config.ChannelIdentity{Kind: "agent", ID: "mia"}, nil},
		{"legacy_wildcard", nil, []config.AgentBinding{{AgentID: "mia", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}}}},
		{"missing_legacy_agent", &config.ChannelIdentity{Kind: "agent", ID: "deleted"}, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.List = []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCore}}
			cfg.Bindings = row.bindings
			cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram.red": {Type: "telegram", Enabled: true}}
			route := NewRouteResolver(cfg).ResolveRoute(RouteInput{
				Channel: "telegram", InstanceID: "telegram.red", Identity: row.identity, BoundInstance: false,
				Peer: &RoutePeer{Kind: "direct", ID: "person-red"},
			})
			assert.True(t, route.Drop, "FR-121: instance without a workspace pair is unroutable")
			assert.Empty(t, route.AgentID, "no default/wildcard/legacy owner may be substituted")
			assert.Empty(t, route.SessionKey, "dropped input cannot carry a routable session key")
			assert.Empty(t, route.MainSessionKey)
		})
	}
	t.Run("bound_named_agent_positive_control", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Agents.List = []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCore}}
		identity := &config.ChannelIdentity{Kind: "agent", ID: "mia"}
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram.red": {Type: "telegram", WorkspaceID: "sales", Identity: identity}}
		route := NewRouteResolver(cfg).ResolveRoute(RouteInput{Channel: "telegram", InstanceID: "telegram.red", Identity: identity, BoundInstance: true})
		assert.False(t, route.Drop)
		assert.Equal(t, "mia", route.AgentID)
		assert.NotEmpty(t, route.SessionKey, "real named-bound control must remain routable")
	})
}
