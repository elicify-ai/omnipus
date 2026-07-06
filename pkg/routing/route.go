package routing

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
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
	// Identity is the per-instance routing identity (Spec-2 US-5 / FR-2.9).
	// When set with Kind=="agent" and a non-empty ID, the message is routed AS
	// that agent, overriding the binding cascade. Kind=="user" (or a nil
	// Identity) leaves the normal cascade in effect (peer→…→default). Populated
	// by the agent loop from the inbound channel instance's persisted identity.
	Identity *config.ChannelIdentity
	// BoundInstance is set true when the inbound instance is workspace-bound
	// (cfg.Channels[InstanceID].WorkspaceID is non-empty). Consumed by the
	// WS-A drift branch in ResolveRoute to enforce the no-global-default rule
	// for bound instances (ADR-029 FR-012/FR-014).
	BoundInstance bool
}

// ResolvedRoute is the result of agent routing.
type ResolvedRoute struct {
	AgentID        string
	Channel        string
	AccountID      string
	SessionKey     string
	MainSessionKey string
	MatchedBy      string // "identity.agent", "binding.peer", "binding.peer.parent", "binding.guild", "binding.team", "binding.account", "binding.channel", "default", "bound.drift.drop"
	// Drop is true when this route is a bound-instance drift drop: the instance
	// is workspace-bound but its configured agent is unresolvable (deleted or a
	// worker — not a chat target). The caller MUST NOT fall back to the global
	// default — instead it enters the FR-015 unroutable path with a structured
	// audit event (ADR-029 FR-012, FR-014, FR-028). WS-A wires the drop branch logic.
	Drop bool
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
		resolvedAgentID := r.pickAgentID(agentID, matchedBy)
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
	// the default agent.
	//
	// ADR-029 FR-012/FR-013/FR-014 — bound-instance drift guard:
	// For workspace-bound instances (BoundInstance=true), if the configured agent
	// is unresolvable (deleted or a worker — not a chat target) the route MUST
	// drop rather than fall through to pickAgentID's default fallback. A member
	// merely removed from CoreTeam but still existing as a non-worker MUST still
	// route (stale, FR-013). pickAgentID validates X against the agent list and
	// logs a fallback to default only for NON-bound callers; for bound callers an
	// unresolvable agent → Drop.
	if input.Identity != nil {
		kind := strings.ToLower(strings.TrimSpace(input.Identity.Kind))
		if kind == "agent" && strings.TrimSpace(input.Identity.ID) != "" {
			agentID := strings.TrimSpace(input.Identity.ID)
			if input.BoundInstance {
				// Drift check: the agent must exist AND be a chat target (non-worker).
				// Removal from CoreTeam alone is NOT a drift condition — the instance
				// routes stale (FR-013) and the SPA shows a config-time warning only.
				unresolvable := true
				agents := r.cfg.Agents.List
				for _, a := range agents {
					if NormalizeAgentID(a.ID) == NormalizeAgentID(agentID) {
						if a.IsChatTarget() {
							unresolvable = false
						}
						break
					}
				}
				if unresolvable {
					return ResolvedRoute{
						Channel:   channel,
						AccountID: accountID,
						MatchedBy: "bound.drift.drop",
						Drop:      true,
					}
				}
			}
			return choose(agentID, "identity.agent")
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

// pickAgentID resolves a binding/identity-supplied agent ID to a concrete,
// route-able agent ID. matchedBy is the routing rule that produced agentID
// (e.g. "binding.peer", "identity.agent", "default") — it is threaded purely so
// the fallback WARN logs can tell an operator WHICH binding rule routed to a
// worker or a non-existent agent.
func (r *RouteResolver) pickAgentID(agentID string, matchedBy string) string {
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
			// A worker is never a chat target — it is invoked only via delegation.
			// An explicit binding or identity override that resolves to a worker
			// must degrade to the base default rather than letting the worker
			// answer as a live persona (M1). Mirror the non-existent-agent
			// fallback below: log a WARN and return the default.
			if !a.IsChatTarget() {
				defaultID := NormalizeAgentID(r.resolveDefaultAgentID())
				logger.WarnCF(
					"routing",
					"Binding/identity references a worker agent (not a chat target); falling back to default",
					map[string]any{
						"requested_agent_id": normalized,
						"default_agent_id":   defaultID,
						"matched_by":         matchedBy,
					},
				)
				return defaultID
			}
			return normalized
		}
	}
	// Binding references an agent ID that is not in the agent list. Log a warning
	// so operators can detect misconfigured bindings at runtime.
	defaultID := NormalizeAgentID(r.resolveDefaultAgentID())
	logger.WarnCF("routing", "Binding references non-existent agent; falling back to default",
		map[string]any{"requested_agent_id": normalized, "default_agent_id": defaultID, "matched_by": matchedBy})
	return defaultID
}

func (r *RouteResolver) resolveDefaultAgentID() string {
	agents := r.cfg.Agents.List
	if len(agents) == 0 {
		return DefaultAgentID
	}
	// Primary: use the agent explicitly marked as default, but only if it is
	// a chat target. A worker is never a chat target (it is invoked only via
	// delegation) — fall through to the first-available-agent fallback so routing
	// never silently drops inbound work and never points it at a worker.
	for _, a := range agents {
		if a.Default && a.IsChatTarget() {
			id := strings.TrimSpace(a.ID)
			if id != "" {
				return NormalizeAgentID(id)
			}
		}
	}
	// Fallback: no agent is marked as default. Pick the first chat-target
	// agent so inbound messages are never silently dropped. Workers are skipped —
	// they must never be resolved as the default. Log a warning so operators can
	// detect misconfigured setups.
	for _, a := range agents {
		if a.IsChatTarget() {
			id := strings.TrimSpace(a.ID)
			if id == "" {
				continue
			}
			normalized := NormalizeAgentID(id)
			logger.WarnCF("routing", "No agent marked as default; falling back to first available agent",
				map[string]any{"fallback_agent_id": normalized, "custom_agent_count": len(agents)})
			return normalized
		}
	}
	// No chat-target agents with valid IDs — last resort is the DefaultAgentID constant.
	logger.WarnCF("routing", "No available agent found; routing falls back to built-in default",
		map[string]any{"custom_agent_count": len(agents)})
	return DefaultAgentID
}
