package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// RoutingDrop is one channel instance's routing degradation: the reason the
// last unroutable inbound message was dropped and when (omnipus-system-agent
// FR-121, K15).
type RoutingDrop struct {
	Reason string
	At     time.Time
}

// routingDropTracker holds the live routing degradation per channel
// instance. In-memory by design: a degradation describes traffic this
// process refused, and the next unroutable message after a restart records
// it again.
type routingDropTracker struct {
	mu    sync.Mutex
	drops map[string]RoutingDrop
}

// RecordRoutingDrop marks instanceID degraded with reason. An empty
// instanceID records nothing (there is no channel to show it on).
func (al *AgentLoop) RecordRoutingDrop(instanceID, reason string) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return
	}
	al.routingDrops.mu.Lock()
	defer al.routingDrops.mu.Unlock()
	if al.routingDrops.drops == nil {
		al.routingDrops.drops = make(map[string]RoutingDrop)
	}
	al.routingDrops.drops[instanceID] = RoutingDrop{Reason: reason, At: time.Now().UTC()}
}

// ClearRoutingDrop removes instanceID's routing degradation (FR-129: saving
// a valid binding clears it).
func (al *AgentLoop) ClearRoutingDrop(instanceID string) {
	al.routingDrops.mu.Lock()
	defer al.routingDrops.mu.Unlock()
	delete(al.routingDrops.drops, strings.TrimSpace(instanceID))
}

// RoutingDropFor returns instanceID's active routing degradation, if any.
func (al *AgentLoop) RoutingDropFor(instanceID string) (RoutingDrop, bool) {
	al.routingDrops.mu.Lock()
	defer al.routingDrops.mu.Unlock()
	drop, ok := al.routingDrops.drops[strings.TrimSpace(instanceID)]
	return drop, ok
}

// unboundChannelReason is the visible drop reason for an instance that is not
// bound to a workspace with a named main agent (FR-121).
const unboundChannelReason = "bind this channel to an agent in a workspace"

// routeDropError is resolveMessageRoute's refusal for a channel message that
// must not reach any agent. processMessage — the single rejection point —
// logs it at error level, audits it and records it on the instance.
type routeDropError struct {
	instanceID string
	reason     string
	// bound is true when the instance is bound and its main agent cannot
	// answer (missing, not a chat target, not on the team) — audited as the
	// ADR-029 drift drop; false for an unbound instance.
	bound         bool
	workspaceID   string
	intendedAgent string
}

func (e *routeDropError) Error() string { return "message dropped: " + e.reason }

// routeDropReason maps a dropped route to its visible reason.
func routeDropReason(route routing.ResolvedRoute, identity *config.ChannelIdentity) string {
	agentID := ""
	if identity != nil {
		agentID = strings.TrimSpace(identity.ID)
	}
	switch route.MatchedBy {
	case routing.MatchedByMissingAgent:
		return fmt.Sprintf("bound agent %s not found", agentID)
	case routing.MatchedByNotChatTarget:
		return fmt.Sprintf("bound agent %s is not a chat target", agentID)
	default:
		return unboundChannelReason
	}
}

// boundMemberRefusal returns the drop reason when agentID is not on the core
// team of the bound workspace (FR-121: the selected agent is validated), or
// "" when it is. An unreadable workspace is a drop too — never a guess.
func (al *AgentLoop) boundMemberRefusal(workspaceID, agentID string) string {
	state, err := workspace.ReadState(omnipusHome(), workspaceID)
	if err != nil {
		return fmt.Sprintf("the bound workspace %s could not be read: %v", workspaceID, err)
	}
	ws := state.Workspace
	for _, member := range ws.CoreTeam {
		if routing.NormalizeAgentID(member) == routing.NormalizeAgentID(agentID) {
			return ""
		}
	}
	name := strings.TrimSpace(ws.Name)
	if name == "" {
		name = workspaceID
	}
	return fmt.Sprintf("%s is not in %s", agentID, name)
}

// routeToSessionOwner routes a message addressed to an existing session to
// that session's canonical owner (§6.4). A session that does not exist, or
// whose owner no longer resolves, is refused — never handed to another agent.
func (al *AgentLoop) routeToSessionOwner(registry *AgentRegistry, msg bus.InboundMessage) (routing.ResolvedRoute, *AgentInstance, error) {
	store := al.ResolveSessionStore(msg.SessionID)
	if store == nil {
		return routing.ResolvedRoute{}, nil, fmt.Errorf("session %q not found", msg.SessionID)
	}
	meta, err := store.GetMeta(msg.SessionID)
	if err != nil || meta == nil {
		return routing.ResolvedRoute{}, nil, fmt.Errorf("session %q could not be read: %w", msg.SessionID, err)
	}
	owner := strings.TrimSpace(meta.AgentID)
	agent, ok := registry.GetAgent(owner)
	if owner == "" || !ok || agent == nil {
		return routing.ResolvedRoute{}, nil, fmt.Errorf("the session's owner agent %s no longer exists", owner)
	}
	return routing.ResolvedRoute{AgentID: agent.ID, SessionKey: agentSessionKey(agent.ID, msg), MatchedBy: "session.owner"}, agent, nil
}

// existingSessionOwner returns the stored owner of msg's existing session, or ""
// when msg names no session, is a system message, or its session cannot be
// found or read (the session-owner route then reports that itself).
func (al *AgentLoop) existingSessionOwner(msg bus.InboundMessage) string {
	if msg.SessionID == "" || msg.Channel == "system" {
		return ""
	}
	store := al.ResolveSessionStore(msg.SessionID)
	if store == nil {
		return ""
	}
	meta, err := store.GetMeta(msg.SessionID)
	if err != nil || meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.AgentID)
}

// recordRouteDrop is the single side-effecting record of a dropped channel
// message (FR-121): an error-level log, one audit entry and the instance's
// routing degradation (K15).
func (al *AgentLoop) recordRouteDrop(msg bus.InboundMessage, drop *routeDropError) {
	logger.ErrorCF("agent", "Inbound message dropped: "+drop.reason, map[string]any{
		"instance_id": drop.instanceID, "channel": msg.Channel, "chat_id": msg.ChatID, "reason": drop.reason,
	})
	al.driftDropped.Add(1)
	if al.auditLogger != nil {
		entry := &audit.Entry{
			Event:    audit.EventChannelMessageDropped,
			Decision: audit.DecisionDeny,
			Details: map[string]any{
				"instance_id": drop.instanceID, "channel": msg.Channel, "chat_id": msg.ChatID, "reason": drop.reason,
			},
		}
		if drop.bound {
			// ADR-029 FR-028: a bound instance whose agent cannot answer keeps
			// its drift-drop event and fields.
			entry.Event = audit.EventChannelRoutingDriftDrop
			entry.Details = map[string]any{
				"instance_id": drop.instanceID, "workspace_id": drop.workspaceID,
				"intended_agent_id": drop.intendedAgent, "chat_id": msg.ChatID, "reason": drop.reason,
			}
		}
		if err := al.auditLogger.Log(entry); err != nil {
			logger.ErrorCF("agent", "audit write failed for a dropped inbound message",
				map[string]any{"instance_id": drop.instanceID, "error": err.Error()})
		}
	}
	instance := drop.instanceID
	if instance == "" {
		instance = msg.Channel
	}
	al.RecordRoutingDrop(instance, drop.reason)
}
