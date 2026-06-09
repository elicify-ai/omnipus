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
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// errProjectNotFound is returned by readProjectFile when the project file does
// not exist on disk. Callers use errors.Is(err, errProjectNotFound).
var errProjectNotFound = errors.New("project not found")

// storedProject mirrors the on-disk format of ~/.omnipus/projects/{id}.json.
type storedProject struct { // not-wire-format: internal disk-cache struct, mapped to gen.Project before sending over the wire
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Pinned      bool     `json:"pinned"`
	PinOrder    int      `json:"pin_order"`
	CoreTeam    []string `json:"core_team,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	IsDefault   bool     `json:"is_default,omitempty"` // true only for the auto-created Inbox project (FR-INX-4)
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// readProjectFile reads and parses ~/.omnipus/projects/{id}.json, applying the
// legacy "agent_ids" → "core_team" migration if needed.
func readProjectFile(home, id string) (storedProject, error) {
	path := filepath.Join(home, "projects", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return storedProject{}, fmt.Errorf("%w: %s", errProjectNotFound, id)
		}
		return storedProject{}, fmt.Errorf("read project %s: %w", id, err)
	}
	var p storedProject
	if err := json.Unmarshal(data, &p); err != nil {
		return storedProject{}, fmt.Errorf("parse project %s: %w", id, err)
	}
	// Lazy migration: agent_ids → core_team fallback.
	if len(p.CoreTeam) == 0 {
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) == nil {
			if legacyRaw, ok := raw["agent_ids"]; ok {
				var legacy []string
				if json.Unmarshal(legacyRaw, &legacy) == nil {
					p.CoreTeam = legacy
				}
			}
		}
	}
	// Legacy files without status field default to active.
	if p.Status == "" {
		p.Status = string(gen.ProjectStatusActive)
	}
	return p, nil
}

// listProjectFiles reads all project JSON files from ~/.omnipus/projects/.
// Files that are malformed are skipped with a Warn log.
func listProjectFiles(home string) ([]storedProject, error) {
	dir := filepath.Join(home, "projects")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list projects: %w", err)
	}
	var projects []storedProject
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		p, err := readProjectFile(home, id)
		if err != nil {
			slog.Warn("rest: skipping malformed project file", "file", e.Name(), "error", err)
			continue
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// scanGTDTasks walks the tasks directory and calls fn for every file that
// deserialises to a GTD task (status ∈ {inbox,next,active,waiting,done}).
// Returns the first I/O error; fn errors are not propagated.
func scanGTDTasks(home string, fn func(id string, t boardTask)) error {
	dir := filepath.Join(home, "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			slog.Warn("rest_projects: scanGTDTasks: failed to read task file", "file", e.Name(), "error", err)
			continue
		}
		var t boardTask
		if err := json.Unmarshal(data, &t); err != nil {
			slog.Warn("rest_projects: scanGTDTasks: failed to parse task file", "file", e.Name(), "error", err)
			continue
		}
		if !isGTDTask(t.Status) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		fn(id, t)
	}
	return nil
}

// computeProjectTaskCounts returns a map[projectID]count by doing a single pass
// over all task files in ~/.omnipus/tasks/. Used by list (O(N) for all projects).
// Only GTD tasks (status ∈ {inbox,next,active,waiting,done}) are counted; workflow
// tasks from pkg/taskstore share the same directory and are excluded.
func computeProjectTaskCounts(home string) (map[string]int, error) {
	counts := make(map[string]int)
	if err := scanGTDTasks(home, func(_ string, t boardTask) {
		if t.ProjectID != "" {
			counts[t.ProjectID]++
		}
	}); err != nil {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}
	return counts, nil
}

// countTasksForProject counts GTD tasks belonging to a single project. O(N tasks)
// but avoids building the full map — used by single-project GET/PUT.
// Only GTD tasks (status ∈ {inbox,next,active,waiting,done}) are counted.
func countTasksForProject(home, projectID string) int {
	count := 0
	if err := scanGTDTasks(home, func(_ string, t boardTask) {
		if t.ProjectID == projectID {
			count++
		}
	}); err != nil {
		slog.Warn("rest_projects: countTasksForProject: failed to scan tasks",
			"project_id", projectID, "error", err)
		return 0
	}
	return count
}

// writeProjectFile atomically writes p to ~/.omnipus/projects/{id}.json.
func writeProjectFile(home string, p storedProject) error {
	dir := filepath.Join(home, "projects")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir projects: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal project: %w", err)
	}
	path := filepath.Join(dir, p.ID+".json")
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// projectWireResponse extends gen.Project with the is_default field (FR-L2-027).
// The generated gen.Project type does not include is_default; this shim adds it.
// not-wire-format: local response shim, not a hand-written wire type.
type projectWireResponse struct { // not-wire-format
	gen.Project
	IsDefault bool `json:"is_default"`
}

// projectToWire converts a storedProject to the projectWireResponse type.
// taskCount is passed in (computed by the caller).
func projectToWire(p storedProject, taskCount int) projectWireResponse {
	createdAt, err := time.Parse(time.RFC3339, p.CreatedAt)
	if err != nil {
		slog.Warn("rest: project: invalid created_at timestamp", "id", p.ID, "raw", p.CreatedAt)
		createdAt = time.Now().UTC()
	}
	updatedAt, err := time.Parse(time.RFC3339, p.UpdatedAt)
	if err != nil {
		slog.Warn("rest: project: invalid updated_at timestamp", "id", p.ID, "raw", p.UpdatedAt)
		updatedAt = time.Now().UTC()
	}

	w := gen.Project{
		Id:        p.ID,
		Name:      p.Name,
		Status:    gen.ProjectStatus(p.Status),
		Pinned:    p.Pinned,
		PinOrder:  p.PinOrder,
		TaskCount: taskCount,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if p.Description != "" {
		w.Description = &p.Description
	}
	if p.Repository != "" {
		w.Repository = &p.Repository
	}
	if len(p.CoreTeam) > 0 {
		team := make([]string, len(p.CoreTeam))
		copy(team, p.CoreTeam)
		w.CoreTeam = &team
	}
	return projectWireResponse{Project: w, IsDefault: p.IsDefault}
}

// deleteTasksForProject removes all GTD task files whose project_id matches projectID.
// Only GTD tasks (status ∈ {inbox,next,active,waiting,done}) are deleted; workflow
// tasks from pkg/taskstore share the same directory and are preserved.
// Per FR-007: individual task-file deletion failures are logged and skipped (best-effort);
// only a scan failure (cannot enumerate the tasks directory) causes a non-nil return.
func deleteTasksForProject(home, projectID string) error {
	tasksDir := filepath.Join(home, "tasks")
	if err := scanGTDTasks(home, func(id string, t boardTask) {
		if t.ProjectID == projectID {
			taskPath := filepath.Join(tasksDir, id+".json")
			if err := os.Remove(taskPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("rest: project cascade: failed to delete task",
					"file", id+".json", "error", err)
			}
		}
	}); err != nil {
		return fmt.Errorf("scan tasks for cascade delete: %w", err)
	}
	return nil
}

// loadProject reads a project by ID and writes the appropriate HTTP error if absent.
// Returns (p, true) on success or (_, false) after writing the error response.
func (a *restAPI) loadProject(w http.ResponseWriter, id string) (storedProject, bool) {
	p, err := readProjectFile(a.homePath, id)
	if err != nil {
		if errors.Is(err, errProjectNotFound) {
			jsonErr(w, http.StatusNotFound, "project not found")
			return storedProject{}, false
		}
		slog.Error("rest: load project", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return storedProject{}, false
	}
	return p, true
}

// ensureInboxProject checks if the default Inbox project exists; if not, creates it.
// Idempotent: if a project with is_default=true already exists, this is a no-op.
// On failure, logs an error but returns nil (non-fatal per spec — gateway continues).
// (FR-L2-001, FR-INX-1)
func ensureInboxProject(home string) error {
	projects, err := listProjectFiles(home)
	if err != nil {
		return fmt.Errorf("ensureInboxProject: list projects: %w", err)
	}
	for _, p := range projects {
		if p.IsDefault {
			return nil // already exists
		}
	}
	// No default project found — create the Inbox.
	now := time.Now().UTC().Format(time.RFC3339)
	inbox := storedProject{
		ID:        ulid.Make().String(),
		Name:      "Inbox",
		Status:    string(gen.ProjectStatusActive),
		Pinned:    false,
		PinOrder:  0,
		IsDefault: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeProjectFile(home, inbox); err != nil {
		return fmt.Errorf("ensureInboxProject: write: %w", err)
	}
	slog.Info("rest: inbox project auto-created", "id", inbox.ID)
	return nil
}

// HandleProjects dispatches all /api/v1/projects* requests.
func (a *restAPI) HandleProjects(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/projects")

	// /api/v1/projects/{id}/milestones[/{milestoneId}] — delegate to HandleMilestones.
	if strings.Contains(rest, "/milestones") {
		a.HandleMilestones(w, r)
		return
	}

	// /api/v1/projects/{id}/sessions
	if strings.HasSuffix(rest, "/sessions") {
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/sessions")
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleProjectSessions(w, r, id)
		return
	}

	// /api/v1/projects/{id}
	if len(rest) > 1 {
		id := strings.TrimPrefix(rest, "/")
		// Unknown sub-paths like /projects/{id}/anything return 404.
		if strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.handleProjectGet(w, r, id)
		case http.MethodPut:
			a.handleProjectPut(w, r, id)
		case http.MethodDelete:
			a.handleProjectDelete(w, r, id)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// /api/v1/projects
	switch r.Method {
	case http.MethodGet:
		a.handleProjectList(w, r)
	case http.MethodPost:
		a.handleProjectPost(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) handleProjectList(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = string(gen.ProjectStatusActive)
	}
	switch statusFilter {
	case string(gen.ProjectStatusActive), string(gen.ProjectStatusArchived), "all":
		// valid
	default:
		jsonErr(w, http.StatusBadRequest, "invalid status filter")
		return
	}

	projects, err := listProjectFiles(a.homePath)
	if err != nil {
		slog.Error("rest: list projects", "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	taskCounts, err := computeProjectTaskCounts(a.homePath)
	if err != nil {
		slog.Warn("rest: list projects: could not compute task counts", "error", err)
		taskCounts = make(map[string]int)
	}

	var result []projectWireResponse
	for _, p := range projects {
		if statusFilter != "all" && p.Status != statusFilter {
			continue
		}
		result = append(result, projectToWire(p, taskCounts[p.ID]))
	}
	if result == nil {
		result = []projectWireResponse{}
	}

	// Sort: Inbox (is_default) always first, then pinned items (ascending pin_order),
	// then unpinned newest-first.
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDefault != result[j].IsDefault {
			return result[i].IsDefault
		}
		if result[i].Pinned != result[j].Pinned {
			return result[i].Pinned
		}
		if result[i].Pinned && result[j].Pinned {
			if result[i].PinOrder != result[j].PinOrder {
				return result[i].PinOrder < result[j].PinOrder
			}
			return result[i].CreatedAt.Before(result[j].CreatedAt)
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	jsonOK(w, result)
}

func (a *restAPI) handleProjectPost(w http.ResponseWriter, r *http.Request) {
	var req gen.ProjectCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ProjectCreateRequest", &req, validateEnabled) {
		return
	}

	if req.Name == "" {
		jsonErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 200 {
		jsonErr(w, http.StatusBadRequest, "name exceeds 200 characters")
		return
	}
	if req.Description != nil && len(*req.Description) > 2000 {
		jsonErr(w, http.StatusBadRequest, "description exceeds 2000 characters")
		return
	}
	if req.Repository != nil && len(*req.Repository) > 500 {
		jsonErr(w, http.StatusBadRequest, "repository exceeds 500 characters")
		return
	}
	if req.CoreTeam != nil && len(*req.CoreTeam) > 20 {
		jsonErr(w, http.StatusBadRequest, "core_team may have at most 20 entries")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	p := storedProject{
		ID:        ulid.Make().String(),
		Name:      req.Name,
		Status:    string(gen.ProjectStatusActive),
		Pinned:    false,
		PinOrder:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if req.Repository != nil {
		p.Repository = *req.Repository
	}
	if req.CoreTeam != nil {
		p.CoreTeam = deduplicateStrings(*req.CoreTeam)
	}

	if err := writeProjectFile(a.homePath, p); err != nil {
		slog.Error("rest: create project", "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	wire := projectToWire(p, 0)
	if a.auditor != nil {
		_ = a.auditor.Log(
			&audit.Entry{
				Event:    "project.create",
				Decision: "allowed",
				Details:  map[string]any{"id": p.ID, "name": p.Name},
			},
		)
	}
	jsonCreated(w, wire)
}

func (a *restAPI) handleProjectGet(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}
	p, ok := a.loadProject(w, id)
	if !ok {
		return
	}
	jsonOK(w, projectToWire(p, countTasksForProject(a.homePath, id)))
}

func (a *restAPI) handleProjectPut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}
	var req gen.ProjectUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ProjectUpdateRequest", &req, validateEnabled) {
		return
	}

	// Validate fields before touching disk.
	if req.Name != nil {
		if *req.Name == "" {
			jsonErr(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		if len(*req.Name) > 200 {
			jsonErr(w, http.StatusBadRequest, "name exceeds 200 characters")
			return
		}
	}
	if req.Description != nil && len(*req.Description) > 2000 {
		jsonErr(w, http.StatusBadRequest, "description exceeds 2000 characters")
		return
	}
	if req.Repository != nil && len(*req.Repository) > 500 {
		jsonErr(w, http.StatusBadRequest, "repository exceeds 500 characters")
		return
	}
	if req.CoreTeam != nil && len(*req.CoreTeam) > 20 {
		jsonErr(w, http.StatusBadRequest, "core_team may have at most 20 entries")
		return
	}
	if req.Status != nil && !req.Status.Valid() {
		jsonErr(w, http.StatusBadRequest, `status must be "active" or "archived"`)
		return
	}

	p, ok := a.loadProject(w, id)
	if !ok {
		return
	}

	// Apply partial update (merge semantics) — track whether anything changed.
	changed := false
	if req.Name != nil && *req.Name != p.Name {
		p.Name = *req.Name
		changed = true
	}
	if req.Description != nil && *req.Description != p.Description {
		p.Description = *req.Description
		changed = true
	}
	if req.Repository != nil && *req.Repository != p.Repository {
		p.Repository = *req.Repository
		changed = true
	}
	if req.CoreTeam != nil {
		deduped := deduplicateStrings(*req.CoreTeam)
		if !stringSlicesEqual(deduped, p.CoreTeam) {
			p.CoreTeam = deduped
			changed = true
		}
	}
	if req.Status != nil && string(*req.Status) != p.Status {
		p.Status = string(*req.Status)
		changed = true
	}
	if req.Pinned != nil && *req.Pinned != p.Pinned {
		p.Pinned = *req.Pinned
		changed = true
	}
	if req.PinOrder != nil && *req.PinOrder != p.PinOrder {
		p.PinOrder = *req.PinOrder
		changed = true
	}

	// No-op: nothing changed — return current state without writing.
	if !changed {
		jsonOK(w, projectToWire(p, countTasksForProject(a.homePath, id)))
		return
	}

	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := writeProjectFile(a.homePath, p); err != nil {
		slog.Error("rest: update project: write", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(&audit.Entry{Event: "project.update", Decision: "allowed", Details: map[string]any{"id": id}})
	}
	jsonOK(w, projectToWire(p, countTasksForProject(a.homePath, id)))
}

func (a *restAPI) handleProjectDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}

	// Verify the project exists before cascading.
	p, ok := a.loadProject(w, id)
	if !ok {
		return
	}

	// Inbox (is_default) cannot be deleted (FR-L2-002 / FR-INX-2).
	if p.IsDefault {
		jsonErr(w, http.StatusConflict, "cannot delete the default Inbox project")
		return
	}

	// Cascade: (1) milestones for project → (2) clear milestone_id on those tasks →
	// (3) delete tasks → (4) session links → (5) project file.
	// Per FR-L2-028 and FR-007: individual file errors are best-effort (logged, not fatal).
	deleteMilestonesForProject(a.homePath, id)

	if err := deleteTasksForProject(a.homePath, id); err != nil {
		slog.Error("rest: delete project: cascade tasks", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to scan tasks for cascade delete")
		return
	}
	systools.RemoveLinksForProject(a.homePath, id)

	path := filepath.Join(a.homePath, "projects", id+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("rest: delete project: remove file", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(&audit.Entry{Event: "project.delete", Decision: "allowed", Details: map[string]any{"id": id}})
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteMilestonesForProject removes all milestone files for the given project and
// clears milestone_id on tasks that referenced them (FR-L2-028).
// Best-effort: individual file errors are logged and skipped.
func deleteMilestonesForProject(home, projectID string) {
	all, err := listMilestoneFiles(home)
	if err != nil {
		slog.Warn("rest: project cascade: could not list milestones for project",
			"project_id", projectID, "error", err)
		return
	}
	for _, m := range all {
		if m.ProjectID != projectID {
			continue
		}
		// Clear milestone_id on tasks referencing this milestone before deleting.
		clearMilestoneOnTasks(home, m.ID)
		path := filepath.Join(home, "milestones", m.ID+".json")
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("rest: project cascade: failed to delete milestone",
				"milestone_id", m.ID, "project_id", projectID, "error", err)
		}
	}
}

func (a *restAPI) handleProjectSessions(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}

	// Verify project exists.
	if _, ok := a.loadProject(w, id); !ok {
		return
	}

	links := systools.ReadLinks(a.homePath, id)
	result := make([]gen.ProjectSessionLink, 0, len(links))
	for _, l := range links {
		createdAt, err := time.Parse(time.RFC3339, l.CreatedAt)
		if err != nil {
			slog.Warn("rest: project sessions: invalid link created_at",
				"project_id", id, "session_id", l.SessionID, "raw", l.CreatedAt)
			createdAt = time.Now().UTC()
		}
		result = append(result, gen.ProjectSessionLink{
			SessionId: l.SessionID,
			CreatedAt: createdAt,
		})
	}
	jsonOK(w, result)
}

// deduplicateStrings removes duplicate strings (case-sensitive) while preserving
// order. Empty strings are dropped.
func deduplicateStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// stringSlicesEqual returns true if a and b contain the same elements in the
// same order.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
