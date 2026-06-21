// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — task time-trigger executor.
//
// TaskTriggerScheduler wires the unified pkg/task store's Trigger field to the
// existing pkg/cron engine by maintaining a SECOND, dedicated CronService
// instance (separate store file: <home>/tasks_triggers/jobs.json). A second
// instance, rather than co-mingling task-trigger jobs in the schedules service,
// keeps the two systems orthogonal: schedules are user-managed chat turns;
// task triggers are engine-managed task re-fires.
//
// Reuse contract: this file imports pkg/cron directly and delegates ALL
// scheduling state to it. There is NO second scheduler implementation here.
//
// Boot wiring (done by the gateway, NOT this file):
//
//	s := agent.NewTaskTriggerScheduler(storePath, taskStore, executor)
//	s.Start()      // SetRunner(s) + cs.Start()
//	s.Reconcile()  // register jobs for all existing triggered tasks
//
// On task create/update: s.OnTaskUpserted(t)
// On task delete:        s.OnTaskDeleted(taskID)
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/task"
)

// taskTriggerOwnerSentinel is the AgentID written to cron jobs that belong to
// tasks without an AgentID. The cron engine's owner-missing guard (executeJobByID)
// skips a job when a runner IS set AND job.AgentID == "". Since TaskTriggerScheduler
// is the runner and does NOT require an owner (it looks up the task by ID from the
// payload), we must supply a non-empty sentinel to bypass that guard while still
// routing dispatch through our RunScheduled method.
const taskTriggerOwnerSentinel = "task-trigger"

// TaskTriggerScheduler registers time-trigger jobs in a dedicated CronService
// and fires them by resetting and dispatching the task via TaskExecutor.
//
// It implements cron.ScheduledRunner — the cron engine calls RunScheduled for
// each due job, and RunScheduled extracts the task ID from the job payload.
type TaskTriggerScheduler struct {
	cs       *cron.CronService
	store    *task.Store
	dispatch func(ctx context.Context, taskID string) error

	mu        sync.Mutex
	taskToJob map[string]string // taskID → cronJobID
}

// NewTaskTriggerScheduler creates a scheduler that uses a dedicated CronService
// with its store at storePath. The executor's SpawnTriggeredRun is used as the
// dispatch function unless overridden (test seam: replace dispatch before Start).
func NewTaskTriggerScheduler(storePath string, store *task.Store, executor *TaskExecutor) *TaskTriggerScheduler {
	s := &TaskTriggerScheduler{
		cs:        cron.NewCronService(storePath),
		store:     store,
		taskToJob: make(map[string]string),
	}
	if executor != nil {
		s.dispatch = executor.SpawnTriggeredRun
	}
	return s
}

// Start wires this scheduler as the cron runner and starts the engine.
// Must be called once at boot, before Reconcile.
func (s *TaskTriggerScheduler) Start() error {
	s.cs.SetRunner(s)
	return s.cs.Start()
}

// Stop shuts down the underlying cron engine.
func (s *TaskTriggerScheduler) Stop() {
	s.cs.Stop()
}

// SetClock passes a test clock through to the underlying CronService.
// Must be called before Start.
func (s *TaskTriggerScheduler) SetClock(c cron.Clock) {
	s.cs.SetClock(c)
}

// RunDueJobs calls the underlying CronService.RunDueJobs with the given time.
// Exported for deterministic testing: tests that inject a fake Clock drive
// dispatch by calling RunDueJobs(clk.Now()) directly, bypassing wall-clock sleep.
func (s *TaskTriggerScheduler) RunDueJobs(now time.Time) {
	s.cs.RunDueJobs(now)
}

// WaitForLane blocks until all in-flight cron lane runs complete.
// Used in tests after RunDueJobs to wait for dispatch to finish.
func (s *TaskTriggerScheduler) WaitForLane() {
	s.cs.WaitForLane()
}

// Reconcile scans the task store and ensures every triggered, non-terminal,
// non-heartbeat task has a registered cron job, and removes stale jobs for tasks
// that no longer need one. Call once at boot, after Start.
func (s *TaskTriggerScheduler) Reconcile() error {
	tasks, err := s.store.List(task.Filter{})
	if err != nil {
		return fmt.Errorf("task_trigger: reconcile list: %w", err)
	}
	for i := range tasks {
		s.OnTaskUpserted(&tasks[i])
	}

	// Prune cron jobs whose task IDs are no longer in the map (deleted tasks or
	// tasks that had their trigger removed between restarts).
	s.mu.Lock()
	registeredTaskIDs := make(map[string]bool, len(s.taskToJob))
	for tID := range s.taskToJob {
		registeredTaskIDs[tID] = true
	}
	s.mu.Unlock()

	existingJobs := s.cs.ListJobs(true)
	for _, job := range existingJobs {
		// Each job's task ID is stored in the payload Message.
		taskID := job.Payload.Message
		if taskID == "" {
			s.cs.RemoveJob(job.ID)
			continue
		}
		if !registeredTaskIDs[taskID] {
			// Either OnTaskUpserted did not register it (no trigger / terminal / heartbeat)
			// or the task was deleted. Remove the orphan.
			s.cs.RemoveJob(job.ID)
		}
	}
	return nil
}

// OnTaskUpserted is called whenever a task is created or updated. It registers,
// replaces, or removes the task's trigger job accordingly.
//
// A job is NOT registered when:
//   - trigger is nil or type==manual (no scheduled firing)
//   - task is terminal (done/failed)
//   - task surface is heartbeat (the heartbeat service owns those recurring fires;
//     registering here would cause double-firing)
func (s *TaskTriggerScheduler) OnTaskUpserted(t *task.Task) {
	// Heartbeat-surface tasks: the per-agent heartbeat service (pkg/heartbeat)
	// owns their periodic execution. Registering a cron job here would cause
	// double-firing. Skip unconditionally regardless of trigger type.
	if t.EffectiveSurface() == task.SurfaceHeartbeat {
		slog.Debug("task_trigger: skipping heartbeat-surface task", "task_id", t.ID)
		return
	}

	noTrigger := t.Trigger == nil || t.Trigger.Type == task.TriggerManual
	terminal := task.IsTerminal(t.Status)

	if noTrigger || terminal {
		// Remove any existing job for this task (trigger was cleared or task ended).
		s.OnTaskDeleted(t.ID)
		return
	}

	sched, err := triggerToCronSchedule(t.Trigger)
	if err != nil {
		slog.Warn("task_trigger: cannot map trigger to cron schedule, skipping",
			"task_id", t.ID, "trigger_type", t.Trigger.Type, "error", err)
		return
	}

	// Remove any existing job for this task (replace on update).
	s.OnTaskDeleted(t.ID)

	// AgentID owner sentinel: cron's executeJobByID skips a job when a runner IS
	// set AND job.AgentID == "". TaskTriggerScheduler is the runner and does not
	// require an owner — it uses the task ID from the payload. When the task has
	// no assigned agent, use the sentinel so the owner-missing guard does not
	// suppress the fire.
	ownerID := t.AgentID
	if ownerID == "" {
		ownerID = taskTriggerOwnerSentinel
	}

	job, err := s.cs.AddJobFull(cron.JobSpec{
		Name:     fmt.Sprintf("task:%s", t.ID),
		Schedule: sched,
		// Message carries the task ID so RunScheduled can look it up.
		Message: t.ID,
		AgentID: ownerID,
		// DeleteAfterRun is handled inside cron for "at" schedules automatically.
	})
	if err != nil {
		slog.Error("task_trigger: failed to register cron job",
			"task_id", t.ID, "error", err)
		return
	}

	s.mu.Lock()
	s.taskToJob[t.ID] = job.ID
	s.mu.Unlock()

	slog.Info("task_trigger: registered trigger job",
		"task_id", t.ID, "job_id", job.ID, "kind", sched.Kind)
}

// OnTaskDeleted removes the cron job associated with taskID, if any.
func (s *TaskTriggerScheduler) OnTaskDeleted(taskID string) {
	s.mu.Lock()
	jobID, ok := s.taskToJob[taskID]
	if ok {
		delete(s.taskToJob, taskID)
	}
	s.mu.Unlock()

	if ok {
		removed := s.cs.RemoveJob(jobID)
		slog.Debug("task_trigger: removed trigger job",
			"task_id", taskID, "job_id", jobID, "was_present", removed)
	}
}

// RunScheduled implements cron.ScheduledRunner. The cron engine calls this for
// each due task-trigger job. It extracts the task ID from the job payload,
// validates the task still needs firing, resets it to `next` via SpawnReset, and
// dispatches it via the dispatch function (defaulting to executor.SpawnTriggeredRun).
func (s *TaskTriggerScheduler) RunScheduled(ctx context.Context, job *cron.CronJob) (string, error) {
	taskID := job.Payload.Message
	if taskID == "" {
		slog.Warn("task_trigger: RunScheduled called with empty task ID in payload",
			"job_id", job.ID)
		s.cs.RemoveJob(job.ID)
		return "", nil
	}

	t, err := s.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			// Task was deleted after the job was registered — remove the orphan.
			slog.Info("task_trigger: task not found, removing orphan job",
				"task_id", taskID, "job_id", job.ID)
			s.OnTaskDeleted(taskID)
			s.cs.RemoveJob(job.ID)
			return "", nil
		}
		return "", fmt.Errorf("task_trigger: get task %q: %w", taskID, err)
	}

	// Remove the job if the task no longer has a time trigger (trigger was cleared
	// or changed to manual after the job was registered).
	if t.Trigger == nil || t.Trigger.Type == task.TriggerManual {
		slog.Info("task_trigger: task trigger removed, cleaning up job",
			"task_id", taskID, "job_id", job.ID)
		s.OnTaskDeleted(taskID)
		s.cs.RemoveJob(job.ID)
		return "", nil
	}

	// Reset the task to `next` for a fresh run.
	fresh, err := s.store.SpawnReset(taskID)
	if err != nil {
		if errors.Is(err, task.ErrAlreadyRunning) {
			// Task is in_progress — overlap guard fires. Benign skip: the next
			// interval will re-fire. Do not stomp an in-flight run.
			slog.Info("task_trigger: task already in_progress, skipping fire (overlap guard)",
				"task_id", taskID, "job_id", job.ID)
			return "", nil
		}
		return "", fmt.Errorf("task_trigger: spawn reset task %q: %w", taskID, err)
	}

	if s.dispatch == nil {
		slog.Warn("task_trigger: no dispatch function set, task reset but not dispatched",
			"task_id", taskID)
		return "", nil
	}

	if err := s.dispatch(ctx, fresh.ID); err != nil {
		slog.Error("task_trigger: dispatch failed",
			"task_id", taskID, "error", err)
		return "", fmt.Errorf("task_trigger: dispatch task %q: %w", taskID, err)
	}

	slog.Info("task_trigger: task dispatched by trigger",
		"task_id", taskID, "trigger_type", t.Trigger.Type)
	return "", nil
}

// triggerToCronSchedule converts a task.Trigger to a cron.CronSchedule.
func triggerToCronSchedule(tr *task.Trigger) (cron.CronSchedule, error) {
	switch tr.Type {
	case task.TriggerOnce:
		if tr.Config.AtMs == nil {
			return cron.CronSchedule{}, fmt.Errorf("trigger 'once' missing config.at_ms")
		}
		return cron.CronSchedule{Kind: "at", AtMS: tr.Config.AtMs}, nil

	case task.TriggerEvery:
		if tr.Config.EveryMs == nil {
			return cron.CronSchedule{}, fmt.Errorf("trigger 'every' missing config.every_ms")
		}
		return cron.CronSchedule{Kind: "every", EveryMS: tr.Config.EveryMs}, nil

	case task.TriggerRecurring:
		if tr.Config.CronExpr == nil || *tr.Config.CronExpr == "" {
			return cron.CronSchedule{}, fmt.Errorf("trigger 'recurring' missing config.cron_expr")
		}
		return cron.CronSchedule{Kind: "cron", Expr: *tr.Config.CronExpr}, nil

	default:
		return cron.CronSchedule{}, fmt.Errorf("unsupported trigger type %q", tr.Type)
	}
}
