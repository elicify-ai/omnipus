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
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

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

// --- wire mapping -----------------------------------------------------------

// wireTodo mirrors the gen.Task.Todos element inline type.
// not-wire-format: this is a local alias for the generated inline struct; see
// the gen.Task.Todos field for the authoritative shape.
type wireTodo = struct {
	Status gen.TaskTodosStatus `json:"status"`
	Text   string              `json:"text"`
}

// rollupIndex groups every task in a per-request snapshot by ParentTaskID, so
// a single upstream task.Store.List call can serve computeRollup for every
// task returned by a list-shaped endpoint. Before this, computeRollup issued
// one additional full Store.List scan PER RETURNED TASK, making
// GET /api/v1/tasks (and GET /tasks/{id}/subtasks) O(n^2) in the number of
// task files on disk — 200 returned tasks against a 2000-task store meant
// ~400k file reads for one request (fix-wave finding #1a). nil is a valid,
// common value of this type: every single-task endpoint (get/create/patch/
// stop/restart/...) passes nil and computeRollup falls back to its own,
// bounded (one-call) List.
type rollupIndex map[string][]task.Task

// buildRollupIndex fetches every task in the store ONCE — an unfiltered
// Store.List, matching computeRollup's own pre-existing (unscoped-by-
// workspace) ParentTaskID filter semantics exactly — and groups the results
// by ParentTaskID for O(1) lookups. handleTaskList and handleTaskSubtasks
// build this once per request and thread it through toWireTask/computeRollup
// for every task in the batch, replacing the previous
// one-List-call-per-returned-task pattern.
func (a *restAPI) buildRollupIndex() (rollupIndex, error) {
	all, err := a.taskStore.List(task.Filter{})
	if err != nil {
		return nil, err
	}
	idx := make(rollupIndex, len(all))
	for _, t := range all {
		if t.ParentTaskID == "" {
			continue
		}
		idx[t.ParentTaskID] = append(idx[t.ParentTaskID], t)
	}
	return idx, nil
}

// toWireTask converts an internal task.Task to the generated wire type, filling
// the read-time agent_name and rollup fields from the registry / store. idx is
// an optional shared rollupIndex (see its doc comment) for batch callers; pass
// nil for a single-task response.
func (a *restAPI) toWireTask(t task.Task, idx rollupIndex) gen.Task {
	out := gen.Task{
		Id:          t.ID,
		Title:       t.Title,
		Action:      gen.TaskAction(t.Action),
		Status:      gen.TaskStatus(t.Status),
		WorkspaceId: t.WorkspaceID,
		Owner:       t.Owner,
		CreatedBy:   t.CreatedBy,
	}
	if out.Action == "" {
		out.Action = gen.TaskAction(task.ActionLLM)
	}

	prio := t.EffectivePriority()
	out.Priority = &prio
	surface := gen.TaskSurface(t.EffectiveSurface())
	out.Surface = &surface

	if t.Description != "" {
		out.Description = ptr(t.Description)
	}
	if t.Prompt != "" {
		out.Prompt = ptr(t.Prompt)
	}
	if t.AgentID != "" {
		out.AgentId = ptr(t.AgentID)
		if name := a.resolveAgentName(t.AgentID); name != "" {
			out.AgentName = ptr(name)
		}
	}
	if len(t.BlockedBy) > 0 {
		bb := append([]string{}, t.BlockedBy...)
		out.BlockedBy = &bb
	}
	if len(t.Todos) > 0 {
		todos := make([]wireTodo, 0, len(t.Todos))
		for _, td := range t.Todos {
			todos = append(todos, wireTodo{Text: td.Text, Status: gen.TaskTodosStatus(td.Status)})
		}
		out.Todos = &todos
	}
	if t.ParentTaskID != "" {
		out.ParentTaskId = ptr(t.ParentTaskID)
	}
	if t.PlanID != "" {
		out.PlanId = ptr(t.PlanID)
	}
	if len(t.WriteSet) > 0 {
		ws := append([]string{}, t.WriteSet...)
		out.WriteSet = &ws
	}
	if t.Stream != "" {
		out.Stream = ptr(t.Stream)
	}
	if t.IsJoin {
		out.IsJoin = ptr(t.IsJoin)
	}
	if len(t.Tags) > 0 {
		tags := append([]string{}, t.Tags...)
		out.Tags = &tags
	}
	if len(t.Criteria) > 0 {
		out.Criteria = toWireCriteria(t.Criteria)
	}
	if t.AttemptCount > 0 {
		out.AttemptCount = ptr(t.AttemptCount)
	}
	if t.MaxAttempts != nil {
		out.MaxAttempts = ptr(*t.MaxAttempts)
	}
	if t.Trigger != nil {
		out.Trigger = toWireTrigger(t.Trigger)
	}
	if t.Due != "" {
		if ts, err := time.Parse(time.RFC3339, t.Due); err == nil {
			out.Due = &ts
		}
	}
	if t.SourceChannel != "" {
		out.SourceChannel = ptr(t.SourceChannel)
	}
	if t.SourceChatID != "" {
		out.SourceChatId = ptr(t.SourceChatID)
	}
	if t.SessionID != "" {
		out.SessionId = ptr(t.SessionID)
	}
	if t.Result != "" {
		out.Result = ptr(t.Result)
	}
	if t.CancelReason != "" {
		cr := gen.TaskCancelReason(t.CancelReason)
		out.CancelReason = &cr
	}
	if len(t.Artifacts) > 0 {
		arts := append([]string{}, t.Artifacts...)
		out.Artifacts = &arts
	}

	out.CreatedAt = parseTimeOrNow(t.CreatedAt)
	out.UpdatedAt = parseTimeOrNow(t.UpdatedAt)
	if t.StartedAt != "" {
		if ts, err := time.Parse(time.RFC3339, t.StartedAt); err == nil {
			out.StartedAt = &ts
		}
	}
	if t.CompletedAt != "" {
		if ts, err := time.Parse(time.RFC3339, t.CompletedAt); err == nil {
			out.CompletedAt = &ts
		}
	}

	// Read-time rollup: live child sub-agent runs (parent_task_id == t.ID).
	out.Rollup = a.computeRollup(t.ID, idx)
	return out
}

// toWireTrigger maps an internal trigger to the gen.Task.Trigger inline type.
// The field type is an anonymous struct wrapping gen.Task_Trigger_Config so we
// build it in place and return via a temp gen.Task to extract the pointer type.
func toWireTrigger(tr *task.Trigger) *struct {
	Config gen.Task_Trigger_Config `json:"config"`
	Type   gen.TaskTriggerType     `json:"type"`
} {
	cfg := gen.Task_Trigger_Config{}
	if tr.Config.AtMs != nil {
		v := *tr.Config.AtMs
		cfg.AtMs = &v
	}
	if tr.Config.EveryMs != nil {
		v := *tr.Config.EveryMs
		cfg.EveryMs = &v
	}
	if tr.Config.CronExpr != nil {
		v := *tr.Config.CronExpr
		cfg.CronExpr = &v
	}
	if tr.Config.Rrule != nil {
		v := *tr.Config.Rrule
		cfg.Rrule = &v
	}
	if tr.Config.DtstartMs != nil {
		v := *tr.Config.DtstartMs
		cfg.DtstartMs = &v
	}
	if tr.Config.Tz != nil {
		v := *tr.Config.Tz
		cfg.Tz = &v
	}
	return &struct {
		Config gen.Task_Trigger_Config `json:"config"`
		Type   gen.TaskTriggerType     `json:"type"`
	}{Config: cfg, Type: gen.TaskTriggerType(tr.Type)}
}

// buildTrigger constructs an internal trigger from its primitive parts. The
// three generated request structs (Task/TaskCreateRequest/TaskUpdateRequest) each
// have their own anonymous trigger type with an identically-shaped config, so
// the callers decompose them and pass the primitives here.
func buildTrigger(
	kind string,
	atMs, everyMs *int64,
	cronExpr, rrule *string,
	dtstartMs *int64,
	tz *string,
) *task.Trigger {
	tr := &task.Trigger{Type: task.TriggerType(kind)}
	if atMs != nil {
		v := *atMs
		tr.Config.AtMs = &v
	}
	if everyMs != nil {
		v := *everyMs
		tr.Config.EveryMs = &v
	}
	if cronExpr != nil {
		v := *cronExpr
		tr.Config.CronExpr = &v
	}
	if rrule != nil {
		v := *rrule
		tr.Config.Rrule = &v
	}
	if dtstartMs != nil {
		v := *dtstartMs
		tr.Config.DtstartMs = &v
	}
	if tz != nil {
		v := *tz
		tr.Config.Tz = &v
	}
	return tr
}

// toWireCriteria converts internal acceptance criteria to the gen.Task.Criteria
// inline wire shape (read path — GET/POST/PATCH responses).
func toWireCriteria(cs []task.AcceptanceCriterion) *[]struct {
	Author struct {
		Id   string                     `json:"id"`
		Kind gen.TaskCriteriaAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                           `json:"max_count,omitempty"`
		MinCount *int                           `json:"min_count,omitempty"`
		Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
		Tool     string                         `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	Id     *string                `json:"id,omitempty"`
	Kind   gen.TaskCriteriaKind   `json:"kind"`
	Status gen.TaskCriteriaStatus `json:"status"`
	Text   string                 `json:"text"`
} {
	out := make([]struct {
		Author struct {
			Id   string                     `json:"id"`
			Kind gen.TaskCriteriaAuthorKind `json:"kind"`
		} `json:"author"`
		Behavior *struct {
			MaxCount *int                           `json:"max_count,omitempty"`
			MinCount *int                           `json:"min_count,omitempty"`
			Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
			Tool     string                         `json:"tool"`
		} `json:"behavior,omitempty"`
		Check *struct {
			Command          string `json:"command"`
			ExpectedExitCode int    `json:"expected_exit_code"`
		} `json:"check,omitempty"`
		Id     *string                `json:"id,omitempty"`
		Kind   gen.TaskCriteriaKind   `json:"kind"`
		Status gen.TaskCriteriaStatus `json:"status"`
		Text   string                 `json:"text"`
	}, 0, len(cs))
	for _, c := range cs {
		item := struct { // not-wire-format: intermediate value built to match gen.Task.Criteria's oapi-codegen anonymous element type, not a parallel wire type
			Author struct {
				Id   string                     `json:"id"`
				Kind gen.TaskCriteriaAuthorKind `json:"kind"`
			} `json:"author"`
			Behavior *struct {
				MaxCount *int                           `json:"max_count,omitempty"`
				MinCount *int                           `json:"min_count,omitempty"`
				Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
				Tool     string                         `json:"tool"`
			} `json:"behavior,omitempty"`
			Check *struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			} `json:"check,omitempty"`
			Id     *string                `json:"id,omitempty"`
			Kind   gen.TaskCriteriaKind   `json:"kind"`
			Status gen.TaskCriteriaStatus `json:"status"`
			Text   string                 `json:"text"`
		}{
			Kind:   gen.TaskCriteriaKind(c.Kind),
			Status: gen.TaskCriteriaStatus(c.Status),
			Text:   c.Text,
		}
		item.Author.Id = c.Author.ID
		item.Author.Kind = gen.TaskCriteriaAuthorKind(c.Author.Kind)
		if c.ID != "" {
			item.Id = ptr(c.ID)
		}
		if c.Check != nil {
			item.Check = &struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			}{Command: c.Check.Command, ExpectedExitCode: c.Check.ExpectedExitCode}
		}
		if c.Behavior != nil {
			beh := &struct { // not-wire-format: intermediate value built to match gen.Task.Criteria's oapi-codegen anonymous element type, not a parallel wire type
				MaxCount *int                           `json:"max_count,omitempty"`
				MinCount *int                           `json:"min_count,omitempty"`
				Scope    *gen.TaskCriteriaBehaviorScope `json:"scope,omitempty"`
				Tool     string                         `json:"tool"`
			}{
				// MinCount/MaxCount are passed straight through — both are
				// already *int on task.CriterionBehavior (fix-wave finding
				// #5), so no ptr()-wrap is needed (or type-correct: wrapping
				// an already-*int value would produce **int). This also
				// preserves the nil/0 distinction on read: an
				// explicitly-zero MinCount round-trips as 0, not defaulted —
				// the wire's own `default: 1` (schema) is authoritative only
				// for an ABSENT create/update request field, never for what
				// GET echoes back (fix-wave finding #6).
				Tool:     c.Behavior.Tool,
				MinCount: c.Behavior.MinCount,
				MaxCount: c.Behavior.MaxCount,
			}
			if c.Behavior.Scope != "" {
				s := gen.TaskCriteriaBehaviorScope(c.Behavior.Scope)
				beh.Scope = &s
			}
			item.Behavior = beh
		}
		out = append(out, item)
	}
	return &out
}

// criteriaFromCreateWire converts the gen.TaskCreateRequest.Criteria inline
// wire shape to internal acceptance criteria (create path).
func criteriaFromCreateWire(items []struct {
	Author struct {
		Id   string                                  `json:"id"`
		Kind gen.TaskCreateRequestCriteriaAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                                        `json:"max_count,omitempty"`
		MinCount *int                                        `json:"min_count,omitempty"`
		Scope    *gen.TaskCreateRequestCriteriaBehaviorScope `json:"scope,omitempty"`
		Tool     string                                      `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	Id     *string                             `json:"id,omitempty"`
	Kind   *gen.TaskCreateRequestCriteriaKind  `json:"kind,omitempty"`
	Status gen.TaskCreateRequestCriteriaStatus `json:"status"`
	Text   string                              `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		// ADR-074 D2 (spec FR-002): the gateway performs NO kind defaulting —
		// an absent kind passes THROUGH as empty and is inferred downstream by
		// the store's normalizeCriteria.
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
		}
		out = append(out, c)
	}
	return out
}

// criteriaFromUpdateWire converts the gen.TaskUpdateRequest.Criteria inline
// wire shape to internal acceptance criteria (PATCH path).
func criteriaFromUpdateWire(items []struct {
	Author struct {
		Id   string                                  `json:"id"`
		Kind gen.TaskUpdateRequestCriteriaAuthorKind `json:"kind"`
	} `json:"author"`
	Behavior *struct {
		MaxCount *int                                        `json:"max_count,omitempty"`
		MinCount *int                                        `json:"min_count,omitempty"`
		Scope    *gen.TaskUpdateRequestCriteriaBehaviorScope `json:"scope,omitempty"`
		Tool     string                                      `json:"tool"`
	} `json:"behavior,omitempty"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	Id     *string                             `json:"id,omitempty"`
	Kind   *gen.TaskUpdateRequestCriteriaKind  `json:"kind,omitempty"`
	Status gen.TaskUpdateRequestCriteriaStatus `json:"status"`
	Text   string                              `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		// ADR-074 D2 (spec FR-002): the gateway performs NO kind defaulting —
		// an absent kind passes THROUGH as empty and is inferred downstream by
		// the store's normalizeCriteria.
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
		}
		out = append(out, c)
	}
	return out
}

// computeRollup returns the read-time roll-up of live child sub-agent runs for
// parentID, or nil when there are none. A "live" child is one that is not yet
// terminal. Never stored on the task record (Detail #6).
//
// When idx is non-nil (a batch caller's shared rollupIndex, see its doc
// comment), children are read from the index — an in-memory grouping already
// built from ONE Store.List call — instead of issuing a fresh List call here.
// idx == nil (every single-task call site) preserves the original behavior:
// one bounded List call scoped to this parentID.
func (a *restAPI) computeRollup(parentID string, idx rollupIndex) *[]struct {
	AgentId string               `json:"agent_id"`
	Label   string               `json:"label"`
	Status  gen.TaskRollupStatus `json:"status"`
} {
	var children []task.Task
	if idx != nil {
		children = idx[parentID]
	} else {
		var err error
		children, err = a.taskStore.List(task.Filter{ParentTaskID: parentID, ParentTaskIDSet: true})
		if err != nil {
			return nil
		}
	}
	if len(children) == 0 {
		return nil
	}
	type rollupItem = struct {
		AgentId string               `json:"agent_id"`
		Label   string               `json:"label"`
		Status  gen.TaskRollupStatus `json:"status"`
	}
	items := make([]rollupItem, 0, len(children))
	for _, c := range children {
		if task.IsTerminal(c.Status) {
			continue
		}
		items = append(items, rollupItem{
			AgentId: c.AgentID,
			Label:   c.Title,
			Status:  gen.TaskRollupStatus(c.Status),
		})
	}
	if len(items) == 0 {
		return nil
	}
	return &items
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

// validateTaskPlanID enforces the same-workspace FK on Task.PlanID (ADR-049
// D1, mirrors the removed validateMilestoneFK): a task may only reference a
// plan that lives in its own workspace. Returns nil when planID is empty
// (nothing to validate). Fails CLOSED (400) when a.planStore is nil — an
// uninitialized dependency is never treated as "no FK to check", mirroring
// validateTaskAgentID's errTaskAgentLoopUnavailable convention immediately
// above.
//
// Also rejects linking to a TERMINAL plan (done/failed): a terminal plan can
// never accept new member tasks, and allowing the attach would leave a member
// pointing at a dead plan. This is data integrity (parity with
// pkg/tools/plan.go's validateTaskPlanLinkage), not a dispatch-approval
// mechanism — task dispatch authority is governed solely by the run_task tool
// policy (allow/deny/ask), enforced in the agent loop.
func (a *restAPI) validateTaskPlanID(planID, workspaceID string) error {
	if planID == "" {
		return nil
	}
	if a.planStore == nil {
		return errTaskPlanStoreUnavailable
	}
	if err := validateEntityID(planID); err != nil {
		return fmt.Errorf("invalid plan_id: %w", err)
	}
	if err := a.planStore.ValidatePlanWorkspace(planID, workspaceID); err != nil {
		return err
	}
	p, err := a.planStore.Get(planID)
	if err != nil {
		return fmt.Errorf("could not load plan %q: %w", planID, err)
	}
	if plan.IsTerminal(p.State) {
		return fmt.Errorf("plan %q is %q (terminal) and cannot accept new member tasks", planID, p.State)
	}
	return nil
}

// errTaskPlanStoreUnavailable is returned by validateTaskPlanID when
// a.planStore is nil — see its doc comment for the fail-closed rationale.
var errTaskPlanStoreUnavailable = errors.New("task: plan store not initialized; cannot validate plan_id")

// resolveAgentName returns the display name for an agent ID from the registry,
// or "" when unknown.
func (a *restAPI) resolveAgentName(agentID string) string {
	if a.agentLoop == nil {
		return ""
	}
	reg := a.agentLoop.GetRegistry()
	if reg == nil {
		return ""
	}
	if ag, ok := reg.GetAgent(agentID); ok {
		return ag.Name
	}
	return ""
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// behaviorFromWire converts a `kind: behavior` criterion's inline wire
// fields to task.CriterionBehavior. Collapses the four byte-identical
// FROM-wire behavior blocks (planDoDFromCreateWire/planDoDFromUpdateWire in
// rest_plans.go, criteriaFromCreateWire/criteriaFromUpdateWire in
// rest_tasks.go) that previously duplicated this same conversion once per
// generated wire-request variant — S is the per-variant generated Scope enum
// type (gen.PlanCreateRequestDodBehaviorScope, gen.TaskCriteriaBehaviorScope,
// etc.), always ~string so a direct conversion to task.BehaviorScope works.
//
// minCount is passed straight through as a pointer — NO default-1
// materialization here. task.CriterionBehavior.MinCount is itself *int
// (fix-wave finding #5): an omitted min_count stays nil on the wire and
// task.validateCriterionBehavior (pkg/task/criterion.go) — via
// EffectiveMinCount() at read time — owns defaulting nil to 1. Duplicating
// that default here would be a second source of truth for the same rule.
//
// The TO-wire direction (toWireCriteria/toWirePlanDoD's anonymous-struct
// literals) is intentionally NOT collapsed here — oapi-codegen generates a
// DISTINCT anonymous struct type per response context, so those literals
// cannot share one helper without reintroducing the wire-shape duplication
// Constraint #8 exists to prevent (see each call site's own comment).
func behaviorFromWire[S ~string](tool string, minCount, maxCount *int, scope *S) *task.CriterionBehavior {
	beh := &task.CriterionBehavior{Tool: tool, MinCount: minCount, MaxCount: maxCount}
	if scope != nil {
		beh.Scope = task.BehaviorScope(*scope)
	}
	return beh
}

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
	out := make([]gen.Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, a.toWireTask(t, idx))
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
	jsonOK(w, a.toWireTask(*t, nil))
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
	out := make([]gen.Task, 0, len(children))
	for _, t := range children {
		out = append(out, a.toWireTask(t, idx))
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
		if err := a.validateTaskPlanID(*req.PlanId, req.WorkspaceId); err != nil {
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
	if req.Criteria != nil {
		t.Criteria = criteriaFromCreateWire(*req.Criteria)
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
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("rest: task create failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not create task")
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
	jsonCreated(w, a.toWireTask(*t, nil))
}

// handleTaskPatch handles PATCH /api/v1/tasks/{id}.
func (a *restAPI) handleTaskPatch(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.TaskUpdateRequest
	if !decodeAndValidate(w, r, "TaskUpdateRequest", &req, validateEnabled) {
		return
	}

	// Fix #6: blocked is a derived side-state; reject it at the gateway seam
	// before reaching the store (defense-in-depth alongside ErrBlockedNotSettable).
	if req.Status != nil && task.Status(*req.Status) == task.StatusBlocked {
		jsonErr(w, http.StatusBadRequest, "blocked is a derived side-state and cannot be set directly")
		return
	}

	// Detail #8: advancing a partial (no prompt/description) task to `next` is
	// rejected — only fully-captured tasks may be triaged.
	if req.Status != nil && task.Status(*req.Status) == task.StatusNext {
		existing, gErr := a.taskStore.Get(id)
		if gErr != nil {
			if errors.Is(gErr, task.ErrNotFound) {
				jsonErr(w, http.StatusNotFound, "task not found")
				return
			}
			jsonErr(w, http.StatusInternalServerError, "could not read task")
			return
		}
		hasPrompt := existing.Prompt != "" || existing.Description != ""
		if req.Prompt != nil && *req.Prompt != "" {
			hasPrompt = true
		}
		if req.Description != nil && *req.Description != "" {
			hasPrompt = true
		}
		if !hasPrompt {
			jsonErr(
				w,
				http.StatusUnprocessableEntity,
				"a partial task cannot be advanced to next — add a prompt or description first",
			)
			return
		}
	}

	patch := task.Patch{}
	// Set when a plan DETACH (plan_id -> "") optimistically adds `status: inbox`
	// to the patch; drives the ErrIllegalTransition retry at the Update below so
	// a terminal/blocked member still detaches instead of 400ing.
	detachResetsStatus := false
	if req.Title != nil {
		patch.Title = req.Title
	}
	if req.Description != nil {
		patch.Description = req.Description
	}
	if req.Prompt != nil {
		patch.Prompt = req.Prompt
	}
	if req.Status != nil {
		st := task.Status(*req.Status)
		patch.Status = &st
	}
	if req.AgentId != nil {
		if *req.AgentId != "" {
			// Team-membership validation is workspace-scoped, and a task's
			// workspace_id is immutable via PATCH (not a TaskUpdateRequest
			// field) — read the existing task to learn it. A dedicated read
			// here (rather than threading through the conditional "next"-status
			// read above) keeps this block correct regardless of which other
			// fields are present in the same PATCH.
			existingForAgentCheck, gErr := a.taskStore.Get(id)
			if gErr != nil {
				if errors.Is(gErr, task.ErrNotFound) {
					jsonErr(w, http.StatusNotFound, "task not found")
					return
				}
				jsonErr(w, http.StatusInternalServerError, "could not read task")
				return
			}
			if err := a.validateTaskAgentID(*req.AgentId, existingForAgentCheck.WorkspaceID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		patch.AgentID = req.AgentId
	}
	if req.Priority != nil {
		patch.Priority = req.Priority
	}
	if req.BlockedBy != nil {
		patch.BlockedBy = req.BlockedBy
	}
	if req.Todos != nil {
		todos := make([]task.Todo, 0, len(*req.Todos))
		for _, td := range *req.Todos {
			todos = append(todos, task.Todo{Text: td.Text, Status: task.TodoStatus(td.Status)})
		}
		patch.Todos = &todos
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
	var priorTriggerForAudit *task.Trigger
	if req.Trigger != nil {
		tr := buildTrigger(
			string(req.Trigger.Type),
			req.Trigger.Config.AtMs,
			req.Trigger.Config.EveryMs,
			req.Trigger.Config.CronExpr,
			req.Trigger.Config.Rrule,
			req.Trigger.Config.DtstartMs,
			req.Trigger.Config.Tz,
		)
		patch.Trigger = &tr
	}
	if req.Due != nil {
		due := req.Due.UTC().Format(time.RFC3339)
		patch.Due = &due
	} else if req.ClearDue != nil && *req.ClearDue {
		// clear_due unambiguously clears the stored due date. Ignored when `due`
		// is set to a value (the value wins). The store applies *patch.Due
		// verbatim, so an empty string clears Task.Due (which omits when empty).
		empty := ""
		patch.Due = &empty
	}
	if req.PlanId != nil {
		if *req.PlanId != "" {
			// A task's workspace_id is immutable via PATCH (not a
			// TaskUpdateRequest field) — read the existing task to learn it,
			// mirroring the AgentId team-membership check's identical
			// dedicated read above.
			existingForPlanCheck, gErr := a.taskStore.Get(id)
			if gErr != nil {
				if errors.Is(gErr, task.ErrNotFound) {
					jsonErr(w, http.StatusNotFound, "task not found")
					return
				}
				jsonErr(w, http.StatusInternalServerError, "could not read task")
				return
			}
			if err := a.validateTaskPlanID(*req.PlanId, existingForPlanCheck.WorkspaceID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
		} else if req.Status == nil {
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
			patch.Status = &inbox
			detachResetsStatus = true
		}
		patch.PlanID = req.PlanId
	}
	if req.WriteSet != nil {
		patch.WriteSet = req.WriteSet
	}
	if req.Stream != nil {
		patch.Stream = req.Stream
	}
	if req.IsJoin != nil {
		patch.IsJoin = req.IsJoin
	}
	if req.Tags != nil {
		patch.Tags = req.Tags
	}
	if req.Criteria != nil {
		criteria := criteriaFromUpdateWire(*req.Criteria)
		patch.Criteria = &criteria
	}
	if req.MaxAttempts != nil {
		v := *req.MaxAttempts
		vp := &v
		patch.MaxAttempts = &vp
	}
	if req.Surface != nil {
		sf := task.Surface(*req.Surface)
		patch.Surface = &sf
	}
	if req.Result != nil {
		patch.Result = req.Result
	}
	if req.Artifacts != nil {
		patch.Artifacts = req.Artifacts
	}
	if req.StartedAt != nil {
		sa := req.StartedAt.UTC().Format(time.RFC3339)
		patch.StartedAt = &sa
	}
	if req.CompletedAt != nil {
		ca := req.CompletedAt.UTC().Format(time.RFC3339)
		patch.CompletedAt = &ca
	}

	// Capture the pre-update status to detect the in_progress transition below.
	var preUpdateStatus task.Status
	if req.Status != nil {
		// Read the current status before applying the patch so we can detect
		// transitions rather than just the new state. We need the original status
		// to distinguish "was already in_progress" from "just moved to in_progress".
		if existing, gErr := a.taskStore.Get(id); gErr == nil {
			preUpdateStatus = existing.Status
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
			if patch.Status != nil && *patch.Status == task.StatusInProgress &&
				task.IsTerminal(existing.Status) &&
				existing.Trigger.IsRepeating() {
				jsonErr(w, http.StatusBadRequest,
					"cannot re-run a repeating task via PATCH status; use POST /api/v1/tasks/"+id+"/runs instead")
				return
			}
		}
	}

	// FR-022/M-BE1: UpdateWithPrior (not a plain Update) so the "prior trigger"
	// snapshot below is captured atomically under the SAME per-task lock as
	// this write — see priorTriggerForAudit's own doc comment above for the
	// TOCTOU window a separate pre-patch Get() would have.
	updated, priorForUpdate, err := a.taskStore.UpdateWithPrior(id, patch)
	if err != nil && detachResetsStatus && errors.Is(err, task.ErrIllegalTransition) {
		// The detach above optimistically added `status: inbox`. A terminal or
		// `blocked` member legitimately refuses that (done is frozen; blocked
		// may only leave via the store's own recompute), but the DETACH itself
		// must still succeed rather than 400 — those states cannot be
		// auto-dispatched anyway, so the laundering risk this reset exists to
		// close does not apply to them. Retry with the plan_id clear alone and
		// let recomputeBlockedStateLocked (which runs at the end of every
		// Update) settle `blocked`.
		patch.Status = nil
		updated, priorForUpdate, err = a.taskStore.UpdateWithPrior(id, patch)
	}
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		if isTaskValidationErr(err) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("rest: task update failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update task")
		return
	}
	if req.Trigger != nil && priorForUpdate != nil {
		priorTriggerForAudit = priorForUpdate.Trigger
	}

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
	if req.Status != nil &&
		updated.Status == task.StatusInProgress &&
		preUpdateStatus != task.StatusInProgress &&
		updated.AgentID != "" &&
		updated.SessionID == "" {
		// buildLaunchRevertPatch assembles the revert-to-prior-state patch shared
		// by both failure branches below (nil executor, StartTaskNow error).
		buildLaunchRevertPatch := func() task.Patch {
			revertStatus := preUpdateStatus
			revertStartedAt := ""
			return task.Patch{Status: &revertStatus, StartedAt: &revertStartedAt}
		}
		if a.taskExecutor == nil {
			// Revert the task to its prior status so it is not left stranded.
			revertPatch := buildLaunchRevertPatch()
			if _, rErr := a.taskStore.Update(id, revertPatch); rErr != nil {
				slog.Error("rest: could not revert task status after nil-executor failure",
					"id", id, "revert_to", preUpdateStatus, "error", rErr)
			}
			slog.Warn("rest: taskExecutor is nil; rejecting in_progress transition",
				"id", id, "agent_id", updated.AgentID)
			jsonErr(w, http.StatusServiceUnavailable, "task executor is not available; retry later")
			return
		}
		sessID, startErr := a.taskExecutor.StartTaskNow(r.Context(), id)
		if startErr != nil {
			// Revert the task to its prior status so it is not left stranded.
			revertPatch := buildLaunchRevertPatch()
			if _, rErr := a.taskStore.Update(id, revertPatch); rErr != nil {
				slog.Error("rest: could not revert task status after StartTaskNow failure",
					"id", id, "revert_to", preUpdateStatus, "error", rErr)
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
				"id", id, "agent_id", updated.AgentID, "prior_status", preUpdateStatus, "error", startErr)
			jsonErr(w, httpStatus, startErr.Error())
			return
		}
		if sessID != "" {
			// Re-read the persisted task so the response contains the session_id.
			if fresh, rerr := a.taskStore.Get(id); rerr == nil {
				updated = fresh
			}
		}
	}

	// If the task reached `done`, advance any dependents (blocked → next).
	if updated.Status == task.StatusDone {
		if advanced, advErr := a.taskStore.AdvanceBlockedDependents(id); advErr != nil {
			slog.Warn("rest: task advance dependents failed", "id", id, "error", advErr)
		} else if len(advanced) > 0 {
			slog.Info("rest: completed task advanced dependents", "completed_id", id, "advanced_ids", advanced)
		}
	}

	a.auditTask("task.update", id)
	if req.Trigger != nil {
		// FR-022: audit a recurrence-trigger change (legacy→RRULE or
		// RRULE→RRULE) — no-ops on a title-only edit (byte-identical
		// trigger, FR-024) or a non-recurring new trigger.
		a.auditTriggerChange(id, priorTriggerForAudit, updated.Trigger)
	}
	a.emitTaskStatus(updated)
	// Re-sync the task's time trigger: a changed/added/removed trigger or a move
	// to a terminal status (re)registers or removes its cron job.
	if a.agentLoop != nil {
		a.agentLoop.NotifyTaskUpserted(updated)
	}
	jsonOK(w, a.toWireTask(*updated, nil))
}

// handleTaskDelete handles DELETE /api/v1/tasks/{id} → 204.
func (a *restAPI) handleTaskDelete(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
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
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("rest: task "+what+" update failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update "+what)
		return
	}
	a.auditTask("task.update", id)
	jsonOK(w, a.toWireTask(*updated, nil))
}

// --- evidence / verdicts / stop (ADR-049 D2, Wave 2-C1 deferred REST paths) --

// toWireEvidenceRecord converts an internal task.EvidenceRecord to the
// generated wire type.
func toWireEvidenceRecord(r task.EvidenceRecord) gen.EvidenceRecord {
	return gen.EvidenceRecord{
		Id:           r.ID,
		TaskId:       r.TaskID,
		CriterionId:  r.CriterionID,
		Attempt:      r.Attempt,
		Command:      r.Command,
		ExitCode:     r.ExitCode,
		Output:       r.Output,
		Truncated:    r.Truncated,
		TimedOut:     r.TimedOut,
		PolicyDenied: r.PolicyDenied,
		RecordedAt:   parseTimeOrNow(r.RecordedAt),
	}
}

// toWireJudgeVerdict converts an internal task.JudgeVerdict to the generated
// wire type.
func toWireJudgeVerdict(v task.JudgeVerdict) gen.JudgeVerdict {
	out := gen.JudgeVerdict{
		Id:           v.ID,
		Scope:        gen.JudgeVerdictScope(v.Scope),
		Round:        v.Round,
		Met:          v.Met,
		Model:        v.Model,
		JudgedAt:     parseTimeOrNow(v.JudgedAt),
		JudgeAgentId: v.JudgeAgentID,
	}
	if v.TaskID != "" {
		out.TaskId = ptr(v.TaskID)
	}
	if v.PlanID != "" {
		out.PlanId = ptr(v.PlanID)
	}
	// Fix-wave finding #3: PerCriterion is a required array on the wire
	// (openapi_types.gen.go, no `omitempty`) — a nil slice marshals as JSON
	// `null`, which fails the SPA's zod schema for a required array and gets
	// dropped. An empty (zero-criteria) verdict must still round-trip as `[]`,
	// so start from a non-nil, empty slice rather than appending onto a nil
	// one.
	out.PerCriterion = make([]struct {
		CriterionId string `json:"criterion_id"`
		Met         bool   `json:"met"`
		Reason      string `json:"reason"`
	}, 0, len(v.PerCriterion))
	for _, c := range v.PerCriterion {
		out.PerCriterion = append(out.PerCriterion, struct {
			CriterionId string `json:"criterion_id"`
			Met         bool   `json:"met"`
			Reason      string `json:"reason"`
		}{CriterionId: c.CriterionID, Met: c.Met, Reason: c.Reason})
	}
	return out
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
	sessStore := a.agentLoop.GetAgentStore(t.AgentID)
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

// handleTaskStop handles POST /api/v1/tasks/{id}/stop (ADR-052 US-7/FR-025).
// Delegates to PlanEngine.StopTask, which cancels the task's own worker
// session AND its registered verifier session (if adjudication is in
// flight — via the same RequestCancelForSession primitive Tier A /cancel
// uses, a safe no-op when no turn is active) and marks the task
// failed+cancel_reason=stopped_by_user. Works identically for a standalone
// task or a SINGLE in-plan member (member-Stop, A5) — StopTask deliberately
// does not touch the task's plan; the plan's other independent members keep
// running (the engine's own reactive dispatch loop evaluates FR-041 "no
// further progress possible" on its next pass, triggered by the
// task_status_changed event StopTask's cancelMemberLocked already emits).
// Rejected 400 when the task is not currently in_progress (nothing running
// to stop — StopTask's own precondition; checked here first for a clearer
// error message and to avoid engaging the engine for an obviously-invalid
// call).
func (a *restAPI) handleTaskStop(w http.ResponseWriter, r *http.Request, id string) {
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
		slog.Error("rest: task stop: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}
	if t.Status != task.StatusInProgress {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("task is %q; only an in-progress task can be stopped", t.Status))
		return
	}

	pe := agent.GetPlanEngine(a.agentLoop)
	if pe == nil {
		jsonErr(w, http.StatusServiceUnavailable, "plan engine is not available")
		return
	}
	c := a.callerIdentity(r)
	updated, serr := pe.StopTask(r.Context(), id, c.Username, "system")
	if serr != nil {
		if errors.Is(serr, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		// TOCTOU between the handler's own precheck (above) and StopTask's
		// own authoritative re-check under planDecisionMu: the task left
		// in_progress in that window. That has two, NOT equivalent, causes:
		//
		//  1. A concurrent Stop request for the SAME task won the race —
		//     the task is now failed+CancelReasonStoppedByUser. This
		//     caller's own request achieved nothing itself, but the outcome
		//     it asked for ("this task is stopped") is already true. A stop
		//     that achieved its purpose must not be reported as an error —
		//     idempotent 200, not 409 (this is the bug this branch used to
		//     have: it mapped ANY non-in_progress re-read to 409, including
		//     this one).
		//  2. The task reached some OTHER terminal state on its own
		//     (completed normally, or failed for an unrelated reason) —
		//     that is a genuine conflict: this Stop request cannot apply,
		//     and never did. 409, same as before.
		//
		// taskStopConflictOutcome makes that call from the re-read task's
		// own Status/CancelReason (see its doc for the full state table). A
		// re-read failure (rerr != nil — including the task having vanished
		// between the two reads) or a re-read that STILL shows in_progress
		// means StopTask's error had nothing to do with the task's status
		// (e.g. an I/O failure from inside the engine's own Get or its
		// cancelMemberLocked write) — falls through to the generic 500
		// below, same mapping handlePlanStop uses for its own non-status
		// engine failures.
		if reread, rerr := a.taskStore.Get(id); rerr == nil {
			if outcome, handled := taskStopConflictOutcome(reread); handled {
				if outcome.alreadyStopped {
					a.auditTask("task.stop", id)
					jsonOK(w, a.toWireTask(*reread, nil))
					return
				}
				jsonErr(w, http.StatusConflict, outcome.message)
				return
			}
		}
		slog.Error("rest: task stop: engine stop failed", "id", id, "error", serr)
		jsonErr(w, http.StatusInternalServerError, "could not stop task")
		return
	}
	// StopTask's own cancelMemberLocked already emits task_status_changed —
	// do not double-emit here. Audit logging remains a REST-layer concern
	// (the engine package writes no audit entries).
	a.auditTask("task.stop", id)
	jsonOK(w, a.toWireTask(*updated, nil))
}

// taskStopOutcome is what taskStopConflictOutcome decided for a task re-read
// after PlanEngine.StopTask has already reported an error for it.
type taskStopOutcome struct {
	// alreadyStopped is true when the task is now failed with
	// CancelReasonStoppedByUser — some request (not necessarily this one)
	// already stopped it. handleTaskStop maps this to 200: the caller's
	// stop achieved its purpose.
	alreadyStopped bool
	// message is the 409 body text; set only when alreadyStopped is false.
	message string
}

// taskStopConflictOutcome maps a task re-read taken AFTER PlanEngine.StopTask
// has already returned an error for taskID to the correct REST outcome. It
// distinguishes "this task is now stopped, just not by this exact request"
// (idempotent success) from a genuine conflict (409): only the task's own
// Status/CancelReason on disk can tell those apart, since StopTask's error
// text alone does not (both cases produce the identical "task %q is %s, not
// in_progress" wrapping, see plan_engine.go's StopTask).
//
// handled is false when t is still in_progress: StopTask's error then can't
// be explained by the task's own status having moved (an I/O failure
// surfaced from inside the engine), and the caller should fall back to its
// own generic 500 rather than treat this as a status-driven outcome.
func taskStopConflictOutcome(t *task.Task) (outcome taskStopOutcome, handled bool) {
	if t.Status == task.StatusInProgress {
		return taskStopOutcome{}, false
	}
	if t.Status == task.StatusFailed && t.CancelReason == task.CancelReasonStoppedByUser {
		return taskStopOutcome{alreadyStopped: true}, true
	}
	return taskStopOutcome{
		message: fmt.Sprintf("task is %q; only an in-progress task can be stopped", t.Status),
	}, true
}

// handleTaskRestart handles POST /api/v1/tasks/{id}/restart (ADR-052
// FR-026, the ▶ Play route for a standalone task previously stopped by the
// user). Per contracts/openapi.yaml's restartTask: rejected 409 when the
// task belongs to a plan (restart the plan instead, via
// POST /plans/{id}/restart, which re-runs its non-done members — G4) or is
// not in a restartable state — `failed` with cancel_reason
// stopped_by_user, specifically. Unlike a plan restart's member reset (which
// un-freezes failed->next for ANY reason, FR-016/DS-5), this standalone-task
// endpoint is reason-gated the same way the plan-level restart guard is: a
// genuinely-failed standalone task (attempts exhausted) is not restartable
// here — same "author fresh" posture as a genuinely-failed plan.
func (a *restAPI) handleTaskRestart(w http.ResponseWriter, id string) {
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
		slog.Error("rest: task restart: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}
	if t.PlanID != "" {
		jsonErr(w, http.StatusConflict,
			"task is a plan member; restart the plan instead via POST /plans/{id}/restart")
		return
	}
	// Gate delegated to task.ValidateStandaloneRestart (pkg/task/store.go) —
	// single source of truth mirroring plan.ValidateRestartTransition,
	// rather than an inline reason check hardcoded here. The handler keeps
	// its own specific, actionable 409 message; the helper's error is
	// wrapped rather than surfaced verbatim.
	if verr := task.ValidateStandaloneRestart(t.Status, t.CancelReason); verr != nil {
		jsonErr(w, http.StatusConflict, fmt.Sprintf(
			"task is %q (cancel_reason=%q); only a task stopped by the user "+
				"(status=failed, cancel_reason=stopped_by_user) can be restarted: %s",
			t.Status, t.CancelReason, verr))
		return
	}

	updated, rerr := a.taskStore.RestartReset(id)
	if rerr != nil {
		if errors.Is(rerr, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		if errors.Is(rerr, task.ErrNotRestartable) {
			jsonErr(w, http.StatusConflict, rerr.Error())
			return
		}
		slog.Error("rest: task restart: reset failed", "id", id, "error", rerr)
		jsonErr(w, http.StatusInternalServerError, "could not restart task")
		return
	}
	a.auditTask("task.restart", id)
	a.emitTaskStatus(updated)
	if a.agentLoop != nil {
		a.agentLoop.NotifyTaskUpserted(updated)
	}
	jsonOK(w, a.toWireTask(*updated, nil))
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
