package routing

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// RouteInput contains the routing context from an inbound message.
type RouteInput struct {
	Channel    string
	AccountID  string
	Peer       *RoutePeer
	ParentPeer *RoutePeer
	GuildID    string
	TeamID     string
	// InstanceID is the channel-instance key the message arrived on. For a
	// single-instance channel this equals the channel type (e.g. "telegram");
	// for additional instances it is the namespaced key (e.g. "telegram.eu").
	// Carried so identity-based routing can address the right instance. (FR-2.5)
	InstanceID string
	// Identity is the per-instance routing identity: for a bound instance,
	// Kind=="agent" names the connection's main agent. Populated by the agent
	// loop from the inbound channel instance's persisted identity.
	Identity *config.ChannelIdentity
	// BoundInstance is true when the inbound instance is bound to a workspace
	// AND names a main agent. An instance that is not bound routes nowhere
	// (omnipus-system-agent FR-121, FR-133).
	BoundInstance bool
}

// Route outcomes reported in ResolvedRoute.MatchedBy.
const (
	// MatchedByMainAgent: a bound instance routed to its main agent.
	MatchedByMainAgent = "bound.main_agent"
	// MatchedByUnbound: the instance is not bound to a workspace and a main
	// agent — the message is dropped (FR-121).
	MatchedByUnbound = "unbound.drop"
	// MatchedByMissingAgent: the bound main agent does not exist (deleted, or
	// its record failed to load) — dropped, never re-routed.
	MatchedByMissingAgent = "bound.agent_missing"
	// MatchedByNotChatTarget: the bound main agent is a worker or a system
	// agent — dropped.
	MatchedByNotChatTarget = "bound.not_chat_target"
)

// ResolvedRoute is the result of agent routing.
type ResolvedRoute struct {
	AgentID        string
	Channel        string
	AccountID      string
	SessionKey     string
	MainSessionKey string
	MatchedBy      string // one of the MatchedBy* constants
	// Drop is true when the message must not reach any agent: the instance is
	// unbound, or its bound main agent cannot answer. There is no fallback
	// agent; the caller drops the message loudly (FR-121).
	Drop bool
}

// RouteResolver determines which agent handles a channel message.
type RouteResolver struct {
	cfg *config.Config
}

// NewRouteResolver creates a new route resolver.
func NewRouteResolver(cfg *config.Config) *RouteResolver {
	return &RouteResolver{cfg: cfg}
}

// ResolveRoute determines which agent answers an inbound channel message and
// builds its session keys (omnipus-system-agent FR-121, FR-133). There is no
// default agent and no global or channel-wildcard fallback: an instance that
// is not bound to a workspace with a named main agent drops, and so does a
// bound instance whose main agent is missing or is not a chat target. A bound
// instance with an eligible main agent routes to it (FR-133: the
// connection's main agent replaces the channel wildcard; instance-scoped
// person rules arrive with US-16 in landing 2). Workspace membership is
// checked by the caller, which knows the workspace.
func (r *RouteResolver) ResolveRoute(input RouteInput) ResolvedRoute {
	channel := strings.ToLower(strings.TrimSpace(input.Channel))
	accountID := NormalizeAccountID(input.AccountID)
	drop := func(by string) ResolvedRoute {
		return ResolvedRoute{Channel: channel, AccountID: accountID, MatchedBy: by, Drop: true}
	}
	identity := input.Identity
	if !input.BoundInstance || identity == nil ||
		strings.ToLower(strings.TrimSpace(identity.Kind)) != "agent" || strings.TrimSpace(identity.ID) == "" {
		return drop(MatchedByUnbound)
	}
	agentID := NormalizeAgentID(strings.TrimSpace(identity.ID))
	found, chatTarget := false, false
	for _, a := range r.cfg.Agents.List {
		if NormalizeAgentID(a.ID) == agentID {
			found, chatTarget = true, a.IsChatTarget()
			break
		}
	}
	if !found || r.isSkippedAgentID(agentID) {
		return drop(MatchedByMissingAgent)
	}
	if !chatTarget {
		return drop(MatchedByNotChatTarget)
	}
	dmScope := DMScope(r.cfg.Session.DMScope)
	if dmScope == "" {
		dmScope = DMScopeMain
	}
	sessionKey := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:       agentID,
		Channel:       channel,
		AccountID:     accountID,
		Peer:          input.Peer,
		DMScope:       dmScope,
		IdentityLinks: r.cfg.Session.IdentityLinks,
	})
	return ResolvedRoute{
		AgentID:        agentID,
		Channel:        channel,
		AccountID:      accountID,
		SessionKey:     sessionKey,
		MainSessionKey: BuildAgentMainSessionKey(agentID),
		MatchedBy:      MatchedByMainAgent,
	}
}

// isSkippedAgentID reports whether id (already normalized) names an agent
// entity record that exists on disk but failed to load during the most
// recent boot/reload (config.Config.SkippedAgentIDs, ADR-054 D7). Such an
// agent cannot answer, and its traffic is never re-routed.
func (r *RouteResolver) isSkippedAgentID(id string) bool {
	for _, s := range r.cfg.SkippedAgentIDs {
		if NormalizeAgentID(s) == id {
			return true
		}
	}
	return false
}
