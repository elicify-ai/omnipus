//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// projectLinkFileMu serialises all writes to project_session_links.jsonl from
// the gateway layer. Must be held (write lock) for any write/rewrite; RLock is
// sufficient for read-only access.
var projectLinkFileMu sync.RWMutex

// storedProject mirrors the on-disk format of ~/.omnipus/projects/{id}.json.
// not-wire-format
type storedProject struct { // not-wire-format
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Pinned      bool     `json:"pinned"`
	PinOrder    int      `json:"pin_order"`
	CoreTeam    []string `json:"core_team,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// storedLink mirrors one line in ~/.omnipus/project_session_links.jsonl.
// not-wire-format
type storedLink struct { // not-wire-format
	ProjectID string `json:"project_id"`
	SessionID string `json:"session_id"`
	CreatedAt string `json:"created_at"`
}

// readProjectFile reads and parses ~/.omnipus/projects/{id}.json, applying the
// legacy "agent_ids" → "core_team" migration if needed.
func readProjectFile(home, id string) (storedProject, error) {
	path := filepath.Join(home, "projects", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return storedProject{}, fmt.Errorf("NOT_FOUND: %s", id)
		}
		return storedProject{}, fmt.Errorf("read project %s: %w", id, err)
	}
	var p storedProject
	if err := json.Unmarshal(data, &p); err != nil {
		return storedProject{}, fmt.Errorf("parse project %s: %w", id, err)
	}
	// Lazy migration: core_team → agent_ids fallback.
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
		p.Status = "active"
	}
	return p, nil
}

// listProjectFiles reads all project JSON files from ~/.omnipus/projects/.
// Files that are malformed are skipped with a Warn log.
func listProjectFiles(home string) ([]storedProject, error) {
	dir := filepath.Join(home, "projects")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
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

// computeProjectTaskCounts returns a map[projectID]count by doing a single pass
// over all task files in ~/.omnipus/tasks/.
func computeProjectTaskCounts(home string) (map[string]int, error) {
	tasksDir := filepath.Join(home, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}
	counts := make(map[string]int)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tasksDir, e.Name()))
		if err != nil {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		if pidRaw, ok := raw["project_id"]; ok {
			var pid string
			if json.Unmarshal(pidRaw, &pid) == nil && pid != "" {
				counts[pid]++
			}
		}
	}
	return counts, nil
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
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

// projectToWire converts a storedProject to the gen.Project wire type.
// taskCount is passed in (computed in a single pass by the caller).
func projectToWire(p storedProject, taskCount int) gen.Project {
	createdAt, _ := time.Parse(time.RFC3339, p.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, p.UpdatedAt)

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
	return w
}

// readProjectSessionLinks returns deduplicated links for the given projectID by
// reading ~/.omnipus/project_session_links.jsonl. Uses projectLinkFileMu (read).
func readProjectSessionLinks(home, projectID string) []storedLink {
	projectLinkFileMu.RLock()
	defer projectLinkFileMu.RUnlock()

	path := filepath.Join(home, "project_session_links.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck

	seen := make(map[string]struct{})
	var out []storedLink
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var link storedLink
		if json.Unmarshal([]byte(line), &link) != nil {
			continue
		}
		if link.ProjectID != projectID {
			continue
		}
		key := link.ProjectID + ":" + link.SessionID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, link)
	}
	return out
}

// rewriteLinksExcluding rewrites project_session_links.jsonl omitting all entries
// for the given projectID. Uses projectLinkFileMu (write).
func rewriteLinksExcluding(home, projectID string) {
	projectLinkFileMu.Lock()
	defer projectLinkFileMu.Unlock()

	path := filepath.Join(home, "project_session_links.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("rest: project cascade: cannot read link file",
				"error", err, "project_id", projectID)
		}
		return
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var link storedLink
		if err := json.Unmarshal([]byte(line), &link); err != nil || link.ProjectID == projectID {
			continue
		}
		kept = append(kept, line)
	}

	var content []byte
	if len(kept) > 0 {
		content = []byte(strings.Join(kept, "\n") + "\n")
	}
	tmp := path + ".gw.tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		slog.Warn("rest: project cascade: failed to write link temp file",
			"error", err, "project_id", projectID)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("rest: project cascade: failed to rename link temp file",
			"error", err, "project_id", projectID)
	}
}

// deleteTasksForProject removes all task files whose project_id matches projectID.
func deleteTasksForProject(home, projectID string) {
	tasksDir := filepath.Join(home, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		taskPath := filepath.Join(tasksDir, e.Name())
		data, err := os.ReadFile(taskPath)
		if err != nil {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		if pidRaw, ok := raw["project_id"]; ok {
			var pid string
			if json.Unmarshal(pidRaw, &pid) == nil && pid == projectID {
				if err := os.Remove(taskPath); err != nil && !os.IsNotExist(err) {
					slog.Warn("rest: project cascade: failed to delete task",
						"file", e.Name(), "error", err)
				}
			}
		}
	}
}

// HandleProjects dispatches all /api/v1/projects* requests.
func (a *restAPI) HandleProjects(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/projects")

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
		statusFilter = "active"
	}

	projects, err := listProjectFiles(a.homePath)
	if err != nil {
		slog.Error("rest: list projects", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list projects: %v", err))
		return
	}

	taskCounts, err := computeProjectTaskCounts(a.homePath)
	if err != nil {
		slog.Warn("rest: list projects: could not compute task counts", "error", err)
		taskCounts = make(map[string]int)
	}

	var result []gen.Project
	for _, p := range projects {
		if statusFilter != "all" && p.Status != statusFilter {
			continue
		}
		result = append(result, projectToWire(p, taskCounts[p.ID]))
	}
	if result == nil {
		result = []gen.Project{}
	}
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
		Status:    "active",
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
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save project: %v", err))
		return
	}
	jsonCreated(w, projectToWire(p, 0))
}

func (a *restAPI) handleProjectGet(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}
	p, err := readProjectFile(a.homePath, id)
	if err != nil {
		if strings.HasPrefix(err.Error(), "NOT_FOUND:") {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("project %q not found", id))
			return
		}
		slog.Error("rest: get project", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read project: %v", err))
		return
	}
	taskCounts, err := computeProjectTaskCounts(a.homePath)
	if err != nil {
		slog.Warn("rest: get project: could not compute task counts", "error", err)
		taskCounts = make(map[string]int)
	}
	jsonOK(w, projectToWire(p, taskCounts[p.ID]))
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

	p, err := readProjectFile(a.homePath, id)
	if err != nil {
		if strings.HasPrefix(err.Error(), "NOT_FOUND:") {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("project %q not found", id))
			return
		}
		slog.Error("rest: update project: read", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read project: %v", err))
		return
	}

	// Apply partial update (merge semantics).
	if req.Name != nil {
		p.Name = *req.Name
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
	if req.Status != nil {
		p.Status = string(*req.Status)
	}
	if req.Pinned != nil {
		p.Pinned = *req.Pinned
	}
	if req.PinOrder != nil {
		p.PinOrder = *req.PinOrder
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := writeProjectFile(a.homePath, p); err != nil {
		slog.Error("rest: update project: write", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save project: %v", err))
		return
	}

	taskCounts, err := computeProjectTaskCounts(a.homePath)
	if err != nil {
		slog.Warn("rest: update project: could not compute task counts", "error", err)
		taskCounts = make(map[string]int)
	}
	jsonOK(w, projectToWire(p, taskCounts[p.ID]))
}

func (a *restAPI) handleProjectDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		jsonErr(w, http.StatusBadRequest, `confirm=true query parameter required to delete a project`)
		return
	}

	// Verify the project exists before cascading.
	if _, err := readProjectFile(a.homePath, id); err != nil {
		if strings.HasPrefix(err.Error(), "NOT_FOUND:") {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("project %q not found", id))
			return
		}
		slog.Error("rest: delete project: read", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read project: %v", err))
		return
	}

	// Cascade: tasks → link entries → project file.
	deleteTasksForProject(a.homePath, id)
	rewriteLinksExcluding(a.homePath, id)

	path := filepath.Join(a.homePath, "projects", id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Error("rest: delete project: remove file", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not delete project: %v", err))
		return
	}

	jsonOK(w, map[string]string{"deleted": id})
}

func (a *restAPI) handleProjectSessions(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid project ID")
		return
	}

	// Verify project exists.
	if _, err := readProjectFile(a.homePath, id); err != nil {
		if strings.HasPrefix(err.Error(), "NOT_FOUND:") {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("project %q not found", id))
			return
		}
		slog.Error("rest: project sessions: read project", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read project: %v", err))
		return
	}

	links := readProjectSessionLinks(a.homePath, id)
	result := make([]gen.ProjectSessionLink, 0, len(links))
	for _, l := range links {
		createdAt, _ := time.Parse(time.RFC3339, l.CreatedAt)
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
