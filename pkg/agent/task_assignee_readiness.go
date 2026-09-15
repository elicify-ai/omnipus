// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_assignee_readiness.go answers one question before a task runs, and when
// a task is assigned: can the chosen agent actually FINISH it? (founder
// decision 2026-09-15, "pre-run check").
//
// A task completes only when its worker's claim is upheld by the Judge
// (task_run_loop.go). Two things make that impossible, and both are knowable
// from configuration alone, before any model is called:
//
//   - a native worker whose effective policy DENIES goal_claim can never
//     report the work done. An external-CLI (subagent_3p) worker reports
//     through its completion marker instead (resolveRunClaim), so this part
//     does not apply to it;
//   - a task with a machine check (an acceptance criterion or Definition of
//     Done item of kind "check") needs the check runner: the Judge runs every
//     check through the assignee's own bash tool, and runs it only when that
//     policy is exactly "allow" — "ask" resolves to deny because nobody is
//     there to approve a Judge's check (ADR-049 D2 rule 2,
//     judge.go::runMachineCheck). A check that can never run is never scored
//     met, so the task can never be done.
//
// Nothing fuzzier is inferred: a task's prose is never read for "needs file
// tools". What a task might need beyond this surfaces while it runs.
//
// Where the answer is used, and what each caller does with it:
//
//   - the task run loop (task_run_loop.go::executeTaskRun) ends the task
//     Failed before its first turn: no attempt used, no restart, no model
//     call, one goal outcome line (endTaskWithoutAttempt — the same ending a
//     turn refused for a reason only an operator can fix takes);
//   - the agent tools create_task / update_task (pkg/tools) and
//     create_task_in_workspace / update_task_in_workspace (pkg/sysagent/tools)
//     refuse the write: an agent cannot change another agent's permissions;
//   - REST, the human task form, accepts the write and returns the same text
//     as Task.assignee_warning: an operator may assign first and fix the
//     permission afterwards. That reject-on-agent-paths / warn-in-the-UI split
//     is ADR-049 D2 rule 5's, and planning-goals-spec's "All-machine criteria
//     with unsatisfiable bash policy rejected at write" scenario ("the same
//     shape via the human UI path is accepted with a warning").
package agent

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TaskAssigneeCannotFinish returns why agentID cannot finish a task judged
// against judged — the task's acceptance criteria and Definition of Done
// together — in the words an operator reads, naming the fix; or "" when
// nothing knowable stops the agent. When both problems apply, both sentences
// are returned, so one read names every fix.
//
// An agent that is not registered returns "": every assignment and dispatch
// path already refuses an unknown agent in its own words, and this check must
// not invent a second message for it.
func (al *AgentLoop) TaskAssigneeCannotFinish(agentID string, judged []task.AcceptanceCriterion) string {
	agentID = strings.TrimSpace(agentID)
	if al == nil || agentID == "" {
		return ""
	}
	registry := al.GetRegistry()
	if registry == nil {
		return ""
	}
	inst, ok := registry.GetAgent(agentID)
	if !ok || inst == nil {
		return ""
	}
	name := strings.TrimSpace(inst.Name)
	if name == "" {
		name = agentID
	}

	var problems []string
	// ResolveApprovalToolPolicy is the one authority the tool filter and the
	// exec-time gate both resolve through, so "denied here" means goal_claim is
	// never offered to this agent and never runs for it.
	if !agentRunsOnExternalCLI(inst) &&
		al.ResolveApprovalToolPolicy(agentID, tools.GoalClaimToolName) == string(config.ToolPolicyDeny) {
		problems = append(problems, assigneeCannotClaimText(name))
	}
	if hasMachineCheck(judged) {
		if policy := machineCheckRunnerPolicy(inst); policy != string(config.ToolPolicyAllow) {
			problems = append(problems, assigneeCannotRunChecksText(name, policy))
		}
	}
	return strings.Join(problems, " ")
}

// agentRunsOnExternalCLI reports whether inst's tasks dispatch to an external
// CLI. A dispatch configuration that cannot be resolved is treated as native,
// matching TaskExecutor.dispatchesExternalCLI; such a run is refused before it
// starts anyway (ErrTaskRunNotDispatched).
func agentRunsOnExternalCLI(inst *AgentInstance) bool {
	kind, err := runner.ResolveDispatch(executorConfigOf(inst))
	return err == nil && kind == runner.DispatchKindExternalCLI
}

// machineCheckRunnerPolicy is the policy judge.go::runMachineCheck resolves
// before it runs a check through inst's bash tool — the same call with the same
// scope, so this check and the Judge cannot disagree (pinned by
// TestTaskReadiness_CheckRunnerPolicyMatchesTheJudge).
func machineCheckRunnerPolicy(inst *AgentInstance) string {
	return tools.EffectiveToolPolicy(inst.LoadToolPolicy(), tools.ScopeCore, inst.AgentType, "bash")
}

// hasMachineCheck reports whether judged holds any criterion of kind "check".
func hasMachineCheck(judged []task.AcceptanceCriterion) bool {
	for _, c := range judged {
		if c.Kind == task.KindCheck {
			return true
		}
	}
	return false
}

// assigneeCannotClaimText is the founder's wording for an agent denied
// goal_claim.
func assigneeCannotClaimText(name string) string {
	return name + " isn't allowed to report tasks as done. " +
		"Allow 'goal_claim' for it in Agents → Tools, or assign another agent."
}

// assigneeCannotRunChecksText words an agent whose bash policy cannot run the
// task's checks, saying why "Ask" is not enough.
func assigneeCannotRunChecksText(name, policy string) string {
	return fmt.Sprintf("%s can't run this task's checks. Checks run with nobody there to approve them, "+
		"so its 'bash' tool must be set to Allow, and it is set to %s. "+
		"Allow 'bash' for it in Agents → Tools, or assign another agent.", name, policyLabel(policy))
}

// policyLabel renders a resolved policy the way the Tools tab labels it.
func policyLabel(policy string) string {
	switch policy {
	case string(config.ToolPolicyAsk):
		return "Ask"
	case string(config.ToolPolicyDeny):
		return "Deny"
	}
	return fmt.Sprintf("%q", policy)
}

// preRunCannotFinishReason is the pre-run check for t's run bound to
// taskSessionID. The judged set is the one adjudicateRunClaim judges: the
// criteria on the run's goal record (else the task's own) plus the record's
// Definition of Done. A checklist card (Scratchpad) never enters the goal loop
// and is not checked.
//
// A Definition of Done that cannot be read is left out, with a warning: this
// check acts only on what it can know, and the adjudication fails closed on
// that same read if the run ever gets that far.
func (te *TaskExecutor) preRunCannotFinishReason(t *task.Task, taskSessionID string) string {
	if t == nil || t.Scratchpad || te.agentLoop == nil {
		return ""
	}
	var judged []task.AcceptanceCriterion
	if rec := activeGoalForSession(taskSessionID); rec != nil && len(rec.Criteria) > 0 {
		judged = append(judged, rec.Criteria...)
	} else {
		judged = append(judged, t.Criteria...)
	}
	dod, err := taskGoalDoD(t.ID)
	if err != nil {
		logger.WarnCF("task_executor",
			"pre-run check: the task's Definition of Done could not be read — checking its acceptance criteria only",
			map[string]any{"task_id": t.ID, "error": err.Error()})
	} else {
		judged = append(judged, dod...)
	}
	return te.agentLoop.TaskAssigneeCannotFinish(t.AgentID, judged)
}

// endTaskAssigneeCannotFinish ends a task whose agent cannot finish it, before
// the run's first turn. The reason is written into the run's transcript as an
// error entry so the run explains itself, the session lifecycle records an
// operator-fix failure, and the task ends Failed through endTaskWithoutAttempt
// — no attempt used, no restart, exactly one goal outcome line — the same
// ending finishRunTurn gives a turn refused for a reason only an operator can
// fix.
func (te *TaskExecutor) endTaskAssigneeCannotFinish(t *task.Task, taskSessionID, reason string, run *activeRun) {
	logger.WarnCF("task_executor",
		"task run: the assigned agent cannot finish this task as configured — failing it before its first turn, no attempt used",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "reason": reason})
	te.appendRunErrorTranscript(t, taskSessionID, te.agentLoop.GetAgentStore(t.AgentID), reason)
	te.transitionTaskLifecycle(taskSessionID, session.LifecycleFailed, "operator_action_required")
	te.endTaskWithoutAttempt(t, taskSessionID, reason, run)
}
