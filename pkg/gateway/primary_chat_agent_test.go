package gateway

import (
	"sort"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// primaryChatAgent returns a test fixture's primary chat agent: "mia" when it
// is registered as a chat target, else the lexicographically-first registered
// chat-target agent; nil when there is none.
//
// Test-only. Production has no default agent (omnipus-system-agent FR-120);
// fixtures written before the retirement addressed "their" one chat agent
// through the old default ladder, and this keeps naming that same agent.
func primaryChatAgent(reg *agent.AgentRegistry) *agent.AgentInstance {
	if reg == nil {
		return nil
	}
	if mia, ok := reg.GetAgent("mia"); ok && mia != nil && mia.IsChatTarget() {
		return mia
	}
	ids := reg.ListAgentIDs()
	sort.Strings(ids)
	for _, id := range ids {
		if ag, ok := reg.GetAgent(id); ok && ag != nil && ag.IsChatTarget() {
			return ag
		}
	}
	return nil
}
