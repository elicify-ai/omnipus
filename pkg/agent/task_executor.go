package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	maxTaskDepth = 10
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

	// parentFollowUp is a test seam ONLY — production leaves it nil.
	parentFollowUp func(parentID string)

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

	// evidenceMu guards evidenceRejectStreak (ADR-052 FR-035 evidence-marker
	// gate bound, Fix-Wave-2). In-memory only, never persisted, and
	// deliberately NOT AttemptCount: rejectBareEvidenceClaim's free re-dispatch
	// must not touch AttemptCount (consumeAttemptOrExhaust is the sole writer)
	// so this streak needs its own storage. A process restart resets it, which
	// is safe — it is a soft bound against an in-process livelock, not a
	// durability contract.
	evidenceMu sync.Mutex
	// evidenceRejectStreak counts CONSECUTIVE evidence-marker-gate rejections
	// per task ID (rejectBareEvidenceClaim). Cleared (entry deleted) the
	// moment the task's evidence gate is no longer being violated — see
	// clearEvidenceGateStreak's call sites (gate pass/not-applicable in
	// finishTaskRun, and both terminal-write chokepoints,
	// completeTaskWithResult and failTask). A nil map is valid for reads
	// (hasEvidenceGateRejection); bumpEvidenceRejectStreak lazily allocates it.
	evidenceRejectStreak map[string]int

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
	// runTaskFromInProgress) end-to-end, INCLUDING the goal-loop's own
	// re-dispatch chain (consumeAttemptOrExhaust/rejectBareEvidenceClaim
	// flipping a task back to `next` and the owning goroutine's trailing
	// defer re-entering ExecuteTask/StartTaskNow for another attempt). Add(1)
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

// evidenceGateMaxConsecutiveRejections is N in ADR-052 FR-035's "after N
// consecutive bare-evidence-claim rejections, stop the free ride" bound
// (Fix-Wave-2, closing the four-reviewer-confirmed livelock). The first
// rejection for a task is always free (a single missing [goal:evidence] line
// is treated as a one-off mechanical formatting miss, per
// rejectBareEvidenceClaim's existing doc comment) — reaching the SECOND
// consecutive rejection (streak == N) is what routes the run through
// consumeAttemptOrExhaust instead of another free re-dispatch, restoring the
// hardCeiling guarantee.
const evidenceGateMaxConsecutiveRejections = 2

// newTaskExecutor creates a TaskExecutor over the unified task store.
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
		evidenceRejectStreak:     make(map[string]int),
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
// ParentAgentID/ParentDurableKey are deliberately left empty: a task
// dispatch is not a `delegate.run` call, so there is no delegating parent to
// attribute — and leaving ParentDurableKey empty also means
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
		SessionID:      sessionID,
		Generation:     0,
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
// completeTaskWithResult and finishTaskRun's already-terminal branch so the
// status->lifecycle-state mapping lives in exactly one place.
func (te *TaskExecutor) finalizeTaskLifecycle(sessionID string, status task.Status) {
	if status == task.StatusDone {
		te.transitionTaskLifecycle(sessionID, session.LifecycleCompleted, "")
		return
	}
	te.transitionTaskLifecycle(sessionID, session.LifecycleFailed, "task_failed")
}

// bumpEvidenceRejectStreak increments and returns taskID's consecutive
// evidence-marker-gate rejection count (ADR-052 FR-035, Fix-Wave-2).
func (te *TaskExecutor) bumpEvidenceRejectStreak(taskID string) int {
	te.evidenceMu.Lock()
	defer te.evidenceMu.Unlock()
	if te.evidenceRejectStreak == nil {
		te.evidenceRejectStreak = make(map[string]int)
	}
	te.evidenceRejectStreak[taskID]++
	return te.evidenceRejectStreak[taskID]
}

// hasEvidenceGateRejection reports whether taskID currently has a pending
// (unresolved) evidence-marker-gate rejection — i.e. whether buildPrompt's
// next render for this task must carry evidenceGateSteeringText forward. This
// is the delivery mechanism the "no free ride" livelock fix required
// (Fix-Wave-2): rejectBareEvidenceClaim deliberately never increments
// AttemptCount, so buildPrompt's pre-existing t.AttemptCount>0-guarded
// feedback block (which renders t.Result) never rendered it — a
// re-dispatched prompt was byte-identical to the first attempt. Tracking
// presence here, independent of AttemptCount/t.Result, is what makes the
// steering actually reach the worker.
func (te *TaskExecutor) hasEvidenceGateRejection(taskID string) bool {
	te.evidenceMu.Lock()
	defer te.evidenceMu.Unlock()
	return te.evidenceRejectStreak[taskID] > 0
}

// clearEvidenceGateStreak resets taskID's evidence-marker-gate rejection
// streak to zero. Called whenever the gate is no longer being violated for
// this task: it passed (or wasn't applicable) on the latest response
// (finishTaskRun), or the task reached ANY terminal disposition
// (completeTaskWithResult, failTask) — the latter also bounds the map's
// lifetime so a long-running gateway does not accumulate one entry per task
// ID forever. See ClearEvidenceGateStreak for the THIRD terminal-write path
// (a user Stop) that also needs this.
func (te *TaskExecutor) clearEvidenceGateStreak(taskID string) {
	te.evidenceMu.Lock()
	defer te.evidenceMu.Unlock()
	delete(te.evidenceRejectStreak, taskID)
}

// ClearEvidenceGateStreak is the exported counterpart of
// clearEvidenceGateStreak (ADR-052 fix-wave, evidence-streak leak item ii):
// PlanEngine.cancelMemberLocked (plan_engine.go) marks a Stopped task
// `failed` via a DIRECT store write, bypassing both of TaskExecutor's own
// terminal-write chokepoints (completeTaskWithResult, failTask) that
// clearEvidenceGateStreak's own doc comment names as clearing "ANY terminal
// disposition" — a Stop IS a terminal disposition too, and without this the
// streak would leak in evidenceRejectStreak for the remainder of the
// process lifetime. Exposed via the planTaskDispatcher interface so
// PlanEngine can reach it through its existing narrow test-seam field
// (dispatcher) rather than holding a second, wider reference to
// *TaskExecutor.
func (te *TaskExecutor) ClearEvidenceGateStreak(taskID string) {
	te.clearEvidenceGateStreak(taskID)
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
// continue, so it is not routed through this helper; see finishTaskRun's own
// doc comment on that intentional divergence).
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
	return taskSessionID, nil
}

// runTask executes the agent prompt and updates the task on completion.
// taskSessionID was already created and persisted SYNCHRONOUSLY by
// ExecuteTask before this goroutine was launched (M1/FR-029 — see
// ExecuteTask's and createTaskSessionSync's doc comments); this goroutine no
// longer creates the session itself.
//
// Goal-loop re-dispatch (SD-B3): when finishTaskRun decides the attempt is
// unmet with attempts remaining, it flips the task back to `next` and
// returns its ID so this goroutine's own trailing cleanup can re-enter
// ExecuteTask — reusing the existing goroutine/dispatch-sema machinery, not
// a new scheduler. The redispatch call is deliberately made from INSIDE the
// single combined deferred closure below, AFTER release()/cancel() have
// already run: calling ExecuteTask while this goroutine still held its own
// dispatch-sema slot would need two slots at once for an instant and could
// spuriously hit the concurrency cap.
func (te *TaskExecutor) runTask(
	ctx context.Context, t *task.Task, taskSessionID string, cancel context.CancelFunc, release func(),
	occurrenceMs *int64, kind task.RunKind,
) {
	var redispatchTaskID string
	defer func() {
		// Outermost defer within this closure: fires LAST, after the
		// redispatch call below (if any) has already run — see wg's doc
		// comment for why this ordering is what keeps the counter from ever
		// being observably zero mid-chain.
		defer te.wg.Done()
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			// The redispatch reuses the SAME occurrenceMs/kind as this attempt —
			// ADR-050 RD5/RD7: a goal-loop redispatch (steering-and-retry) is
			// another attempt at the SAME execution episode, not a new one, and
			// openRun's (taskID, occurrenceMs) idempotency means the redispatch's
			// own runTask call reopens (not duplicates) the still-open run this
			// attempt leaves behind — see finishTaskRun/consumeAttemptOrExhaust's
			// own doc comments for why intermediate redispatches do not close it.
			if err := te.ExecuteTask(context.Background(), redispatchTaskID, occurrenceMs); err != nil && !isRoutineAutoDispatchRefusal(err) {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	// run is populated once openRun below succeeds. Declared here (rather than
	// via := at the openRun call site) so the panic-recovery defer immediately
	// below closes over this SAME variable and observes whatever it holds at
	// the moment of a panic — nil (closeRun no-ops) if the panic happened
	// before openRun ran, the real handle otherwise.
	//
	// L5 (operator decision 2026-07-20): the ADR-050 RD10 stuck-run reaper was
	// removed — there is no backstop besides this goroutine's own top-level
	// recover (matching the pattern in session_end.go's runRecap,
	// subturn.go's spawnSubTurn, hooks.go's runObserver, and this file's own
	// notifyParentIfAllSiblingsDone). A panic here that left an open TaskRun
	// un-closed would strand it in_progress forever. Logs and returns rather
	// than re-panicking — this goroutine has no caller to propagate to
	// (launched via `go te.runTask(...)`).
	var run *activeRun
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("task_executor",
				"Panic in runTask — closing its TaskRun as failed (no reaper backstop exists)",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "panic": r})
			te.closeRun(t.ID, run, task.StatusFailed, fmt.Sprintf("panic during task execution: %v", r))
		}
	}()

	logger.InfoCF("task_executor", "runTask started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})
	// FR-118/G-13: the goroutine is now genuinely executing this attempt —
	// mirrors pkg/tools/delegate.go's transitionLifecycle(..., LifecycleRunning,
	// "") at the start of its own executeAsync/executeSync.
	te.transitionTaskLifecycle(taskSessionID, session.LifecycleRunning, "")

	// Test seam: when goroutineCtxHook is set, invoke it and return without
	// performing real agent execution — mirrors runTaskFromInProgress's
	// identical seam below (see goroutineCtxHook's doc comment). Added so a
	// test can intercept THIS (ExecuteTask) dispatch path too: this is where
	// the now-removed per-agent concurrency cap used to live (the
	// `defaultMaxConcurrentTasksPerAgent = 3` gate deleted from executeTask
	// above), and the regression test for its removal needs to hold a real
	// runTask goroutine in flight to measure peak concurrent dispatch for one
	// agent — see task_executor_no_per_agent_cap_test.go.
	if te.goroutineCtxHook != nil {
		te.goroutineCtxHook(ctx, t.ID)
		return
	}

	// ADR-050 RD5 run-open (task-run-history-spec.md §3.2): now that
	// taskSessionID is settled (created successfully, or left empty on a
	// session-store failure), open this execution's TaskRun record using the
	// session the dispatch actually minted — see openRun's own doc comment
	// for why run-open cannot happen any earlier than this point.
	run = te.openRun(t.ID, occurrenceMs, kind, taskSessionID)

	taskCtx := tools.WithAgentID(ctx, t.AgentID)
	if t.WorkspaceID != "" {
		taskCtx = tools.WithWorkspaceID(taskCtx, t.WorkspaceID)
	}
	// D13/G-12 (E.5): root a Play-resumed member's turn at its restored tree.
	// No-op (ctx unchanged) for an ordinary attempt.
	taskCtx = WithResumeWorkDirOverride(taskCtx, te.resumeWorkDirFor(t))
	// Carry the task's delegation generation into the run. processTaskDirect reads
	// it back to seed the root turnState depth (so the per-agent depth gate trips
	// inside the run) and to stamp any nested task_create as generation + 1. This
	// is what bounds an A→B→A task-mode delegation chain — without it every task
	// run starts at depth 0 and the gate never trips (see maxTaskDepth).
	taskCtx = tools.WithDelegationDepth(taskCtx, t.DelegationDepth)
	// review r2 Chunk 1: mark this turn as THIS task's own executor run so
	// TaskUpdateTool can tell an in-run done-claim (staged, adjudicated below
	// by finishTaskRun) from an out-of-band one (rejected — see
	// tools.WithRunningTaskID's doc comment).
	taskCtx = tools.WithRunningTaskID(taskCtx, t.ID)

	sessionKey := fmt.Sprintf("agent:%s:task:%s", t.AgentID, t.ID)
	prompt := te.buildPrompt(t)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	resp, err := te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	redispatchTaskID = te.finishTaskRun(ctx, t, taskSessionID, resp, err, "", run)
}

// finishTaskRun handles the shared post-execution logic for both runTask and
// runTaskFromInProgress. It appends the failure/success transcript entry,
// updates the session meta, and — when the agent did not call task_update
// itself — resolves completion from the standardized TASK_STATUS/TASK_SUMMARY
// marker (ADR-043). The marker (or an explicit update_task terminal write) is
// now a CLAIM, never the terminal decision by itself (ADR-049 US-5/US-6):
//
//   - No parseable signal (FR-045): an UNMET claim — the attempt is consumed
//     and the task re-dispatches (or wakes the owner on exhaustion) — NOT an
//     immediate terminal failure.
//   - An explicit FAILURE marker, or an already-terminal `failed` status from
//     a real update_task(status:"failed") call (SD-B1): an accepted give-up —
//     terminal immediately, exactly as before this feature. There is nothing
//     to verify in a worker's own honest failure report.
//   - An explicit update_task(status:"done") call on a task WITH acceptance
//     criteria (hard tier) is now ALSO a CLAIM, not a terminal decision
//     (SD-B2, review r1 blocker C1 fix): pkg/tools/task.go's TaskUpdateTool
//     stages it as Task.PendingJudgeClaim instead of writing a terminal
//     `done` — no DAG-advance, no onComplete, at the tool-call boundary. The
//     block below detects PendingJudgeClaim and routes it through
//     adjudicateClaim exactly like a SUCCESS marker. A criteria-less
//     (soft-tier) explicit done write is UNCHANGED — trusted immediately,
//     current.Status is already terminal `done`, and the IsTerminal check
//     just below handles it exactly as before this feature.
//   - A SUCCESS marker (no explicit terminal tool call, or no
//     PendingJudgeClaim staged): a CLAIM — routed to the evidence-ladder
//     judge (judge.go) before it may become terminal `done` (US-5/US-6, the
//     #1 self-certification failure mode this feature closes).
//
// Scratchpad tasks (FR-048/D5) are exempt from the goal loop entirely: every
// branch below trusts the marker directly, exactly as today, for a
// Scratchpad task.
//
// Returns a non-empty redispatchTaskID when the caller (runTask/
// runTaskFromInProgress) should re-enter ExecuteTask for another attempt
// (SD-B3) — see those callers' own doc comments for why the actual
// ExecuteTask call happens AFTER this function returns, not from inside it.
//
// logSuffix is appended to the "Agent execution failed" log message so
// callers can be identified in structured logs (e.g. " (StartTaskNow path)").
//
// Do NOT merge the pre-execution session-setup blocks of runTask and
// runTaskFromInProgress: runTask logs-and-continues on NewSession failure while
// runTaskFromInProgress aborts; that divergence is intentional and load-bearing.
func (te *TaskExecutor) finishTaskRun(
	ctx context.Context, t *task.Task, taskSessionID, resp string, err error, logSuffix string, run *activeRun,
) (redispatchTaskID string) {
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)

	if err != nil {
		logger.ErrorCF("task_executor", "Agent execution failed"+logSuffix,
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "error": err.Error()})
		if taskSessionID != "" && sessStore != nil {
			if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
				ID:        t.ID + "-error",
				Role:      "assistant",
				Content:   fmt.Sprintf("Task execution failed: %v", err),
				Status:    "error",
				Timestamp: time.Now().UTC(),
			}); appendErr != nil {
				taskGoalTranscriptWriteFailures.Add(1)
				logger.WarnCF("task_executor", "Transcript write failed",
					map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
			}
			status := session.StatusInterrupted
			if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &status}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": t.ID, "error": setErr.Error()})
			}
		}
		// FR-118/G-13: a genuine run-time execution error (distinct from the
		// boot sweep's own "interrupted" reason for a crash-stranded session —
		// this run actually completed, badly) terminates the durable lifecycle
		// record too, so it is never left non-terminal for a later boot sweep
		// to (correctly, but redundantly) sweep.
		te.transitionTaskLifecycle(taskSessionID, session.LifecycleFailed, "execution_error")
		te.failTask(t.ID, fmt.Sprintf("execution error: %v", err))
		failedTask := *t
		failedTask.Status = task.StatusFailed
		failedTask.Result = fmt.Sprintf("execution error: %v", err)
		te.closeRun(t.ID, run, task.StatusFailed, failedTask.Result)
		te.notifySourceChannel(&failedTask)
		return ""
	}

	if taskSessionID != "" && resp != "" && sessStore != nil {
		if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
			ID:        t.ID + "-response",
			Role:      "assistant",
			Content:   resp,
			Timestamp: time.Now().UTC(),
		}); appendErr != nil {
			taskGoalTranscriptWriteFailures.Add(1)
			logger.WarnCF("task_executor", "Transcript write failed",
				map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
		}
	}

	// Re-read so we see any explicit update_task write the agent made mid-run.
	current, lerr := te.store.Get(t.ID)
	if lerr != nil {
		logger.WarnCF("task_executor", "Could not re-read task after execution",
			map[string]any{"task_id": t.ID, "error": lerr.Error()})
		// M1: this early return used to leave any open run stranded in_progress
		// forever (no reaper backstop) — close it best-effort as failed, since
		// the task's real outcome cannot be determined without the re-read
		// that just failed.
		te.closeRun(t.ID, run, task.StatusFailed,
			fmt.Sprintf("execution finished but the task record could not be re-read to resolve its outcome: %v", lerr))
		return ""
	}

	if task.IsTerminal(current.Status) {
		// An explicit update_task(done|failed) call already decided (and, for
		// a criteria-less `done`, already fired DAG-advance at the tool-call
		// boundary) — trust it directly, exactly as today. A hard-tier done
		// claim never reaches this branch: TaskUpdateTool deliberately leaves
		// Status non-terminal for that case (see the PendingJudgeClaim check
		// below).
		if taskSessionID != "" && sessStore != nil {
			statusCompleted := session.StatusArchived
			if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusCompleted}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": t.ID, "error": setErr.Error()})
			}
		}
		// FR-118/G-13: an explicit update_task(done|failed) call already wrote
		// the task terminal — mirror that outcome onto the durable lifecycle
		// record too (see finalizeTaskLifecycle's doc comment).
		te.finalizeTaskLifecycle(taskSessionID, current.Status)
		// The agent already called update_task — its tool call is the mirror
		// write; run-close here is purely additive to it (ADR-050 RD5,
		// task-run-history-spec.md §3.3), which is exactly why update_task/
		// update_task_in_workspace need no change of their own.
		te.closeRun(t.ID, run, current.Status, current.Result)
		te.notifySourceChannel(current)
		return ""
	}

	if current.PendingJudgeClaim != "" {
		// SD-B2/review r1 C1: an explicit update_task(status:"done") call on a
		// task WITH acceptance criteria was staged here by TaskUpdateTool
		// instead of writing a terminal `done` directly. Clear the staging
		// field up front (adjudication is about to consume it one way or
		// another; leaving it set would leak into the next attempt/read) and
		// route through the SAME evidence-ladder judge path a TASK_STATUS
		// marker uses — this explicit claim takes priority over any marker
		// text that might also be present in resp, so the marker is not
		// parsed at all in this branch.
		claimText := current.PendingJudgeClaim
		cleared := ""
		if _, cerr := te.store.Update(t.ID, task.Patch{PendingJudgeClaim: &cleared}); cerr != nil {
			logger.WarnCF("task_executor", "Could not clear pending judge claim",
				map[string]any{"task_id": t.ID, "error": cerr.Error()})
		}
		return te.adjudicateClaim(ctx, current, taskSessionID, claimText, run)
	}

	// ADR-052 FR-035 (evidence-marker gate, R3-13): scan resp for the gate
	// BEFORE parseTaskCompletionSignal — checkEvidenceMarkerGate's own doc
	// comment names this exact call order. A completion claim (marker line
	// found, success OR failure — the gate does not distinguish) with no
	// genuine [goal:evidence] line immediately preceding it is REJECTED
	// pre-judge: the worker is re-prompted with the gate's steering text and
	// NO verifier is ever dispatched for this run. Scratchpad tasks are
	// exempt (FR-048 — they trust ANY found marker directly, success or
	// failure, and never reach the judge/verifier either way, so the gate
	// would have nothing to protect). The FIRST rejection for a task does NOT
	// consume an attempt via rejectBareEvidenceClaim — forgetting the evidence
	// marker once is a mechanical formatting miss, not a genuine
	// work-verification failure (contrast the "no signal at all" and
	// judge-"unmet" paths below, both of which DO consume an attempt via
	// consumeAttemptOrExhaust) — while still actively re-prompting (unlike the
	// D7 judge-Unavailable case, which pauses silently with no redispatch).
	// From the SECOND consecutive rejection onward (Fix-Wave-2,
	// evidenceGateMaxConsecutiveRejections), rejectBareEvidenceClaim itself
	// routes through consumeAttemptOrExhaust instead — an unbroken streak of
	// bare claims is no longer treated as one-off and must not be a free,
	// unbounded re-dispatch loop (four independent gate reviews confirmed
	// exactly that livelock).
	if !current.Scratchpad {
		if gate := checkEvidenceMarkerGate(resp); gate.Applicable && !gate.Honored {
			return te.rejectBareEvidenceClaim(ctx, current, taskSessionID, gate.SteeringText, run)
		}
	}
	// Gate is no longer being violated for this run (honored, not
	// applicable, or a Scratchpad task that never checks it at all) — any
	// earlier rejection streak for this task is resolved; clear it so a
	// LATER, unrelated bare claim starts counting from zero rather than
	// inheriting a stale streak.
	te.clearEvidenceGateStreak(current.ID)

	// Agent did not call task_update — no explicit signal, or an explicit
	// non-terminal write. Parse the agent's final output for the standardized
	// TASK_STATUS completion marker instead (ADR-043), uniform across native
	// and external-CLI (subagent_3p) dispatch — see the marker parser's own
	// doc comment for why an external-CLI worker ALWAYS lands here.
	signal := parseTaskCompletionSignal(resp)
	if !signal.Found() {
		logger.WarnCF("task_executor",
			"agent finished with no parseable TASK_STATUS completion signal",
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
		rawOutput := resp
		if strings.TrimSpace(rawOutput) == "" {
			rawOutput = "(agent produced no output)"
		} else {
			rawOutput = truncateTaskOutput(rawOutput)
		}
		reason := fmt.Sprintf(
			"agent finished without a completion signal (TASK_STATUS line) — review the run "+
				"transcript and re-run; raw output follows:\n\n%s",
			rawOutput,
		)
		if current.Scratchpad {
			// FR-048: Scratchpad (set_todos) tasks are exempt from the goal
			// loop entirely — today's exact fail-closed-immediately behavior.
			te.completeTaskWithResult(current, taskSessionID, task.StatusInProgress, false, reason, run)
			return ""
		}
		// FR-045: for a real task, no signal is an UNMET claim (attempt
		// consumed) — NOT an immediate terminal failure.
		return te.consumeAttemptOrExhaust(ctx, current, taskSessionID, reason, nil, run)
	}

	if current.Scratchpad || signal.Status() == task.StatusFailed {
		// FR-048 (Scratchpad: exempt from the goal loop entirely, even for a
		// success marker — trust it directly) OR SD-B1 (an explicit failure
		// marker is an accepted give-up: terminal immediately, no judge).
		te.completeTaskWithResult(current, taskSessionID, task.StatusInProgress, signal.Status() == task.StatusDone, signal.Result, run)
		return ""
	}

	// signal.Status() == task.StatusDone, non-Scratchpad: a success CLAIM —
	// route to the evidence-ladder judge (US-5/US-6).
	return te.adjudicateClaim(ctx, current, taskSessionID, signal.Result, run)
}

// adjudicateClaim routes a worker's SUCCESS claim through the evidence-ladder
// judge (US-5/US-6, judge.go). Empty Criteria falls back to the ADR-049 D5
// soft tier (SoftTierCriterion: judge against Prompt, else title+description).
// When the soft tier applies AND the Judge System Agent is not registered at
// all (never true post-boot in production, since coreagent.SeedConfig always
// seeds it — only reachable from a raw pkg/agent harness that never ran
// SeedConfig), the claim is trusted directly rather than paused forever: a
// missing Judge in that specific combination is a structural/environment
// gap, not a transient D7 "unavailable" cause.
func (te *TaskExecutor) adjudicateClaim(
	ctx context.Context, t *task.Task, taskSessionID, claimSummary string, run *activeRun,
) (redispatchTaskID string) {
	if strings.TrimSpace(claimSummary) == "" {
		// ADR-052 (7-reviewer gate item 3): an empty completion claim has
		// nothing to adjudicate — fail closed BEFORE any verifier dispatch,
		// never a full (potentially D7-backoff-stalled) verifier turn for a
		// claim carrying no content to check evidence against.
		reason := "worker reported a completion signal with an empty claim summary — " +
			"nothing to adjudicate (fail-closed, no verifier dispatched)"
		return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, reason, nil, run)
	}

	criteria := t.Criteria
	usedSoftTier := false
	if len(criteria) == 0 {
		if soft := SoftTierCriterion(t.Title, t.Description, t.Prompt); soft != nil {
			criteria = []task.AcceptanceCriterion{*soft}
			usedSoftTier = true
		}
	}

	if len(criteria) == 0 {
		// Structurally empty task (no criteria, no prompt, no title/description
		// text worth judging) — nothing to judge; trust the claim.
		te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, true, claimSummary, run)
		return ""
	}

	if usedSoftTier {
		if _, ok := te.agentLoop.GetRegistry().GetAgent(string(coreagent.IDJudge)); !ok {
			logger.WarnCF("task_executor",
				"goal-loop: Judge System Agent not configured; trusting the worker's claim "+
					"directly for this criteria-less task",
				map[string]any{"task_id": t.ID})
			te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, true, claimSummary, run)
			return ""
		}
	}

	result := te.agentLoop.JudgeCriteria(ctx, JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          t.ID,
		AssigneeAgentID: t.AgentID,
		Criteria:        criteria,
		Attempt:         t.AttemptCount + 1,
		ClaimText:       claimSummary,
		// Product-blocker fix (ADR-052 FR-011/012 x ADR-046 P1): the task's
		// own workspace — every task is required-scoped to one (task.go:246)
		// — so the Judge's verifier turn roots at the WORK-UNDER-REVIEW's
		// workspace, not its own agent home. See JudgeCriteriaInput.WorkspaceID.
		WorkspaceID: t.WorkspaceID,
	})

	if result.Unavailable {
		// D7: the judge itself is unavailable and JudgeCriteria's own
		// internal backoff loop gave up only because ctx was canceled — do
		// NOT consume the attempt or record a verdict; abandon this run.
		//
		// ADR-050: deliberately does NOT closeRun here — the task's own
		// Task.status also stays in_progress (nothing was written), so the
		// run correctly mirrors that same "genuinely unresolved, paused"
		// reality. A LATER retry of this same task re-enters runTask, whose
		// openRun call is idempotent on (taskID, occurrenceMs) and simply
		// resumes this SAME still-open run rather than creating a stray
		// second one. If the task is never retried, boot reconciliation
		// (reconcileStuckTaskRuns) is the accepted backstop (spec §3.5) —
		// there is no dedicated in-process reaper.
		logger.WarnCF("task_executor",
			"goal-loop: judge cycle abandoned (context canceled during backoff)",
			map[string]any{"task_id": t.ID, "reason": result.Reason})
		return ""
	}

	// FR-014 (member-path parity with plan_engine.go's verdictStillApplicable
	// — item 4 of the 7-reviewer gate): JudgeCriteria's verifier turn ran
	// OUTSIDE any lock, by design (the SAME reason the plan-round path
	// decouples into its own goroutine) — a concurrent Stop
	// (PlanEngine.StopTask/StopPlan) may have already moved this task out of
	// in_progress while the judge call was in flight. Re-check BEFORE
	// writing the verdict transcript or applying its outcome: a task the
	// Stop fan-out already cancelled/terminated must never have that outcome
	// silently overwritten by a stale verdict, and must never have an
	// attempt "consumed" for a run that was actually cancelled.
	if !te.taskVerdictStillApplicable(t.ID) {
		// ADR-050: same rationale as the D7 branch above — no closeRun. A
		// concurrent Stop already moved the task out of in_progress by some
		// OTHER writer, so this run's fate is that writer's responsibility;
		// boot reconciliation (reconcileStuckTaskRuns) is the accepted
		// backstop if it leaves the run open.
		logger.InfoCF("task_executor",
			"judge verdict dropped: task left in_progress during adjudication (Stop landed concurrently)",
			map[string]any{"task_id": t.ID})
		return ""
	}

	verdict := result.Verdict
	te.writeJudgeVerdictTranscript(t, taskSessionID, verdict)

	if verdict.Met {
		te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, true, claimSummary, run)
		return ""
	}

	return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, claimSummary, verdict, run)
}

// taskVerdictStillApplicable re-reads taskID's CURRENT status directly from
// the store (FR-014) and reports whether a judge verdict computed moments
// ago is still safe to apply: the task must still be in_progress. A store
// read failure fails SAFE (returns false, drops the verdict) rather than
// risking a stale-verdict overwrite on an unreadable/uncertain state.
func (te *TaskExecutor) taskVerdictStillApplicable(taskID string) bool {
	current, err := te.store.Get(taskID)
	if err != nil {
		logger.WarnCF("task_executor",
			"adjudicateClaim: could not re-read task before applying verdict (fail-safe: dropping verdict)",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return false
	}
	return current.Status == task.StatusInProgress
}

// consumeAttemptOrExhaust increments+persists Task.AttemptCount (server-set,
// FR-042/R4/C17), and either re-dispatches (attempts remain) with steering
// fed forward into buildPrompt (FR-043), or — once the attempt reaches
// EffectiveTaskMaxAttempts — marks the task terminal `failed`, writes a
// graceful wind-down handover to BOTH the task record and the owning session
// transcript (NFR-3/SD-B9), and wakes the owner via the async-notifier
// (FR-044). verdict is nil for a no-signal unmet outcome (nothing to judge)
// and non-nil for a judge-adjudicated unmet outcome.
//
// FR-047: the hard ceiling (2x the configured attempt bound) is enforced
// independently of the normal maxAttempts gate — belt-and-suspenders so a
// pending re-dispatch can never loop past it "regardless of pending
// re-dispatch or interrupt state", even though under this function's own
// invariants (it is the sole writer of AttemptCount) the two gates always
// coincide.
//
// ADR-052 FR-014/§6.4(b) TOCTOU fix (interleaving (a), 7-reviewer +
// architect gate): this is one of the "no-recheck sibling paths"
// (finishTaskRun's no-signal branch, adjudicateClaim's empty-claim branch,
// rejectBareEvidenceClaim's streak-exhaust branch, and adjudicateClaim's own
// post-recheck unmet branch all funnel through here) that previously wrote
// via a plain store.Update with NO guard at all against a concurrent Stop
// having already moved t out of in_progress — a Stop landing between the
// caller's stale read of t and this write would be silently REVIVED
// (Status -> next, and CancelReason auto-cleared by updateLocked's own
// leaving-failed clear) even though the user had just stopped it. The write
// below is now a compare-and-swap (UpdateIfStatus, expecting t to still be
// in_progress) — on a conflict the outcome is dropped (logged, never
// re-dispatched), mirroring the documented drop-stale-verdict semantics
// adjudicateClaim's taskVerdictStillApplicable fast-path already uses.
func (te *TaskExecutor) consumeAttemptOrExhaust(
	ctx context.Context,
	t *task.Task,
	taskSessionID string,
	claimSummary string,
	verdict *task.JudgeVerdict,
	run *activeRun,
) (redispatchTaskID string) {
	var planningCfg config.PlanningConfig
	if cfg := te.agentLoop.GetConfig(); cfg != nil {
		planningCfg = cfg.Planning
	}
	maxAttempts := planningCfg.EffectiveTaskMaxAttempts(t.MaxAttempts)
	hardCeiling := 2 * maxAttempts

	// FR-178: AttemptCount (per member/task scope) and the plan's JudgeRounds
	// (per goal/plan scope) are TWO DISTINCT brakes, never conflated. This
	// function is the SOLE writer of Task.AttemptCount — it never touches the
	// owning plan's JudgeRounds, symmetric to PlanEngine.applyJudgeRoundOutcome
	// Locked being the sole writer of JudgeRounds (which never touches
	// AttemptCount). Whichever trips first stops its OWN scope locally; an
	// attempts-exhaustion here fails the TASK, it does not trip the plan's
	// rounds brake. Pinned by TestAttemptsVsRounds_DistinctBrakes.
	newAttempt := t.AttemptCount + 1
	nextStatus := task.StatusNext
	updated, uerr := te.store.UpdateIfStatus(t.ID, task.StatusInProgress, task.Patch{AttemptCount: &newAttempt, Status: &nextStatus})
	if uerr != nil {
		if errors.Is(uerr, task.ErrStatusConflict) {
			// ADR-050: deliberately does NOT closeRun — a concurrent Stop
			// already moved the task out of in_progress by some OTHER writer
			// (that writer's own outcome is authoritative), so this run's
			// fate is that writer's responsibility; boot reconciliation
			// (reconcileStuckTaskRuns) is the accepted backstop if it leaves
			// the run open (spec §3.5 — no dedicated in-process reaper).
			logger.WarnCF("task_executor",
				"goal-loop: dropping unmet outcome — task left in_progress before the attempt could be "+
					"recorded (Stop landed concurrently); not re-dispatching",
				map[string]any{"task_id": t.ID})
			return ""
		}
		logger.ErrorCF("task_executor",
			"goal-loop: could not persist attempt increment; failing the run closed",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
		reason := fmt.Sprintf("goal-loop: could not persist attempt increment: %v", uerr)
		te.failTask(t.ID, reason)
		te.closeRun(t.ID, run, task.StatusFailed, reason)
		return ""
	}

	if newAttempt < maxAttempts && newAttempt <= hardCeiling {
		te.writeSteeringPrompt(updated, taskSessionID, claimSummary, verdict)
		// M6 fix: this attempt's own session is about to be superseded by a
		// fresh one createTaskSessionSync mints when the caller's deferred
		// closure re-enters ExecuteTask(updated.ID) — nothing else ever
		// closes THIS session out otherwise. See supersedeTaskSession's doc
		// comment for why this is a direct SetMeta, not just the lifecycle
		// mediator.
		te.supersedeTaskSession(updated.AgentID, taskSessionID)
		// ADR-050: deliberately does NOT closeRun — this is a goal-loop
		// redispatch of the SAME execution episode (steering fed forward,
		// another attempt at the same claim), not a new one. runTask's own
		// redispatch defer re-enters ExecuteTask with THIS SAME occurrenceMs,
		// and openRun's (taskID, occurrenceMs) idempotency means that call
		// resumes this same still-open run rather than opening a second one —
		// see runTask's redispatch-defer doc comment.
		return updated.ID
	}

	// updated.Status is `next` here (just written above by the CAS write this
	// function performed) — the terminal write below CASes against THAT
	// status, not in_progress, chaining the same guarantee: nothing besides
	// this same goroutine could have touched the task between the two writes
	// (a real Stop requires in_progress, per StopTask's own guard, so it
	// cannot land on a `next` task at all — see completeTaskWithResult's
	// `expected` parameter doc).
	handover := buildHandover(updated, claimSummary, verdict, maxAttempts)
	if te.completeTaskWithResult(updated, taskSessionID, task.StatusNext, false, handover, run) {
		te.wakeOwnerAttemptsExhausted(updated, taskSessionID, handover)
	}
	return ""
}

// writeSteeringPrompt persists the judge's (or the no-signal reminder's)
// steering text so the NEXT attempt's buildPrompt call carries it forward
// (FR-043, evaluator-optimizer pattern). t.Result is repurposed as the
// in-flight steering carrier between attempts — it is NOT yet the FINAL
// result while the goal loop is still running; the terminal write
// (completeTaskWithResult) always overwrites it with the real final result.
func (te *TaskExecutor) writeSteeringPrompt(
	t *task.Task, taskSessionID, claimSummary string, verdict *task.JudgeVerdict,
) {
	steering := buildSteeringText(claimSummary, verdict)
	if _, uerr := te.store.Update(t.ID, task.Patch{Result: &steering}); uerr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not persist steering for re-dispatch",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
	}
	if taskSessionID == "" {
		return
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		return
	}
	if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-steering-%d", t.ID, t.AttemptCount+1),
		Role:      "system",
		Content:   steering,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("task_executor", "goal-loop: steering transcript write failed",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
	}
}

// rejectBareEvidenceClaim (ADR-052 FR-035, R3-13) handles a completion claim
// the evidence-marker gate rejected. The FIRST consecutive rejection for a
// task re-dispatches WITHOUT incrementing AttemptCount — a missing/empty
// [goal:evidence] line immediately before the completion marker is a
// mechanical formatting miss, not a genuine work-verification failure, so it
// must not cost the worker a real attempt (this is the "does not consume"
// side of the same D7 distinction JudgeCriteriaResult.Unavailable relies on,
// kept deliberately separate from — and never routed through —
// consumeAttemptOrExhaust, which is the sole writer of AttemptCount). Unlike
// D7's silent pause (no redispatch at all while the judge itself is down),
// this DOES actively redispatch: the fix is mechanical and the worker is
// expected to self-correct on the very next turn.
//
// From the SECOND consecutive rejection onward (streak reaches
// evidenceGateMaxConsecutiveRejections, tracked in-memory via
// bumpEvidenceRejectStreak — Fix-Wave-2), the free ride ends: an unbroken
// run of bare claims is no longer a one-off slip, and re-dispatching it
// forever with no AttemptCount movement is an unbounded, silent, full-LLM-
// spend loop (the exact livelock four independent gate reviews confirmed).
// This branch instead routes through consumeAttemptOrExhaust — the SAME
// attempt/hardCeiling budget every other unmet outcome uses — so the task
// eventually reaches a terminal state even if the worker never emits the
// marker at all (whether it is trying to succeed or trying to fail out
// cleanly; the gate does not distinguish, and neither does this bound).
//
// Every rejection (bounded or not) is logged at Warn with the consecutive
// count, per Fix-Wave-2's "make it loud" requirement.
func (te *TaskExecutor) rejectBareEvidenceClaim(
	ctx context.Context, t *task.Task, taskSessionID, steeringText string, run *activeRun,
) (redispatchTaskID string) {
	streak := te.bumpEvidenceRejectStreak(t.ID)
	logger.WarnCF("task_executor",
		"evidence-marker gate: rejected bare completion claim (no [goal:evidence] line immediately before the completion marker)",
		map[string]any{"task_id": t.ID, "consecutive_rejections": streak})

	if streak >= evidenceGateMaxConsecutiveRejections {
		te.clearEvidenceGateStreak(t.ID)
		reason := fmt.Sprintf(
			"worker repeated a completion claim with no [goal:evidence] line %d consecutive times — "+
				"treating as an unmet attempt instead of re-dispatching for free",
			streak,
		)
		return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, reason, nil, run)
	}

	// ADR-052 FR-014/§6.4(b) TOCTOU fix: this is the FREE re-dispatch write
	// (does not consume an attempt) — but it is still an "outcome" write in
	// the same sense as consumeAttemptOrExhaust's: an unguarded plain Update
	// here would just as readily revive a task a concurrent Stop already
	// moved to failed+stopped_by_user (Status -> next, CancelReason
	// auto-cleared) as the attempt-consuming path would. CAS it the same way.
	nextStatus := task.StatusNext
	updated, uerr := te.store.UpdateIfStatus(t.ID, task.StatusInProgress, task.Patch{Status: &nextStatus})
	if uerr != nil {
		if errors.Is(uerr, task.ErrStatusConflict) {
			// ADR-050: deliberately does NOT closeRun — same rationale as
			// consumeAttemptOrExhaust's identical CAS-conflict branch (a
			// concurrent Stop is authoritative; boot reconciliation is the
			// accepted backstop).
			logger.WarnCF("task_executor",
				"evidence-marker gate: dropping free re-dispatch — task left in_progress concurrently "+
					"(Stop landed); not re-dispatching",
				map[string]any{"task_id": t.ID})
			return ""
		}
		logger.ErrorCF("task_executor",
			"evidence-marker gate: could not persist re-dispatch status; failing the run closed",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
		reason := fmt.Sprintf("evidence-marker gate: could not persist re-dispatch status: %v", uerr)
		te.failTask(t.ID, reason)
		te.closeRun(t.ID, run, task.StatusFailed, reason)
		return ""
	}

	if _, uerr := te.store.Update(updated.ID, task.Patch{Result: &steeringText}); uerr != nil {
		logger.WarnCF("task_executor",
			"evidence-marker gate: could not persist steering for re-dispatch",
			map[string]any{"task_id": updated.ID, "error": uerr.Error()})
	}
	if taskSessionID != "" {
		if sessStore := te.agentLoop.GetAgentStore(updated.AgentID); sessStore != nil {
			if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
				ID:        fmt.Sprintf("%s-evidence-gate-%d", updated.ID, time.Now().UnixNano()),
				Role:      "system",
				Content:   steeringText,
				Timestamp: time.Now().UTC(),
			}); appendErr != nil {
				taskGoalTranscriptWriteFailures.Add(1)
				logger.WarnCF("task_executor",
					"evidence-marker gate: steering transcript write failed",
					map[string]any{"task_id": updated.ID, "session_id": taskSessionID, "error": appendErr.Error()})
			}
		}
	}
	// M6 fix: this is the OTHER redispatch path (the free re-dispatch that
	// does not consume an attempt) — it mints a fresh session for the next
	// attempt via the same createTaskSessionSync route and leaves THIS
	// session's own status/lifecycle untouched otherwise. See
	// supersedeTaskSession's doc comment for the full rationale.
	te.supersedeTaskSession(updated.AgentID, taskSessionID)
	// ADR-050: deliberately does NOT closeRun — same rationale as
	// consumeAttemptOrExhaust's redispatch branch (this is another attempt at
	// the SAME execution episode; the redispatch reopens this same run via
	// openRun's idempotency, it does not need it pre-closed).
	return updated.ID
}

// supersedeTaskSession closes out a retry-attempt's own session when the
// goal loop moves on to a fresh attempt (M6, UAT 2026-07-31): both
// consumeAttemptOrExhaust's attempt-consuming redispatch and
// rejectBareEvidenceClaim's free redispatch mint a BRAND NEW session for the
// next attempt (createTaskSessionSync's sessStore.NewSession, reached again
// when the caller's deferred closure re-enters ExecuteTask) but never closed
// out the PREVIOUS attempt's session — only the FINAL attempt's session was
// ever touched, by completeTaskWithResult's direct SetMeta(StatusArchived) +
// finalizeTaskLifecycle. Every intermediate, superseded attempt kept
// session.StatusActive permanently, misleading anything that counts or
// reconciles active work (the sessions list, usage accounting, orphan
// sweeps).
//
// This is a DIRECT SetMeta call — the same shape completeTaskWithResult uses
// for its own terminal write — rather than relying solely on
// transitionTaskLifecycle's mediator (session.TransitionSession):
// transitionTaskLifecycle no-ops ENTIRELY (including its UnifiedMeta mirror)
// when te.getLifecycleStore() returns nil, which a TaskExecutor can validly
// run without (test harnesses, and any caller that has not wired the S2
// durable lifecycle store) — a fix that only worked when that store happens
// to be configured would silently fail to close out the session in exactly
// the configurations most likely to go unnoticed. The direct SetMeta below
// is called unconditionally (whenever sessStore is available at all); the
// transitionTaskLifecycle call alongside it is best-effort, mirroring the
// durable S2 record too when that store IS wired, exactly like
// completeTaskWithResult's own belt-and-suspenders dual write.
//
// session.StatusInterrupted (via LifecycleCancelled — mirrors to
// StatusInterrupted per lifecycle_bridge.go's canonical mapping) rather than
// StatusArchived: this attempt didn't error out and the TASK itself has not
// been judged failed (nextStatus is `next`, not `failed`) — the session's
// own life simply ended in favor of a new attempt, the same "terminated, not
// cleanly completed" shape a genuine execution error or a user Stop already
// use StatusInterrupted for, rather than the "intentionally closed" shape
// StatusArchived captures for a task that actually reached a terminal
// outcome.
func (te *TaskExecutor) supersedeTaskSession(agentID, taskSessionID string) {
	if taskSessionID == "" {
		return
	}
	if sessStore := te.agentLoop.GetAgentStore(agentID); sessStore != nil {
		statusInterrupted := session.StatusInterrupted
		if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusInterrupted}); setErr != nil {
			logger.WarnCF("task_executor",
				"goal-loop: could not mark superseded attempt's session interrupted",
				map[string]any{"session_id": taskSessionID, "error": setErr.Error()})
		}
	}
	te.transitionTaskLifecycle(taskSessionID, session.LifecycleCancelled, "")
}

// buildSteeringText renders the feedback fed forward into the next attempt.
func buildSteeringText(claimSummary string, verdict *task.JudgeVerdict) string {
	if verdict == nil {
		// No-signal case: the composed "no completion signal" reason IS the
		// steering — there is no per-criterion breakdown to report.
		return claimSummary
	}
	var sb strings.Builder
	sb.WriteString("The judge reviewed your last attempt and found it UNMET:\n")
	for _, c := range verdict.PerCriterion {
		if !c.Met {
			fmt.Fprintf(&sb, "- criterion %s: %s\n", c.CriterionID, c.Reason)
		}
	}
	return sb.String()
}

// buildHandover renders the graceful wind-down summary written to the task
// Result and the owning session transcript when the goal loop's attempts are
// exhausted (FR-044/NFR-3/SD-B9).
func buildHandover(t *task.Task, claimSummary string, verdict *task.JudgeVerdict, maxAttempts int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb,
		"Goal loop exhausted after %d attempt(s) (max %d) without a judge-confirmed success.\n\n",
		t.AttemptCount, maxAttempts,
	)
	if verdict != nil {
		sb.WriteString("Last judge verdict:\n")
		for _, c := range verdict.PerCriterion {
			status := "met"
			if !c.Met {
				status = "UNMET"
			}
			fmt.Fprintf(&sb, "- criterion %s: %s (%s)\n", c.CriterionID, status, c.Reason)
		}
	} else {
		sb.WriteString("Last attempt outcome:\n")
		sb.WriteString(claimSummary)
		sb.WriteString("\n")
	}
	sb.WriteString(
		"\nProgress/remaining/blockers: review the run transcript for details; the task has " +
			"been marked failed and its owner notified.",
	)
	return sb.String()
}

// wakeOwnerAttemptsExhausted wakes the task's owning agent via the
// async-notifier (FR-044/async_notifier.go) once the goal loop's attempts
// are exhausted. Falls back to a "system"/"task:<id>" destination when the
// task has no SourceChannel/SourceChatID (e.g. a board/REST-created task) —
// AsyncNotifier.Notify rejects an empty destination outright (FR-N7).
func (te *TaskExecutor) wakeOwnerAttemptsExhausted(t *task.Task, taskSessionID, handover string) {
	channel, chatID := t.SourceChannel, t.SourceChatID
	if channel == "" || chatID == "" {
		channel, chatID = "system", "task:"+t.ID
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content := fmt.Sprintf(
		"Task %q (%s) exhausted its goal-loop attempts and needs your attention.\n\n%s",
		t.Title, t.ID, handover,
	)
	if notifyErr := te.agentLoop.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
		Channel:             channel,
		ChatID:              chatID,
		AgentID:             t.AgentID,
		TranscriptSessionID: taskSessionID,
		SourceKind:          "task_goal_loop",
		Content:             content,
	}); notifyErr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not wake owner on attempts-exhausted",
			map[string]any{"task_id": t.ID, "error": notifyErr.Error()})
	}
}

// writeJudgeVerdictTranscript writes verdict as a dedicated judge_verdict
// transcript entry (FR-056, EntryTypeJudgeVerdict) alongside the worker's own
// ADR-043 completion marker so the two can never silently disagree (ADR §6).
func (te *TaskExecutor) writeJudgeVerdictTranscript(t *task.Task, taskSessionID string, verdict *task.JudgeVerdict) {
	if taskSessionID == "" || verdict == nil {
		return
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		return
	}
	payload, merr := json.Marshal(verdict)
	if merr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not marshal judge verdict for transcript",
			map[string]any{"task_id": t.ID, "error": merr.Error()})
		return
	}
	if appendErr := sessStore.AppendTranscriptStrict(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-judge-%d", t.ID, verdict.Round),
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   verdict.JudgeAgentID,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("task_executor", "goal-loop: judge verdict transcript write failed",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": appendErr.Error()})
	}
}

// completeTaskWithResult marks task t terminal — Done when success is true,
// Failed otherwise — with the given result text, archives its session (if
// any), and runs the shared post-completion hooks (status-changed event,
// parent follow-up, and — for a Done status only, per onTaskComplete's own
// gate — blocked-dependent advance) plus source-channel notification. Shared
// by finishTaskRun's three non-error completion paths (explicit success
// marker, explicit failure marker, fail-closed no-signal). NOT used by the
// hard "agent execution error" branch above, which keeps failTask's existing,
// deliberately narrower shape (no parent follow-up) — that asymmetry predates
// this change and is out of scope here.
//
// The success parameter is deliberately a plain bool, not a task.Status
// (review C1): completeTaskWithResult only ever writes one of the two
// terminal statuses, so narrowing the signature to "success or not" makes
// writing a non-terminal status here a compile error instead of a
// reviewable-but-possible mistake.
//
// expected is the on-disk status the caller believes t is CURRENTLY at
// (ADR-052 FR-014/§6.4(b) TOCTOU fix, 7-reviewer + architect gate): every
// call site reached this function after some earlier, possibly-unlocked
// work (a judge/verifier turn, an attempt-increment write) during which a
// concurrent Stop could have moved the task out from under it. The write
// below is a compare-and-swap (UpdateIfStatus) against expected rather than
// a plain Update — on a conflict (the task is no longer at expected, most
// commonly because StopTask/StopPlan already moved it to
// failed+stopped_by_user) the completion is DROPPED: logged, no session
// archive, no onTaskComplete/notifySourceChannel side effects, and — via
// the returned bool — no owner-wake either at call sites that gate one on
// it. This is the same "drop the stale outcome, never resurrect or
// silently overwrite a Stop" contract taskVerdictStillApplicable's
// pre-existing fast-path already documents; the CAS makes it authoritative
// (belt-and-suspenders) rather than relying solely on that earlier,
// separately-timed re-check. Returns whether the write actually landed.
func (te *TaskExecutor) completeTaskWithResult(
	t *task.Task, taskSessionID string, expected task.Status, success bool, result string, run *activeRun,
) (applied bool) {
	// Fix-Wave-2: this is a terminal write for t.ID — any evidence-marker-gate
	// rejection streak still tracked for it is now moot (and, left uncleared,
	// would leak for the process lifetime; see evidenceRejectStreak's doc
	// comment). Cleared unconditionally, even on a CAS conflict below: either
	// way the task IS terminal by now (just via a different writer), so the
	// streak is moot regardless.
	te.clearEvidenceGateStreak(t.ID)
	status := task.StatusDone
	if !success {
		status = task.StatusFailed
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	now := time.Now().UTC().Format(time.RFC3339)
	final, uerr := te.store.UpdateIfStatus(t.ID, expected, task.Patch{
		Status:      &status,
		Result:      &result,
		CompletedAt: &now,
	})
	if uerr != nil {
		if errors.Is(uerr, task.ErrStatusConflict) {
			logger.WarnCF("task_executor",
				"goal-loop: dropping completion outcome — task left its expected status concurrently "+
					"(Stop landed); the task's own outcome is authoritative",
				map[string]any{"task_id": t.ID, "expected_status": string(expected), "target_status": string(status)})
			// M5: close the run here too, with the outcome this function was
			// asked to write — the Task mirror write was dropped (a concurrent
			// Stop is authoritative), but run-history has nowhere else to
			// record this execution's own completion, and there is no reaper
			// backstop to fall back on if it is left in_progress.
			te.closeRun(t.ID, run, status, result)
			return false
		}
		// Known, accepted limitation (ADR-043 §3): the task is left stuck at
		// whatever non-terminal status it had before this call (typically
		// in_progress) — we do not retry and we do not force a second write
		// with a synthesized failure status. A persistent store failure here
		// (disk full, permissions, corrupt file) would very likely fail a
		// retry identically, and forcing a follow-up write risks compounding
		// a partially-written/corrupted task file rather than recovering it.
		// An operator must notice this ERROR log and manually resolve the
		// stuck task (e.g. via a direct store fix or `omnipus` CLI update).
		logger.ErrorCF("task_executor", "Completion update failed",
			map[string]any{"task_id": t.ID, "status": string(status), "error": uerr.Error()})
		// M5: close the run here too — unlike the Task mirror, run-history has
		// nowhere else to record this completion, and there is no reaper
		// backstop to fall back on if we leave it in_progress.
		te.closeRun(t.ID, run, status, result)
		return false
	}
	if taskSessionID != "" && sessStore != nil {
		statusArchived := session.StatusArchived
		if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusArchived}); setErr != nil {
			logger.WarnCF("task_executor", "Meta update failed",
				map[string]any{"task_id": t.ID, "error": setErr.Error()})
		}
	}
	// FR-118/G-13: this is completeTaskWithResult's own terminal write —
	// mirror it onto the durable lifecycle record (see finalizeTaskLifecycle's
	// doc comment). Placed AFTER the CAS write above lands (never on the
	// dropped-conflict early returns), exactly like the UnifiedMeta archive
	// this line sits next to.
	te.finalizeTaskLifecycle(taskSessionID, status)
	te.closeRun(t.ID, run, status, result)
	te.recordEvidenceBoundary(final)
	te.onTaskComplete(final)
	te.notifySourceChannel(final)
	return true
}

// recordEvidenceBoundary takes the write-set-scoped boundary commit for a task
// that has just reached a terminal state (D13/G-12, E.4). This is the PRODUCER
// half of Play-from-commit: without it LastMemberCommit resolves "" forever and
// Play silently degrades to a fresh attempt.
//
// Deliberately best-effort and non-fatal — the task has ALREADY been written
// terminal by the caller, so a broken evidence repo must not retroactively fail
// it. Every outcome is logged so an operator can tell "no evidence recorded"
// from "evidence recorded", which is exactly the signal whose absence made the
// unwired state invisible.
func (te *TaskExecutor) recordEvidenceBoundary(t *task.Task) {
	evidence := te.getEvidenceCommitter()
	if evidence == nil || t == nil {
		return
	}
	res, recorded, err := evidence.CommitTaskBoundary(t)
	switch {
	case err != nil:
		logger.WarnCF("task_executor", "evidence boundary commit failed — Play will fall back to a fresh attempt",
			map[string]any{"task_id": t.ID, "workspace_id": t.WorkspaceID, "error": err.Error()})
	case !recorded:
		// Nothing to record (no workspace, no write set, unmaterialized work
		// dir). Normal for every non-plan-member task — stay quiet at debug.
		logger.DebugCF("task_executor", "evidence boundary: nothing to record",
			map[string]any{"task_id": t.ID})
	case res.Skipped:
		logger.InfoCF("task_executor", "evidence boundary skipped",
			map[string]any{"task_id": t.ID, "reason": res.SkipReason})
	default:
		logger.InfoCF("task_executor", "evidence boundary commit recorded",
			map[string]any{
				"task_id": t.ID, "commit": res.Hash, "files": len(res.Committed),
				"contention": len(res.Contention), "excluded_for_secret": len(res.ExcludedForSecret),
			})
	}
}

// resumeWorkDirFor returns the materialized Play-from-commit resume tree for t,
// or "" when this run is an ordinary (non-resumed) attempt (D13/G-12, E.5).
//
// PlanEngine.Play persists t.ResumeFromCommit and materializes the checkout at
// the deterministic workspaces/<ws>/resume/<taskID> path; this reads that same
// path back. The directory is re-derived rather than threaded through a new
// task field precisely BECAUSE it is deterministic — Play and this call site
// cannot disagree about where the tree is.
//
// Returns "" for every degrade (no resume baseline, no workspace, unsafe id, or
// a tree that is not actually on disk) so the turn falls through to the
// workspace's shared work/ dir exactly as it did before Play-from-commit
// existed.
func (te *TaskExecutor) resumeWorkDirFor(t *task.Task) string {
	if t == nil || t.ResumeFromCommit == "" || t.WorkspaceID == "" {
		return ""
	}
	dir, err := memberResumeDir(omnipusHome(), t.WorkspaceID, t.ID)
	if err != nil {
		logger.WarnCF("task_executor", "resume tree: unsafe path — running in the shared work dir",
			map[string]any{"task_id": t.ID, "error": err.Error()})
		return ""
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		// Play recorded a baseline but the tree is gone (manual cleanup, or a
		// materialization that failed and degraded). Fall back rather than
		// refusing the run.
		logger.WarnCF("task_executor", "resume tree missing — running in the shared work dir",
			map[string]any{"task_id": t.ID, "dir": dir, "commit": t.ResumeFromCommit})
		return ""
	}
	logger.InfoCF("task_executor", "resume tree: turn rooted at the restored checkout",
		map[string]any{"task_id": t.ID, "dir": dir, "commit": t.ResumeFromCommit})
	return dir
}

// SetEvidenceCommitter installs the boundary-commit producer (D13/G-12). Wired
// at the gateway boot seam next to PlanEngine.SetCommitResolver; leaving it
// unset disables evidence recording without affecting task execution.
//
// Guarded by mu (fix-wave finding #3): the gateway starts dispatch (via
// newTaskExecutor) before this late-binding boot-seam call lands, so a
// concurrent recordEvidenceBoundary read on another goroutine must never race
// this write — see the evidence field's own doc comment.
func (te *TaskExecutor) SetEvidenceCommitter(c evidenceCommitter) {
	te.mu.Lock()
	te.evidence = c
	te.mu.Unlock()
}

// getEvidenceCommitter returns the installed evidence committer (nil if
// unset), guarded by mu so a concurrent SetEvidenceCommitter never races a
// goroutine reading it mid-dispatch. Mirrors getLifecycleStore exactly.
func (te *TaskExecutor) getEvidenceCommitter() evidenceCommitter {
	te.mu.Lock()
	defer te.mu.Unlock()
	return te.evidence
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

// notifySourceChannel sends a compact task result back to the originating
// channel. Only sends for terminal statuses.
func (te *TaskExecutor) notifySourceChannel(t *task.Task) {
	if t.SourceChannel == "" || t.SourceChatID == "" {
		return
	}
	if te.agentLoop.bus == nil {
		logger.WarnCF("task_executor", "Cannot notify source channel — message bus is nil",
			map[string]any{"task_id": t.ID, "channel": t.SourceChannel})
		return
	}
	if !task.IsTerminal(t.Status) {
		return
	}

	msg := fmt.Sprintf("**%s** — %s", t.Title, t.Status)
	if t.Result != "" {
		result := t.Result
		if len(result) > 500 {
			result = result[:497] + "..."
		}
		msg += "\n\n" + result
	}

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := te.agentLoop.bus.PublishOutbound(notifyCtx, bus.OutboundMessage{
		Channel: t.SourceChannel,
		ChatID:  t.SourceChatID,
		Content: msg,
	}); err != nil {
		logger.WarnCF("task_executor", "Could not notify source channel",
			map[string]any{"task_id": t.ID, "channel": t.SourceChannel, "error": err.Error()})
	}
}

// buildPrompt constructs the prompt sent to the agent for a task.
//
// ADR-043 (task completion contract): every dispatch kind is instructed to
// end its final message with a standardized TASK_STATUS/TASK_SUMMARY marker
// — this is the fail-closed fallback finishTaskRun parses when there is no
// explicit task_update call. Native agents ADDITIONALLY get the task_update
// instruction: for a task WITH acceptance criteria, an explicit call and the
// marker now converge on the SAME evidence-ladder judge path (review r1 C1
// fix, SD-B2) — neither "wins outright" over the other — so this instruction
// deliberately does NOT claim the tool call bypasses adjudication.
//
// Echo-safety (review B1/B2): with the marker grammar now tolerant of
// trailing content (parseTaskCompletionSignal / taskStatusLineRe), a model
// that echoes this instruction block VERBATIM as its own "final message"
// (without ever emitting a real signal) must not resolve to verdictSuccess —
// that would be a false success from the instruction text itself, not from
// anything the agent actually reported. The two TASK_STATUS lines below are
// listed as separate, clean lines with the FAILURE variant deliberately
// LAST: parseTaskCompletionSignal's "last occurrence wins" rule means a raw
// echo of just this block resolves to verdictFailure (the safe direction),
// never verdictSuccess. TestBuildPrompt_InstructionEchoNeverResolvesToSuccess
// (task_completion_contract_test.go) pins this invariant for both dispatch
// kinds — do not reorder the two status lines without re-verifying it holds.
//
// FIX 3 (7-reviewer gate, prompt/capability mismatch, predates ADR-043): the
// task_update instruction stays dispatch-aware. A subagent_3p (external-CLI)
// worker's tool registry is its own CLI's, never Omnipus's — it has no
// task_update tool wired at all (see processTaskDirectExternalCLI's doc
// comment, loop.go) — so telling it to call the tool describes a capability
// it structurally cannot use; it gets the marker instruction only.
func (te *TaskExecutor) buildPrompt(t *task.Task) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Task: %s\n\n", t.Title)
	if t.Prompt != "" {
		sb.WriteString(t.Prompt)
		sb.WriteString("\n\n")
	}
	// FR-043: on a re-dispatch (attempt >= 2), carry the previous attempt's
	// steering forward — the judge's per-criterion unmet reasons, or the
	// no-signal reminder (evaluator-optimizer pattern, ADR D2). t.Result is
	// repurposed as the in-flight steering carrier between attempts (see
	// writeSteeringPrompt) — it is NOT yet the FINAL result while the goal
	// loop is still running; completeTaskWithResult always overwrites it with
	// the real final result at the end of the loop. Attempt 1 (AttemptCount
	// == 0) never has steering, so first-attempt prompts are unaffected.
	if t.AttemptCount > 0 && t.Result != "" {
		fmt.Fprintf(&sb, "## Feedback from attempt %d — address this before re-claiming success:\n", t.AttemptCount)
		sb.WriteString(t.Result)
		sb.WriteString("\n\n")
	}
	// ADR-052 FR-035 fix (Fix-Wave-2): a pending evidence-marker-gate
	// rejection (rejectBareEvidenceClaim) is delivered here, NOT via the
	// t.AttemptCount>0 block above — rejectBareEvidenceClaim deliberately
	// never increments AttemptCount (a missing [goal:evidence] line is a
	// mechanical formatting miss, not a genuine work-verification failure, so
	// it must not cost a real attempt), so that block's guard is never true
	// for this path. hasEvidenceGateRejection/evidenceRejectStreak is
	// in-memory TaskExecutor state kept independent of AttemptCount/t.Result
	// for exactly this reason — see its doc comment.
	if te.hasEvidenceGateRejection(t.ID) {
		sb.WriteString("## Your last attempt was rejected by the evidence-marker gate — address this before re-claiming completion:\n")
		sb.WriteString(evidenceGateSteeringText)
		sb.WriteString("\n\n")
	}
	fmt.Fprintf(&sb, "Priority: %d (1=highest, 5=lowest)\n", t.EffectivePriority())
	fmt.Fprintf(&sb, "Task ID: %s\n\n", t.ID)

	// ADR-052 FR-035: teach the evidence marker itself, immediately before the
	// completion marker it must precede — checkEvidenceMarkerGate
	// (task_completion_signal.go) requires a non-empty [goal:evidence] line
	// as the nearest non-blank line above TASK_STATUS. This block sits BEFORE
	// the external-CLI early return below so both dispatch kinds are taught
	// it; a subagent_3p worker has no task_update tool escape hatch at all
	// (see dispatchesExternalCLI's doc comment), so the marker instruction is
	// its ONLY path to ever satisfy the gate.
	sb.WriteString("When you are done, verify your work, then end your final message with the " +
		"evidence line immediately followed by ONE of the two status lines below (never both), " +
		"plus an optional one-line summary:\n")
	sb.WriteString("  " + goalEvidenceLabel + " <one line stating what you verified>\n")
	sb.WriteString("  " + taskStatusLabel + ": success\n")
	sb.WriteString("  " + taskStatusLabel + ": failure\n")
	sb.WriteString("  " + taskSummaryLabel + ": <one-paragraph summary of the outcome>\n")

	if te.dispatchesExternalCLI(t.AgentID) {
		return sb.String()
	}

	// NOTE: the tool's real registered name is "update_task" (pkg/tools/task.go
	// TaskUpdateTool.Name()) — this instruction was previously misnamed
	// "task_update" (a name no registered tool answers to); fixed alongside the
	// B1 rewrite since this block was already being touched.
	sb.WriteString("You may also call `update_task` explicitly when you finish " +
		"(a task with acceptance criteria is adjudicated by the evidence-ladder judge either way — " +
		"calling the tool does not skip that review):\n")
	fmt.Fprintf(&sb, "  task_id: %q\n", t.ID)
	sb.WriteString("  status: \"done\" (or \"failed\" if unsuccessful)\n")
	sb.WriteString("  result: a brief summary of what was accomplished\n")
	return sb.String()
}

// dispatchesExternalCLI reports whether agentID's configured executor
// resolves to external-CLI dispatch (subagent_3p) rather than the native
// Omnipus engine — the same runner.ResolveDispatch gate processTaskDirect
// (pkg/agent/loop.go) uses to decide HOW a task actually runs. Fails closed
// to "native" (false) on any resolution failure (unknown agent, nil
// registry, unresolvable executor kind) so a config problem never silently
// strips the task_update instruction from a run that will actually need it.
func (te *TaskExecutor) dispatchesExternalCLI(agentID string) bool {
	registry := te.agentLoop.GetRegistry()
	if registry == nil {
		logger.DebugCF("task_executor", "dispatchesExternalCLI: nil agent registry — failing closed to native dispatch",
			map[string]any{"agent_id": agentID})
		return false
	}
	ag, ok := registry.GetAgent(agentID)
	if !ok || ag == nil {
		logger.DebugCF("task_executor", "dispatchesExternalCLI: agent not found — failing closed to native dispatch",
			map[string]any{"agent_id": agentID})
		return false
	}
	kind, err := runner.ResolveDispatch(executorConfigOf(ag))
	if err != nil {
		logger.DebugCF(
			"task_executor",
			"dispatchesExternalCLI: ResolveDispatch failed — failing closed to native dispatch",
			map[string]any{"agent_id": agentID, "error": err.Error()},
		)
		return false
	}
	return kind == runner.DispatchKindExternalCLI
}

// onTaskComplete handles post-completion logic: parent notification + the
// blocked_by auto-advance (dispatch tasks whose deps are now all done).
func (te *TaskExecutor) onTaskComplete(t *task.Task) {
	te.emitStatusChanged(t, t.Status)

	if t.ParentTaskID != "" {
		te.notifyParentIfAllSiblingsDone(t.ParentTaskID)
	}

	// Only a `done` task unblocks downstream tasks (a `failed` dep does not).
	if t.Status != task.StatusDone {
		return
	}
	// Move dependents blocked→next, then attempt to dispatch the ready ones.
	if _, err := te.store.AdvanceBlockedDependents(t.ID); err != nil {
		logger.WarnCF("task_executor", "Could not advance blocked dependents",
			map[string]any{"completed_task_id": t.ID, "error": err.Error()})
	}
	te.advanceBlockedTasks(context.Background(), t.ID)
}

// notifyParentIfAllSiblingsDone resumes the parent agent once every child task
// of parentID has reached a terminal state. Safe under concurrent sibling
// completions via the atomic FollowedUp claim.
func (te *TaskExecutor) notifyParentIfAllSiblingsDone(parentID string) {
	siblings, err := te.store.List(task.Filter{ParentTaskID: parentID, ParentTaskIDSet: true})
	if err != nil {
		logger.WarnCF("task_executor", "Could not list siblings",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	for _, s := range siblings {
		if !task.IsTerminal(s.Status) {
			return
		}
	}

	parent, err := te.store.Get(parentID)
	if err != nil {
		logger.WarnCF("task_executor", "Could not load parent task",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	if parent.Status != task.StatusInProgress {
		return
	}

	claimed, claimErr := te.store.ClaimParentFollowUp(parent.ID)
	if claimErr != nil {
		logger.WarnCF("task_executor", "Could not claim parent follow-up",
			map[string]any{"parent_id": parent.ID, "error": claimErr.Error()})
		return
	}
	if !claimed {
		return
	}

	if te.parentFollowUp != nil {
		te.parentFollowUp(parent.ID)
		return
	}

	summary := te.buildChildSummary(siblings)
	sessionKey := fmt.Sprintf("agent:%s:task:%s", parent.AgentID, parent.ID)
	followUp := fmt.Sprintf("All child tasks of task %q have completed.\n\n%s", parent.ID, summary)
	parentChatID := "task:" + parent.ID
	// Fix-wave finding #1: this goroutine calls processTaskDirect — a real
	// agent turn that reads/writes session and transcript stores — exactly
	// like runTask/runTaskFromInProgress, but until now it was launched with
	// NO wg tracking at all, so Drain's wg.Wait could never see it and it
	// could still be writing through stores Close() had already torn down.
	// Add(1) before `go` (mirroring the other two dispatch sites); Done() is
	// the FIRST defer registered inside the goroutine so it fires LAST (after
	// the panic-recovery defer below, which is registered second and thus
	// runs first) — Drain only ever sees this goroutine as "done" once it has
	// genuinely finished, panic or not.
	//
	// Routed through enterDispatch, NOT a bare Add: unlike runTask and
	// runTaskFromInProgress, this launch site does NOT run while its caller
	// already holds a wg entry. It is reached from the task_update tool via
	// AgentLoop's SetOnComplete hook — an ordinary agent turn that holds no
	// count of its own. A bare Add here can therefore take the counter 0 -> 1
	// while Drain has a waiter parked, which is the sync.WaitGroup panic this
	// gate exists to prevent (see dispatchGate's doc comment). Refusing while
	// draining is also the correct BEHAVIOUR, not merely the safe one: Close()
	// is already tearing down the session and transcript stores this follow-up
	// turn would write through.
	if !te.enterDispatch() {
		return
	}
	go func() {
		defer te.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("task_executor", "Panic in parent follow-up",
					map[string]any{"parent_id": parent.ID, "panic": r})
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, ferr := te.agentLoop.processTaskDirect(ctx, parent.AgentID, followUp, sessionKey, parentChatID)
		if ferr != nil {
			logger.WarnCF("task_executor", "Parent follow-up failed",
				map[string]any{"parent_id": parent.ID, "error": ferr.Error()})
		}
	}()
}

// readyBlockedCandidates returns the IDs of all `next` tasks that list
// completedTaskID as a blocker AND whose ENTIRE blocked_by set is now `done`.
func (te *TaskExecutor) readyBlockedCandidates(completedTaskID string) []string {
	// Scan BOTH `blocked` and `next` — either is a legitimate resting state for
	// a dependent, and this used to scan only `next`.
	//
	//   blocked → a dependency was unmet, so the S2 UAT fix (pkg/task/store.go
	//             derives `blocked` at Create and at the end of every
	//             updateLocked) persisted it as blocked. This is the common
	//             case here and scanning only `next` MISSED IT ENTIRELY,
	//             silently killing this advance path for exactly the tasks it
	//             exists to advance. CI caught it via
	//             TestOrchestratorAdvance_StillBlockedWhenDepNotComplete.
	//   next    → every dependency was already `done` when the task was
	//             created, so the recompute never blocked it. Still a valid
	//             candidate, and dropping it would break
	//             TestOrchestratorAdvance_UnblockedTaskFoundAfterDep.
	//
	// The allSatisfied loop below re-verifies the FULL dependency set either
	// way, so this filter is only a pre-narrowing — it must not be the thing
	// that decides readiness.
	var candidates []task.Task
	for _, st := range []task.Status{task.StatusBlocked, task.StatusNext} {
		batch, listErr := te.store.List(task.Filter{
			Status:      st,
			BlockedByID: completedTaskID,
		})
		if listErr != nil {
			logger.WarnCF("task_executor", "Orchestrator: could not scan blocked tasks",
				map[string]any{"completed_task_id": completedTaskID, "status": string(st), "error": listErr.Error()})
			return nil
		}
		candidates = append(candidates, batch...)
	}
	var ready []string
	for i := range candidates {
		t := &candidates[i]
		allSatisfied := true
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				allSatisfied = false
				break
			}
		}
		if allSatisfied {
			ready = append(ready, t.ID)
		}
	}
	return ready
}

// advanceBlockedTasks dispatches every `next` task whose full dependency set is
// now satisfied by the completion of completedTaskID.
//
// ExecuteTask's own requirePlanExecuting gate (see its doc) is what prevents
// this from re-opening the Stop leak the S1 fix was written to close: an
// in-flight plan member that finishes (landing here via onTaskComplete) after
// its plan was already Stopped/failed(stopped_by_user) must not have this
// function dispatch its now-unblocked dependents just because they satisfy
// their BlockedBy set — ExecuteTask itself now refuses that dispatch.
func (te *TaskExecutor) advanceBlockedTasks(ctx context.Context, completedTaskID string) {
	for _, taskID := range te.readyBlockedCandidates(completedTaskID) {
		if err := te.ExecuteTask(ctx, taskID, nil); err != nil {
			if !isRoutineAutoDispatchRefusal(err) {
				logger.WarnCF("task_executor", "Orchestrator: advance dispatch failed",
					map[string]any{"task_id": taskID, "error": err.Error()})
			}
		} else {
			logger.InfoCF("task_executor", "Orchestrator: advanced blocked task",
				map[string]any{"task_id": taskID, "unblocked_by": completedTaskID})
		}
	}
}

// buildChildSummary produces a markdown summary of all child task results.
func (te *TaskExecutor) buildChildSummary(children []task.Task) string {
	var sb strings.Builder
	sb.WriteString("## Child Task Results\n\n")
	for _, c := range children {
		fmt.Fprintf(&sb, "- **%s** (status: %s)", c.Title, c.Status)
		if c.Result != "" {
			fmt.Fprintf(&sb, ": %s", c.Result)
		}
		sb.WriteString("\n")
	}
	return sb.String()
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
// point it is closed in finishTaskRun / completeTaskWithResult.
//
// nil means "no run is being tracked for this execution": openRun's own call
// to Store.OpenRun failed (openRun degrades to nil rather than aborting the
// dispatch) — the ONE remaining nil case now that runTaskFromInProgress/
// StartTaskNow also opens a run (BLK-3, operator decision 2026-07-20; every
// production dispatch path participates in run-history today). Test-only
// direct calls into finishTaskRun/completeTaskWithResult (see
// task_completion_contract_test.go) also legitimately pass nil.
// Task.status/result/session_id keep their exact existing behavior
// regardless of whether a run is being tracked (RD2) — activeRun only ever
// ADDS a parallel record, never gates or alters the mirror.
type activeRun struct {
	runID        string
	occurrenceMs *int64
}

// openRun best-effort opens (or, per Store.OpenRun's own idempotency,
// transparently reuses) the TaskRun record for (taskID, occurrenceMs) —
// ADR-050 RD5/RD7. Called from runTask AFTER the task's session has been
// created so the run's session_id is the session the dispatch actually
// minted (task-run-history-spec.md §3.2: "the run's session_id is the one
// the dispatch already mints"). Run-open cannot happen any earlier than that
// point without either duplicating session creation or leaving the run's
// session_id permanently empty: Store.OpenRun's signature requires a
// session_id at open time (a close record cannot amend it later — see
// TaskRun's own doc comment on why folding is last-record-wins with no
// field-level merge), and pkg/session.UnifiedStore.NewSession always
// self-mints its own ID — there is no way to pre-select one from outside
// pkg/session.
//
// A run-store failure here is logged and degrades to nil (no run tracked
// for this execution) rather than failing the task dispatch — TaskRun is a
// purely additive record layer (RD2); a run-history I/O problem must never
// prevent or fail a real agent execution.
func (te *TaskExecutor) openRun(taskID string, occurrenceMs *int64, kind task.RunKind, sessionID string) *activeRun {
	run, _, err := te.store.OpenRun(taskID, occurrenceMs, kind, sessionID)
	if err != nil {
		// M3-log: escalated from Warn to Error — a failed open means no run
		// will ever be tracked for this execution, and there is no reaper to
		// notice or retry it later.
		logger.ErrorCF("task_executor", "Could not open task run record",
			map[string]any{"task_id": taskID, "kind": string(kind), "error": err.Error()})
		return nil
	}
	te.emitRunStatus(taskID, run.RunID, run.OccurrenceMs, task.StatusInProgress)
	return &activeRun{runID: run.RunID, occurrenceMs: run.OccurrenceMs}
}

// closeRun best-effort closes run's TaskRun record with the given terminal
// status/result (ADR-050 RD5) — a no-op when run is nil (openRun degraded,
// or this execution never opened one). Called from finishTaskRun and
// completeTaskWithResult always AFTER the existing Task.status/result mirror
// write those functions already perform, so run-history strictly observes
// the SAME completion signal, never a second source of truth for it.
//
// closeRun can legitimately be invoked TWICE for the same run (delta-review
// Fix 2, 2026-07-20): runTask's own top-level panic-recovery defer
// (~line 218) closes over the SAME *activeRun completeTaskWithResult already
// closed, and re-invokes closeRun if a panic occurs in POST-completion
// housekeeping (onTaskComplete / notifyParentIfAllSiblingsDone) that runs
// AFTER completeTaskWithResult's own successful closeRun call. That second
// call hits task.ErrRunAlreadyClosed — the record is correctly terminal, not
// stranded — so it is logged at Info, not Error: an ERROR log here reading
// "permanently strands" would fire a false on-call alert for a run that
// closed successfully the first time.
func (te *TaskExecutor) closeRun(taskID string, run *activeRun, status task.Status, result string) {
	if run == nil || run.runID == "" {
		return
	}
	if err := te.store.CloseRun(taskID, run.runID, status, result); err != nil {
		if errors.Is(err, task.ErrRunAlreadyClosed) {
			logger.InfoCF(
				"task_executor",
				"Task run already closed by an earlier step; ignoring duplicate close from panic-recovery/housekeeping",
				map[string]any{"task_id": taskID, "run_id": run.runID, "attempted_status": string(status)},
			)
			return
		}
		// M3-log: escalated from Warn to Error — a failed close permanently
		// strands this run in_progress; there is no reaper to close it later.
		logger.ErrorCF("task_executor", "Could not close task run record",
			map[string]any{"task_id": taskID, "run_id": run.runID, "error": err.Error()})
		return
	}
	te.emitRunStatus(taskID, run.runID, run.occurrenceMs, status)
}

// emitRunStatus publishes a TaskRun open/close transition (ADR-050
// Consequences "Realtime", task-run-history-spec.md §3.8) onto
// the agent event bus via AgentLoop.EmitTaskRunStatus — the same
// emitEvent-based mechanism emitStatusChanged above uses for Task.status
// transitions. Best-effort; a nil agentLoop (test seams that construct a
// bare TaskExecutor) is silently skipped.
func (te *TaskExecutor) emitRunStatus(taskID, runID string, occurrenceMs *int64, status task.Status) {
	if te.agentLoop == nil {
		return
	}
	te.agentLoop.EmitTaskRunStatus(TaskRunStatusPayload{
		TaskID:       taskID,
		RunID:        runID,
		OccurrenceMs: occurrenceMs,
		Status:       string(status),
	})
}

// failTask marks a task as failed with the given reason.
func (te *TaskExecutor) failTask(taskID, reason string) {
	// Fix-Wave-2: terminal write — see completeTaskWithResult's identical
	// clear for why.
	te.clearEvidenceGateStreak(taskID)
	now := time.Now().UTC().Format(time.RFC3339)
	failed := task.StatusFailed
	updated, err := te.store.Update(taskID, task.Patch{
		Status:      &failed,
		Result:      &reason,
		CompletedAt: &now,
	})
	if err != nil {
		logger.ErrorCF("task_executor", "Could not mark task failed",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return
	}
	te.emitStatusChanged(updated, task.StatusFailed)
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
	// Replace the reserved slot (inserted above, cancel==nil, reserved==true)
	// with a live slot (cancel set, reserved==false) under the same mutex so
	// any concurrent reader always observes a consistent, named state.
	taskCtx, cancel := context.WithCancel(context.Background())
	te.mu.Lock()
	te.running[taskID] = &taskSlot{cancel: cancel, reserved: false}
	te.mu.Unlock()
	slotReleased = true // goroutine now owns the slot; don't let releaseSlot clear it

	te.wg.Add(1)
	go te.runTaskFromInProgress(taskCtx, t, taskSessionID, cancel, release)
	return taskSessionID, nil
}

// runTaskFromInProgress is the goroutine body for tasks launched via
// StartTaskNow. The session has already been created and the session_id
// persisted; it skips the session-creation block that runTask performs and
// goes straight to execution, reusing the shared completion logic.
//
// BLK-3 (operator decision 2026-07-20): it DOES open an ADR-050 RD5/RD7
// TaskRun record (task-run-history-spec.md §3.2), threading the resulting
// *activeRun into finishTaskRun so completion closes it. This was previously
// out of scope — StartTaskNow's raw-PATCH entry point is distinct from the
// ClaimForRun/SpawnReset-guarded paths §3.2 originally scoped run-open to —
// but the gateway's "Start Task" and "Create & Run now" UI actions BOTH
// PATCH→in_progress→StartTaskNow→runTaskFromInProgress, making this the most
// common launch path in practice; leaving it unrecorded meant the majority
// of real runs recorded no history at all. kind is always RunKindManual
// (every launch through here is user-initiated) with occurrenceMs always nil
// (no recurring-fire context reaches this entry point). StartOccurrenceRun
// below is the OTHER run-aware manual entry point — the calendar's
// per-occurrence Run-now.
func (te *TaskExecutor) runTaskFromInProgress(
	ctx context.Context,
	t *task.Task,
	taskSessionID string,
	cancel context.CancelFunc,
	release func(),
) {
	var redispatchTaskID string
	defer func() {
		// Outermost defer within this closure: fires LAST, after the
		// redispatch call below (if any) has already run — see
		// TaskExecutor.wg's doc comment for why this ordering is what keeps
		// the counter from ever being observably zero mid-chain.
		defer te.wg.Done()
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			// occurrenceMs is always nil here — see this function's own doc
			// comment (no recurring-fire context reaches StartTaskNow).
			if err := te.ExecuteTask(context.Background(), redispatchTaskID, nil); err != nil && !isRoutineAutoDispatchRefusal(err) {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	// run is populated once openRun below succeeds. Declared here (rather than
	// via := at the openRun call site) so the panic-recovery defer immediately
	// below closes over this SAME variable — see runTask's identical
	// declaration for the full rationale (no reaper backstop).
	var run *activeRun
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("task_executor",
				"Panic in runTaskFromInProgress — closing its TaskRun as failed (no reaper backstop exists)",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "panic": r})
			te.closeRun(t.ID, run, task.StatusFailed, fmt.Sprintf("panic during task execution: %v", r))
		}
	}()

	// FR-118/G-13: the goroutine is now genuinely executing this attempt —
	// mirrors runTask's identical call (see its doc comment) and, deliberately,
	// sits BEFORE the goroutineCtxHook test seam below: a test using the hook
	// to intercept before real execution is still simulating a goroutine that
	// truly started, so the durable record should show running, not queued,
	// at the moment of interception.
	te.transitionTaskLifecycle(taskSessionID, session.LifecycleRunning, "")

	// Test seam: when goroutineCtxHook is set, invoke it and return without
	// performing real agent execution. The hook receives the goroutine's context so
	// tests can assert it is not canceled by the originating request context.
	// Deliberately BEFORE the run-open below: this seam never reaches
	// finishTaskRun, so opening a run here would create one that this
	// (never-executing) test double can never close.
	if te.goroutineCtxHook != nil {
		te.goroutineCtxHook(ctx, t.ID)
		return
	}

	logger.InfoCF("task_executor", "runTaskFromInProgress started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})

	// ADR-050 RD5/RD7 run-open (task-run-history-spec.md §3.2): taskSessionID
	// was already created and persisted synchronously by StartTaskNow before
	// this goroutine was launched (unlike runTask, which must wait for its
	// own session-creation block to settle), so it is available immediately.
	run = te.openRun(t.ID, nil, task.RunKindManual, taskSessionID)

	taskCtx := tools.WithAgentID(ctx, t.AgentID)
	if t.WorkspaceID != "" {
		taskCtx = tools.WithWorkspaceID(taskCtx, t.WorkspaceID)
	}
	// D13/G-12 (E.5): root a Play-resumed member's turn at its restored tree.
	// No-op (ctx unchanged) for an ordinary attempt. Both dispatch entry points
	// set this — runTaskFromInProgress is the one Play itself re-enters through.
	taskCtx = WithResumeWorkDirOverride(taskCtx, te.resumeWorkDirFor(t))
	taskCtx = tools.WithDelegationDepth(taskCtx, t.DelegationDepth)
	// review r2 Chunk 1: same in-run marker as runTask above — see
	// tools.WithRunningTaskID's doc comment.
	taskCtx = tools.WithRunningTaskID(taskCtx, t.ID)

	sessionKey := fmt.Sprintf("agent:%s:task:%s", t.AgentID, t.ID)
	prompt := te.buildPrompt(t)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	resp, err := te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	redispatchTaskID = te.finishTaskRun(ctx, t, taskSessionID, resp, err, " (StartTaskNow path)", run)
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
