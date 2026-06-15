package routing

import (
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// RouteInput contains the routing context from an inbound message.
type RouteInput struct {
	Channel    string
	AccountID  string
	Peer       *RoutePeer
	ParentPeer *RoutePeer
	GuildID    string
	TeamID     string
	// InstanceID is the channel-instance key the message arrived on. In v0.1
	// (cap-1/type) this equals the channel type. Carried so identity-based
	// routing can address the right instance once v0.3 lifts the cap. (FR-2.5)
	InstanceID string
	// Identity is the per-instance routing identity (Spec-2 US-5 / FR-2.9).
	// When set with Kind=="agent" and a non-empty ID, the message is routed AS
	// that agent, overriding the binding cascade. Kind=="user" (or a nil
	// Identity) leaves the normal cascade in effect (peer→…→default). Populated
	// by the agent loop from the inbound channel instance's persisted identity.
	Identity *config.ChannelIdentity
}

// ResolvedRoute is the result of agent routing.
type ResolvedRoute struct {
	AgentID        string
	Channel        string
	AccountID      string
	SessionKey     string
	MainSessionKey string
	MatchedBy      string // "identity.agent", "binding.peer", "binding.peer.parent", "binding.guild", "binding.team", "binding.account", "binding.channel", "default"
}

// RouteResolver determines which agent handles a message based on config bindings.
type RouteResolver struct {
	cfg *config.Config
}

// NewRouteResolver creates a new route resolver.
func NewRouteResolver(cfg *config.Config) *RouteResolver {
	return &RouteResolver{cfg: cfg}
}

// ResolveRoute determines which agent handles the message and constructs session keys.
// Implements the 7-level priority cascade:
// peer > parent_peer > guild > team > account > channel_wildcard > default
func (r *RouteResolver) ResolveRoute(input RouteInput) ResolvedRoute {
	channel := strings.ToLower(strings.TrimSpace(input.Channel))
	accountID := NormalizeAccountID(input.AccountID)
	peer := input.Peer

	dmScope := DMScope(r.cfg.Session.DMScope)
	if dmScope == "" {
		dmScope = DMScopeMain
	}
	identityLinks := r.cfg.Session.IdentityLinks

	bindings := r.filterBindings(channel, accountID)

	choose := func(agentID string, matchedBy string) ResolvedRoute {
		resolvedAgentID := r.pickAgentID(agentID)
		sessionKey := strings.ToLower(BuildAgentPeerSessionKey(SessionKeyParams{
			AgentID:       resolvedAgentID,
			Channel:       channel,
			AccountID:     accountID,
			Peer:          peer,
			DMScope:       dmScope,
			IdentityLinks: identityLinks,
		}))
		mainSessionKey := strings.ToLower(BuildAgentMainSessionKey(resolvedAgentID))
		return ResolvedRoute{
			AgentID:        resolvedAgentID,
			Channel:        channel,
			AccountID:      accountID,
			SessionKey:     sessionKey,
			MainSessionKey: mainSessionKey,
			MatchedBy:      matchedBy,
		}
	}

	// Priority 0: Per-instance identity override (Spec-2 US-5 / FR-2.9).
	// When the inbound channel instance carries identity{kind:agent,id:X}, the
	// connection acts AS agent X — this overrides every binding. A user-kind
	// identity (or no identity) leaves the normal cascade in effect: the message
	// is attributed to the user and routed by the binding rules below, ending at
	// the default agent. pickAgentID validates X against the agent list and logs
	// a fallback to default if it is unknown, so a stale identity can never drop
	// the message.
	if input.Identity != nil {
		kind := strings.ToLower(strings.TrimSpace(input.Identity.Kind))
		if kind == "agent" && strings.TrimSpace(input.Identity.ID) != "" {
			return choose(input.Identity.ID, "identity.agent")
		}
	}

	// Priority 1: Peer binding
	if peer != nil && strings.TrimSpace(peer.ID) != "" {
		if match := r.findPeerMatch(bindings, peer); match != nil {
			return choose(match.AgentID, "binding.peer")
		}
	}

	// Priority 2: Parent peer binding
	parentPeer := input.ParentPeer
	if parentPeer != nil && strings.TrimSpace(parentPeer.ID) != "" {
		if match := r.findPeerMatch(bindings, parentPeer); match != nil {
			return choose(match.AgentID, "binding.peer.parent")
		}
	}

	// Priority 3: Guild binding
	guildID := strings.TrimSpace(input.GuildID)
	if guildID != "" {
		if match := r.findGuildMatch(bindings, guildID); match != nil {
			return choose(match.AgentID, "binding.guild")
		}
	}

	// Priority 4: Team binding
	teamID := strings.TrimSpace(input.TeamID)
	if teamID != "" {
		if match := r.findTeamMatch(bindings, teamID); match != nil {
			return choose(match.AgentID, "binding.team")
		}
	}

	// Priority 5: Account binding
	if match := r.findAccountMatch(bindings); match != nil {
		return choose(match.AgentID, "binding.account")
	}

	// Priority 6: Channel wildcard binding
	if match := r.findChannelWildcardMatch(bindings); match != nil {
		return choose(match.AgentID, "binding.channel")
	}

	// Priority 7: Default agent
	return choose(r.resolveDefaultAgentID(), "default")
}

func (r *RouteResolver) filterBindings(channel, accountID string) []config.AgentBinding {
	var filtered []config.AgentBinding
	for _, b := range r.cfg.Bindings {
		matchChannel := strings.ToLower(strings.TrimSpace(b.Match.Channel))
		if matchChannel == "" {
			logger.WarnCF("routing", "Binding skipped: match.channel is empty", map[string]any{
				"agent_id": b.AgentID,
			})
			continue
		}
		if matchChannel != channel {
			continue
		}
		if !matchesAccountID(b.Match.AccountID, accountID) {
			continue
		}
		filtered = append(filtered, b)
	}
	return filtered
}

func matchesAccountID(matchAccountID, actual string) bool {
	trimmed := strings.TrimSpace(matchAccountID)
	if trimmed == "" {
		return actual == DefaultAccountID
	}
	if trimmed == "*" {
		return true
	}
	return strings.ToLower(trimmed) == strings.ToLower(actual)
}

func (r *RouteResolver) findPeerMatch(bindings []config.AgentBinding, peer *RoutePeer) *config.AgentBinding {
	for i := range bindings {
		b := &bindings[i]
		if b.Match.Peer == nil {
			continue
		}
		peerKind := strings.ToLower(strings.TrimSpace(b.Match.Peer.Kind))
		peerID := strings.TrimSpace(b.Match.Peer.ID)
		if peerKind == "" || peerID == "" {
			continue
		}
		// Peer ID comparison is case-sensitive by design; channels must normalize IDs before routing.
		if peerKind == strings.ToLower(peer.Kind) && peerID == peer.ID {
			return b
		}
	}
	return nil
}

func (r *RouteResolver) findGuildMatch(bindings []config.AgentBinding, guildID string) *config.AgentBinding {
	for i := range bindings {
		b := &bindings[i]
		matchGuild := strings.TrimSpace(b.Match.GuildID)
		if matchGuild != "" && matchGuild == guildID {
			return &bindings[i]
		}
	}
	return nil
}

func (r *RouteResolver) findTeamMatch(bindings []config.AgentBinding, teamID string) *config.AgentBinding {
	for i := range bindings {
		b := &bindings[i]
		matchTeam := strings.TrimSpace(b.Match.TeamID)
		if matchTeam != "" && matchTeam == teamID {
			return &bindings[i]
		}
	}
	return nil
}

func (r *RouteResolver) findAccountMatch(bindings []config.AgentBinding) *config.AgentBinding {
	for i := range bindings {
		b := &bindings[i]
		accountID := strings.TrimSpace(b.Match.AccountID)
		if accountID == "*" {
			continue
		}
		if b.Match.Peer != nil || b.Match.GuildID != "" || b.Match.TeamID != "" {
			continue
		}
		return &bindings[i]
	}
	return nil
}

func (r *RouteResolver) findChannelWildcardMatch(bindings []config.AgentBinding) *config.AgentBinding {
	for i := range bindings {
		b := &bindings[i]
		accountID := strings.TrimSpace(b.Match.AccountID)
		if accountID != "*" {
			continue
		}
		if b.Match.Peer != nil || b.Match.GuildID != "" || b.Match.TeamID != "" {
			continue
		}
		return &bindings[i]
	}
	return nil
}

func (r *RouteResolver) pickAgentID(agentID string) string {
	trimmed := strings.TrimSpace(agentID)
	if trimmed == "" {
		return NormalizeAgentID(r.resolveDefaultAgentID())
	}
	normalized := NormalizeAgentID(trimmed)
	agents := r.cfg.Agents.List
	if len(agents) == 0 {
		return normalized
	}
	for _, a := range agents {
		if NormalizeAgentID(a.ID) == normalized {
			return normalized
		}
	}
	// Binding references an agent ID that is not in the agent list. Log a warning
	// so operators can detect misconfigured bindings at runtime.
	defaultID := NormalizeAgentID(r.resolveDefaultAgentID())
	logger.WarnCF("routing", "Binding references non-existent agent; falling back to default",
		map[string]any{"requested_agent_id": normalized, "default_agent_id": defaultID})
	return defaultID
}

func (r *RouteResolver) resolveDefaultAgentID() string {
	agents := r.cfg.Agents.List
	if len(agents) == 0 {
		return DefaultAgentID
	}
	// Primary: use the agent explicitly marked as default, but only if it is
	// active AND a chat target. A disabled default must not receive messages, and
	// a worker is never a chat target (it is invoked only via delegation) — in
	// either case fall through to the first-enabled-agent fallback so routing
	// never silently drops inbound work and never points it at a worker.
	for _, a := range agents {
		if a.Default && a.IsActive() && a.IsChatTarget() {
			id := strings.TrimSpace(a.ID)
			if id != "" {
				return NormalizeAgentID(id)
			}
		}
	}
	// Fallback: no agent is marked as default. Pick the first enabled chat-target
	// agent so inbound messages are never silently dropped. Workers are skipped —
	// they must never be resolved as the default. Log a warning so operators can
	// detect misconfigured setups.
	for _, a := range agents {
		if a.IsActive() && a.IsChatTarget() {
			id := strings.TrimSpace(a.ID)
			if id == "" {
				continue
			}
			normalized := NormalizeAgentID(id)
			logger.WarnCF("routing", "No agent marked as default; falling back to first enabled agent",
				map[string]any{"fallback_agent_id": normalized, "custom_agent_count": len(agents)})
			return normalized
		}
	}
	// All agents disabled or have empty IDs — last resort is the DefaultAgentID constant.
	logger.WarnCF("routing", "No enabled agent found; routing falls back to built-in default",
		map[string]any{"custom_agent_count": len(agents)})
	return DefaultAgentID
}
