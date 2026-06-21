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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// milestone mirrors the on-disk format of ~/.omnipus/milestones/{id}.json.
// not-wire-format: internal disk-cache struct, mapped to wire type before serving.
type milestone struct { // not-wire-format: on-disk JSON cache; mapped to generated wire type before serving
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	// Owner is the username of the user who created this milestone. Attribution only — not an access gate.
	Owner     string `json:"owner,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// dueDatePattern validates YYYY-MM-DD format.
var dueDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// milestoneFileLock is the process-wide striped mutex pool for milestone file
// read-modify-write paths, keyed by milestone ID. It mirrors boardtask.TaskFileLock
// (same StripedLock type): the milestone PUT handler must acquire the per-ID lock
// across the WHOLE read→mutate→write so two concurrent PUTs on the same milestone
// cannot lose an update. The advisory flock inside writeMilestoneFile only serializes
// the write itself, not the preceding read, so it is insufficient on its own.
//
//nolint:gochecknoglobals
var milestoneFileLock = &boardtask.StripedLock{}

// milestonesDir returns the absolute path of ~/.omnipus/milestones/.
func (a *restAPI) milestonesDir() string {
	return filepath.Join(a.homePath, "milestones")
}

// readMilestoneFile reads a single milestone from disk by ID.
// Returns os.ErrNotExist when the file is absent.
func readMilestoneFile(home, id string) (milestone, error) {
	path := filepath.Join(home, "milestones", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return milestone{}, err
	}
	var m milestone
	if err := json.Unmarshal(data, &m); err != nil {
		return milestone{}, fmt.Errorf("parse milestone %s: %w", id, err)
	}
	return m, nil
}

// writeMilestoneFile atomically persists a milestone to disk.
func writeMilestoneFile(home string, m milestone) error {
	dir := filepath.Join(home, "milestones")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, m.ID+".json")
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// listMilestoneFiles reads all milestone JSON files from the milestones directory.
// Files that are malformed are skipped with a Warn log.
func listMilestoneFiles(home string) ([]milestone, error) {
	dir := filepath.Join(home, "milestones")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list milestones: %w", err)
	}
	var milestones []milestone
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		m, err := readMilestoneFile(home, id)
		if err != nil {
			slog.Warn("rest: milestones: skipping malformed file", "file", e.Name(), "error", err)
			continue
		}
		milestones = append(milestones, m)
	}
	return milestones, nil
}

// milestoneToWireWithProgress converts an on-disk milestone to the generated
// wire type, attaching the computed read-time progress fraction (FR-L2-010).
func milestoneToWireWithProgress(
	m milestone,
	createdAt, updatedAt time.Time,
	progress float64,
) gen.Milestone {
	p := float32(progress)
	mw := gen.Milestone{
		Id:          m.ID,
		WorkspaceId: m.WorkspaceID,
		Name:        m.Name,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		Progress:    &p,
	}
	if m.Description != "" {
		d := m.Description
		mw.Description = &d
	}
	if m.DueDate != "" {
		dd := m.DueDate
		mw.DueDate = &dd
	}
	if m.Owner != "" {
		o := m.Owner
		mw.Owner = &o
	}
	return mw
}

// computeMilestoneCounts builds a map[milestoneID]{total, done} by single-pass
// over all GTD task files. Used for milestone progress computation (FR-L2-010).
func computeMilestoneCounts(home string) (map[string][2]int, error) {
	counts := make(map[string][2]int) // [0]=total, [1]=done
	if err := scanGTDTasks(home, func(_ string, t boardTask) {
		if t.MilestoneID == "" {
			return
		}
		c := counts[t.MilestoneID]
		c[0]++ // total
		if t.Status == "done" {
			c[1]++ // done
		}
		counts[t.MilestoneID] = c
	}); err != nil {
		return nil, fmt.Errorf("scan tasks for milestone progress: %w", err)
	}
	return counts, nil
}

// milestoneProgress computes progress for a single milestone given task counts.
// Returns 0.0 when no tasks are associated (FR-L2-010).
func milestoneProgress(total, done int) float64 {
	if total == 0 {
		return 0.0
	}
	return float64(done) / float64(total)
}

// clearMilestoneOnTasks clears milestone_id on all tasks referencing the given milestoneID.
// Best-effort: individual file errors are logged as WARN, not returned (FR-L2-011).
//
// The scan is only used to identify candidate task IDs; the actual read→mutate→write
// for each candidate is performed under the process-wide boardtask.TaskFileLock striped
// mutex (with a re-read inside the lock), so a concurrent PUT/start/advance on the same
// task cannot be clobbered. Taking only the advisory flock (as before) was insufficient:
// it serialized writers against each other but the in-process board-task handlers RMW
// under TaskFileLock, not WithFlock, so the two paths could interleave and lose updates.
func clearMilestoneOnTasks(home, milestoneID string) {
	tasksDir := filepath.Join(home, "tasks")

	// Phase 1: collect candidate IDs (the lock-free scan may see slightly stale
	// state, which is fine — the authoritative check happens under the lock below).
	var candidates []string
	if err := scanGTDTasks(home, func(id string, t boardTask) {
		if t.MilestoneID == milestoneID {
			candidates = append(candidates, id)
		}
	}); err != nil {
		slog.Warn("rest: milestones: cascade clear: scan failed",
			"milestone_id", milestoneID, "error", err)
		return
	}

	// Phase 2: RMW each candidate under the striped lock, re-reading inside the
	// lock to avoid clobbering a concurrent mutation.
	for _, id := range candidates {
		taskPath := filepath.Join(tasksDir, id+".json")
		mu := boardtask.TaskFileLock.Get(id)
		mu.Lock()
		func() {
			defer mu.Unlock()
			freshData, readErr := os.ReadFile(taskPath)
			if readErr != nil {
				if !errors.Is(readErr, os.ErrNotExist) {
					slog.Warn("rest: milestones: cascade clear: re-read task failed",
						"task_id", id, "milestone_id", milestoneID, "error", readErr)
				}
				return
			}
			var fresh boardTask
			if jsonErr := json.Unmarshal(freshData, &fresh); jsonErr != nil {
				slog.Warn("rest: milestones: cascade clear: re-read task corrupt",
					"task_id", id, "milestone_id", milestoneID, "error", jsonErr)
				return
			}
			// Idempotency / race guard: only clear if it still references this milestone.
			if fresh.MilestoneID != milestoneID {
				return
			}
			fresh.MilestoneID = ""
			fresh.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			data, marshalErr := json.MarshalIndent(fresh, "", "  ")
			if marshalErr != nil {
				slog.Warn("rest: milestones: cascade clear: marshal task failed",
					"task_id", id, "milestone_id", milestoneID, "error", marshalErr)
				return
			}
			if writeErr := fileutil.WriteFileAtomic(taskPath, data, 0o600); writeErr != nil {
				slog.Warn("rest: milestones: cascade clear: write task failed",
					"task_id", id, "milestone_id", milestoneID, "error", writeErr)
			}
		}()
	}
}

// handleMilestoneList handles GET /api/v1/workspaces/{workspace_id}/milestones.
func (a *restAPI) handleMilestoneList(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	// Verify workspace exists.
	if _, ok := a.loadWorkspace(w, workspaceID); !ok {
		return
	}

	all, err := listMilestoneFiles(a.homePath)
	if err != nil {
		slog.Error("rest: milestones: list failed", "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list milestones")
		return
	}

	// Filter to this workspace.
	var workspaceMilestones []milestone
	for _, m := range all {
		if m.WorkspaceID == workspaceID {
			workspaceMilestones = append(workspaceMilestones, m)
		}
	}

	// Sort: due_date ASC, empty due_date last; then created_at ASC (FR-L2-013).
	sort.Slice(workspaceMilestones, func(i, j int) bool {
		a, b := workspaceMilestones[i].DueDate, workspaceMilestones[j].DueDate
		if a == "" && b == "" {
			return workspaceMilestones[i].CreatedAt < workspaceMilestones[j].CreatedAt
		}
		if a == "" {
			return false
		}
		if b == "" {
			return true
		}
		if a != b {
			return a < b
		}
		return workspaceMilestones[i].CreatedAt < workspaceMilestones[j].CreatedAt
	})

	// Single-pass compute progress for all milestones in this workspace.
	mCounts, err := computeMilestoneCounts(a.homePath)
	if err != nil {
		slog.Warn(
			"rest: milestones: could not compute progress",
			"workspace_id",
			workspaceID,
			"error",
			err,
		)
		mCounts = make(map[string][2]int)
	}

	result := make([]gen.Milestone, 0, len(workspaceMilestones))
	for _, m := range workspaceMilestones {
		createdAt, err := time.Parse(time.RFC3339, m.CreatedAt)
		if err != nil {
			createdAt = time.Now().UTC()
		}
		updatedAt, err := time.Parse(time.RFC3339, m.UpdatedAt)
		if err != nil {
			updatedAt = time.Now().UTC()
		}
		counts := mCounts[m.ID]
		progress := milestoneProgress(counts[0], counts[1])
		result = append(result, milestoneToWireWithProgress(m, createdAt, updatedAt, progress))
	}

	jsonOK(w, gen.MilestoneListResponse{Milestones: result, Total: len(result)})
}

// handleMilestonePost handles POST /api/v1/workspaces/{workspace_id}/milestones → 201 Created.
func (a *restAPI) handleMilestonePost(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	// Verify workspace exists.
	if _, ok := a.loadWorkspace(w, workspaceID); !ok {
		return
	}
	c := a.callerIdentity(r)

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.MilestoneCreateRequest
	if !decodeAndValidate(w, r, "MilestoneCreateRequest", &req, validateEnabled) {
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
	if req.Description != nil && len(*req.Description) > 2000 {
		jsonErr(w, http.StatusBadRequest, "description must be 2000 characters or fewer")
		return
	}

	dueDate := ""
	if req.DueDate != nil && *req.DueDate != "" {
		dueDate = *req.DueDate
		if !dueDatePattern.MatchString(dueDate) {
			jsonErr(w, http.StatusBadRequest, "due_date must be in YYYY-MM-DD format")
			return
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m := milestone{
		ID:          ulid.Make().String(),
		WorkspaceID: workspaceID,
		Name:        req.Name,
		DueDate:     dueDate,
		// Stamp the creating user's username as owner (attribution only).
		Owner:     c.Username,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.Description != nil {
		m.Description = *req.Description
	}

	if err := writeMilestoneFile(a.homePath, m); err != nil {
		slog.Error("rest: milestones: create failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not create milestone")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(
			&audit.Entry{
				Event: "milestone.create", Decision: audit.DecisionAllow,
				Details: map[string]any{"id": m.ID, "workspace_id": workspaceID},
			},
		)
	}

	// Return with progress=0 (new milestone has no tasks yet).
	createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, m.UpdatedAt)
	jsonCreated(w, milestoneToWireWithProgress(m, createdAt, updatedAt, 0.0))
}

// handleMilestoneGet handles GET /api/v1/workspaces/{workspace_id}/milestones/{id}.
func (a *restAPI) handleMilestoneGet(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, milestoneID string,
) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if err := validateEntityID(milestoneID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid milestone ID")
		return
	}

	// Verify workspace exists.
	if _, ok := a.loadWorkspace(w, workspaceID); !ok {
		return
	}

	m, err := readMilestoneFile(a.homePath, milestoneID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "milestone not found")
			return
		}
		slog.Error("rest: milestones: get failed", "id", milestoneID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read milestone")
		return
	}

	// Validate milestone belongs to the workspace.
	if m.WorkspaceID != workspaceID {
		jsonErr(w, http.StatusNotFound, "milestone not found")
		return
	}

	// Compute progress for this single milestone.
	mCounts, err := computeMilestoneCounts(a.homePath)
	if err != nil {
		slog.Warn(
			"rest: milestones: get: could not compute progress",
			"id",
			milestoneID,
			"error",
			err,
		)
		mCounts = make(map[string][2]int)
	}
	counts := mCounts[m.ID]
	progress := milestoneProgress(counts[0], counts[1])

	createdAt, err := time.Parse(time.RFC3339, m.CreatedAt)
	if err != nil {
		createdAt = time.Now().UTC()
	}
	updatedAt, err := time.Parse(time.RFC3339, m.UpdatedAt)
	if err != nil {
		updatedAt = time.Now().UTC()
	}

	jsonOK(w, milestoneToWireWithProgress(m, createdAt, updatedAt, progress))
}

// milestoneUpdateRequest is a local struct for milestone PUT that supports
// distinguishing explicit null (clear) from absent (no-op) for due_date.
// not-wire-format: local decode struct for PUT body; not emitted over the wire.
type milestoneUpdateRequest struct { // not-wire-format: PUT decode target only; never serialized to any HTTP response
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	// DueDate is not decoded here; the PUT handler reads it from the raw body map
	// so it can distinguish JSON null (clear) from absent (no-op). Go's json decoder
	// sets *json.RawMessage to nil for both absent and null, making them indistinguishable.
}

// handleMilestonePut handles PUT /api/v1/workspaces/{workspace_id}/milestones/{id} → 200.
func (a *restAPI) handleMilestonePut(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, milestoneID string,
) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if err := validateEntityID(milestoneID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid milestone ID")
		return
	}

	// Verify workspace exists.
	if _, ok := a.loadWorkspace(w, workspaceID); !ok {
		return
	}

	// Hold the per-milestone striped lock across the WHOLE read-modify-write so
	// two concurrent PUTs on the same milestone cannot lose an update. Mirrors the
	// board-task RMW (boardtask.TaskFileLock). The advisory flock inside
	// writeMilestoneFile only spans the write, not the read, so it is insufficient.
	mu := milestoneFileLock.Get(milestoneID)
	mu.Lock()
	defer mu.Unlock()

	m, err := readMilestoneFile(a.homePath, milestoneID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "milestone not found")
			return
		}
		slog.Error("rest: milestones: put read failed", "id", milestoneID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read milestone")
		return
	}
	if m.WorkspaceID != workspaceID {
		jsonErr(w, http.StatusNotFound, "milestone not found")
		return
	}

	// Decode into raw map first so we can distinguish JSON null (clear due_date)
	// from absent field (no-op). Go's json decoder sets *json.RawMessage to nil for
	// both cases, making them indistinguishable when using a typed struct directly.
	var rawBody map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var req milestoneUpdateRequest
	if raw, ok := rawBody["name"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			jsonErr(w, http.StatusBadRequest, "name must be a string")
			return
		}
		req.Name = &s
	}
	if raw, ok := rawBody["description"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			jsonErr(w, http.StatusBadRequest, "description must be a string")
			return
		}
		req.Description = &s
	}

	changed := false
	if req.Name != nil {
		if *req.Name == "" {
			jsonErr(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		if len(*req.Name) > 200 {
			jsonErr(w, http.StatusBadRequest, "name must be 200 characters or fewer")
			return
		}
		if *req.Name != m.Name {
			m.Name = *req.Name
			changed = true
		}
	}
	if req.Description != nil {
		if len(*req.Description) > 2000 {
			jsonErr(w, http.StatusBadRequest, "description must be 2000 characters or fewer")
			return
		}
		if *req.Description != m.Description {
			m.Description = *req.Description
			changed = true
		}
	}
	if rawDD, ok := rawBody["due_date"]; ok {
		if string(rawDD) == "null" {
			// Explicit JSON null — clear due_date.
			if m.DueDate != "" {
				m.DueDate = ""
				changed = true
			}
		} else {
			// Decode as string.
			var ddStr string
			if err := json.Unmarshal(rawDD, &ddStr); err != nil {
				jsonErr(w, http.StatusBadRequest, "due_date must be a string in YYYY-MM-DD format or null")
				return
			}
			if ddStr == "" {
				jsonErr(w, http.StatusBadRequest, "due_date must be in YYYY-MM-DD format or null")
				return
			}
			if !dueDatePattern.MatchString(ddStr) {
				jsonErr(w, http.StatusBadRequest, "due_date must be in YYYY-MM-DD format")
				return
			}
			if ddStr != m.DueDate {
				m.DueDate = ddStr
				changed = true
			}
		}
	}

	if !changed {
		// No-op: return current state.
		mCounts, _ := computeMilestoneCounts(a.homePath)
		counts := mCounts[m.ID]
		progress := milestoneProgress(counts[0], counts[1])
		createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
		updatedAt, _ := time.Parse(time.RFC3339, m.UpdatedAt)
		jsonOK(w, milestoneToWireWithProgress(m, createdAt, updatedAt, progress))
		return
	}

	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeMilestoneFile(a.homePath, m); err != nil {
		slog.Error("rest: milestones: put write failed", "id", milestoneID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update milestone")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(
			&audit.Entry{
				Event: "milestone.update", Decision: audit.DecisionAllow,
				Details: map[string]any{"id": milestoneID, "workspace_id": workspaceID},
			},
		)
	}

	mCounts, _ := computeMilestoneCounts(a.homePath)
	counts := mCounts[m.ID]
	progress := milestoneProgress(counts[0], counts[1])
	createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, m.UpdatedAt)
	jsonOK(w, milestoneToWireWithProgress(m, createdAt, updatedAt, progress))
}

// handleMilestoneDelete handles DELETE /api/v1/workspaces/{workspace_id}/milestones/{id} → 204.
func (a *restAPI) handleMilestoneDelete(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, milestoneID string,
) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if err := validateEntityID(milestoneID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid milestone ID")
		return
	}

	// Verify workspace exists.
	if _, ok := a.loadWorkspace(w, workspaceID); !ok {
		return
	}

	m, err := readMilestoneFile(a.homePath, milestoneID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "milestone not found")
			return
		}
		slog.Error("rest: milestones: delete read failed", "id", milestoneID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read milestone")
		return
	}
	if m.WorkspaceID != workspaceID {
		jsonErr(w, http.StatusNotFound, "milestone not found")
		return
	}

	// Clear milestone_id on all tasks referencing this milestone (FR-L2-011).
	clearMilestoneOnTasks(a.homePath, milestoneID)

	path := filepath.Join(a.milestonesDir(), milestoneID+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("rest: milestones: delete failed", "id", milestoneID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not delete milestone")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(
			&audit.Entry{
				Event: "milestone.delete", Decision: audit.DecisionAllow,
				Details: map[string]any{"id": milestoneID, "workspace_id": workspaceID},
			},
		)
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleMilestones dispatches all /api/v1/workspaces/{workspace_id}/milestones* requests.
func (a *restAPI) HandleMilestones(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")

	// Extract workspace_id from path: /api/v1/workspaces/{workspace_id}/milestones[/{id}]
	// Strip prefix /api/v1/workspaces/
	rest := strings.TrimPrefix(path, "/api/v1/workspaces/")

	// Find the /milestones segment.
	milestoneIdx := strings.Index(rest, "/milestones")
	if milestoneIdx < 0 {
		http.NotFound(w, r)
		return
	}
	workspaceID := rest[:milestoneIdx]
	after := rest[milestoneIdx+len("/milestones"):]

	if workspaceID == "" {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	if after == "" || after == "/" {
		// /api/v1/workspaces/{workspace_id}/milestones
		switch r.Method {
		case http.MethodGet:
			a.handleMilestoneList(w, r, workspaceID)
		case http.MethodPost:
			a.handleMilestonePost(w, r, workspaceID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// /api/v1/workspaces/{workspace_id}/milestones/{id}
	milestoneID := strings.TrimPrefix(after, "/")
	if strings.Contains(milestoneID, "/") {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.handleMilestoneGet(w, r, workspaceID, milestoneID)
	case http.MethodPut:
		a.handleMilestonePut(w, r, workspaceID, milestoneID)
	case http.MethodDelete:
		a.handleMilestoneDelete(w, r, workspaceID, milestoneID)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
