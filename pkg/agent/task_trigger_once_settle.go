// task_trigger_once_settle.go: keep a one-time trigger's job until its task
// run settles (6b U2b, architect option (a)).
//
// Task dispatch is asynchronous, so RunScheduled returns while the fired task
// run is still going. Without this, the engine deleted the one-time job at
// that return, and a gateway crash or shutdown during the run left nothing for
// the physical-boot replay (cron.InterruptedOneTimeFire, markBootOwedFires) to
// find. Now RunScheduled returns cron.ErrRunHandedOff for a dispatched once
// fire: the job stays with its Running flag persisted, and the executor
// settles it when the run ends.
//
//   - The run ends on its own (any outcome, including a person's Stop or task
//     cancel): the job is removed, exactly what the engine did before.
//   - The gateway stops the scheduler while the run is going (orderly shutdown
//     or crash): the run ends as interrupted (InterruptedTaskResult) and the
//     job is kept, so the next physical boot replays it once through the
//     unchanged #1484 boot check.
//
// Only this scheduler's own Stop can mark a run interrupted; a person's Stop
// always wins (onceRunInterruptedByGatewayStop).

package agent

import (
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// handOffOnceRun records that jobID's fire was handed off to taskID's run.
func (s *TaskTriggerScheduler) handOffOnceRun(taskID, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onceRuns == nil {
		s.onceRuns = make(map[string]string)
	}
	s.onceRuns[taskID] = jobID
}

// forgetOnceRun drops a hand-off whose dispatch failed; the engine then
// records the failure and deletes the job as before.
func (s *TaskTriggerScheduler) forgetOnceRun(taskID, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onceRuns[taskID] == jobID {
		delete(s.onceRuns, taskID)
	}
}

// onceRunHandedOff decides, after a successful dispatch, whether jobID's fire
// is handed off (RunScheduled returns cron.ErrRunHandedOff). It is when the
// executor holds taskID's run, which settles it at its end, or when that run
// has already settled it (the hand-off is gone). Otherwise nothing will ever
// settle the fire (no executor, or a dispatch that started no held run): the
// hand-off is dropped and the engine records the fire and deletes the job as
// before.
//
// The order matters: a run settles BEFORE it releases its executor slot, so
// "not held" read first and "still pending" read second means no run ever
// held this fire, never that one ended in between.
func (s *TaskTriggerScheduler) onceRunHandedOff(taskID, jobID string) bool {
	if s.executor != nil && s.executor.holdsRun(taskID) {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onceRuns[taskID] != jobID {
		return true // the run already ended and settled the job
	}
	delete(s.onceRuns, taskID)
	return false
}

// stoppingDuringOnceRun reports whether Stop has begun while taskID's
// handed-off one-time run is still going.
func (s *TaskTriggerScheduler) stoppingDuringOnceRun(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.onceRuns[taskID]
	return ok && s.stopping
}

// settleOnceRun is called when taskID's run has ended. A run the gateway
// interrupted (the task saved as failed with InterruptedTaskResult) keeps its
// job, Running still persisted, for the next physical boot's replay. Every
// other outcome removes the job.
//
// The job is removed by its own id: a trigger edited during the run replaced
// it with a new job, which this never touches.
func (s *TaskTriggerScheduler) settleOnceRun(taskID string) {
	s.mu.Lock()
	jobID, ok := s.onceRuns[taskID]
	delete(s.onceRuns, taskID)
	s.mu.Unlock()
	if !ok {
		return
	}
	t, err := s.store.Get(taskID)
	if err == nil && t.Status == task.StatusFailed && t.Result == InterruptedTaskResult {
		slog.Info("task_trigger: one-time run interrupted by the gateway stopping; job kept for the next boot",
			"task_id", taskID, "job_id", jobID)
		return
	}
	if err != nil {
		slog.Warn("task_trigger: one-time run settled but its task is unreadable; removing its job",
			"task_id", taskID, "job_id", jobID, "error", err)
	}
	s.cs.RemoveJob(jobID)
}

// settleTriggeredOnceRun settles a trigger's handed-off one-time fire when
// taskID's run has ended: no goal-loop redispatch follows (redispatchTaskID
// empty; a redispatched attempt settles at its own end). It runs before the
// run releases its executor slot, so a fire still deciding its hand-off sees
// the run held (TaskTriggerScheduler.onceRunHandedOff). A no-op for a task
// with no handed-off fire.
func (te *TaskExecutor) settleTriggeredOnceRun(taskID, redispatchTaskID string) {
	if te.agentLoop == nil || redispatchTaskID != "" {
		return
	}
	if sched := te.agentLoop.taskTriggerScheduler(); sched != nil {
		sched.settleOnceRun(taskID)
	}
}

// stoppedTurnReason is the result a stopped turn writes on its task:
// "Stopped: <why>", except when the gateway stopping (not a person)
// interrupted a one-time fire's run, which is left interrupted so the next
// boot replays it once.
func (te *TaskExecutor) stoppedTurnReason(taskID, taskSessionID string, turnErr error) string {
	if te.onceRunInterruptedByGatewayStop(taskID, taskSessionID) {
		return InterruptedTaskResult
	}
	return "Stopped: " + turnErrorUserText(turnErr)
}

// onceRunInterruptedByGatewayStop reports whether a stopped turn of taskID's
// run is the gateway stopping, not a person: the trigger scheduler began
// Stop while this one-time fire's run was going, and no Stop of a person or
// agent is recorded (no lifecycle Stop note, no task cancel reason). An
// unreadable record counts as a person's Stop, so a run is never made
// replayable on doubt.
func (te *TaskExecutor) onceRunInterruptedByGatewayStop(taskID, taskSessionID string) bool {
	if te.agentLoop == nil {
		return false
	}
	sched := te.agentLoop.taskTriggerScheduler()
	if sched == nil || !sched.stoppingDuringOnceRun(taskID) {
		return false
	}
	if ls := te.recordStore(); ls != nil && taskSessionID != "" {
		rec, err := ls.Load(taskSessionID)
		if err != nil || rec == nil || rec.StopNote != nil {
			return false
		}
	}
	cur, err := te.store.Get(taskID)
	return err == nil && cur.CancelReason == ""
}
