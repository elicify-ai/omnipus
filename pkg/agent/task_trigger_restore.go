package agent

import (
	"fmt"
	"log/slog"
	"reflect"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// restoreStoredJobs reconstructs the volatile task-to-job index BEFORE the cron
// loop starts. Reconcile can then recognize an unchanged, already-armed job
// instead of adding a new one on every boot/reload. Only derived task-trigger
// jobs are deduplicated; task records and other scheduler stores are untouched.
func (s *TaskTriggerScheduler) restoreStoredJobs() error {
	// NewCronService's constructor cannot return its load error. Check it here
	// before any cleanup can overwrite an unreadable store.
	if err := s.cs.Load(); err != nil {
		return fmt.Errorf("task_trigger: restore jobs: %w", err)
	}
	tasks, unreadable, err := s.store.ListWithUnreadable(task.Filter{})
	if err != nil {
		return fmt.Errorf("task_trigger: restore tasks: %w", err)
	}
	byID := make(map[string]*task.Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}
	for _, id := range unreadable {
		// Retain its binding without claiming to know its trigger generation.
		// Reconcile logs unreadable tasks and recovery can retry later; an
		// unreadable definition is not evidence that its schedule was deleted.
		byID[id] = nil
	}
	nowMs := s.now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()

	groups := make(map[string][]cron.CronJob)
	for _, job := range s.cs.ListJobs(true) {
		id := job.Payload.Message
		if id != "" && job.Name == "task:"+id && job.Payload.Kind == "agent_turn" {
			groups[id] = append(groups[id], job)
		}
	}
	bindings := make(map[string]trackedTrigger, len(groups))
	var duplicates []cron.CronJob
	var removeIDs []string
	for id, jobs := range groups {
		t, exists := byID[id]
		if !exists || (t != nil && t.EffectiveSurface() == task.SurfaceHeartbeat) {
			// Reconcile prunes jobs for deleted tasks and heartbeat-owned work.
			// Restoring a binding for either would incorrectly keep it alive.
			continue
		}
		kept := jobs[0] // stable persisted order; preserve the original job/state
		generation := ""
		if t != nil && t.Trigger != nil {
			// An offline trigger/owner edit must not bless a stale job with the
			// current generation. Leaving generation empty makes OnTaskUpserted
			// replace it. Invalid/exhausted triggers are also retired there.
			sched, mapErr := triggerToCronSchedule(t.Trigger, nowMs)
			if mapErr == nil {
				spec := s.jobSpecFor(t, sched)
				for _, job := range jobs {
					if job.Enabled && !job.State.Running && job.AgentID == spec.AgentID &&
						job.Payload.Command == spec.Command && reflect.DeepEqual(job.Schedule, sched) {
						kept = job
						generation = triggerGeneration(t.Trigger)
						break
					}
				}
			}
		}
		bindings[id] = trackedTrigger{jobID: kept.ID, generation: generation}
		for _, job := range jobs {
			if job.ID != kept.ID {
				duplicates = append(duplicates, job)
				removeIDs = append(removeIDs, job.ID)
			}
		}
	}
	if err := s.cs.RemoveJobs(removeIDs); err != nil {
		return fmt.Errorf("task_trigger: remove duplicate jobs: %w", err)
	}
	s.taskToJob = bindings
	for _, job := range duplicates {
		slog.Info("task_trigger: removed duplicate trigger job",
			"task_id", job.Payload.Message, "job_id", job.ID,
			"kept_job_id", bindings[job.Payload.Message].jobID)
	}
	return nil
}
