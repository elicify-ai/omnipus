package agent

import (
	"context"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

// primaryChatAgentForTest returns a test fixture's primary chat agent: "mia"
// when it is registered as a chat target, else the lexicographically-first
// registered chat-target agent; nil when there is none.
//
// Test-only. Production has no default agent (omnipus-system-agent FR-120);
// every production caller names the agent it means. Fixtures written before
// the retirement addressed "their" one chat agent through the old default
// ladder, and this helper keeps naming that same agent explicitly in the
// test instead of re-deriving it in every file.
func (r *AgentRegistry) primaryChatAgentForTest() *AgentInstance {
	if r == nil {
		return nil
	}
	if mia, ok := r.GetAgent("mia"); ok && mia != nil && mia.IsChatTarget() {
		return mia
	}
	ids := r.ListAgentIDs()
	sort.Strings(ids)
	for _, id := range ids {
		if ag, ok := r.GetAgent(id); ok && ag != nil && ag.IsChatTarget() {
			return ag
		}
	}
	return nil
}

// processDirectPrimaryForTest runs a direct turn as the fixture's primary chat
// agent (see primaryChatAgentForTest). Test-only replacement for the retired
// agent-less ProcessDirect.
func (al *AgentLoop) processDirectPrimaryForTest(ctx context.Context, content, sessionKey string) (string, error) {
	agentID := ""
	if primary := al.GetRegistry().primaryChatAgentForTest(); primary != nil {
		agentID = primary.ID
	}
	return al.ProcessDirectAs(ctx, agentID, content, sessionKey)
}

// processDirectWithChannelPrimaryForTest is processDirectPrimaryForTest on a
// given channel and chat.
func (al *AgentLoop) processDirectWithChannelPrimaryForTest(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	agentID := ""
	if primary := al.GetRegistry().primaryChatAgentForTest(); primary != nil {
		agentID = primary.ID
	}
	return al.ProcessDirectWithChannelAs(ctx, agentID, content, sessionKey, channel, chatID)
}

// directTurnHistoryKeyForTest is the history key a direct turn run as agentID
// on channel/chat uses: the explicit-agent route's key (no session id), the
// same agentSessionKey resolveMessageRoute builds.
func directTurnHistoryKeyForTest(agentID, channel, chatID string) string {
	return agentSessionKey(agentID, bus.InboundMessage{Channel: channel, ChatID: chatID})
}

// processMessageAsPrimaryForTest is processMessage for fixtures whose subject
// is not channel routing: an inbound message that names no agent and no
// session (and is not a system message) is addressed explicitly — to the
// agent its agent-scoped session key names, else to the fixture's primary
// chat agent — the way the retired default agent used to pick it up. Routing
// tests call processMessage directly.
func (al *AgentLoop) processMessageAsPrimaryForTest(ctx context.Context, msg bus.InboundMessage) (string, *AgentInstance, error) {
	return al.processMessage(ctx, al.addressToPrimaryForTest(msg))
}

// resolveMessageRouteAsPrimaryForTest is resolveMessageRoute for the same
// fixtures: the message is addressed exactly as processMessageAsPrimaryForTest
// addresses it, so the route (and its history key) matches the turn's.
func (al *AgentLoop) resolveMessageRouteAsPrimaryForTest(msg bus.InboundMessage) (routing.ResolvedRoute, *AgentInstance, error) {
	return al.resolveMessageRoute(al.addressToPrimaryForTest(msg))
}

// addressToPrimaryForTest names an agent on a message that names none (see
// processMessageAsPrimaryForTest).
func (al *AgentLoop) addressToPrimaryForTest(msg bus.InboundMessage) bus.InboundMessage {
	if msg.Channel == "system" || msg.SessionID != "" || inboundMetadata(msg, "agent_id") != "" {
		return msg
	}
	agentID := ""
	if parsed := routing.ParseAgentSessionKey(msg.SessionKey); parsed != nil {
		agentID = parsed.AgentID
	} else if primary := al.GetRegistry().primaryChatAgentForTest(); primary != nil {
		agentID = primary.ID
	}
	if agentID == "" {
		return msg
	}
	meta := make(map[string]string, len(msg.Metadata)+1)
	for k, v := range msg.Metadata {
		meta[k] = v
	}
	meta["agent_id"] = agentID
	msg.Metadata = meta
	return msg
}
