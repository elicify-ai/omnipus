//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/session"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// boardTask is the canonical on-disk GTD task type, re-exported from pkg/boardtask
// so gateway code can use the short name without fully qualifying every reference.
// not-wire-format: mapped to gen.BoardTask at the REST layer.
type boardTask = boardtask.Task

// gtdStatuses is the set of valid GTD task status values.
// Workflow tasks (pkg/taskstore) use queued/assigned/running/completed/failed — never these.
var gtdStatuses = boardtask.GTDStatuses

// boardTaskLock returns the per-task striped lock. It falls back to the package-level
// singleton when the field is nil (test setups that do not wire taskLock explicitly).
func (a *restAPI) boardTaskLock() *boardtask.StripedLock {
	if a.taskLock != nil {
		return a.taskLock
	}
	return boardtask.TaskFileLock
}

// isGTDTask returns true when status is a known GTD status value.
func isGTDTask(status boardtask.Status) bool {
	return boardtask.IsGTDStatus(string(status))
}

// validateBoardTaskAgentID checks that the given agent ID exists in the registry
// when the registry is non-nil and has at least one registered agent.
// Returns nil when the agent ID is valid or when the check is skipped (empty registry).
// Returns an error when the agent ID is non-empty and not found in a populated registry.
func (a *restAPI) validateBoardTaskAgentID(agentID string) error {
	if agentID == "" {
		return nil // empty agent_id is valid (start resolves default)
	}
	reg := a.agentLoop.GetRegistry()
	if reg == nil {
		return nil
	}
	if len(reg.ListAgentIDs()) == 0 {
		// No agents registered — skip validation (fresh install or test fixture).
		return nil
	}
	if _, ok := reg.GetAgent(agentID); !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	return nil
}

// validateStatusTransition returns an error when a PUT is attempting an illegal
// status transition. The only rule is: "active" may only be set via POST /start,
// never via PUT. All other transitions between GTD statuses are permitted — the
// allowedPUTTransitions map previously encoded a 6×5 table that reduced to this
// single rule (every source mapped to the same five non-active targets).
//
// Invariant: a PUT may never set status=active; /start is the only path.
func validateStatusTransition(_, newStatus boardtask.Status) error {
	if newStatus == boardtask.StatusActive {
		return fmt.Errorf("status 'active' can only be set via POST /start, not via PUT")
	}
	return nil
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
	var workspaceID *string
	if t.WorkspaceID != "" {
		p := t.WorkspaceID
		workspaceID = &p
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

	var owner *string
	if t.Owner != "" {
		o := t.Owner
		owner = &o
	}

	// Spec-5 fields: parse start/due from RFC3339 strings; pass recurrence and blocked_by as-is.
	var startTime *time.Time
	if t.Start != "" {
		if ts, parseErr := time.Parse(time.RFC3339, t.Start); parseErr == nil {
			startTime = &ts
		} else {
			slog.Warn("rest: board task: invalid start timestamp", "id", t.ID, "raw", t.Start)
		}
	}
	var dueTime *time.Time
	if t.Due != "" {
		if ts, parseErr := time.Parse(time.RFC3339, t.Due); parseErr == nil {
			dueTime = &ts
		} else {
			slog.Warn("rest: board task: invalid due timestamp", "id", t.ID, "raw", t.Due)
		}
	}
	var recurrence *string
	if t.Recurrence != "" {
		r := t.Recurrence
		recurrence = &r
	}
	var blockedBy *[]string
	if len(t.BlockedBy) > 0 {
		bb := make([]string, len(t.BlockedBy))
		copy(bb, t.BlockedBy)
		blockedBy = &bb
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
		WorkspaceId: workspaceID,
		AgentId:     agentID,
		Owner:       owner,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		Start:       startTime,
		Due:         dueTime,
		Recurrence:  recurrence,
		BlockedBy:   blockedBy,
	}
}

// makeBoardTaskLoader returns a TaskLoader function for use by the blocked_by DAG
// validator. It reads a single GTD task by ID from the tasks directory.
// Returns os.ErrNotExist when the task is absent or is not a GTD task.
func (a *restAPI) makeBoardTaskLoader() boardtask.TaskLoader {
	return func(id string) (boardtask.Task, error) {
		return a.readBoardTask(id)
	}
}

// handleBoardTaskList handles GET /api/v1/board/tasks with optional filters.
func (a *restAPI) handleBoardTaskList(w http.ResponseWriter, r *http.Request) {
	workspaceFilter := r.URL.Query().Get("workspace_id")
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

	// Apply workspace_id, status, agent_id, milestone_id filters.
	// FR-1.9: owner gate removed — all tasks are visible regardless of owner.
	filtered := make([]boardTask, 0, len(all))
	for _, t := range all {
		if workspaceFilter != "" && t.WorkspaceID != workspaceFilter {
			continue
		}
		if statusFilter != "" && string(t.Status) != statusFilter {
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

	// Build the response using the generated gen.BoardTaskListItem type directly.
	// BoardTaskListResponse.Items is now []gen.BoardTaskListItem (commit 1 promoted
	// BoardTaskListItem from an allOf wrapper to a plain named object).
	items := make([]gen.BoardTaskListItem, 0, len(filtered))
	for _, t := range filtered {
		wt := toWireBoardTask(t)
		items = append(items, gen.BoardTaskListItem{
			AgentId:     wt.AgentId,
			CreatedAt:   wt.CreatedAt,
			Description: wt.Description,
			Id:          wt.Id,
			MilestoneId: wt.MilestoneId,
			Name:        wt.Name,
			WorkspaceId: wt.WorkspaceId,
			Owner:       wt.Owner,
			Priority:    wt.Priority,
			Prompt:      wt.Prompt,
			Result:      wt.Result,
			SessionId:   wt.SessionId,
			// t.Status is a validated GTD status; convert boardtask.Status → gen.BoardTaskListItemStatus
			// (both are named string types — no runtime cost).
			Status:     gen.BoardTaskListItemStatus(t.Status),
			UpdatedAt:  wt.UpdatedAt,
			Start:      wt.Start,
			Due:        wt.Due,
			Recurrence: wt.Recurrence,
			BlockedBy:  wt.BlockedBy,
		})
	}

	jsonOK(w, gen.BoardTaskListResponse{Items: items, Total: total})
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
	// FR-1.9: owner gate removed — owner is attribution only.
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
	status := boardtask.StatusInbox
	if req.Status != nil && string(*req.Status) != "" {
		status = boardtask.Status(string(*req.Status))
	}
	if !isGTDTask(status) {
		jsonErr(
			w,
			http.StatusBadRequest,
			"status must be one of: inbox, next, active, waiting, done, failed",
		)
		return
	}

	// Resolve caller identity for owner stamp.
	c := a.callerIdentity(r)

	workspaceID := ""
	if req.WorkspaceId != nil {
		workspaceID = *req.WorkspaceId
		if len(workspaceID) > 50 {
			jsonErr(w, http.StatusBadRequest, "workspace_id must be 50 characters or fewer")
			return
		}
		if workspaceID != "" {
			if err := validateEntityID(workspaceID); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid workspace_id")
				return
			}
			_, wsErr := readWorkspaceFile(a.homePath, workspaceID)
			if errors.Is(wsErr, errWorkspaceNotFound) || errors.Is(wsErr, os.ErrNotExist) {
				jsonErr(w, http.StatusBadRequest, "workspace not found")
				return
			} else if wsErr != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to validate workspace_id")
				return
			}
			// FR-1.9: owner gate removed — no cross-owner check.
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
			// A2: validate agent_id exists in the registry (when registry is populated).
			if err := a.validateBoardTaskAgentID(agentID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
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
			// Validate milestone FK: must exist and belong to the same workspace (when workspace_id is provided).
			if err := validateMilestoneFK(a.homePath, milestoneID, workspaceID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}

	// Spec-5 fields: start, due, recurrence, blocked_by.
	startStr := ""
	if req.Start != nil {
		startStr = req.Start.UTC().Format(time.RFC3339)
	}
	dueStr := ""
	if req.Due != nil {
		dueStr = req.Due.UTC().Format(time.RFC3339)
	}
	recurrenceStr := ""
	if req.Recurrence != nil {
		recurrenceStr = *req.Recurrence
	}
	var newBlockedBy []string
	if req.BlockedBy != nil {
		newBlockedBy = *req.BlockedBy
	}

	// The newly created task has no ID yet, but we need it for the self-edge check.
	// Generate the ID here so ValidateBlockedBy can use it.
	newTaskID := ulid.Make().String()
	if len(newBlockedBy) > 0 {
		loader := a.makeBoardTaskLoader()
		if err := boardtask.ValidateBlockedBy(newTaskID, newBlockedBy, loader); err != nil {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("blocked_by: %v", err))
			return
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	t := boardTask{
		ID:          newTaskID,
		Name:        req.Name,
		Description: description,
		Prompt:      prompt,
		Priority:    priority,
		MilestoneID: milestoneID,
		Status:      status,
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		// Stamp the creating user's username as owner (attribution only).
		Owner:      c.Username,
		CreatedAt:  now,
		UpdatedAt:  now,
		Start:      startStr,
		Due:        dueStr,
		Recurrence: recurrenceStr,
		BlockedBy:  newBlockedBy,
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

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.UpdateBoardTaskJSONBody
	if !decodeAndValidate(w, r, "UpdateBoardTaskJSONBody", &req, validateEnabled) {
		return
	}

	// becameDone records that this PUT transitions the task to terminal "done";
	// advanceDeps is armed only after the write succeeds. The deferred closure
	// below runs AFTER the per-task lock is released and un-gates any waiting
	// dependents whose blocked_by deps are now all done (FR-6.5). Registered
	// before the unlock defer so it executes last (LIFO).
	becameDone := false
	advanceDeps := false
	defer func() {
		if !advanceDeps {
			return
		}
		if advanced, advErr := boardtask.AdvanceBlockedDependents(a.boardTasksDir(), id); advErr != nil {
			slog.Warn("rest: board task: advance dependents failed", "id", id, "error", advErr)
		} else if len(advanced) > 0 {
			slog.Info("rest: board task: completed task advanced dependents",
				"completed_id", id, "advanced_ids", advanced)
		}
	}()

	// Hold the per-task striped lock for the entire read→mutate→write so that
	// two concurrent PUTs on the same task do not race.
	mu := a.boardTaskLock().Get(id)
	mu.Lock()
	defer mu.Unlock()

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

	// owner is immutable — the stored value is always preserved; any owner field
	// in the request body is ignored.

	// Track whether the request explicitly provided session_id (for #405 auto-clear logic below).
	reqExplicitSessionID := req.SessionId != nil

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
		newStatus := boardtask.Status(string(*req.Status))
		if !isGTDTask(newStatus) {
			jsonErr(
				w,
				http.StatusBadRequest,
				"status must be one of: inbox, next, active, waiting, done, failed",
			)
			return
		}
		// Validate the transition (blocks status=active — only /start may set that).
		if err := validateStatusTransition(existing.Status, newStatus); err != nil {
			jsonErr(w, http.StatusForbidden, err.Error())
			return
		}
		oldStatus := existing.Status
		existing.Status = newStatus
		// FR-6.5: record when a board task newly reaches terminal "done"; the
		// post-unlock dependent advance is armed only after a successful write.
		if newStatus == boardtask.StatusDone && oldStatus != boardtask.StatusDone {
			becameDone = true
		}
		// #405: when transitioning from a terminal/active state to a re-queue state
		// (next/inbox), auto-clear session_id UNLESS the request explicitly sets it.
		// This is the defensive backend half; the SPA already sends session_id:"" on retry.
		terminalOrActive := oldStatus == boardtask.StatusDone || oldStatus == boardtask.StatusFailed ||
			oldStatus == boardtask.StatusActive
		requeue := newStatus == boardtask.StatusNext || newStatus == boardtask.StatusInbox
		if terminalOrActive && requeue && !reqExplicitSessionID {
			existing.SessionID = ""
		}
	}
	// session_id via PUT:
	//   - Setting a non-empty value is server-only (written by /start); reject from PUT.
	//   - Clearing to "" is allowed (doRetry path), EXCEPT when the task is active
	//     (that would break the task→session link while the goroutine is still running).
	if req.SessionId != nil {
		if *req.SessionId != "" {
			jsonErr(w, http.StatusForbidden, "setting session_id is only allowed via POST /start")
			return
		}
		// Clear to "" is allowed unless the task is currently active.
		if existing.Status == boardtask.StatusActive {
			jsonErr(w, http.StatusForbidden, "clearing session_id on an active task is not allowed")
			return
		}
		existing.SessionID = ""
	}
	// workspaceChanged records that this PUT moved the task to a different workspace;
	// if so and a milestone stays attached, the milestone-workspace FK must be
	// re-validated below (a milestone belongs to exactly one workspace).
	workspaceChanged := false
	if req.WorkspaceId != nil {
		if len(*req.WorkspaceId) > 50 {
			jsonErr(w, http.StatusBadRequest, "workspace_id must be 50 characters or fewer")
			return
		}
		if *req.WorkspaceId != "" {
			if err := validateEntityID(*req.WorkspaceId); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid workspace_id")
				return
			}
			_, wsErr := readWorkspaceFile(a.homePath, *req.WorkspaceId)
			if errors.Is(wsErr, errWorkspaceNotFound) || errors.Is(wsErr, os.ErrNotExist) {
				jsonErr(w, http.StatusBadRequest, "workspace not found")
				return
			} else if wsErr != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to validate workspace_id")
				return
			}
		}
		workspaceChanged = *req.WorkspaceId != existing.WorkspaceID
		existing.WorkspaceID = *req.WorkspaceId
	}
	if req.MilestoneId != nil {
		milestoneID := *req.MilestoneId
		if milestoneID != "" {
			// Use the (possibly updated) workspace_id for FK validation.
			effectiveWorkspaceID := existing.WorkspaceID
			if err := validateMilestoneFK(a.homePath, milestoneID, effectiveWorkspaceID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		existing.MilestoneID = milestoneID
	} else if workspaceChanged && existing.MilestoneID != "" {
		// Workspace moved but milestone_id was not part of this request: the
		// still-attached milestone may now belong to a different workspace, so
		// re-validate the FK. If it no longer holds, clear it rather than 400 —
		// the move is the operator's primary intent and the stale FK is collateral.
		if err := validateMilestoneFK(a.homePath, existing.MilestoneID, existing.WorkspaceID); err != nil {
			slog.Info("rest: board task: clearing milestone after workspace change broke FK",
				"id", id, "milestone_id", existing.MilestoneID, "workspace_id", existing.WorkspaceID, "reason", err.Error())
			existing.MilestoneID = ""
		}
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
			// A2: validate agent_id exists in the registry (when registry is populated).
			if err := a.validateBoardTaskAgentID(agentID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		existing.AgentID = agentID
	}

	// Spec-5 fields: start, due, recurrence, blocked_by.
	if req.Start != nil {
		existing.Start = req.Start.UTC().Format(time.RFC3339)
	}
	if req.Due != nil {
		existing.Due = req.Due.UTC().Format(time.RFC3339)
	}
	if req.Recurrence != nil {
		existing.Recurrence = *req.Recurrence
	}
	if req.BlockedBy != nil {
		updatedBlockedBy := *req.BlockedBy
		if len(updatedBlockedBy) > 0 {
			loader := a.makeBoardTaskLoader()
			if err := boardtask.ValidateBlockedBy(existing.ID, updatedBlockedBy, loader); err != nil {
				jsonErr(w, http.StatusBadRequest, fmt.Sprintf("blocked_by: %v", err))
				return
			}
		}
		existing.BlockedBy = updatedBlockedBy
		if len(existing.BlockedBy) == 0 {
			existing.BlockedBy = nil
		}
	}

	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := a.writeBoardTask(existing); err != nil {
		slog.Error("rest: board task: put write failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update board task")
		return
	}
	// Write succeeded: arm the post-unlock dependent advance if this PUT
	// completed the task (FR-6.5).
	if becameDone {
		advanceDeps = true
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
	if _, err := a.readBoardTask(id); err != nil {
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

	// Spec-5 (FR-8.2): cascade-clean inbound blocked_by edges after delete.
	// Any task that was solely blocked by the deleted task is now unblocked.
	// Log the unblocked IDs so the Orchestrator (Spec-3) can act on them.
	if unblocked, cascadeErr := boardtask.CascadeDeleteEdges(a.boardTasksDir(), id); cascadeErr != nil {
		slog.Warn("rest: board task: cascade edge cleanup failed", "id", id, "error", cascadeErr)
	} else if len(unblocked) > 0 {
		slog.Info("rest: board task: deleted task unblocked dependents",
			"deleted_id", id, "unblocked_ids", unblocked)
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

// startDispatchParams holds the parameters extracted by startBoardTaskLocked
// that are needed to dispatch agent execution after the lock is released.
type startDispatchParams struct {
	agentID     string
	sessionID   string
	prompt      string
	workspaceID string
	task        boardTask // final task state written to disk (status=active)
}

// startBoardTaskLocked performs the locked read→validate→mutate→write phase of
// POST /start. It acquires the per-task striped lock, does all validation and
// disk work inside, and releases the lock via defer before returning.
//
// Invariant: the lock is always released before this function returns, so the
// caller dispatches the agent goroutine with the lock already unlocked.
// This preserves the original semantic: lock is held only for the synchronous
// RMW, not for the (potentially long) agent execution.
//
// On success it returns (params, 0, ""). On error it returns (zero, httpStatus, errMsg).
func (a *restAPI) startBoardTaskLocked(
	id string,
) (startDispatchParams, int, string) {
	mu := a.boardTaskLock().Get(id)
	mu.Lock()
	defer mu.Unlock()

	existing, err := a.readBoardTask(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return startDispatchParams{}, http.StatusNotFound, "board task not found"
		}
		slog.Error("rest: board task: start read failed", "id", id, "error", err)
		return startDispatchParams{}, http.StatusInternalServerError, "could not read board task"
	}

	if existing.Status == boardtask.StatusActive || existing.Status == boardtask.StatusDone ||
		existing.Status == boardtask.StatusFailed {
		return startDispatchParams{}, http.StatusConflict, "task already started or completed"
	}

	agentID := existing.AgentID
	if agentID == "" {
		if reg := a.agentLoop.GetRegistry(); reg != nil {
			if def := reg.GetDefaultAgent(); def != nil {
				agentID = def.ID
			}
		}
		if agentID == "" {
			agentID = firstEnabledAgentID(a.agentLoop.GetConfig())
		}
	}
	if agentID == "" {
		return startDispatchParams{}, http.StatusInternalServerError, "no agent configured"
	}

	// A2: validate that the resolved agent actually exists in the registry
	// (when the registry is non-empty — empty means fresh install or test fixture).
	if validateErr := a.validateBoardTaskAgentID(agentID); validateErr != nil {
		return startDispatchParams{}, http.StatusBadRequest, validateErr.Error()
	}

	// Reject early if there's nothing to execute — a task with neither prompt nor
	// description would be dispatched silently and stuck in "active" forever.
	prompt := existing.Prompt
	if prompt == "" {
		prompt = existing.Description
	}
	if prompt == "" {
		return startDispatchParams{}, http.StatusUnprocessableEntity, "task has no prompt or description"
	}

	// Use GetAgentStore first — must match processTaskDirect's TranscriptStore.
	// Fall back to the shared session store only if the per-agent store is nil.
	store := a.agentLoop.GetAgentStore(agentID)
	if store == nil {
		store = a.agentLoop.GetSessionStore()
	}
	if store == nil {
		return startDispatchParams{}, http.StatusInternalServerError, "session store unavailable"
	}

	meta, err := store.NewSession(session.SessionTypeTask, "board", agentID)
	if err != nil {
		slog.Error("rest: board task: start session create failed", "id", id, "error", err)
		return startDispatchParams{}, http.StatusInternalServerError, "failed to create session"
	}

	// H2: Link session metadata back to this task — treat failure as fatal.
	// An orphaned session (metadata not linked to the task) would be invisible
	// from the UI and cannot be cleaned up; abort dispatch rather than proceed.
	// M-2: best-effort delete the just-created session before returning 500 so
	// the abort path does not leak an orphan session that is invisible from the UI.
	title := existing.Name
	tid := id
	// Propagate the board task's owner onto the session so that sysagent tools
	// running inside this turn inherit the correct owner (SEC-2/#406, Rule-2).
	taskOwner := existing.Owner
	metaPatch := session.MetaPatch{Title: &title, TaskID: &tid, Owner: &taskOwner}
	if setErr := store.SetMeta(meta.ID, metaPatch); setErr != nil {
		slog.Error("rest: board task: start set meta failed — aborting dispatch",
			"id", id, "session_id", meta.ID, "error", setErr)
		if delErr := store.DeleteSession(meta.ID); delErr != nil {
			slog.Warn("rest: board task: start orphan session cleanup failed",
				"id", id, "session_id", meta.ID, "error", delErr)
		}
		return startDispatchParams{}, http.StatusInternalServerError, "failed to link session metadata"
	}

	// H2: Write the task prompt as the user-turn transcript entry — fatal on failure.
	if appendErr := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID:        id + "-prompt",
		Role:      "user",
		Content:   prompt,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		slog.Error("rest: board task: start transcript write failed — aborting dispatch",
			"id", id, "session_id", meta.ID, "error", appendErr)
		if delErr := store.DeleteSession(meta.ID); delErr != nil {
			slog.Warn("rest: board task: start orphan session cleanup failed",
				"id", id, "session_id", meta.ID, "error", delErr)
		}
		return startDispatchParams{}, http.StatusInternalServerError, "failed to write session transcript"
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
	existing.Status = boardtask.StatusActive
	// #403: persist the resolved agentID so GET /tasks/{id} reflects which agent ran it.
	existing.AgentID = agentID
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := a.writeBoardTask(existing); err != nil {
		slog.Error("rest: board task: start write failed", "id", id, "session_id", meta.ID, "error", err)
		return startDispatchParams{}, http.StatusInternalServerError, "failed to update task"
	}

	return startDispatchParams{
		agentID:     agentID,
		sessionID:   meta.ID,
		prompt:      prompt,
		workspaceID: existing.WorkspaceID,
		task:        existing,
	}, 0, ""
}

// handleBoardTaskStart handles POST /api/v1/board/tasks/{id}/start.
func (a *restAPI) handleBoardTaskStart(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}

	// startBoardTaskLocked acquires and releases the per-task striped lock inside,
	// using defer mu.Unlock() so every error path is covered without explicit unlocks.
	// The lock is released before this function returns, so dispatch is always
	// outside the critical section (lock held only for synchronous RMW, not execution).
	params, httpStatus, errMsg := a.startBoardTaskLocked(id)
	if httpStatus != 0 {
		jsonErr(w, httpStatus, errMsg)
		return
	}

	// Record the workspace↔session link so GET /workspaces/{id}/sessions works for board tasks.
	if params.workspaceID != "" {
		if linkErr := systools.AppendLinkExported(a.homePath, params.workspaceID, params.sessionID); linkErr != nil {
			// Non-fatal: log but continue; the task is already dispatched.
			slog.Warn("rest: board task: start workspace session link failed",
				"id", id, "workspace_id", params.workspaceID, "session_id", params.sessionID, "error", linkErr)
		}
	}

	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:     "board_task.start",
			Decision:  audit.DecisionAllow,
			AgentID:   params.agentID,
			SessionID: params.sessionID,
			Details:   map[string]any{"id": id},
		}); err != nil {
			slog.Error("rest: board task: audit log failed", "event", "board_task.start", "error", err)
		}
	}

	// Dispatch agent execution asynchronously. On completion, update the task to
	// done/failed — but only if the task is still active (guard against user retry
	// resetting the status before the goroutine finishes).
	taskID := id
	agentID := params.agentID
	sessionID := params.sessionID
	prompt := params.prompt
	a.agentLoop.ExecuteBoardTask(agentID, taskID, sessionID, prompt, func(result string, execErr error) {
		// becameDone records that this completion transitioned the task to terminal
		// "done"; advanceDeps is armed only after the write succeeds. The callback
		// must un-gate any waiting dependents whose blocked_by deps are now all done
		// (FR-6.5) — mirroring the PUT/tool paths — otherwise board tasks blocked on
		// an autonomously-completed task are stranded forever. We do this AFTER the
		// per-task lock is released (AdvanceBlockedDependents takes the striped lock
		// of each dependent itself, and could deadlock if we held this task's lock
		// while a dependent also depended back, so keep dispatch outside the section).
		advanceDeps := false
		func() {
			// Hold the task lock for the completion RMW (B8).
			cMu := a.boardTaskLock().Get(taskID)
			cMu.Lock()
			defer cMu.Unlock()

			t, readErr := a.readBoardTask(taskID)
			if readErr != nil {
				slog.Error("rest: board task: completion read failed", "id", taskID, "error", readErr)
				return
			}
			if t.Status != boardtask.StatusActive {
				// Task was modified externally (e.g. user retried) — don't overwrite.
				return
			}
			if execErr != nil {
				t.Status = boardtask.StatusFailed
				t.Result = execErr.Error()
			} else {
				t.Status = boardtask.StatusDone
				// #404: capture the agent's output string (truncated to 50000 chars).
				if len(result) > 50000 {
					result = result[:50000]
				}
				t.Result = result
			}
			t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if writeErr := a.writeBoardTask(t); writeErr != nil {
				slog.Error("rest: board task: completion update failed", "id", taskID, "error", writeErr)
				return
			}
			// Only un-gate dependents once the terminal "done" write actually persisted.
			advanceDeps = execErr == nil
		}()

		if advanceDeps {
			if advanced, advErr := boardtask.AdvanceBlockedDependents(a.boardTasksDir(), taskID); advErr != nil {
				slog.Warn("rest: board task: advance dependents failed", "id", taskID, "error", advErr)
			} else if len(advanced) > 0 {
				slog.Info("rest: board task: completed task advanced dependents",
					"completed_id", taskID, "advanced_ids", advanced)
			}
		}
	})

	jsonAccepted(w, toWireBoardTask(params.task))
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

// validateMilestoneFK validates that a milestone exists and (when workspaceID is
// non-empty) belongs to the given workspace.
// Returns a user-facing error string on failure (caller writes 400), nil on success.
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
