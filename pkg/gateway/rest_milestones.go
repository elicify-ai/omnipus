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
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// milestone mirrors the on-disk format of ~/.omnipus/milestones/{id}.json.
// not-wire-format: internal disk-cache struct, mapped to wire type before serving.
type milestone struct { // not-wire-format: on-disk JSON cache; mapped to generated wire type before serving
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// milestoneWithProgress is a milestone plus computed task progress.
// not-wire-format: local response shim that includes the computed progress field
// (not stored on disk, computed at read time per FR-L2-010).
type milestoneWithProgress struct { // not-wire-format: serialized directly to HTTP response; extends on-disk type with computed progress field
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	DueDate     *string   `json:"due_date,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Progress    float64   `json:"progress"`
}

// dueDatePattern validates YYYY-MM-DD format.
var dueDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

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

// milestoneToWireWithProgress converts a milestone to a milestoneWithProgress (includes progress field).
func milestoneToWireWithProgress(
	m milestone,
	createdAt, updatedAt time.Time,
	progress float64,
) milestoneWithProgress {
	mwp := milestoneWithProgress{
		ID:        m.ID,
		ProjectID: m.ProjectID,
		Name:      m.Name,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Progress:  progress,
	}
	if m.Description != "" {
		d := m.Description
		mwp.Description = &d
	}
	if m.DueDate != "" {
		dd := m.DueDate
		mwp.DueDate = &dd
	}
	return mwp
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
func clearMilestoneOnTasks(home, milestoneID string) {
	tasksDir := filepath.Join(home, "tasks")
	if err := scanGTDTasks(home, func(id string, t boardTask) {
		if t.MilestoneID != milestoneID {
			return
		}
		t.MilestoneID = ""
		t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		taskPath := filepath.Join(tasksDir, id+".json")
		data, err := json.MarshalIndent(t, "", "  ")
		if err != nil {
			slog.Warn("rest: milestones: cascade clear: marshal task failed",
				"task_id", id, "milestone_id", milestoneID, "error", err)
			return
		}
		if err := fileutil.WithFlock(taskPath, func() error {
			return fileutil.WriteFileAtomic(taskPath, data, 0o600)
		}); err != nil {
			slog.Warn("rest: milestones: cascade clear: write task failed",
				"task_id", id, "milestone_id", milestoneID, "error", err)
		}
	}); err != nil {
		slog.Warn("rest: milestones: cascade clear: scan failed",
			"milestone_id", milestoneID, "error", err)
	}
}

// handleMilestoneList handles GET /api/v1/projects/{project_id}/milestones.
func (a *restAPI) handleMilestoneList(w http.ResponseWriter, r *http.Request, projectID string) {
	if err := validateEntityID(projectID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}

	// Verify project exists.
	if _, ok := a.loadProject(w, projectID); !ok {
		return
	}

	all, err := listMilestoneFiles(a.homePath)
	if err != nil {
		slog.Error("rest: milestones: list failed", "project_id", projectID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list milestones")
		return
	}

	// Filter to this project.
	var projectMilestones []milestone
	for _, m := range all {
		if m.ProjectID == projectID {
			projectMilestones = append(projectMilestones, m)
		}
	}

	// Sort: due_date ASC, empty due_date last; then created_at ASC (FR-L2-013).
	sort.Slice(projectMilestones, func(i, j int) bool {
		a, b := projectMilestones[i].DueDate, projectMilestones[j].DueDate
		if a == "" && b == "" {
			return projectMilestones[i].CreatedAt < projectMilestones[j].CreatedAt
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
		return projectMilestones[i].CreatedAt < projectMilestones[j].CreatedAt
	})

	// Single-pass compute progress for all milestones in this project.
	mCounts, err := computeMilestoneCounts(a.homePath)
	if err != nil {
		slog.Warn(
			"rest: milestones: could not compute progress",
			"project_id",
			projectID,
			"error",
			err,
		)
		mCounts = make(map[string][2]int)
	}

	type listMilestoneShim struct { // not-wire-format: response shim including computed progress field
		Milestones []milestoneWithProgress `json:"milestones"`
		Total      int                     `json:"total"`
	}
	result := make([]milestoneWithProgress, 0, len(projectMilestones))
	for _, m := range projectMilestones {
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

	jsonOK(w, listMilestoneShim{Milestones: result, Total: len(result)})
}

// handleMilestonePost handles POST /api/v1/projects/{project_id}/milestones → 201 Created.
func (a *restAPI) handleMilestonePost(w http.ResponseWriter, r *http.Request, projectID string) {
	if err := validateEntityID(projectID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}

	// Verify project exists.
	if _, ok := a.loadProject(w, projectID); !ok {
		return
	}

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
		ID:        ulid.Make().String(),
		ProjectID: projectID,
		Name:      req.Name,
		DueDate:   dueDate,
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
				Details: map[string]any{"id": m.ID, "project_id": projectID},
			},
		)
	}

	// Return with progress=0 (new milestone has no tasks yet).
	createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, m.UpdatedAt)
	jsonCreated(w, milestoneToWireWithProgress(m, createdAt, updatedAt, 0.0))
}

// handleMilestoneGet handles GET /api/v1/projects/{project_id}/milestones/{id}.
func (a *restAPI) handleMilestoneGet(
	w http.ResponseWriter,
	r *http.Request,
	projectID, milestoneID string,
) {
	if err := validateEntityID(projectID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}
	if err := validateEntityID(milestoneID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid milestone ID")
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

	// Validate project ownership.
	if m.ProjectID != projectID {
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

// handleMilestonePut handles PUT /api/v1/projects/{project_id}/milestones/{id} → 200.
func (a *restAPI) handleMilestonePut(
	w http.ResponseWriter,
	r *http.Request,
	projectID, milestoneID string,
) {
	if err := validateEntityID(projectID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}
	if err := validateEntityID(milestoneID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid milestone ID")
		return
	}

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
	if m.ProjectID != projectID {
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
				Details: map[string]any{"id": milestoneID, "project_id": projectID},
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

// handleMilestoneDelete handles DELETE /api/v1/projects/{project_id}/milestones/{id} → 204.
func (a *restAPI) handleMilestoneDelete(
	w http.ResponseWriter,
	r *http.Request,
	projectID, milestoneID string,
) {
	if err := validateEntityID(projectID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}
	if err := validateEntityID(milestoneID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid milestone ID")
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
	if m.ProjectID != projectID {
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
				Details: map[string]any{"id": milestoneID, "project_id": projectID},
			},
		)
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleMilestones dispatches all /api/v1/projects/{project_id}/milestones* requests.
func (a *restAPI) HandleMilestones(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")

	// Extract project_id from path: /api/v1/projects/{project_id}/milestones[/{id}]
	// Strip prefix /api/v1/projects/
	rest := strings.TrimPrefix(path, "/api/v1/projects/")

	// Find the /milestones segment.
	milestoneIdx := strings.Index(rest, "/milestones")
	if milestoneIdx < 0 {
		http.NotFound(w, r)
		return
	}
	projectID := rest[:milestoneIdx]
	after := rest[milestoneIdx+len("/milestones"):]

	if projectID == "" {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}

	if after == "" || after == "/" {
		// /api/v1/projects/{project_id}/milestones
		switch r.Method {
		case http.MethodGet:
			a.handleMilestoneList(w, r, projectID)
		case http.MethodPost:
			a.handleMilestonePost(w, r, projectID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// /api/v1/projects/{project_id}/milestones/{id}
	milestoneID := strings.TrimPrefix(after, "/")
	if strings.Contains(milestoneID, "/") {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.handleMilestoneGet(w, r, projectID, milestoneID)
	case http.MethodPut:
		a.handleMilestonePut(w, r, projectID, milestoneID)
	case http.MethodDelete:
		a.handleMilestoneDelete(w, r, projectID, milestoneID)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
