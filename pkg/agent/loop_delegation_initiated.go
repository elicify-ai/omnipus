// loop_delegation_initiated.go: the one authorization decision for a task or
// plan run an AGENT started (run_task, execute_plan).

package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// delegationBudget is the one budget computation behind both the launcher
// (startingRemainingDepth) and the initiated-run authorizer, so they can never
// disagree: the effective cap (edge depth against the global ceiling, see
// resolveEffectiveDelegationDepth) minus the chain depth already used, further
// capped by an inherited onward budget when the acting session carries one.
func delegationBudget(edgeDepth *int, globalMaxDepth, chainDepth int, inherited *int) int {
	available := resolveEffectiveDelegationDepth(edgeDepth, globalMaxDepth) - chainDepth
	if inherited != nil && *inherited < available {
		available = *inherited
	}
	return available
}

// authorizeInitiatedRun is THE decision for a task or plan-member run that an
// agent's own action started (founder ruling 2026-10-10: the delegation policy
// always applies). The executor calls it for every run that names an agent
// initiator, before anything is written; run_task and execute_plan call it
// earlier as a pre-check so they can refuse before touching the task or plan.
//
// It checks, failing closed at each step:
//  1. the initiator is identified;
//  2. the initiator->assignee edge exists in the TASK's workspace (never the
//     workspace a turn happens to be bound to, so asynchronous plan dispatch is
//     checked against the right graph) — there is NO self-exemption, an agent
//     running its own task needs the self-edge;
//  3. the edge's mode and depth rules, at the initiator's explicit chain depth;
//  4. the onward budget left, capped by any budget the initiator's own session
//     inherited; a run with no budget left is denied.
//
// On success it returns the InitiatedBy the run's lifecycle record must carry.
func (al *AgentLoop) authorizeInitiatedRun(
	ctx context.Context, ini task.Initiator, assigneeAgentID, taskWorkspaceID string,
) (*session.InitiatedBy, *tools.DelegationDenial) {
	deny := func(policy tools.DelegationDenyReason, reason string) (*session.InitiatedBy, *tools.DelegationDenial) {
		return nil, &tools.DelegationDenial{Reason: reason, Policy: policy, TargetAgentID: assigneeAgentID}
	}
	if ini.AgentID == "" {
		return deny(tools.DenyTrustSet, "a task or plan run started by an agent needs an identified initiating agent")
	}
	cfg := al.GetConfig()
	globalMax := 0
	if cfg != nil {
		limit, ok := configuredDelegationDepth(cfg.Performance, "initiated run authorization")
		if !ok {
			return deny(tools.DenyDepth, "delegation is disabled: performance.max_delegation_depth is invalid (must be >= 0)")
		}
		globalMax = limit
	}

	var exists []func(string) bool
	if probe := agentExistsChecker(al.GetRegistry()); probe != nil {
		exists = []func(string) bool{probe}
	}
	edge, denial := findDelegationEdge(tools.WithWorkspaceID(ctx, taskWorkspaceID), ini.AgentID, assigneeAgentID,
		config.DelegationModeTask, exists...)
	if denial != nil {
		return nil, denial
	}
	if denial := enforceEdgeModeAndDepthAt(edge, ini.AgentID, assigneeAgentID, config.DelegationModeTask,
		globalMax, ini.Depth); denial != nil {
		return nil, denial
	}

	var inherited *int
	if ini.SessionID != "" {
		if ls := al.GetSessionLifecycleStore(); ls != nil {
			rec, err := ls.Load(ini.SessionID)
			switch {
			case err == nil:
				if b, ok := rec.OnwardBudget(); ok {
					inherited = &b
				}
			case errors.Is(err, session.ErrLifecycleNotFound):
				// An initiator session with no lifecycle record inherited no budget.
			default:
				// FAIL CLOSED: an unreadable record is not "no restriction".
				logger.WarnCF("agent", "initiated run denied: initiator session record unreadable", map[string]any{
					"agent_id": ini.AgentID, "session_id": ini.SessionID, "error": err.Error(),
				})
				return deny(tools.DenyDepth, "the initiating session's delegation budget cannot be read")
			}
		}
	}
	remaining := delegationBudget(edge.Depth, globalMax, ini.Depth, inherited)
	if remaining <= 0 {
		return deny(tools.DenyDepth, fmt.Sprintf(
			"no onward delegation budget is left for agent %q to start work for %q", ini.AgentID, assigneeAgentID))
	}
	return &session.InitiatedBy{
		AgentID:   ini.AgentID,
		SessionID: ini.SessionID,
		Depth:     ini.Depth + 1,
		Authorization: session.Authorization{
			Mode:           session.AuthorizationModeTask,
			RemainingDepth: remaining - 1,
		},
	}, nil
}

// initiatorFor names the agent that is making the current tool call: agentID is
// the identity the tool was wired with (a specific agent's own instance), never
// a value read back from ctx, which a task run re-stamps with the assignee. The
// session and depth come from the acting turn.
func (al *AgentLoop) initiatorFor(ctx context.Context, agentID string) *task.Initiator {
	return &task.Initiator{
		AgentID:   agentID,
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Depth:     currentDelegationDepth(ctx),
	}
}
