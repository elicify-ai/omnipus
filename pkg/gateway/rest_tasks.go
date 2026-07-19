//go:build !cgo

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
	"github.com/elicify-ai/omnipus/pkg/task"
)

// validateMilestoneFK validates that a milestone exists and (when workspaceID is
// non-empty) belongs to the given workspace. Returns a user-facing error on
// failure, nil on success. Folded in from the deleted rest_board.go.
func validateMilestoneFK(homePath, milestoneID, workspaceID string) error {
	m, err := readMilestoneFile(homePath, milestoneID)
	if err != nil {
		return errors.New("milestone not found")
	}
	if workspaceID != "" && m.WorkspaceID != workspaceID {
		return errors.New("milestone does not belong to this workspace")
	}
	return nil
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
//	GET    /tasks                     list (workspace_id/status/agent_id/milestone_id/surface/parent_task_id/limit/offset)
//	POST   /tasks                     create (201, lands in inbox)
//	GET    /tasks/{id}                get one
//	PATCH  /tasks/{id}                partial update
//	DELETE /tasks/{id}                delete (204)
//	GET    /tasks/{id}/subtasks       list children
//	PUT    /tasks/{id}/todos          replace the embedded checklist
//	PUT    /tasks/{id}/dependencies   replace the blocked_by set
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

	sets, err := buildOccurrenceSets(eligible, fromMs, toMs, tz, everyAnchor)
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

// toWireTask converts an internal task.Task to the generated wire type, filling
// the read-time agent_name and rollup fields from the registry / store.
func (a *restAPI) toWireTask(t task.Task) gen.Task {
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
	if t.MilestoneID != "" {
		out.MilestoneId = ptr(t.MilestoneID)
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
	out.Rollup = a.computeRollup(t.ID)
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

// computeRollup returns the read-time roll-up of live child sub-agent runs for
// parentID, or nil when there are none. A "live" child is one that is not yet
// terminal. Never stored on the task record (Detail #6).
func (a *restAPI) computeRollup(parentID string) *[]struct {
	AgentId string               `json:"agent_id"`
	Label   string               `json:"label"`
	Status  gen.TaskRollupStatus `json:"status"`
} {
	children, err := a.taskStore.List(task.Filter{ParentTaskID: parentID, ParentTaskIDSet: true})
	if err != nil || len(children) == 0 {
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
	if !workspaceTeamSet(ws)[agentID] {
		return fmt.Errorf(
			"agent %q is not a member of this workspace's team — add it to the workspace's core team or a delegation edge before assigning tasks to it",
			agentID,
		)
	}
	return nil
}

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
		MilestoneID: q.Get("milestone_id"),
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

	out := make([]gen.Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, a.toWireTask(t))
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
	jsonOK(w, a.toWireTask(*t))
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
	out := make([]gen.Task, 0, len(children))
	for _, t := range children {
		out = append(out, a.toWireTask(t))
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
	if req.MilestoneId != nil && *req.MilestoneId != "" {
		if err := validateMilestoneFK(a.homePath, *req.MilestoneId, req.WorkspaceId); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		t.MilestoneID = *req.MilestoneId
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
	jsonCreated(w, a.toWireTask(*t))
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
	if req.MilestoneId != nil {
		patch.MilestoneID = req.MilestoneId
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
	// freshRunReset, when true, records that this PATCH performed the
	// fresh-run reset below (a "Run now" on a done/failed REPEATING task) and
	// origSessionID/origResult/origArtifacts/origCompletedAt/origFollowedUp
	// hold the PRE-reset values. The launch-failure revert branches further
	// down restore these when freshRunReset is set, so a FAILED "Run now"
	// leaves the task exactly as it was before the click rather than
	// permanently discarding the prior (completed) run's session link,
	// result text, artifacts, and completion timestamp.
	var freshRunReset bool
	var origSessionID, origResult, origCompletedAt string
	var origArtifacts []string
	var origFollowedUp bool
	if req.Status != nil {
		// Read the current status before applying the patch so we can detect
		// transitions rather than just the new state. We need the original status
		// to distinguish "was already in_progress" from "just moved to in_progress".
		if existing, gErr := a.taskStore.Get(id); gErr == nil {
			preUpdateStatus = existing.Status
			// "Run now" on a done/failed REPEATING task is a FRESH run, not a
			// resume: clear the stale session_id + result + artifacts +
			// completed_at + followed_up from the prior run so (a) the
			// in_progress launch guard below (which requires updated.SessionID
			// == "") fires and StartTaskNow mints a new session, and (b) the
			// panel doesn't carry the old run's result/artifacts into the new
			// one. Mirrors SpawnReset's fresh-run reset on a scheduled fire
			// field-for-field (SpawnReset clears session_id, result,
			// artifacts, started_at, completed_at, followed_up). StartedAt is
			// deliberately NOT cleared here — the in_progress transition below
			// (updateLocked's own auto-stamp) re-stamps it to this new run's
			// start, which is exactly the value SpawnReset's own StartedAt
			// clear is making room for. Scoped to repeating triggers so a
			// one-shot/manual resume is unaffected.
			if patch.Status != nil && *patch.Status == task.StatusInProgress &&
				task.IsTerminal(existing.Status) &&
				existing.Trigger.IsRepeating() {
				freshRunReset = true
				origSessionID = existing.SessionID
				origResult = existing.Result
				origArtifacts = existing.Artifacts
				origCompletedAt = existing.CompletedAt
				origFollowedUp = existing.FollowedUp
				empty := ""
				emptyArtifacts := []string{}
				falseVal := false
				patch.SessionID = &empty
				patch.Result = &empty
				patch.Artifacts = &emptyArtifacts
				patch.CompletedAt = &empty
				patch.FollowedUp = &falseVal
			}
		}
	}

	// M-BE1: UpdateWithPrior captures the pre-patch task snapshot under the
	// same per-task lock as the write, so priorTriggerForAudit below is the
	// true immediately-prior state rather than a separately-read, possibly
	// stale one (see the doc comment above patch.Trigger's construction).
	updated, priorForUpdate, err := a.taskStore.UpdateWithPrior(id, patch)
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
	// When freshRunReset fired above (a "Run now" fresh-run reset on a
	// done/failed REPEATING task), the revert ALSO restores the pre-reset
	// session_id/result/artifacts/completed_at/followed_up: those were wiped
	// pre-emptively so the launch guard below and StartTaskNow could mint a
	// fresh run, but if the launch itself never succeeds, the task must land
	// back exactly where it was before the click — not lose the completed
	// prior run's result and chat link. Without this, a retryable 409
	// (dispatch cap) or a 503 (nil executor) would silently and permanently
	// discard that data.
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
			p := task.Patch{Status: &revertStatus, StartedAt: &revertStartedAt}
			if freshRunReset {
				p.SessionID = &origSessionID
				p.Result = &origResult
				p.Artifacts = &origArtifacts
				p.CompletedAt = &origCompletedAt
				p.FollowedUp = &origFollowedUp
			}
			return p
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
			slog.Warn("rest: StartTaskNow failed; task reverted to prior status",
				"id", id, "agent_id", updated.AgentID, "prior_status", preUpdateStatus, "error", startErr)
			httpStatus := http.StatusInternalServerError
			if errors.Is(startErr, agent.ErrDispatchCapReached) {
				httpStatus = http.StatusConflict
			}
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
	jsonOK(w, a.toWireTask(*updated))
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
	jsonOK(w, a.toWireTask(*updated))
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
	}
	if reset > 0 {
		slog.Info("rest: reconcile stuck tasks: reset in_progress→failed on boot", "count", reset)
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
