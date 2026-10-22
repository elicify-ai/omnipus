package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// taskExecutionInterrupted carries an actual native outcome across the legacy
// text/error task entry. It is neither a provider failure nor a request to retry.
type taskExecutionInterrupted struct{ result turnResult }

func (e *taskExecutionInterrupted) Error() string {
	return "task execution interrupted: " + string(e.result.interruptionReason)
}

func taskTurnResponse(result turnResult, taskRun bool) (string, error) {
	if taskRun && result.interruptionReason != "" {
		return result.finalContent, &taskExecutionInterrupted{result: result}
	}
	return result.finalContent, nil
}

func (te *TaskExecutor) endInterruptedTaskExecution(ctx context.Context, id string, result turnResult) error {
	d := taskExecutionFor(ctx, id)
	if d == nil {
		return fmt.Errorf("task interruption: admitted execution identity is unavailable")
	}
	d.recordTurnOutcome(nil)
	if !d.steered {
		// The ordinary outer release settles Interrupted under the existing
		// Stop/admission lock, after retiring its tail. Do not bypass that race.
		return nil
	}
	rec, err := te.agentLoop.GetSessionLifecycleStore().Load(id)
	if err != nil {
		return err
	}
	return te.agentLoop.completeSteeredTurnForExecution(ctx, rec, result, nil, d.claim)
}

func (al *AgentLoop) reportTaskInterruptionFailure(id string, cause error) {
	const text = "The execution was interrupted, but its saved ending or notice delivery could not be confirmed. It was not automatically restarted. Repair the reported storage problem before continuing."
	logger.ErrorCF("task_executor", "interruption ending is unconfirmed", map[string]any{"session_id": id, "error": cause.Error()})
	al.emitEvent(EventKindError, EventMeta{SessionKey: id, Source: "task_executor", TracePath: "task.interruption.not_saved"},
		ErrorPayload{Stage: "recovery", SessionID: id, Message: text})
	if lc := al.GetSessionLifecycleStore(); lc != nil {
		if rec, err := lc.Load(id); err == nil && rec.SteeredBy != nil {
			if err := al.deliverSteeredNotice(context.Background(), rec, steer.OutcomeLifecycleNotice, "", text); err != nil {
				logger.ErrorCF("task_executor", "interruption storage notice could not reach the parent", map[string]any{"session_id": id, "error": err.Error()})
			}
		}
	}
}

func taskInterruptedResult(err error) (turnResult, bool) {
	var interrupted *taskExecutionInterrupted
	if !errors.As(err, &interrupted) {
		return turnResult{}, false
	}
	return interrupted.result, true
}

func taskDispositionInterrupted(d *executionDisposition) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.interruptionReason == session.InterruptionToolLimit || d.interruptionReason == session.InterruptionReconciliationRequired
}
