package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ErrDispatchCapReached is returned by StartTaskNow when the global dispatch
// semaphore is exhausted. Callers (e.g. the REST handler) use errors.Is to
// distinguish this retryable condition from hard failures.
var ErrDispatchCapReached = errors.New("task_executor: global dispatch cap reached")

// taskGoalTranscriptWriteFailures is incremented each time one of this
// unit's task/goal-path transcript writers — task_executor.go's task
// prompt/error/response/steering/evidence-gate/judge-verdict entries and
// goal_loop.go's goal judge-verdict/handover entries — calls
// UnifiedStore.AppendTranscriptStrict against a session id that does not
// resolve to a real, store-backed session (ADR-057 FR-002/W3d). Before
// ADR-057, AppendTranscript silently minted an orphan session directory for
// exactly this case and returned nil, so a lost task/goal transcript write
// was indistinguishable from a successful one. Mirrors pkg/agent/turn.go's
// transcriptWriteFailures (U3) and pkg/tools/handoff.go's
// handoffTranscriptWriteFailures (U22) — a package-local counter scoped to
// this unit's own call sites, never a shared cross-package counter.
var taskGoalTranscriptWriteFailures atomic.Uint64

// TaskGoalTranscriptWriteFailures returns the current value of the
// task/goal-path transcript-write-failure counter (ADR-057 FR-002/W3d).
// Used by tests and operator tooling.
func TaskGoalTranscriptWriteFailures() uint64 {
	return taskGoalTranscriptWriteFailures.Load()
}

// taskSlot holds the state for one running (or reserved) task goroutine. A
// slot is inserted into te.running atomically under te.mu before the goroutine
// launches; this eliminates the nil-sentinel that previously required every
// reader to remember the nil-check.
//
// States:
//
//	reserved == true, cancel == nil  → slot claimed, goroutine not yet started
//	reserved == false, cancel != nil → goroutine live and cancellable
type taskSlot struct {
	cancel   context.CancelFunc // non-nil once the goroutine starts
	reserved bool               // true while the slot is held but goroutine not yet launched
}

// TaskExecutor runs dispatchable tasks by handing them to agent sessions. It
// operates over the unified pkg/task store and 6-state vocabulary: a
// dispatchable task is `next`, running is `in_progress`, terminal is
// `done`/`failed`.
type TaskExecutor struct {
	agentLoop *AgentLoop
	store     *task.Store
	launcher  steer.SessionLauncher
	mu        sync.Mutex
	running   map[string]*taskSlot
	// dispatchSema is the ONLY concurrency gate on task dispatch. It bounds
	// the total number of concurrently dispatched tasks across all agents and
	// resolves from the single central authority
	// (config.PerformanceConfig.EffectiveMaxParallelAgents), live-resized by
	// syncDispatchCapacity.
	//
	// There is deliberately NO per-agent cap. A hardcoded
	// defaultMaxConcurrentTasksPerAgent = 3 used to sit alongside this field
	// and was the gate that actually bound: the semaphore would resize to the
	// configured value (~1026 on a 4 GB box) while real behaviour stayed
	// pinned at 3 per agent, so the UI control reported success while
	// changing nothing. Do not reintroduce a per-agent bound without making
	// it resolve from the same central, operator-configurable authority.
	dispatchSema *DispatchSemaphore

	// liveTaskActivity (founder decision 2026-09-14) is the REST surface's
	// read seam for a running task's live last-activity stamp: the AgentLoop
	// itself, wired once at boot via SetLiveTaskActivitySource, answering
	// from the running turn's progress atomics (which advance on streamed
	// reasoning as well as tool-call deltas, UAT E-15c). Nil in test
	// harnesses; LiveTaskLastActivity then reports false.
	liveTaskActivity TaskLiveActivitySource

	// evidence records the write-set-scoped boundary commit that Play later
	// resumes a member from (D13/G-12). Wired at the gateway boot seam
	// alongside PlanEngine.SetCommitResolver — the two are the producer and
	// consumer of the same contract. Nil in test harnesses and on a degraded
	// boot, in which case no evidence is recorded and Play takes its
	// documented fresh-attempt path.
	//
	// Guarded by mu (fix-wave finding #3) — the SAME mutex protecting
	// lifecycleStore/running, for the same reason: the gateway boot sequence
	// starts TaskDrain-reachable dispatch (via newTaskExecutor) before it
	// calls SetEvidenceCommitter, so a bare unsynchronized field write here
	// raced recordEvidenceBoundary's unsynchronized read on another
	// goroutine. Always go through SetEvidenceCommitter (write) and
	// getEvidenceCommitter (read) — never touch the field directly.
	evidence evidenceCommitter

	// planStore is the shared *plan.Store (ADR-049/ADR-052), wired at the
	// gateway boot seam right alongside AgentLoop.SetPlanStore — see
	// SetPlanStore's doc comment below. It exists on TaskExecutor (not just
	// reached via te.agentLoop.GetPlanStore()) so CheckQueuedTasks' plan-gate
	// (see its doc) has a direct, test-friendly seam: newTaskExecutor's test
	// callers construct a bare TaskExecutor with agentLoop left nil, and the
	// gate must still be exercisable without a full AgentLoop. Nil in test
	// harnesses that never call SetPlanStore and on a boot sequence not yet
	// past gateway wiring — CheckQueuedTasks treats a nil store as fail-closed
	// for any task that names a PlanID (never auto-dispatch a plan member
	// whose parent plan's live state cannot be verified).
	//
	// Guarded by mu (fix-wave finding #3) — see evidence's doc comment above
	// for the identical race this closes. Always go through SetPlanStore
	// (write) and getPlanStore (read) — never touch the field directly.
	planStore *plan.Store

	// goroutineCtxHook is a test seam ONLY — production leaves it nil.
	// When non-nil, runTaskFromInProgress calls it with the goroutine's context
	// and returns immediately without performing real agent execution. This lets
	// tests observe the context that the goroutine received (e.g. to verify it is
	// not derived from the HTTP request context and survives request cancellation).
	goroutineCtxHook func(ctx context.Context, taskID string)

	// lifecycleStore is the durable S2 session-lifecycle store (ADR-053,
	// pkg/session/lifecycle.go), wired by the gateway boot seam via
	// SetLifecycleStore alongside PlanEngine.SetLifecycleStore /
	// AgentLoop.SetSessionMessagingStores — all three point at the SAME
	// *session.LifecycleStore instance. Nil in test harnesses and any boot
	// sequence that has not wired it yet: every access below
	// (mintTaskLifecycleRecord, transitionTaskLifecycle) nil-guards it via
	// getLifecycleStore and is a silent no-op, exactly mirroring
	// createTaskSessionSync's own sessStore-nil handling — a missing/unwired
	// durable record must never block or fail task dispatch.
	//
	// FR-118/G-13 gap this closes: before this field existed, the ONLY
	// production constructor of session.LifecycleRecord was
	// pkg/tools/delegate.go's `run` mint — a task/plan-member dispatch
	// session (created by createTaskSessionSync/StartTaskNow below) had NO
	// durable lifecycle record at all, so the boot sweep (boot_sweep.go)
	// could never see it, let alone reconcile it to failed(interrupted)
	// after a crash. Guarded by mu (the same mutex protecting `running`)
	// rather than a dedicated lock — SetLifecycleStore is a boot-time,
	// low-frequency write and every read is equally cheap.
	lifecycleStore *session.LifecycleStore

	// autoSyncDispatchCapacity, when true, makes ExecuteTask/StartTaskNow
	// re-resolve Performance.EffectiveMaxParallelAgents() and resize
	// dispatchSema before every dispatch attempt (see syncDispatchCapacity).
	// Set to true ONLY by newTaskExecutor (the real production constructor);
	// deliberately false (Go's zero value) for every test that constructs a
	// bare TaskExecutor{...} literal with its own hand-picked dispatchSema
	// capacity for a specific test scenario (e.g. forcing
	// ErrDispatchCapReached with cap=1) — such tests must keep full control
	// of the capacity they set, so auto-resync is opt-in-by-construction
	// rather than always-on.
	autoSyncDispatchCapacity bool

	// wg tracks every in-flight task-dispatch goroutine (runTask,
	// runTaskFromInProgress) end-to-end, INCLUDING the run loop's own
	// restart chain (consumeTaskAttempt flipping a task back to `next` and
	// the owning goroutine's trailing defer re-entering ExecuteTask for the
	// task's next run). Add(1)
	// happens at each of the two goroutine-launch sites, immediately before
	// the `go` statement; Done() is deferred as the OUTERMOST defer in each
	// goroutine body, so it fires only after that goroutine's own trailing
	// redispatch call (if any) has synchronously performed the NEXT
	// attempt's Add(1) — the counter is therefore never observably zero
	// mid-chain. See Drain's doc comment for why AgentLoop.Close() needs
	// this (previously Close() drained recaps/session-workers but never
	// TaskExecutor, so a still-running goal-loop chain could keep writing
	// session/transcript/run-history files after Close() returned).
	wg sync.WaitGroup

	// draining, once set by Drain, makes ExecuteTask/StartTaskNow refuse NEW
	// dispatch immediately (ErrExecutorDraining). Without this gate Drain's
	// wg.Wait could never return: a goal-loop redispatch chain's trailing
	// defer re-enters dispatch synchronously (see wg's doc comment), so with
	// intake open the counter never observably empties — and a chain
	// re-dispatching against stores Close() is about to tear down spins
	// failure-allocations flat out (observed as runner OOM/SIGTERM on all
	// three CI matrix legs before this gate existed). Never reset: a drained
	// executor belongs to an AgentLoop that is shutting down for good.
	draining atomic.Bool

	// dispatchGate makes "is intake still open?" and "register this dispatch on
	// wg" ONE atomic step with respect to Drain's "close intake, then wait".
	//
	// The two halves cannot be separate: sync.WaitGroup panics
	// ("Add called concurrently with Wait", sync/waitgroup.go) when an Add takes
	// the counter 0 -> N while a waiter is parked, and Drain parks exactly such
	// a waiter for the whole drain budget. Checking `draining` BEFORE the Add
	// does not close that window either — Drain's Store can land between the
	// check and the Add — and checking AFTER (the previous shape) guaranteed a
	// refused dispatch still drove the counter 0 -> 1, which is the panicking
	// transition. Only mutual exclusion removes it.
	//
	// Dispatchers take RLock (concurrent with each other, uncontended in the
	// steady state); Drain takes Lock around the Store alone, never across
	// wg.Wait. Held for two atomic ops and never across I/O or a lock of
	// te.mu, so it cannot participate in a lock cycle.
	dispatchGate sync.RWMutex
}

// SetSessionLauncher installs ADR-091's single session launch/dispatch
// primitive. StartTaskNow is its task-front consumer.
func (te *TaskExecutor) SetSessionLauncher(launcher steer.SessionLauncher) {
	if te != nil {
		te.launcher = launcher
	}
}

// newTaskExecutor creates a TaskExecutor over the unified task store.
// SetLiveTaskActivitySource wires the executor's live last-activity seam
// (founder decision 2026-09-14). Called once at boot, right after
// newTaskExecutor, with the AgentLoop itself (which implements
// TaskLiveActivitySource); the REST tasks surface reads through
// TaskExecutor.LiveTaskLastActivity to stamp Task.last_activity_at on the
// wire. Nil-safe no-op so partial test constructions keep working.
func (te *TaskExecutor) SetLiveTaskActivitySource(src TaskLiveActivitySource) {
	if te == nil {
		return
	}
	te.liveTaskActivity = src
}

// LiveTaskLastActivity forwards to the wired source, returning false when
// none is wired (test harnesses) — honest absence, never a fabricated stamp.
func (te *TaskExecutor) LiveTaskLastActivity(taskID string) (time.Time, bool) {
	if te == nil || te.liveTaskActivity == nil {
		return time.Time{}, false
	}
	return te.liveTaskActivity.TaskLiveLastActivity(taskID)
}

func newTaskExecutor(al *AgentLoop, store *task.Store) *TaskExecutor {
	// Resolve from the central authority. When al.cfg is nil (test seams
	// only — production always supplies a config), fall back to what a
	// zero-valued PerformanceConfig resolves to rather than a magic number,
	// so there is exactly one resolution path in the codebase.
	//
	// The capped=false case yields the physical OS-thread backstop, and that
	// is the RIGHT value for a semaphore capacity even though it is the wrong
	// value to show an operator: this semaphore is not the admission control.
	// Admission is gated live on memory (see applyMemoryCap in admission.go),
	// which refuses long before two thousand dispatches are in flight; the
	// semaphore's job is only to never be the thing that deadlocks the
	// process. A bare 0 here would do exactly that — every dispatch would
	// block forever on a zero-capacity semaphore — which is why
	// EffectiveMaxParallelAgents returns a two-valued answer instead of a 0
	// sentinel.
	capacity, _ := config.PerformanceConfig{}.EffectiveMaxParallelAgents()
	if al.cfg != nil {
		if eff, _ := al.cfg.Performance.EffectiveMaxParallelAgents(); eff > 0 {
			capacity = eff
		}
	}
	return &TaskExecutor{
		agentLoop:                al,
		store:                    store,
		running:                  make(map[string]*taskSlot),
		dispatchSema:             newDispatchSemaphore(capacity),
		autoSyncDispatchCapacity: true,
	}
}

// Drain blocks until every in-flight task-dispatch goroutine tracked by wg —
// including a goal-loop's own chain of re-dispatch attempts, see wg's doc
// comment — has completed, OR until budget elapses, whichever comes first.
// Mirrors AgentLoop.waitRecapDrain's identical bounded-drain rationale
// (loop.go): called from AgentLoop.Close(), BEFORE session workers, browser
// managers, and the stores those goroutines write through are torn down, so
// a task goroutine can never still be writing session/transcript/run-history
// files after Close() returns and races a caller's own teardown (e.g. a
// test's t.TempDir() cleanup removing the directory tree those files live
// under — the exact "TempDir RemoveAll cleanup: directory not empty" failure
// this closes). Bounded so a wedged execution (a mock/real LLM that never
// returns) can never hang Close() forever; on timeout it logs a warning and
// returns so the rest of teardown can proceed.
// enterDispatch registers one in-flight dispatch against te.wg unless intake
// has been closed by Drain. Returns false when the executor is draining, in
// which case NOTHING was added and the caller must refuse WITHOUT calling
// te.wg.Done.
//
// This is the only place outside a goroutine-launch site that may touch
// te.wg.Add for a dispatch entry point — see dispatchGate's doc comment for
// why the check and the Add must not be separated.
func (te *TaskExecutor) enterDispatch() bool {
	te.dispatchGate.RLock()
	defer te.dispatchGate.RUnlock()
	if te.draining.Load() {
		return false
	}
	te.wg.Add(1)
	return true
}

func (te *TaskExecutor) Drain(budget time.Duration) {
	// Close intake FIRST — see draining's doc comment: with intake open the
	// goal-loop's synchronous redispatch chains keep wg forever non-empty
	// and spin against half-torn-down stores.
	//
	// Under dispatchGate's write lock so it cannot interleave with an
	// enterDispatch that has already passed its draining check but not yet
	// reached its Add — that interleaving is what used to panic wg.Wait below.
	// The lock is released BEFORE wg.Wait: holding it across the wait would
	// block the very dispatchers whose Done() the wait depends on.
	te.dispatchGate.Lock()
	te.draining.Store(true)
	te.dispatchGate.Unlock()

	done := make(chan struct{})
	go func() {
		te.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All task-dispatch goroutines drained cleanly.
	case <-time.After(budget):
		logger.WarnCF("task_executor", "Drain: task-goroutine drain budget exceeded; proceeding with teardown",
			map[string]any{"budget": budget.String()})
	}
}

// SetLifecycleStore installs the durable S2 session-lifecycle store
// (ADR-053/FR-118) every task-dispatch session is minted into (see the
// lifecycleStore field's own doc comment for the gap this closes). Mirrors
// PlanEngine.SetLifecycleStore — the gateway wiring seam calls both with the
// SAME store instance so the boot sweep and this producer agree on one
// durable record per session_id. Optional: leaving it unset (nil) leaves
// task dispatch exactly as it behaved before this wave — sessions created,
// but with no durable lifecycle record and therefore invisible to the boot
// sweep.
func (te *TaskExecutor) SetLifecycleStore(ls *session.LifecycleStore) {
	te.mu.Lock()
	te.lifecycleStore = ls
	te.mu.Unlock()
}

// getLifecycleStore returns the installed lifecycle store (nil if unset),
// guarded by mu so a concurrent SetLifecycleStore never races a goroutine
// reading it mid-dispatch.
func (te *TaskExecutor) getLifecycleStore() *session.LifecycleStore {
	te.mu.Lock()
	defer te.mu.Unlock()
	return te.lifecycleStore
}

// mintTaskLifecycleRecord persists the initial durable S2 lifecycle record
// (ADR-053, state=queued) for the freshly-created task-dispatch session
// sessionID — closing the FR-118/G-13 producer gap described on the
// lifecycleStore field's doc comment. Called synchronously by BOTH
// task-session creation chokepoints — createTaskSessionSync (used by
// ExecuteTask, and therefore by every one of CheckQueuedTasks,
// advanceBlockedTasks, SpawnTriggeredRun, and plan-member dispatch via
// executeTaskPlanVerified) and StartTaskNow's own inline session-creation
// block — so EVERY task-dispatch path gets a record, not just one of them.
//
// OwnerScopeKind: a plan-member task (t.PlanID != "") names its plan as the
// durable owner (OwnerScopePlan/t.PlanID) — the same discriminator a plan's
// own OWNER/SUPERVISION sessions use (pkg/agent/plan_engine.go's
// mintPlanSession, out of this wave's write-set/scope). A standalone task
// has no single owning session, so it takes the same OwnerScopeHuman
// default pkg/tools/delegate.go's own top-level (non-parented) mint uses.
// ParentAgentID/SteeringSessionID are deliberately left empty: a task
// dispatch is not a `delegate.run` call, so there is no delegating parent to
// attribute — and leaving SteeringSessionID empty also means
// verifyCallerOwnsSession (pkg/tools/delegate.go) fails closed if some
// caller ever names a task's session_id in a delegate.* admin action
// (cancel/steer/respond/follow_up/peek/inbox), preserving today's behavior
// where such a call found no record at all.
//
// Best-effort: a mint failure is logged at Error but NEVER propagated —
// unlike delegate.go's `run` (which refuses the WHOLE call on a mint
// failure per FR-015's attribution contract), a task dispatch that already
// claimed the task and created its chat-transcript session must proceed
// even if the durable lifecycle record could not be written; the
// crash-recovery gap this wave closes is strictly better than before
// regardless of this one failure mode.
func (te *TaskExecutor) mintTaskLifecycleRecord(sessionID string, t *task.Task) {
	ls := te.getLifecycleStore()
	if ls == nil || sessionID == "" {
		return
	}
	ownerKind := session.OwnerScopeHuman
	ownerID := ""
	if t.PlanID != "" {
		ownerKind = session.OwnerScopePlan
		ownerID = t.PlanID
	}
	rec := &session.LifecycleRecord{
		SessionID: sessionID,
		// Generation starts at 1 (LifecycleRecord.Generation's own doc
		// comment, and what every other minter writes —
		// steer_launcher.go's launchDirect and launchSteered both use 1).
		// This was 0, which persistLocked rejects, so EVERY task dispatch
		// silently failed to write its durable record: the Persist error is
		// logged and deliberately not propagated, so the task ran on with no
		// lifecycle record ever being born.
		Generation:     1,
		State:          session.LifecycleQueued,
		OwnerScopeKind: ownerKind,
		OwnerScopeID:   ownerID,
		WorkspaceID:    t.WorkspaceID,
		AgentID:        t.AgentID,
	}
	if err := ls.Persist(rec); err != nil {
		logger.ErrorCF("task_executor", "mintTaskLifecycleRecord: failed to persist durable session record",
			map[string]any{"task_id": t.ID, "session_id": sessionID, "error": err.Error()})
	}
}

// transitionTaskLifecycle atomically transitions sessionID's durable S2
// lifecycle record to state (+ failedReason when state==LifecycleFailed),
// preserving every other field — mirrors
// pkg/tools/delegate.go's DelegateTool.transitionLifecycle exactly (same
// Mutate-based atomic RMW rationale: two concurrent transitions on the same
// session_id must serialize through the store's own per-session lock,
// rather than race a manual Load+Persist pair — Correctness-MAJOR-3/S4
// INV-3). Best-effort: an error (including the no-record-yet case, when
// lifecycleStore is nil or the initial mint above failed/was skipped) is
// logged at Warn and never propagated — a durable-record write failure
// must never fail or mask the outcome of the underlying task run.
func (te *TaskExecutor) transitionTaskLifecycle(sessionID string, state session.LifecycleState, failedReason string) {
	ls := te.getLifecycleStore()
	if ls == nil || sessionID == "" {
		return
	}
	// Resolve the chat-transcript UnifiedStore for the mediator's dual-store
	// transition (Defect #28). Nil agentLoop (test harness) or unresolvable
	// session yields nil — the mediator skips the mirror.
	var us *session.UnifiedStore
	if te.agentLoop != nil {
		us = te.agentLoop.ResolveSessionStore(sessionID)
	}
	if err := session.TransitionSession(ls, us, sessionID, state, failedReason); err != nil {
		logger.WarnCF("task_executor", "transitionTaskLifecycle: dual-store transition failed",
			map[string]any{"session_id": sessionID, "state": string(state), "error": err.Error()})
	}
}

// finalizeTaskLifecycle transitions sessionID's durable lifecycle record to
// the terminal state matching a task's own just-written terminal status:
// session.LifecycleCompleted for task.StatusDone, session.LifecycleFailed
// (reason "task_failed") for anything else (task.StatusFailed is the only
// other terminal task status this function is ever called with). Shared by
// completeTaskWithResult and the run loop's already-terminal close-out so the
// status->lifecycle-state mapping lives in exactly one place.
func (te *TaskExecutor) finalizeTaskLifecycle(sessionID string, status task.Status) {
	if status == task.StatusDone {
		te.transitionTaskLifecycle(sessionID, session.LifecycleCompleted, "")
		return
	}
	te.transitionTaskLifecycle(sessionID, session.LifecycleFailed, "task_failed")
}

// ExecuteTask starts executing the dispatchable task identified by taskID. It
// atomically claims the task (next→in_progress via ClaimForRun) and dispatches
// it to the agent in a goroutine, gated by per-agent and global concurrency
// controls. For a plan member task (PlanID != ""), it also enforces
// requirePlanExecuting's plan-state gate — see that method's doc comment.
//
// occurrenceMs (ADR-050 RD3/RD5, task-run-history-spec.md §3.2) is the
// scheduled RRULE instant this dispatch realizes — threaded through from the
// trigger fire (TaskTriggerScheduler.RunScheduled -> SpawnTriggeredRun) so
// the TaskRun opened for this execution carries the same calendar join key
// the occurrence overlay reads. nil for every non-recurring-fire caller
// (redispatch, plan-engine dispatch, auto-advance, heartbeat) — an ad-hoc/
// manual run keyed only by "an open run exists" for this task. Every dispatch
// through this entry point records task.RunKindScheduled; StartTaskNow/
// StartOccurrenceRun (the user-initiated launch paths) record
// task.RunKindManual instead.
func (te *TaskExecutor) ExecuteTask(ctx context.Context, taskID string, occurrenceMs *int64) error {
	// Reserve a wg slot BEFORE checking draining (fix-wave finding #1): the
	// old order — check draining, THEN wg.Add(1) only right before the `go`
	// statement deep inside executeTask (past store reads, ClaimForRun, and
	// the fsync-bound session-creation writes) — left the entire synchronous
	// body of executeTask invisible to Drain's wg.Wait. A dispatch that
	// passed the draining check a moment before Drain() stored the flag could
	// still be reading/claiming/writing through stores Close() was about to
	// tear down, with Drain already returned clean (wg observed 0 the whole
	// time). Reserving here first, then re-checking draining, closes that
	// window: sync/atomic operations are sequentially consistent (Go memory
	// model, Go 1.19+), so either our Add(1) is ordered before Drain's
	// Store(true) — in which case Drain's following wg.Wait is guaranteed to
	// observe our outstanding count — or Drain's Store is ordered before our
	// Add, in which case our own Load below is guaranteed to observe it and
	// refuse before touching any store. The goroutine launched below (if any)
	// gets its OWN independent Add(1)/Done() pair (te.wg's doc comment) that
	// this defer does not double-count.
	if !te.enterDispatch() {
		return ErrExecutorDraining
	}
	defer te.wg.Done()
	return te.executeTask(ctx, taskID, occurrenceMs, task.RunKindScheduled, false)
}

// executeTaskPlanVerified is the ONE documented bypass of the plan-state gate
// executeTask enforces (see requirePlanExecuting) — reserved EXCLUSIVELY for
// PlanEngine.dispatchReadyMembers (plan_engine.go), via the planTaskDispatcher
// interface. Every one of dispatchReadyMembers' own callers
// (tryStartApprovedPlan, processPlan, AppendCorrection) re-reads the plan's
// State (and, as of the paused-state follow-up, PausedReason) under
// pe.planDecisionMu and returns early unless it is Approved-about-to-become-
// Running or Running-and-unpaused, in the SAME critical section that then
// calls dispatchReadyMembers — so re-verifying it a second time here would be
// REDUNDANT, not safer.
//
// It is also not merely an optimization: TaskExecutor.planStore and
// PlanEngine.planStore are two independently-wired fields (see SetPlanStore's
// doc comment) that a boot-ordering bug could leave out of sync (e.g. the
// gateway wires PlanEngine's but not TaskExecutor's). Without this bypass,
// such a bug would make requirePlanExecuting fail closed on "no plan store
// wired" for EVERY plan-engine-driven dispatch — silently stalling every plan
// in the process despite the plan engine itself having correctly verified
// the plan's state through its own, correctly-wired store. The bypass
// decouples "the plan engine already knows this dispatch is authorized" from
// "TaskExecutor's own independent plan-store wiring happens to agree".
//
// Do not add a second caller of this method without the same
// planDecisionMu-held-state-check-immediately-before guarantee — every OTHER
// caller of a dispatch primitive must go through ExecuteTask/StartTaskNow and
// pay the real requirePlanExecuting check.
func (te *TaskExecutor) executeTaskPlanVerified(ctx context.Context, taskID string) error {
	// Same wg-before-draining gate as ExecuteTask/StartTaskNow (fix-wave
	// finding #1) — this bypass entry point skipped both halves entirely,
	// leaving plan-member dispatches invisible to Drain's wg.Wait.
	if !te.enterDispatch() {
		return ErrExecutorDraining
	}
	defer te.wg.Done()
	// D-08/FR-057: plan-member dispatch is the plan engine's own async
	// promotion loop (Tick/runEventLoop), never a live "click and watch" —
	// dispatchReadyMembers's own doc explains why its dispatchCtx is
	// context.WithoutCancel(ctx), which means this stamp survives that
	// chokepoint automatically. Stamped here (executeTaskPlanVerified's own
	// doc calls this "the single chokepoint every plan-member dispatch funnels
	// through") rather than at dispatchReadyMembers's several callers, so no
	// future caller can accidentally dispatch a plan member attended.
	ctx = tools.WithAutoDenyAsk(ctx, true)
	// Plan-member dispatch is never tied to a recurring occurrence — nil,
	// task.RunKindScheduled (matching every other non-manual dispatch path).
	return te.executeTask(ctx, taskID, nil, task.RunKindScheduled, true)
}

// executeTask is ExecuteTask's real body. planVerifiedUnderDecisionMu is true
// ONLY via the executeTaskPlanVerified bypass above; every other caller goes
// through ExecuteTask (which always passes false) and pays the
// requirePlanExecuting check for any task naming a PlanID. This unexported
// method has exactly those two callers in this file — there is no exported
// or otherwise-reachable way to pass true from outside — so the bypass
// cannot be reached by accident.
func (te *TaskExecutor) executeTask(
	ctx context.Context, taskID string, occurrenceMs *int64, kind task.RunKind, planVerifiedUnderDecisionMu bool,
) error {
	t, err := te.store.Get(taskID)
	if err != nil {
		return fmt.Errorf("task_executor: get task %q: %w", taskID, err)
	}
	if t.Status != task.StatusNext {
		return fmt.Errorf("task_executor: task %q is %s, not next", taskID, t.Status)
	}

	// S1 UAT follow-up (primitive-level plan-state gate): the original
	// bc66345f fix placed this gate ONLY in CheckQueuedTasks, one level ABOVE
	// this function — every OTHER caller (advanceBlockedTasks after a
	// completion, SpawnTriggeredRun's cron fire, the goal-loop redispatch
	// below, and transitively a REST "run now") was left free to dispatch a
	// Draft, terminal, or paused plan's member with no gate at all. See
	// requirePlanExecuting's own doc for the full rationale; see
	// executeTaskPlanVerified above for the one legitimate bypass.
	if !planVerifiedUnderDecisionMu {
		if gateErr := te.requirePlanExecuting(t); gateErr != nil {
			return gateErr
		}
	}

	// NOTE: the run_task auto-dispatch approval gate (requireRunTaskAutoDispatchApproved)
	// was removed per operator instruction — there is no task-run approval gate; the
	// tool-policy (allow/deny/ask) governs the tool call only, not auto-dispatch. The
	// function definition is retained as dead code below for reference; it is never called.

	// Guard: do not dispatch a task that still has unsatisfied dependencies.
	if len(t.BlockedBy) > 0 {
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				return fmt.Errorf("task_executor: task %q is blocked by %q (not done)", taskID, depID)
			}
		}
	}

	te.syncDispatchCapacity()
	ok, release := te.dispatchSema.TryAcquire()
	if !ok {
		return fmt.Errorf(
			"%w (%d/%d in flight), retry later",
			ErrDispatchCapReached,
			te.dispatchSema.InFlight(), te.dispatchSema.Cap(),
		)
	}

	registry := te.agentLoop.GetRegistry()
	if _, ok := registry.GetAgent(t.AgentID); !ok {
		release()
		logger.ErrorCF("task_executor", "Agent not found, failing task",
			map[string]any{"task_id": taskID, "agent_id": t.AgentID})
		te.failTask(taskID, fmt.Sprintf("agent %q not found", t.AgentID))
		return fmt.Errorf("task_executor: agent %q not found", t.AgentID)
	}

	// No per-agent cap: dispatchSema (acquired above) is the sole concurrency
	// gate, so a single agent may use the whole configured budget.
	//
	// Atomically claim the task (next→in_progress) under the store lock.
	now := time.Now().UTC()
	claimed, err := te.store.ClaimForRun(taskID, now)
	if err != nil {
		release()
		if errors.Is(err, task.ErrAlreadyClaimed) {
			return fmt.Errorf("task_executor: task %q already claimed by concurrent dispatch", taskID)
		}
		return fmt.Errorf("task_executor: claim task %q for run: %w", taskID, err)
	}
	t = claimed

	// M1 (ADR-052 FR-029): create the task session and persist its SessionID
	// SYNCHRONOUSLY, in THIS call, before the task ever leaves ExecuteTask's
	// own goroutine — not async inside the run goroutine as before. This
	// closes the concurrent-dispatch race SC-005 requires: the plan engine's
	// Stop fan-out (PlanEngine.StopPlan/StopTask) runs under planDecisionMu,
	// the SAME lock dispatchReadyMembers dispatches under, so a member whose
	// SessionID was only assigned asynchronously could be dispatched and
	// then immediately escape a concurrently-running Stop fan-out (the fan-
	// out's snapshot, taken microseconds earlier under the same lock, would
	// have seen no SessionID for it yet). Mirrors StartTaskNow's existing
	// synchronous pattern; unlike StartTaskNow (which aborts the whole call
	// on a session-creation failure), this logs-and-continues — dispatch
	// still proceeds session-less exactly as it always has when sessStore is
	// nil (see createTaskSessionSync's own doc comment for why these two
	// callers' error-handling divergence is intentional, not an oversight).
	taskSessionID, sessErr := te.createTaskSessionSync(t)
	if sessErr != nil {
		logger.ErrorCF("task_executor",
			"Could not create task session (dispatch continues without a session)",
			map[string]any{"task_id": taskID, "agent_id": t.AgentID, "error": sessErr.Error()})
	} else if taskSessionID != "" {
		t.SessionID = taskSessionID
	}

	te.emitStatusChanged(t, task.StatusInProgress)

	taskCtx, cancel := context.WithCancel(ctx)
	te.mu.Lock()
	te.running[taskID] = &taskSlot{cancel: cancel, reserved: false}
	te.mu.Unlock()

	te.wg.Add(1)
	go te.runTask(taskCtx, t, taskSessionID, cancel, release, occurrenceMs, kind)
	return nil
}

// createTaskSessionSync creates task t's session (SessionTypeTask), sets its
// meta (Title/TaskID/WorkspaceID), persists SessionID on the task record via
// te.store.Update, and appends the initial prompt transcript entry — all
// synchronously, in the CALLER's own goroutine (M1/FR-029; see ExecuteTask's
// doc comment for why this matters). Shared by ExecuteTask; StartTaskNow
// performs the equivalent block inline (its own error-handling — abort the
// whole call on failure — deliberately differs from ExecuteTask's log-and-
// continue, so it is not routed through this helper).
//
// Returns ("", nil) when sessStore is nil (no agent store configured for
// t.AgentID) — callers treat that as "no session", not an error, exactly as
// before this refactor moved the block out of runTask's goroutine.
func (te *TaskExecutor) createTaskSessionSync(t *task.Task) (string, error) {
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		logger.ErrorCF("task_executor", "Agent store not found, task will have no session",
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
		return "", nil
	}
	meta, err := sessStore.NewSession(session.SessionTypeTask, "system", t.AgentID)
	if err != nil {
		return "", fmt.Errorf("task_executor: create task session for %q: %w", t.ID, err)
	}
	taskSessionID := meta.ID
	title := t.Title
	taskID := t.ID
	wsID := t.WorkspaceID
	metaPatch := session.MetaPatch{Title: &title, TaskID: &taskID}
	if wsID != "" {
		metaPatch.WorkspaceID = &wsID
	}
	if setErr := sessStore.SetMeta(meta.ID, metaPatch); setErr != nil {
		logger.ErrorCF("task_executor", "Could not set task session meta",
			map[string]any{"task_id": t.ID, "error": setErr.Error()})
	}
	if _, updateErr := te.store.Update(t.ID, task.Patch{SessionID: &taskSessionID}); updateErr != nil {
		logger.ErrorCF("task_executor", "Could not persist session_id on task",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": updateErr.Error()})
	}
	// FR-118/G-13: mint the durable S2 lifecycle record for this session — see
	// mintTaskLifecycleRecord's doc comment for why this is the producer that
	// closes the boot-sweep visibility gap for CheckQueuedTasks,
	// advanceBlockedTasks, SpawnTriggeredRun, and plan-member dispatch (every
	// caller of ExecuteTask funnels through this function).
	te.mintTaskLifecycleRecord(taskSessionID, t)
	if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
		ID:        t.ID + "-prompt",
		Role:      "user",
		Content:   te.buildPrompt(t),
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("task_executor", "Transcript write failed",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
	}
	// GOAL-FR-010/FR-012 (E12): bind this task's own goal record (created in
	// the defining phase at task creation/edit — rest_tasks.go's
	// syncTaskGoalRecord) into the active phase against the session just
	// minted. See activateTaskGoal's own doc comment for the full contract.
	//
	// silent-SF-9: the error is DELIBERATELY not propagated out of
	// createTaskSessionSync — GOAL-FR-023 is explicit that a task with no
	// usable goal still runs — but it is no longer discarded at the point of
	// failure either: activateTaskGoal has already logged at ERROR and written
	// the failure into this task's own transcript before returning it.
	_ = te.activateTaskGoal(t, taskSessionID)
	return taskSessionID, nil
}

// activateTaskGoal is GOAL-FR-010/FR-012's task-side activation: transitions
// the task's own pkg/goal record (created up front in the defining phase at
// task creation/edit time — pkg/gateway/rest_tasks.go's syncTaskGoalRecord,
// GOAL-FR-009) into the active phase, bound to the session this dispatch
// just minted. Called from BOTH createTaskSessionSync (ExecuteTask's own
// dispatch path) and StartTaskNow's equivalent inline session-creation
// block — the two places a task session is minted (see createTaskSessionSync's
// own doc comment for why StartTaskNow does not route through it).
//
// A task with no paired goal record (GOAL-FR-023: a task created before
// D-C's criteria+DoD-mandatory-at-creation rule, or a test fixture that
// never called syncTaskGoalRecord) is left alone — this is NOT an error.
// The task still runs; it simply never enters the goal loop, exactly as it
// did before this wave, and pkg/agent/judge.go's SoftTierCriterion path
// judges it the same way it always has.
//
// R-04 (re-run, GOAL-FR-028): a task-owned goal that already reached a
// terminal state on a PRIOR run re-enters active via Reactivate rather than
// minting a second goal record — rest_tasks.go's syncTaskGoalRecord never
// creates a second record for a task that already has one (GetByOwner finds
// it and Update()s it in place instead), so this is the ONLY place a
// re-run's goal state actually flips back to active. An already-active
// record (double-activation defensiveness — should not happen under the
// single-dispatch-per-claim invariant ClaimForRun enforces, but a defensive
// no-op costs nothing) is left untouched.
//
// ADR-086 (S6): the session-meta mirror this function used to write after
// activating the record (GoalID/GoalCondition/GoalCriteriaJSON/
// GoalRoundsUsed/GoalMaxRounds/GoalLatestReason/GoalStartedAt/
// GoalLastActivityAt/GoalQuestionRoundsUsed/GoalZeroOutputPushes) is GONE.
// It existed for exactly one reason — checkGoalLoopAfterTurn (goal_loop.go)
// and the keeper drivers (goal_triggers.go) read their entry condition off
// session.UnifiedMeta, so a task-owned goal had to be made to look like a
// chat-owned one there. Those readers are re-pointed now: they resolve the
// ACTIVE goal record BOUND to the turn's session (activeGoalForSession,
// goal_record_wiring.go), which Activate below sets to taskSessionID, so
// GOAL-FR-013's "one code path" holds without a second copy of the state.
//
// The "task_explicit" GoalCriteriaJSON sentinel goes with it, and its
// purpose survives structurally: the D3 "unregistered goal" nudge ladder
// now tests the record's own criteria list (len(rec.Criteria) == 0), and a
// task goal's criteria are fixed on its record at creation (D-C), so the
// ladder stays unreachable for a task-owned goal (GOAL-FR-020).
//
// That unreachability is the NUDGE LADDER's alone, and says nothing about
// the keeper that hosts it. The quiet-window keeper
// (goal_triggers.go::goalQuietWindowSettle) selects ACTIVE goal records of
// BOTH owner kinds (GOAL-FR-015, C-24): once the record below goes active
// it is swept exactly like a chat goal's, so a task that goes quiet gets
// the six suppressions (GOAL-FR-016), the bounded continue-push
// (GOAL-FR-017) and the one-action-per-quiet-spell re-arm (GOAL-FR-018).
// An earlier revision of this comment asserted the keeper "selects
// session-owned records only" — it did, and that was the defect
// GOAL-FR-015 names, not a design. Do not restore that filter.
//
// It returns an error (it used to be a void function, silent-SF-9). Neither
// caller treats that error as fatal — GOAL-FR-023 is explicit that a task
// with no usable goal record still runs — but the failure is now reported
// where it happens (ERROR log plus a line in the task's OWN session
// transcript, reportTaskGoalActivationFailure) rather than swallowed, so a
// task running with no goal loop is visible instead of merely quiet. A task
// with no paired record at all is NOT one of those failures and returns nil.
//
// Activation also RECORDS THE GOAL'S ROUTING (review finding 10) — the step
// both chat activation paths take and this one did not, which left every
// task-owned goal unreachable by the keeper. See the call to
// recordGoalRouting below for the full failure it closes.
func (te *TaskExecutor) activateTaskGoal(t *task.Task, taskSessionID string) error {
	if t == nil || taskSessionID == "" {
		return fmt.Errorf("task_executor: activate task goal: a task and a session id are both required")
	}
	gstore := resolveGoalRecordStore()
	g, err := gstore.GetByOwner(generated.GoalOwnerKindTask, t.ID)
	if err != nil {
		if !errors.Is(err, goal.ErrOwnerNotFound) {
			return te.reportTaskGoalActivationFailure(t, taskSessionID, "",
				fmt.Errorf("task_executor: look up goal record for task %q: %w", t.ID, err))
		}
		if t.Scratchpad {
			// A set_todos checklist card never enters the goal loop (FR-048).
			return nil
		}
		// Founder decision 2026-09-14: a task completes ONLY when its goal's
		// claim is upheld by the Judge, and the worker claims with goal_claim,
		// which needs an active goal bound to the run's session. A legacy task
		// with no paired record (created before D-C, GOAL-FR-023) therefore
		// gets one minted here, at its first run: its own criteria (or none —
		// the Judge then uses the soft-tier criterion) plus the built-in floor
		// Definition of Done every compiled goal carries (ADR-080 D-DOD layer
		// 3). The task still runs and is still judged, as GOAL-FR-023 requires;
		// it now does so through the one claim path.
		minted, mErr := te.mintLegacyTaskGoal(t)
		if mErr != nil {
			return te.reportTaskGoalActivationFailure(t, taskSessionID, "",
				fmt.Errorf("task_executor: create a goal record for legacy task %q: %w", t.ID, mErr))
		}
		logger.InfoCF("task_executor", "goal: legacy task had no goal record — one was created for this run",
			map[string]any{"task_id": t.ID, "goal_id": minted.GoalID, "session_id": taskSessionID})
		g = minted
	}

	now := time.Now().UTC()
	// Founder decision 2026-09-14 (D-D/D-E): a task's goal takes the goal try
	// limit in force when its RUN starts — the same snapshot semantics a chat
	// goal has at `/goal` set time (activateInstantGoal). The record's
	// MaxRounds was written at task creation, possibly days and several
	// Settings changes ago, so it is re-stamped from the live value here, AFTER
	// the transition (Reactivate archives the previous run's record into
	// TerminalHistory and must archive that run's own limit, not this one's).
	// The run loop (task_run_loop.go::runGoalMaxTries) enforces this snapshot, so a later Settings
	// change never moves the bound of a run already in progress.
	tryLimit := goalTryLimit(te.agentLoop)
	if _, uerr := gstore.Update(g.GoalID, func(cur *goal.Goal) error {
		switch {
		case cur.IsDefining():
			if err := cur.Activate(taskSessionID, now); err != nil {
				return err
			}
			cur.MaxRounds = tryLimit
			return nil
		case cur.IsTerminal():
			if err := cur.Reactivate(taskSessionID, now); err != nil {
				return err
			}
			cur.MaxRounds = tryLimit
			return nil
		default:
			// Already active. Under the two-level run model this can only mean
			// a terminal writer skipped its goal hook: every legitimate path
			// into a fresh run leaves the record TERMINAL first (a failed run
			// ends its goal before the restart re-enters here —
			// consumeTaskAttempt), so Reactivate re-binds it with a fresh try
			// budget. A record left active by a previous run would make THIS
			// run inherit that run's rounds, per-criterion statuses, latest
			// reason and keeper budgets, with no TerminalHistory entry — the
			// D-2 damage. Forcing a transition is not the answer (Reactivate
			// refuses a non-terminal record, and inventing one would fake an
			// adjudication); making it LOUD is. WARN, not Debug.
			logger.WarnCF("task_executor",
				"goal: paired goal record was already ACTIVE at run start — Goal.Reactivate is being "+
					"skipped, so this run inherits the PREVIOUS run's attempts, rounds, criterion "+
					"statuses and keeper budgets (R-04 re-entry contract not applied). Some terminal "+
					"writer did not end this record when its task last terminated.",
				map[string]any{"task_id": t.ID, "goal_id": g.GoalID, "session_id": taskSessionID})
			return nil
		}
	}); uerr != nil {
		return te.reportTaskGoalActivationFailure(t, taskSessionID, g.GoalID,
			fmt.Errorf("task_executor: activate goal record %q for task %q: %w", g.GoalID, t.ID, uerr))
	}

	// Review finding 10: record this goal's ROUTING, exactly as the two chat
	// activation paths do (goal_loop.go's applyGoalCommandPrompt and
	// activateInstantGoal both call recordGoalRouting immediately after
	// activating). This call site did not exist, so a task-owned goal carried
	// no RouteChannel/RouteChatID on its record and no entry in the in-memory
	// routing map. Now that goalQuietWindowSettle selects task-owned records
	// (GOAL-FR-015), that omission was load-bearing: dispatchGoalAsyncFollowUp
	// resolves its destination through routeFor, which found nothing on either
	// side, so NO keeper push ever reached a quiet task — and the miss took
	// routeFor's RecordRoutingLost branch, stamping "keeper cannot reach the
	// goal's channel — routing lost" into LatestReason, where it surfaced to
	// the user in the goal status frame as if the goal itself were broken.
	//
	// The destination is the task's own SourceChannel/SourceChatID with the
	// same "system"/"task:<id>" fallback wakeOwnerAttemptsExhausted already
	// uses for a board/REST-created task with no chat origin —
	// AsyncNotifier.Notify rejects an empty destination outright (FR-N7), so
	// the fallback is what makes a board-created task reachable at all.
	//
	// The session key is empty by design: GOAL-FR-032 deleted the persisted
	// session-key field and routeFor reads it from nothing (see
	// recordGoalRouting's own doc comment).
	if te.agentLoop != nil {
		channel, chatID := t.SourceChannel, t.SourceChatID
		if channel == "" || chatID == "" {
			channel, chatID = "system", "task:"+t.ID
		}
		te.agentLoop.recordGoalRouting(taskSessionID, g.GoalID, channel, chatID, "", t.AgentID)
	}

	logger.InfoCF("task_executor", "goal: task goal record activated for this run",
		map[string]any{"task_id": t.ID, "goal_id": g.GoalID, "session_id": taskSessionID})
	return nil
}

// reportTaskGoalActivationFailure is silent-SF-9's loud surface: it makes a
// task-side goal-activation failure as VISIBLE as the chat side's already is.
//
// The chat path fails in front of the user — createAndActivateSessionGoalRecord
// returning an error makes applyGoalCommandPrompt reply "Could not start the
// goal loop (internal error persisting the goal record)" straight into the
// chat. The task path used to swallow the identical failure in a void function
// with one WARN line and a bare return, so a task ran to completion with no
// goal loop, no adjudication and no criteria ever judged, and nothing anywhere
// the operator would look said so. That asymmetry breaks ADR-086's
// identical-behaviour promise (GOAL-FR-013) in exactly the direction that
// hides a defect.
//
// It logs at ERROR and writes a system line into the task's OWN session
// transcript — the task-side equivalent of the chat reply, and the surface an
// operator reading the run actually sees — then returns err for the caller to
// propagate or log.
func (te *TaskExecutor) reportTaskGoalActivationFailure(t *task.Task, taskSessionID, goalID string, err error) error {
	logger.ErrorCF("task_executor", "goal: task goal activation failed — this task will run with NO goal loop (no adjudication, no criteria judged)",
		map[string]any{"task_id": t.ID, "goal_id": goalID, "session_id": taskSessionID, "error": err.Error()})
	if te.agentLoop != nil {
		if sessStore := te.agentLoop.taskSessionStore(taskSessionID, t.AgentID); sessStore != nil {
			te.agentLoop.writeGoalSystemTranscript(sessStore, taskSessionID, t.AgentID, fmt.Sprintf(
				"This task's goal could not be activated (%v). The run continues WITHOUT a goal loop: no acceptance criteria will be adjudicated for it.",
				err))
		}
	}
	return err
}

// SetPlanStore installs the shared *plan.Store so CheckQueuedTasks' plan-gate
// (see its doc) can resolve a plan member task's parent plan state. Wired at
// the gateway boot seam right alongside AgentLoop.SetPlanStore (the same
// planStore value goes to both — see gateway.go's boot wiring region);
// mirrors SetEvidenceCommitter's late-binding discipline. Leaving it unset
// (nil, the test-harness default) makes the gate fail-closed for any task
// carrying a PlanID — see planForGate.
//
// Guarded by mu (fix-wave finding #3) — see SetEvidenceCommitter's doc
// comment for the identical race this closes.
func (te *TaskExecutor) SetPlanStore(store *plan.Store) {
	te.mu.Lock()
	te.planStore = store
	te.mu.Unlock()
}

// getPlanStore returns the installed plan store (nil if unset), guarded by mu
// so a concurrent SetPlanStore never races a goroutine reading it mid-gate.
// Mirrors getLifecycleStore exactly.
func (te *TaskExecutor) getPlanStore() *plan.Store {
	te.mu.Lock()
	defer te.mu.Unlock()
	return te.planStore
}

// emitStatusChanged publishes a task_status_changed event onto the agent event
// bus. Best-effort; a nil agentLoop is silently skipped.
func (te *TaskExecutor) emitStatusChanged(t *task.Task, status task.Status) {
	if te.agentLoop == nil {
		return
	}
	sessionID := t.SessionID
	if sessionID == "" {
		sessionID = "task:" + t.ID
	}
	te.agentLoop.EmitTaskStatusChanged(TaskStatusChangedPayload{
		TaskID:    t.ID,
		Status:    string(status),
		SessionID: sessionID,
		AgentID:   t.AgentID,
	})
}

// activeRun carries the identity of an open TaskRun record (ADR-050, an
// additive record layer — docs/internal/specs/task-run-history-spec.md
// §3.2/3.3) from the point it is opened in runTask or runTaskFromInProgress
// (right after each function's own session is available — see openRun's own
// doc comment for why run-open cannot happen any earlier) through to the
// point it is closed in completeTaskWithResult.
//
// nil means "no run is being tracked for this execution": openRun's own call
// to Store.OpenRun failed (openRun degrades to nil rather than aborting the
// dispatch) — the ONE remaining nil case now that runTaskFromInProgress/
// StartTaskNow also opens a run (BLK-3, operator decision 2026-07-20; every
// production dispatch path participates in run-history today). Test-only
// direct calls into finishRunTurn/completeTaskWithResult (see
// task_run_loop_test.go) also legitimately pass nil.
// Task.status/result/session_id keep their exact existing behavior
// regardless of whether a run is being tracked (RD2) — activeRun only ever
// ADDS a parallel record, never gates or alters the mirror.
type activeRun struct {
	runID        string
	occurrenceMs *int64
}

// StartTaskNow creates the task session, sets session_id on the task, registers
// the cancel in the running map, and launches the agent goroutine — all without
// requiring the task to be in `next` first (the caller has already transitioned
// it to `in_progress` via a PATCH). It is the path taken when the UI hits
// "Run" on a task that already has an assigned agent.
//
// For a plan member task (PlanID != ""), it also enforces
// requirePlanExecuting's plan-state gate (see that method's doc) — unlike
// ExecuteTask, StartTaskNow has NO bypass: its one production caller (the
// REST PATCH-to-in_progress handler) is always a single, independent
// dispatch decision, never one made immediately after verifying plan state
// under planDecisionMu the way PlanEngine.dispatchReadyMembers is. This is
// the fix for the bypass a REST PATCH to in_progress on a Draft (or
// Stopped/paused) plan's member previously sailed straight through: the
// launch block only ever checked the TASK's own status transition, never
// its PlanID.
//
// Idempotency: if the task already has a SessionID the call is a no-op and
// returns the existing session ID immediately without launching a second agent.
//
// Returns the session ID that was created (or already existed) on success, or
// an empty string and an error when the task cannot be found, already has no
// agent, or the concurrency cap is exhausted.
func (te *TaskExecutor) StartTaskNow(ctx context.Context, taskID string) (string, error) {
	// Reserve a wg slot BEFORE checking draining — see ExecuteTask's identical
	// fix and its doc comment (fix-wave finding #1) for the full race this
	// closes and why the ordering (Add, then Load) is race-free under Go's
	// sequentially-consistent atomics. Every early return below (agent not
	// found, already running, dispatch cap reached, session-creation failure,
	// ...) is now covered by this single deferred Done(); the goroutine
	// launched near the bottom of this function keeps its own separate
	// Add(1)/Done() pair (unchanged).
	if !te.enterDispatch() {
		return "", ErrExecutorDraining
	}
	defer te.wg.Done()
	t, err := te.store.Get(taskID)
	if err != nil {
		return "", fmt.Errorf("task_executor: StartTaskNow get task %q: %w", taskID, err)
	}
	if t.AgentID == "" {
		return "", fmt.Errorf("task_executor: StartTaskNow: task %q has no agent assigned", taskID)
	}

	// S1 UAT follow-up (see this function's own doc comment and
	// requirePlanExecuting's): no bypass here, unlike ExecuteTask's
	// executeTaskPlanVerified for the plan engine's own dispatch.
	if gateErr := te.requirePlanExecuting(t); gateErr != nil {
		return "", gateErr
	}
	if te.launcher != nil {
		return te.startTaskNowViaLauncher(ctx, t)
	}

	// Idempotency guard: if a session already exists, don't create another one.
	if t.SessionID != "" {
		return t.SessionID, nil
	}

	// Atomically claim the slot: under a single te.mu critical section, re-check
	// whether a goroutine is already running AND insert a sentinel cancel so
	// competing concurrent callers observe the slot as taken before we unlock.
	// This closes the TOCTOU window where two concurrent StartTaskNow calls could
	// both pass the running-check before either one registered its goroutine.
	//
	// A nil sentinel marks "slot reserved, cancel not yet set". The goroutine
	// replaces it with the real cancel before returning. If setup fails we delete
	// the slot so the task is not permanently locked.
	te.mu.Lock()
	if _, alreadyRunning := te.running[taskID]; alreadyRunning {
		te.mu.Unlock()
		// A goroutine is live (or starting up); the session_id may have been
		// written by now — re-read.
		fresh, rerr := te.store.Get(taskID)
		if rerr == nil && fresh.SessionID != "" {
			return fresh.SessionID, nil
		}
		return "", fmt.Errorf("task_executor: StartTaskNow: task %q goroutine already running", taskID)
	}
	// Reserve the slot explicitly so competing callers bail at the check above.
	// reserved=true, cancel=nil: slot is claimed but the goroutine has not started yet.
	te.running[taskID] = &taskSlot{reserved: true}
	te.mu.Unlock()

	// releaseSlot removes the reservation if setup fails so future callers can
	// retry. A no-op after the goroutine successfully replaces the sentinel.
	slotReleased := false
	releaseSlot := func() {
		if !slotReleased {
			slotReleased = true
			te.mu.Lock()
			delete(te.running, taskID)
			te.mu.Unlock()
		}
	}
	defer releaseSlot()

	// Check that the assigned agent is known.
	registry := te.agentLoop.GetRegistry()
	if registry == nil {
		return "", fmt.Errorf("task_executor: StartTaskNow: agent registry is not available")
	}
	if _, ok := registry.GetAgent(t.AgentID); !ok {
		return "", fmt.Errorf("task_executor: StartTaskNow: agent %q not found for task %q", t.AgentID, taskID)
	}

	te.syncDispatchCapacity()
	ok, release := te.dispatchSema.TryAcquire()
	if !ok {
		return "", fmt.Errorf(
			"%w (%d/%d in flight), retry later",
			ErrDispatchCapReached,
			te.dispatchSema.InFlight(), te.dispatchSema.Cap(),
		)
	}

	// Create the session synchronously so we can return the session_id to the
	// caller before the goroutine starts.
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	var taskSessionID string
	if sessStore != nil {
		meta, sessErr := sessStore.NewSession(session.SessionTypeTask, "system", t.AgentID)
		if sessErr != nil {
			release()
			return "", fmt.Errorf("task_executor: StartTaskNow: create session for task %q: %w", taskID, sessErr)
		}
		taskSessionID = meta.ID

		title := t.Title
		tid := t.ID
		wsID := t.WorkspaceID
		metaPatch := session.MetaPatch{Title: &title, TaskID: &tid}
		if wsID != "" {
			metaPatch.WorkspaceID = &wsID
		}
		if setErr := sessStore.SetMeta(meta.ID, metaPatch); setErr != nil {
			logger.ErrorCF("task_executor", "StartTaskNow: could not set task session meta",
				map[string]any{"task_id": taskID, "error": setErr.Error()})
		}
		updated, updateErr := te.store.Update(taskID, task.Patch{SessionID: &taskSessionID})
		if updateErr != nil {
			logger.ErrorCF("task_executor", "StartTaskNow: could not persist session_id on task",
				map[string]any{"task_id": taskID, "session_id": taskSessionID, "error": updateErr.Error()})
		} else {
			t = updated
		}
		// FR-118/G-13: mint the durable S2 lifecycle record for this session —
		// see mintTaskLifecycleRecord's doc comment. StartTaskNow is the SECOND
		// (and only other) task-session creation chokepoint besides
		// createTaskSessionSync; both must call this so every dispatch path
		// gets a record.
		te.mintTaskLifecycleRecord(taskSessionID, t)
		if err := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
			ID:        t.ID + "-prompt",
			Role:      "user",
			Content:   te.buildPrompt(t),
			Timestamp: time.Now().UTC(),
		}); err != nil {
			taskGoalTranscriptWriteFailures.Add(1)
			logger.WarnCF("task_executor", "StartTaskNow: transcript write failed",
				map[string]any{"task_id": taskID, "session_id": taskSessionID, "error": err.Error()})
		}
		// GOAL-FR-010/FR-012 (E12): see activateTaskGoal's doc comment —
		// StartTaskNow is the second of the two task-session-creation
		// chokepoints and must activate the task's goal record exactly like
		// createTaskSessionSync does. The error is reported by
		// activateTaskGoal itself (ERROR log + a line in the task's own
		// transcript) and is not fatal to the run — see the sibling call in
		// createTaskSessionSync.
		_ = te.activateTaskGoal(t, taskSessionID)
	} else {
		logger.WarnCF("task_executor", "StartTaskNow: no agent store found, task will have no session",
			map[string]any{"task_id": taskID, "agent_id": t.AgentID})
	}

	te.emitStatusChanged(t, task.StatusInProgress)

	// Detach from the caller's context (typically an HTTP request context that
	// gets canceled as soon as the response is sent). The goroutine must outlive
	// the HTTP request; the explicit cancel stored in te.running[taskID] is the
	// intended cancellation path (a future "cancel task" API).
	//
	// D-08/FR-057: StartTaskNow has two callers with different attended-ness —
	// the REST "Run now"/"Start Task" handlers (rest_tasks.go, a literal user
	// click, ctx carries no AutoDenyAsk marker so this defaults false/attended)
	// and the run_task AGENT TOOL (pkg/tools/run_task.go), whose ctx is the
	// calling turn's own execCtx and so already carries whatever AutoDenyAsk
	// that turn was stamped with (true if the calling turn is itself headless,
	// e.g. a task dispatched via run_task from inside a Calendar-triggered
	// run). Read it from the caller's ctx BEFORE detaching to Background()
	// below, or the value is silently lost and the child always defaults
	// attended regardless of its caller.
	//
	// Replace the reserved slot (inserted above, cancel==nil, reserved==true)
	// with a live slot (cancel set, reserved==false) under the same mutex so
	// any concurrent reader always observes a consistent, named state.
	autoDenyAsk := tools.ToolAutoDenyAsk(ctx)
	taskCtx, cancel := context.WithCancel(context.Background())
	taskCtx = tools.WithAutoDenyAsk(taskCtx, autoDenyAsk)
	te.mu.Lock()
	te.running[taskID] = &taskSlot{cancel: cancel, reserved: false}
	te.mu.Unlock()
	slotReleased = true // goroutine now owns the slot; don't let releaseSlot clear it

	te.wg.Add(1)
	go te.runTaskFromInProgress(taskCtx, t, taskSessionID, cancel, release)
	return taskSessionID, nil
}

// startTaskNowViaLauncher is FR-A-010's single task front: Launch+Dispatch
// for a task without a session and Dispatch alone for an existing session.
func (te *TaskExecutor) startTaskNowViaLauncher(ctx context.Context, t *task.Task) (string, error) {
	if t.SessionID != "" {
		generation := 1
		if lifecycle := te.getLifecycleStore(); lifecycle != nil {
			if rec, err := lifecycle.Load(t.SessionID); err == nil {
				generation = rec.Generation
			}
		}
		if _, err := te.launcher.Dispatch(ctx, t.SessionID, generation); err != nil {
			return "", fmt.Errorf("task_executor: StartTaskNow: dispatch existing session: %w", err)
		}
		return t.SessionID, nil
	}

	steeringSessionID := ""
	if t.OriginSessionID != "" {
		if sessions := te.agentLoop.GetSessionStore(); sessions != nil {
			if _, err := sessions.GetMeta(t.OriginSessionID); err == nil {
				steeringSessionID = t.OriginSessionID
				// ADR-093 D6: a task from a stopped or finished chat runs as
				// an ordinary root - the task's start never revives the
				// creating conversation (only a human message or a parent
				// follow-up revives). Emptying the steering id here drops the
				// launch to launchOrdinaryRoot, so the task runs against the
				// task's own workspace/owner instead of the inactive chat.
				// The loop's own lifecycle store, not te.lifecycleStore: the
				// loop's store is always wired (it is the store the revival
				// paths read), while the executor's optional injection may be
				// nil.
				if lifecycle := te.agentLoop.GetSessionLifecycleStore(); lifecycle != nil {
					if rec, lerr := lifecycle.Load(t.OriginSessionID); lerr == nil &&
						(rec.Terminal() || (rec.Stop != nil && rec.Stop.Generation == rec.Generation)) {
						steeringSessionID = ""
					}
				}
			}
		}
	}
	req := steer.LaunchRequest{
		SteeringSessionID: steeringSessionID,
		TargetAgentID:     t.AgentID,
		Label:             t.Title,
		Task:              te.buildPrompt(t),
		Origin: steer.Origin{
			Kind:   steer.OriginKindTask,
			CallID: t.OriginCallID,
			TaskID: t.ID,
		},
		PlanID: t.PlanID,
	}
	if steeringSessionID == "" {
		req.WorkspaceID = t.WorkspaceID
		req.Owner = t.Owner
	}
	launched, err := te.launcher.Launch(ctx, req)
	if err != nil {
		// ADR-093 D5: the same plain sentence as the delegate tool when
		// the launch is refused because the conversation is not active
		// (e.g. the creator stopped between the gate above and Launch).
		if steer.IsSteeringUnavailable(err) {
			return "", errors.New(steer.SteeringUnavailableMessage)
		}
		return "", fmt.Errorf("task_executor: StartTaskNow: launch: %w", err)
	}
	updated, err := te.store.Update(t.ID, task.Patch{SessionID: &launched.SessionID})
	if err != nil {
		return "", fmt.Errorf("task_executor: StartTaskNow: persist session id: %w", err)
	}
	_ = te.activateTaskGoal(updated, launched.SessionID)
	if _, err := te.launcher.Dispatch(ctx, launched.SessionID, launched.Generation); err != nil {
		return "", fmt.Errorf("task_executor: StartTaskNow: dispatch: %w", err)
	}
	return launched.SessionID, nil
}

// dispatchLaunchedTask enters the existing task orchestration after the
// shared steer admission gate has accepted a task-origin session.
func (te *TaskExecutor) dispatchLaunchedTask(rec *session.LifecycleRecord, release func()) error {
	if rec == nil || rec.Origin == nil || rec.Origin.TaskID == "" {
		return fmt.Errorf("task_executor: dispatched task session has no task origin")
	}
	t, err := te.store.Get(rec.Origin.TaskID)
	if err != nil {
		return fmt.Errorf("task_executor: load launched task %q: %w", rec.Origin.TaskID, err)
	}
	if !te.enterDispatch() {
		return ErrExecutorDraining
	}

	te.mu.Lock()
	if _, exists := te.running[t.ID]; exists {
		te.mu.Unlock()
		te.wg.Done()
		return fmt.Errorf("task_executor: task %q already running", t.ID)
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	te.running[t.ID] = &taskSlot{cancel: cancel}
	te.mu.Unlock()

	if t.SessionID != rec.SessionID {
		updated, updateErr := te.store.Update(t.ID, task.Patch{SessionID: &rec.SessionID})
		if updateErr != nil {
			cancel()
			te.mu.Lock()
			delete(te.running, t.ID)
			te.mu.Unlock()
			te.wg.Done()
			return fmt.Errorf("task_executor: bind launched session: %w", updateErr)
		}
		t = updated
	}
	te.emitStatusChanged(t, task.StatusInProgress)
	go te.runTaskFromInProgress(taskCtx, t, rec.SessionID, cancel, release)
	return nil
}

// SpawnTriggeredRun dispatches a fresh run of a task that a time trigger just
// fired. The task has already been reset to `next` by Store.SpawnReset; this
// claims and dispatches it via the normal ExecuteTask path. ExecuteTask
// guards status==next, concurrency, AND (since the S1 primitive-level plan-
// gate fix) the task's parent plan state via requirePlanExecuting — so a
// cron-triggered plan member whose plan has since been stopped or paused is
// refused here too, with no separate gate needed in this function.
//
// STALE-COMMENT CORRECTION: this used to claim "ExecuteTask already guards
// status==next and concurrency, so no additional gate is needed here" as
// its FULL justification. That was true before bc66345f (the original S1
// fix), which added a plan-state gate but placed it one level ABOVE
// ExecuteTask (in CheckQueuedTasks only) — for a while after that commit,
// this comment was actively WRONG about this function's own safety: a plan
// stopped/paused after its cron trigger fired had no gate at all here. The
// primitive-level fix (this file, requirePlanExecuting) closes that gap;
// this comment is updated to describe the CURRENT, actually-gated behavior
// rather than repeat a claim that had quietly stopped being true.
func (te *TaskExecutor) SpawnTriggeredRun(ctx context.Context, taskID string, occurrenceMs *int64) error {
	return te.ExecuteTask(ctx, taskID, occurrenceMs)
}

// StartOccurrenceRun is the calendar's per-occurrence Run-now entry point
// (ADR-050 RD7, task-run-history-spec.md §3.4) — POST /api/v1/tasks/{id}/runs
// (handleTaskRunNow, pkg/gateway/rest_task_runs.go). With occurrenceMs it
// materializes-on-demand that specific recurring occurrence; without it, it
// re-runs a normal/once task as a fresh run (the prior run is preserved, not
// overwritten — this supersedes the old ADR-049 fresh-run reset, which
// clobbered it). occurrenceMs is threaded straight into the dispatch as the
// TaskRun's calendar join key, with kind always task.RunKindManual (every
// launch through here is user-initiated) — see runTaskFromInProgress's own
// doc comment for why StartTaskNow (the OTHER manual entry point) hardcodes
// occurrenceMs=nil instead.
//
// SpawnReset first resets the task to `next` — mirrors TaskTriggerScheduler.
// RunScheduled's own reset-then-dispatch sequence (task_trigger.go) so this
// entry point can claim a task that is not necessarily `next` yet (a
// recurring task's Status does not cycle the same way a once task's does
// between fires). Returns task.ErrAlreadyRunning if the task is currently
// in_progress — a concurrent scheduler fire or an earlier Run-now already
// claimed it; the caller (handleTaskRunNow) surfaces this as an error rather
// than silently double-dispatching.
//
// Dispatch then reuses executeTask's exact claim (ClaimForRun)-and-launch
// path ExecuteTask uses — the same exactly-once dispatch guard a scheduled
// fire relies on — so a concurrent scheduler fire for the SAME occurrence and
// this manual Run-now cannot both win the claim. OpenRun's own
// (taskID, occurrenceMs) idempotency (called from inside the launched
// goroutine, not here) is what then makes this idempotent against a run
// either side already opened — see
// TestStartOccurrenceRun_IdempotentAgainstConcurrentSchedulerFire.
//
// The caller-supplied context is intentionally IGNORED as the dispatch
// parent (hence "_", not "ctx"). handleTaskRunNow's only production caller
// invokes this with r.Context(), and executeTask derives the goroutine's
// context as a direct child of its own ctx argument via context.WithCancel —
// net/http cancels a request's context the instant the handler returns
// after WriteHeader(202) flushes, which would abort the just-launched agent
// run almost immediately with "turn not started: context canceled"
// (live-UAT-reproduced 2/2 against the merged release build, 2026-08-07).
// Detaching onto context.Background() mirrors StartTaskNow's identical fix
// (this file — see its own "Detach from the caller's context" comment) and
// PlanEngine.dispatchReadyMembers' context.WithoutCancel(ctx) use
// (plan_engine.go) before its own executeTaskPlanVerified call, for the
// exact same reason; the per-task cancel stored in te.running[taskID]
// remains the intended cancellation path (a future "cancel task" API).
//
// This exact fix already shipped once, 7-reviewer-approved, on 2026-07-20
// (commit 4352ebbe: "Run-now cancelled itself... Detach onto
// context.Background() (mirrors StartTaskNow)") but was lost in a later
// cross-branch merge (ab1c1aad, 2026-08-06, release/v0.1.1 into
// feature/plan-swimlane-board) that resolved this function back to
// threading ctx through — the regression that then rode PR #597 into the
// release build. Re-applying it here; do not re-thread ctx through again.
//
// The draining check mirrors the one 3bef0d16 added to ExecuteTask/
// StartTaskNow (but never to this sibling entry point): detaching onto
// context.Background() removes the free "canceled the moment the caller's
// context dies" backstop request-context threading accidentally provided —
// without this check, a Run-now racing AgentLoop.Close/TaskExecutor.Drain
// could dispatch a goroutine Drain's bounded wg.Wait can only wait out, not
// prevent from starting in the first place.
func (te *TaskExecutor) StartOccurrenceRun(_ context.Context, taskID string, occurrenceMs *int64) error {
	// Same wg-before-draining order as ExecuteTask/StartTaskNow (fix-wave
	// finding #1): reserve the slot first so a dispatch that passes the gate
	// concurrently with Drain's Store(true) is still visible to its wg.Wait.
	if !te.enterDispatch() {
		return ErrExecutorDraining
	}
	defer te.wg.Done()
	if _, err := te.store.SpawnReset(taskID); err != nil {
		return fmt.Errorf("task_executor: StartOccurrenceRun: reset task %q: %w", taskID, err)
	}
	// D-08/FR-057: this IS the "run now" a user clicks while watching
	// (handleTaskRunNow, POST /api/v1/tasks/{id}/runs) — RunKindManual is
	// exactly that per its own doc. context.Background() here is deliberate,
	// not an oversight (the caller's ctx is even named `_` above): it carries
	// no AutoDenyAsk marker, so ToolAutoDenyAsk defaults false and the run
	// stays attended — cards keep showing, matching FR-057's "a turn a person
	// actually started must keep showing cards."
	return te.executeTask(context.Background(), taskID, occurrenceMs, task.RunKindManual, false)
}

// ResizeDispatchSema updates the global dispatch semaphore capacity.
func (te *TaskExecutor) ResizeDispatchSema(newCap int) {
	te.dispatchSema.Resize(newCap)
	logger.InfoCF("task_executor", "Dispatch semaphore resized",
		map[string]any{"new_cap": te.dispatchSema.Cap(), "in_flight": te.dispatchSema.InFlight()})
}

// syncDispatchCapacity re-resolves Performance.EffectiveMaxParallelAgents()
// and resizes dispatchSema when it has drifted from the currently-applied
// capacity. Called at the top of every dispatch attempt (see the two
// TryAcquire call sites in ExecuteTask and StartTaskNow) so this — the
// single central authority for agent concurrency (concurrency-gate
// consolidation, 2026-08-04) — never runs on a value frozen at
// newTaskExecutor's construction time.
//
// This closes the SAME boot-time-read gap documented on
// pkg/config's availableRAMBytes: when performance.max_parallel_agents is
// unset (auto-detect), the capacity newTaskExecutor originally resolved may
// have been computed from an available-memory reading taken moments after
// process start, before the host's real availability settled. Re-checking
// on every dispatch (a cheap config-field read; Resize itself is a no-op
// unless the value actually changed) means that reading self-corrects the
// moment EffectiveMaxParallelAgents() would return something different —
// including an operator's own explicit override landing via
// PUT /api/v1/performance's config write, independent of that handler's own
// explicit ResizeDispatchSema call (defense in depth: this makes the
// explicit call redundant-but-harmless rather than load-bearing).
//
// A no-op unless autoSyncDispatchCapacity is true — see that field's doc
// comment for why this must be opt-in-by-construction (newTaskExecutor
// only), not always-on: a bare TaskExecutor{...} test literal that
// deliberately hand-picks a small dispatchSema capacity (e.g. to force
// ErrDispatchCapReached) must keep full control of that capacity.
func (te *TaskExecutor) syncDispatchCapacity() {
	if !te.autoSyncDispatchCapacity || te.agentLoop == nil {
		return
	}
	cfg := te.agentLoop.GetConfig()
	if cfg == nil {
		return
	}
	// Same reasoning as newTaskExecutor: the capped flag is discarded because
	// this is a semaphore capacity, not a figure shown to anyone, and the
	// live memory gate on the admission path is what actually bounds work.
	if eff, _ := cfg.Performance.EffectiveMaxParallelAgents(); eff > 0 && eff != te.dispatchSema.Cap() {
		te.dispatchSema.Resize(eff)
	}
}

// DispatchSemaCap returns the current dispatch semaphore capacity.
func (te *TaskExecutor) DispatchSemaCap() int {
	return te.dispatchSema.Cap()
}

// TryAcquireDispatchSema attempts to claim one dispatch slot without blocking.
// Returns (true, release) when a slot is available; (false, nil) otherwise.
// Callers MUST call release() when done to avoid permanently exhausting the cap.
// Intended for testing and diagnostic tooling — production dispatch uses the
// internal sema path inside StartTaskNow / ExecuteTask.
func (te *TaskExecutor) TryAcquireDispatchSema() (bool, func()) {
	return te.dispatchSema.TryAcquire()
}

// ErrExecutorDraining is returned by ExecuteTask/StartTaskNow once Drain has
// closed intake during AgentLoop.Close() — new dispatch (including a
// goal-loop chain's own trailing redispatch) is refused so the drain can
// complete instead of chasing an ever-refilling WaitGroup. See
// TaskExecutor.draining's doc comment for the CI OOM this prevents.
var ErrExecutorDraining = errors.New("task_executor: executor is draining for shutdown — new dispatch refused")

// ErrPlanNotExecuting is returned by requirePlanExecuting (and therefore by
// ExecuteTask/StartTaskNow) when a plan MEMBER task's parent plan WAS
// resolved but is not currently in a state that permits autonomous dispatch
// — see plan.Plan.PermitsMemberDispatch for the full predicate (Draft never
// approved; Done/Failed terminal, including Stop's failed(stopped_by_user);
// or Approved/Running but PausedReason != "", FR-065). This is the ROUTINE,
// expected refusal case: requirePlanExecuting logs it at Debug, not Warn (see
// its own doc comment for why a per-call Warn here would be exactly the
// "operators learn to ignore the log" problem CheckQueuedTasks' per-plan-
// per-tick dedup exists to prevent, generalized to every OTHER dispatch
// primitive that did not have a per-tick cache to hang a dedup on).
var ErrPlanNotExecuting = errors.New("task_executor: parent plan is not in a dispatchable state (approved/running, unpaused)")

// ErrPlanStateUnresolvable is returned by requirePlanExecuting (and therefore
// by ExecuteTask/StartTaskNow) when a plan member task's parent plan could
// NOT be resolved at all — no plan.Store wired, an I/O error, or the plan
// was deleted out from under a task that still names it. Unlike
// ErrPlanNotExecuting this IS anomalous — requirePlanExecuting logs it at
// Warn — and fails CLOSED exactly the same way (never dispatch): a plan
// member whose parent's live state cannot even be verified must not run just
// because the verification itself failed.
var ErrPlanStateUnresolvable = errors.New("task_executor: parent plan's state could not be verified")

// isPlanGateRefusal reports whether err is one of requirePlanExecuting's own
// sentinels. requirePlanExecuting already logs each at its own correct level
// (Debug for the routine ErrPlanNotExecuting, Warn for the anomalous
// ErrPlanStateUnresolvable) the instant it returns them; every ExecuteTask
// caller in this file that otherwise blanket-Warns on "dispatch failed for
// ANY reason" checks this first so the identical event is not logged a
// second time at a mismatched (always-Warn) severity — the same quiet-
// routine/loud-anomalous split CheckQueuedTasks' own per-tick plan cache
// applies, generalized to callers with no such cache (advanceBlockedTasks
// fires once per real task completion, not once per member per ~60s tick, so
// there is no multiplicative blowup to dedupe here — one refused dispatch
// attempt is one (Debug-level) log line, which is already the right
// granularity).
func isPlanGateRefusal(err error) bool {
	return errors.Is(err, ErrPlanNotExecuting) || errors.Is(err, ErrPlanStateUnresolvable)
}

// requirePlanExecuting is the plan-state gate shared by ExecuteTask and
// StartTaskNow — the two primitives every dispatch path funnels through
// (S1 UAT follow-up: the original bc66345f fix placed this gate ONLY in
// CheckQueuedTasks, one level ABOVE these two primitives, leaving every
// OTHER caller — a REST PATCH-to-in_progress via StartTaskNow,
// advanceBlockedTasks' post-completion auto-advance, SpawnTriggeredRun's
// cron fire, the goal-loop redispatch in runTask/runTaskFromInProgress —
// free to dispatch a Draft/terminal/paused plan's member with no gate at
// all). Moving the check here converts what were N independent audit
// obligations (one per caller) into one shared check plus the ONE documented
// bypass (executeTaskPlanVerified, for PlanEngine.dispatchReadyMembers).
//
// Returns nil immediately for a standalone task (t.PlanID == "") — the gate
// only ever applies to a plan member task, exactly as CheckQueuedTasks' own
// (now-shared) predicate does.
//
// Delegates the actual permission question to plan.Plan.PermitsMemberDispatch
// — see that method's doc for why State alone (a bare map[plan.State]bool
// that once lived here) is not sufficient: PausedReason is a same-State
// side-flag a State-only predicate would miss entirely (the paused-plan
// follow-up to this same S1 fix).
func (te *TaskExecutor) requirePlanExecuting(t *task.Task) error {
	if t.PlanID == "" {
		return nil
	}
	p, err := te.planForGate(t.PlanID)
	if err != nil {
		logger.WarnCF("task_executor", "plan-state gate: could not resolve parent plan, failing closed",
			map[string]any{"task_id": t.ID, "plan_id": t.PlanID, "error": err.Error()})
		return fmt.Errorf("%w: plan %q: %w", ErrPlanStateUnresolvable, t.PlanID, err)
	}
	if !p.PermitsMemberDispatch() {
		logger.DebugCF("task_executor", "plan-state gate: refusing dispatch, parent plan not in a dispatchable state",
			map[string]any{
				"task_id": t.ID, "plan_id": t.PlanID,
				"plan_state": string(p.State), "paused_reason": p.PausedReason,
			})
		return fmt.Errorf("%w: plan %q is %s (paused_reason=%q)", ErrPlanNotExecuting, t.PlanID, p.State, p.PausedReason)
	}
	return nil
}

// planForGate resolves planID's current Plan for the plan-state gate
// (CheckQueuedTasks' own copy and requirePlanExecuting alike). Returns an
// error (nil plan) when no plan.Store is wired — a minimal test harness, or
// a boot sequence not yet past gateway.go's SetPlanStore wiring — or when
// the lookup itself fails (I/O error, or the plan was deleted out from under
// a task that still names it). Both cases are FAIL-CLOSED by the caller: a
// plan member task whose parent plan's live state cannot be verified must
// never auto-dispatch, matching SetPlanStore's own "will remain fail-closed"
// convention for a nil store.
func (te *TaskExecutor) planForGate(planID string) (*plan.Plan, error) {
	planStore := te.getPlanStore()
	if planStore == nil {
		return nil, errors.New("task_executor: no plan store wired, cannot verify parent plan state")
	}
	return planStore.Get(planID)
}

// ErrRunTaskApprovalRequired is returned by executeTask's automatic-dispatch
// gate (requireRunTaskAutoDispatchApproved, below) when a STANDALONE task
// (PlanID == "") is about to be cold-dispatched for an agent whose run_task
// tool policy is "ask".
//
// Closes a real fail-open defect: a human DENYING an ask-gated run_task tool
// call (pkg/tools/run_task.go's Execute, gated in pkg/agent/loop.go's runTurn
// ask-policy branch) had NO durable effect on the underlying task at all —
// Execute() is never even invoked on a denial (the loop `continue`s past it
// after recording the transcript/audit entries), so the task simply stayed
// `next`, and every UNATTENDED dispatch path (CheckQueuedTasks' ~60s
// heartbeat drain, advanceBlockedTasks, SpawnTriggeredRun's cron fire) picked
// it up and ran it anyway on its very next pass, because none of them ever
// consulted the SAME tool-policy decision that gated the explicit call.
//
// The fix generalizes the existing S1 plan-state-gate pattern
// (requirePlanExecuting, this file): the dispatch PRIMITIVE (executeTask)
// itself now consults AgentLoop.ResolveApprovalToolPolicy(agentID,
// "run_task") — the SAME authoritative resolver the gateway's own WS
// approval hook and the runTurn ask-gate already resolve through (see that
// method's own doc comment: "the SINGLE authority both the agent-loop tool
// filter... and the gateway WS approval hook... resolve through, so the two
// can never drift again") — for every cold, unattended dispatch of a
// standalone task. This closes the loophole for EVERY future denial, not
// merely a single recorded event: an agent whose run_task policy is "ask"
// can never have a fresh standalone task auto-fire without a human
// explicitly approving a run_task call.
//
// Deliberately scoped to policy=="ask" only, not "deny": (1) the reported
// defect is specifically the ask-then-deny case — a "deny" policy never
// produces a human "denial" event at all, since the tool call fails fast
// with no approval prompt in the first place; (2) "deny" is genuinely
// ambiguous at this call site — config.ValidateToolPolicyCoverage guarantees
// every real production agent has an EXPLICIT allow/ask/deny entry for
// every static builtin tool (CLAUDE.md hard constraint 6), but that
// validation is boot-time-only and many lightweight test harnesses in this
// package construct an AgentLoop with NO tool-policy configuration at all —
// tools.EffectiveToolPolicy's own documented fail-closed default for that
// gap is ALSO "deny" (logged at Error). Gating on bare policy!="allow" would
// therefore misfire on every such harness (an artifact of incomplete test
// fixtures, not an operator's real "deny" decision) and refuse dispatch for
// unrelated tests across this package. "ask" has no such fallback anywhere
// in the resolution chain — it can only ever be an explicit, deliberate
// config.ToolPolicyAsk entry — so it is the one value this gate can act on
// without a false-positive risk from missing test coverage.
var ErrRunTaskApprovalRequired = errors.New(
	"task_executor: standalone task's assigned agent requires an explicit run_task approval (ask policy); refusing unattended dispatch")

// isRoutineAutoDispatchRefusal reports whether err is one of executeTask's
// own fail-closed automatic-dispatch sentinels — the plan-state gate
// (isPlanGateRefusal) or the run_task tool-policy gate
// (ErrRunTaskApprovalRequired). Both already log themselves at their own
// correct level the instant they return, so callers that otherwise
// blanket-Warn on "dispatch failed for ANY reason" check this first to avoid
// a duplicate, mismatched-severity second log line for the identical event.
func isRoutineAutoDispatchRefusal(err error) bool {
	return isPlanGateRefusal(err) || errors.Is(err, ErrRunTaskApprovalRequired)
}

// CheckQueuedTasks picks the highest-priority *dispatchable* `next` task per
// agent and starts it. Called by the heartbeat service (pkg/heartbeat's
// TaskDrainService) on an unconditional ~1-minute ticker — this is the
// UNATTENDED auto-dispatch path. It keeps its OWN copy of the plan-state gate
// below (rather than relying solely on ExecuteTask's identical, now-shared
// requirePlanExecuting check — see that method's doc) purely as a genuine
// per-tick optimization: this loop already walks every dispatchable `next`
// task across every agent/plan once per tick, so caching each distinct
// PlanID's resolved state for the tick avoids N redundant plan.Store.Get
// reads for a plan with N ready members. This is belt-and-braces with
// requirePlanExecuting by design, not a duplicate authority — see
// executeTask's own gate call for the primitive-level twin every OTHER
// dispatch path (StartTaskNow, advanceBlockedTasks, SpawnTriggeredRun, the
// goal-loop redispatch, and PlanEngine.dispatchReadyMembers via its
// documented bypass) now goes through instead.
//
// Skips tasks whose blocked_by dependencies are not all `done`.
//
// S1 UAT fix (PRIYA-GATE-never-executed / PRIYA-D8-race), plus the PAUSED-
// state follow-up: also skips any task whose PlanID names a plan that
// plan.Plan.PermitsMemberDispatch reports as not dispatchable (not
// Approved/Running, or Approved/Running but PausedReason != "", FR-065).
// Without this, a plan member task's status is fully player-settable (a
// Kanban drag straight from Inbox to Next) independent of the plan's own
// lifecycle, so this unattended drain would dispatch a Draft plan's member
// the moment it turned `next` — the Execute confirm dialog's promise that
// "member tasks will run ... without further approval" only ever holds AFTER
// Execute was actually clicked. The same gate closes the Stop leak: a Stop
// transitions the plan to the terminal `failed` state
// (FailedReasonStoppedByUser) but only cancels members already `in_progress`
// at that instant — a member still `next` at the moment of Stop was
// previously left in the queue for this exact drain to pick up and run to
// completion after the user had already stopped the plan. It also closes the
// PAUSED leak: FR-065 pauses a plan WITHOUT moving it out of StateRunning
// (see PausedReason's doc), so a State-only predicate would keep dispatching
// a paused plan's members every tick — PermitsMemberDispatch checks both
// fields together (plan.go's own doc explains why). Standalone tasks
// (PlanID == "") are entirely unaffected: the gate only ever runs when
// PlanID is non-empty.
//
// Log-level note: the routine "not yet approved / paused" case is logged at
// Debug, ONCE PER PLAN in the cache-miss branch below — NOT once per member
// task. A 20-member draft plan must not emit 20 WARN lines every ~60s for a
// completely normal "user hasn't clicked Execute yet" state; that trains
// operators to ignore the log. An UNRESOLVABLE plan (lookup error, no store
// wired, deleted out from under the task) stays at Warn — that path is
// genuinely anomalous and is also the fail-closed branch, which must stay
// visible.
//
// Plan lookups are cached for the duration of one tick (rather than
// re-resolving the same plan once per member task) since a single tick
// already walks every dispatchable `next` task across every agent/plan; a
// plan with N ready members would otherwise cost N redundant
// plan.Store.Get reads on the same pass.
func (te *TaskExecutor) CheckQueuedTasks(ctx context.Context) {
	// D-08/FR-057: the queued-task drain (TaskDrainService) is the
	// unconditional owner of this dispatch — no operator triggers it, ever —
	// so every task it dispatches this tick runs headless. Stamped once here,
	// the sole call site of this method (pkg/heartbeat/task_drain.go).
	ctx = tools.WithAutoDenyAsk(ctx, true)
	queued, err := te.store.List(task.Filter{Status: task.StatusNext})
	if err != nil {
		logger.WarnCF("task_executor", "Check queued tasks: list failed",
			map[string]any{"error": err.Error()})
		return
	}
	if len(queued) == 0 {
		return
	}

	type agentState struct{ picked bool }
	agentDone := make(map[string]agentState)

	// planCache holds the resolved *plan.Plan (or nil on any lookup failure,
	// including "no store wired") for every distinct PlanID seen this tick.
	planCache := make(map[string]*plan.Plan)

	for i := range queued {
		t := &queued[i]
		if t.AgentID == "" {
			continue // human-only task; not dispatchable by an agent
		}
		if agentDone[t.AgentID].picked {
			continue
		}

		if t.PlanID != "" {
			p, cached := planCache[t.PlanID]
			if !cached {
				var gerr error
				p, gerr = te.planForGate(t.PlanID)
				switch {
				case gerr != nil:
					// Anomalous — no plan store wired, an I/O error, or the
					// plan was deleted out from under a task that still
					// names it. Stays at Warn: this fail-closed path is
					// worth an operator's attention, unlike the routine
					// "not yet approved / paused" case below.
					logger.WarnCF("task_executor", "Heartbeat: could not resolve parent plan, skipping member task",
						map[string]any{"task_id": t.ID, "plan_id": t.PlanID, "error": gerr.Error()})
					p = nil
				case !p.PermitsMemberDispatch():
					// Routine, expected state (Draft never approved, a
					// terminal state including Stop's
					// failed(stopped_by_user), or Approved/Running but
					// paused per FR-065) — Debug, not Warn, and logged ONCE
					// HERE per plan on this tick's cache miss rather than
					// once per member task below (see this function's own
					// doc comment for why: N members of the same
					// not-yet-approved plan must not multiply into N WARN
					// lines every ~60s).
					logger.DebugCF("task_executor", "Heartbeat: parent plan not in a dispatchable state, skipping its member tasks this tick",
						map[string]any{"plan_id": t.PlanID, "plan_state": string(p.State), "paused_reason": p.PausedReason})
				}
				planCache[t.PlanID] = p
			}
			if !p.PermitsMemberDispatch() {
				continue
			}
		}

		depsSatisfied := true
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				depsSatisfied = false
				break
			}
		}
		if !depsSatisfied {
			logger.WarnCF("task_executor", "Heartbeat: skipping blocked task, trying next",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
			continue
		}

		if err := te.ExecuteTask(ctx, t.ID, nil); err != nil && !isRoutineAutoDispatchRefusal(err) {
			// A plan-gate refusal is already logged by requirePlanExecuting at
			// its own correct level (see isPlanGateRefusal's doc) — this
			// branch's own plan-gate pre-filter above means THAT case should
			// be unreachable in practice, but the check is kept so a future
			// change to the pre-filter fails safe (no duplicate/mismatched-
			// severity log) rather than silently reintroducing the exact
			// per-tick spam this function's log-level note warns against.
			// A run_task-policy refusal (ErrRunTaskApprovalRequired) has NO
			// such pre-filter here — this loop has no per-agent tool-policy
			// cache to hang one on, unlike the per-tick plan cache above — so
			// isRoutineAutoDispatchRefusal reaching that branch is the
			// EXPECTED, routine path for an ask-policy agent's standalone
			// task, already logged at Debug by
			// requireRunTaskAutoDispatchApproved itself.
			logger.WarnCF("task_executor", "Heartbeat: could not start task",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
		agentDone[t.AgentID] = agentState{picked: true}
	}
}

// --- moved from loop.go 2026-09-15 ---

// GetTaskStore returns the shared unified task Store (may be nil in tests).
func GetTaskStore(al *AgentLoop) *task.Store {
	return al.taskStore
}

// GetTaskExecutor returns the shared TaskExecutor (may be nil in tests).
func GetTaskExecutor(al *AgentLoop) *TaskExecutor {
	return al.taskExecutor
}

// SetTaskTriggerScheduler installs the task time-trigger scheduler so every task
// create/update/delete path can (re)register or remove the task's cron trigger.
// Called once at boot by the gateway. Idempotent.
func (al *AgentLoop) SetTaskTriggerScheduler(s *TaskTriggerScheduler) {
	al.mu.Lock()
	al.taskTrigger = s
	al.mu.Unlock()
}

// taskTriggerScheduler returns the installed scheduler under the loop lock.
func (al *AgentLoop) taskTriggerScheduler() *TaskTriggerScheduler {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.taskTrigger
}

// NotifyTaskUpserted (re)registers or removes the task's time-trigger cron job
// after a create or update. Nil-safe — a no-op when no scheduler is wired (tests).
func (al *AgentLoop) NotifyTaskUpserted(t *task.Task) {
	if s := al.taskTriggerScheduler(); s != nil && t != nil {
		s.OnTaskUpserted(t)
	}
}

// NotifyTaskDeleted removes the task's time-trigger cron job after a delete.
// Nil-safe — a no-op when no scheduler is wired (tests).
func (al *AgentLoop) NotifyTaskDeleted(taskID string) {
	if s := al.taskTriggerScheduler(); s != nil && taskID != "" {
		s.OnTaskDeleted(taskID)
	}
}

// processTaskDirect runs the agent loop for a task, dispatching to the given agent.
// taskChatID identifies the WebSocket chat for event forwarding (defaults to "task:" + sessionKey).
// Channel is "webchat" for streaming; tool context is "system" so exec/cron tools are permitted.
//
// D-08 (founder decision 2026-09-24, FR-057): AutoDenyAsk is read off ctx
// rather than hardcoded — every genuinely unattended dispatcher (the
// Calendar/task-trigger fire, the queued-task drain, the auto-advance
// cascade, plan-member dispatch, the parent follow-up wake) stamps
// tools.WithAutoDenyAsk(ctx, true) onto the context BEFORE it reaches this
// function; a literal REST "Run now" click (StartOccurrenceRun/StartTaskNow)
// leaves it unstamped, so ToolAutoDenyAsk defaults to false and the run stays
// attended (cards show), matching runAgentLoop's identical
// ProcessScheduled(ctx) convention (loop.go).
func (al *AgentLoop) processTaskDirect(
	ctx context.Context,
	agentID, prompt, sessionKey, taskChatID string,
) (string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", fmt.Errorf("processTaskDirect: hooks: %w: %w", ErrTaskRunNotDispatched, err)
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", fmt.Errorf("processTaskDirect: mcp: %w: %w", ErrTaskRunNotDispatched, err)
	}

	registry := al.GetRegistry()
	ag, ok := registry.GetAgent(agentID)
	if !ok {
		logger.WarnCF(
			"agent",
			"processTaskDirect: agent not found, using default",
			map[string]any{"requested": agentID},
		)
		ag = registry.GetDefaultAgent()
	}
	if ag == nil {
		return "", fmt.Errorf("processTaskDirect: no agent %q: %w", agentID, ErrTaskRunNotDispatched)
	}

	// Tool context uses "system" channel so exec/cron tools are permitted.
	taskCtx := tools.WithAgentID(ctx, agentID)
	taskCtx = tools.WithToolContext(taskCtx, "system", "")

	// Carry the task's delegation generation forward. The caller (task executor)
	// seeds tools.WithDelegationDepth(ctx, task.DelegationDepth) before invoking
	// this; we read it back to (a) seed the root turnState depth so the per-agent
	// await/background depth gate trips inside the task run, and (b) keep it on the
	// context so a nested task_create stamps its child as generation + 1. A normal
	// chat/board run leaves it 0.
	delegationDepth := tools.ToolDelegationDepth(taskCtx)

	if taskChatID == "" {
		taskChatID = "task:" + sessionKey
	}

	// Fix C: a task assigned to a subagent_3p (external-CLI) worker must
	// dispatch through the SAME external-CLI machinery the agent-to-agent
	// delegation path uses (runner.ResolveDispatch / runExternalCLISubTurn —
	// see task_executor_run.go's dispatchesExternalCLI for the identical
	// gate; pre-ADR-091 this lived in subturn.go's now-deleted spawnSubTurn
	// native/external branch) rather than unconditionally falling into
	// runAgentLoop below.
	// Running a subagent_3p's task on the native engine would silently
	// mis-execute it with full system-level Omnipus tool access instead of the
	// configured external CLI — exactly the gap the assignment-time guards in
	// rest_tasks.go / pkg/tools/task.go / pkg/sysagent/tools/task.go existed to
	// paper over. This dispatch branch is what lets those guards be relaxed.
	dispatchKind, dispatchErr := runner.ResolveDispatch(executorConfigOf(ag))
	if dispatchErr != nil {
		return "", fmt.Errorf("processTaskDirect: %w: %w", ErrTaskRunNotDispatched, dispatchErr)
	}
	if dispatchKind == runner.DispatchKindExternalCLI {
		return al.processTaskDirectExternalCLI(taskCtx, ag, prompt, sessionKey, taskChatID, delegationDepth)
	}

	return al.runAgentLoop(taskCtx, ag, processOptions{
		SessionKey:             sessionKey,
		Channel:                "webchat",
		ChatID:                 taskChatID,
		SenderID:               "task-executor",
		UserMessage:            prompt,
		DefaultResponse:        defaultResponse,
		SendResponse:           false,
		TranscriptSessionID:    taskChatID,
		TranscriptStore:        al.taskSessionStore(taskChatID, agentID),
		OriginKind:             session.OriginKindTask,
		InitialDelegationDepth: delegationDepth,
		IsTaskRun:              true,
		RunningTaskID:          tools.ToolRunningTaskID(taskCtx),
		// D-08/FR-057: propagate the unattended marker a headless dispatcher
		// stamped on ctx (see this function's own doc comment) onto the turn's
		// own opts — this is what loop_run_turn_tools.go's AutoDenyAsk branch
		// actually reads.
		AutoDenyAsk: tools.ToolAutoDenyAsk(taskCtx),
		// WorkspaceID is already on taskCtx via tools.WithWorkspaceID (the task
		// executor sets it on ctx before calling processTaskDirect — see
		// runTask/runTaskFromInProgress's tools.WithWorkspaceID(ctx, t.WorkspaceID)
		// calls in task_executor.go); thread it through processOptions
		// explicitly too, mirroring processTaskDirectExternalCLI's identical
		// field below, so runTurn's re-root block (loop.go ~6428) resolves the
		// work dir from ts.opts.WorkspaceID via FindForAgentPreferring rather
		// than falling through to workspace.FindForAgent's arbitrary
		// sort.Strings(matches)[0] pick when the agent belongs to 2+
		// workspaces. Without this, a native task run silently rooted in the
		// WRONG workspace whenever the assigned agent had more than one
		// CoreTeam membership — this field reads ts.opts, not the context, so
		// leaving it unset here (while the external-CLI sibling below sets it)
		// was the gap.
		WorkspaceID: tools.ToolWorkspaceID(taskCtx),
	})
}

// ExecuteBoardTask dispatches a GTD board task to the agent loop in a background
// goroutine. The session must already exist in the per-agent store (via GetAgentStore).
// onComplete is called with the result string and execution error once the agent
// finishes; the caller is responsible for persisting the terminal task status.
//
// Shutdown behavior:
//   - Graceful shutdown (Stop + WaitForActiveRequests): the goroutine is tracked in
//     activeRequests, so WaitForActiveRequests/Close drain it before the process exits
//     and onComplete is called with the cancellation error, transitioning the task to
//     "failed" normally.
//   - Crash / SIGKILL / OOM: the goroutine is abandoned and onComplete never runs,
//     leaving the task persisted with status "active". On next boot,
//     gateway.reconcileStuckBoardTasks scans for any task with status=="active" and
//     resets it to "failed" with a note that the gateway restarted while it was running.
func (al *AgentLoop) ExecuteBoardTask(agentID, taskID, sessionID, prompt string, onComplete func(string, error)) {
	// Board-task goroutines run on context.Background() — they are detached from the
	// HTTP request lifecycle and outlive the Run loop.
	taskCtx := context.Background()

	al.activeRequests.Add(1)
	go func() {
		defer al.activeRequests.Done()
		defer func() {
			if r := recover(); r != nil {
				panicMsg := fmt.Sprintf("%v", r)
				logger.ErrorCF("agent", "ExecuteBoardTask: panic recovered",
					map[string]any{
						"task_id":    taskID,
						"agent_id":   agentID,
						"session_id": sessionID,
						"panic":      panicMsg,
					})
				if onComplete != nil {
					onComplete("", fmt.Errorf("panic: %v", r))
				}
			}
		}()
		sessionKey := fmt.Sprintf("agent:%s:board:%s", agentID, taskID)
		logger.InfoCF("agent", "ExecuteBoardTask: dispatching",
			map[string]any{
				"task_id":     taskID,
				"agent_id":    agentID,
				"session_id":  sessionID,
				"session_key": sessionKey,
			})
		result, err := al.processTaskDirect(taskCtx, agentID, prompt, sessionKey, sessionID)
		if err != nil {
			logger.ErrorCF("agent", "ExecuteBoardTask: execution failed",
				map[string]any{
					"task_id":    taskID,
					"agent_id":   agentID,
					"session_id": sessionID,
					"error":      err.Error(),
				})
		}
		if onComplete != nil {
			onComplete(result, err)
		}
	}()
}
