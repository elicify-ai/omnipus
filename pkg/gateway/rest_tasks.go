// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_tasks.go — the unified /api/v1/tasks REST surface (Sprint 2). One store
// (pkg/task), one wire schema (gen.Task), one create/update path. It folds in
// the legacy /board/tasks and workflow-task handlers: GET/POST /tasks,
// GET/PATCH/DELETE /tasks/{id}, GET /tasks/{id}/subtasks,
// PUT /tasks/{id}/todos, PUT /tasks/{id}/dependencies.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// This file has NO goal-store derivation and NO task-delete goal cleanup of
// its own. It calls tools.GoalStoreForTasks and tools.RemoveTaskGoalRecords
// (pkg/tools/task.go) — the single implementations shared with the create_task
// / delete_task agent tools and the System Agent's workspace task tools. The
// private copies that used to live here (goalStoreForTasks,
// goalTerminalReasonOwnerDeleted, terminateGoalForOwnerDeletion,
// removeTaskGoalRecords) were byte-identical mirrors and are deleted; mirroring
// is the shape that let the sibling task-terminal hook reach only three of its
// seven writers. pkg/tools/task_goal_delete_guard_test.go keeps the delete-side
// call-site set closed.

// errGoalRecordNeedsBothLists marks the one syncTaskGoalRecord failure that is
// the CALLER's fault rather than a storage fault: bootstrapping a paired goal
// record for a task that never had one requires both criteria and dod, so a
// request that supplies only one of them is a 400, not a 500. Every other
// failure out of syncTaskGoalRecord is a server-side write/read problem.
var errGoalRecordNeedsBothLists = errors.New("this task doesn't have a Definition of Done yet — the " +
	"first time you set one, you need to supply both the acceptance criteria and the " +
	"definition-of-done items together")

// frozenTaskDefinitionFields reports which fields of a PATCH body belong to the
// JUDGED CONTRACT — the definition the Judge measures the finished work
// against — and are therefore frozen for as long as the task is running
// (operator decision, 2026-09-12).
//
// The rule is FIELD-LEVEL, never a blanket lock on a running task. Changing
// the target mid-run means neither a pass nor a fail from the Judge means
// anything: the work was done against one target and scored against another.
// That, and only that, is what this exists to prevent.
//
// FROZEN — the four named parts of the contract, mapped onto the wire fields
// that actually carry them on a task:
//
//   - the goal definition — carried by `prompt`: syncTaskGoalRecord builds the
//     paired goal record's own Prompt from Task.Prompt (ADR-086 D2/D5), so on
//     a task the goal definition and the instructions are the same string.
//   - the acceptance criteria — `criteria`.
//   - the definition of done — `dod`.
//   - the instructions — `prompt` again, by the wire schema's own words
//     ("the instruction handed to the assigned agent when the task runs").
//
// STILL MUTABLE — everything else, because it is the RECORD OF PROGRESS, not
// the target: `status` and `todos` above all (the working agent writes both as
// it goes and MUST be able to), plus `result`, `artifacts`, `started_at`,
// `completed_at`, `priority`, `due`, `tags`, `agent_id`, `plan_id`,
// `blocked_by`, `write_set`, `stream`, `is_join`, `surface`, `max_attempts`
// and `trigger`. Over-freezing breaks the agent mid-run, which is strictly
// worse than under-freezing, so an unclassifiable field stays mutable.
//
// DELIBERATELY LEFT MUTABLE, and reported rather than frozen: `title` and
// `description`. The wire schema calls them "the name field" and "human-facing
// notes" — bookkeeping, on the mutable side of the operator's line. They are
// NOT purely cosmetic in one narrow case: when a task has an empty `prompt`,
// pkg/agent's SoftTierCriterion falls back to title (+ description) to build
// the single prose criterion the Judge scores. Freezing them outright would
// block renaming any running task, which is the over-freezing the decision
// warns against; freezing them only-when-prompt-is-empty would be a rule no
// interface could state plainly. They stay mutable and the residual hole is
// reported upward instead.
//
// The engine's own writes are NOT operator edits and never reach this check:
// attempt counts, verdicts, run records and the verdict->criterion-status
// projection all go through task.Store.Update from pkg/agent directly, not
// through this handler or the update_task tool. Stopping or cancelling a
// running task is likewise not an edit — POST /tasks/{id}/stop and a plain
// `status` change are untouched by this rule.
//
// Returns the frozen field names present in the request, in a stable order, so
// the refusal can name every one of them rather than just the first. An empty
// result means the request touches nothing frozen and may proceed even while
// the task runs.
func frozenTaskDefinitionFields(req *gen.TaskUpdateRequest) []string {
	if req == nil {
		return nil
	}
	var frozen []string
	if req.Prompt != nil {
		frozen = append(frozen, "prompt")
	}
	if req.Criteria != nil {
		frozen = append(frozen, "criteria")
	}
	if req.Dod != nil {
		frozen = append(frozen, "dod")
	}
	return frozen
}

// runningTaskFrozenFieldMessage is the single refusal wording shared by both
// entry points that can change a task's definition — this REST PATCH handler
// and the `update_task` tool (pkg/tools/task.go, which builds the identical
// sentence from its own copy of the field list). Both answer with the same
// 409 Conflict semantics: the request is well formed and the field is legal,
// but the resource is in a state that forbids this particular change.
//
// It names WHICH field was refused and says the task is running, because a
// generic failure leaves the caller — human or agent — with no way to tell a
// frozen field from a malformed one, and an agent that cannot tell will retry
// the same edit forever.
func runningTaskFrozenFieldMessage(frozen []string) string {
	return fmt.Sprintf(
		"cannot change %s while the task is running: the goal definition, the acceptance criteria, "+
			"the definition of done and the prompt are frozen for the duration of a run, because the "+
			"Judge measures the finished work against exactly those — changing one mid-run means "+
			"neither a pass nor a fail would mean anything. Stop the task first, then edit it. "+
			"Progress fields (status, todos, result, artifacts) stay editable while it runs.",
		quoteFieldList(frozen),
	)
}

// quoteFieldList renders a field-name list as `"a", "b" and "c"` for the
// refusal sentence above.
func quoteFieldList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, strconv.Quote(n))
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}

// syncTaskGoalRecord creates or updates the goal record paired with task t
// (ADR-086 D2/D5, GOAL-FR-003/FR-012/FR-021/FR-029). Mirrors
// pkg/tools/task.go's syncTaskGoalRecord exactly — see its doc comment for
// the full contract (create-or-update, both-lists-required when bootstrapping
// a record for a legacy task that never had one). This copy reads the live
// Settings -> Performance goal-round ceiling directly via
// a.agentLoop.GetConfig() rather than through an optional injected accessor,
// since the gateway (unlike the tool constructors wired in pkg/agent/loop.go,
// outside this wave's write-set) always has it.
//
// THE CRITERIA COME FROM t, NEVER FROM THE CALLER. This signature used to take
// a `criteria []task.AcceptanceCriterion` alongside criteriaProvided, and every
// one of its call sites handed it the PRE-normalisation slice it had just given
// to the task store — the one whose criteria still carry empty ids. The store
// mints ids into its own deep copy (task.normalizeCriteria), and goal.New /
// Goal.SetCriteria then mint a SECOND, different set for the same text. The
// criterion id is the join key the verdict projection de-unions the Judge's
// result on (GOAL-FR-007/FR-041), so the same criterion ended up reading `met`
// on the task and `pending` on its goal record: two contradictory answers to
// "was this met". Taking the list off t — the record the store has already
// normalised and persisted — makes that divergence unrepresentable rather than
// merely fixed at three call sites. criteriaProvided remains a parameter
// because it carries something t cannot: whether THIS request touched criteria
// at all (an edit that supplies only `dod` must leave the goal's criteria
// alone).
func (a *restAPI) syncTaskGoalRecord(
	t *task.Task,
	criteriaProvided bool,
	dod []task.AcceptanceCriterion, dodProvided bool,
) error {
	if !criteriaProvided && !dodProvided {
		return nil
	}
	criteria := t.Criteria
	gs := tools.GoalStoreForTasks(a.taskStore)
	now := time.Now().UTC()
	existing, err := gs.GetByOwner(gen.GoalOwnerKindTask, t.ID)
	if err != nil {
		if !errors.Is(err, goal.ErrOwnerNotFound) {
			return fmt.Errorf("load paired goal record: %w", err)
		}
		if !criteriaProvided || !dodProvided {
			return errGoalRecordNeedsBothLists
		}
		maxRounds := config.DefaultGoalMaxRounds
		if a.agentLoop != nil {
			if cfg := a.agentLoop.GetConfig(); cfg != nil {
				maxRounds = cfg.Planning.EffectiveGoalMaxRounds()
			}
		}
		// goal.New requires a non-empty Prompt ("the raw user intent this
		// goal was set/compiled from"); Task.Prompt is optional on the wire
		// (an llm-action task may carry only a title, e.g. a human-tracking
		// task later assigned an agent), so fall back to Title, which the
		// wire schema always requires.
		goalPrompt := t.Prompt
		if goalPrompt == "" {
			goalPrompt = t.Title
		}
		g, nErr := goal.New(
			gen.GoalOwnerKindTask, t.ID, gen.GoalSourceTaskExplicit,
			goalPrompt, "", criteria, dod, maxRounds, now,
		)
		if nErr != nil {
			return fmt.Errorf("build goal record: %w", nErr)
		}
		return gs.Create(g)
	}
	_, err = gs.Update(existing.GoalID, func(g *goal.Goal) error {
		if criteriaProvided {
			if sErr := g.SetCriteria(criteria, now); sErr != nil {
				return sErr
			}
		}
		if dodProvided {
			if sErr := g.SetDoD(dod, now); sErr != nil {
				return sErr
			}
		}
		return nil
	})
	return err
}

// pairedGoalDoD returns the Definition of Done currently persisted on the goal
// record paired with taskID, or nil when the task has no paired record at all
// (a legacy pre-D-C task, GOAL-FR-023/FR-048).
//
// It exists for the one edit shape that cannot evaluate the distinctness rule
// (GOAL-FR-021/FR-047/FR-048) from the request alone: a PATCH that replaces
// `criteria` and leaves `dod` untouched has to compare the NEW criteria against
// the DoD already on file, and the task record has no DoD field to read it from
// (ADR-086 D5 — pkg/task/task.go has no Dod at all).
//
// A read fault is returned, never swallowed: it means the rule could not be
// evaluated, and a rule that silently does not run is exactly the failure this
// whole change exists to stop.
func (a *restAPI) pairedGoalDoD(taskID string) ([]task.AcceptanceCriterion, error) {
	g, err := tools.GoalStoreForTasks(a.taskStore).GetByOwner(gen.GoalOwnerKindTask, taskID)
	if err != nil {
		if errors.Is(err, goal.ErrOwnerNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load paired goal record: %w", err)
	}
	return g.DoD, nil
}

// decodeTaskJSONBody decodes a JSON request body into dst, writing a 400 and
// returning false on a malformed body.
func decodeTaskJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// HandleTasks dispatches every request under /api/v1/tasks and /api/v1/tasks/.
//
//	GET    /tasks                     list (workspace_id/status/agent_id/surface/parent_task_id/limit/offset)
//	POST   /tasks                     create (201, lands in inbox)
//	GET    /tasks/{id}                get one
//	PATCH  /tasks/{id}                partial update
//	DELETE /tasks/{id}                delete (204)
//	GET    /tasks/{id}/subtasks       list children
//	PUT    /tasks/{id}/todos          replace the embedded checklist
//	PUT    /tasks/{id}/dependencies   replace the blocked_by set
//	GET    /tasks/{id}/evidence       list judge-check evidence (ADR-049 D2)
//	GET    /tasks/{id}/verdicts       list judge verdicts (ADR-049 D2)
//	POST   /tasks/{id}/stop           stop the task's running goal-loop (ADR-049)
//	POST   /tasks/{id}/restart        restart a user-stopped standalone task (ADR-052 FR-026)
//	GET    /tasks/{id}/runs           list per-execution run history (ADR-050 RD8)
//	POST   /tasks/{id}/runs           Run-now — task-level or per-occurrence (ADR-050 RD7)
func (a *restAPI) HandleTasks(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/tasks")
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			a.handleTaskList(w, r)
		case http.MethodPost:
			a.handleTaskCreate(w, r)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Sub-resources: /{id}/subtasks, /{id}/todos, /{id}/dependencies.
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		id := rest[:idx]
		sub := rest[idx+1:]
		switch sub {
		case "subtasks":
			if r.Method != http.MethodGet {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleTaskSubtasks(w, id)
		case "todos":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleTaskTodos(w, r, id)
		case "dependencies":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleTaskDependencies(w, r, id)
		case "evidence":
			if r.Method != http.MethodGet {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleTaskEvidence(w, id)
		case "verdicts":
			if r.Method != http.MethodGet {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleTaskVerdicts(w, id)
		case "stop":
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleTaskStop(w, r, id)
		case "restart":
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleTaskRestart(w, id)
		case "runs":
			// ADR-050 RD7/RD8 (rest_task_runs.go's own doc comment): the
			// dedicated taskReadLimiter (240/min) is applied HERE, at the
			// dispatch point, since HandleTasks itself carries no rate
			// limiter and handleTaskRuns takes the extra `id` argument
			// withRateLimit's http.HandlerFunc signature does not.
			withRateLimit(taskReadLimiter, func(w http.ResponseWriter, r *http.Request) {
				a.handleTaskRuns(w, r, id)
			})(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}

	// /{id}
	id := rest
	switch r.Method {
	case http.MethodGet:
		a.handleTaskGet(w, id)
	case http.MethodPatch:
		a.handleTaskPatch(w, r, id)
	case http.MethodDelete:
		a.handleTaskDelete(w, id)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// HandleTaskOccurrences handles GET /api/v1/tasks/occurrences — the Calendar
// Recurrence Redesign occurrence expansion endpoint (spec "Occurrence
// expansion endpoint", FR-008/FR-008a, contracts/openapi.yaml operationId
// listTaskOccurrences). Registered as an EXACT path in
// registerAdditionalEndpoints (rest.go), which always wins over this file's
// "/api/v1/tasks/" prefix route regardless of registration order (see that
// registration's comment) — so this handler never needs to branch on a
// trailing "occurrences" segment itself.
//
// Query params (all required): workspace_id, from_ms, to_ms, tz. The actual
// expansion/bucketing work is the pure, separately-unit-tested
// buildOccurrenceSets (task_occurrences.go); this handler only does
// param validation, the task-selection predicate, and status-code mapping.
func (a *restAPI) HandleTaskOccurrences(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()

	workspaceID := q.Get("workspace_id")
	if workspaceID == "" {
		jsonErr(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}

	fromMs, fErr := strconv.ParseInt(q.Get("from_ms"), 10, 64)
	if fErr != nil {
		jsonErr(w, http.StatusBadRequest, "from_ms is required and must be an integer (Unix epoch milliseconds)")
		return
	}
	toMs, tErr := strconv.ParseInt(q.Get("to_ms"), 10, 64)
	if tErr != nil {
		jsonErr(w, http.StatusBadRequest, "to_ms is required and must be an integer (Unix epoch milliseconds)")
		return
	}
	// from_ms >= to_ms (including from == to) is a 400: an empty half-open
	// range is a client bug, not a valid query returning [].
	if fromMs >= toMs {
		jsonErr(w, http.StatusBadRequest, "from_ms must be strictly before to_ms")
		return
	}
	if toMs-fromMs > maxOccurrenceRangeSpanMs {
		jsonErr(w, http.StatusBadRequest, "requested range exceeds the 400-day maximum span")
		return
	}

	tz := q.Get("tz")
	if tz == "" {
		jsonErr(w, http.StatusBadRequest, "tz is required")
		return
	}
	if _, tzErr := time.LoadLocation(tz); tzErr != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("tz %q could not be loaded: %v", tz, tzErr))
		return
	}

	tasks, err := a.taskStore.List(task.Filter{WorkspaceID: workspaceID})
	if err != nil {
		slog.Error("rest: task occurrences list failed", "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list tasks")
		return
	}

	// Task selection (spec "Occurrence expansion endpoint"): expand only
	// tasks the scheduler would actually arm — the SAME predicate
	// TaskTriggerScheduler.OnTaskUpserted applies before registering a job
	// (pkg/agent/task_trigger.go OnTaskUpserted's early skips). Heartbeat
	// -surface tasks are always omitted (the heartbeat service owns those
	// fires). A terminal task is omitted UNLESS its trigger REPEATS
	// (recurring/every): a per-run done/failed status does not end a
	// repeating series (see OnTaskUpserted's doc comment) — the scheduler
	// re-arms a terminal recurring/every task's next occurrence exactly as it
	// would a non-terminal one, so the calendar must keep rendering it too.
	// A truly exhausted RRULE series (COUNT/UNTIL) naturally yields zero
	// occurrences from buildOccurrenceSets below and is omitted that way, not
	// by this predicate. A terminal `once`/manual task is still omitted here
	// (task.Trigger.IsRepeating is false for them) — its single occurrence IS
	// its whole series — and would be omitted a second time regardless by
	// buildOccurrenceSets' own trigger-FLAVOR filter (recurring/every only).
	eligible := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.EffectiveSurface() == task.SurfaceHeartbeat {
			continue
		}
		if t.SeriesRetired() {
			continue
		}
		eligible = append(eligible, t)
	}

	// FR-008a: the every_ms projection anchor is the live armed job's
	// NextRunAtMS, read from the installed TaskTriggerScheduler. Nil-safe —
	// a nil scheduler (not yet wired / test scaffolding) makes every
	// `every`-triggered task omit cleanly rather than erroring the request.
	sched := agent.GetTaskTriggerScheduler(a.agentLoop)
	everyAnchor := func(taskID string) (int64, bool) {
		if sched == nil {
			return 0, false
		}
		return sched.NextRunAtMSForTask(taskID)
	}

	// ADR-050 RD6, task-run-history-spec.md §3.7: the occurrence-run overlay
	// dependency is wired in here, sourced from the live store. Unlike
	// everyAnchor above — a required, non-variadic func(taskID string)
	// (int64, bool) parameter — runsInRange is buildOccurrenceSets' own
	// trailing VARIADIC parameter (see its doc comment, task_occurrences.go);
	// that is what lets this single positional call add the dependency
	// without breaking the pre-existing task_occurrences_test.go call sites
	// that predate this feature and pass none.
	sets, err := buildOccurrenceSets(eligible, fromMs, toMs, tz, everyAnchor, a.taskStore.RunsInRange)
	if err != nil {
		// Range/tz were already validated above, so a non-nil error here
		// indicates a genuine internal failure rather than bad input.
		slog.Error("rest: task occurrences expansion failed", "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not expand occurrences")
		return
	}
	jsonOK(w, sets)
}

// LiveTaskActivityReader is the narrow gateway-side seam for a running
// task's live last-activity stamp (founder decision 2026-09-14). Implemented
// by *agent.TaskExecutor (forwarding to the AgentLoop's turn-progress
// atomics); kept as a local interface so tests wire a stub without
// constructing an executor.
type LiveTaskActivityReader interface {
	LiveTaskLastActivity(taskID string) (time.Time, bool)
}

// validateTaskAgentID checks that a human-assigned agent_id exists in the
// registry and is a member of the task's workspace TEAM. A subagent_3p
// (external-CLI) worker is no longer rejected here: AgentLoop.processTaskDirect
// (pkg/agent/loop.go) now branches on runner.ResolveDispatch the same way
// spawnSubTurn does for agent-to-agent delegation, dispatching an
// external-CLI worker's task run through runExternalCLISubTurn instead of
// silently falling into the native engine — see processTaskDirect's doc
// comment for the dispatch design.
//
// This is the human/REST-surface assignment path (SPA task create/edit): a
// human assigning a task via the SPA is the workspace owner directing work,
// not one agent delegating to another. It is therefore deliberately NOT
// routed through the agent-to-agent delegation-deny checker
// (buildDelegationDenyChecker / NewSysagentDelegationDeny in
// pkg/agent/loop.go) — that graph governs delegation ACTS between agents, not
// a human's direct task assignment. The authority here is workspace TEAM
// membership instead: the union of the workspace's core_team and the
// endpoints of its stored delegation edges (workspace.TeamSet, via the
// workspaceTeamSet adapter — the same set the Team tab and the delegation
// graph PUT validate edge endpoints against; see
// rest_workspace_delegation.go). An agent absent from that set — worker or
// not — cannot be assigned a task in this workspace. A worker that IS a team
// member CAN be assigned directly (native or subagent_3p alike).
//
// The two guards immediately below (nil agentLoop, empty registry) exist
// purely as defense-in-depth for a hypothetical future caller that does NOT
// share the call sites' precondition that a.agentLoop.GetConfig() already
// succeeded (e.g. narrow test scaffolding constructing a restAPI/agentLoop by
// hand). Per the codebase's established convention for an uninitialized
// dependency (see rest_god_mode.go, rest_sandbox_config.go,
// rest_security_wave5.go: "agent loop not initialized" -> 503), these guards
// FAIL CLOSED (deny) rather than silently allowing the assignment through.
//
// Returns nil only when agent_id is empty (no assignment to validate — not a
// fail-open case, there is simply nothing to check). A non-nil agentID with an
// unavailable agent loop/registry now returns errTaskAgentLoopUnavailable
// instead of silently allowing the assignment; both current call sites map any
// non-nil error to 400 today. A dedicated 503 mapping specifically for
// errTaskAgentLoopUnavailable would require touching those two call sites,
// which is a straightforward follow-up but out of scope for this change —
// the important property (fail CLOSED instead of silently allowing) already
// holds either way.
func (a *restAPI) validateTaskAgentID(agentID, workspaceID string) error {
	if agentID == "" {
		return nil
	}
	if a.agentLoop == nil {
		return errTaskAgentLoopUnavailable
	}
	reg := a.agentLoop.GetRegistry()
	if reg == nil || len(reg.ListAgentIDs()) == 0 {
		return errTaskAgentLoopUnavailable
	}
	if _, ok := reg.GetAgent(agentID); !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	if workspaceID == "" {
		return fmt.Errorf(
			"cannot assign agent %q: task has no workspace_id to validate team membership against",
			agentID,
		)
	}
	ws, wsErr := readWorkspaceFile(a.homePath, workspaceID)
	if wsErr != nil {
		return fmt.Errorf("cannot assign agent %q: workspace %q could not be loaded: %w", agentID, workspaceID, wsErr)
	}
	if !workspaceTeamSet(a.homePath, ws)[agentID] {
		return fmt.Errorf(
			"agent %q is not a member of this workspace's team — add it to the workspace's core team or a delegation edge before assigning tasks to it",
			agentID,
		)
	}
	return nil
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// parseTimeOrNow parses an RFC 3339 timestamp, falling back to now on error.
// Fix #5: logs a Warn when a non-empty string fails to parse (empty is normal
// for an unset optional field and does not warrant a log line).
func parseTimeOrNow(s string) time.Time {
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts
	}
	if s != "" {
		slog.Warn("rest_tasks: corrupt timestamp, defaulting to now", "value", s)
	}
	return time.Now().UTC()
}

// --- handlers ---------------------------------------------------------------

// handleTaskList handles GET /api/v1/tasks.
func (a *restAPI) handleTaskList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := task.Filter{
		WorkspaceID: q.Get("workspace_id"),
		Status:      task.Status(q.Get("status")),
		AgentID:     q.Get("agent_id"),
		Surface:     task.Surface(q.Get("surface")),
	}
	if filter.Status != "" && !task.IsValidStatus(filter.Status) {
		jsonErr(w, http.StatusBadRequest, "invalid status filter")
		return
	}
	if filter.Surface != "" && !task.IsValidSurface(filter.Surface) {
		jsonErr(w, http.StatusBadRequest, "invalid surface filter")
		return
	}
	if ptid := q.Get("parent_task_id"); ptid != "" {
		filter.ParentTaskID = ptid
		filter.ParentTaskIDSet = true
	}

	limit := 200
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		if v > 1000 {
			v = 1000
		}
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		offset = v
	}

	tasks, err := a.taskStore.List(filter)
	if err != nil {
		slog.Error("rest: task list failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list tasks")
		return
	}

	// Newest-first by created_at (List sorts by priority; the board/list views
	// want reverse-chronological, matching the legacy board surface).
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt > tasks[j].CreatedAt
	})

	if offset >= len(tasks) {
		tasks = tasks[:0]
	} else {
		tasks = tasks[offset:]
		if len(tasks) > limit {
			tasks = tasks[:limit]
		}
	}

	// Fix-wave finding #1a: build the rollup index ONCE for this whole
	// response batch instead of letting each toWireTask call trigger its own
	// full Store.List scan (previously O(n^2) — see rollupIndex's doc
	// comment).
	idx, ridxErr := a.buildRollupIndex()
	if ridxErr != nil {
		slog.Error("rest: task list: build rollup index failed", "error", ridxErr)
		jsonErr(w, http.StatusInternalServerError, "could not list tasks")
		return
	}
	// GOAL-FR-029/FR-048: same batching rationale as the rollup index above,
	// applied to `dod` (taskGoalIndex's doc comment) — one goal.Store.List
	// scan for the whole response instead of one per returned task.
	gidx, gidxErr := a.buildTaskGoalIndex()
	if gidxErr != nil {
		slog.Error("rest: task list: build goal index failed", "error", gidxErr)
		jsonErr(w, http.StatusInternalServerError, "could not list tasks")
		return
	}
	out := make([]gen.Task, 0, len(tasks))
	for _, t := range tasks {
		// gidx is non-nil here, so toWireTask never takes its direct-read
		// branch and cannot report a DoD read failure — the gidxErr branch
		// above already answered 500 for that. Checked anyway so a future
		// error source inside toWireTask cannot re-open SF-6 through the
		// batch door.
		wire, wErr := a.toWireTask(t, idx, gidx)
		if wErr != nil {
			slog.Error("rest: task list: could not render task completely",
				"task_id", t.ID, "error", wErr)
			jsonErr(w, http.StatusInternalServerError, "could not list tasks")
			return
		}
		out = append(out, wire)
	}
	jsonOK(w, out)
}

// handleTaskGet handles GET /api/v1/tasks/{id}.
func (a *restAPI) handleTaskGet(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.taskStore.Get(id)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		slog.Error("rest: task get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}
	a.writeWireTask(w, http.StatusOK, *t)
}

// handleTaskSubtasks handles GET /api/v1/tasks/{id}/subtasks.
func (a *restAPI) handleTaskSubtasks(w http.ResponseWriter, parentID string) {
	if err := validateEntityID(parentID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	children, err := a.taskStore.List(task.Filter{ParentTaskID: parentID, ParentTaskIDSet: true})
	if err != nil {
		slog.Error("rest: task subtasks list failed", "parent_id", parentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list subtasks")
		return
	}
	// Fix-wave finding #1a: each subtask's OWN rollup (its grandchildren)
	// previously triggered its own full Store.List scan per loop iteration —
	// build the shared index once instead (see rollupIndex's doc comment).
	idx, ridxErr := a.buildRollupIndex()
	if ridxErr != nil {
		slog.Error("rest: task subtasks: build rollup index failed", "parent_id", parentID, "error", ridxErr)
		jsonErr(w, http.StatusInternalServerError, "could not list subtasks")
		return
	}
	gidx, gidxErr := a.buildTaskGoalIndex()
	if gidxErr != nil {
		slog.Error("rest: task subtasks: build goal index failed", "parent_id", parentID, "error", gidxErr)
		jsonErr(w, http.StatusInternalServerError, "could not list subtasks")
		return
	}
	out := make([]gen.Task, 0, len(children))
	for _, t := range children {
		// Same rationale as handleTaskList's loop above.
		wire, wErr := a.toWireTask(t, idx, gidx)
		if wErr != nil {
			slog.Error("rest: task subtasks: could not render task completely",
				"parent_id", parentID, "task_id", t.ID, "error", wErr)
			jsonErr(w, http.StatusInternalServerError, "could not list subtasks")
			return
		}
		out = append(out, wire)
	}
	jsonOK(w, out)
}

// handleTaskCreate handles POST /api/v1/tasks → 201 Created. The task always
// lands in `inbox` (Detail #8); status is never a create-time field.
func (a *restAPI) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.TaskCreateRequest
	if !decodeAndValidate(w, r, "TaskCreateRequest", &req, validateEnabled) {
		return
	}

	if req.WorkspaceId == "" {
		jsonErr(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if err := validateEntityID(req.WorkspaceId); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}
	if _, wsErr := readWorkspaceFile(a.homePath, req.WorkspaceId); wsErr != nil {
		if errors.Is(wsErr, errWorkspaceNotFound) || errors.Is(wsErr, os.ErrNotExist) {
			jsonErr(w, http.StatusBadRequest, "workspace not found")
			return
		}
		jsonErr(w, http.StatusInternalServerError, "failed to validate workspace_id")
		return
	}

	c := a.callerIdentity(r)

	t := &task.Task{
		Title:       req.Title,
		Action:      task.Action(req.Action),
		Status:      task.StatusInbox,
		WorkspaceID: req.WorkspaceId,
		Owner:       c.Username,
		CreatedBy:   c.Username,
	}
	if t.Action == "" {
		t.Action = task.ActionLLM
	}
	if req.Prompt != nil {
		t.Prompt = *req.Prompt
	}
	if req.Description != nil {
		t.Description = *req.Description
	}
	if req.Priority != nil {
		// M2(a) fix: req.Priority is a wire *int, so its presence is unambiguous
		// here — a non-nil pointer to 0 IS an explicit priority:0 and must be
		// rejected now, BEFORE that presence information is lost by assigning
		// into t.Priority (a plain int, where 0 means "unset" per
		// task.Task.Priority's own contract). Previously this block skipped
		// straight to the assignment and relied on task.Store.Create's own
		// validation to catch an out-of-range value — but that validation
		// (task.go normalize()) deliberately treats Priority==0 as "unset, skip
		// the check" for exactly the same reason, so an explicit priority:0
		// sailed through uncaught and was silently persisted as unset (read
		// back as 3 via EffectivePriority).
		if err := task.ValidatePriority(*req.Priority); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		t.Priority = *req.Priority
	}
	if req.AgentId != nil && *req.AgentId != "" {
		agentID := *req.AgentId
		if err := validateEntityID(agentID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid agent_id")
			return
		}
		if err := a.validateTaskAgentID(agentID, req.WorkspaceId); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		t.AgentID = agentID
	}
	if req.ParentTaskId != nil {
		t.ParentTaskID = *req.ParentTaskId
	}
	if req.PlanId != nil && *req.PlanId != "" {
		// Same-workspace FK + draft-only membership, via the one choke point
		// every attach path shares (tools.ValidateTaskPlanMembership).
		if err := tools.ValidateTaskPlanMembership(a.planStore, *req.PlanId, req.WorkspaceId); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		t.PlanID = *req.PlanId
	}
	if req.WriteSet != nil {
		t.WriteSet = *req.WriteSet
	}
	if req.Stream != nil {
		t.Stream = *req.Stream
	}
	if req.IsJoin != nil {
		t.IsJoin = *req.IsJoin
	}
	if req.Tags != nil {
		t.Tags = *req.Tags
	}
	// GOAL-FR-021/FR-047/GOAL-MV-6/D-C: creating a task through the API or
	// the interface requires at least one acceptance criterion AND at least
	// one definition-of-done item — naming explicitly which is missing.
	var criteria, dod []task.AcceptanceCriterion
	if req.Criteria == nil || len(*req.Criteria) == 0 {
		jsonErrField(w, http.StatusBadRequest,
			"Add at least one acceptance criterion — what must be true for this task to be done.",
			"criteria")
		return
	}
	criteria = criteriaFromCreateWire(*req.Criteria)
	t.Criteria = criteria
	if req.Dod == nil || len(*req.Dod) == 0 {
		jsonErrField(w, http.StatusBadRequest,
			"Add at least one Definition of Done item, distinct from the acceptance criteria.",
			"dod")
		return
	}
	dod = dodFromCreateWire(*req.Dod)
	// The DISTINCTNESS half of the same rule the refusal above advertises
	// ("distinct from its acceptance criteria"). It was advertised and never
	// checked: a UAT tester pasted one sentence into both boxes and the task
	// saved with 201. See task.ValidateDoDDistinct for the rule and for why it
	// stops at whitespace and case rather than reaching for similarity.
	//
	// Checked BEFORE taskStore.Create, so a refused pair leaves no task and no
	// goal record behind.
	if err := task.ValidateDoDDistinct(criteria, dod); err != nil {
		// jsonTaskValidationErr routes the refusal to `field: "dod"` via
		// errors.Is(err, task.ErrDoDNotDistinct), exactly as the update path
		// does, so a client never has to parse the plain-language message.
		jsonTaskValidationErr(w, err)
		return
	}
	if req.MaxAttempts != nil {
		v := *req.MaxAttempts
		t.MaxAttempts = &v
	}
	if req.Surface != nil {
		t.Surface = task.Surface(*req.Surface)
	}
	if req.Due != nil {
		t.Due = req.Due.UTC().Format(time.RFC3339)
	}
	// source_channel/source_chat_id are internal routing, set ONLY server-side
	// from the delegating turn context (the task_create tool sets them from
	// ToolChannel/ToolChatID); never client-supplied.
	if req.BlockedBy != nil {
		t.BlockedBy = *req.BlockedBy
	}
	if req.Todos != nil {
		for _, td := range *req.Todos {
			t.Todos = append(t.Todos, task.Todo{Text: td.Text, Status: task.TodoStatus(td.Status)})
		}
	}
	if req.Trigger != nil {
		t.Trigger = buildTrigger(
			string(req.Trigger.Type),
			req.Trigger.Config.AtMs,
			req.Trigger.Config.EveryMs,
			req.Trigger.Config.CronExpr,
			req.Trigger.Config.Rrule,
			req.Trigger.Config.DtstartMs,
			req.Trigger.Config.Tz,
		)
	}

	if err := a.taskStore.Create(t); err != nil {
		if isTaskValidationErr(err) {
			jsonTaskValidationErr(w, err)
			return
		}
		slog.Error("rest: task create failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not create task")
		return
	}

	// ADR-086 D2/D5, GOAL-FR-003/FR-012/FR-021/FR-029: a task's Definition of
	// Done is authored ahead of time onto its own paired goal record, which
	// stays in the "defining" phase until the task itself starts (GOAL-FR-012
	// — nothing here activates it). t.Criteria above is a dual-write for the
	// consumers not yet re-pointed to read the goal record this round — and,
	// since taskStore.Create has now normalised t.Criteria in place, it is
	// also the list syncTaskGoalRecord reads, so both records carry ONE set of
	// criterion ids (see that function's doc comment).
	if gErr := a.syncTaskGoalRecord(t, true, dod, true); gErr != nil {
		slog.Error("rest: task create: failed to create paired goal record",
			"task_id", t.ID, "error", gErr)
		// Both lists are always provided on the create path, so the
		// caller-fault branch is unreachable here — mapped anyway so the two
		// handlers answer the same condition with the same status if that
		// ever stops being true.
		if errors.Is(gErr, errGoalRecordNeedsBothLists) {
			jsonErr(w, http.StatusBadRequest, gErr.Error())
			return
		}
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf(
			"task %q was created but its Definition of Done could not be persisted: %v", t.ID, gErr))
		return
	}

	a.auditTask("task.create", t.ID)
	// FR-022: no-op today (a freshly-created task has no prior trigger to
	// diff against) — see auditTriggerChange's doc comment for why this
	// call site stays in place regardless.
	a.auditTriggerChange(t.ID, nil, t.Trigger)
	a.emitTaskStatus(t)
	// Register the task's time trigger (once/every/recurring) so it actually
	// fires; a no-op for manual/heartbeat tasks.
	if a.agentLoop != nil {
		a.agentLoop.NotifyTaskUpserted(t)
	}
	a.writeWireTask(w, http.StatusCreated, *t)
}

// taskPatch carries the shared state of handleTaskPatch across its stages.
type taskPatch struct {
	a                     *restAPI
	w                     http.ResponseWriter
	r                     *http.Request
	id                    string
	req                   gen.TaskUpdateRequest
	existingForDefinition *task.Task
	patch                 task.Patch
	detachResetsStatus    bool
	priorTriggerForAudit  *task.Trigger
	patchCriteria         []task.AcceptanceCriterion
	patchDoD              []task.AcceptanceCriterion
	criteriaProvided      bool
	dodProvided           bool
	preUpdateStatus       task.Status
	updated               *task.Task
	priorForUpdate        *task.Task
}

// handleTaskPatch handles PATCH /api/v1/tasks/{id}.
func (a *restAPI) handleTaskPatch(w http.ResponseWriter, r *http.Request, id string) {
	tp := &taskPatch{a: a, w: w, r: r, id: id}

	if tp.validate() {
		return
	}
	if tp.buildPatch() {
		return
	}
	if tp.checkDefinitionOfDone() {
		return
	}
	tp.applyRunFields()
	if tp.capturePriorStatus() {
		return
	}
	if tp.applyUpdate() {
		return
	}
	if tp.syncGoalRecord() {
		return
	}
	if tp.launchIfStarted() {
		return
	}
	tp.finish()
}

// validate checks the id and body and refuses changes the task's current state forbids.
func (tp *taskPatch) validate() bool {
	if err := validateEntityID(tp.id); err != nil {
		jsonErr(tp.w, http.StatusBadRequest, "invalid task ID")
		return true
	}
	validateEnabled := tp.a.agentLoop.GetConfig().Gateway.ValidateInbound

	if !decodeAndValidate(tp.w, tp.r, "TaskUpdateRequest", &tp.req, validateEnabled) {
		return true
	}

	// Fix #6: blocked is a derived side-state; reject it at the gateway seam
	// before reaching the store (defense-in-depth alongside ErrBlockedNotSettable).
	if tp.req.Status != nil && task.Status(*tp.req.Status) == task.StatusBlocked {
		jsonErr(tp.w, http.StatusBadRequest, "blocked is a derived side-state and cannot be set directly")
		return true
	}

	// Operator decision, 2026-09-12: the judged contract freezes for the
	// duration of a run. See frozenTaskDefinitionFields for the full rule and
	// for why the refusal is whole-request rather than partial. The check runs
	// HERE — before any field is copied into the patch and before any store
	// write — so a mixed frozen+mutable request leaves nothing behind.
	//
	// existingForDefinition is retained past the freeze check for the
	// distinctness rule below: frozenTaskDefinitionFields reports a non-empty
	// list whenever `criteria` or `dod` is present, so whenever that rule needs
	// the task's CURRENT criteria (an edit that supplies only `dod`), this read
	// has already happened and must not be repeated.

	if frozen := frozenTaskDefinitionFields(&tp.req); len(frozen) > 0 {
		existing, gErr := tp.a.taskStore.Get(tp.id)
		if gErr != nil {
			if errors.Is(gErr, task.ErrNotFound) {
				jsonErr(tp.w, http.StatusNotFound, "task not found")
				return true
			}
			slog.Error("rest: task patch: could not read task for the running-definition freeze check",
				"task_id", tp.id, "error", gErr)
			jsonErr(tp.w, http.StatusInternalServerError, "could not read task")
			return true
		}
		if existing.Status == task.StatusInProgress {
			jsonErr(tp.w, http.StatusConflict, runningTaskFrozenFieldMessage(frozen))
			return true
		}
		tp.existingForDefinition = existing
	}

	// Detail #8: advancing a partial (no prompt/description) task to `next` is
	// rejected — only fully-captured tasks may be triaged.
	if tp.req.Status != nil && task.Status(*tp.req.Status) == task.StatusNext {
		existing, gErr := tp.a.taskStore.Get(tp.id)
		if gErr != nil {
			if errors.Is(gErr, task.ErrNotFound) {
				jsonErr(tp.w, http.StatusNotFound, "task not found")
				return true
			}
			jsonErr(tp.w, http.StatusInternalServerError, "could not read task")
			return true
		}
		hasPrompt := existing.Prompt != "" || existing.Description != ""
		if tp.req.Prompt != nil && *tp.req.Prompt != "" {
			hasPrompt = true
		}
		if tp.req.Description != nil && *tp.req.Description != "" {
			hasPrompt = true
		}
		if !hasPrompt {
			jsonErr(
				tp.w,
				http.StatusUnprocessableEntity,
				"a partial task cannot be advanced to next — add a prompt or description first",
			)
			return true
		}
	}
	return false
}

// buildPatch translates the request's fields into a task.Patch.
func (tp *taskPatch) buildPatch() bool {
	tp.patch = task.Patch{}
	// Set when a plan DETACH (plan_id -> "") optimistically adds `status: inbox`
	// to the patch; drives the ErrIllegalTransition retry at the Update below so
	// a terminal/blocked member still detaches instead of 400ing.
	tp.detachResetsStatus = false
	if tp.req.Title != nil {
		tp.patch.Title = tp.req.Title
	}
	if tp.req.Description != nil {
		tp.patch.Description = tp.req.Description
	}
	if tp.req.Prompt != nil {
		tp.patch.Prompt = tp.req.Prompt
	}
	if tp.req.Status != nil {
		st := task.Status(*tp.req.Status)
		tp.patch.Status = &st
	}
	if tp.req.AgentId != nil {
		if *tp.req.AgentId != "" {
			// Team-membership validation is workspace-scoped, and a task's
			// workspace_id is immutable via PATCH (not a TaskUpdateRequest
			// field) — read the existing task to learn it. A dedicated read
			// here (rather than threading through the conditional "next"-status
			// read above) keeps this block correct regardless of which other
			// fields are present in the same PATCH.
			existingForAgentCheck, gErr := tp.a.taskStore.Get(tp.id)
			if gErr != nil {
				if errors.Is(gErr, task.ErrNotFound) {
					jsonErr(tp.w, http.StatusNotFound, "task not found")
					return true
				}
				jsonErr(tp.w, http.StatusInternalServerError, "could not read task")
				return true
			}
			if err := tp.a.validateTaskAgentID(*tp.req.AgentId, existingForAgentCheck.WorkspaceID); err != nil {
				jsonErr(tp.w, http.StatusBadRequest, err.Error())
				return true
			}
		}
		tp.patch.AgentID = tp.req.AgentId
	}
	if tp.req.Priority != nil {
		tp.patch.Priority = tp.req.Priority
	}
	if tp.req.BlockedBy != nil {
		tp.patch.BlockedBy = tp.req.BlockedBy
	}
	if tp.req.Todos != nil {
		todos := make([]task.Todo, 0, len(*tp.req.Todos))
		for _, td := range *tp.req.Todos {
			todos = append(todos, task.Todo{Text: td.Text, Status: task.TodoStatus(td.Status)})
		}
		tp.patch.Todos = &todos
	}
	// FR-022 audit prerequisite: patch.Trigger is built here; the prior
	// trigger snapshot itself is captured atomically below by
	// UpdateWithPrior (M-BE1), under the SAME per-task lock as the write —
	// closing the TOCTOU window a separate pre-patch Get() had under two
	// concurrent same-task trigger PATCHes (the second call's "prior" read
	// could complete before the first call's write landed, then the first
	// call's write would land, then the second call's own write would land
	// on top of it — leaving the second call's recorded "prior" stale, never
	// reflecting the first call's write even though both calls' own writes
	// were correctly serialized by the store's per-task lock).

	if tp.req.Trigger != nil {
		tr := buildTrigger(
			string(tp.req.Trigger.Type),
			tp.req.Trigger.Config.AtMs,
			tp.req.Trigger.Config.EveryMs,
			tp.req.Trigger.Config.CronExpr,
			tp.req.Trigger.Config.Rrule,
			tp.req.Trigger.Config.DtstartMs,
			tp.req.Trigger.Config.Tz,
		)
		tp.patch.Trigger = &tr
	}
	if tp.req.Due != nil {
		due := tp.req.Due.UTC().Format(time.RFC3339)
		tp.patch.Due = &due
	} else if tp.req.ClearDue != nil && *tp.req.ClearDue {
		// clear_due unambiguously clears the stored due date. Ignored when `due`
		// is set to a value (the value wins). The store applies *patch.Due
		// verbatim, so an empty string clears Task.Due (which omits when empty).
		empty := ""
		tp.patch.Due = &empty
	}
	if tp.req.PlanId != nil {
		if *tp.req.PlanId != "" {
			// A task's workspace_id is immutable via PATCH (not a
			// TaskUpdateRequest field) — read the existing task to learn it,
			// mirroring the AgentId team-membership check's identical
			// dedicated read above.
			existingForPlanCheck, gErr := tp.a.taskStore.Get(tp.id)
			if gErr != nil {
				if errors.Is(gErr, task.ErrNotFound) {
					jsonErr(tp.w, http.StatusNotFound, "task not found")
					return true
				}
				jsonErr(tp.w, http.StatusInternalServerError, "could not read task")
				return true
			}
			// Same-workspace FK + draft-only membership, via the one choke
			// point every attach path shares. This is the RE-PARENT/attach
			// half of PATCH (plan A -> plan B, or standalone -> plan B); the
			// DETACH half (plan_id -> "") is the else branch below and is
			// deliberately not gated — leaving a plan is always allowed.
			if err := tools.ValidateTaskPlanMembership(
				tp.a.planStore, *tp.req.PlanId, existingForPlanCheck.WorkspaceID); err != nil {
				jsonErr(tp.w, http.StatusBadRequest, err.Error())
				return true
			}
		} else if tp.req.Status == nil {
			// DETACH (plan_id -> ""). Sibling of the plan-delete laundering
			// hole: detaching a member without touching its status turns a
			// `next` member of a draft/stopped plan into a STANDALONE `next`
			// task, which requirePlanExecuting rightly permits (PlanID == ""
			// means "not a plan member") and CheckQueuedTasks' ~60s drain then
			// auto-dispatches — running work the Execute gate never approved.
			// Reachable from the UI today: TaskDetailPanel's Plan dropdown ->
			// "No plan" sends exactly this patch.
			//
			// Mirror detachMemberOnPlanDelete: a detached non-terminal member
			// returns to triage rather than inheriting a dispatchable resting
			// state it was never approved for. Applied on the SAME patch as the
			// plan_id clear, so there is no window where the task is standalone
			// and still `next`.
			//
			// Only when the caller did NOT also set `status` in the same
			// request (an explicit status wins). Re-parenting plan A -> plan B
			// is untouched — that takes the non-empty branch above.
			// detachResetsStatus drives the ErrIllegalTransition retry at the
			// Update call site: the store refuses this combined patch for
			// `blocked`/`done`/`failed` (blocked may only leave via the store's
			// own recompute; done is frozen), and those must still detach
			// rather than 400 — history is never rewritten.
			inbox := task.StatusInbox
			tp.patch.Status = &inbox
			tp.detachResetsStatus = true
		}
		tp.patch.PlanID = tp.req.PlanId
	}
	if tp.req.WriteSet != nil {
		tp.patch.WriteSet = tp.req.WriteSet
	}
	if tp.req.Stream != nil {
		tp.patch.Stream = tp.req.Stream
	}
	if tp.req.IsJoin != nil {
		tp.patch.IsJoin = tp.req.IsJoin
	}
	if tp.req.Tags != nil {
		tp.patch.Tags = tp.req.Tags
	}
	// GOAL-FR-021/FR-023/FR-047/FR-048/D-C: the mandatory-count gate binds at
	// edit too, uniformly with create — an update supplying either list must
	// not reduce it below one item; a PATCH that does not touch criteria/dod
	// at all is unaffected (GOAL-FR-023/FR-048 exempt an untouched legacy
	// task from the rule, never from being edited once it IS touched).

	tp.criteriaProvided, tp.dodProvided = false, false
	if tp.req.Criteria != nil {
		tp.criteriaProvided = true
		if len(*tp.req.Criteria) == 0 {
			jsonErrField(tp.w, http.StatusBadRequest,
				"An update that changes the acceptance criteria must leave at least one.",
				"criteria")
			return true
		}
		tp.patchCriteria = criteriaFromUpdateWire(*tp.req.Criteria)
		tp.patch.Criteria = &tp.patchCriteria
	}
	if tp.req.Dod != nil {
		tp.dodProvided = true
		if len(*tp.req.Dod) == 0 {
			jsonErrField(tp.w, http.StatusBadRequest,
				"An update that changes the Definition of Done must leave at least one item.",
				"dod")
			return true
		}
		tp.patchDoD = dodFromUpdateWire(*tp.req.Dod)
	}
	return false
}

// checkDefinitionOfDone refuses a Definition of Done that duplicates the acceptance criteria, before anything is written.
func (tp *taskPatch) checkDefinitionOfDone() bool {
	// GOAL-FR-048 binds the distinctness rule at SAVE, not only at create —
	// otherwise the rule is a door you walk around: create with a distinct DoD,
	// then edit it into a duplicate.
	//
	// An edit may touch one list and not the other, so the comparison is
	// against the EFFECTIVE post-edit pair: the submitted list where one was
	// submitted, the persisted list otherwise. The persisted criteria come off
	// the task record; the persisted DoD can only come off the paired goal
	// record, which is the only place a task's DoD exists (ADR-086 D5).
	//
	// Checked BEFORE UpdateWithPrior, so a refusal writes nothing at all —
	// matching the freeze check's own "a mixed request leaves nothing behind".
	if tp.criteriaProvided || tp.dodProvided {
		effectiveCriteria := tp.patchCriteria
		if !tp.criteriaProvided {
			if tp.existingForDefinition == nil {
				// A never-firing tripwire, not a fallback:
				// frozenTaskDefinitionFields reports "criteria"/"dod" whenever
				// either is present, so the read above has always happened by
				// the time this branch is reachable. If that ever stops being
				// true, the rule must FAIL LOUDLY rather than silently not run
				// — a rule that quietly skips itself is the whole defect class
				// this change exists to close.
				slog.Error("rest: task patch: the Definition-of-Done distinctness check was reached "+
					"with no task read — frozenTaskDefinitionFields and the criteria/dod block have "+
					"drifted apart", "task_id", tp.id)
				jsonErr(tp.w, http.StatusInternalServerError,
					"could not check the Definition of Done against the acceptance criteria")
				return true
			}
			effectiveCriteria = tp.existingForDefinition.Criteria
		}
		effectiveDoD := tp.patchDoD
		if !tp.dodProvided {
			persistedDoD, dErr := tp.a.pairedGoalDoD(tp.id)
			if dErr != nil {
				slog.Error("rest: task patch: could not read the paired goal record's "+
					"Definition of Done for the distinctness check", "task_id", tp.id, "error", dErr)
				jsonErr(tp.w, http.StatusInternalServerError,
					"could not read the task's Definition of Done")
				return true
			}
			effectiveDoD = persistedDoD
		}
		if vErr := task.ValidateDoDDistinct(effectiveCriteria, effectiveDoD); vErr != nil {
			jsonErr(tp.w, http.StatusBadRequest, vErr.Error())
			return true
		}
	}
	return false
}

// applyRunFields copies attempt, surface, result and timing fields into the patch.
func (tp *taskPatch) applyRunFields() {
	if tp.req.MaxAttempts != nil {
		v := *tp.req.MaxAttempts
		vp := &v
		tp.patch.MaxAttempts = &vp
	}
	if tp.req.Surface != nil {
		sf := task.Surface(*tp.req.Surface)
		tp.patch.Surface = &sf
	}
	if tp.req.Result != nil {
		tp.patch.Result = tp.req.Result
	}
	if tp.req.Artifacts != nil {
		tp.patch.Artifacts = tp.req.Artifacts
	}
	if tp.req.StartedAt != nil {
		sa := tp.req.StartedAt.UTC().Format(time.RFC3339)
		tp.patch.StartedAt = &sa
	}
	if tp.req.CompletedAt != nil {
		ca := tp.req.CompletedAt.UTC().Format(time.RFC3339)
		tp.patch.CompletedAt = &ca
	}
}

// capturePriorStatus records the status before the update and refuses re-running a repeating task via PATCH.
func (tp *taskPatch) capturePriorStatus() bool {
	// Capture the pre-update status to detect the in_progress transition below.

	if tp.req.Status != nil {
		// Read the current status before applying the patch so we can detect
		// transitions rather than just the new state. We need the original status
		// to distinguish "was already in_progress" from "just moved to in_progress".
		if existing, gErr := tp.a.taskStore.Get(tp.id); gErr == nil {
			tp.preUpdateStatus = existing.Status
			// ADR-050 RD7, task-run-history-spec.md §3.4 ("the ADR-049
			// fresh-run reset is superseded by this in the same change...do
			// not keep both paths"): a "Run now" on a done/failed REPEATING
			// task must go through the run-aware POST
			// /api/v1/tasks/{id}/runs entry point (handleTaskRunNow ->
			// TaskExecutor.StartOccurrenceRun -> Store.SpawnReset +
			// Store.OpenRun, rest_task_runs.go), not this PATCH-status
			// endpoint. This PATCH path's in_progress launch (below) goes
			// through StartTaskNow -> runTaskFromInProgress, which passes
			// nil for ADR-050's *activeRun and therefore never opens/closes
			// a TaskRun record (see runTaskFromInProgress's own doc comment
			// in pkg/agent/task_executor.go) — a repeating task re-run
			// through this path would be invisible to the calendar
			// occurrence overlay and the run-history list.
			//
			// This used to be handled by a field-clearing "fresh-run reset"
			// (session_id/result/artifacts/completed_at/followed_up wiped
			// pre-emptively so the launch guard below, which requires
			// updated.SessionID == "", would fire). That reset existed ONLY
			// to force this untracked path to launch at all; deleting it
			// with no replacement would leave a PATCH like this silently
			// flip the task to in_progress with the PRIOR run's stale
			// session_id still attached (the guard below never fires since
			// SessionID != ""), so the task never re-launches and sits
			// permanently "in progress" with a dead session. Reject instead,
			// before any store write, and point the caller at the run-aware
			// endpoint.
			if tp.patch.Status != nil && *tp.patch.Status == task.StatusInProgress &&
				task.IsTerminal(existing.Status) &&
				existing.Trigger.IsRepeating() {
				jsonErr(tp.w, http.StatusBadRequest,
					"cannot re-run a repeating task via PATCH status; use POST /api/v1/tasks/"+tp.id+"/runs instead")
				return true
			}
		}
	}
	return false
}

// applyUpdate writes the patch to the task store, retrying once without a status reset on detach.
func (tp *taskPatch) applyUpdate() bool {
	// FR-022/M-BE1: UpdateWithPrior (not a plain Update) so the "prior trigger"
	// snapshot below is captured atomically under the SAME per-task lock as
	// this write — see priorTriggerForAudit's own doc comment above for the
	// TOCTOU window a separate pre-patch Get() would have.
	var err error
	tp.updated, tp.priorForUpdate, err = tp.a.taskStore.UpdateWithPrior(tp.id, tp.patch)
	if err != nil && tp.detachResetsStatus && errors.Is(err, task.ErrIllegalTransition) {
		// The detach above optimistically added `status: inbox`. A terminal or
		// `blocked` member legitimately refuses that (done is frozen; blocked
		// may only leave via the store's own recompute), but the DETACH itself
		// must still succeed rather than 400 — those states cannot be
		// auto-dispatched anyway, so the laundering risk this reset exists to
		// close does not apply to them. Retry with the plan_id clear alone and
		// let recomputeBlockedStateLocked (which runs at the end of every
		// Update) settle `blocked`.
		tp.patch.Status = nil
		tp.updated, tp.priorForUpdate, err = tp.a.taskStore.UpdateWithPrior(tp.id, tp.patch)
	}
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(tp.w, http.StatusNotFound, "task not found")
			return true
		}
		if isTaskValidationErr(err) {
			jsonTaskValidationErr(tp.w, err)
			return true
		}
		slog.Error("rest: task update failed", "id", tp.id, "error", err)
		jsonErr(tp.w, http.StatusInternalServerError, "could not update task")
		return true
	}
	if tp.req.Trigger != nil && tp.priorForUpdate != nil {
		tp.priorTriggerForAudit = tp.priorForUpdate.Trigger
	}
	return false
}

// syncGoalRecord persists changed criteria or Definition of Done on the paired goal record.
func (tp *taskPatch) syncGoalRecord() bool {
	// GOAL-FR-029/FR-030: updated.Criteria above already carries the new
	// criteria (dual-write, via patch.Criteria); this is what actually
	// persists the change onto the task's paired goal record — creating one
	// if this is a legacy task's first-ever criteria/dod (see
	// syncTaskGoalRecord's doc comment).
	//
	// This MUST fail visibly, exactly as handleTaskCreate's identical call
	// does (review finding 8 / SF-5). It used to log at Error and fall
	// through to a 200 carrying the task body, on the reasoning that
	// gen.Task has no field to carry a partial-failure warning. The effect
	// was that editing a pre-D-C task to ADD a Definition of Done — which
	// sends `dod` with no `criteria`, the exact shape syncTaskGoalRecord
	// refuses — answered "saved" and the next GET returned no dod at all.
	// A caller cannot act on a log line; the two write paths disagreeing
	// about the same condition is worse still. No wire field is needed to
	// say "this did not save": the status code says it.
	//
	// The one caller-fault case (bootstrapping a record with only one of the
	// two lists) is a 400 — the client can fix it by sending both. Every
	// other failure is a storage fault and stays a 500.
	if tp.criteriaProvided || tp.dodProvided {
		if gErr := tp.a.syncTaskGoalRecord(tp.updated, tp.criteriaProvided, tp.patchDoD, tp.dodProvided); gErr != nil {
			slog.Error("rest: task update: failed to sync paired goal record",
				"id", tp.id, "error", gErr)
			if errors.Is(gErr, errGoalRecordNeedsBothLists) {
				jsonErr(tp.w, http.StatusBadRequest, gErr.Error())
				return true
			}
			jsonErr(tp.w, http.StatusInternalServerError, fmt.Sprintf(
				"task %q was updated but its Definition of Done could not be persisted: %v",
				tp.id, gErr))
			return true
		}
	}
	return false
}

// launchIfStarted starts the task run when the update moved it into in_progress, reverting on failure.
func (tp *taskPatch) launchIfStarted() bool {
	// If the task transitioned INTO in_progress (from a different state) and has
	// an assigned agent, launch the agent immediately via StartTaskNow. The
	// idempotency guard inside StartTaskNow prevents a double-launch if the task
	// already has a session_id. After StartTaskNow returns, re-read the task so
	// the response carries the newly-minted session_id.
	//
	// On failure the task must NOT be left stranded in in_progress with no agent
	// running. We revert its status back to the pre-update state so the client
	// observes the failure and can retry. The HTTP status signals the cause:
	//   503 — taskExecutor is nil (gateway degraded / not fully initialized)
	//   409 — dispatch cap exhausted (retryable congestion)
	//   500 — any other launch error
	//
	// The revert also clears StartedAt: the in_progress patch above (via
	// Store.updateLocked) stamped it on the (failed) transition, so a bare
	// Status-only revert would leave the task carrying a "started" timestamp
	// from an attempt that never actually ran, until the next real in_progress
	// transition happens to overwrite it.
	//
	// Guard: only fire when the client explicitly set status=in_progress in this
	// PATCH (req.Status != nil). A PATCH with no status field must never enter
	// the launch path even if the task happens to already be in_progress —
	// preUpdateStatus would be "" in that case, making the revert call
	// Update(id, Patch{Status: &""}) which the store rejects (IsValidStatus("")
	// == false), causing a silent no-op revert and a misleading log entry.
	if tp.req.Status != nil &&
		tp.updated.Status == task.StatusInProgress &&
		tp.preUpdateStatus != task.StatusInProgress &&
		tp.updated.AgentID != "" &&
		tp.updated.SessionID == "" {
		// buildLaunchRevertPatch assembles the revert-to-prior-state patch shared
		// by both failure branches below (nil executor, StartTaskNow error).
		buildLaunchRevertPatch := func() task.Patch {
			revertStatus := tp.preUpdateStatus
			revertStartedAt := ""
			return task.Patch{Status: &revertStatus, StartedAt: &revertStartedAt}
		}
		if tp.a.taskExecutor == nil {
			// Revert the task to its prior status so it is not left stranded.
			revertPatch := buildLaunchRevertPatch()
			if _, rErr := tp.a.taskStore.Update(tp.id, revertPatch); rErr != nil {
				slog.Error("rest: could not revert task status after nil-executor failure",
					"id", tp.id, "revert_to", tp.preUpdateStatus, "error", rErr)
			}
			slog.Warn("rest: taskExecutor is nil; rejecting in_progress transition",
				"id", tp.id, "agent_id", tp.updated.AgentID)
			jsonErr(tp.w, http.StatusServiceUnavailable, "task executor is not available; retry later")
			return true
		}
		sessID, startErr := tp.a.taskExecutor.StartTaskNow(tp.r.Context(), tp.id)
		if startErr != nil {
			// Revert the task to its prior status so it is not left stranded.
			revertPatch := buildLaunchRevertPatch()
			if _, rErr := tp.a.taskStore.Update(tp.id, revertPatch); rErr != nil {
				slog.Error("rest: could not revert task status after StartTaskNow failure",
					"id", tp.id, "revert_to", tp.preUpdateStatus, "error", rErr)
			}
			httpStatus := http.StatusInternalServerError
			switch {
			case errors.Is(startErr, agent.ErrDispatchCapReached):
				httpStatus = http.StatusConflict
			case errors.Is(startErr, agent.ErrPlanNotExecuting), errors.Is(startErr, agent.ErrPlanStateUnresolvable):
				// S1 plan-gate follow-up: a PATCH to in_progress on a plan
				// member whose parent plan is not (or can no longer be
				// verified as) approved/running/unpaused — e.g. a Kanban drag
				// on a Draft plan's member, or a member of a plan the user
				// already Stopped. 409 (state conflict), not 500: the request
				// itself is well-formed, it conflicts with the plan's current
				// state.
				httpStatus = http.StatusConflict
			}
			// This is a client-triggered, one-shot rejection (bounded by
			// request rate, not a background loop) — logging it at Warn here
			// is the appropriate severity regardless of which error it was;
			// see agent.ErrPlanNotExecuting's own doc comment for why the
			// SAME error is intentionally logged quieter (Debug) inside
			// TaskExecutor's periodic/event-driven callers.
			slog.Warn("rest: StartTaskNow failed; task reverted to prior status",
				"id", tp.id, "agent_id", tp.updated.AgentID, "prior_status", tp.preUpdateStatus, "error", startErr)
			jsonErr(tp.w, httpStatus, startErr.Error())
			return true
		}
		if sessID != "" {
			// Re-read the persisted task so the response contains the session_id.
			if fresh, rerr := tp.a.taskStore.Get(tp.id); rerr == nil {
				tp.updated = fresh
			}
		}
	}
	return false
}

// finish advances dependents, ends the goal record, audits, emits and writes the response.
func (tp *taskPatch) finish() {
	// If the task reached `done`, advance any dependents (blocked → next).
	if tp.updated.Status == task.StatusDone {
		if advanced, advErr := tp.a.taskStore.AdvanceBlockedDependents(tp.id); advErr != nil {
			slog.Warn("rest: task advance dependents failed", "id", tp.id, "error", advErr)
		} else if len(advanced) > 0 {
			slog.Info("rest: completed task advanced dependents", "completed_id", tp.id, "advanced_ids", advanced)
		}
	}

	// GOAL-FR-015/FR-027/FR-028 (review finding C1): end the paired goal
	// record when THIS patch is what moved the task terminal.
	//
	// This is the writer the original three-call-site fix missed most
	// visibly, because it is the one a user drives by hand: validateTransition
	// permits in_progress->done and in_progress->failed, so dragging a card
	// onto Done or Failed on the board arrives here, answered 200, and left
	// the goal record `state: active` forever. The next run of that task then
	// silently skipped Goal.Reactivate (activateTaskGoal's `default:` branch
	// matches neither IsDefining nor IsTerminal), inheriting the previous
	// run's attempts, rounds and per-criterion statuses — a DoD item marked
	// `met` in run 1 served as `met` for work run 2 never did.
	//
	// priorForUpdate is UpdateWithPrior's snapshot, captured atomically under
	// the SAME per-task lock as the write, so the "did this patch move it"
	// test cannot be raced by a concurrent writer the way a separate pre-patch
	// Get() could be. The hook is idempotent, so a nil prior (defensive only —
	// UpdateWithPrior returns one on every success) falls through to the
	// terminal test alone rather than skipping the record.
	if task.IsTerminal(tp.updated.Status) &&
		(tp.priorForUpdate == nil || !task.IsTerminal(tp.priorForUpdate.Status)) {
		tools.TerminateTaskGoalRecord(
			tools.GoalStoreForTasks(tp.a.taskStore), tp.id, tp.updated.Status, tp.updated.CancelReason, tp.updated.Result)
	}

	tp.a.auditTask("task.update", tp.id)
	if tp.req.Trigger != nil {
		// FR-022: audit a recurrence-trigger change (legacy→RRULE or
		// RRULE→RRULE) — no-ops on a title-only edit (byte-identical
		// trigger, FR-024) or a non-recurring new trigger.
		tp.a.auditTriggerChange(tp.id, tp.priorTriggerForAudit, tp.updated.Trigger)
	}
	tp.a.emitTaskStatus(tp.updated)
	// Re-sync the task's time trigger: a changed/added/removed trigger or a move
	// to a terminal status (re)registers or removes its cron job.
	if tp.a.agentLoop != nil {
		tp.a.agentLoop.NotifyTaskUpserted(tp.updated)
	}
	tp.a.writeWireTask(tp.w, http.StatusOK, *tp.updated)
}

// handleTaskDelete handles DELETE /api/v1/tasks/{id} → 204.
func (a *restAPI) handleTaskDelete(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	// GOAL-FR-044/EC-4: a goal MUST NOT outlive its owner as an unreferenced
	// record — deleting a task transitions and removes its paired goal. Every
	// deleted task used to leave its goal record behind, owned by a task id
	// that no longer resolves and (if the task had ever run) permanently
	// `active`; the retention sweep was the only thing that would ever have
	// touched it again.
	//
	// Runs BEFORE the task file is removed, and a failure REFUSES the whole
	// delete (500) rather than logging and answering 204. The old ordering
	// could only report the orphan; this one prevents it. Answering 204 while
	// knowing a permanent orphan was just created is the same class of lie as
	// SF-6 in this file (emitting a task with no `dod` after a goal-record read
	// fault, indistinguishable from a task that genuinely has none) — and
	// unlike a mid-delete failure, nothing has happened yet here, so the
	// caller's retry is both meaningful and safe (RemoveTaskGoalRecords is
	// idempotent). The two agent tools take the identical path; this is the one
	// answer all three delete surfaces now give.
	if gErr := tools.RemoveTaskGoalRecords(tools.GoalStoreForTasks(a.taskStore), id); gErr != nil {
		slog.Error("rest: task delete: refusing to delete a task whose paired goal record could not "+
			"be removed (GOAL-FR-044) — nothing was deleted", "task_id", id, "error", gErr)
		jsonErr(w, http.StatusInternalServerError,
			"could not delete task: its paired goal record could not be removed, and deleting the "+
				"task anyway would leave that record behind as an unreferenced orphan")
		return
	}
	unblocked, err := a.taskStore.Delete(id)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		if !errors.Is(err, task.ErrCascadeEdgeCleanupFailed) {
			slog.Error("rest: task delete failed", "id", id, "error", err)
			jsonErr(w, http.StatusInternalServerError, "could not delete task")
			return
		}
		// The task file itself was already removed successfully; only
		// cleaning up OTHER tasks' dangling blocked_by edges partially
		// failed. Non-fatal to this delete — log loudly and keep serving the
		// successful delete, matching how the AdvanceUnblocked/advance-
		// dependents write failures below are treated as a logged, non-fatal
		// side effect rather than a failure of the primary operation.
		slog.Warn("rest: task delete: cascade edge cleanup partially failed", "id", id, "error", err)
	}
	// A task whose blocked_by list became empty after this delete (all blockers
	// gone) and is still `blocked` must advance to `next` — the cascade only
	// rewrote the edges, not the status.
	for _, depID := range unblocked {
		// AdvanceUnblocked is a no-op when the dependent is not `blocked`; it uses
		// the internal hatch so the transition guard does not reject blocked→next.
		if _, uErr := a.taskStore.AdvanceUnblocked(depID); uErr != nil {
			slog.Warn("rest: task delete: advance unblocked dependent failed", "id", depID, "error", uErr)
			continue
		}
		slog.Info(
			"rest: deleted task advanced unblocked dependent blocked→next",
			"deleted_id",
			id,
			"advanced_id",
			depID,
		)
	}
	// Remove the deleted task's time-trigger cron job (if any) so it does not
	// fire against a missing task.
	if a.agentLoop != nil {
		a.agentLoop.NotifyTaskDeleted(id)
	}
	a.auditTask("task.delete", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleTaskTodos handles PUT /api/v1/tasks/{id}/todos — replaces the checklist.
// The contract body is SetTaskTodosJSONRequestBody = []gen.Todo (a bare JSON
// array, NOT the TaskUpdateRequest object). An empty array is valid and clears
// the checklist.
func (a *restAPI) handleTaskTodos(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	var body gen.SetTaskTodosJSONRequestBody
	if !decodeTaskJSONBody(w, r, &body) {
		return
	}
	todos := make([]task.Todo, 0, len(body))
	for _, td := range body {
		todos = append(todos, task.Todo{Text: td.Text, Status: task.TodoStatus(td.Status)})
	}
	a.applyTaskFieldUpdate(w, id, task.Patch{Todos: &todos}, "todos")
}

// handleTaskDependencies handles PUT /api/v1/tasks/{id}/dependencies — replaces
// the blocked_by set (with cycle validation). The contract body is
// SetTaskDependenciesJSONRequestBody = []string (a bare JSON array, NOT the
// TaskUpdateRequest object). Cycle/self-edge rejection surfaces as 400 via
// isTaskValidationErr → ErrValidation.
func (a *restAPI) handleTaskDependencies(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	var body gen.SetTaskDependenciesJSONRequestBody
	if !decodeTaskJSONBody(w, r, &body) {
		return
	}
	a.applyTaskFieldUpdate(w, id, task.Patch{BlockedBy: &body}, "dependencies")
}

// applyTaskFieldUpdate applies a single-field patch and writes the standard
// task response, mapping store errors to HTTP statuses. `what` is used only for
// the error log line.
func (a *restAPI) applyTaskFieldUpdate(w http.ResponseWriter, id string, patch task.Patch, what string) {
	updated, err := a.taskStore.Update(id, patch)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		if isTaskValidationErr(err) {
			jsonTaskValidationErr(w, err)
			return
		}
		slog.Error("rest: task "+what+" update failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update "+what)
		return
	}
	a.auditTask("task.update", id)
	a.writeWireTask(w, http.StatusOK, *updated)
}

// handleTaskEvidence handles GET /api/v1/tasks/{id}/evidence. Read-only —
// evidence is written only by the evidence-ladder judge (pkg/agent's
// JudgeCriteria via task.EvidenceStore.Record), never via this endpoint. A
// fresh, redaction-less EvidenceStore is constructed on demand for the read
// path (List never touches Redact — only Record does, at write time; the
// persisted records are already redacted), mirroring AgentLoop.evidenceStore's
// on-demand construction in pkg/agent/judge.go.
func (a *restAPI) handleTaskEvidence(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	if _, err := a.taskStore.Get(id); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		slog.Error("rest: task evidence: get task failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}
	es := task.NewEvidenceStore(a.homePath, nil)
	records, err := es.List(id)
	if err != nil {
		slog.Error("rest: task evidence list failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list evidence")
		return
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].RecordedAt < records[j].RecordedAt })
	out := make([]gen.EvidenceRecord, 0, len(records))
	for _, rec := range records {
		out = append(out, toWireEvidenceRecord(rec))
	}
	jsonOK(w, out)
}

// handleTaskVerdicts handles GET /api/v1/tasks/{id}/verdicts. Reads judge
// verdicts from the task's session transcript (EntryTypeJudgeVerdict entries
// written by TaskExecutor.writeJudgeVerdictTranscript, task_executor.go) — the
// durable carrier; the live JudgeVerdictFrame WS push is the other, ephemeral
// carrier of the same shape (Round-1 Grill Reconciliation R3). A task with no
// session, no agent, or no judged attempt yet returns an empty array, not an
// error.
func (a *restAPI) handleTaskVerdicts(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.taskStore.Get(id)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		slog.Error("rest: task verdicts: get task failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}
	out := make([]gen.JudgeVerdict, 0)
	if t.SessionID == "" || t.AgentID == "" || a.agentLoop == nil {
		jsonOK(w, out)
		return
	}
	// Resolved by SESSION id, exactly like every other session-reading REST
	// boundary (getSession/getSessionMessages, rest_sessions.go). Resolving by
	// AGENT was correct only while every task session was minted in the
	// assigned agent's own legacy store; since ADR-091 routes task starts
	// through SteerLauncher.Launch the session lives in the SHARED store, and
	// the per-agent lookup found no transcript.jsonl at all — ReadTranscript
	// answers an empty slice with no error, so this endpoint returned 200 []
	// and the caller could not tell "the Judge never ran" from "we looked in
	// the wrong store".
	sessStore := a.resolveSessionStore(t.SessionID)
	if sessStore == nil {
		jsonOK(w, out)
		return
	}
	entries, rerr := sessStore.ReadTranscript(t.SessionID)
	if rerr != nil {
		slog.Error("rest: task verdicts: read transcript failed",
			"id", id, "session_id", t.SessionID, "error", rerr)
		jsonErr(w, http.StatusInternalServerError, "could not read task verdicts")
		return
	}
	for _, e := range entries {
		if e.Type != session.EntryTypeJudgeVerdict {
			continue
		}
		var v task.JudgeVerdict
		if uerr := json.Unmarshal([]byte(e.Content), &v); uerr != nil {
			slog.Warn("rest: task verdicts: could not parse verdict transcript entry",
				"id", id, "entry_id", e.ID, "error", uerr)
			continue
		}
		out = append(out, toWireJudgeVerdict(v))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Round < out[j].Round })
	jsonOK(w, out)
}

// --- helpers ----------------------------------------------------------------

// emitTaskStatus emits a task_status_changed WS frame for a task mutation.
func (a *restAPI) emitTaskStatus(t *task.Task) {
	if a.agentLoop == nil {
		return
	}
	sessionID := t.SessionID
	if sessionID == "" {
		sessionID = "task:" + t.ID
	}
	a.agentLoop.EmitTaskStatusChanged(agent.TaskStatusChangedPayload{
		TaskID:    t.ID,
		Status:    string(t.Status),
		SessionID: sessionID,
		AgentID:   t.AgentID,
	})
}

// auditTask writes an audit entry for a task mutation (best-effort).
func (a *restAPI) auditTask(event, id string) {
	if a.auditor == nil {
		return
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    event,
		Decision: audit.DecisionAllow,
		Details:  map[string]any{"id": id},
	}); err != nil {
		slog.Error("rest: task audit log failed", "event", event, "error", err)
	}
}

// auditTriggerChange implements FR-022: every save that CHANGES a task's
// recurrence trigger to a new `recurring` (RRULE or legacy cron_expr) rule
// emits an audit entry recording the task id, the prior trigger, and the
// new trigger — covering both the US-5.3 legacy→RRULE conversion and an
// RRULE→RRULE rule change (FR-024's "re-anchor" case) alike, so a change
// that moves every future fire is reconstructible after the fact.
//
// Deliberately scoped narrower than "every trigger patch":
//   - priorTrigger == nil never fires. On task CREATE there is no prior
//     trigger to diff against — "changes a recurrence trigger" presupposes
//     one existed. (handleTaskCreate still calls this — with priorTrigger
//     always nil today — so every trigger-touching write path is
//     uniformly covered by one hook; the guard makes that call a
//     documented no-op rather than a spurious audit entry.)
//   - newTrigger.Type != task.TriggerRecurring never fires — FR-022 is
//     scoped to recurrence trigger changes, not e.g. a save that flips a
//     task to `manual`/`once`.
//   - a byte-identical trigger never fires — this is FR-024's title-only
//     edit ("Save MUST preserve the trigger byte-identical when no
//     recurrence or time field was touched"), which must NOT read as a
//     rule change.
//
// Best-effort: log failures are recorded but never surfaced to the caller,
// matching auditTask's existing behavior.
func (a *restAPI) auditTriggerChange(taskID string, priorTrigger, newTrigger *task.Trigger) {
	if priorTrigger == nil || newTrigger == nil {
		return
	}
	if newTrigger.Type != task.TriggerRecurring {
		return
	}
	if reflect.DeepEqual(*priorTrigger, *newTrigger) {
		return
	}
	if a.auditor == nil {
		return
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    "task.trigger.recurrence_changed",
		Decision: audit.DecisionAllow,
		Details: map[string]any{
			"task_id":       taskID,
			"prior_trigger": priorTrigger,
			"new_trigger":   newTrigger,
		},
	}); err != nil {
		slog.Error("rest: task trigger recurrence-change audit log failed", "task_id", taskID, "error", err)
	}
}

// isTaskValidationErr reports whether err is a user-facing validation error
// (400) rather than an internal failure (500). All store validation errors wrap
// task.ErrValidation (ErrBlockedByCycle, ErrBlockedBySelfEdge,
// ErrBlockedByDepthExceeded, ErrParentCycle, ErrIllegalTransition,
// ErrBlockedNotSettable, and all verr() calls). ErrNotFound is handled
// separately as 404 by every caller — it must NOT match here.
func isTaskValidationErr(err error) bool {
	return errors.Is(err, task.ErrValidation)
}

// taskValidationField attributes a task-validation error to the wire
// ErrorResponse.field a client should route it to inline, via errors.Is
// against the specific sentinel each rule raises — never by parsing the
// (now plain-language, human-facing) message text. Returns "" for a
// validation error that names no single field (e.g. an illegal status
// transition), which callers use to fall back to jsonErr's fieldless body.
func taskValidationField(err error) string {
	switch {
	case errors.Is(err, task.ErrDoDNotDistinct):
		return "dod"
	case errors.Is(err, task.ErrBlockedByCycle),
		errors.Is(err, task.ErrBlockedBySelfEdge),
		errors.Is(err, task.ErrBlockedByDepthExceeded):
		return "blocked_by"
	default:
		return ""
	}
}

// jsonTaskValidationErr writes a task-validation error (already confirmed via
// isTaskValidationErr) as a 400, attaching the wire `field` property when
// taskValidationField recognizes the rejection's sentinel (ADR-068 body
// shape) so the SPA can route the (plain-language) message inline without
// parsing it.
func jsonTaskValidationErr(w http.ResponseWriter, err error) {
	if field := taskValidationField(err); field != "" {
		jsonErrField(w, http.StatusBadRequest, err.Error(), field)
		return
	}
	jsonErr(w, http.StatusBadRequest, err.Error())
}

// errTaskAgentLoopUnavailable is returned by validateTaskAgentID's early
// guards when a.agentLoop or its registry is not yet available. It replaces
// the guards' previous `return nil` (silent allow) — see the fail-closed
// discussion in validateTaskAgentID's docstring. Both current call sites
// (handleTaskCreate, handleTaskPatch) map it to 400 via the same
// `jsonErr(w, http.StatusBadRequest, err.Error())` path every other
// validateTaskAgentID error takes; a caller wanting a dedicated 503 for this
// specific condition can branch on errors.Is(err, errTaskAgentLoopUnavailable).
var errTaskAgentLoopUnavailable = errors.New("task: agent loop not initialized; cannot validate agent_id")

// --- boot reconciliation (folded from board_reconcile.go) -------------------

// reconcileStuckTasks resets any task left `in_progress` by a crashed/abandoned
// previous gateway process to `failed`, so a crash does not strand a task in a
// running state forever. Idempotent; safe when the tasks dir is absent.
//
// ADR-050 RD10 / task-run-history-spec.md §3.5 removed the stuck-run reaper
// (operator decision 2026-07-20: "a run stays in_progress until closed" — no
// liveness-aware background sweep). That leaves a gap this boot reconciler
// must close: a TaskRun opened by the crashed process (task.Store.OpenRun,
// pkg/task/run_store.go) is never told the task it belongs to was reset here,
// so — with no reaper — it would stay `in_progress` FOREVER, even across
// further restarts, diverging from the just-recovered Task. For each task
// this function resets to failed, it also best-effort closes that task's own
// open TaskRun(s) to `failed`, bounding the orphaned-run gap to "next
// restart" (MED-#M2 in the ADR). This does not touch pkg/task's store logic
// (CloseRun already refuses to double-close or accept a non-terminal
// status) — it only calls the existing store API, mirroring how this
// function already only calls taskStore.Update rather than writing task
// files directly.
func (a *restAPI) reconcileStuckTasks() {
	tasks, err := a.taskStore.List(task.Filter{Status: task.StatusInProgress})
	if err != nil {
		slog.Error("rest: reconcile stuck tasks: list failed", "error", err)
		return
	}
	reset := 0
	for _, t := range tasks {
		failed := task.StatusFailed
		result := "interrupted: gateway restarted while task was running"
		now := time.Now().UTC().Format(time.RFC3339)
		if _, uErr := a.taskStore.Update(t.ID, task.Patch{
			Status:      &failed,
			Result:      &result,
			CompletedAt: &now,
		}); uErr != nil {
			slog.Error("rest: reconcile stuck tasks: reset failed", "id", t.ID, "error", uErr)
			continue
		}
		reset++
		a.reconcileStuckTaskRuns(t.ID)
		// GOAL-FR-015/FR-027/FR-028 (review finding C1): this reset is a real
		// terminal write — every task this loop touches was in_progress a
		// moment ago and is `failed` now — so its paired goal record must end
		// with it, exactly as the judged and user-driven endings do. Left
		// unhooked, a single crash-and-restart was enough to leave every
		// in-flight task's goal record permanently ACTIVE, which is also the
		// state that makes the NEXT run of each of those tasks skip
		// Goal.Reactivate and inherit the dead run's counters and criterion
		// statuses. CancelReason is empty here (this is not a user Stop), so
		// the record ends `exhausted`, matching the attempts-exhausted ending.
		tools.TerminateTaskGoalRecord(
			tools.GoalStoreForTasks(a.taskStore), t.ID, task.StatusFailed, "", result)
	}
	if reset > 0 {
		slog.Info("rest: reconcile stuck tasks: reset in_progress→failed on boot", "count", reset)
	}
}

// reconcileStuckTaskRuns closes every currently-open (in_progress) TaskRun
// belonging to taskID to `failed`, called immediately after reconcileStuckTasks
// resets that same task's Task.status. Best-effort and non-fatal: a task with
// zero runs (pre-ADR-050 history, or a task that was never dispatched through
// OpenRun) is not an error, and any store failure is logged at Warn rather
// than aborting the boot reconciliation pass for the remaining stuck tasks.
func (a *restAPI) reconcileStuckTaskRuns(taskID string) {
	runs, err := a.taskStore.ListRuns(taskID)
	if err != nil {
		slog.Warn(
			"rest: reconcile stuck tasks: list runs failed, leaving any open run untouched",
			"task_id",
			taskID,
			"error",
			err,
		)
		return
	}
	closed := 0
	for _, run := range runs {
		if !run.IsOpen() {
			continue
		}
		if cErr := a.taskStore.CloseRun(
			taskID,
			run.RunID,
			task.StatusFailed,
			"abandoned: gateway restarted mid-execution",
		); cErr != nil {
			slog.Warn(
				"rest: reconcile stuck tasks: close orphaned run failed",
				"task_id",
				taskID,
				"run_id",
				run.RunID,
				"error",
				cErr,
			)
			continue
		}
		closed++
	}
	if closed > 0 {
		slog.Info(
			"rest: reconcile stuck tasks: closed orphaned in_progress run(s) on boot",
			"task_id",
			taskID,
			"count",
			closed,
		)
	}
}

// reconcileOrphanBlockedByEdges drops blocked_by edges that point at missing
// task files, so the dependency graph self-heals on boot.
func (a *restAPI) reconcileOrphanBlockedByEdges() {
	removed, err := a.taskStore.DropOrphanEdges()
	if err != nil {
		slog.Error("rest: reconcile orphan blocked_by edges failed", "error", err)
		return
	}
	if removed > 0 {
		slog.Info("rest: dropped orphan blocked_by edges on boot", "count", removed)
	}
}

// --- moved from rest.go 2026-09-15 ---

// --- Tasks ---

// validateEntityID rejects IDs that contain path separators, "..", or null bytes
// to prevent path traversal attacks.
func validateEntityID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("invalid id")
	}
	return nil
}
