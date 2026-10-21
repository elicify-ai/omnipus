package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// activateGoalFromTool reuses /goal's activation, including its admission cap,
// routing and normal goal_status emission. It does not manufacture a command
// or a human message, and an already-active goal is never replaced here.
func (al *AgentLoop) activateGoalFromTool(ctx context.Context, sessionID, definition string) error {
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return fmt.Errorf("goal activation: session %q is not available", sessionID)
	}
	active, err := activeGoalForSession(sessionID)
	if err != nil {
		return err
	}
	if active != nil {
		return nil
	}
	agentID := tools.ToolAgentID(ctx)
	agent, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || agent == nil {
		return fmt.Errorf("goal activation: calling agent %q is unavailable", agentID)
	}
	opts := processOptions{
		SessionKey: tools.ToolSessionKey(ctx), Channel: tools.ToolChannel(ctx), ChatID: tools.ToolChatID(ctx),
		TranscriptStore: store, TranscriptSessionID: sessionID, WorkspaceID: tools.ToolWorkspaceID(ctx),
	}
	if pe := GetPlanEngine(al); pe != nil {
		if admitted, activeCount, capN := pe.Admit("goal"); !admitted {
			return fmt.Errorf("cannot start a new goal: active loops %d/%d (cap reached)", activeCount, capN)
		}
	}
	return al.activateInstantGoal(sessionID, &opts, agent, definition)
}
