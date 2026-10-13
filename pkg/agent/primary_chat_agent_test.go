package agent

import (
	"context"
	"sort"
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
