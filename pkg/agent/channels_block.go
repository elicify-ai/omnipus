package agent

// The per-turn "## Channels" block injector (channels-spec-r8 FR-001–FR-006).

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/steer"
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
		agentInst.ContextBuilder.WithChannelsInjector(func(workspaceID, sessionID string) string {
			return al.renderChannelsBlock(id, workspaceID, sessionID)
		})
	}
}

// renderChannelsBlock builds the block for one turn of agentID in transcript
// session sessionID.
func (al *AgentLoop) renderChannelsBlock(agentID, workspaceID, sessionID string) string {
	in := channels.ChannelsBlockInput{
		Config:            al.GetConfig(),
		AgentID:           agentID,
		WorkspaceID:       workspaceID,
		Delegated:         al.sessionRestrictedToOwnChat(sessionID),
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

// sessionRestrictedToOwnChat reports whether send_message would limit the
// session to its own conversation, using the classification the tool itself
// uses (pkg/tools/message.go::denySteeredSessionOffOwnChat): the injected
// steer.AudienceResolver on the transcript session (FR-004, US-1 AS5,
// S-02). No resolver or no session id means the tool does not restrict
// either. Any answer other than AudienceUser, a resolver error included,
// restricts the block, so it never lists a target the tool would refuse.
// The resolver is read directly, not through audienceFor: rendering the
// block is not a publication boundary, so nothing is observed.
func (al *AgentLoop) sessionRestrictedToOwnChat(sessionID string) bool {
	resolver := al.getSteerAudienceResolver()
	if resolver == nil || sessionID == "" {
		return false
	}
	audience, _, err := resolver.Audience(context.Background(), sessionID)
	if err != nil {
		logger.WarnCF("agent.env", "channels block: cannot classify the session; showing the own-conversation block",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return true
	}
	return audience != steer.AudienceUser
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
