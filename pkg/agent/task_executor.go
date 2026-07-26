package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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
	defaultMaxConcurrentTasksPerAgent = 3
	maxTaskDepth                      = 10
)

// ErrDispatchCapReached is returned by StartTaskNow when the global dispatch
// semaphore is exhausted. Callers (e.g. the REST handler) use errors.Is to
// distinguish this retryable condition from hard failures.
var ErrDispatchCapReached = errors.New("task_executor: global dispatch cap reached")

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
	agentLoop     *AgentLoop
	store         *task.Store
	mu            sync.Mutex
	running       map[string]*taskSlot
	maxConcurrent int
	// dispatchSema gates the total number of concurrently dispatched tasks
	// across all agents.
	dispatchSema *DispatchSemaphore

	// parentFollowUp is a test seam ONLY — production leaves it nil.
	parentFollowUp func(parentID string)

	// evidence records the write-set-scoped boundary commit that Play later
	// resumes a member from (D13/G-12). Wired at the gateway boot seam
	// alongside PlanEngine.SetCommitResolver — the two are the producer and
	// consumer of the same contract. Nil in test harnesses and on a degraded
	// boot, in which case no evidence is recorded and Play takes its
	// documented fresh-attempt path.
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
	capacity := defaultMaxConcurrentTasksPerAgent
	if al.cfg != nil {
		if eff := al.cfg.Performance.EffectiveMaxParallelAgents(); eff > 0 {
			capacity = eff
		}
	}
	return &TaskExecutor{
		agentLoop:            al,
		store:                store,
		running:              make(map[string]*taskSlot),
		maxConcurrent:        defaultMaxConcurrentTasksPerAgent,
		dispatchSema:         newDispatchSemaphore(capacity),
		evidenceRejectStreak: make(map[string]int),
	}
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
// controls.
func (te *TaskExecutor) ExecuteTask(ctx context.Context, taskID string) error {
	t, err := te.store.Get(taskID)
	if err != nil {
		return fmt.Errorf("task_executor: get task %q: %w", taskID, err)
	}
	if t.Status != task.StatusNext {
		return fmt.Errorf("task_executor: task %q is %s, not next", taskID, t.Status)
	}

	// Guard: do not dispatch a task that still has unsatisfied dependencies.
	if len(t.BlockedBy) > 0 {
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				return fmt.Errorf("task_executor: task %q is blocked by %q (not done)", taskID, depID)
			}
		}
	}

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

	// Per-agent cap: count running (in_progress) tasks for this agent.
	runningTasks, err := te.store.List(task.Filter{Status: task.StatusInProgress, AgentID: t.AgentID})
	if err != nil {
		release()
		return fmt.Errorf("task_executor: list running tasks for agent %q: %w", t.AgentID, err)
	}
	if len(runningTasks) >= te.maxConcurrent {
		release()
		return fmt.Errorf(
			"task_executor: concurrency limit reached for agent %q (%d running)",
			t.AgentID, len(runningTasks),
		)
	}

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

	go te.runTask(taskCtx, t, taskSessionID, cancel, release)
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
	if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
		ID:        t.ID + "-prompt",
		Role:      "user",
		Content:   te.buildPrompt(t),
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		logger.ErrorCF("task_executor", "Transcript write failed",
			map[string]any{"task_id": t.ID, "error": appendErr.Error()})
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
func (te *TaskExecutor) runTask(ctx context.Context, t *task.Task, taskSessionID string, cancel context.CancelFunc, release func()) {
	var redispatchTaskID string
	defer func() {
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			if err := te.ExecuteTask(context.Background(), redispatchTaskID); err != nil {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	logger.InfoCF("task_executor", "runTask started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})

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
	redispatchTaskID = te.finishTaskRun(ctx, t, taskSessionID, resp, err, "")
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
	ctx context.Context, t *task.Task, taskSessionID, resp string, err error, logSuffix string,
) (redispatchTaskID string) {
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)

	if err != nil {
		logger.ErrorCF("task_executor", "Agent execution failed"+logSuffix,
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "error": err.Error()})
		if taskSessionID != "" && sessStore != nil {
			if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
				ID:        t.ID + "-error",
				Role:      "assistant",
				Content:   fmt.Sprintf("Task execution failed: %v", err),
				Status:    "error",
				Timestamp: time.Now().UTC(),
			}); appendErr != nil {
				logger.WarnCF("task_executor", "Transcript write failed",
					map[string]any{"task_id": t.ID, "error": appendErr.Error()})
			}
			status := session.StatusInterrupted
			if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &status}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": t.ID, "error": setErr.Error()})
			}
		}
		te.failTask(t.ID, fmt.Sprintf("execution error: %v", err))
		failedTask := *t
		failedTask.Status = task.StatusFailed
		failedTask.Result = fmt.Sprintf("execution error: %v", err)
		te.notifySourceChannel(&failedTask)
		return ""
	}

	if taskSessionID != "" && resp != "" && sessStore != nil {
		if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
			ID:        t.ID + "-response",
			Role:      "assistant",
			Content:   resp,
			Timestamp: time.Now().UTC(),
		}); appendErr != nil {
			logger.WarnCF("task_executor", "Transcript write failed",
				map[string]any{"task_id": t.ID, "error": appendErr.Error()})
		}
	}

	// Re-read so we see any explicit update_task write the agent made mid-run.
	current, lerr := te.store.Get(t.ID)
	if lerr != nil {
		logger.WarnCF("task_executor", "Could not re-read task after execution",
			map[string]any{"task_id": t.ID, "error": lerr.Error()})
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
		return te.adjudicateClaim(ctx, current, taskSessionID, claimText)
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
			return te.rejectBareEvidenceClaim(ctx, current, taskSessionID, gate.SteeringText)
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
			te.completeTaskWithResult(current, taskSessionID, task.StatusInProgress, false, reason)
			return ""
		}
		// FR-045: for a real task, no signal is an UNMET claim (attempt
		// consumed) — NOT an immediate terminal failure.
		return te.consumeAttemptOrExhaust(ctx, current, taskSessionID, reason, nil)
	}

	if current.Scratchpad || signal.Status() == task.StatusFailed {
		// FR-048 (Scratchpad: exempt from the goal loop entirely, even for a
		// success marker — trust it directly) OR SD-B1 (an explicit failure
		// marker is an accepted give-up: terminal immediately, no judge).
		te.completeTaskWithResult(current, taskSessionID, task.StatusInProgress, signal.Status() == task.StatusDone, signal.Result)
		return ""
	}

	// signal.Status() == task.StatusDone, non-Scratchpad: a success CLAIM —
	// route to the evidence-ladder judge (US-5/US-6).
	return te.adjudicateClaim(ctx, current, taskSessionID, signal.Result)
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
	ctx context.Context, t *task.Task, taskSessionID, claimSummary string,
) (redispatchTaskID string) {
	if strings.TrimSpace(claimSummary) == "" {
		// ADR-052 (7-reviewer gate item 3): an empty completion claim has
		// nothing to adjudicate — fail closed BEFORE any verifier dispatch,
		// never a full (potentially D7-backoff-stalled) verifier turn for a
		// claim carrying no content to check evidence against.
		reason := "worker reported a completion signal with an empty claim summary — " +
			"nothing to adjudicate (fail-closed, no verifier dispatched)"
		return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, reason, nil)
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
		te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, true, claimSummary)
		return ""
	}

	if usedSoftTier {
		if _, ok := te.agentLoop.GetRegistry().GetAgent(string(coreagent.IDJudge)); !ok {
			logger.WarnCF("task_executor",
				"goal-loop: Judge System Agent not configured; trusting the worker's claim "+
					"directly for this criteria-less task",
				map[string]any{"task_id": t.ID})
			te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, true, claimSummary)
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
		logger.InfoCF("task_executor",
			"judge verdict dropped: task left in_progress during adjudication (Stop landed concurrently)",
			map[string]any{"task_id": t.ID})
		return ""
	}

	verdict := result.Verdict
	te.writeJudgeVerdictTranscript(t, taskSessionID, verdict)

	if verdict.Met {
		te.completeTaskWithResult(t, taskSessionID, task.StatusInProgress, true, claimSummary)
		return ""
	}

	return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, claimSummary, verdict)
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
			logger.WarnCF("task_executor",
				"goal-loop: dropping unmet outcome — task left in_progress before the attempt could be "+
					"recorded (Stop landed concurrently); not re-dispatching",
				map[string]any{"task_id": t.ID})
			return ""
		}
		logger.ErrorCF("task_executor",
			"goal-loop: could not persist attempt increment; failing the run closed",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
		te.failTask(t.ID, fmt.Sprintf("goal-loop: could not persist attempt increment: %v", uerr))
		return ""
	}

	if newAttempt < maxAttempts && newAttempt <= hardCeiling {
		te.writeSteeringPrompt(updated, taskSessionID, claimSummary, verdict)
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
	if te.completeTaskWithResult(updated, taskSessionID, task.StatusNext, false, handover) {
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
	if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-steering-%d", t.ID, t.AttemptCount+1),
		Role:      "system",
		Content:   steering,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		logger.WarnCF("task_executor", "goal-loop: steering transcript write failed",
			map[string]any{"task_id": t.ID, "error": appendErr.Error()})
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
	ctx context.Context, t *task.Task, taskSessionID, steeringText string,
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
		return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, reason, nil)
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
			logger.WarnCF("task_executor",
				"evidence-marker gate: dropping free re-dispatch — task left in_progress concurrently "+
					"(Stop landed); not re-dispatching",
				map[string]any{"task_id": t.ID})
			return ""
		}
		logger.ErrorCF("task_executor",
			"evidence-marker gate: could not persist re-dispatch status; failing the run closed",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
		te.failTask(t.ID, fmt.Sprintf("evidence-marker gate: could not persist re-dispatch status: %v", uerr))
		return ""
	}

	if _, uerr := te.store.Update(updated.ID, task.Patch{Result: &steeringText}); uerr != nil {
		logger.WarnCF("task_executor",
			"evidence-marker gate: could not persist steering for re-dispatch",
			map[string]any{"task_id": updated.ID, "error": uerr.Error()})
	}
	if taskSessionID != "" {
		if sessStore := te.agentLoop.GetAgentStore(updated.AgentID); sessStore != nil {
			if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
				ID:        fmt.Sprintf("%s-evidence-gate-%d", updated.ID, time.Now().UnixNano()),
				Role:      "system",
				Content:   steeringText,
				Timestamp: time.Now().UTC(),
			}); appendErr != nil {
				logger.WarnCF("task_executor",
					"evidence-marker gate: steering transcript write failed",
					map[string]any{"task_id": updated.ID, "error": appendErr.Error()})
			}
		}
	}
	return updated.ID
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
	if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-judge-%d", t.ID, verdict.Round),
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   verdict.JudgeAgentID,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		logger.WarnCF("task_executor", "goal-loop: judge verdict transcript write failed",
			map[string]any{"task_id": t.ID, "error": appendErr.Error()})
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
	t *task.Task, taskSessionID string, expected task.Status, success bool, result string,
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
		return false
	}
	if taskSessionID != "" && sessStore != nil {
		statusArchived := session.StatusArchived
		if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusArchived}); setErr != nil {
			logger.WarnCF("task_executor", "Meta update failed",
				map[string]any{"task_id": t.ID, "error": setErr.Error()})
		}
	}
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
	if te.evidence == nil || t == nil {
		return
	}
	res, err := te.evidence.CommitTaskBoundary(t)
	switch {
	case err != nil:
		logger.WarnCF("task_executor", "evidence boundary commit failed — Play will fall back to a fresh attempt",
			map[string]any{"task_id": t.ID, "workspace_id": t.WorkspaceID, "error": err.Error()})
	case res == nil:
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
func (te *TaskExecutor) SetEvidenceCommitter(c evidenceCommitter) {
	te.evidence = c
}

// SetPlanStore installs the shared *plan.Store so CheckQueuedTasks' plan-gate
// (see its doc) can resolve a plan member task's parent plan state. Wired at
// the gateway boot seam right alongside AgentLoop.SetPlanStore (the same
// planStore value goes to both — see gateway.go's boot wiring region);
// mirrors SetEvidenceCommitter's late-binding discipline. Leaving it unset
// (nil, the test-harness default) makes the gate fail-closed for any task
// carrying a PlanID — see planForGate.
func (te *TaskExecutor) SetPlanStore(store *plan.Store) {
	te.planStore = store
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
	sb.WriteString(fmt.Sprintf("# Task: %s\n\n", t.Title))
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
		sb.WriteString(fmt.Sprintf("## Feedback from attempt %d — address this before re-claiming success:\n", t.AttemptCount))
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
	sb.WriteString(fmt.Sprintf("Priority: %d (1=highest, 5=lowest)\n", t.EffectivePriority()))
	sb.WriteString(fmt.Sprintf("Task ID: %s\n\n", t.ID))

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
	sb.WriteString(fmt.Sprintf("  task_id: %q\n", t.ID))
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
	go func() {
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
	candidates, err := te.store.List(task.Filter{
		Status:      task.StatusNext,
		BlockedByID: completedTaskID,
	})
	if err != nil {
		logger.WarnCF("task_executor", "Orchestrator: could not scan blocked tasks",
			map[string]any{"completed_task_id": completedTaskID, "error": err.Error()})
		return nil
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
func (te *TaskExecutor) advanceBlockedTasks(ctx context.Context, completedTaskID string) {
	for _, taskID := range te.readyBlockedCandidates(completedTaskID) {
		if err := te.ExecuteTask(ctx, taskID); err != nil {
			logger.WarnCF("task_executor", "Orchestrator: advance dispatch failed",
				map[string]any{"task_id": taskID, "error": err.Error()})
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
		sb.WriteString(fmt.Sprintf("- **%s** (status: %s)", c.Title, c.Status))
		if c.Result != "" {
			sb.WriteString(fmt.Sprintf(": %s", c.Result))
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
// Idempotency: if the task already has a SessionID the call is a no-op and
// returns the existing session ID immediately without launching a second agent.
//
// Returns the session ID that was created (or already existed) on success, or
// an empty string and an error when the task cannot be found, already has no
// agent, or the concurrency cap is exhausted.
func (te *TaskExecutor) StartTaskNow(ctx context.Context, taskID string) (string, error) {
	t, err := te.store.Get(taskID)
	if err != nil {
		return "", fmt.Errorf("task_executor: StartTaskNow get task %q: %w", taskID, err)
	}
	if t.AgentID == "" {
		return "", fmt.Errorf("task_executor: StartTaskNow: task %q has no agent assigned", taskID)
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
		if err := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
			ID:        t.ID + "-prompt",
			Role:      "user",
			Content:   te.buildPrompt(t),
			Timestamp: time.Now().UTC(),
		}); err != nil {
			logger.ErrorCF("task_executor", "StartTaskNow: transcript write failed",
				map[string]any{"task_id": taskID, "error": err.Error()})
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

	go te.runTaskFromInProgress(taskCtx, t, taskSessionID, cancel, release)
	return taskSessionID, nil
}

// runTaskFromInProgress is the goroutine body for tasks launched via
// StartTaskNow. The session has already been created and the session_id
// persisted; it skips the session-creation block that runTask performs and
// goes straight to execution, reusing the shared completion logic.
func (te *TaskExecutor) runTaskFromInProgress(
	ctx context.Context,
	t *task.Task,
	taskSessionID string,
	cancel context.CancelFunc,
	release func(),
) {
	var redispatchTaskID string
	defer func() {
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			if err := te.ExecuteTask(context.Background(), redispatchTaskID); err != nil {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	// Test seam: when goroutineCtxHook is set, invoke it and return without
	// performing real agent execution. The hook receives the goroutine's context so
	// tests can assert it is not canceled by the originating request context.
	if te.goroutineCtxHook != nil {
		te.goroutineCtxHook(ctx, t.ID)
		return
	}

	logger.InfoCF("task_executor", "runTaskFromInProgress started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})

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
	redispatchTaskID = te.finishTaskRun(ctx, t, taskSessionID, resp, err, " (StartTaskNow path)")
}

// SpawnTriggeredRun dispatches a fresh run of a task that a time trigger just
// fired. The task has already been reset to `next` by Store.SpawnReset; this
// claims and dispatches it via the normal ExecuteTask path. ExecuteTask already
// guards status==next and concurrency, so no additional gate is needed here.
func (te *TaskExecutor) SpawnTriggeredRun(ctx context.Context, taskID string) error {
	return te.ExecuteTask(ctx, taskID)
}

// ResizeDispatchSema updates the global dispatch semaphore capacity.
func (te *TaskExecutor) ResizeDispatchSema(newCap int) {
	te.dispatchSema.Resize(newCap)
	logger.InfoCF("task_executor", "Dispatch semaphore resized",
		map[string]any{"new_cap": te.dispatchSema.Cap(), "in_flight": te.dispatchSema.InFlight()})
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

// executingPlanStates is the allow-list of plan.State values under which
// CheckQueuedTasks may auto-dispatch a `next` PLAN MEMBER task (S1 fix, UAT
// "PRIYA-GATE-never-executed" / "PRIYA-D8-race"). A member task's own Status
// is user-settable independently of its parent plan's lifecycle — e.g. a
// Kanban drag from Inbox straight to Next in the SPA — so task.StatusNext
// alone is NOT proof the Execute approval gate was ever passed. Approved
// (DoD/owner locked in, ready to run or cap-waiting) and Running (the engine
// is actively dispatching under the plan judge) are the only two states in
// which a human granted permission for autonomous execution; Draft (never
// approved) and the two terminal states Done/Failed (which a user Stop also
// lands on, via FailedReasonStoppedByUser) must never auto-dispatch a
// member — see CheckQueuedTasks' doc for the full rationale.
var executingPlanStates = map[plan.State]bool{ //nolint:gochecknoglobals
	plan.StateApproved: true,
	plan.StateRunning:  true,
}

// planForGate resolves planID's current Plan for CheckQueuedTasks' plan-gate.
// Returns an error (nil plan) when no plan.Store is wired — a minimal test
// harness, or a boot sequence not yet past gateway.go's SetPlanStore wiring —
// or when the lookup itself fails (I/O error, or the plan was deleted out
// from under a task that still names it). Both cases are FAIL-CLOSED by the
// caller: a plan member task whose parent plan's live state cannot be
// verified must never auto-dispatch, matching SetPlanStore's own "will
// remain fail-closed" convention for a nil store.
func (te *TaskExecutor) planForGate(planID string) (*plan.Plan, error) {
	if te.planStore == nil {
		return nil, errors.New("task_executor: no plan store wired, cannot verify parent plan state")
	}
	return te.planStore.Get(planID)
}

// CheckQueuedTasks picks the highest-priority *dispatchable* `next` task per
// agent and starts it. Called by the heartbeat service (pkg/heartbeat's
// TaskDrainService) on an unconditional ~1-minute ticker — this is the
// UNATTENDED auto-dispatch path, distinct from a deliberate single-task
// dispatch (StartTaskNow / a direct ExecuteTask call from e.g. the plan
// engine's own dispatchReadyMembers or a REST "run now" action), which this
// function does not gate and must not: those callers already know exactly
// why the specific task is being run right now.
//
// Skips tasks whose blocked_by dependencies are not all `done`.
//
// S1 UAT fix (PRIYA-GATE-never-executed / PRIYA-D8-race): also skips any
// task whose PlanID names a plan that is not currently in an executing state
// (executingPlanStates — approved or running). Without this, a plan member
// task's status is fully player-settable (a Kanban drag straight from Inbox
// to Next) independent of the plan's own lifecycle, so this unattended drain
// would dispatch a Draft plan's member the moment it turned `next` — the
// Execute confirm dialog's promise that "member tasks will run ... without
// further approval" only ever holds AFTER Execute was actually clicked. The
// same gate closes the Stop leak: a Stop transitions the plan to the
// terminal `failed` state (FailedReasonStoppedByUser) but only cancels
// members already `in_progress` at that instant — a member still `next` at
// the moment of Stop was previously left in the queue for this exact drain
// to pick up and run to completion after the user had already stopped the
// plan. Standalone tasks (PlanID == "") are entirely unaffected: the gate
// only ever runs when PlanID is non-empty.
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
				if gerr != nil {
					logger.WarnCF("task_executor", "Heartbeat: could not resolve parent plan, skipping member task",
						map[string]any{"task_id": t.ID, "plan_id": t.PlanID, "error": gerr.Error()})
					p = nil
				}
				planCache[t.PlanID] = p
			}
			if p == nil || !executingPlanStates[p.State] {
				state := "unresolved"
				if p != nil {
					state = string(p.State)
				}
				logger.WarnCF("task_executor", "Heartbeat: skipping plan member task, parent plan not in an executing state",
					map[string]any{"task_id": t.ID, "plan_id": t.PlanID, "plan_state": state})
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

		if err := te.ExecuteTask(ctx, t.ID); err != nil {
			logger.WarnCF("task_executor", "Heartbeat: could not start task",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
		agentDone[t.AgentID] = agentState{picked: true}
	}
}
