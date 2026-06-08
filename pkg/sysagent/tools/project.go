// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/oklog/ulid/v2"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

type project struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"` // "active" | "archived"
	Pinned      bool     `json:"pinned"`
	PinOrder    int      `json:"pin_order"`           // 0 = unpinned
	CoreTeam    []string `json:"core_team,omitempty"` // replaces "agent_ids"
	Repository  string   `json:"repository,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

func projectsDir(home string) string { return filepath.Join(home, "projects") }

// projectFromFile reads a project JSON and handles the legacy "agent_ids" → "core_team" migration.
func projectFromFile(data []byte) (project, error) {
	var p project
	if err := json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	// Lazy migration: if core_team is empty but agent_ids exists, migrate.
	if len(p.CoreTeam) == 0 {
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(data, &raw)
		if legacyRaw, ok := raw["agent_ids"]; ok {
			var legacy []string
			if json.Unmarshal(legacyRaw, &legacy) == nil {
				p.CoreTeam = legacy
			}
		}
	}
	// Legacy files without status field default to active.
	if p.Status == "" {
		p.Status = "active"
	}
	return p, nil
}

// sanitizeCoreTeam deduplicates (case-sensitive) and enforces max 20 entries.
// Returns an error if the limit is exceeded (after dedup). Empty strings are silently dropped.
func sanitizeCoreTeam(raw []any) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	for _, v := range raw {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	if len(out) > 20 {
		return nil, fmt.Errorf("core_team may have at most 20 entries, got %d", len(out))
	}
	return out, nil
}

// computeTaskCount counts GTD tasks on disk with matching project_id.
// Only GTD tasks (status ∈ {inbox,next,active,waiting,done}) are counted;
// workflow tasks (queued/assigned/running/completed/failed) are excluded.
// Single-pass: called per-project only (list endpoint does its own single-pass; see computeTaskCounts).
func computeTaskCount(home, projectID string) int {
	tasks, err := listEntities[task](tasksDir(home))
	if err != nil {
		return 0
	}
	n := 0
	for i := range tasks {
		if !gtdStatusSet[tasks[i].Status] {
			continue
		}
		if tasks[i].ProjectID == projectID {
			n++
		}
	}
	return n
}

// computeTaskCounts returns a map[projectID]count from a single listEntities call.
// Only GTD tasks (status ∈ {inbox,next,active,waiting,done}) are counted;
// workflow tasks (queued/assigned/running/completed/failed) are excluded.
// Use this for list responses to avoid O(N×M) disk reads.
func computeTaskCounts(home string) map[string]int {
	counts := make(map[string]int)
	tasks, err := listEntities[task](tasksDir(home))
	if err != nil {
		return counts
	}
	for i := range tasks {
		if !gtdStatusSet[tasks[i].Status] {
			continue
		}
		if tasks[i].ProjectID != "" {
			counts[tasks[i].ProjectID]++
		}
	}
	return counts
}

// readProjectFromDisk reads the raw bytes for a project entity and calls projectFromFile
// to handle legacy migration. Returns an error if the file is missing or unreadable.
func readProjectFromDisk(home, id string) (project, error) {
	if err := validateID(id); err != nil {
		return project{}, fmt.Errorf("read project: %w", err)
	}
	path := entityPath(projectsDir(home), id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return project{}, fmt.Errorf("NOT_FOUND: %s", id)
		}
		return project{}, fmt.Errorf("read project %s: %w", id, err)
	}
	return projectFromFile(data)
}

// ---- system.project.create ----

type ProjectCreateTool struct{ deps *Deps }

func NewProjectCreateTool(d *Deps) *ProjectCreateTool { return &ProjectCreateTool{deps: d} }
func (t *ProjectCreateTool) Name() string             { return "system.project.create" }
func (t *ProjectCreateTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *ProjectCreateTool) Description() string {
	return "Create a new project to group related tasks. Call this when the user mentions starting a new project, initiative, or area of work. Returns the created project's id — pass this id to system.task.create when creating tasks for the project.\nParameters: name (required, project title), description (optional, free text), core_team (optional, list of agent IDs who default to this project — not an access gate), repository (optional, git URL)."
}

func (t *ProjectCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "Project title (required)"},
			"description": map[string]any{"type": "string", "description": "Optional free-text description"},
			"core_team": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional list of agent IDs who default to this project (not an access gate)",
			},
			"repository": map[string]any{"type": "string", "description": "Optional git repository URL"},
		},
		"required": []string{"name"},
	}
}

func (t *ProjectCreateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	if len(name) > 200 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name exceeds 200 characters", ""))
	}
	p := project{
		ID:        ulid.Make().String(),
		Name:      name,
		Status:    "active",
		Pinned:    false,
		PinOrder:  0,
		CreatedAt: nowISO(),
		UpdatedAt: nowISO(),
	}
	if v, ok := args["description"].(string); ok {
		if len(v) > 2000 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "description exceeds 2000 characters", ""))
		}
		p.Description = v
	}
	if raw, ok := args["core_team"].([]any); ok {
		team, err := sanitizeCoreTeam(raw)
		if err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Provide at most 20 agent IDs"))
		}
		p.CoreTeam = team
	}
	if v, ok := args["repository"].(string); ok {
		if len(v) > 500 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "repository exceeds 500 characters", ""))
		}
		p.Repository = v
	}
	if err := writeEntity(projectsDir(t.deps.Home), p.ID, p); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description,
		"status": p.Status, "pinned": p.Pinned, "pin_order": p.PinOrder,
		"core_team": p.CoreTeam, "repository": p.Repository,
		"task_count": 0, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}))
}

// ---- system.project.update ----

type ProjectUpdateTool struct{ deps *Deps }

func NewProjectUpdateTool(d *Deps) *ProjectUpdateTool { return &ProjectUpdateTool{deps: d} }
func (t *ProjectUpdateTool) Name() string             { return "system.project.update" }
func (t *ProjectUpdateTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *ProjectUpdateTool) Description() string {
	return "Update an existing project's name, description, status, pin state, core team, or repository. Call this when the user wants to rename, archive, pin, or reconfigure a project. Use system.project.list first to find the project id.\nParameters: id (required, from system.project.list), name, description, status (active/archived), pinned (bool), pin_order (int), core_team (list of agent IDs), repository (git URL). Only provided fields are updated."
}

func (t *ProjectUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Project ID from system.project.list"},
			"name":        map[string]any{"type": "string", "description": "New project title"},
			"description": map[string]any{"type": "string", "description": "New description"},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"active", "archived"},
				"description": "active or archived",
			},
			"pinned":    map[string]any{"type": "boolean", "description": "Pin to top of sidebar"},
			"pin_order": map[string]any{"type": "integer", "description": "Sort position among pinned projects"},
			"core_team": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "List of agent IDs who default to this project",
			},
			"repository": map[string]any{"type": "string", "description": "Git repository URL"},
		},
		"required": []string{"id"},
	}
}

func (t *ProjectUpdateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	p, err := readProjectFromDisk(t.deps.Home, id)
	if err != nil {
		return tools.ErrorResult(errorJSON("PROJECT_NOT_FOUND", fmt.Sprintf("No project %q", id),
			"Use system.project.list to see available projects"))
	}

	if v, ok := args["name"].(string); ok && v != "" {
		if len(v) > 200 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "name exceeds 200 characters", ""))
		}
		p.Name = v
	}
	if v, ok := args["description"].(string); ok {
		if len(v) > 2000 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "description exceeds 2000 characters", ""))
		}
		p.Description = v
	}
	if v, ok := args["status"].(string); ok {
		if v != "active" && v != "archived" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				fmt.Sprintf("invalid status %q: must be active or archived", v), ""))
		}
		p.Status = v
	}
	if v, ok := args["pinned"].(bool); ok {
		p.Pinned = v
	}
	if v, ok := args["pin_order"].(float64); ok {
		p.PinOrder = int(v)
	}
	if raw, ok := args["core_team"].([]any); ok {
		team, err := sanitizeCoreTeam(raw)
		if err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Provide at most 20 agent IDs"))
		}
		p.CoreTeam = team
	}
	if v, ok := args["repository"].(string); ok {
		if len(v) > 500 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "repository exceeds 500 characters", ""))
		}
		p.Repository = v
	}

	p.UpdatedAt = nowISO()
	if err := writeEntity(projectsDir(t.deps.Home), id, p); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	tc := computeTaskCount(t.deps.Home, id)
	return tools.NewToolResult(successJSON(map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description,
		"status": p.Status, "pinned": p.Pinned, "pin_order": p.PinOrder,
		"core_team": p.CoreTeam, "repository": p.Repository,
		"task_count": tc, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}))
}

// ---- system.project.delete ----

type ProjectDeleteTool struct{ deps *Deps }

func NewProjectDeleteTool(d *Deps) *ProjectDeleteTool { return &ProjectDeleteTool{deps: d} }
func (t *ProjectDeleteTool) Name() string             { return "system.project.delete" }
func (t *ProjectDeleteTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *ProjectDeleteTool) Description() string {
	return "Delete a project and all its tasks. This is irreversible — all GTD tasks belonging to the project are permanently deleted. Call system.project.list first to find the project id. Requires confirm:true.\nParameters: id (required), confirm (bool, must be true to prevent accidental deletion)."
}

func (t *ProjectDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Project ID from system.project.list"},
			"confirm": map[string]any{
				"type":        "boolean",
				"description": "Must be true to confirm irreversible deletion",
			},
		},
		"required": []string{"id", "confirm"},
	}
}

func (t *ProjectDeleteTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	confirm, _ := args["confirm"].(bool)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to delete a project", ""))
	}

	// Guard: verify the project exists before any irreversible mutations (FR-007).
	if _, err := readProjectFromDisk(t.deps.Home, id); err != nil {
		return tools.ErrorResult(errorJSON("PROJECT_NOT_FOUND", fmt.Sprintf("No project %q", id),
			"Use system.project.list to see available projects"))
	}

	// Step 1: cascade-delete tasks
	tasks, err := listEntities[task](tasksDir(t.deps.Home))
	if err != nil {
		slog.Warn("sysagent: project cascade delete: failed to list tasks", "project_id", id, "error", err)
		// Continue with partial deletion — log but don't abort the project delete
	}
	tasksDeleted := 0
	for _, tk := range tasks {
		if !gtdStatusSet[tk.Status] {
			continue
		}
		if tk.ProjectID == id {
			if err := deleteEntity(tasksDir(t.deps.Home), tk.ID); err != nil {
				slog.Warn("sysagent: project cascade delete: failed to delete task",
					"project_id", id, "task_id", tk.ID, "error", err)
			} else {
				tasksDeleted++
			}
		}
	}

	// Step 2: remove session links for this project
	RemoveLinksForProject(t.deps.Home, id)

	// Step 3: delete the project file (last)
	if err := deleteEntity(projectsDir(t.deps.Home), id); err != nil {
		return tools.ErrorResult(errorJSON("PROJECT_NOT_FOUND", err.Error(),
			"Use system.project.list to see available projects"))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": id, "deleted": true, "tasks_deleted": tasksDeleted,
	}))
}

// ---- system.project.list ----

type ProjectListTool struct{ deps *Deps }

func NewProjectListTool(d *Deps) *ProjectListTool { return &ProjectListTool{deps: d} }
func (t *ProjectListTool) Name() string           { return "system.project.list" }
func (t *ProjectListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ProjectListTool) Description() string {
	return "List all active projects with their task counts. Call this to find project ids before calling system.task.create, system.task.list, or system.project.update. Returns newest projects first.\nParameters: status (optional: active/archived/all, defaults to active)."
}

func (t *ProjectListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"active", "archived", "all"},
				"description": "Filter by status: active (default), archived, or all",
			},
		},
	}
}

func (t *ProjectListTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	statusFilter, _ := args["status"].(string)
	if statusFilter == "" {
		statusFilter = "active"
	}

	// Single-pass task count for all projects.
	counts := computeTaskCounts(t.deps.Home)

	// Read all projects from disk, applying the legacy migration via projectFromFile.
	dir := projectsDir(t.deps.Home)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return tools.ErrorResult(errorJSON("LIST_FAILED", err.Error(), ""))
	}

	var projects []project
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < 6 || e.Name()[len(e.Name())-5:] != ".json" {
			continue
		}
		pid := e.Name()[:len(e.Name())-5]
		data, err := os.ReadFile(entityPath(dir, pid))
		if err != nil {
			slog.Warn("sysagent: skipping unreadable project file", "file", e.Name(), "error", err)
			continue
		}
		p, err := projectFromFile(data)
		if err != nil {
			slog.Warn("sysagent: skipping corrupt project file", "file", e.Name(), "error", err)
			continue
		}
		// Apply status filter.
		if statusFilter != "all" && p.Status != statusFilter {
			continue
		}
		projects = append(projects, p)
	}

	// Sort: pinned first by pin_order ascending (oldest-first tiebreaker per FR-010),
	// then unpinned by created_at descending (newest first).
	sort.Slice(projects, func(i, j int) bool {
		pi, pj := projects[i], projects[j]
		if pi.Pinned != pj.Pinned {
			return pi.Pinned // pinned before unpinned
		}
		if pi.Pinned && pj.Pinned {
			if pi.PinOrder != pj.PinOrder {
				return pi.PinOrder < pj.PinOrder
			}
			return pi.CreatedAt < pj.CreatedAt // pinned same pin_order: oldest first (FR-010)
		}
		return pi.CreatedAt > pj.CreatedAt // unpinned: newest first
	})

	// Build response list attaching computed task counts.
	// task_count is computed at read time and never stored (per contract schema).
	type projectResponse struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Status      string   `json:"status"`
		Pinned      bool     `json:"pinned"`
		PinOrder    int      `json:"pin_order"`
		CoreTeam    []string `json:"core_team,omitempty"`
		Repository  string   `json:"repository,omitempty"`
		TaskCount   int      `json:"task_count"`
		CreatedAt   string   `json:"created_at"`
		UpdatedAt   string   `json:"updated_at"`
	}
	resp := make([]projectResponse, 0, len(projects))
	for _, p := range projects {
		resp = append(resp, projectResponse{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Status:      p.Status,
			Pinned:      p.Pinned,
			PinOrder:    p.PinOrder,
			CoreTeam:    p.CoreTeam,
			Repository:  p.Repository,
			TaskCount:   counts[p.ID],
			CreatedAt:   p.CreatedAt,
			UpdatedAt:   p.UpdatedAt,
		})
	}
	return tools.NewToolResult(successJSON(map[string]any{"projects": resp}))
}

// ---- system.project.get ----

type ProjectGetTool struct{ deps *Deps }

func NewProjectGetTool(d *Deps) *ProjectGetTool  { return &ProjectGetTool{deps: d} }
func (t *ProjectGetTool) Name() string           { return "system.project.get" }
func (t *ProjectGetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ProjectGetTool) Description() string {
	return "Get a single project by ID including its live task count. Use this to refresh project data after creating tasks.\nParameters: id (required, from system.project.list)."
}

func (t *ProjectGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Project ID from system.project.list"},
		},
		"required": []string{"id"},
	}
}

func (t *ProjectGetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	p, err := readProjectFromDisk(t.deps.Home, id)
	if err != nil {
		return tools.ErrorResult(errorJSON("PROJECT_NOT_FOUND", fmt.Sprintf("No project %q", id),
			"Use system.project.list to see available projects"))
	}
	tc := computeTaskCount(t.deps.Home, id)
	return tools.NewToolResult(successJSON(map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description,
		"status": p.Status, "pinned": p.Pinned, "pin_order": p.PinOrder,
		"core_team": p.CoreTeam, "repository": p.Repository,
		"task_count": tc, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}))
}
