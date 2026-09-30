// task_run_goal_error.go keeps goal-store faults separate from worker failures.
package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// pauseRunForGoalError follows the existing transcript-read / DoD-read pause:
// leave the run open, explain the fault, and spend neither a try nor an attempt.
func (te *TaskExecutor) pauseRunForGoalError(t *task.Task, taskSessionID string, err error) (runStep, string, string) {
	reason := "Task goal processing deferred: " + err.Error()
	logger.ErrorCF("task_executor", "goal_read_error: "+reason,
		map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": err.Error()})
	te.writeTaskReason(t, reason)
	te.appendRunErrorTranscript(t, taskSessionID, te.agentLoop.taskSessionStore(taskSessionID, t.AgentID), reason)
	return runStepEnded, "", ""
}

// continueAfterInnerTry shares the five no-verdict retry branches, including
// their storage-fault refusal. The existing steering text is left unchanged.
func (te *TaskExecutor) continueAfterInnerTry(ctx context.Context, t *task.Task, taskSessionID, why, steering string, store *session.UnifiedStore, run *activeRun, state *taskRunState) (runStep, string, string) {
	exhausted, reason, err := te.spendInnerTry(t, taskSessionID, why, state)
	if err != nil {
		return te.pauseRunForGoalError(t, taskSessionID, err)
	}
	if exhausted {
		return te.failedRunStep(ctx, t, taskSessionID, reason, run)
	}
	te.appendRunSystemTranscript(t, taskSessionID, store, steering)
	return runStepContinue, steering, ""
}
