package agent

// The per-turn "## Channels" block injector (channels-spec-r8 FR-001–FR-006).

import (
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// wireChannelsInjectors installs the "## Channels" block callback on every
// agent's ContextBuilder in registry. Each call reads ONE live configuration
// snapshot (the loop's current config, which send_message and dispatch read
// too), one Manager.Availability per listed connection, and Discord kind
// labels from the adapter's local cache only (no network call).
func wireChannelsInjectors(al *AgentLoop, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}
		id := agentID
		agentInst.ContextBuilder.WithChannelsInjector(func(workspaceID string, delegationDepth int) string {
			return al.renderChannelsBlock(id, workspaceID, delegationDepth)
		})
	}
}

// renderChannelsBlock builds the block for one turn of agentID.
func (al *AgentLoop) renderChannelsBlock(agentID, workspaceID string, delegationDepth int) string {
	in := channels.ChannelsBlockInput{
		Config:            al.GetConfig(),
		AgentID:           agentID,
		WorkspaceID:       workspaceID,
		Delegated:         delegationDepth > 0,
		SendMessageDenied: al.sendMessageDeniedFor(agentID),
	}
	if in.WorkspaceID == "" {
		// Same fallback as the delegation block and resolveEffectiveWorkspaceID.
		def, err := workspace.ResolveDefaultID(omnipusHome())
		if err != nil {
			logger.WarnCF("agent.env", "channels block: cannot resolve the default workspace; no connection is listed",
				map[string]any{"agent_id": agentID, "error": err.Error()})
		}
		in.WorkspaceID = def
	}
	if mgr := al.GetChannelManager(); mgr != nil {
		in.Availability = mgr.Availability
		in.CachedGroup = mgr.CachedPostableChannel
	}
	return channels.RenderChannelsBlock(in)
}

// sendMessageDeniedFor reports whether send_message resolves to deny for
// agentID through the two-layer resolver (FR-004). An agent whose policy
// cannot be read fails closed: denied.
func (al *AgentLoop) sendMessageDeniedFor(agentID string) bool {
	reg := al.GetRegistry()
	if reg == nil {
		return true
	}
	inst, ok := reg.GetAgent(agentID)
	if !ok || inst == nil {
		return true
	}
	cfg := inst.LoadToolPolicy()
	if cfg == nil {
		return true
	}
	return tools.ResolveEffectivePolicy(cfg, "send_message") == string(config.ToolPolicyDeny)
}
