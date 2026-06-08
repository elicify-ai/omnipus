//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
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
)

// boardTask mirrors the on-disk format of ~/.omnipus/tasks/{id}.json for GTD tasks.
type boardTask struct { // not-wire-format: internal disk-cache struct, never emitted directly over the wire
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
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
	return fileutil.WriteFileAtomic(path, data, 0o600)
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
		if len(name) < 6 || name[len(name)-5:] != ".json" {
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

	// Apply project_id and status filters.
	filtered := make([]boardTask, 0, len(all))
	for _, t := range all {
		if projectFilter != "" && t.ProjectID != projectFilter {
			continue
		}
		if statusFilter != "" && t.Status != statusFilter {
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
		Name        string                               `json:"name"`
		ProjectId   *string                              `json:"project_id,omitempty"`
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
			Name:        wt.Name,
			ProjectId:   wt.ProjectId,
			Status:      gen.BoardTaskListResponseItemsStatus(wt.Status),
			UpdatedAt:   wt.UpdatedAt,
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
		jsonErr(w, http.StatusBadRequest, "status must be one of: inbox, next, active, waiting, done")
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
			if _, projErr := readProjectFile(a.homePath, projectID); errors.Is(projErr, errProjectNotFound) || errors.Is(projErr, os.ErrNotExist) {
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
	}

	description := ""
	if req.Description != nil {
		description = *req.Description
		if len(description) > 2000 {
			jsonErr(w, http.StatusBadRequest, "description must be 2000 characters or fewer")
			return
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	t := boardTask{
		ID:          ulid.Make().String(),
		Name:        req.Name,
		Description: description,
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
		_ = a.auditor.Log(&audit.Entry{Event: "board_task.create", Decision: "allowed", Details: map[string]any{"id": t.ID}})
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
	if req.Status != nil {
		newStatus := string(*req.Status)
		if !isGTDTask(newStatus) {
			jsonErr(w, http.StatusBadRequest, "status must be one of: inbox, next, active, waiting, done")
			return
		}
		existing.Status = newStatus
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
			if _, projErr := readProjectFile(a.homePath, *req.ProjectId); errors.Is(projErr, errProjectNotFound) || errors.Is(projErr, os.ErrNotExist) {
				jsonErr(w, http.StatusBadRequest, "project not found")
				return
			} else if projErr != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to validate project_id")
				return
			}
		}
		existing.ProjectID = *req.ProjectId
	}
	if req.AgentId != nil {
		existing.AgentID = *req.AgentId
	}

	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := a.writeBoardTask(existing); err != nil {
		slog.Error("rest: board task: put write failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update board task")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(&audit.Entry{Event: "board_task.update", Decision: "allowed", Details: map[string]any{"id": id}})
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
		_ = a.auditor.Log(&audit.Entry{Event: "board_task.delete", Decision: "allowed", Details: map[string]any{"id": id}})
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleBoardTasks dispatches requests for /api/v1/board/tasks and /api/v1/board/tasks/{id}.
func (a *restAPI) HandleBoardTasks(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/board/tasks")

	if len(rest) > 1 {
		id := strings.TrimPrefix(rest, "/")
		// Reject sub-paths (e.g. /api/v1/board/tasks/foo/bar).
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
