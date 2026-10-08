// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// A task run is one admitted outer execution: the many turns of its run loop
// (task_run_loop.go::executeTaskRun) share one execution disposition, owned by
// the dispatcher that started the run and finished by the run goroutine's
// release callback after the run's last write. That is what lets the one Stop
// (retainSelectedStop) find the run's barrier, interrupt its live turn, and
// land `stopped` after the task executor's own tail has finished.
//
// Two dispatchers admit a task run:
//   - the steering launcher's task front (steer_launcher.go::
//     dispatchSteeredSessionWithReservation), which already holds a steered
//     reservation and attaches the steered owner to it; and
//   - TaskExecutor.executeTask (a Calendar tick, Run now, the queue drain, a
//     plan member), which admits the freshly minted task session as an
//     ordinary execution (admitTaskRun).
//
// Either way the owner reaches processTaskDirect through the context
// (withTaskExecution), which binds it to each turn it builds.

type taskExecutionCtxKey struct{}

// withTaskExecution carries a task run's execution owner to its turns. A nil
// owner (no lifecycle store wired) leaves ctx unchanged.
func withTaskExecution(ctx context.Context, d *executionDisposition) context.Context {
	if d == nil {
		return ctx
	}
	return context.WithValue(ctx, taskExecutionCtxKey{}, d)
}

// taskExecutionFor returns the owner carried on ctx only when it belongs to
// the session the turn writes to (the task's own transcript session). Another
// session's turn that happens to run under the same context — the Judge's
// adjudication turn — never binds to the task run's execution.
func taskExecutionFor(ctx context.Context, taskSessionID string) *executionDisposition {
	d, _ := ctx.Value(taskExecutionCtxKey{}).(*executionDisposition)
	if d == nil || taskSessionID == "" || d.claim.SessionID != taskSessionID {
		return nil
	}
	return d
}

// ownsTaskBarrier reports whether a task run it dispatches owns an execution:
// only when a lifecycle store is wired, because only then was a record minted
// for the run's session and there is a barrier for a Stop to find.
func (te *TaskExecutor) ownsTaskBarrier() bool {
	return te.agentLoop != nil && te.getLifecycleStore() != nil
}

// admitTaskRun admits taskSessionID — the session executeTask just minted —
// as an ordinary execution: a fresh run identity stamped on its lifecycle
// record and an owner attached to the admission scope. It does not take a
// steered slot or a worker lease, and it never revives anything (a nil
// revival principal): the session is brand new. Callers check ownsTaskBarrier
// first.
func (te *TaskExecutor) admitTaskRun(ctx context.Context, taskSessionID string) (*executionDisposition, error) {
	preparation, err := te.agentLoop.prepareOrdinarySessionExecution(ctx, taskSessionID, processOptions{}, nil)
	if err != nil {
		return nil, fmt.Errorf("task_executor: admit task run %q: %w", taskSessionID, err)
	}
	return preparation.execution, nil
}

// finishTaskRun is the run goroutine's release callback for a run that owns an
// execution: it finishes the owner (retiring its slot and landing a selected
// Stop) and then runs after, if any. A failure to land the Stop is reported to
// the stopping conversation by finishExecutionDisposition itself
// (reportStopSettlementFailure); it is logged here for the operator as well.
func (te *TaskExecutor) finishTaskRun(d *executionDisposition, taskID string, after func()) func() {
	return func() {
		if err := te.agentLoop.finishExecutionDisposition(d); err != nil {
			logger.ErrorCF("task_executor", "task run: finishing its execution failed — a Stop may not have landed",
				map[string]any{"task_id": taskID, "session_id": d.claim.SessionID, "run_id": d.claim.RunID, "error": err.Error()})
		}
		if after != nil {
			after()
		}
	}
}
