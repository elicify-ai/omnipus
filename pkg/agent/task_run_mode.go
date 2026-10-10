// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_mode.go: how a task run is started under session-core U6
// (FR-017 MAIN half, FR-018, FR-019, FR-034). There is no user-facing session
// mode: the mode is DERIVED from who runs the task.
//
//   - MAIN: the assignee agent owns an eligible main and run_isolated is off.
//     Each run is a fresh, real child of THAT main (so grants and the per-chat
//     Auto setting inherit from the main, never from whichever chat started it).
//   - ISOLATED: run_isolated is on, or there is no eligible main. A fresh,
//     parentless chat per run.
//   - CONTINUE: a recurring worker keeps its own chat (the cron/schedule side).
//
// The run's recipients — the starter agent's main and the assignee's main,
// deduplicated, never the creator — are captured when the run actually starts
// and stored on its TaskRun record (FR-019).

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// MainSessionResolver tells the executor which (workspace, agent) pairs
// currently own a visible main. It is injected by the gateway, which owns main
// eligibility. A read error is NEVER "not eligible": for the assignee the run is
// refused, and for a starter or recipient the address is omitted with an ERROR
// log and nothing is substituted.
type MainSessionResolver interface {
	// EligibleMain returns the computed main id when (workspaceID, agentID)
	// currently owns a visible main.
	EligibleMain(workspaceID, agentID string) (sessionID string, ok bool, err error)
}

// SetMainSessionResolver installs the resolver (nil leaves every run ISOLATED).
func (te *TaskExecutor) SetMainSessionResolver(r MainSessionResolver) {
	if te != nil {
		te.mainResolver = r
	}
}

// taskRunMode is the derived mode of one run.
type taskRunMode int

const (
	taskRunIsolated taskRunMode = iota
	taskRunMain
	taskRunContinue
)

// deriveTaskRunMode derives a run's mode (FR-017). run_isolated forces
// ISOLATED for either role; otherwise an eligible assignee main means MAIN;
// otherwise the run is a fresh independent ISOLATED chat. CONTINUE (a recurring
// worker keeping its own chat) belongs to the schedule runner, not to task
// dispatch, so it is never derived here.
func deriveTaskRunMode(t *task.Task, assigneeMainOK bool) taskRunMode {
	if t == nil || t.RunIsolated {
		return taskRunIsolated
	}
	if assigneeMainOK {
		return taskRunMain
	}
	return taskRunIsolated
}

// assigneeMain resolves the assignee's eligible main. (id, false, nil) means
// the assignee has none (a worker, or a hidden/removed membership); an error
// means the answer could not be read and the caller must refuse the run.
func (te *TaskExecutor) assigneeMain(t *task.Task) (string, bool, error) {
	if te == nil || te.mainResolver == nil || t == nil || t.AgentID == "" || t.WorkspaceID == "" {
		return "", false, nil
	}
	id, ok, err := te.mainResolver.EligibleMain(t.WorkspaceID, t.AgentID)
	if err != nil {
		return "", false, fmt.Errorf("task_executor: resolve the main of assignee %q in workspace %q: %w",
			t.AgentID, t.WorkspaceID, err)
	}
	return id, ok && id != "", nil
}

// captureRunRecipients is the FR-019 capture: the starter agent's eligible main
// (when an agent started the run) and the assignee's eligible main, in that
// order, deduplicated. The creator is never used. A person, a Calendar fire,
// the queue drain or a dependency contribute no starter. The starter is the
// agent recorded as the run's authorized initiator (never read from ctx: a task
// run re-stamps ctx with the assignee, so "who is calling" there is wrong), so
// "starter" and "initiator" are the same fact. An address whose read fails or
// that is not eligible is omitted (ERROR logged for a read failure) and nothing
// is substituted.
func (te *TaskExecutor) captureRunRecipients(ctx context.Context, t *task.Task, initiatedBy *session.InitiatedBy) []string {
	if te == nil || te.mainResolver == nil || t == nil {
		return nil
	}
	var out []string
	add := func(id string) {
		for _, have := range out {
			if have == id {
				return
			}
		}
		out = append(out, id)
	}
	lookup := func(role, workspaceID, agentID string) {
		if workspaceID == "" || agentID == "" {
			return
		}
		id, ok, err := te.mainResolver.EligibleMain(workspaceID, agentID)
		if err != nil {
			logger.ErrorCF("task_executor", "run recipient omitted: its main could not be resolved",
				map[string]any{"task_id": t.ID, "role": role, "agent_id": agentID, "error": err.Error()})
			return
		}
		if ok && id != "" {
			add(id)
		}
	}
	starterWorkspace := tools.ToolWorkspaceID(ctx)
	if starterWorkspace == "" {
		starterWorkspace = t.WorkspaceID
	}
	if initiatedBy != nil {
		lookup("starter", starterWorkspace, strings.TrimSpace(initiatedBy.AgentID))
	}
	lookup("assignee", t.WorkspaceID, t.AgentID)
	return out
}

// runRecipientsKey carries a run's captured recipients across the detach from
// the caller's context (StartTaskNow runs the task on a fresh root context).
type runRecipientsKey struct{}

func withRunRecipients(ctx context.Context, recipients []string) context.Context {
	if len(recipients) == 0 {
		return ctx
	}
	return context.WithValue(ctx, runRecipientsKey{}, append([]string(nil), recipients...))
}

func runRecipientsFrom(ctx context.Context) []string {
	v, _ := ctx.Value(runRecipientsKey{}).([]string)
	return v
}

// launchMainTaskRun starts one MAIN run: a fresh real child of the assignee's
// main (the steering session), bound to the task, with its run opened under the
// captured recipients before the child is dispatched. Returns the child's
// session id. A dispatch failure closes the opened run as failed.
func (te *TaskExecutor) launchMainTaskRun(ctx context.Context, t *task.Task, occurrenceMs *int64, kind task.RunKind, initiatedBy *session.InitiatedBy) (string, error) {
	if te == nil || te.launcher == nil {
		return "", errors.New("task_executor: no session launcher configured")
	}
	mainID, ok, err := te.assigneeMain(t)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("task_executor: assignee %q has no eligible main in workspace %q", t.AgentID, t.WorkspaceID)
	}
	recipients := te.captureRunRecipients(ctx, t, initiatedBy)

	launched, err := te.launcher.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: mainID,
		TargetAgentID:     t.AgentID,
		Label:             t.Title,
		Task:              te.buildPrompt(t),
		Origin:            steer.Origin{Kind: steer.OriginKindTask, CallID: t.OriginCallID, TaskID: t.ID},
		PlanID:            t.PlanID,
		Initiator:         initiatedBy,
	})
	if err != nil {
		return "", te.mapLaunchRefusal(t, mainID, "launch", err)
	}
	updated, err := te.store.Update(t.ID, task.Patch{SessionID: &launched.SessionID})
	if err != nil {
		return "", fmt.Errorf("task_executor: persist the main run's session id: %w", err)
	}
	_ = te.activateTaskGoal(updated, launched.SessionID)
	run := te.openRun(t.ID, occurrenceMs, kind, launched.SessionID, recipients...)
	if _, err := te.launcher.Dispatch(ctx, launched.SessionID, launched.Generation); err != nil {
		te.closeRun(t.ID, run, task.StatusFailed, "dispatch refused: "+err.Error())
		return "", te.mapLaunchRefusal(t, launched.SessionID, "dispatch", err)
	}
	return launched.SessionID, nil
}

// mapLaunchRefusal turns a launcher refusal into the task surface's error: the
// plain steering sentence for an unavailable conversation (cause kept
// identifiable), otherwise a wrapped error.
func (te *TaskExecutor) mapLaunchRefusal(t *task.Task, sessionID, stage string, err error) error {
	if steer.IsSteeringUnavailable(err) {
		logger.ErrorCF("agent", "adr093: task "+stage+" refused (session not available)",
			map[string]any{"task_id": t.ID, "session_id": sessionID, "error": err.Error()})
		return newSteeringRefusalError(err)
	}
	return fmt.Errorf("task_executor: main run %s: %w", stage, err)
}
