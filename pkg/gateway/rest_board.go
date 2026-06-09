//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// boardTask mirrors the on-disk format of ~/.omnipus/tasks/{id}.json for GTD tasks.
type boardTask struct { // not-wire-format: internal disk-cache struct, never emitted directly over the wire
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt,omitempty"`       // Agent execution instruction (multiline markdown); max 10000 chars
	Priority    int    `json:"priority,omitempty"`     // 1 (critical) – 5 (low); 0 = unset (treated as 3 on read)
	MilestoneID string `json:"milestone_id,omitempty"` // optional FK to milestone in same project
	SessionID   string `json:"session_id,omitempty"`   // set by system when agent starts; links to chat session
	Result      string `json:"result,omitempty"`       // execution output; set on done/failed
	Status      string `json:"status"`
	ProjectID   string `json:"project_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// gtdStatuses is the set of valid GTD task status values.
// Workflow tasks (pkg/taskstore) use queued/assigned/running/completed/failed — never these.
var gtdStatuses = map[string]bool{
	"inbox":   true,
	"next":    true,
	"active":  true,
	"waiting": true,
	"done":    true,
	"failed":  true,
}

// isGTDTask returns true when status is a known GTD status value.
func isGTDTask(status string) bool {
	return gtdStatuses[status]
}

// boardTasksDir returns the absolute path of ~/.omnipus/tasks/.
func (a *restAPI) boardTasksDir() string {
	return filepath.Join(a.homePath, "tasks")
}

// writeBoardTask atomically persists a boardTask to disk.
func (a *restAPI) writeBoardTask(t boardTask) error {
	dir := a.boardTasksDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, t.ID+".json")
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// readBoardTask reads a single GTD task from disk by ID.
// Returns os.ErrNotExist when the file is absent or when the file exists but
// is not a GTD task (e.g. it is a workflow task with status queued/running/etc.).
func (a *restAPI) readBoardTask(id string) (boardTask, error) {
	path := filepath.Join(a.boardTasksDir(), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return boardTask{}, err
	}
	var t boardTask
	if err := json.Unmarshal(data, &t); err != nil {
		return boardTask{}, err
	}
	// Reject workflow tasks (status ∈ {queued,assigned,running,completed,failed})
	// and tasks with no name field — those are taskstore entities, not GTD tasks.
	if t.Name == "" || !isGTDTask(t.Status) {
		return boardTask{}, os.ErrNotExist
	}
	return t, nil
}

// listBoardTasks reads all GTD task JSON files from the tasks directory.
// Unreadable/corrupt files are logged at Warn. Workflow tasks (status ∈
// {queued,assigned,running,completed,failed}) are silently skipped.
func (a *restAPI) listBoardTasks() ([]boardTask, error) {
	dir := a.boardTasksDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []boardTask
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := name[:len(name)-5]
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			slog.Warn("rest: board task: skipping unreadable file", "file", name, "error", err)
			continue
		}
		var t boardTask
		if err := json.Unmarshal(data, &t); err != nil {
			slog.Warn("rest: board task: skipping corrupt file", "file", name, "error", err)
			continue
		}
		// Skip workflow tasks and tasks with no name field.
		if t.Name == "" || !isGTDTask(t.Status) {
			continue
		}
		if t.ID == "" {
			t.ID = id
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// toWireBoardTask converts the internal boardTask to the generated wire type.
// priority defaults to 3 (FR-L2-007) when the on-disk value is 0 (absent in legacy files).
func toWireBoardTask(t boardTask) gen.BoardTask {
	var desc *string
	if t.Description != "" {
		d := t.Description
		desc = &d
	}
	var projectID *string
	if t.ProjectID != "" {
		p := t.ProjectID
		projectID = &p
	}
	var agentID *string
	if t.AgentID != "" {
		ag := t.AgentID
		agentID = &ag
	}
	var prompt *string
	if t.Prompt != "" {
		pr := t.Prompt
		prompt = &pr
	}
	// priority defaults to 3 when absent in legacy files (value 0 on disk).
	priority := t.Priority
	if priority == 0 {
		priority = 3
	}
	prio := priority
	var milestoneID *string
	if t.MilestoneID != "" {
		m := t.MilestoneID
		milestoneID = &m
	}
	var sessionID *string
	if t.SessionID != "" {
		s := t.SessionID
		sessionID = &s
	}
	var result *string
	if t.Result != "" {
		r := t.Result
		result = &r
	}

	createdAt, err := time.Parse(time.RFC3339, t.CreatedAt)
	if err != nil {
		slog.Warn("rest: board task: invalid created_at timestamp", "id", t.ID, "raw", t.CreatedAt)
		createdAt = time.Now().UTC()
	}
	updatedAt, err := time.Parse(time.RFC3339, t.UpdatedAt)
	if err != nil {
		slog.Warn("rest: board task: invalid updated_at timestamp", "id", t.ID, "raw", t.UpdatedAt)
		updatedAt = time.Now().UTC()
	}

	return gen.BoardTask{
		Id:          t.ID,
		Name:        t.Name,
		Description: desc,
		Prompt:      prompt,
		Priority:    &prio,
		MilestoneId: milestoneID,
		SessionId:   sessionID,
		Result:      result,
		Status:      gen.BoardTaskStatus(t.Status),
		ProjectId:   projectID,
		AgentId:     agentID,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
}

// handleBoardTaskList handles GET /api/v1/board/tasks with optional filters.
func (a *restAPI) handleBoardTaskList(w http.ResponseWriter, r *http.Request) {
	projectFilter := r.URL.Query().Get("project_id")
	statusFilter := r.URL.Query().Get("status")
	agentFilter := r.URL.Query().Get("agent_id")
	milestoneFilter := r.URL.Query().Get("milestone_id")

	// Parse limit (default 200, max 1000 per spec).
	limit := 200
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if v, err := strconv.Atoi(lStr); err == nil && v > 0 {
			if v > 1000 {
				v = 1000
			}
			limit = v
		}
	}
	// Parse offset (default 0).
	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if v, err := strconv.Atoi(oStr); err == nil && v >= 0 {
			offset = v
		}
	}

	if statusFilter != "" && !gtdStatuses[statusFilter] {
		jsonErr(w, http.StatusBadRequest, "invalid status filter")
		return
	}

	all, err := a.listBoardTasks()
	if err != nil {
		slog.Error("rest: board task: list failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list board tasks")
		return
	}

	// Apply project_id, status, agent_id, milestone_id filters.
	filtered := make([]boardTask, 0, len(all))
	for _, t := range all {
		if projectFilter != "" && t.ProjectID != projectFilter {
			continue
		}
		if statusFilter != "" && t.Status != statusFilter {
			continue
		}
		if agentFilter != "" && t.AgentID != agentFilter {
			continue
		}
		if milestoneFilter != "" && t.MilestoneID != milestoneFilter {
			continue
		}
		filtered = append(filtered, t)
	}

	// Sort newest-first by created_at.
	sort.Slice(filtered, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, filtered[i].CreatedAt)
		tj, _ := time.Parse(time.RFC3339, filtered[j].CreatedAt)
		return ti.After(tj)
	})

	total := len(filtered)

	// Apply pagination.
	if offset >= len(filtered) {
		filtered = filtered[:0]
	} else {
		filtered = filtered[offset:]
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
	}

	// Build the response. BoardTaskListResponse.Items is an inline anonymous struct
	// in the generated code, so we use a local shape that is JSON-identical.
	// gen.BoardTaskListResponse.Items is an inline anonymous struct in the generated code;
	// use a local shim until oapi-codegen emits a named type for the item shape.
	type listItem struct { // not-wire-format: local shim for anonymous generated struct item, not a hand-written wire type
		AgentId     *string                              `json:"agent_id,omitempty"`
		CreatedAt   time.Time                            `json:"created_at"`
		Description *string                              `json:"description,omitempty"`
		Id          string                               `json:"id"`
		MilestoneId *string                              `json:"milestone_id,omitempty"`
		Name        string                               `json:"name"`
		Priority    *int                                 `json:"priority,omitempty"`
		ProjectId   *string                              `json:"project_id,omitempty"`
		Prompt      *string                              `json:"prompt,omitempty"`
		Result      *string                              `json:"result,omitempty"`
		SessionId   *string                              `json:"session_id,omitempty"`
		Status      gen.BoardTaskListResponseItemsStatus `json:"status"`
		UpdatedAt   time.Time                            `json:"updated_at"`
	}
	type boardTaskListShim struct { // not-wire-format: wrapper for anonymous generated struct, mirrors BoardTaskListResponse exactly
		Items []listItem `json:"items"`
		Total int        `json:"total"`
	}

	items := make([]listItem, 0, len(filtered))
	for _, t := range filtered {
		wt := toWireBoardTask(t)
		items = append(items, listItem{
			AgentId:     wt.AgentId,
			CreatedAt:   wt.CreatedAt,
			Description: wt.Description,
			Id:          wt.Id,
			MilestoneId: wt.MilestoneId,
			Name:        wt.Name,
			Priority:    wt.Priority,
			ProjectId:   wt.ProjectId,
			Prompt:      wt.Prompt,
			Result:      wt.Result,
			SessionId:   wt.SessionId,
			// t.Status is a validated GTD status string (readBoardTask/listBoardTasks
			// reject non-GTD values); convert via the source string rather than an
			// enum-to-enum cast.
			Status:    gen.BoardTaskListResponseItemsStatus(t.Status),
			UpdatedAt: wt.UpdatedAt,
		})
	}

	jsonOK(w, boardTaskListShim{Items: items, Total: total})
}

// handleBoardTaskGet handles GET /api/v1/board/tasks/{id}.
func (a *restAPI) handleBoardTaskGet(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.readBoardTask(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read board task")
		return
	}
	jsonOK(w, toWireBoardTask(t))
}

// handleBoardTaskPost handles POST /api/v1/board/tasks → 201 Created.
func (a *restAPI) handleBoardTaskPost(w http.ResponseWriter, r *http.Request) {
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.CreateBoardTaskJSONBody
	if !decodeAndValidate(w, r, "CreateBoardTaskJSONBody", &req, validateEnabled) {
		return
	}

	if req.Name == "" {
		jsonErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 200 {
		jsonErr(w, http.StatusBadRequest, "name must be 200 characters or fewer")
		return
	}

	// Determine status; default to inbox.
	status := "inbox"
	if req.Status != nil && string(*req.Status) != "" {
		status = string(*req.Status)
	}
	if !isGTDTask(status) {
		jsonErr(
			w,
			http.StatusBadRequest,
			"status must be one of: inbox, next, active, waiting, done, failed",
		)
		return
	}

	projectID := ""
	if req.ProjectId != nil {
		projectID = *req.ProjectId
		if len(projectID) > 50 {
			jsonErr(w, http.StatusBadRequest, "project_id must be 50 characters or fewer")
			return
		}
		if projectID != "" {
			if err := validateEntityID(projectID); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid project_id")
				return
			}
			if _, projErr := readProjectFile(a.homePath, projectID); errors.Is(
				projErr,
				errProjectNotFound,
			) ||
				errors.Is(projErr, os.ErrNotExist) {
				jsonErr(w, http.StatusBadRequest, "project not found")
				return
			} else if projErr != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to validate project_id")
				return
			}
		}
	}

	agentID := ""
	if req.AgentId != nil {
		agentID = *req.AgentId
		if len(agentID) > 50 {
			jsonErr(w, http.StatusBadRequest, "agent_id must be 50 characters or fewer")
			return
		}
		if agentID != "" {
			if err := validateEntityID(agentID); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid agent_id")
				return
			}
		}
	}

	description := ""
	if req.Description != nil {
		description = *req.Description
		if len(description) > 2000 {
			jsonErr(w, http.StatusBadRequest, "description must be 2000 characters or fewer")
			return
		}
	}

	prompt := ""
	if req.Prompt != nil {
		prompt = *req.Prompt
		if len(prompt) > 10000 {
			jsonErr(w, http.StatusBadRequest, "prompt must be 10000 characters or fewer")
			return
		}
	}

	// priority: default 3; validate 1–5 if supplied.
	priority := 3
	if req.Priority != nil {
		priority = *req.Priority
		if priority < 1 || priority > 5 {
			jsonErr(w, http.StatusBadRequest, "priority must be between 1 and 5")
			return
		}
	}

	milestoneID := ""
	if req.MilestoneId != nil {
		milestoneID = *req.MilestoneId
		if milestoneID != "" {
			// Validate milestone FK: must exist and belong to the same project.
			if err := validateMilestoneFK(a.homePath, milestoneID, projectID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	t := boardTask{
		ID:          ulid.Make().String(),
		Name:        req.Name,
		Description: description,
		Prompt:      prompt,
		Priority:    priority,
		MilestoneID: milestoneID,
		Status:      status,
		ProjectID:   projectID,
		AgentID:     agentID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := a.writeBoardTask(t); err != nil {
		slog.Error("rest: board task: create failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not create board task")
		return
	}

	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    "board_task.create",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"id": t.ID},
		}); err != nil {
			slog.Error(
				"rest: board task: audit log failed",
				"event",
				"board_task.create",
				"error",
				err,
			)
		}
	}
	jsonCreated(w, toWireBoardTask(t))
}

// handleBoardTaskPut handles PUT /api/v1/board/tasks/{id} → 200 with updated task.
func (a *restAPI) handleBoardTaskPut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}

	// Read existing task (must be a GTD task — returns 404 if not found or not GTD).
	existing, err := a.readBoardTask(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: put read failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read board task")
		return
	}

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.UpdateBoardTaskJSONBody
	if !decodeAndValidate(w, r, "UpdateBoardTaskJSONBody", &req, validateEnabled) {
		return
	}

	// Check agent-context header — required for setting status=active or session_id.
	isAgentContext := r.Header.Get("X-Omnipus-Agent-Context") == "true"

	// Apply partial update (only provided non-nil fields).
	if req.Name != nil {
		if *req.Name == "" {
			jsonErr(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		if len(*req.Name) > 200 {
			jsonErr(w, http.StatusBadRequest, "name must be 200 characters or fewer")
			return
		}
		existing.Name = *req.Name
	}
	if req.Description != nil {
		if len(*req.Description) > 2000 {
			jsonErr(w, http.StatusBadRequest, "description must be 2000 characters or fewer")
			return
		}
		existing.Description = *req.Description
	}
	if req.Prompt != nil {
		if len(*req.Prompt) > 10000 {
			jsonErr(w, http.StatusBadRequest, "prompt must be 10000 characters or fewer")
			return
		}
		existing.Prompt = *req.Prompt
	}
	if req.Priority != nil {
		if *req.Priority < 1 || *req.Priority > 5 {
			jsonErr(w, http.StatusBadRequest, "priority must be between 1 and 5")
			return
		}
		existing.Priority = *req.Priority
	}
	if req.Result != nil {
		if len(*req.Result) > 50000 {
			jsonErr(w, http.StatusBadRequest, "result must be 50000 characters or fewer")
			return
		}
		existing.Result = *req.Result
	}
	if req.Status != nil {
		newStatus := string(*req.Status)
		if !isGTDTask(newStatus) {
			jsonErr(
				w,
				http.StatusBadRequest,
				"status must be one of: inbox, next, active, waiting, done, failed",
			)
			return
		}
		// active status can only be set by an agent (FR-L2-006).
		if newStatus == "active" && !isAgentContext {
			jsonErr(w, http.StatusForbidden, "status 'active' can only be set by an agent")
			return
		}
		existing.Status = newStatus
	}
	// session_id: clearing (empty string) is allowed from any caller so users can
	// retry failed tasks. Setting a non-empty session_id requires agent-context
	// (AW-3: prevents UI from overwriting an active session link).
	if req.SessionId != nil {
		if *req.SessionId != "" && !isAgentContext {
			jsonErr(w, http.StatusForbidden, "setting session_id requires agent context")
			return
		}
		existing.SessionID = *req.SessionId
	}
	if req.ProjectId != nil {
		if len(*req.ProjectId) > 50 {
			jsonErr(w, http.StatusBadRequest, "project_id must be 50 characters or fewer")
			return
		}
		if *req.ProjectId != "" {
			if err := validateEntityID(*req.ProjectId); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid project_id")
				return
			}
			if _, projErr := readProjectFile(a.homePath, *req.ProjectId); errors.Is(
				projErr,
				errProjectNotFound,
			) ||
				errors.Is(projErr, os.ErrNotExist) {
				jsonErr(w, http.StatusBadRequest, "project not found")
				return
			} else if projErr != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to validate project_id")
				return
			}
		}
		existing.ProjectID = *req.ProjectId
	}
	if req.MilestoneId != nil {
		milestoneID := *req.MilestoneId
		if milestoneID != "" {
			// Use the (possibly updated) project_id for FK validation.
			effectiveProjectID := existing.ProjectID
			if err := validateMilestoneFK(a.homePath, milestoneID, effectiveProjectID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		existing.MilestoneID = milestoneID
	}
	if req.AgentId != nil {
		agentID := *req.AgentId
		if len(agentID) > 50 {
			jsonErr(w, http.StatusBadRequest, "agent_id must be 50 characters or fewer")
			return
		}
		if agentID != "" {
			if err := validateEntityID(agentID); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid agent_id")
				return
			}
		}
		existing.AgentID = agentID
	}

	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := a.writeBoardTask(existing); err != nil {
		slog.Error("rest: board task: put write failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update board task")
		return
	}

	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    "board_task.update",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"id": id},
		}); err != nil {
			slog.Error(
				"rest: board task: audit log failed",
				"event",
				"board_task.update",
				"error",
				err,
			)
		}
	}
	jsonOK(w, toWireBoardTask(existing))
}

// handleBoardTaskDelete handles DELETE /api/v1/board/tasks/{id} → 204 No Content.
func (a *restAPI) handleBoardTaskDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}

	// Confirm the file exists and is a GTD task before deleting.
	_, err := a.readBoardTask(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: delete read failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read board task")
		return
	}

	path := filepath.Join(a.boardTasksDir(), id+".json")
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: delete failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not delete board task")
		return
	}

	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    "board_task.delete",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"id": id},
		}); err != nil {
			slog.Error(
				"rest: board task: audit log failed",
				"event",
				"board_task.delete",
				"error",
				err,
			)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBoardTaskStart handles POST /api/v1/board/tasks/{id}/start.
func (a *restAPI) handleBoardTaskStart(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}

	existing, err := a.readBoardTask(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: start read failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read board task")
		return
	}

	if existing.Status == "active" || existing.Status == "done" || existing.Status == "failed" {
		jsonErr(w, http.StatusConflict, "task already started or completed")
		return
	}

	agentID := existing.AgentID
	if agentID == "" {
		if reg := a.agentLoop.GetRegistry(); reg != nil {
			if def := reg.GetDefaultAgent(); def != nil {
				agentID = def.ID
			}
		}
		if agentID == "" {
			// fall back to first enabled agent
			cfg := a.agentLoop.GetConfig()
			for _, ag := range cfg.Agents.List {
				if ag.IsActive() {
					agentID = ag.ID
					break
				}
			}
		}
	}
	if agentID == "" {
		jsonErr(w, http.StatusInternalServerError, "no agent configured")
		return
	}

	// Use GetAgentStore first — must match processTaskDirect's TranscriptStore.
	// Fall back to the shared session store only if the per-agent store is nil.
	store := a.agentLoop.GetAgentStore(agentID)
	if store == nil {
		store = a.agentLoop.GetSessionStore()
	}
	if store == nil {
		jsonErr(w, http.StatusInternalServerError, "session store unavailable")
		return
	}

	meta, err := store.NewSession(session.SessionTypeTask, "board", agentID)
	if err != nil {
		slog.Error("rest: board task: start session create failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Link session metadata back to this task.
	title := existing.Name
	tid := id
	if setErr := store.SetMeta(meta.ID, session.MetaPatch{Title: &title, TaskID: &tid}); setErr != nil {
		slog.Warn("rest: board task: start set meta failed", "id", id, "session_id", meta.ID, "error", setErr)
	}

	// Write the task prompt as the user-turn transcript entry so the session
	// is not blank when the user navigates to it.
	prompt := existing.Prompt
	if prompt == "" {
		prompt = existing.Description
	}
	if prompt != "" {
		if appendErr := store.AppendTranscript(meta.ID, session.TranscriptEntry{
			ID:        id + "-prompt",
			Role:      "user",
			Content:   prompt,
			AgentID:   agentID,
			Timestamp: time.Now().UTC(),
		}); appendErr != nil {
			slog.Warn("rest: board task: start transcript write failed", "id", id, "session_id", meta.ID, "error", appendErr)
		}
	}

	if existing.SessionID != "" {
		slog.Warn(
			"rest: board task: start overwrites existing session_id",
			"id", id,
			"old_session_id", existing.SessionID,
			"new_session_id", meta.ID,
		)
	}
	existing.SessionID = meta.ID
	existing.Status = "active"
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := a.writeBoardTask(existing); err != nil {
		slog.Error("rest: board task: start write failed", "id", id, "session_id", meta.ID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to update task")
		return
	}

	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:     "board_task.start",
			Decision:  audit.DecisionAllow,
			AgentID:   agentID,
			SessionID: meta.ID,
			Details:   map[string]any{"id": id},
		}); err != nil {
			slog.Error("rest: board task: audit log failed", "event", "board_task.start", "error", err)
		}
	}

	// Dispatch agent execution asynchronously — returns immediately (202 Accepted).
	if prompt != "" {
		a.agentLoop.ExecuteBoardTask(context.Background(), agentID, id, meta.ID, prompt)
	}

	jsonAccepted(w, toWireBoardTask(existing))
}

// HandleBoardTasks dispatches requests for /api/v1/board/tasks and /api/v1/board/tasks/{id}.
func (a *restAPI) HandleBoardTasks(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/board/tasks")

	if len(rest) > 1 {
		id := strings.TrimPrefix(rest, "/")

		// Must precede the sub-path rejection: id retains "/start" here, which would match strings.Contains below.
		if strings.HasSuffix(id, "/start") {
			taskID := strings.TrimSuffix(id, "/start")
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handleBoardTaskStart(w, r, taskID)
			return
		}

		if strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.handleBoardTaskGet(w, r, id)
		case http.MethodPut:
			a.handleBoardTaskPut(w, r, id)
		case http.MethodDelete:
			a.handleBoardTaskDelete(w, r, id)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.handleBoardTaskList(w, r)
	case http.MethodPost:
		a.handleBoardTaskPost(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// validateMilestoneFK validates that a milestone exists and belongs to the given project.
// Returns a user-facing error string on failure (caller writes 400), nil on success.
// If projectID is empty, only existence is checked (milestone_id on a task with no project).
func validateMilestoneFK(homePath, milestoneID, projectID string) error {
	m, err := readMilestoneFile(homePath, milestoneID)
	if err != nil {
		return errors.New("milestone not found")
	}
	if projectID != "" && m.ProjectID != projectID {
		return errors.New("milestone does not belong to this project")
	}
	return nil
}
