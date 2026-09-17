package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// GoalStoreForTasks returns a goal.Store rooted at the SAME $OMNIPUS_HOME the
// given task.Store is rooted at (task.Store.Dir() is always "<home>/tasks",
// by this codebase's universal convention — see task.New's own doc comment).
//
// Exported because it is THE shared derivation, not one of several: pkg/gateway
// carried a byte-identical private copy until this consolidation, and pkg/
// sysagent/tools carried a third spelling over its own `home` field. One
// implementation, three callers — mirroring is what let the sibling goal rules
// drift in the first place (see TerminateTaskGoalRecord's section header).
//
// This is deliberately NOT a constructor-injected dependency: pkg/goal
// already imports pkg/task (for the shared AcceptanceCriterion type), so
// pkg/task cannot import pkg/goal back, and pkg/agent/loop.go (which
// constructs every tool in this file) is a highest-risk shared file this
// wave's write-set does not include — see this wave's report for the full
// rationale. Deriving the goal store from the already-injected task.Store
// avoids needing any new wiring at the construction call sites. Constructing
// a fresh goal.Store per call is safe and cheap: pkg/entity's cross-call
// locking (pkg/entity/lock.go's package-level `fileLock`, keyed on the full
// data-file path) is process-wide and shared by every Store[T] instance
// regardless of how many are constructed, and goal.NewStore's directory
// pre-create is idempotent.
func GoalStoreForTasks(store *task.Store) *goal.Store {
	return goal.NewStore(filepath.Dir(store.Dir()))
}

// syncTaskGoalRecord creates or updates the goal record paired with task t
// (ADR-086 D2/D5, GOAL-FR-003/FR-012/FR-021/FR-029). A task's goal stays in
// the "defining" phase until the task itself starts and mints a session
// (GOAL-FR-012) — this function only ever creates or updates the record, it
// never activates it.
//
// Exactly one of the three outcomes happens:
//   - Neither criteria nor dod changed: no-op, returns nil.
//   - A goal record already exists for this task: whichever of
//     criteria/dod changed is replaced on it (Goal.SetCriteria/SetDoD);
//     the other list is left untouched.
//   - No goal record exists yet (a legacy, pre-D-C task, GOAL-FR-023/
//     FR-048): one is created, which requires BOTH lists to be supplied and
//     non-empty together — a goal record cannot exist with an empty DoD
//     (goal.Goal.Validate), and "saving an edit" (GOAL-FR-048) submits both
//     lists as one unit, so a caller providing only one of the two on a task
//     with no existing Definition of Done is rejected with a clear reason
//     rather than silently leaving the other list empty.
//
// THE CRITERIA COME FROM t, NEVER FROM THE CALLER. This signature used to take
// a `criteria []task.AcceptanceCriterion` alongside criteriaProvided, and every
// one of its call sites handed it the PRE-normalisation slice it had just given
// to the task store — the one whose criteria still carry empty ids. The store
// mints ids into its own deep copy (task.normalizeCriteria), and goal.New /
// Goal.SetCriteria then mint a SECOND, different set for the same text. The
// criterion id is the join key the verdict projection de-unions the Judge's
// result on (GOAL-FR-007/FR-041), so the same criterion ended up reading `met`
// on the task and `pending` on its goal record. Taking the list off t — the
// record the store has already normalised and persisted — makes that
// divergence unrepresentable rather than merely fixed at three call sites.
// criteriaProvided remains a parameter because it carries something t cannot:
// whether THIS request touched criteria at all.
func syncTaskGoalRecord(
	store *task.Store,
	t *task.Task,
	criteriaProvided bool,
	dod []task.AcceptanceCriterion, dodProvided bool,
	goalMaxRoundsFn func() int,
) error {
	if !criteriaProvided && !dodProvided {
		return nil
	}
	criteria := t.Criteria
	gs := GoalStoreForTasks(store)
	now := time.Now().UTC()
	existing, err := gs.GetByOwner(generated.GoalOwnerKindTask, t.ID)
	if err != nil {
		if !errors.Is(err, goal.ErrOwnerNotFound) {
			return fmt.Errorf("load paired goal record: %w", err)
		}
		if !criteriaProvided || !dodProvided {
			return fmt.Errorf(
				"this task has no existing Definition of Done — saving criteria or dod for the " +
					"first time requires supplying BOTH together (GOAL-FR-048)")
		}
		maxRounds := config.DefaultGoalMaxRounds
		if goalMaxRoundsFn != nil {
			maxRounds = goalMaxRoundsFn()
		}
		// goal.New requires a non-empty Prompt; fall back to Title (always
		// present) when the task carries no Prompt.
		goalPrompt := t.Prompt
		if goalPrompt == "" {
			goalPrompt = t.Title
		}
		g, nErr := goal.New(
			generated.GoalOwnerKindTask, t.ID, generated.GoalSourceTaskExplicit,
			goalPrompt, "", criteria, dod, maxRounds, now,
		)
		if nErr != nil {
			return fmt.Errorf("build goal record: %w", nErr)
		}
		if cErr := gs.Create(g); cErr != nil {
			return fmt.Errorf("create task goal record: %w", cErr)
		}
		return nil
	}
	_, err = gs.Update(existing.GoalID, func(g *goal.Goal) error {
		if criteriaProvided {
			if sErr := g.SetCriteria(criteria, now); sErr != nil {
				return fmt.Errorf("Goal.SetCriteria: %w", sErr)
			}
		}
		if dodProvided {
			if sErr := g.SetDoD(dod, now); sErr != nil {
				return fmt.Errorf("Goal.SetDoD: %w", sErr)
			}
		}
		return nil
	})
	return fmt.Errorf("syncTaskGoalRecord: %w", err)
}

// pairedGoalDoD returns the Definition of Done currently persisted on the goal
// record paired with taskID, or nil when the task has no paired record at all
// (a legacy pre-D-C task, GOAL-FR-023/FR-048).
//
// It exists for the one edit shape that cannot evaluate the distinctness rule
// (GOAL-FR-021/FR-047/FR-048) from the call arguments alone: an update that
// replaces `criteria` and leaves `dod` untouched has to compare the NEW criteria
// against the DoD already on file, and the task record has no DoD field to read
// it from (ADR-086 D5 — pkg/task/task.go has no Dod at all).
//
// A read fault is returned, never swallowed: it means the rule could not be
// evaluated, and a rule that silently does not run is the failure this change
// exists to stop.
func pairedGoalDoD(store *task.Store, taskID string) ([]task.AcceptanceCriterion, error) {
	g, err := GoalStoreForTasks(store).GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		if errors.Is(err, goal.ErrOwnerNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load paired goal record: %w", err)
	}
	return g.DoD, nil
}

// goalTerminalReasonOwnerDeleted is the TerminalReason stamped on a goal record
// whose owning task was deleted, mirroring the vocabulary
// pkg/agent/task_goal_terminal.go's terminalReasonForTask uses for the other
// task endings.
const goalTerminalReasonOwnerDeleted = "owning task was deleted"

// terminateGoalForOwnerDeletion is the TRANSITION half of GOAL-FR-044 ("a goal
// MUST NOT outlive its owner as an unreferenced record. Deleting a task MUST
// transition and remove its goal"), as a pure function over the record so it is
// directly testable without a store.
//
// Only an ACTIVE record is transitioned, and it is transitioned to `cleared` —
// the terminal vocabulary FR-028 reserves for an explicit operator ending,
// which is what deleting the owning task is. A `defining` record (a task
// deleted before it ever ran) is left untouched and is NOT an error: forcing a
// transition there would invent an adjudication that never happened, the same
// reasoning pkg/agent's terminateTaskGoalRecord states. An already-terminal
// record is a no-op, which makes the whole delete path idempotent.
//
// Unexported because RemoveTaskGoalRecords below is the only thing that should
// ever call it, and RemoveTaskGoalRecords is exported. pkg/gateway and
// pkg/sysagent/tools each carried a byte-identical private copy of this
// function — and of the const above — until this consolidation; both are gone.
func terminateGoalForOwnerDeletion(g *goal.Goal, now time.Time) error {
	if g == nil || g.State != generated.GoalStateActive {
		return nil
	}
	if err := g.Terminate(generated.GoalStateCleared, goalTerminalReasonOwnerDeleted, now); err != nil {
		return fmt.Errorf("terminateGoalForOwnerDeletion: %w", err)
	}
	return nil
}

// RemoveTaskGoalRecords implements GOAL-FR-044/EC-4 in full for one task: every
// goal record owned by taskID is transitioned out of the active phase and then
// removed, so no goal survives the deletion of its owner.
//
// ONE implementation, called by all three task-delete surfaces — this file's
// TaskDeleteTool, pkg/gateway/rest_tasks.go's handleTaskDelete, and
// pkg/sysagent/tools/task.go's delete_task_in_workspace. It used to be three
// mirrored private copies, which is the same shape that let the sibling
// task-terminal hook reach only three of its seven writers (see
// TerminateTaskGoalRecord's section header). pkg/tools is the nearest package
// all three callers already import; task.Store.Delete itself cannot host the
// rule because pkg/goal imports pkg/task, so the dependency can only run one
// way. task_goal_delete_guard_test.go is what keeps the call-site set closed.
//
// CALL IT BEFORE deleting the task, and treat a failure as fatal to the whole
// delete. All three surfaces used to delete the task first and then clean up
// best-effort, which meant a goal-store fault produced a PERMANENT orphan — an
// `active` goal record owned by a task id that no longer resolves — that the
// REST surface then reported as 204 No Content. Doing the cleanup first makes
// the failure recoverable instead of reportable: the task is still there, the
// caller gets a real error, and a retry is safe because this function is
// idempotent (an already-terminal record is a no-op, and a record that is
// already gone is simply not found in the List below).
//
// It iterates List rather than calling GetByOwner because GetByOwner refuses to
// guess when a task somehow owns more than one record — and refusing is the
// wrong answer here. "Remove everything this owner owns" is well defined for
// any number of records, and leaving one behind would be precisely the
// unreferenced orphan FR-044 forbids. Errors are joined rather than
// short-circuited so one unwritable record cannot strand the others.
func RemoveTaskGoalRecords(gs *goal.Store, taskID string) error {
	if gs == nil || taskID == "" {
		return nil
	}
	goals, skipped, err := gs.List()
	if err != nil {
		return fmt.Errorf("list goal records: %w", err)
	}
	if len(skipped) > 0 {
		slog.Warn("task delete: unreadable goal records skipped while removing a deleted task's goal",
			"task_id", taskID, "skipped", skipped)
	}
	now := time.Now().UTC()
	var errs []error
	for i := range goals {
		if goals[i].OwnerKind != generated.GoalOwnerKindTask || goals[i].OwnerID != taskID {
			continue
		}
		goalID := goals[i].GoalID
		if _, uErr := gs.Update(goalID, func(g *goal.Goal) error {
			return terminateGoalForOwnerDeletion(g, now)
		}); uErr != nil {
			// Record the fault and still remove the record: a goal that cannot
			// be transitioned must not therefore be left behind ACTIVE and
			// unreferenced, which is the worse of the two outcomes.
			errs = append(errs, fmt.Errorf("terminate goal record %q: %w", goalID, uErr))
		}
		if dErr := gs.Delete(goalID); dErr != nil {
			errs = append(errs, fmt.Errorf("delete goal record %q: %w", goalID, dErr))
		}
	}
	return errors.Join(errs...)
}

// --- the task-terminal -> goal-terminal hook (GOAL-FR-015/FR-027/FR-028) ----
//
// Review finding C1. terminateTaskGoalRecord shipped as an UNEXPORTED helper
// in pkg/agent with three call sites, and its own doc comment asserted those
// three "are" the terminal writers. They are not. A task also reaches a
// terminal status through PATCH /api/v1/tasks/{id} (rest_tasks.go's
// handleTaskPatch — validateTransition permits in_progress->done and ->failed,
// which is what a Kanban drag onto Done or Failed sends), through the boot
// reconciler that resets EVERY stranded in_progress task to failed
// (reconcileStuckTasks), and through update_task / update_task_in_workspace
// with status:"failed" (the done-claim judge deferral only intercepts `done`).
// Those three live in pkg/gateway, pkg/tools and pkg/sysagent/tools, none of
// which can see an unexported pkg/agent symbol — so the hook was structurally
// unreachable from more than half of the writers it claimed to cover.
//
// WHY THIS LIVES HERE, AND WHY IT IS NOT A STORE-LEVEL CHOKEPOINT. The real
// chokepoint would be task.Store's own status-write path (updateLocked): one
// place, no call sites to forget. It is impossible — pkg/goal imports pkg/task
// (for the shared AcceptanceCriterion type), so pkg/task cannot import pkg/goal
// back. task.Patch.Criteria's own doc comment and removeTaskGoalRecords' both
// already state this for the two sibling problems ("this store has no way to do
// that itself ... the dependency can only run the other way").
//
// pkg/tools is the nearest thing to a chokepoint that IS reachable: it imports
// pkg/goal and pkg/task, and it is imported by all three of the other writer
// packages (pkg/agent, pkg/gateway, pkg/sysagent/tools). So the TRANSITION has
// exactly one implementation, shared — not the five mirrored copies this file
// family's other goal helpers use (goalStoreForTasks, syncTaskGoalRecord,
// terminateGoalForOwnerDeletion). Mirroring is what let three of seven writers
// ship without the hook at all; for a rule whose whole job is to be applied
// everywhere, one copy is the point.
//
// The remaining call sites are then made ENFORCEABLE rather than remembered:
// task_goal_terminal_guard_test.go enumerates every function in pkg/agent,
// pkg/gateway, pkg/tools and pkg/sysagent/tools that writes task.Patch.Status,
// and fails when a new one appears that neither calls this hook nor carries a
// written justification for why it cannot reach a terminal status.

// terminalGoalReasonMaxRunes bounds the task text carried onto the goal
// record's TerminalReason. Mirrors pkg/agent's maxFailClosedOutputChars, the
// bound the task's own Result already carries, so the goal record cannot grow
// a copy of an unbounded worker response.
const terminalGoalReasonMaxRunes = 2000

// GoalStateForTerminalTask maps a task's terminal disposition onto the goal
// state its paired record must end in, using the SAME vocabulary
// pkg/agent/goal_loop.go's clearGoalStatus already uses for a chat goal — so a
// task goal and a chat goal that ended the same way read the same way:
//
//	task done                       -> met        (clearGoalStatus's goalClearNoteMet)
//	task failed, stopped by a user  -> cleared    (clearGoalStatus's goalClearNoteUser)
//	task failed, any other reason   -> exhausted  (clearGoalStatus's default)
//
// `expired` is deliberately NOT produced here: it belongs to the idle-expiry
// calendar sweep (goalIdleExpirySweep), which is about a goal nobody touched
// for days, not about a task that ran and finished.
//
// ok is false for a non-terminal status, so a caller that reaches this with a
// mid-flight task transitions nothing.
func GoalStateForTerminalTask(status task.Status, cancelReason task.CancelReason) (generated.GoalState, bool) {
	switch status {
	case task.StatusDone:
		return generated.GoalStateMet, true
	case task.StatusFailed:
		if cancelReason == task.CancelReasonStoppedByUser {
			return generated.GoalStateCleared, true
		}
		return generated.GoalStateExhausted, true
	default:
		return "", false
	}
}

// TerminalGoalReasonForTask builds the Goal.TerminalReason text retained on the
// record. It names the task outcome that ended the goal and carries the task's
// own result/reason text when there is one, so a terminal goal record explains
// itself without a reader having to go and find the task.
func TerminalGoalReasonForTask(status task.Status, reason string) string {
	if reason == "" {
		return fmt.Sprintf("owning task reached %s", status)
	}
	return fmt.Sprintf("owning task reached %s: %s", status, truncateGoalReason(reason))
}

// truncateGoalReason bounds s to terminalGoalReasonMaxRunes runes, noting the
// cut so a reader is not misled into thinking the text ended there naturally.
// Rune-safe (slices a []rune, never a byte index) for the same reason
// pkg/agent's truncateRunes is.
func truncateGoalReason(s string) string {
	r := []rune(s)
	if len(r) <= terminalGoalReasonMaxRunes {
		return s
	}
	return string(r[:terminalGoalReasonMaxRunes]) + "\n... (truncated, output continues)"
}

// TerminateTaskGoalRecord ends the goal record paired with a task that has just
// reached a terminal status (GOAL-FR-015/FR-027/FR-028). Call it from EVERY
// terminal disposition of a task — see this section's header comment for the
// seven that exist and why they cannot be collapsed into one.
//
// Every outcome is a no-op rather than an error except a genuine store fault:
//
//   - no paired goal record (GOAL-FR-023 legacy/Scratchpad task): nothing to end.
//   - the record is still `defining`: the task terminated without ever starting
//     (Stop on a queued task, or a create-then-cancel). `defining` is the
//     legitimate resting state for a record whose task has not run —
//     goal.Goal.Terminate refuses it outright, and forcing a transition would
//     invent an adjudication that never happened.
//   - the record is already terminal: another writer (or a previous call) ended
//     it. Idempotent by construction, which is what makes it safe to call from
//     every writer without any of them having to know about the others.
//
// Best-effort and non-fatal: the task has ALREADY been written terminal by the
// caller, so a broken goal store must never retroactively change the task's own
// outcome. A fault is logged at Warn so "the goal record was not closed" is
// never silent — which is precisely how the original defect survived to UAT.
func TerminateTaskGoalRecord(
	gs *goal.Store, taskID string, status task.Status, cancelReason task.CancelReason, reason string,
) {
	if gs == nil || taskID == "" {
		return
	}
	state, ok := GoalStateForTerminalTask(status, cancelReason)
	if !ok {
		slog.Warn("task goal: refusing to terminate a task's goal record for a non-terminal task status",
			"task_id", taskID, "status", string(status))
		return
	}

	g, err := gs.GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		if errors.Is(err, goal.ErrOwnerNotFound) {
			slog.Debug("task goal: terminated task has no paired goal record to end (GOAL-FR-023)",
				"task_id", taskID)
			return
		}
		slog.Warn("task goal: could not look up the paired goal record of a terminated task — "+
			"the record may be left ACTIVE (GOAL-FR-015)",
			"task_id", taskID, "status", string(status), "error", err)
		return
	}
	if g.State != generated.GoalStateActive {
		slog.Debug("task goal: paired goal record is not active — nothing to terminate",
			"task_id", taskID, "goal_id", g.GoalID, "goal_state", string(g.State))
		return
	}

	now := time.Now().UTC()
	terminalReason := TerminalGoalReasonForTask(status, reason)
	transitioned := false
	updated, uErr := gs.Update(g.GoalID, func(cur *goal.Goal) error {
		if cur.State != generated.GoalStateActive {
			// Another writer ended it between the read above and this
			// lock-held mutate. Not a fault — see the idempotence note.
			return nil
		}
		if terr := cur.Terminate(state, terminalReason, now); terr != nil {
			return fmt.Errorf("Goal.Terminate: %w", terr)
		}
		transitioned = true
		return nil
	})
	if uErr != nil {
		slog.Warn("task goal: the paired goal record of a terminated task could not be transitioned — "+
			"it is still ACTIVE (GOAL-FR-015)",
			"task_id", taskID, "goal_id", g.GoalID, "goal_state", string(state), "error", uErr)
		return
	}
	if !transitioned || updated == nil {
		return
	}
	slog.Info("task goal: paired goal record terminated with its task",
		"task_id", taskID, "goal_id", g.GoalID, "goal_state", string(state))
	if hook := taskGoalEndedHook.Load(); hook != nil {
		(*hook)(*updated, status)
	}
}

// taskGoalEndedHook is TerminateTaskGoalRecord's after-transition observer:
// called exactly once per task-owned goal this function actually ended, with
// the record as saved — never when the record was already terminal, still
// defining, or the store refused the write. pkg/agent installs its goal
// outcome recorder here at gateway boot (AgentLoop.InstallTaskGoalOutcomeRecorder)
// so a task's run session gets the same lasting outcome line a chat goal's
// session does; this package cannot reach pkg/agent to do it directly.
// Process-wide, like every seam of its kind: one agent loop per process.
var taskGoalEndedHook atomic.Pointer[func(ended goal.Goal, taskStatus task.Status)]

// SetTaskGoalEndedHook installs fn as TerminateTaskGoalRecord's
// after-transition observer (nil removes it) and returns a func that restores
// the previous observer — for tests; production installs once at boot.
func SetTaskGoalEndedHook(fn func(ended goal.Goal, taskStatus task.Status)) (restore func()) {
	var next *func(ended goal.Goal, taskStatus task.Status)
	if fn != nil {
		next = &fn
	}
	prev := taskGoalEndedHook.Swap(next)
	return func() { taskGoalEndedHook.Store(prev) }
}

// TaskCreateTool creates a task and delegates it to another agent.
type TaskCreateTool struct {
	BaseTool
	store *task.Store
	// delegationDeny, when non-nil, applies the full delegation policy (trust
	// set + modes ("task") + depth — FR-6.2). Returns a non-nil *DelegationDenial
	// to DENY (carrying the structured reason + policy axis) or nil to ALLOW.
	// This is the ONLY delegation gate (ADR-037 retired the legacy boolean
	// delegateCheck fallback, which was only ever consulted when this was nil —
	// never happened in production wiring).
	delegationDeny func(ctx context.Context, targetAgentID string) *DelegationDenial
	// onCreate, when non-nil, is invoked after a task is successfully created so
	// the caller can emit a task_status_changed event.
	onCreate func(*task.Task)
	// home is the OMNIPUS_HOME path used to resolve the default workspace ID
	// when no workspace is bound to the current turn context. Set via SetHome.
	home string
	// maxDelegationDepth is the hard ceiling on the task-mode delegation
	// generation counter. A task_create issued from within a task run whose
	// generation already equals (or exceeds) this bound is rejected, so an
	// A→B→A task→task chain cannot recurse unboundedly. Set via
	// SetMaxDelegationDepth; 0 disables the bound (no caller should leave it 0).
	maxDelegationDepth int
	// bashPolicyChecker resolves an assignee agent's effective "bash" tool
	// policy (ADR-049 D2 rule 5, FR-017/052). Set via SetBashPolicyChecker.
	bashPolicyChecker func(assigneeAgentID string) (policy string, ok bool)
	// assigneeCannotFinish answers whether the assignee can finish the task at
	// all (founder decision 2026-09-15, task_assignee_readiness.go). Set via
	// SetAssigneeReadinessChecker.
	assigneeCannotFinish AssigneeReadinessChecker
	// planStore, when set, backs the optional plan_id linkage arg (ADR-052
	// FR-002): validates the same-workspace FK and refuses any plan that has
	// left draft (ValidateTaskPlanMembership, plan.go). A nil planStore with
	// a non-empty plan_id arg fails closed (see SetPlanStore).
	planStore *plan.Store
	// goalMaxRoundsFn, when set, resolves the live single global goal-round
	// ceiling (Settings -> Performance, D-D/D-E — one setting governs task
	// goals and chat goals identically; there is no per-goal override) for
	// the paired goal record this tool creates. Wired in production by
	// pkg/agent/loop.go (registerSharedTools); unwired (nil, e.g. a bare unit
	// test) falls back to config.DefaultGoalMaxRounds.
	goalMaxRoundsFn func() int
}

func NewTaskCreateTool(store *task.Store) *TaskCreateTool {
	return &TaskCreateTool{store: store}
}

// SetHome configures the OMNIPUS_HOME path so that task_create can resolve the
// real default workspace ID (via workspace.ResolveDefaultID) when no workspace
// is bound to the turn context. The agent loop calls this after constructing the
// tool (pkg/agent/loop.go ~line 1608).
func (t *TaskCreateTool) SetHome(home string) {
	t.home = home
}

// SetMaxDelegationDepth installs the hard task-mode recursion bound. A
// task_create issued from within a task run whose stored DelegationDepth is
// already >= the bound is rejected. The agent loop passes agent.maxTaskDepth (10).
func (t *TaskCreateTool) SetMaxDelegationDepth(bound int) {
	t.maxDelegationDepth = bound
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2).
func (t *TaskCreateTool) SetDelegationDenyChecker(
	fn func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDeny = fn
}

// SetOnCreate sets the callback invoked after a task is successfully created.
func (t *TaskCreateTool) SetOnCreate(fn func(*task.Task)) {
	t.onCreate = fn
}

// SetBashPolicyChecker installs the D2 rule 5 checker (ADR-049, FR-017/052):
// resolves the assignee agent's effective "bash" tool policy so a create
// whose criteria are ALL kind=check can be rejected as structurally
// unsatisfiable when that policy is deny or ask (ask resolves to deny
// unattended at judge time, D2 rule 2 — a machine check that can never even
// run can never adjudicate MET). fn should return ok=false when the assignee
// agent cannot be resolved at all.
//
// Mirrors the fail-closed-when-unwired discipline SetDelegationDenyChecker
// documents above — an unwired checker is a configuration error, never a
// permission grant. Do NOT default an unwired checker's outcome to "allow".
func (t *TaskCreateTool) SetBashPolicyChecker(fn func(assigneeAgentID string) (policy string, ok bool)) {
	t.bashPolicyChecker = fn
}

// SetPlanStore installs the plan store backing the optional plan_id linkage
// arg (ADR-052 FR-002). Wired by the agent loop alongside the task store; a
// nil (unwired) store makes any create_task(plan_id=...) call fail closed —
// see ValidateTaskPlanMembership (plan.go). create_task calls with no plan_id
// are entirely unaffected by whether this is wired.
func (t *TaskCreateTool) SetPlanStore(store *plan.Store) {
	t.planStore = store
}

// SetGoalMaxRoundsFn installs the live accessor for the single global
// Settings -> Performance goal-round ceiling (D-D/D-E), applied to the goal
// record this tool creates alongside every task. Unwired (the zero value,
// nil), create_task falls back to config.DefaultGoalMaxRounds — the SAME
// shipped default the config system itself falls back to
// (PlanningConfig.EffectiveGoalMaxRounds) — rather than failing the create
// outright, because refusing every task creation on an unwired accessor would
// be a much larger regression. Production wiring: pkg/agent/loop.go's
// registerSharedTools calls this with the live goal try limit
// (goalTryLimit), next to SetPlanStore — pinned by
// pkg/agent/task_attempt_budget_test.go. The value written here is the
// creation-time snapshot only; the task executor re-stamps the live value
// when the task's run starts (TaskExecutor.activateTaskGoal).
func (t *TaskCreateTool) SetGoalMaxRoundsFn(fn func() int) {
	t.goalMaxRoundsFn = fn
}

// parseCriteriaArgs converts the create_task tool's raw "criteria" argument
// (a []any of map[string]any — the shape LLM tool-call arguments always
// decode into) into []task.AcceptanceCriterion. Every criterion is
// server-authored as the CALLING agent — agent-created criteria are, by
// definition, agent-authored (SD-A7); author is never accepted from args.
// Shape/length validation (kind enum, text bounds, check-shape-iff-kind,
// ID/status defaulting) is left to the store's own normalizeCriteria,
// invoked from Store.Create — this only handles the untyped-map decode.
//
// Behavior payloads (ADR-052 FR-034 / ADR-074 D3a) decode via the shared
// task.DecodeBehaviorPayload, which honors the pointer semantics
// pkg/task/criterion.go documents (absent min_count/max_count stay nil; an
// explicit 0 decodes to a pointer at 0).
func parseCriteriaArgs(raw []any, authorAgentID string) ([]task.AcceptanceCriterion, error) {
	out := make([]task.AcceptanceCriterion, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("criteria[%d]: must be an object", i)
		}
		kind, _ := m["kind"].(string)
		judgment, _ := m["judgment"].(string)
		text, _ := m["text"].(string)
		c := task.AcceptanceCriterion{
			Kind:     task.CriterionKind(kind),
			Judgment: task.JudgmentKind(judgment),
			Text:     text,
			Author:   task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: authorAgentID},
		}
		if chk, ok := m["check"].(map[string]any); ok {
			command, _ := chk["command"].(string)
			var expectedExitCode int
			if v, ok := chk["expected_exit_code"].(float64); ok {
				expectedExitCode = int(v)
			}
			c.Check = &task.CriterionCheck{Command: command, ExpectedExitCode: expectedExitCode}
		}
		if beh, ok := m["behavior"].(map[string]any); ok {
			c.Behavior = task.DecodeBehaviorPayload(beh)
		}
		// ADR-074 D2: kind is optional at authoring time — resolve it from the
		// payload shape HERE, before the caller's ADR-049 D2-rule-5 all-check
		// bash-policy gate runs, so the gate fires on inferred kinds too. An
		// explicit kind passes through unchanged; kind-less with BOTH payloads
		// is rejected as ambiguous.
		k, kErr := task.InferCriterionKind(&c)
		if kErr != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, kErr)
		}
		c.Kind = k
		// ADR-080 D-TYPES: judgment is likewise optional at authoring time —
		// resolve it from the now-resolved kind HERE (mirroring InferCriterionKind
		// immediately above), so every criterion this parser produces carries an
		// explicit judgment before it ever reaches criterionKey/sameShape
		// dedup comparisons against already-normalized (and therefore
		// judgment-backfilled) stored criteria.
		j, jErr := task.InferJudgment(&c)
		if jErr != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, jErr)
		}
		c.Judgment = j
		out = append(out, c)
	}
	return out, nil
}

// allCheckCriteria reports whether criteria is non-empty and EVERY entry is
// kind=check (ADR-049 D2 rule 5 gate condition).
func allCheckCriteria(criteria []task.AcceptanceCriterion) bool {
	if len(criteria) == 0 {
		return false
	}
	for _, c := range criteria {
		if c.Kind != task.KindCheck {
			return false
		}
	}
	return true
}

// describeBashPolicy renders a bashPolicyChecker result for an error message:
// the resolved policy string, or "unresolvable" when the assignee agent
// itself could not be found.
func describeBashPolicy(policy string, ok bool) string {
	if !ok {
		return "unresolvable"
	}
	return policy
}

func (t *TaskCreateTool) Name() string { return "create_task" }

func (t *TaskCreateTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskCreateTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskCreateTool) Description() string {
	return "Create a task and assign it to an agent for execution.\n" +
		"This is a DELEGATION: it passes the same delegation-policy gate (trust set + modes + depth) as " +
		"any other delegation, and is refused if you are not authorized to delegate to the assignee. " +
		"criteria AND dod are BOTH REQUIRED: at least one acceptance criterion and at least one " +
		"definition-of-done item — a task created with either missing is rejected (GOAL-FR-021). " +
		"criteria are the outcome-specific checks for THIS task; dod are the generic standing quality " +
		"gates (mirrors Goal.dod) — the two are judged identically but never mixed together. Before " +
		"authoring either, load the define-goal skill (via the Skill tool) and follow its quality bar. " +
		"If every criterion is kind=check, the assignee's effective bash policy " +
		"must be allow, or the create is rejected as structurally unsatisfiable (a machine check that can " +
		"never run can never adjudicate MET). The task lands as a visible card on the workspace board in " +
		"status `next` (triaged and dispatchable) — never `inbox`."
}

func (t *TaskCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Short title for the task",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Full instructions for the agent",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to assign the task to",
			},
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest); default 3",
			},
			"due": map[string]any{
				"type":        "string",
				"description": "Due date/time in RFC 3339 format (optional)",
			},
			"parent_task_id": map[string]any{
				"type":        "string",
				"description": "ID of the parent task (optional) — set when this is a subtask of another task",
			},
			"plan_id": map[string]any{
				"type":        "string",
				"description": "ID of the Plan this task is a member of (optional). Must exist in the same workspace and must still be a DRAFT plan — a plan's membership is frozen at approval, so an approved, running, done or failed plan rejects a new member. To add work to a plan that is already under way, use a plan-supervision correction instead.",
			},
			"write_set": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Concrete paths this plan member creates/edits (optional). Meaningful only alongside plan_id; plan-lint reads this at approve to reject overlapping parallel streams. Empty/omitted for an exploratory member whose write footprint is unknowable up front.",
			},
			"stream": map[string]any{
				"type":        "string",
				"description": "The parallel-group id this plan member belongs to (optional). Members sharing a stream run serially within it; different streams may run concurrently provided their write_sets are disjoint.",
			},
			"is_join": map[string]any{
				"type":        "boolean",
				"description": "True marks this plan member as an authored join/assemble member with its own criteria, converging one or more parallel streams into a single artifact. Defaults to false.",
			},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs this task is blocked by (optional). Each blocker must exist and be in the same workspace; a cycle is rejected.",
			},
			"criteria": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type": "string",
							"enum": []string{"check", "prose", "behavior"},
							"description": "check: a shell command verified via the assignee's own bash tool; " +
								"prose: a free-text statement judged by the Judge System Agent; " +
								"behavior: a deterministic count of successful calls of a named tool in the " +
								"session's tool-call log. Optional (ADR-074 D2) — when omitted, inferred " +
								"from the payload: check payload => check, behavior payload => behavior, " +
								"no payload => prose. An explicit kind mismatching its payload is rejected.",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "The criterion statement (1-1000 characters)",
						},
						"check": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"command":            map[string]any{"type": "string", "description": "Shell command to run"},
								"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
							},
							"description": "Required when kind is \"check\"; must be omitted for other kinds",
						},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Acceptance criteria for this task — the outcome-specific checks. " +
					"REQUIRED: at least one criterion — an agent-created task with zero criteria is rejected.",
			},
			"dod": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type": "string",
							"enum": []string{"check", "prose", "behavior"},
							"description": "check: a shell command verified via the assignee's own bash tool; " +
								"prose: a free-text statement judged by the Judge System Agent; " +
								"behavior: a deterministic count of successful calls of a named tool in the " +
								"session's tool-call log. Optional (ADR-074 D2) — when omitted, inferred " +
								"from the payload: check payload => check, behavior payload => behavior, " +
								"no payload => prose. An explicit kind mismatching its payload is rejected.",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "The definition-of-done statement (1-1000 characters)",
						},
						"check": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"command":            map[string]any{"type": "string", "description": "Shell command to run"},
								"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
							},
							"description": "Required when kind is \"check\"; must be omitted for other kinds",
						},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Definition of Done for this task (GOAL-FR-003/FR-021/FR-048) — generic " +
					"standing quality gates, DISTINCT from criteria and never mixed into it, judged " +
					"identically. REQUIRED: at least one item — an agent-created task with zero dod " +
					"items is rejected.",
			},
		},
		"required": []string{"title", "prompt", "agent_id", "criteria", "dod"},
	}
}

// resolveBlockedBy parses the optional "blocked_by" array arg into a slice of
// non-empty task IDs. It distinguishes three cases so the caller can tell a
// CLEAR (provided empty) from an unchanged (absent) request:
//   - absent   → deps == nil, provided == false  (leave the field unchanged)
//   - provided → deps == nil/empty, provided == true (CLEAR the list)
//   - provided → deps non-empty, provided == true (REPLACE with deps)
//
// Empty-string and non-string entries are dropped (they are not valid task IDs).
func resolveBlockedBy(args map[string]any) (deps []string, provided bool) {
	rawDeps, ok := args["blocked_by"].([]any)
	if !ok {
		return nil, false
	}
	deps = make([]string, 0, len(rawDeps))
	for _, d := range rawDeps {
		if s, ok := d.(string); ok && s != "" {
			deps = append(deps, s)
		} else {
			slog.Debug("task: dropped invalid blocked_by entry", "entry", d)
		}
	}
	if len(deps) == 0 {
		return nil, true // provided but empty → caller treats as CLEAR
	}
	return deps, true
}

// validateBlockersWorkspace loads each blocker task and verifies it is in the
// same workspace as the dependent. This mirrors TaskAddDependencyTool's
// cross-workspace guard; the store's validateBlockedByLocked handles
// cycle/self-edge/missing/depth, but NOT the same-workspace constraint, so the
// tool layer enforces it.
//
// TOCTOU note: WorkspaceID is immutable (set at create, never in task.Patch), so
// the same-workspace check is race-free — a blocker's workspace cannot change
// between this pre-check and the locked write. The store re-validates the DAG
// invariants (cycle/self-edge/missing/depth) atomically under the per-task lock;
// this tool-layer guard adds the same-workspace rule the store does not enforce.
func validateBlockersWorkspace(store *task.Store, dependentWorkspaceID string, blockers []string) error {
	for _, b := range blockers {
		bt, err := store.Get(b)
		if err != nil {
			if errors.Is(err, task.ErrNotFound) {
				return fmt.Errorf("blocker task %q not found", b)
			}
			return fmt.Errorf("could not load blocker task %q: %w", b, err)
		}
		if bt.WorkspaceID != dependentWorkspaceID {
			return fmt.Errorf("blocker task %q is in a different workspace", b)
		}
	}
	return nil
}

// resolveWorkspaceID returns the workspace ID for the current turn. It first
// checks the turn context (explicitly bound workspace), then falls back to the
// real default workspace on disk (via workspace.ResolveDefaultID). It returns
// an error rather than inventing a literal ID, so chat-delegated tasks never
// land in an invisible workspace.
func (t *TaskCreateTool) resolveWorkspaceID(ctx context.Context) (string, error) {
	if ws := ToolWorkspaceID(ctx); ws != "" {
		// Belt-and-suspenders (M4): a bound id that no longer exists on disk
		// (stale/typo'd) would land the task on an invisible board. Treat a
		// non-existent ctx id as unbound and fall through to the default.
		if t.home == "" || workspace.Exists(t.home, ws) {
			return ws, nil
		}
		slog.Warn("create_task: bound workspace_id does not exist — falling back to default",
			"workspace_id", ws)
	}
	if t.home != "" {
		id, err := workspace.ResolveDefaultID(t.home)
		if err != nil {
			return "", fmt.Errorf("could not resolve default workspace: %w", err)
		}
		return id, nil
	}
	return "", fmt.Errorf("no active workspace bound and no default workspace resolver configured")
}

// taskCreateToolExecute carries the shared state of Execute across its stages.
type taskCreateToolExecute struct {
	t          *TaskCreateTool
	ctx        context.Context
	args       map[string]any
	title      string
	prompt     string
	agentID    string
	callerID   string
	criteria   []task.AcceptanceCriterion
	childDepth int
	priority   int
	due        string
	dod        []task.AcceptanceCriterion
	entity     *task.Task
}

func (t *TaskCreateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	tc := &taskCreateToolExecute{t: t, ctx: ctx, args: args}

	if r0, stop := tc.validateRequest(); stop {
		return r0
	}

	if r0, stop := tc.prepareContract(); stop {
		return r0
	}

	if r0, stop := tc.buildTask(); stop {
		return r0
	}

	return tc.persistAndRespond()
}

// validateRequest checks required inputs, caller identity, delegation authority, criteria, and the assignee's machine-check capability.
func (tc *taskCreateToolExecute) validateRequest() (*ToolResult, bool) {
	if tc.t.store == nil {
		return ErrorResult("create_task failed: task store is not available"), true
	}
	tc.title, _ = tc.args["title"].(string)
	tc.prompt, _ = tc.args["prompt"].(string)
	tc.agentID, _ = tc.args["agent_id"].(string)
	tc.callerID = strings.TrimSpace(ToolAgentID(tc.ctx))

	if tc.title == "" {
		return ErrorResult("title is required"), true
	}
	if tc.prompt == "" {
		return ErrorResult("prompt is required"), true
	}
	if tc.agentID == "" {
		return ErrorResult("agent_id is required"), true
	}
	// FAIL CLOSED on an unresolvable caller. create_task is a delegation: it
	// assigns work to ANOTHER agent, and both halves of that record — the
	// delegation-policy decision and the FR-037 provenance stamp
	// (Store.CreateByAgent, which itself rejects an empty agent id) — are
	// meaningless without a principal. Refusing here keeps the two consistent
	// rather than letting the policy gate run against an empty caller and then
	// discovering the missing principal at write time.
	if tc.callerID == "" {
		return ErrorResult("create_task: cannot resolve the calling agent; refusing to create a delegated task"), true
	}

	// Delegation policy gate (FR-6.2): trust set + modes ("task") + depth.
	// ADR-037: the legacy boolean delegateCheck fallback is retired — this is
	// now the only gate. Checked BEFORE the criteria validation below so an
	// unauthorized caller always gets a consistent delegation-denied response
	// regardless of what else they did or didn't supply (authorization first,
	// business-rule validation second).
	//
	// FAIL CLOSED, not open, when no checker is wired: an unwired deny-checker
	// is a configuration error, never a permission grant. Unreachable in
	// today's production wiring (pkg/agent/loop.go always calls
	// SetDelegationDenyChecker), but the legacy fallback this replaced was
	// itself deny-by-default — removing it must not silently flip the
	// unwired-checker case from deny to allow for the NEXT wiring bug (a new
	// agent-construction path, a v0.3 plugin-system entry point, a refactor
	// slip). Do NOT "simplify" this back to fail-open — CLAUDE.md Hard
	// Constraint #6 forbids a silent runtime default here.
	if tc.t.delegationDeny != nil {
		if denial := tc.t.delegationDeny(tc.ctx, tc.agentID); denial != nil {
			return DelegationDeniedResult("create_task", denial), true
		}
	} else {
		slog.Error("create_task: no delegation-deny checker installed — denying by default",
			"caller_id", tc.callerID, "target_agent_id", tc.agentID)
		return DelegationDeniedResult("create_task", &DelegationDenial{
			Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
			Policy:        DenyTrustSet,
			TargetAgentID: tc.agentID,
		}), true
	}

	// FR-6/D5 strict criteria enforcement (ADR-049, SD-A7, review r1 major
	// M5): an agent-created task requires at least one acceptance criterion —
	// human/UI creation (which never calls this tool) may still leave
	// Criteria empty (the soft tier, judged against Prompt/title/description
	// at judge time instead, ADR-049 D5). rawCriteria absent or an empty
	// array both fail this check identically.
	rawCriteria, _ := tc.args["criteria"].([]any)
	if len(rawCriteria) == 0 {
		return ErrorResult(
			"Add at least one acceptance criterion: say what must be true for this task to be done.",
		), true
	}
	var cErr error
	tc.criteria, cErr = parseCriteriaArgs(rawCriteria, tc.callerID)
	if cErr != nil {
		return ErrorResult(fmt.Sprintf("task_create failed: %v", cErr)), true
	}

	// D2 rule 5 (FR-017/052): an all-check criteria set can never be
	// adjudicated MET if the assignee's effective bash policy is deny or ask
	// (ask resolves to deny unattended at judge time, D2 rule 2) — the
	// machine check could never even run. Reject at write time rather than
	// let the task loop forever against a structurally unsatisfiable DoD.
	if allCheckCriteria(tc.criteria) {
		if tc.t.bashPolicyChecker == nil {
			// FAIL CLOSED, not open, when no checker is wired — same rationale
			// as the delegation gate above: an unwired checker is a
			// configuration error, never a permission grant.
			slog.Error("create_task: no bash-policy checker installed — denying an "+
				"all-check criteria set by default",
				"caller_id", tc.callerID, "target_agent_id", tc.agentID)
			return ErrorResult(
				"task_create failed: cannot verify the assignee's bash policy (D2 rule 5 checker not " +
					"configured) — denying an all-machine-criteria create by default",
			), true
		}
		policy, ok := tc.t.bashPolicyChecker(tc.agentID)
		if !ok || policy != string(config.ToolPolicyAllow) {
			return ErrorResult(fmt.Sprintf(
				"task_create failed: all criteria are machine-checkable (kind=check) but agent %q's "+
					"effective bash policy is %q — this criteria set could never be satisfied "+
					"(structurally unsatisfiable, ADR-049 D2 rule 5)",
				tc.agentID, describeBashPolicy(policy, ok),
			)), true
		}
	}
	return nil, false
}

// prepareContract enforces delegation depth and field ordering, parses the Definition of Done, and validates contract distinctness and assignee readiness.
func (tc *taskCreateToolExecute) prepareContract() (*ToolResult, bool) {
	// Task-mode recursion bound (SEC): a task_create issued from *within* a task
	// run carries that run's delegation generation on the context. Each task→task
	// hop increments the generation; reject once it would exceed the hard ceiling.
	// This is the runtime bound the per-agent depth gate cannot enforce on its own
	// because every task run starts a FRESH turn at turnState depth 0 — without
	// this counter, an A→B→A task-mode chain would recurse unboundedly.
	parentDepth := ToolDelegationDepth(tc.ctx)
	tc.childDepth = parentDepth + 1
	if tc.t.maxDelegationDepth > 0 && tc.childDepth > tc.t.maxDelegationDepth {
		return DelegationDeniedResult("create_task", &DelegationDenial{
			Reason: fmt.Sprintf(
				"maximum task delegation depth (%d) reached — cannot create a further delegated task",
				tc.t.maxDelegationDepth,
			),
			Policy:        DenyDepth,
			TargetAgentID: tc.agentID,
		}), true
	}

	// M2(a)/(b) fix: args["priority"] being PRESENT (ok==true) means the caller
	// supplied an explicit value — including an explicit 0 — which must be
	// validated and, if invalid, REJECTED rather than silently replaced with
	// the default. The previous `ok && p >= 1 && p <= 5` guard let any
	// out-of-range value (0, 6, negative...) simply fail the condition and
	// fall through to priority=3 with no error at all: the caller received a
	// success response with their input silently discarded. task.ValidatePriority
	// is the same shared range-check update_task, create_task_in_workspace, and
	// the REST create/update handlers all use, so this can't drift out of sync
	// with them again.
	tc.priority = 3
	if p, ok := tc.args["priority"].(float64); ok {
		pr := int(p)
		if err := task.ValidatePriority(pr); err != nil {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", err)), true
		}
		tc.priority = pr
	}

	// Optional due date (RFC 3339), mirroring update_task's own validation:
	// due is a general task attribute, not gated behind plan_id the way
	// write_set/stream/is_join are, so its absence here (while
	// create_task_in_workspace and update_task both accept it) was a genuine
	// schema-consistency gap rather than a deliberate restriction.

	if d, ok := tc.args["due"].(string); ok && d != "" {
		if _, pErr := time.Parse(time.RFC3339, d); pErr != nil {
			return ErrorResult(fmt.Sprintf("invalid due date %q (must be RFC 3339): %v", d, pErr)), true
		}
		tc.due = d
	}

	// GOAL-FR-021/D-C: dod is mandatory alongside criteria, uniformly, on
	// every task-creation surface — this tool included (R-25). Distinct
	// list, same author-stamping and parse rules as criteria above.
	//
	// ORDER MATTERS — do NOT hoist this back above the per-field checks.
	// It sits deliberately AFTER title/prompt/agent_id, the delegation gate,
	// criteria, the D2-rule-5 bash gate, the depth bound, priority and due,
	// because a gate placed first PRE-EMPTS every one of them: a caller who
	// sends priority=6 and no dod must hear "priority must be between 1 and
	// 5", not a dod complaint that hides the real defect in their call. The
	// gate itself is unconditional (D-C) either way; only which message the
	// caller sees first changes. Regression oracle:
	// TestTaskCreate_DoDGateDoesNotPreemptFieldValidation.
	rawDoD, _ := tc.args["dod"].([]any)
	if len(rawDoD) == 0 {
		return ErrorResult(
			"Add at least one Definition of Done item, distinct from the acceptance criteria.",
		), true
	}
	var dErr error
	tc.dod, dErr = parseCriteriaArgs(rawDoD, tc.callerID)
	if dErr != nil {
		return ErrorResult(fmt.Sprintf("task_create failed: dod: %v", dErr)), true
	}
	// The DISTINCTNESS half of the same rule the refusal above advertises. It
	// was advertised on every surface and checked on none: a task whose DoD
	// restates its acceptance criteria spends a second judged slot on a
	// sentence already being scored (the judge unions Criteria and DoD). See
	// task.ValidateDoDDistinct for the rule and for why it stops at whitespace
	// and case rather than reaching for similarity. Checked before any store
	// write, so a refused pair leaves no task and no goal record behind.
	if vErr := task.ValidateDoDDistinct(tc.criteria, tc.dod); vErr != nil {
		return ErrorResult(fmt.Sprintf("task_create failed: %v", vErr)).WithError(vErr), true
	}
	// Founder decision 2026-09-15: refuse an assignee that cannot finish this
	// task — denied goal_claim, or a check its bash policy cannot run — before
	// anything is written (task_assignee_readiness.go).
	if res := assigneeCannotFinishResult("create_task", tc.t.assigneeCannotFinish, tc.agentID,
		append(append([]task.AcceptanceCriterion{}, tc.criteria...), tc.dod...)); res != nil {
		return res, true
	}
	return nil, false
}

// buildTask resolves the workspace and builds the task with channel, dependency, plan, and parallel-work metadata.
func (tc *taskCreateToolExecute) buildTask() (*ToolResult, bool) {
	parentTaskID, _ := tc.args["parent_task_id"].(string)

	wsID, err := tc.t.resolveWorkspaceID(tc.ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not resolve workspace: %v", err)), true
	}

	// A delegated task is ready to be picked up by the executor: it lands in
	// `next` (triaged & dispatchable) rather than `inbox`. Detail #6: it carries
	// a parent link and the originating channel for result delivery.
	tc.entity = &task.Task{
		Title:           tc.title,
		Prompt:          tc.prompt,
		Action:          task.ActionLLM,
		AgentID:         tc.agentID,
		CreatedBy:       tc.callerID,
		Priority:        tc.priority,
		Due:             tc.due,
		ParentTaskID:    parentTaskID,
		WorkspaceID:     wsID,
		Status:          task.StatusNext,
		DelegationDepth: tc.childDepth,
		Criteria:        tc.criteria,
	}

	// Propagate the originating channel so completed tasks can route results back.
	if channel := ToolChannel(tc.ctx); channel != "" && channel != "webchat" {
		tc.entity.SourceChannel = channel
		tc.entity.SourceChatID = ToolChatID(tc.ctx)
	}

	// Optional blocked_by: mirror admin create. The store's Create validates the
	// blocked_by DAG (cycle/self-edge/missing/depth); the tool layer additionally
	// enforces the same-workspace constraint (the store does not), mirroring
	// TaskAddDependencyTool's cross-workspace guard.
	//
	// For create, provided-empty (CLEAR) and absent are equivalent — a brand-new
	// task starts with no deps either way — so only the populated path sets deps.
	deps, depsProvided := resolveBlockedBy(tc.args)
	if depsProvided && len(deps) > 0 {
		if wErr := validateBlockersWorkspace(tc.t.store, wsID, deps); wErr != nil {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", wErr)), true
		}
		tc.entity.BlockedBy = deps
	}

	// Optional plan_id (ADR-052 FR-002): same-workspace FK + draft-only
	// membership (ValidateTaskPlanMembership, plan.go — the one choke point
	// this tool shares with create_task_in_workspace and the REST surface;
	// see its doc for why an approved/running plan refuses a new member). A
	// create_task call with no plan_id is entirely unaffected — the check is
	// a no-op for planID == "".
	if planID, _ := tc.args["plan_id"].(string); planID != "" {
		if pErr := ValidateTaskPlanMembership(tc.t.planStore, planID, wsID); pErr != nil {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", pErr)), true
		}
		tc.entity.PlanID = planID
	}

	// Optional write_set/stream/is_join (ADR-053 §Contract Surface, US-11
	// G-16): meaningful only alongside plan_id, but accepted unconditionally
	// (ignored by plan-lint on a standalone task) — matching the wire
	// contract's own "meaningful only when plan_id is set" convention rather
	// than rejecting a caller who supplies them without a plan_id.
	if rawWriteSet, ok := tc.args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		tc.entity.WriteSet = writeSet
	}
	if stream, ok := tc.args["stream"].(string); ok {
		tc.entity.Stream = stream
	}
	if isJoin, ok := tc.args["is_join"].(bool); ok {
		tc.entity.IsJoin = isJoin
	}
	return nil, false
}

// persistAndRespond persists the task and paired goal, invokes the creation observer, and returns the created task identity.
func (tc *taskCreateToolExecute) persistAndRespond() *ToolResult {
	// CreateByAgent, not Create: this is the AGENT creation path, so the task
	// carries FR-037 provenance (Task.CreatedByAgentID = the calling agent) in
	// the agent-id namespace. That stamp is what makes the created task
	// findable by its author — list_jobs' dispatched half and list_tasks
	// role="delegator" both filter on it, and neither can use the
	// mixed-namespace CreatedBy. callerID is guaranteed non-empty by the
	// fail-closed guard at the top of Execute, so CreateByAgent's own
	// empty-agent-id rejection is belt-and-suspenders here, not the primary
	// gate.
	if err := tc.t.store.CreateByAgent(tc.entity, tc.callerID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
		}
		return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
	}

	// ADR-086 D2/D5, GOAL-FR-003/FR-012/FR-021/FR-029: a task's Definition of
	// Done is authored ahead of time onto its own paired goal record, which
	// stays in the "defining" phase until the task itself starts (GOAL-FR-012
	// — nothing here activates it). The task record itself was created above
	// with entity.Criteria already populated (dual-write, for the consumers
	// not yet re-pointed to read the goal record this round — see this
	// wave's report); this call is what actually persists dod anywhere.
	if gErr := syncTaskGoalRecord(tc.t.store, tc.entity, true, tc.dod, true, tc.t.goalMaxRoundsFn); gErr != nil {
		slog.Error("create_task: failed to create paired goal record",
			"task_id", tc.entity.ID, "error", gErr)
		return ErrorResult(fmt.Sprintf(
			"task_create failed: task %q was created but its Definition of Done could not be "+
				"persisted: %v", tc.entity.ID, gErr))
	}

	if tc.t.onCreate != nil {
		tc.t.onCreate(tc.entity)
	}

	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":%q}`, tc.entity.ID, tc.entity.Status))
}

// TaskUpdateTool allows an agent to update status of its own task.
type TaskUpdateTool struct {
	BaseTool
	store *task.Store
	// delegationDeny, when non-nil, applies the full delegation policy (trust
	// set + modes ("task") + depth — FR-6.2) to a reassignment. Returns a
	// non-nil *DelegationDenial to DENY or nil to ALLOW. This is the ONLY
	// delegation gate (ADR-037 retired the legacy boolean delegateCheck
	// fallback). Reassignment is re-delegation, so it routes through the
	// SAME gate task_create uses.
	delegationDeny func(ctx context.Context, targetAgentID string) *DelegationDenial
	onComplete     func(*task.Task)
	// goalMaxRoundsFn mirrors TaskCreateTool.goalMaxRoundsFn (wired by
	// pkg/agent/loop.go alongside it). It is consulted only when update_task must CREATE a goal
	// record that did not exist before (a legacy task getting criteria/dod
	// for the first time via an edit); updating an existing record never
	// touches the budget.
	goalMaxRoundsFn func() int
	// assigneeCannotFinish mirrors TaskCreateTool.assigneeCannotFinish (set via
	// SetAssigneeReadinessChecker).
	assigneeCannotFinish AssigneeReadinessChecker
}

func NewTaskUpdateTool(store *task.Store) *TaskUpdateTool {
	return &TaskUpdateTool{store: store}
}

// SetGoalMaxRoundsFn installs the live Settings -> Performance goal-round
// ceiling accessor — see TaskCreateTool.SetGoalMaxRoundsFn.
func (t *TaskUpdateTool) SetGoalMaxRoundsFn(fn func() int) {
	t.goalMaxRoundsFn = fn
}

// SetOnComplete sets the callback invoked when a task reaches a terminal status.
func (t *TaskUpdateTool) SetOnComplete(fn func(*task.Task)) {
	t.onComplete = fn
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2)
// for reassignment. Reassignment is re-delegation, so it routes through the
// SAME gate task_create uses.
func (t *TaskUpdateTool) SetDelegationDenyChecker(
	fn func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDeny = fn
}

// frozenUpdateTaskDefinitionFields is the update_task half of the
// running-task field freeze (operator decision, 2026-09-12). It is the exact
// counterpart of pkg/gateway/rest_tasks.go's frozenTaskDefinitionFields —
// read that function's doc comment for the full rule, the four frozen parts of
// the judged contract, and the reasoning for what deliberately stays mutable.
//
// The two entry points enforce the same rule over different field sets simply
// because they expose different fields. update_task's schema carries NO
// `prompt` and no `description` argument at all, so the only frozen-set fields
// reachable from here are `criteria` and `dod`. Nothing is enumerated for the
// fields this tool cannot touch: a guard for an argument that does not exist
// would be dead code, and adding a frozen argument to the schema later must
// carry adding it here in the same change.
//
// Presence is decided with the SAME type assertion the apply path below uses
// (`args[k].([]any)`), so the gate refuses exactly what would otherwise be
// written — never more. A `criteria` argument of the wrong JSON shape changes
// nothing downstream, so it is not something to refuse.
//
// `status` and every other progress field are untouched by this: an agent
// working the task must be able to advance its own status, its todos (a
// separate tool), its result and its artifacts while it runs.
func frozenUpdateTaskDefinitionFields(args map[string]any) []string {
	var frozen []string
	if _, ok := args["criteria"].([]any); ok {
		frozen = append(frozen, "criteria")
	}
	if _, ok := args["dod"].([]any); ok {
		frozen = append(frozen, "dod")
	}
	return frozen
}

// runningTaskFrozenFieldToolMessage is the tool-side refusal, deliberately
// word-for-word the REST handler's
// (pkg/gateway/rest_tasks.go::runningTaskFrozenFieldMessage) so an operator
// reading a 409 body and an agent reading a tool error are told the same
// thing. It states the HTTP status the REST surface answers with, because a
// tool result has no status line of its own and an agent that cannot tell a
// state conflict from a malformed argument will retry the same edit forever.
//
// pkg/tools cannot import pkg/gateway (the dependency runs the other way), so
// the sentence is duplicated rather than shared. Keeping the two copies
// identical is the point; if one is reworded, reword both.
func runningTaskFrozenFieldToolMessage(frozen []string) string {
	quoted := make([]string, 0, len(frozen))
	for _, n := range frozen {
		quoted = append(quoted, strconv.Quote(n))
	}
	var list string
	if len(quoted) == 1 {
		list = quoted[0]
	} else {
		list = strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
	return fmt.Sprintf(
		"409 Conflict: cannot change %s while the task is running: the goal definition, the "+
			"acceptance criteria, the definition of done and the prompt are frozen for the duration "+
			"of a run, because the Judge measures the finished work against exactly those — changing "+
			"one mid-run means neither a pass nor a fail would mean anything. Stop the task first, "+
			"then edit it. Progress fields (status, todos, result, artifacts) stay editable while it runs.",
		list,
	)
}

func (t *TaskUpdateTool) Name() string { return "update_task" }

func (t *TaskUpdateTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskUpdateTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskUpdateTool) Description() string {
	return "Update a task assigned to you or that you created: status, title, priority, due date, agent_id, or blocked_by.\n" +
		"Status: failed ends a task you are NOT currently running; done on a task with acceptance criteria " +
		"is refused outside its own run (the judge decides completion there), and while a task's own run is " +
		"in flight you cannot write its status at all — claim completion with the goal_claim tool instead " +
		"(status \"met\" with evidence, or \"blocked\" when you cannot proceed). Only provided fields are updated."
}

func (t *TaskUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the task to update",
			},
			"status": map[string]any{
				"type": "string",
				"enum": []string{"done", "failed"},
				"description": "New status for the task. in_progress is NOT settable here (issue #593) — " +
					"it is only ever reached through real dispatch; use run_task to actually start this task.",
			},
			"result": map[string]any{
				"type":        "string",
				"description": "Summary of what was accomplished (for done/failed)",
			},
			"artifacts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "File paths or URLs produced as artifacts",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "New title for the task (1-200 chars)",
			},
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest)",
			},
			"due": map[string]any{
				"type":        "string",
				"description": "Due date/time in RFC 3339 format",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to reassign the task to",
			},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of task IDs this task is blocked by. Pass the full list to REPLACE the existing deps; pass [] to CLEAR; omit to leave unchanged. Each blocker must exist + be same-workspace; cycles rejected.",
			},
			"write_set": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Replacement set of concrete paths this plan member creates/edits. Pass the full list to REPLACE; pass [] to CLEAR; omit to leave unchanged. Meaningful only alongside plan_id.",
			},
			"stream": map[string]any{
				"type":        "string",
				"description": "New parallel-group id this plan member belongs to. Pass \"\" to CLEAR; omit to leave unchanged.",
			},
			"is_join": map[string]any{
				"type":        "boolean",
				"description": "Set/clear whether this plan member is an authored join/assemble member.",
			},
			"criteria": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type":        "string",
							"enum":        []string{"check", "prose", "behavior"},
							"description": "See create_task's criteria.kind — same inference rules.",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "The criterion statement (1-1000 characters)",
						},
						"check": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"command":            map[string]any{"type": "string", "description": "Shell command to run"},
								"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
							},
							"description": "Required when kind is \"check\"; must be omitted for other kinds",
						},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Replacement acceptance-criteria set (replaces the current set atomically). " +
					"Pass at least one item — GOAL-FR-021/D-C binds at edit too: an update supplying an " +
					"empty criteria array is rejected. Omit to leave criteria unchanged. " +
					"FROZEN WHILE THE TASK IS RUNNING: once the task is in_progress this field cannot be " +
					"changed (the Judge scores the finished work against it), and supplying it is refused. " +
					"Your status, todos, result and artifacts stay editable throughout.",
			},
			"dod": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type":        "string",
							"enum":        []string{"check", "prose", "behavior"},
							"description": "See create_task's dod.kind — same inference rules.",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "The definition-of-done statement (1-1000 characters)",
						},
						"check": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"command":            map[string]any{"type": "string", "description": "Shell command to run"},
								"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
							},
							"description": "Required when kind is \"check\"; must be omitted for other kinds",
						},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Replacement Definition-of-Done set (replaces the current set atomically). " +
					"Pass at least one item — GOAL-FR-021/D-C binds at edit too: an update supplying an " +
					"empty dod array is rejected. Omit to leave dod unchanged. " +
					"FROZEN WHILE THE TASK IS RUNNING: same rule as criteria above.",
			},
		},
		"required": []string{"task_id"},
	}
}

// taskUpdateToolExecute carries the shared state of Execute across its stages.
type taskUpdateToolExecute struct {
	t                *TaskUpdateTool
	ctx              context.Context
	args             map[string]any
	taskID           string
	callerID         string
	existing         *task.Task
	err              error
	patch            task.Patch
	updatedFields    []string
	newStatus        task.Status
	newDoD           []task.AcceptanceCriterion
	criteriaProvided bool
	dodProvided      bool
	updated          *task.Task
	goalSyncWarning  string
}

func (t *TaskUpdateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	tu := &taskUpdateToolExecute{t: t, ctx: ctx, args: args}

	if r0, stop := tu.validateAndLoad(); stop {
		return r0
	}

	if r0, stop := tu.buildPatchFields(); stop {
		return r0
	}

	if r0, stop := tu.buildJudgedContract(); stop {
		return r0
	}

	if r0, stop := tu.persistUpdate(); stop {
		return r0
	}

	return tu.respond()
}

// validateAndLoad checks the request, loads the task, and enforces ownership and the running-task definition freeze.
func (tu *taskUpdateToolExecute) validateAndLoad() (*ToolResult, bool) {
	if tu.t.store == nil {
		return ErrorResult("update_task failed: task store is not available"), true
	}
	tu.taskID, _ = tu.args["task_id"].(string)
	tu.callerID = ToolAgentID(tu.ctx)
	if tu.callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership"), true
	}

	if tu.taskID == "" {
		return ErrorResult("task_id is required"), true
	}

	tu.existing, tu.err = tu.t.store.Get(tu.taskID)
	if tu.err != nil {
		if errors.Is(tu.err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", tu.taskID)), true
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", tu.err)), true
	}

	// CreatedByAgent, never CreatedBy. Task.CreatedBy is MIXED-NAMESPACE
	// (a human username on the REST path, an agent id on the tool path) and
	// its own doc comment on Task.CreatedBy / Task.CreatedByAgent forbids
	// using CreatedBy as an ownership predicate. CreatedByAgent reads the
	// agent-id-namespaced CreatedByAgentID and fails closed on both sides,
	// so a task's creator (the delegator) can update it even when it is
	// assigned to a different agent — the same union check delete_task's
	// gate applies. Reassignment of the assignee itself still routes
	// through the separate delegationDeny gate below.
	if tu.existing.AgentID != tu.callerID && !tu.existing.CreatedByAgent(tu.callerID) {
		return ErrorResult("you can only update tasks you own or are assigned"), true
	}

	// Operator decision, 2026-09-12: the judged contract freezes for the
	// duration of a run — the SAME rule the REST PATCH handler enforces, with
	// the SAME 409-Conflict semantics and the same wording (see
	// frozenUpdateTaskDefinitionFields for the field mapping and for why the
	// refusal is whole-request). Checked here, before a single field is copied
	// into the patch, so a call mixing frozen and mutable fields applies none
	// of it rather than half of it.
	if frozen := frozenUpdateTaskDefinitionFields(tu.args); len(frozen) > 0 && tu.existing.Status == task.StatusInProgress {
		return ErrorResult(runningTaskFrozenFieldToolMessage(frozen)), true
	}
	return nil, false
}

// buildPatchFields translates mutable status, result, metadata, dependency, and plan fields into the task patch.
func (tu *taskUpdateToolExecute) buildPatchFields() (*ToolResult, bool) {
	tu.patch = task.Patch{}
	tu.updatedFields = []string{}

	// Status (optional — was the only field historically; still the common path).

	statusStr, _ := tu.args["status"].(string)
	if statusStr != "" {
		st := task.Status(statusStr)
		if !task.IsValidStatus(st) {
			return ErrorResult(fmt.Sprintf("invalid status %q", statusStr)), true
		}
		// Issue #593 (Option A): in_progress is a DISPATCH state, not a
		// caller-settable status the way done/failed are. The only legitimate
		// writers are the executor's ClaimForRun, REST's handleTaskPatch,
		// run_task, and set_todos-via-Create — none of which call this tool.
		// Before this guard, ANY permitted caller — including the task's own
		// creator, who passes the ownership union check above — could force
		// next/inbox -> in_progress here with no session, no goroutine, and
		// nothing that will ever revisit it: the task then reads "running"
		// forever. Mirror the existing done-with-criteria guard's shape below
		// (reject outright, name the real path) rather than silently accept a
		// forged state. A resend on a task that is ALREADY in_progress is a
		// harmless no-op and is let through unchanged — only a transition INTO
		// in_progress from a different status is rejected.
		if st == task.StatusInProgress && tu.existing.Status != task.StatusInProgress {
			return ErrorResult("in_progress cannot be set directly — it is only ever reached through " +
				"real dispatch; call run_task to actually start this task"), true
		}
		tu.newStatus = st
		tu.updatedFields = append(tu.updatedFields, "status")
		// Founder decision 2026-09-14 (one claim mechanism): while THIS task's
		// own executor run is in flight (the context names it as the running
		// task), the worker cannot write any terminal status itself — done or
		// failed. Completion is claimed with goal_claim and decided by the
		// judge; an honest give-up is goal_claim(status:"blocked"). A worker
		// that could mark its own run failed would bypass the judge entirely.
		if ToolRunningTaskID(tu.ctx) == tu.taskID {
			return ErrorResult("you cannot set this task's status while it is running — its completion is " +
				"decided by the judge. Call goal_claim with status \"met\" and your one-line evidence when the " +
				"work is verified, or goal_claim with status \"blocked\" if you cannot proceed"), true
		}
		// Out-of-band done on a task with acceptance criteria: nothing would
		// ever adjudicate it — the judge runs inside the task's own run.
		if st == task.StatusDone && !tu.existing.Scratchpad && len(tu.existing.Criteria) > 0 {
			return ErrorResult("this task has acceptance criteria — completion is adjudicated by the judge " +
				"during a task run; it cannot be marked done here. Run the task and let its worker claim"), true
		}
		tu.patch.Status = &st
	}

	// Result / artifacts — accepted with or without a status.
	if result, ok := tu.args["result"].(string); ok && result != "" {
		tu.patch.Result = &result
		tu.updatedFields = append(tu.updatedFields, "result")
	}
	if rawArtifacts, ok := tu.args["artifacts"].([]any); ok {
		artifacts := make([]string, 0, len(rawArtifacts))
		for _, a := range rawArtifacts {
			if s, ok := a.(string); ok {
				artifacts = append(artifacts, s)
			}
		}
		tu.patch.Artifacts = &artifacts
		tu.updatedFields = append(tu.updatedFields, "artifacts")
	}

	// Title.
	if title, ok := tu.args["title"].(string); ok && title != "" {
		tu.patch.Title = &title
		tu.updatedFields = append(tu.updatedFields, "title")
	}

	// Priority (1-5). Shared range-check with create_task/create_task_in_
	// workspace/REST (task.ValidatePriority) — see its doc comment.
	if p, ok := tu.args["priority"].(float64); ok {
		pr := int(p)
		if pErr := task.ValidatePriority(pr); pErr != nil {
			return ErrorResult(pErr.Error()), true
		}
		tu.patch.Priority = &pr
		tu.updatedFields = append(tu.updatedFields, "priority")
	}

	// Due (RFC 3339 string).
	if due, ok := tu.args["due"].(string); ok && due != "" {
		if _, pErr := time.Parse(time.RFC3339, due); pErr != nil {
			return ErrorResult(fmt.Sprintf("invalid due date %q (must be RFC 3339): %v", due, pErr)), true
		}
		tu.patch.Due = &due
		tu.updatedFields = append(tu.updatedFields, "due")
	}

	// agent_id (reassign). Reassignment is re-delegation: when the new agent
	// differs from the current assignee, route it through the SAME delegation-
	// policy gate task_create uses (FR-6.2). A no-op reassign (same agent) needs
	// no gate. Mirrors TaskCreateTool.Execute's denial shape.
	if agentID, ok := tu.args["agent_id"].(string); ok && agentID != "" && agentID != tu.existing.AgentID {
		// FAIL CLOSED, not open, when no checker is wired — same rationale as
		// TaskCreateTool.Execute above: an unwired deny-checker is a
		// configuration error, never a permission grant. Do NOT "simplify"
		// this back to fail-open.
		if tu.t.delegationDeny != nil {
			if denial := tu.t.delegationDeny(tu.ctx, agentID); denial != nil {
				return DelegationDeniedResult("update_task", denial), true
			}
		} else {
			slog.Error("update_task: no delegation-deny checker installed — denying by default",
				"caller_id", tu.callerID, "target_agent_id", agentID)
			return DelegationDeniedResult("update_task", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: agentID,
			}), true
		}
		tu.patch.AgentID = &agentID
		tu.updatedFields = append(tu.updatedFields, "agent_id")
	}

	// blocked_by (replaces the list). Cross-workspace guard at the tool layer
	// (mirrors TaskAddDependencyTool's cross-workspace guard); the store's
	// validateBlockedByLocked handles cycle/self-edge/missing/depth atomically
	// under the per-task lock.
	//
	// Three-way: provided-empty CLEARs the list, populated REPLACEs it, absent
	// leaves it unchanged.
	deps, depsProvided := resolveBlockedBy(tu.args)
	if depsProvided {
		if len(deps) == 0 {
			// CLEAR — empty list trivially passes the cross-workspace guard.
			tu.patch.BlockedBy = &[]string{}
			tu.updatedFields = append(tu.updatedFields, "blocked_by")
		} else {
			if wErr := validateBlockersWorkspace(tu.t.store, tu.existing.WorkspaceID, deps); wErr != nil {
				return ErrorResult(fmt.Sprintf("task_update failed: %v", wErr)), true
			}
			tu.patch.BlockedBy = &deps
			tu.updatedFields = append(tu.updatedFields, "blocked_by")
		}
	}

	// write_set (ADR-053 §Contract Surface, US-11 G-16): three-way, mirroring
	// blocked_by — provided-empty CLEARs the declared write-set (reverts to
	// an exploratory member, D10), populated REPLACEs it, absent leaves it
	// unchanged.
	if rawWriteSet, ok := tu.args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		tu.patch.WriteSet = &writeSet
		tu.updatedFields = append(tu.updatedFields, "write_set")
	}

	// stream (empty string CLEARs the label; absent leaves it unchanged).
	if stream, ok := tu.args["stream"].(string); ok {
		tu.patch.Stream = &stream
		tu.updatedFields = append(tu.updatedFields, "stream")
	}

	// is_join (plain overwrite; absent leaves it unchanged).
	if isJoin, ok := tu.args["is_join"].(bool); ok {
		tu.patch.IsJoin = &isJoin
		tu.updatedFields = append(tu.updatedFields, "is_join")
	}
	return nil, false
}

// buildJudgedContract parses criteria and Definition of Done changes and validates the effective judged contract and assignee readiness.
func (tu *taskUpdateToolExecute) buildJudgedContract() (*ToolResult, bool) {
	// criteria / dod (GOAL-FR-021/FR-029/FR-030/D-C): the mandatory-count
	// gate binds at edit too, uniformly with create_task — an update
	// supplying either list must not reduce it below one item. Persisted to
	// the paired goal record (syncTaskGoalRecord, below, after the store
	// write succeeds); entity.Criteria is ALSO dual-written onto the task
	// record itself via patch.Criteria for the consumers not yet re-pointed
	// to read the goal record this round.
	var newCriteria []task.AcceptanceCriterion
	tu.criteriaProvided, tu.dodProvided = false, false
	if rawCriteria, ok := tu.args["criteria"].([]any); ok {
		tu.criteriaProvided = true
		if len(rawCriteria) == 0 {
			return ErrorResult("An update that changes the acceptance criteria must leave at least one."), true
		}
		parsed, cErr := parseCriteriaArgs(rawCriteria, tu.callerID)
		if cErr != nil {
			return ErrorResult(fmt.Sprintf("task_update failed: criteria: %v", cErr)), true
		}
		newCriteria = parsed
		tu.patch.Criteria = &parsed
		tu.updatedFields = append(tu.updatedFields, "criteria")
	}
	if rawDoD, ok := tu.args["dod"].([]any); ok {
		tu.dodProvided = true
		if len(rawDoD) == 0 {
			return ErrorResult("An update that changes the Definition of Done must leave at least one item."), true
		}
		parsed, dErr := parseCriteriaArgs(rawDoD, tu.callerID)
		if dErr != nil {
			return ErrorResult(fmt.Sprintf("task_update failed: dod: %v", dErr)), true
		}
		tu.newDoD = parsed
		tu.updatedFields = append(tu.updatedFields, "dod")
	}

	// GOAL-FR-048 binds the distinctness rule at SAVE, not only at create —
	// otherwise the rule is a door you walk around: create with a distinct DoD,
	// then edit it into a duplicate.
	//
	// An edit may touch one list and not the other, so the comparison is
	// against the EFFECTIVE post-edit pair: the submitted list where one was
	// submitted, the persisted list otherwise. The persisted criteria come off
	// the task record; the persisted DoD can only come off the paired goal
	// record, which is the only place a task's DoD exists (ADR-086 D5).
	// Checked before store.Update, so a refusal writes nothing at all.
	if tu.criteriaProvided || tu.dodProvided {
		effectiveCriteria := newCriteria
		if !tu.criteriaProvided {
			effectiveCriteria = tu.existing.Criteria
		}
		effectiveDoD := tu.newDoD
		if !tu.dodProvided {
			persistedDoD, dErr := pairedGoalDoD(tu.t.store, tu.taskID)
			if dErr != nil {
				return ErrorResult(fmt.Sprintf(
					"task_update failed: could not read the task's Definition of Done to check it "+
						"stays distinct from the criteria: %v", dErr)), true
			}
			effectiveDoD = persistedDoD
		}
		if vErr := task.ValidateDoDDistinct(effectiveCriteria, effectiveDoD); vErr != nil {
			return ErrorResult(fmt.Sprintf("task_update failed: %v", vErr)).WithError(vErr), true
		}
	}

	// Founder decision 2026-09-15: a reassignment, or a change to what the task
	// is judged against, may not leave it with an agent that cannot finish it.
	// Checked before store.Update, so a refusal writes nothing.
	if res := tu.t.updateAssigneeCannotFinish(tu.existing, tu.patch.AgentID,
		newCriteria, tu.newDoD, tu.criteriaProvided, tu.dodProvided); res != nil {
		return res, true
	}
	return nil, false
}

// persistUpdate requires a changed field, timestamps and persists the patch, then synchronizes and terminates the paired goal when needed.
func (tu *taskUpdateToolExecute) persistUpdate() (*ToolResult, bool) {
	if len(tu.updatedFields) == 0 {
		return ErrorResult(
			"no updatable fields provided (supply at least one of status, result, artifacts, title, priority, due, agent_id, blocked_by, write_set, stream, is_join)",
		), true
	}

	// Timestamps keyed off status.
	now := time.Now().UTC().Format(time.RFC3339)
	switch tu.newStatus {
	case task.StatusInProgress:
		tu.patch.StartedAt = &now
	case task.StatusDone, task.StatusFailed:
		tu.patch.CompletedAt = &now
	}

	tu.updated, tu.err = tu.t.store.Update(tu.taskID, tu.patch)
	if tu.err != nil {
		// WithError carries the store sentinel (task.ErrBlockedByCycle,
		// ErrValidation, ...) so callers and tests assert identity rather
		// than the plain-language message wording.
		return ErrorResult(fmt.Sprintf("task_update failed: %v", tu.err)).WithError(tu.err), true
	}

	// GOAL-FR-029/FR-030: the task record's write already landed above
	// (dual-write); this is what actually persists the change onto the
	// task's paired goal record — creating one if this is a legacy task's
	// first-ever criteria/dod (see syncTaskGoalRecord's doc comment).

	if tu.criteriaProvided || tu.dodProvided {
		if gErr := syncTaskGoalRecord(tu.t.store, tu.updated, tu.criteriaProvided, tu.newDoD, tu.dodProvided, tu.t.goalMaxRoundsFn); gErr != nil {
			slog.Error("update_task: failed to sync paired goal record",
				"task_id", tu.taskID, "error", gErr)
			tu.goalSyncWarning = gErr.Error()
		}
	}

	// GOAL-FR-015/FR-027/FR-028 (review finding C1): this tool is one of the
	// seven terminal task writers, and it was one of the four with no goal
	// hook at all. The done-claim judge deferral above only intercepts `done`
	// — status:"failed" was written straight through, leaving the paired goal
	// record ACTIVE forever and silently killing Goal.Reactivate on every
	// subsequent re-run of that task (activateTaskGoal's `default:` branch).
	//
	// Keyed on `updated.Status` (what actually landed on disk) rather than on
	// the requested `newStatus`. The prior-
	// status guard keeps a no-op resend on an already-terminal task from
	// re-entering the hook; the hook is idempotent anyway, this just keeps the
	// logs honest.
	if task.IsTerminal(tu.updated.Status) && !task.IsTerminal(tu.existing.Status) {
		TerminateTaskGoalRecord(
			GoalStoreForTasks(tu.t.store), tu.taskID, tu.updated.Status, tu.updated.CancelReason, tu.updated.Result)
	}
	return nil, false
}

// respond advances dependents, invokes completion observers, and returns the encoded update result with any warnings.
func (tu *taskUpdateToolExecute) respond() *ToolResult {
	// FR-6.5: when the task newly reaches "done", advance dependents (mirror
	// admin: pkg/sysagent/tools/task.go:252-259). The primary update already
	// persisted, so this is best-effort: a storage fault here is surfaced to the
	// caller as an advance_warning rather than turning a successful update into a
	// failure (which would orphan dependents with no signal either way).
	//
	// A done write only reaches here for a task with no acceptance criteria,
	// from outside that task's own run: a status write on the caller's own
	// running task, and a done write on a criteria task, are both refused
	// above, because only a Judge-upheld claim may complete those (founder
	// decision 2026-09-14, pkg/agent/task_run_loop.go).
	var advanceWarning string
	if tu.newStatus == task.StatusDone {
		advanced, advErr := tu.t.store.AdvanceBlockedDependents(tu.taskID)
		if advErr != nil {
			// Storage fault advancing dependents — the update itself succeeded,
			// so this stays a success, but the warning must reach the LLM/user.
			slog.Error("update_task: advance dependents failed",
				"id", tu.taskID, "error", advErr)
			advanceWarning = advErr.Error()
		} else if len(advanced) > 0 {
			slog.Info("update_task: completed task advanced dependents",
				"completed_id", tu.taskID, "advanced_ids", advanced)
		}
	}

	if task.IsTerminal(tu.newStatus) && tu.t.onComplete != nil {
		tu.t.onComplete(tu.updated)
	}

	// Marshal cannot fail on a []string (updatedFields is always a concrete
	// slice of strings), so the error is impossible in practice — discard it.
	resultPayload := map[string]any{
		"task_id":        tu.updated.ID,
		"status":         tu.updated.Status,
		"updated_fields": tu.updatedFields,
	}
	if advanceWarning != "" {
		resultPayload["advance_warning"] = advanceWarning
	}
	if tu.goalSyncWarning != "" {
		// GOAL-FR-029/FR-030: the task record itself already carries the new
		// criteria (dual-write, via patch.Criteria above), so this is never a
		// silent data loss — but the paired goal record (the authoritative
		// store per ADR-086 D5) failed to pick up the change, so the caller
		// must see that explicitly rather than assume both landed together.
		resultPayload["goal_sync_warning"] = "criteria/dod saved on the task, but the paired goal " +
			"record could not be updated: " + tu.goalSyncWarning
	}
	encoded, mErr := json.Marshal(resultPayload)
	if mErr != nil {
		// Every field above is a concrete string/slice — Marshal cannot
		// fail on this shape in practice; fall back to the minimal payload
		// rather than dropping a successful update's response entirely.
		return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":%q}`, tu.updated.ID, tu.updated.Status))
	}
	return NewToolResult(string(encoded))
}

// --- TaskDeleteTool ---

type TaskDeleteTool struct {
	BaseTool
	store *task.Store
}

func NewTaskDeleteTool(store *task.Store) *TaskDeleteTool {
	return &TaskDeleteTool{store: store}
}

func (t *TaskDeleteTool) Name() string { return "delete_task" }

func (t *TaskDeleteTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskDeleteTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskDeleteTool) Description() string {
	return "Permanently delete a to-do/task item by task_id. Only use when explicitly asked to remove a " +
		"task. You may only delete a task you own — one you created or are assigned to; a task created " +
		"or assigned to someone else is refused, with no delegation override on this path."
}

func (t *TaskDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "ID of the task to delete"},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskDeleteTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("delete_task failed: task store is not available")
	}
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return ErrorResult("task_id is required")
	}

	callerID := strings.TrimSpace(ToolAgentID(ctx))
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	// Ownership gate: load the task first to verify the caller owns it.
	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}
	// CreatedByAgent, never CreatedBy. Task.CreatedBy is MIXED-NAMESPACE and its
	// own doc comment states the rule this predicate used to break: it "MUST
	// NEVER be used as an ownership or authorization predicate", because the
	// REST path writes a human USERNAME into it (pkg/gateway/rest_tasks.go)
	// while callerID is an agent id. A human who registers the username `jim`
	// would otherwise have every task they created become deletable by the
	// agent `jim` — and the base roster ids (mia/jim/ava/ray) are all plausible
	// usernames. CreatedByAgent reads the agent-id-namespaced CreatedByAgentID
	// and fails closed on BOTH sides, so "" is never a wildcard in either
	// direction.
	//
	// This does NOT narrow the legitimate case: this file's own create_task
	// persists through Store.CreateByAgent, which stamps CreatedByAgentID with
	// the same callerID, so an agent can still delete what it created. What it
	// drops is deletion of REST/human-created tasks, which carry no agent
	// attribution at all and were never this caller's to delete.
	if existing.AgentID != callerID && !existing.CreatedByAgent(callerID) {
		return ErrorResult("you can only modify/delete tasks you own or are assigned")
	}

	// GOAL-FR-044/EC-4: a goal MUST NOT outlive its owner as an unreferenced
	// record. Run BEFORE the task file is removed and treat a failure as fatal
	// to the whole delete — see RemoveTaskGoalRecords' doc for why this
	// ordering replaced the best-effort-afterwards shape all three delete
	// surfaces used to share (and the three different answers they gave when
	// it failed). Nothing has been deleted yet at this point, so refusing here
	// leaves the task and its goal exactly as they were, and a retry is safe.
	if gErr := RemoveTaskGoalRecords(GoalStoreForTasks(t.store), taskID); gErr != nil {
		slog.Error("delete_task: refusing to delete a task whose paired goal record could not be "+
			"removed (GOAL-FR-044) — nothing was deleted", "task_id", taskID, "error", gErr)
		return ErrorResult(fmt.Sprintf(
			"task %q was NOT deleted: its paired goal record could not be removed, and deleting the "+
				"task anyway would leave that record behind as an unreferenced orphan: %v", taskID, gErr))
	}

	unblocked, err := t.store.Delete(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		if !errors.Is(err, task.ErrCascadeEdgeCleanupFailed) {
			return ErrorResult(fmt.Sprintf("could not delete task: %v", err))
		}
		// The task itself was deleted; only cleaning up OTHER tasks' dangling
		// blocked_by edges partially failed. Non-fatal — log and continue
		// reporting success for the primary delete, matching how this file's
		// update_task path already treats AdvanceBlockedDependents's
		// write-failure error as a logged, non-fatal side effect.
		slog.Warn("delete_task: cascade edge cleanup partially failed", "deleted_id", taskID, "error", err)
	}

	// Advance any dependents that became fully unblocked by this delete.
	// The cascade only rewrote the blocked_by edges; a task that was `blocked`
	// with this task as its only blocker must be moved to `next`. AdvanceUnblocked
	// is a no-op when the dependent is not `blocked` and uses the internal hatch
	// so the transition guard does not reject blocked→next.
	for _, depID := range unblocked {
		if _, uErr := t.store.AdvanceUnblocked(depID); uErr != nil {
			slog.Warn(
				"delete_task: advance unblocked dependent failed",
				"deleted_id",
				taskID,
				"dependent_id",
				depID,
				"error",
				uErr,
			)
			continue
		}
		slog.Info("delete_task: advanced unblocked dependent blocked→next", "deleted_id", taskID, "advanced_id", depID)
	}

	// No goal_cleanup_warning field any more: the cleanup ran before the
	// delete and a failure returned above, so reaching here means both the
	// goal record and the task are gone.
	return NewToolResult(fmt.Sprintf(`{"deleted":%q}`, taskID))
}
