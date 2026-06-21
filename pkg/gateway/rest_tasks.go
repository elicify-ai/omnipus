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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/task"
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

// --- wire mapping -----------------------------------------------------------

// wireTodo mirrors the gen.Task.Todos element inline type.
type wireTodo = struct {
	Done bool   `json:"done"`
	Text string `json:"text"`
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
			todos = append(todos, wireTodo{Text: td.Text, Done: td.Done})
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
	return &struct {
		Config gen.Task_Trigger_Config `json:"config"`
		Type   gen.TaskTriggerType     `json:"type"`
	}{Config: cfg, Type: gen.TaskTriggerType(tr.Type)}
}

// buildTrigger constructs an internal trigger from its primitive parts. The
// three generated request structs (Task/TaskCreateRequest/TaskUpdateRequest) each
// have their own anonymous trigger type with an identically-shaped config, so
// the callers decompose them and pass the primitives here.
func buildTrigger(kind string, atMs, everyMs *int64, cronExpr *string) *task.Trigger {
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
// registry and is not a worker. A worker is a delegation-only tier and must
// never be human-assigned via the REST surface (it is invoked via delegation).
// Returns nil when the check is skipped (empty registry / empty agent_id).
func (a *restAPI) validateTaskAgentID(agentID string) error {
	if agentID == "" {
		return nil
	}
	if a.agentLoop == nil {
		return nil
	}
	reg := a.agentLoop.GetRegistry()
	if reg == nil || len(reg.ListAgentIDs()) == 0 {
		return nil
	}
	if _, ok := reg.GetAgent(agentID); !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	if reg.IsWorker(agentID) {
		return fmt.Errorf(
			"agent %q is a worker and cannot be directly assigned a task — workers are invoked via delegation",
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
		if err := a.validateTaskAgentID(agentID); err != nil {
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
			t.Todos = append(t.Todos, task.Todo{Text: td.Text, Done: td.Done})
		}
	}
	if req.Trigger != nil {
		t.Trigger = buildTrigger(
			string(req.Trigger.Type),
			req.Trigger.Config.AtMs,
			req.Trigger.Config.EveryMs,
			req.Trigger.Config.CronExpr,
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
			if err := a.validateTaskAgentID(*req.AgentId); err != nil {
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
			todos = append(todos, task.Todo{Text: td.Text, Done: td.Done})
		}
		patch.Todos = &todos
	}
	if req.Trigger != nil {
		tr := buildTrigger(
			string(req.Trigger.Type),
			req.Trigger.Config.AtMs,
			req.Trigger.Config.EveryMs,
			req.Trigger.Config.CronExpr,
		)
		patch.Trigger = &tr
	}
	if req.Due != nil {
		due := req.Due.UTC().Format(time.RFC3339)
		patch.Due = &due
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
		slog.Error("rest: task update failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update task")
		return
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
		slog.Error("rest: task delete failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not delete task")
		return
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
		todos = append(todos, task.Todo{Text: td.Text, Done: td.Done})
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

// isTaskValidationErr reports whether err is a user-facing validation error
// (400) rather than an internal failure (500). All store validation errors wrap
// task.ErrValidation (ErrBlockedByCycle, ErrBlockedBySelfEdge,
// ErrBlockedByDepthExceeded, ErrParentCycle, ErrIllegalTransition,
// ErrBlockedNotSettable, and all verr() calls). ErrNotFound is handled
// separately as 404 by every caller — it must NOT match here.
func isTaskValidationErr(err error) bool {
	return errors.Is(err, task.ErrValidation)
}

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
