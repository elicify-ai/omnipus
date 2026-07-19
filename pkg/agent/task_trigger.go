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
// RRULE triggers (Calendar Recurrence Redesign): a `recurring` trigger
// carrying `config.rrule` arms as a single cron `kind:"at"` job at its next
// occurrence. Because an `at` job is deleted by the engine the instant it
// fires (DeleteAfterRun), the ONLY thing that continues an RRULE series is
// this scheduler re-arming the next occurrence on every readable-task exit of
// RunScheduled — see the re-arm helper (rearmRrule), the trigger-generation
// guard that protects a re-arm racing a concurrent edit (replaceJobLocked),
// and the crash-recovery sweep (RunRecoverySweep) below.
//
// Boot wiring (done by the gateway, NOT this file):
//
//	s := agent.NewTaskTriggerScheduler(storePath, taskStore, executor)
//	s.Start()      // SetRunner(s) + cs.Start() + starts the recovery sweep ticker
//	s.Reconcile()  // register jobs for all existing triggered tasks
//
// On task create/update: s.OnTaskUpserted(t)
// On task delete:        s.OnTaskDeleted(taskID)

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// taskTriggerOwnerSentinel is the AgentID written to cron jobs that belong to
// tasks without an AgentID. The cron engine's owner-missing guard (executeJobByID)
// skips a job when a runner IS set AND job.AgentID == "". Since TaskTriggerScheduler
// is the runner and does NOT require an owner (it looks up the task by ID from the
// payload), we must supply a non-empty sentinel to bypass that guard while still
// routing dispatch through our RunScheduled method.
const taskTriggerOwnerSentinel = "task-trigger"

// recoverySweepInterval is the cadence of the crash-recovery/observability
// sweep (Scheduler rule 6). There is no existing maintenance cadence to
// reuse — Reconcile runs at boot only, and pkg/cron has only the due-job tick
// loop — so this scheduler owns a dedicated ticker.
const recoverySweepInterval = 5 * time.Minute

// errRruleExhausted is returned internally by triggerToCronSchedule when an
// RRULE series has no further occurrences (COUNT reached or past UNTIL,
// evaluated statelessly from DTSTART). Callers treat it as "retire silently"
// rather than a mapping failure — it is not logged as a WARN/ERROR.
var errRruleExhausted = errors.New("task_trigger: rrule series exhausted")

// trackedTrigger is what TaskTriggerScheduler remembers about the single cron
// job it currently maintains for a task: the job's ID, and a content hash
// ("generation") of the trigger that produced it. The generation is the
// mutex-atomic guard against a delayed re-arm (from a slow dispatch) clobbering
// a newer rule installed by a concurrent edit (Scheduler rule 4).
type trackedTrigger struct {
	jobID      string
	generation string
}

// systemClock is the default cron.Clock implementation, used until a test
// clock is injected via SetClock. Mirrors pkg/cron's own unexported realClock
// so this scheduler's notion of "now" (used for RRULE next-occurrence math)
// stays in lockstep with the underlying CronService's clock without pkg/cron
// needing to expose one.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

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
	taskToJob map[string]trackedTrigger // taskID → {cronJobID, trigger generation}
	clock     cron.Clock                // mirrors cs's clock; defaults to systemClock, set via SetClock

	// sweepStop signals the recovery-sweep goroutine to exit; nil when the
	// sweep is not running. Guarded by mu.
	sweepStop chan struct{}
	sweepWG   sync.WaitGroup

	// rearmTestHook, when set, is invoked by rearmRrule just before its
	// critical section begins (test-only seam for deterministically forcing
	// an edit-during-fire interleaving — see TestTriggerScheduler_EditDuringFire).
	// Guarded by mu.
	rearmTestHook func()

	// spawnResetTestHook, when set, is invoked by RunScheduled immediately
	// before it calls store.SpawnReset (test-only seam for deterministically
	// forcing the task-deleted-mid-fire race — F5, see
	// TestTriggerScheduler_SpawnResetTaskNotFound_NoReArm). Guarded by mu.
	spawnResetTestHook func()

	// ambiguousSweeps tracks, per task ID, the number of consecutive
	// RunRecoverySweep observations that found that task's tracked job in
	// the H1 "ambiguous" shape: present, State.NextRunAtMS nil, and
	// State.Running false. RunDueJobs clears NextRunAtMS (under cs.mu)
	// before dispatching, and executeJobByID does not persist
	// State.Running=true until AFTER it acquires a lane slot on the
	// (8-slot) semaphore — so a due-but-queued dispatch under lane
	// saturation is, for a sweep landing in that window, byte-for-byte the
	// same shape as a genuine pre-Running-flag crash orphan. Escalating to
	// a re-arm only once the SAME task stays in that shape across two
	// consecutive sweeps gives one full recoverySweepInterval tick for a
	// legitimately queued dispatch to resolve before it is treated as dead.
	// Guarded by mu; an entry is deleted whenever that task is observed
	// armed again or its job is replaced.
	ambiguousSweeps map[string]int

	// unreadableSweeps tracks, per task ID, how many consecutive
	// Reconcile/RunRecoverySweep observations found that task's file
	// unreadable (H2). Surfaced in the escalated ERROR log
	// (logUnreadableTasks) so operators can see how long a recurring series
	// has been silently dead. Guarded by mu; an ID is dropped once a scan no
	// longer reports it (the file became readable again, or was deleted).
	unreadableSweeps map[string]int
}

// NewTaskTriggerScheduler creates a scheduler that uses a dedicated CronService
// with its store at storePath. The executor's SpawnTriggeredRun is used as the
// dispatch function unless overridden (test seam: replace dispatch before Start).
func NewTaskTriggerScheduler(storePath string, store *task.Store, executor *TaskExecutor) *TaskTriggerScheduler {
	s := &TaskTriggerScheduler{
		cs:        cron.NewCronService(storePath),
		store:     store,
		taskToJob: make(map[string]trackedTrigger),
		clock:     systemClock{},
	}
	if executor != nil {
		s.dispatch = executor.SpawnTriggeredRun
	}
	return s
}

// Start wires this scheduler as the cron runner, starts the engine, and
// starts the recovery-sweep ticker (Scheduler rule 6). Must be called once at
// boot, before Reconcile.
func (s *TaskTriggerScheduler) Start() error {
	s.cs.SetRunner(s)
	if err := s.cs.Start(); err != nil {
		return err
	}
	s.startSweep()
	return nil
}

// startSweep launches the recovery-sweep goroutine if it is not already
// running. Idempotent.
func (s *TaskTriggerScheduler) startSweep() {
	s.mu.Lock()
	if s.sweepStop != nil {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.sweepStop = stop
	s.mu.Unlock()

	s.sweepWG.Add(1)
	go func() {
		defer s.sweepWG.Done()
		ticker := time.NewTicker(recoverySweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.RunRecoverySweep()
			}
		}
	}()
}

// Stop shuts down the recovery-sweep ticker and the underlying cron engine.
func (s *TaskTriggerScheduler) Stop() {
	s.mu.Lock()
	stop := s.sweepStop
	s.sweepStop = nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	s.sweepWG.Wait()

	s.cs.Stop()
}

// SetClock overrides the time source for both this scheduler's own "now"
// (used for RRULE next-occurrence math) and the underlying CronService, so
// the two never disagree under a test clock. Must be called before Start.
func (s *TaskTriggerScheduler) SetClock(c cron.Clock) {
	if c != nil {
		s.mu.Lock()
		s.clock = c
		s.mu.Unlock()
	}
	s.cs.SetClock(c)
}

// now returns the current time from the injected clock (systemClock by
// default).
func (s *TaskTriggerScheduler) now() time.Time {
	s.mu.Lock()
	c := s.clock
	s.mu.Unlock()
	if c == nil {
		return time.Now()
	}
	return c.Now()
}

// SetRearmTestHook installs a callback invoked by every RRULE re-arm just
// before its critical section begins (test-only seam: TestTriggerScheduler_EditDuringFire
// uses it to make the operator's concurrent edit deterministically land
// before the re-arm's generation check, rather than racing goroutine
// scheduling). Pass nil to clear it (the default; production never sets one).
func (s *TaskTriggerScheduler) SetRearmTestHook(fn func()) {
	s.mu.Lock()
	s.rearmTestHook = fn
	s.mu.Unlock()
}

func (s *TaskTriggerScheduler) getRearmTestHook() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rearmTestHook
}

// SetSpawnResetTestHook installs a callback invoked by RunScheduled
// immediately before its store.SpawnReset call (test-only seam: F5's
// regression test uses it to deterministically delete the task in the
// window between RunScheduled's earlier store.Get — which has already
// succeeded by the time this hook fires — and the SpawnReset call, forcing
// SpawnReset to observe task.ErrNotFound). Pass nil to clear it (the
// default; production never sets one).
func (s *TaskTriggerScheduler) SetSpawnResetTestHook(fn func()) {
	s.mu.Lock()
	s.spawnResetTestHook = fn
	s.mu.Unlock()
}

func (s *TaskTriggerScheduler) getSpawnResetTestHook() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawnResetTestHook
}

// jobSpecFor builds the cron.JobSpec used to (re-)register t's trigger job:
// the "task:<id>" name, the owner-sentinel fallback for a task with no
// assigned agent (see taskTriggerOwnerSentinel's doc comment), and the
// payload Message carrying the task ID so RunScheduled can look it up.
// OnTaskUpserted, rearmRrule, and RunRecoverySweep each feed this straight
// into replaceJobLocked unchanged — extracted here so the three previously
// hand-duplicated, byte-for-byte copies cannot drift apart.
func (s *TaskTriggerScheduler) jobSpecFor(t *task.Task, sched cron.CronSchedule) cron.JobSpec {
	ownerID := t.AgentID
	if ownerID == "" {
		ownerID = taskTriggerOwnerSentinel
	}
	return cron.JobSpec{
		Name:     fmt.Sprintf("task:%s", t.ID),
		Schedule: sched,
		// Message carries the task ID so RunScheduled can look it up.
		Message: t.ID,
		AgentID: ownerID,
	}
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
// that no longer need one. Call once at boot, after Start. For RRULE tasks this
// is also the documented crash-recovery path for the boot window (Scheduler
// rule 6): OnTaskUpserted re-arms the next future occurrence for each one.
func (s *TaskTriggerScheduler) Reconcile() error {
	tasks, unreadableIDs, err := s.store.ListWithUnreadable(task.Filter{})
	if err != nil {
		return fmt.Errorf("task_trigger: reconcile list: %w", err)
	}
	s.logUnreadableTasks(unreadableIDs, s.now().UnixMilli())
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
//   - task is terminal (done/failed) AND its trigger does NOT repeat — a
//     `once` (or no-trigger) task's terminal status really does mean its one
//     and only occurrence is over
//   - task surface is heartbeat (the heartbeat service owns those recurring fires;
//     registering here would cause double-firing)
//   - trigger is a REPEATING trigger (recurring/every) whose series is already
//     exhausted (COUNT reached or past UNTIL for an rrule; `every` never
//     exhausts) — retired silently, no WARN
//
// A REPEATING trigger (recurring/every) that is terminal is deliberately NOT
// removed by the terminal check above — `done`/`failed` reflects only the
// outcome of the single RUN that just finished, not that the series itself
// has ended. Bug fixed here: dispatch is asynchronous (SpawnTriggeredRun /
// ExecuteTask launch `go te.runTask(...)` and return immediately), so
// RunScheduled's own exit-path re-arm (rearmRrule) always runs and arms the
// next occurrence BEFORE that async run finishes. When it later finishes and
// its terminal status lands — via a task_update tool call or a REST PATCH,
// both of which call NotifyTaskUpserted → this method — the previous,
// unconditional "terminal ⇒ remove" rule deleted the job rearmRrule had just
// armed, silently ending the series after exactly one fire. A terminal
// repeating task now falls through to the normal (re)registration path
// below instead, exactly as a non-terminal upsert would. The series' only
// TRUE end conditions remain trigger removal/change-to-manual (noTrigger
// above) and RRULE exhaustion (handled below via errRruleExhausted).
//
// Race note: because both this path and rearmRrule (task_trigger.go) route
// through replaceJobLocked under mu, and triggerToCronSchedule/
// NextOccurrenceAfter is a pure function of (rule, now), a fire's own
// rearmRrule call and this OnTaskUpserted call (fired later, once the async
// run's terminal status lands) can legitimately both attempt to register a
// job for the same task around the same time. In the ordinary case neither
// call observes an occurrence boundary crossing between them, so they
// compute the IDENTICAL next-occurrence time — whichever runs second just
// atomically replaces an equivalent job with a new job ID under mu; there is
// no torn or double-registered state, and no wrong occurrence is ever armed.
//
// The remove-old→register-new→map-write sequence is one atomic critical
// section under mu (Scheduler rule 4), closing the check-then-register window
// a concurrent RRULE re-arm (rearmRrule) or recovery-sweep re-arm could
// otherwise race.
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
	// isRepeating: a recurring (rrule or legacy cron_expr) or every trigger's
	// series survives a per-run terminal status — see the doc comment above.
	// `once` is deliberately excluded: its single occurrence IS its whole
	// series, so a terminal `once` task is still removed exactly as before.
	isRepeating := t.Trigger != nil &&
		(t.Trigger.Type == task.TriggerRecurring || t.Trigger.Type == task.TriggerEvery)

	if noTrigger || (terminal && !isRepeating) {
		// Remove any existing job for this task (trigger was cleared, the
		// task ended with a non-repeating trigger, or there is nothing to
		// track).
		s.OnTaskDeleted(t.ID)
		return
	}

	nowMs := s.now().UnixMilli()
	sched, err := triggerToCronSchedule(t.Trigger, nowMs)
	if err != nil {
		if errors.Is(err, errRruleExhausted) {
			slog.Info("task_trigger: rrule series exhausted, retiring", "task_id", t.ID)
		} else {
			slog.Warn("task_trigger: cannot map trigger to cron schedule, skipping",
				"task_id", t.ID, "trigger_type", t.Trigger.Type, "error", err)
		}
		s.OnTaskDeleted(t.ID)
		return
	}

	generation := triggerGeneration(t.Trigger)

	s.mu.Lock()
	job, err := s.replaceJobLocked(t.ID, s.jobSpecFor(t, sched), generation)
	s.mu.Unlock()
	if err != nil {
		slog.Error("task_trigger: failed to register cron job",
			"task_id", t.ID, "error", err)
		return
	}

	slog.Info("task_trigger: registered trigger job",
		"task_id", t.ID, "job_id", job.ID, "kind", sched.Kind)
}

// replaceJobLocked removes any job currently tracked for taskID — idempotent
// healing of any residual double-arm — then registers a new one from spec and
// records its generation. Every re-arm registration (OnTaskUpserted, the
// RunScheduled exit-path re-arm, and the recovery sweep) goes through this one
// helper so "replace-by-task" (Scheduler rule 4) is applied uniformly.
//
// Caller MUST already hold mu; the whole remove→AddJobFull→map-write sequence
// executes as one critical section. cs's own mutex is independent and always
// acquired only while mu is already held (scheduler-mutex → engine-mutex
// ordering) — cs's callers (RemoveJob/AddJobFull) never themselves try to
// acquire mu, so this ordering can never invert.
func (s *TaskTriggerScheduler) replaceJobLocked(
	taskID string,
	spec cron.JobSpec,
	generation string,
) (*cron.CronJob, error) {
	if existing, ok := s.taskToJob[taskID]; ok {
		s.cs.RemoveJob(existing.jobID)
		delete(s.taskToJob, taskID)
	}
	job, err := s.cs.AddJobFull(spec)
	if err != nil {
		return nil, err
	}
	s.taskToJob[taskID] = trackedTrigger{jobID: job.ID, generation: generation}
	return job, nil
}

// OnTaskDeleted removes the cron job associated with taskID, if any.
func (s *TaskTriggerScheduler) OnTaskDeleted(taskID string) {
	s.mu.Lock()
	tracked, ok := s.taskToJob[taskID]
	if ok {
		delete(s.taskToJob, taskID)
	}
	s.mu.Unlock()

	if ok {
		removed := s.cs.RemoveJob(tracked.jobID)
		slog.Debug("task_trigger: removed trigger job",
			"task_id", taskID, "job_id", tracked.jobID, "was_present", removed)
	}
}

// RunScheduled implements cron.ScheduledRunner. The cron engine calls this for
// each due task-trigger job. It extracts the task ID from the job payload,
// validates the task still needs firing, resets it to `next` via SpawnReset, and
// dispatches it via the dispatch function (defaulting to executor.SpawnTriggeredRun).
//
// For an RRULE-backed recurring trigger, every readable-task exit — success,
// the overlap-guard skip, a dispatch error, and a SpawnReset error alike — also
// re-arms the next occurrence (Scheduler rule 2): because the fired job is an
// `at` job the engine deletes on completion (DeleteAfterRun), this re-arm is
// the SOLE continuation of the series. Legacy (once/every/cron_expr) triggers
// are entirely unaffected — their exits are byte-for-byte unchanged
// (Scheduler rule 7).
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
		// Task-unreadable exit (Scheduler rule 2e): a transient read error means
		// the trigger cannot be consulted, so re-arming is impossible here — the
		// recovery sweep (rule 6) is the designated recovery once the task
		// becomes readable again.
		slog.Error("task_trigger: task unreadable, cannot re-arm (relying on recovery sweep)",
			"task_id", taskID, "job_id", job.ID, "error", err)
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

	isRrule := t.Trigger.Type == task.TriggerRecurring &&
		t.Trigger.Config.Rrule != nil && *t.Trigger.Config.Rrule != ""
	var fireGen string
	if isRrule {
		fireGen = triggerGeneration(t.Trigger)
	}

	// Test seam (F5 determinism, TestTriggerScheduler_SpawnResetTaskNotFound_NoReArm):
	// fires immediately before SpawnReset so a test can force the task to be
	// deleted in the exact window this fix closes — after the store.Get
	// above already succeeded, but before SpawnReset's own read.
	if hook := s.getSpawnResetTestHook(); hook != nil {
		hook()
	}

	// Reset the task to `next` for a fresh run.
	fresh, err := s.store.SpawnReset(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			// F5: the task was deleted in the window between the store.Get
			// above (which succeeded) and this SpawnReset call. Route to the
			// same cleanup as the earlier ErrNotFound branch — remove the
			// orphan job, do NOT fall through to the isRrule re-arm below
			// (which would re-arm a stale snapshot of a task that no longer
			// exists).
			slog.Info("task_trigger: task not found on spawn reset (deleted mid-fire), removing orphan job",
				"task_id", taskID, "job_id", job.ID)
			s.OnTaskDeleted(taskID)
			s.cs.RemoveJob(job.ID)
			return "", nil
		}
		if errors.Is(err, task.ErrAlreadyRunning) {
			// Task is in_progress — overlap guard fires. Benign skip: the next
			// interval will re-fire. Do not stomp an in-flight run.
			slog.Info("task_trigger: task already in_progress, skipping fire (overlap guard)",
				"task_id", taskID, "job_id", job.ID)
			if isRrule {
				s.rearmRrule(t, fireGen)
			}
			return "", nil
		}
		if isRrule {
			// No retry backoff (Scheduler rule 3): re-arm the next occurrence and
			// return nil — "the next occurrence is the retry", mirroring
			// cron-kind semantics instead of enrolling this at-job in the
			// engine's transient backoff.
			slog.Warn("task_trigger: spawn reset failed for rrule task, re-arming next occurrence (no retry backoff)",
				"task_id", taskID, "job_id", job.ID, "error", err)
			s.rearmRrule(t, fireGen)
			return "", nil
		}
		return "", fmt.Errorf("task_trigger: spawn reset task %q: %w", taskID, err)
	}

	if s.dispatch == nil {
		slog.Warn("task_trigger: no dispatch function set, task reset but not dispatched",
			"task_id", taskID)
		if isRrule {
			s.rearmRrule(t, fireGen)
		}
		return "", nil
	}

	if err := s.dispatch(ctx, fresh.ID); err != nil {
		if isRrule {
			slog.Warn("task_trigger: dispatch failed for rrule task, re-arming next occurrence (no retry backoff)",
				"task_id", taskID, "job_id", job.ID, "error", err)
			s.rearmRrule(t, fireGen)
			return "", nil
		}
		slog.Error("task_trigger: dispatch failed",
			"task_id", taskID, "error", err)
		return "", fmt.Errorf("task_trigger: dispatch task %q: %w", taskID, err)
	}

	slog.Info("task_trigger: task dispatched by trigger",
		"task_id", taskID, "trigger_type", t.Trigger.Type)
	if isRrule {
		s.rearmRrule(t, fireGen)
	}
	return "", nil
}

// rearmRrule computes the next RRULE occurrence after now and registers it,
// replacing whatever job is currently tracked for the task (replace-by-task,
// Scheduler rule 4). t is the task snapshot this fire cycle read at its start;
// fireGen is that snapshot's trigger-generation hash.
//
// Guard: if the task's currently tracked generation no longer matches fireGen
// — including the task no longer being tracked at all — a concurrent
// registration (a PUT's OnTaskUpserted, another re-arm, or the recovery
// sweep) has already superseded this fire's view of the rule, so this no-ops
// rather than clobbering the newer job. On series exhaustion (COUNT/UNTIL)
// this retires silently — the series legitimately ended, not an error.
func (s *TaskTriggerScheduler) rearmRrule(t *task.Task, fireGen string) {
	nowMs := s.now().UnixMilli()
	sched, err := triggerToCronSchedule(t.Trigger, nowMs)
	if err != nil {
		if errors.Is(err, errRruleExhausted) {
			slog.Info("task_trigger: rrule series exhausted at re-arm, retiring", "task_id", t.ID)
		} else {
			slog.Error("task_trigger: rrule re-arm: cannot compute next occurrence",
				"task_id", t.ID, "error", err)
		}
		return
	}

	// Test seam (edit-during-fire determinism, TestTriggerScheduler_EditDuringFire):
	// fires before this re-arm's critical section begins so a test can force a
	// concurrent OnTaskUpserted to run to completion first, making the
	// generation-mismatch path below deterministic instead of racing goroutine
	// scheduling.
	if hook := s.getRearmTestHook(); hook != nil {
		hook()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, tracked := s.taskToJob[t.ID]
	if !tracked || current.generation != fireGen {
		// Either a concurrent edit installed a different rule (generation
		// mismatch), or the task's registration was retired entirely by a
		// concurrent OnTaskUpserted/OnTaskDeleted (untracked) — either way, a
		// newer registration event already superseded this fire. No-op
		// (Scheduler rule 4).
		slog.Debug("task_trigger: rrule re-arm superseded by a concurrent registration, no-op",
			"task_id", t.ID)
		return
	}

	job, err := s.replaceJobLocked(t.ID, s.jobSpecFor(t, sched), fireGen)
	if err != nil {
		slog.Error("task_trigger: rrule re-arm: failed to register cron job",
			"task_id", t.ID, "error", err)
		return
	}
	slog.Debug("task_trigger: rrule re-armed next occurrence", "task_id", t.ID, "job_id", job.ID)
}

// RunRecoverySweep scans every RRULE-triggered task (terminal-but-repeating
// included, see below) and re-arms any whose tracked job has gone missing or
// dead (Scheduler rule 6: crash-recovery + observability), and escalates any
// persistently-unreadable task file to a loud ERROR log (H2). It is exported
// so tests can invoke it deterministically instead of waiting on the
// 5-minute ticker; the ticker started by Start calls this same method in
// production.
//
// "Armed" (isArmed) is: a registered job exists AND (its NextRunAtMS is
// non-nil and >= now, OR it is currently Running). The Running disjunct is
// load-bearing — RunDueJobs clears NextRunAtMS before dispatch, so an
// in-flight fire (re-arm pending on its exit path) must never be treated as
// orphaned and re-armed out from under itself.
//
// Terminal-but-repeating is a CANDIDATE, not a skip (companion to the
// OnTaskUpserted fix): an RRULE task's `done`/`failed` status does not mean
// its series ended — only trigger removal or RRULE exhaustion does. Skipping
// terminal tasks here would leave a crash-orphaned repeating series (e.g. one
// left behind by a pre-fix build across an upgrade, or a genuine crash
// between SpawnReset and the terminal write landing) permanently dead until
// the next full boot Reconcile. Exhaustion is still handled below exactly as
// for a non-terminal candidate — triggerToCronSchedule returns
// errRruleExhausted and the task is left with no job, not resurrected.
//
// H1: a job that is neither armed nor Running but is still tracked and
// resolves in the cron engine, with NextRunAtMS nil, is AMBIGUOUS rather
// than an immediate orphan — see shouldReArmLocked's doc comment. Only a
// task with no tracked job at all (or a tracked job ID that no longer
// resolves) is re-armed immediately, with no grace period.
func (s *TaskTriggerScheduler) RunRecoverySweep() {
	tasks, unreadableIDs, err := s.store.ListWithUnreadable(task.Filter{})
	if err != nil {
		slog.Error("task_trigger: recovery sweep: list tasks failed", "error", err)
		return
	}
	nowMs := s.now().UnixMilli()
	s.logUnreadableTasks(unreadableIDs, nowMs)

	for i := range tasks {
		t := &tasks[i]
		if t.EffectiveSurface() == task.SurfaceHeartbeat {
			continue
		}
		if t.Trigger == nil || t.Trigger.Type != task.TriggerRecurring ||
			t.Trigger.Config.Rrule == nil || *t.Trigger.Config.Rrule == "" {
			continue // sweep recovery is scoped to already-RRULE triggers
		}

		s.mu.Lock()
		reArm := s.shouldReArmLocked(t.ID, nowMs)
		s.mu.Unlock()
		if !reArm {
			continue
		}

		fireGen := triggerGeneration(t.Trigger)
		sched, err := triggerToCronSchedule(t.Trigger, nowMs)
		if err != nil {
			if !errors.Is(err, errRruleExhausted) {
				slog.Error("task_trigger: recovery sweep: cannot compute next occurrence",
					"task_id", t.ID, "error", err)
			}
			continue // exhaustion is not an orphan — retire silently, legitimately
		}

		s.mu.Lock()
		if s.isArmedLocked(t.ID, nowMs) {
			// Healed by a concurrent registration between shouldReArmLocked
			// above and acquiring the lock again here — no-op (closes that
			// TOCTOU window). Mirrors the pre-H1 recheck exactly.
			s.mu.Unlock()
			continue
		}
		job, err := s.replaceJobLocked(t.ID, s.jobSpecFor(t, sched), fireGen)
		delete(s.ambiguousSweeps, t.ID) // fresh job installed: reset the streak
		s.mu.Unlock()
		if err != nil {
			slog.Error("task_trigger: recovery sweep: failed to re-arm orphaned rrule task",
				"task_id", t.ID, "error", err)
			continue
		}
		slog.Warn("task_trigger: recovery sweep re-armed orphaned rrule task",
			"task_id", t.ID, "job_id", job.ID)
	}
}

// isArmed reports whether taskID's tracked job is armed (Scheduler rule 6).
// Acquires mu internally; use isArmedLocked when mu is already held.
func (s *TaskTriggerScheduler) isArmed(taskID string, nowMs int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isArmedLocked(taskID, nowMs)
}

// isArmedLocked is isArmed with mu already held by the caller.
func (s *TaskTriggerScheduler) isArmedLocked(taskID string, nowMs int64) bool {
	tracked, ok := s.taskToJob[taskID]
	if !ok {
		return false
	}
	job, ok := s.cs.GetJob(tracked.jobID)
	if !ok {
		return false
	}
	if job.State.Running {
		return true
	}
	return job.State.NextRunAtMS != nil && *job.State.NextRunAtMS >= nowMs
}

// shouldReArmLocked is RunRecoverySweep's per-task orphan/ambiguous
// classification and streak bookkeeping (H1). Caller must hold mu.
//
// isArmedLocked alone cannot distinguish a true crash orphan from a
// due-but-queued dispatch: RunDueJobs clears a job's State.NextRunAtMS
// (under cs.mu) before dispatching it, and executeJobByID does not persist
// State.Running=true until AFTER it acquires a lane slot on the cron
// engine's bounded (8-slot) semaphore. Under lane saturation, a sweep tick
// landing in that window sees a legitimately queued fire in EXACTLY the
// same shape as a genuine "crashed between the clear and the Running flag"
// orphan: job present, NextRunAtMS nil, not Running.
//
// Classification:
//   - No tracked job for taskID, or the tracked job ID no longer resolves in
//     the cron engine: an immediate, unambiguous orphan — re-arm now (same
//     behavior as before this fix; there is nothing to wait on).
//   - Tracked job is Running, or has a live future NextRunAtMS: armed —
//     healthy, no action. Any ambiguous streak recorded for this task is
//     cleared (Running flipping true, or a fresh NextRunAtMS appearing, both
//     mean the earlier ambiguity has resolved favorably).
//   - Tracked job is present, not Running, with a NextRunAtMS that is
//     non-nil but stale (< now): NOT the queued-dispatch shape (that always
//     has NextRunAtMS==nil) — a genuine orphan, re-armed immediately as
//     before.
//   - Tracked job is present, not Running, NextRunAtMS nil: ambiguous. Bumps
//     a per-task consecutive-sweep counter (ambiguousSweeps) and reports
//     "re-arm" only once the SAME task has been observed in this shape on
//     two consecutive sweeps. A genuinely queued dispatch resolves — Running
//     flips true, or the fire completes and its own exit path installs a
//     fresh job — well within one recoverySweepInterval tick; a real dead
//     orphan does not, and gets re-armed on the second observation.
func (s *TaskTriggerScheduler) shouldReArmLocked(taskID string, nowMs int64) bool {
	tracked, ok := s.taskToJob[taskID]
	if !ok {
		delete(s.ambiguousSweeps, taskID)
		return true
	}
	job, ok := s.cs.GetJob(tracked.jobID)
	if !ok {
		delete(s.ambiguousSweeps, taskID)
		return true
	}
	if job.State.Running {
		delete(s.ambiguousSweeps, taskID)
		return false
	}
	if job.State.NextRunAtMS != nil {
		armed := *job.State.NextRunAtMS >= nowMs
		delete(s.ambiguousSweeps, taskID)
		return !armed
	}

	// Ambiguous shape: job present, NextRunAtMS nil, not Running.
	if s.ambiguousSweeps == nil {
		s.ambiguousSweeps = make(map[string]int)
	}
	s.ambiguousSweeps[taskID]++
	return s.ambiguousSweeps[taskID] >= 2
}

// logUnreadableTasks is H2's rediscovery/observability fix. store.List (and
// the readable half of ListWithUnreadable) silently Warn+skips a
// present-but-unreadable task file — corrupt, permission-denied, or
// otherwise unparsable — which made a dead RRULE-trigger series invisible to
// both boot Reconcile and the crash-recovery sweep, the very mechanisms
// meant to rescue it, forever, with only a generic unattributed WARN.
//
// Both Reconcile and RunRecoverySweep call this with the unreadableIDs
// ListWithUnreadable reports alongside the readable task set. For every ID
// that isn't currently armed (i.e. does not still have a live tracked job —
// an older registration from before the file went bad, whose own exit path
// or next staleness check will keep surfacing this independently), it logs
// at ERROR (not WARN) with the task ID front-and-center and a running
// consecutive-observation streak, so a persistently-dead recurring series is
// loud instead of silent. The streak for any ID no longer reported
// unreadable (it became readable again, or the file was deleted) is
// dropped.
func (s *TaskTriggerScheduler) logUnreadableTasks(unreadableIDs []string, nowMs int64) {
	current := make(map[string]bool, len(unreadableIDs))
	for _, id := range unreadableIDs {
		current[id] = true
	}

	s.mu.Lock()
	if s.unreadableSweeps == nil {
		s.unreadableSweeps = make(map[string]int)
	}
	for id := range s.unreadableSweeps {
		if !current[id] {
			delete(s.unreadableSweeps, id)
		}
	}
	streaks := make(map[string]int, len(unreadableIDs))
	for _, id := range unreadableIDs {
		s.unreadableSweeps[id]++
		streaks[id] = s.unreadableSweeps[id]
	}
	s.mu.Unlock()

	for _, id := range unreadableIDs {
		if s.isArmed(id, nowMs) {
			// A live tracked job still exists (registered before the file
			// went bad) — less urgent right now; it will go stale and be
			// caught by the orphan/ambiguous path (or this same ERROR log
			// on a later sweep once it's no longer armed) on its own.
			continue
		}
		slog.Error("task_trigger: task file unreadable, cannot verify or re-arm trigger",
			"task_id", id, "consecutive_sweeps", streaks[id])
	}
}

// NextRunAtMSForTask returns taskID's currently-tracked job's live
// State.NextRunAtMS — the FR-008a display-projection anchor for a legacy
// `every` trigger's occurrences (the engine has no stored DTSTART-like
// anchor for `every` jobs; computeNextRun is `now + interval`,
// drift-anchored and re-baselined on every fire/restart, so the occurrences
// endpoint projects forward from whatever this returns "right now"). Read
// -only: looks up taskToJob then cs.GetJob, mirrors isArmedLocked's shape,
// never mutates scheduler or cron-engine state.
//
// ok is false when: taskID has no tracked job (never registered, or
// removed by a delete/terminal transition); the tracked job has since gone
// (a race with removal); or the job's NextRunAtMS is nil (e.g. mid-fire —
// RunDueJobs clears NextRunAtMS before dispatch, service.go:515-519). All
// three cases mean "no live anchor to project from right now" — callers
// (the occurrences endpoint) treat that as "task omitted from this
// response", not an error.
func (s *TaskTriggerScheduler) NextRunAtMSForTask(taskID string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tracked, ok := s.taskToJob[taskID]
	if !ok {
		return 0, false
	}
	job, ok := s.cs.GetJob(tracked.jobID)
	if !ok || job.State.NextRunAtMS == nil {
		return 0, false
	}
	return *job.State.NextRunAtMS, true
}

// GetTaskTriggerScheduler returns al's installed task-trigger scheduler (nil
// before boot wiring or in tests that don't set one). Mirrors the existing
// agent.GetTaskExecutor accessor pattern (pkg/agent/loop.go) so gateway
// handlers (the occurrences endpoint's every_ms anchor lookup) can reach the
// scheduler through one exported, same-package call to AgentLoop's already
// -locked private accessor (al.taskTriggerScheduler, defined in loop.go)
// without AgentLoop needing to expose its private field directly. Defined
// here (task_trigger.go) rather than loop.go so this scheduler's own public
// surface for the Calendar Recurrence Redesign lives in one file.
func GetTaskTriggerScheduler(al *AgentLoop) *TaskTriggerScheduler {
	if al == nil {
		return nil
	}
	return al.taskTriggerScheduler()
}

// triggerGeneration computes a content hash of tr, used as the trigger
// "generation" for the mutex-atomic re-arm guard (Scheduler rule 4). Two
// triggers with identical content (including a title-only edit that leaves
// the trigger untouched) hash identically; any change to type/config changes
// the hash.
func triggerGeneration(tr *task.Trigger) string {
	if tr == nil {
		return ""
	}
	data, err := json.Marshal(tr)
	if err != nil {
		// Should be unreachable for a valid Trigger (no unmarshalable fields).
		// Degrade to a sentinel that can never match a real generation, so the
		// guard fails safe (treats it as "always superseded") rather than
		// silently trusting stale content.
		return "!marshal-error"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// triggerToCronSchedule converts a task.Trigger to a cron.CronSchedule.
// nowMs is used only by the RRULE path (config.rrule) to compute the next
// occurrence strictly after now; the once/every/legacy-cron_expr mappings are
// pure and ignore it (Scheduler rule 7: byte-for-byte unchanged for legacy
// triggers).
//
// For an RRULE trigger whose series is already exhausted (COUNT/UNTIL),
// returns errRruleExhausted — callers must check with errors.Is and treat it
// as "retire silently", not a mapping failure.
func triggerToCronSchedule(tr *task.Trigger, nowMs int64) (cron.CronSchedule, error) {
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
		if tr.Config.Rrule != nil && *tr.Config.Rrule != "" {
			if tr.Config.DtstartMs == nil {
				return cron.CronSchedule{}, fmt.Errorf("trigger 'recurring' rrule missing config.dtstart_ms")
			}
			if tr.Config.Tz == nil || *tr.Config.Tz == "" {
				return cron.CronSchedule{}, fmt.Errorf("trigger 'recurring' rrule missing config.tz")
			}
			next, ok, err := task.NextOccurrenceAfter(*tr.Config.Rrule, *tr.Config.DtstartMs, *tr.Config.Tz, nowMs)
			if err != nil {
				return cron.CronSchedule{}, fmt.Errorf("trigger 'recurring' rrule expansion: %w", err)
			}
			if !ok {
				return cron.CronSchedule{}, errRruleExhausted
			}
			return cron.CronSchedule{Kind: "at", AtMS: &next}, nil
		}
		if tr.Config.CronExpr == nil || *tr.Config.CronExpr == "" {
			return cron.CronSchedule{}, fmt.Errorf("trigger 'recurring' missing config.cron_expr")
		}
		return cron.CronSchedule{Kind: "cron", Expr: *tr.Config.CronExpr}, nil

	default:
		return cron.CronSchedule{}, fmt.Errorf("unsupported trigger type %q", tr.Type)
	}
}
