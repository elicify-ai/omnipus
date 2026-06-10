//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// isGTDTask returns true when status is a known GTD status value.
func isGTDTask(status string) bool {
	return boardtask.IsGTDStatus(status)
}

// boardTaskLock returns the striped mutex for the given task ID.
// The pool is a fixed 64 shards (O(1) memory), keyed by FNV hash of the task ID,
// matching the pattern in pkg/memory/jsonl.go::JSONLStore.
func (a *restAPI) boardTaskLock(taskID string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(taskID))
	return &a.taskStripedMu[h.Sum32()%64]
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
func validateStatusTransition(_, newStatus string) error {
	if newStatus == "active" {
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

	var owner *string
	if t.Owner != "" {
		o := t.Owner
		owner = &o
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
		Owner:       owner,
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

	c := a.callerIdentity(r)

	all, err := a.listBoardTasks()
	if err != nil {
		slog.Error("rest: board task: list failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list board tasks")
		return
	}

	// Apply ownership scope, then project_id, status, agent_id, milestone_id filters.
	// SEC-2: tasks with a non-empty owner that doesn't match the caller are hidden.
	filtered := make([]boardTask, 0, len(all))
	for _, t := range all {
		if !c.canAccess(t.Owner) {
			continue
		}
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
		Owner       *string                              `json:"owner,omitempty"`
		Priority    *int                                 `json:"priority,omitempty"`
		ProjectId   *string                              `json:"project_id,omitempty"`
		Prompt      *string                              `json:"prompt,omitempty"`
		Result      *string                              `json:"result,omitempty"`
		SessionId   *string                              `json:"session_id,omitempty"`
		Status      gen.BoardTaskListItemStatus `json:"status"`
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
			Owner:       wt.Owner,
			Priority:    wt.Priority,
			ProjectId:   wt.ProjectId,
			Prompt:      wt.Prompt,
			Result:      wt.Result,
			SessionId:   wt.SessionId,
			// t.Status is a validated GTD status string (readBoardTask/listBoardTasks
			// reject non-GTD values); convert via the source string rather than an
			// enum-to-enum cast.
			Status:    gen.BoardTaskListItemStatus(t.Status),
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
	// SEC-2: 404 on cross-owner access to avoid resource enumeration.
	if a.denyIfNoAccess(w, a.callerIdentity(r), t.Owner, "board task not found") {
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

	// SEC-2: resolve caller identity early — needed for both the owner stamp and
	// the cross-owner reference check on project_id / milestone_id.
	c := a.callerIdentity(r)

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
			proj, projErr := readProjectFile(a.homePath, projectID)
			if errors.Is(projErr, errProjectNotFound) || errors.Is(projErr, os.ErrNotExist) {
				jsonErr(w, http.StatusBadRequest, "project not found")
				return
			} else if projErr != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to validate project_id")
				return
			}
			// SEC-2: cross-owner reference is an enumeration oracle. Return the
			// same "project not found" 400 on denial so there is no oracle.
			if !c.canAccess(proj.Owner) {
				jsonErr(w, http.StatusBadRequest, "project not found")
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
			// Validate milestone FK: must exist, be accessible to the caller, and
			// belong to the same project (when project_id is provided).
			// Pass the caller so cross-owner milestones are denied without an oracle
			// (SEC-2: same "milestone not found" 400 as the not-exist case).
			if err := validateMilestoneFK(a.homePath, milestoneID, projectID, c); err != nil {
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
		// SEC-2: stamp the creating user's username as owner. In dev-mode bypass,
		// the caller has no username — stamp empty string (unowned/shared).
		Owner:     c.Username,
		CreatedAt: now,
		UpdatedAt: now,
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

	// Hold the per-task striped lock for the entire read→mutate→write so that
	// two concurrent PUTs on the same task do not race.
	mu := a.boardTaskLock(id)
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

	// SEC-2: 404 on cross-owner access to avoid resource enumeration.
	// owner is immutable — the stored value is always preserved; any owner field
	// in the request body is ignored.
	c := a.callerIdentity(r)
	if a.denyIfNoAccess(w, c, existing.Owner, "board task not found") {
		return
	}

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
		newStatus := string(*req.Status)
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
		// #405: when transitioning from a terminal/active state to a re-queue state
		// (next/inbox), auto-clear session_id UNLESS the request explicitly sets it.
		// This is the defensive backend half; the SPA already sends session_id:"" on retry.
		terminalOrActive := oldStatus == "done" || oldStatus == "failed" || oldStatus == "active"
		requeue := newStatus == "next" || newStatus == "inbox"
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
		if existing.Status == "active" {
			jsonErr(w, http.StatusForbidden, "clearing session_id on an active task is not allowed")
			return
		}
		existing.SessionID = ""
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
			proj, projErr := readProjectFile(a.homePath, *req.ProjectId)
			if errors.Is(projErr, errProjectNotFound) || errors.Is(projErr, os.ErrNotExist) {
				jsonErr(w, http.StatusBadRequest, "project not found")
				return
			} else if projErr != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to validate project_id")
				return
			}
			// SEC-2: cross-owner reference is an enumeration oracle. Return the
			// same "project not found" 400 on denial so there is no oracle.
			if !c.canAccess(proj.Owner) {
				jsonErr(w, http.StatusBadRequest, "project not found")
				return
			}
		}
		existing.ProjectID = *req.ProjectId
	}
	if req.MilestoneId != nil {
		milestoneID := *req.MilestoneId
		if milestoneID != "" {
			// Use the (possibly updated) project_id for FK validation.
			// Pass the caller for SEC-2 ownership check (anti-enumeration oracle).
			effectiveProjectID := existing.ProjectID
			if err := validateMilestoneFK(a.homePath, milestoneID, effectiveProjectID, c); err != nil {
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
			// A2: validate agent_id exists in the registry (when registry is populated).
			if err := a.validateBoardTaskAgentID(agentID); err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
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
	existing, err := a.readBoardTask(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: delete read failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read board task")
		return
	}

	// SEC-2: 404 on cross-owner access to avoid resource enumeration.
	if a.denyIfNoAccess(w, a.callerIdentity(r), existing.Owner, "board task not found") {
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

	// Hold the per-task striped lock for the entire read→check→write so that two
	// concurrent /start calls cannot both reach "active" (B8: atomic check-and-set).
	mu := a.boardTaskLock(id)
	mu.Lock()

	existing, err := a.readBoardTask(id)
	if err != nil {
		mu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: start read failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read board task")
		return
	}

	// SEC-2: 404 on cross-owner access to avoid resource enumeration.
	if !a.callerIdentity(r).canAccess(existing.Owner) {
		mu.Unlock()
		jsonErr(w, http.StatusNotFound, "board task not found")
		return
	}

	if existing.Status == "active" || existing.Status == "done" || existing.Status == "failed" {
		mu.Unlock()
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
			agentID = firstEnabledAgentID(a.agentLoop.GetConfig())
		}
	}
	if agentID == "" {
		mu.Unlock()
		jsonErr(w, http.StatusInternalServerError, "no agent configured")
		return
	}

	// A2: validate that the resolved agent actually exists in the registry
	// (when the registry is non-empty — empty means fresh install or test fixture).
	if err := a.validateBoardTaskAgentID(agentID); err != nil {
		mu.Unlock()
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Reject early if there's nothing to execute — a task with neither prompt nor
	// description would be dispatched silently and stuck in "active" forever.
	prompt := existing.Prompt
	if prompt == "" {
		prompt = existing.Description
	}
	if prompt == "" {
		mu.Unlock()
		jsonErr(w, http.StatusUnprocessableEntity, "task has no prompt or description")
		return
	}

	// Use GetAgentStore first — must match processTaskDirect's TranscriptStore.
	// Fall back to the shared session store only if the per-agent store is nil.
	store := a.agentLoop.GetAgentStore(agentID)
	if store == nil {
		store = a.agentLoop.GetSessionStore()
	}
	if store == nil {
		mu.Unlock()
		jsonErr(w, http.StatusInternalServerError, "session store unavailable")
		return
	}

	meta, err := store.NewSession(session.SessionTypeTask, "board", agentID)
	if err != nil {
		mu.Unlock()
		slog.Error("rest: board task: start session create failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to create session")
		return
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
	if setErr := store.SetMeta(meta.ID, session.MetaPatch{Title: &title, TaskID: &tid, Owner: &taskOwner}); setErr != nil {
		mu.Unlock()
		slog.Error("rest: board task: start set meta failed — aborting dispatch",
			"id", id, "session_id", meta.ID, "error", setErr)
		if delErr := store.DeleteSession(meta.ID); delErr != nil {
			slog.Warn("rest: board task: start orphan session cleanup failed",
				"id", id, "session_id", meta.ID, "error", delErr)
		}
		jsonErr(w, http.StatusInternalServerError, "failed to link session metadata")
		return
	}

	// H2: Write the task prompt as the user-turn transcript entry — fatal on failure.
	if appendErr := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID:        id + "-prompt",
		Role:      "user",
		Content:   prompt,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		mu.Unlock()
		slog.Error("rest: board task: start transcript write failed — aborting dispatch",
			"id", id, "session_id", meta.ID, "error", appendErr)
		if delErr := store.DeleteSession(meta.ID); delErr != nil {
			slog.Warn("rest: board task: start orphan session cleanup failed",
				"id", id, "session_id", meta.ID, "error", delErr)
		}
		jsonErr(w, http.StatusInternalServerError, "failed to write session transcript")
		return
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
	// #403: persist the resolved agentID so GET /tasks/{id} reflects which agent ran it.
	existing.AgentID = agentID
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := a.writeBoardTask(existing); err != nil {
		mu.Unlock()
		slog.Error("rest: board task: start write failed", "id", id, "session_id", meta.ID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to update task")
		return
	}

	// Release the lock BEFORE dispatching the goroutine — we hold the lock only
	// for the synchronous read→mutate→write, not for the entire agent execution.
	mu.Unlock()

	// A1: record the project↔session link so GET /projects/{id}/sessions works for board tasks.
	if existing.ProjectID != "" {
		if linkErr := systools.AppendLinkExported(a.homePath, existing.ProjectID, meta.ID); linkErr != nil {
			// Non-fatal: log but continue; the task is already dispatched.
			slog.Warn("rest: board task: start project session link failed",
				"id", id, "project_id", existing.ProjectID, "session_id", meta.ID, "error", linkErr)
		}
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

	// Dispatch agent execution asynchronously. On completion, update the task to
	// done/failed — but only if the task is still active (guard against user retry
	// resetting the status before the goroutine finishes).
	taskID := id
	a.agentLoop.ExecuteBoardTask(agentID, taskID, meta.ID, prompt, func(result string, execErr error) {
		// Hold the task lock for the completion RMW as well (B8).
		cMu := a.boardTaskLock(taskID)
		cMu.Lock()
		defer cMu.Unlock()

		t, readErr := a.readBoardTask(taskID)
		if readErr != nil {
			slog.Error("rest: board task: completion read failed", "id", taskID, "error", readErr)
			return
		}
		if t.Status != "active" {
			// Task was modified externally (e.g. user retried) — don't overwrite.
			return
		}
		if execErr != nil {
			t.Status = "failed"
			t.Result = execErr.Error()
		} else {
			t.Status = "done"
			// #404: capture the agent's output string (truncated to 50000 chars).
			if len(result) > 50000 {
				result = result[:50000]
			}
			t.Result = result
		}
		t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if writeErr := a.writeBoardTask(t); writeErr != nil {
			slog.Error("rest: board task: completion update failed", "id", taskID, "error", writeErr)
		}
	})

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

// validateMilestoneFK validates that a milestone exists, is accessible to the
// caller, and (when projectID is non-empty) belongs to the given project.
// Returns a user-facing error string on failure (caller writes 400), nil on success.
//
// SEC-2 / anti-enumeration: when the milestone exists but the caller cannot
// access it (cross-owner), we return the same "milestone not found" error as the
// not-exist case so that non-admin users cannot enumerate other users' milestones
// via the 400 vs. success distinction.
func validateMilestoneFK(homePath, milestoneID, projectID string, c caller) error {
	m, err := readMilestoneFile(homePath, milestoneID)
	if err != nil {
		return errors.New("milestone not found")
	}
	// Ownership check: deny access with the same error as not-found (no oracle).
	if !c.canAccess(m.Owner) {
		return errors.New("milestone not found")
	}
	if projectID != "" && m.ProjectID != projectID {
		return errors.New("milestone does not belong to this project")
	}
	return nil
}
