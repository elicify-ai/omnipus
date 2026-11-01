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
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	cs    *cron.CronService
	store *task.Store
	// dispatch fires taskID's fresh run. occurrenceMs is the calendar join
	// key for a per-execution TaskRun record (ADR-050 §3.2,
	// docs/internal/specs/task-run-history-spec.md §3.2): the specific RRULE
	// instant this fire realizes, or nil for a non-recurring (once)
	// trigger. RunScheduled is the sole production caller
	// and computes it — see its own doc comment.
	dispatch func(ctx context.Context, taskID string, occurrenceMs *int64) error

	// emitRunStatus, when set, publishes a TaskRun status transition onto the
	// agent event bus — the exact same emitEvent-based mechanism OpenRun/
	// CloseRun already use via TaskExecutor.emitRunStatus (task_executor.go),
	// so a recorded skipped-occurrence run also gets a live task_run_status
	// WS push, matching every other run-status transition (ADR-050
	// Consequences "Realtime"). Wired from the *TaskExecutor passed to
	// NewTaskTriggerScheduler — nil in tests that construct a scheduler
	// without one, mirroring dispatch's own "unset = no-op" convention.
	emitRunStatus func(taskID, runID string, occurrenceMs *int64, status task.Status)

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
		s.emitRunStatus = executor.emitRunStatus
	}
	return s
}

// Start restores persisted task-to-job bindings and removes duplicate internal
// jobs before the cron runner can fire, then starts the engine and recovery
// sweep (Scheduler rule 6). Must be called once at boot, before Reconcile.
func (s *TaskTriggerScheduler) Start() error {
	if err := s.restoreStoredJobs(); err != nil {
		return err
	}
	// r15 boot order: checked restoration, then the once-per-boot
	// recomputation, then engine activation — so no restored overdue
	// occurrence can be collected before it is recomputed (a past once is
	// not re-armed: skipped, never replayed).
	if err := s.reconcileAll(true); err != nil {
		return err
	}
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

// Reconcile scans the task store and brings every task's registration in line
// with its persisted trigger (the running-process rule: an unchanged trigger
// whose registration owns its occurrence keeps it), then removes jobs for
// tasks that no longer need one. Start already ran the once-per-boot
// recomputation before the engine was activated; the gateway's later call is
// an ordinary running-process reconciliation.
func (s *TaskTriggerScheduler) Reconcile() error {
	return s.reconcileAll(false)
}

// reconcileAll is Reconcile; boot selects the physical-start recomputation
// (r17 TK-30: an occurrence already overdue at boot is not owned).
func (s *TaskTriggerScheduler) reconcileAll(boot bool) error {
	tasks, unreadableIDs, err := s.store.ListWithUnreadable(task.Filter{})
	if err != nil {
		return fmt.Errorf("task_trigger: reconcile list: %w", err)
	}
	s.logUnreadableTasks(unreadableIDs, s.now().UnixMilli())
	for i := range tasks {
		id := tasks[i].ID
		unlock := task.LockTaskWriteOrder(id)
		if rErr := s.reconcileTaskLocked(id, boot); rErr != nil {
			slog.Error("task_trigger: reconcile task failed", "task_id", id, "error", rErr)
		}
		unlock()
	}

	// Prune cron jobs whose task IDs are no longer in the map (deleted tasks or
	// tasks that had their trigger removed between restarts).
	s.mu.Lock()
	registeredTaskIDs := make(map[string]bool, len(s.taskToJob))
	for tID := range s.taskToJob {
		registeredTaskIDs[tID] = true
	}
	s.mu.Unlock()

	for _, job := range s.cs.ListJobs(true) {
		taskID := job.Payload.Message
		if taskID == "" || !registeredTaskIDs[taskID] {
			s.cs.RemoveJob(job.ID)
		}
	}
	return nil
}

// OnTaskUpserted is called whenever a task is created or updated. Under the
// producer rule (r17 TK-30) the snapshot t identifies the task only: the
// registration is decided from the task state persisted now, read under the
// task's coordination lock, so a notification carrying a superseded snapshot
// can never restore an obsolete schedule. See task_trigger_reconcile.go for
// the registration and keep rules. Must not be called with the task's
// coordination lock held (use ReconcileTaskLocked there).
func (s *TaskTriggerScheduler) OnTaskUpserted(t *task.Task) {
	if t == nil || t.ID == "" {
		return
	}
	unlock := task.LockTaskWriteOrder(t.ID)
	defer unlock()
	if err := s.ReconcileTaskLocked(t.ID); err != nil {
		slog.Error("task_trigger: failed to reconcile trigger job", "task_id", t.ID, "error", err)
	}
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
// each due task-trigger job. prepareFire validates the job and the occurrence
// against the fresh persisted task under its coordination lock and resets the
// task with its delivery marker; the dispatch (defaulting to
// executor.SpawnTriggeredRun) runs after the lock is released.
//
// For an RRULE-backed recurring trigger, every readable-task exit — success,
// the overlap-guard skip, a dispatch error, and a SpawnReset error alike — also
// re-arms the next occurrence (Scheduler rule 2): because the fired job is an
// `at` job the engine deletes on completion (DeleteAfterRun), this re-arm is
// the SOLE continuation of the series.
func (s *TaskTriggerScheduler) RunScheduled(ctx context.Context, job *cron.CronJob) (string, error) {
	taskID := job.Payload.Message
	if taskID == "" {
		slog.Warn("task_trigger: RunScheduled called with empty task ID in payload",
			"job_id", job.ID)
		s.cs.RemoveJob(job.ID)
		return "", nil
	}
	d := s.prepareFire(job)
	if d.err != nil {
		return "", d.err
	}
	if !d.dispatch {
		if d.rearm {
			s.rearmRrule(d.t, d.fireGen, job.ID)
		}
		return "", nil
	}
	if s.dispatch == nil {
		slog.Warn("task_trigger: no dispatch function set, task reset but not dispatched",
			"task_id", taskID)
		if d.rearm {
			s.rearmRrule(d.t, d.fireGen, job.ID)
		}
		return "", nil
	}

	// D-08 (founder decision 2026-09-24, FR-057): a Calendar/task-trigger fire
	// is an unattended dispatch with no operator present. Stamp the headless
	// marker so an always-ask tool is refused at once instead of opening a
	// live approval card; executor.SpawnTriggeredRun preserves ctx values.
	dispatchCtx := tools.WithAutoDenyAsk(ctx, true)
	if err := s.dispatch(dispatchCtx, d.reset.ID, d.occurrenceMs); err != nil {
		if d.isRrule {
			slog.Warn("task_trigger: dispatch failed for rrule task, re-arming next occurrence (no retry backoff)",
				"task_id", taskID, "job_id", job.ID, "error", err)
			s.rearmRrule(d.t, d.fireGen, job.ID)
			return "", nil
		}
		slog.Error("task_trigger: dispatch failed",
			"task_id", taskID, "error", err)
		return "", fmt.Errorf("task_trigger: dispatch task %q: %w", taskID, err)
	}

	slog.Info("task_trigger: task dispatched by trigger",
		"task_id", taskID, "trigger_type", d.t.Trigger.Type)
	if d.rearm {
		s.rearmRrule(d.t, d.fireGen, job.ID)
	}
	return "", nil
}

// rearmRrule computes the next RRULE occurrence after now and registers it,
// replacing the firing job (replace-by-task, Scheduler rule 4). t is the task
// read at the fire; fireGen its trigger generation; firingJobID the job that
// fired.
//
// Guard (registration identity, r13/r14): the re-arm happens only while the
// firing job is still the task's current registration with the same
// generation, and the fresh persisted trigger — read under the task's
// coordination lock — still has that generation. A clear or replacement in the
// meantime (even an identical reattach, which installs a new registration)
// makes this a no-op, so a revoked callback can never replace a successor.
// On series exhaustion (COUNT/UNTIL) this retires silently.
func (s *TaskTriggerScheduler) rearmRrule(t *task.Task, fireGen, firingJobID string) {
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

	// Test seam (edit-during-fire determinism): fires before the task lock and
	// the critical section, so a concurrent writer is never blocked by it.
	if hook := s.getRearmTestHook(); hook != nil {
		hook()
	}

	unlock := task.LockTaskWriteOrder(t.ID)
	defer unlock()
	fresh, err := s.store.Get(t.ID)
	if err != nil || triggerGeneration(fresh.Trigger) != fireGen {
		slog.Debug("task_trigger: rrule re-arm superseded by a newer persisted trigger, no-op", "task_id", t.ID)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, tracked := s.taskToJob[t.ID]
	if !tracked || current.generation != fireGen || current.jobID != firingJobID {
		slog.Debug("task_trigger: rrule re-arm superseded by a concurrent registration, no-op",
			"task_id", t.ID)
		return
	}
	job, err := s.replaceJobLocked(t.ID, s.jobSpecFor(fresh, sched), fireGen)
	if err != nil {
		s.dearmAllLocked(t.ID)
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

		s.sweepRearm(t.ID, nowMs)
	}
}

// sweepRearm re-arms one orphaned RRULE task found by RunRecoverySweep. The
// producer rule applies (r17 TK-30): the persisted trigger is re-read under
// the task's coordination lock, so a sweep snapshot taken before a completed
// clear or replacement registers nothing.
func (s *TaskTriggerScheduler) sweepRearm(taskID string, nowMs int64) {
	unlock := task.LockTaskWriteOrder(taskID)
	defer unlock()
	t, err := s.store.Get(taskID)
	if err != nil || t.Trigger == nil || t.Trigger.Type != task.TriggerRecurring ||
		t.Trigger.Config.Rrule == nil || *t.Trigger.Config.Rrule == "" {
		return
	}
	fireGen := triggerGeneration(t.Trigger)
	sched, err := triggerToCronSchedule(t.Trigger, nowMs)
	if err != nil {
		if !errors.Is(err, errRruleExhausted) {
			slog.Error("task_trigger: recovery sweep: cannot compute next occurrence",
				"task_id", t.ID, "error", err)
		}
		return // exhaustion is not an orphan — retire silently, legitimately
	}

	s.mu.Lock()
	if s.isArmedLocked(t.ID, nowMs) {
		// Healed by a concurrent registration since the classification —
		// no-op (closes that TOCTOU window).
		s.mu.Unlock()
		return
	}
	job, err := s.replaceJobLocked(t.ID, s.jobSpecFor(t, sched), fireGen)
	if err != nil {
		s.dearmAllLocked(t.ID)
	}
	delete(s.ambiguousSweeps, t.ID) // fresh job installed: reset the streak
	s.mu.Unlock()
	if err != nil {
		slog.Error("task_trigger: recovery sweep: failed to re-arm orphaned rrule task",
			"task_id", t.ID, "error", err)
		return
	}
	slog.Warn("task_trigger: recovery sweep re-armed orphaned rrule task",
		"task_id", t.ID, "job_id", job.ID)
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
	// r14 ownership: an unconsumed next run (future or overdue — the engine's
	// due-collection is what consumes it) or a dispatch queued or running.
	return job.State.NextRunAtMS != nil || s.cs.JobDispatchInFlight(tracked.jobID)
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
	if job.State.NextRunAtMS != nil || s.cs.JobDispatchInFlight(tracked.jobID) {
		// r14: an unconsumed next run — including an overdue one the engine
		// has not collected yet — or a dispatch in flight is healthy.
		delete(s.ambiguousSweeps, taskID)
		return false
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

// GetTaskTriggerScheduler returns al's installed task-trigger scheduler (nil
// before boot wiring or in tests that don't set one). Mirrors the existing
// agent.GetTaskExecutor accessor pattern (pkg/agent/loop.go) so gateway
// handlers (the occurrences endpoint) can reach the
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
// occurrence strictly after now; the once mapping is
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
		// session-core DEL-19 / FR-017: recurring task timing is RRULE-only. The
		// legacy cron_expr compatibility adapter is deleted; a recurring trigger
		// without an rrule is refused, never adapted.
		return cron.CronSchedule{}, fmt.Errorf("trigger 'recurring' requires config.rrule (cron_expr is no longer supported)")

	default:
		return cron.CronSchedule{}, fmt.Errorf("unsupported trigger type %q", tr.Type)
	}
}
