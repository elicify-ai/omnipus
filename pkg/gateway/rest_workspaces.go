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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/task"
)

// errWorkspaceNotFound is returned by readWorkspaceFile when the workspace file
// does not exist on disk. Callers use errors.Is(err, errWorkspaceNotFound).
var errWorkspaceNotFound = errors.New("workspace not found")

// defaultWorkspaceSeedMu serializes concurrent calls to ensureDefaultWorkspace
// (e.g. from two racing gateway boots) so exactly one default workspace is created.
var defaultWorkspaceSeedMu sync.Mutex

// storedWorkspace mirrors the on-disk format of ~/.omnipus/workspaces/{id}.json.
type storedWorkspace struct { // not-wire-format: internal disk-cache struct, mapped to gen.Workspace before sending over the wire
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Pinned      bool     `json:"pinned"`
	PinOrder    int      `json:"pin_order"`
	CoreTeam    []string `json:"core_team,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	IsDefault   bool     `json:"is_default,omitempty"` // true only for the auto-created default workspace
	// Owner is the username of the user who created this workspace. Set at creation;
	// never updated. Attribution only — not an access gate (FR-1.9).
	Owner string `json:"owner,omitempty"`
	// Delegation is the per-workspace delegation graph (M5): the directed edges
	// that authorize who-delegates-to-whom on this workspace. This is the editable
	// source of truth surfaced in the workspace Team tab. nil/empty means no
	// delegation configured. The per-agent delegation_policy remains the
	// enforcement cap; this graph is what the UI edits.
	Delegation []storedDelegationEdge `json:"delegation,omitempty"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}

// storedDelegationEdge mirrors the on-disk format of a single delegation edge,
// matching gen.WorkspaceDelegationEdge. Modes use the canonical delegation-mode
// strings (await|background|task).
type storedDelegationEdge struct { // not-wire-format: internal disk-cache struct, mapped to gen.WorkspaceDelegationEdge before sending over the wire
	FromAgent string   `json:"from_agent"`
	ToAgent   string   `json:"to_agent"`
	Modes     []string `json:"modes,omitempty"`
	Depth     *int     `json:"depth,omitempty"`
}

// caller holds the identity of the authenticated request caller.
// Passed by value; zero value is unauthenticated (empty username).
// Only Username is used by callers — all post-gate role/multi-user checks
// were removed when FR-1.9 dropped the owner-gate (attribution only).
type caller struct {
	Username string
}

// callerIdentity extracts the caller's username from the request context.
// In dev-mode bypass (no UserContextKey set), Username is empty.
func (a *restAPI) callerIdentity(r *http.Request) caller {
	var c caller
	if u, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && u != nil {
		c.Username = u.Username
	}
	return c
}

// validateRepositoryURL returns an error when the repository field is non-empty but
// does not use an http:// or https:// scheme. Empty repository is always accepted
// (the field is optional). (SEC-5)
func validateRepositoryURL(repository string) error {
	if repository == "" {
		return nil
	}
	u, err := url.Parse(repository)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("repository must be an http:// or https:// URL")
	}
	return nil
}

// readWorkspaceFile reads and parses ~/.omnipus/workspaces/{id}.json.
// Greenfield: no legacy agent_ids→core_team migration (FR-1.10).
func readWorkspaceFile(home, id string) (storedWorkspace, error) {
	path := filepath.Join(home, "workspaces", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return storedWorkspace{}, fmt.Errorf("%w: %s", errWorkspaceNotFound, id)
		}
		return storedWorkspace{}, fmt.Errorf("read workspace %s: %w", id, err)
	}
	var w storedWorkspace
	if err := json.Unmarshal(data, &w); err != nil {
		return storedWorkspace{}, fmt.Errorf("parse workspace %s: %w", id, err)
	}
	// Legacy files without status field default to active.
	if w.Status == "" {
		w.Status = string(gen.WorkspaceStatusActive)
	}
	return w, nil
}

// listWorkspaceFiles reads all workspace JSON files from ~/.omnipus/workspaces/.
// Files that are malformed are skipped with a Warn log.
func listWorkspaceFiles(home string) ([]storedWorkspace, error) {
	dir := filepath.Join(home, "workspaces")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	var workspaces []storedWorkspace
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		w, err := readWorkspaceFile(home, id)
		if err != nil {
			slog.Warn("rest: skipping malformed workspace file", "file", e.Name(), "error", err)
			continue
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, nil
}

// scanTasks walks the unified tasks directory and calls fn for every file that
// deserialises to a valid task (status in the 7-state vocabulary).
// Returns the first I/O error; fn errors are not propagated.
func scanTasks(home string, fn func(id string, t task.Task)) error {
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
			slog.Warn("rest_workspaces: scanTasks: failed to read task file", "file", e.Name(), "error", err)
			continue
		}
		var t task.Task
		if err := json.Unmarshal(data, &t); err != nil {
			slog.Warn("rest_workspaces: scanTasks: failed to parse task file", "file", e.Name(), "error", err)
			continue
		}
		if !task.IsValidStatus(t.Status) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if t.ID == "" {
			t.ID = id
		}
		fn(id, t)
	}
	return nil
}

// computeWorkspaceTaskCounts returns a map[workspaceID]count by doing a single pass
// over all task files in ~/.omnipus/tasks/. Used by list (O(N) for all workspaces).
// Only GTD tasks (status ∈ {inbox,next,active,waiting,done,failed}) are counted.
func computeWorkspaceTaskCounts(home string) (map[string]int, error) {
	counts := make(map[string]int)
	if err := scanTasks(home, func(_ string, t task.Task) {
		if t.WorkspaceID != "" {
			counts[t.WorkspaceID]++
		}
	}); err != nil {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}
	return counts, nil
}

// countTasksForWorkspace counts GTD tasks belonging to a single workspace. O(N tasks)
// but avoids building the full map — used by single-workspace GET/PUT.
func countTasksForWorkspace(home, workspaceID string) int {
	count := 0
	if err := scanTasks(home, func(_ string, t task.Task) {
		if t.WorkspaceID == workspaceID {
			count++
		}
	}); err != nil {
		slog.Warn("rest_workspaces: countTasksForWorkspace: failed to scan tasks",
			"workspace_id", workspaceID, "error", err)
		return 0
	}
	return count
}

// writeWorkspaceFile atomically writes w to ~/.omnipus/workspaces/{id}.json.
func writeWorkspaceFile(home string, w storedWorkspace) error {
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir workspaces: %w", err)
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace: %w", err)
	}
	path := filepath.Join(dir, w.ID+".json")
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// workspaceToWire converts a storedWorkspace to the generated gen.Workspace wire type.
// taskCount is passed in (computed by the caller).
func workspaceToWire(w storedWorkspace, taskCount int) gen.Workspace {
	createdAt, err := time.Parse(time.RFC3339, w.CreatedAt)
	if err != nil {
		slog.Warn("rest: workspace: invalid created_at timestamp", "id", w.ID, "raw", w.CreatedAt)
		createdAt = time.Now().UTC()
	}
	updatedAt, err := time.Parse(time.RFC3339, w.UpdatedAt)
	if err != nil {
		slog.Warn("rest: workspace: invalid updated_at timestamp", "id", w.ID, "raw", w.UpdatedAt)
		updatedAt = time.Now().UTC()
	}

	wire := gen.Workspace{
		Id:        w.ID,
		Name:      w.Name,
		Status:    gen.WorkspaceStatus(w.Status),
		Pinned:    w.Pinned,
		PinOrder:  w.PinOrder,
		TaskCount: taskCount,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if w.IsDefault {
		t := true
		wire.IsDefault = &t
	}
	if w.Description != "" {
		wire.Description = &w.Description
	}
	if w.Repository != "" {
		wire.Repository = &w.Repository
	}
	if len(w.CoreTeam) > 0 {
		team := make([]string, len(w.CoreTeam))
		copy(team, w.CoreTeam)
		wire.CoreTeam = &team
	}
	if w.Owner != "" {
		o := w.Owner
		wire.Owner = &o
	}
	return wire
}

// deleteTasksForWorkspace removes all GTD task files whose workspace_id matches workspaceID.
// Per FR-007: individual task-file deletion failures are logged and skipped (best-effort).
func deleteTasksForWorkspace(home, workspaceID string) error {
	tasksDir := filepath.Join(home, "tasks")
	if err := scanTasks(home, func(id string, t task.Task) {
		if t.WorkspaceID == workspaceID {
			taskPath := filepath.Join(tasksDir, id+".json")
			if err := os.Remove(taskPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("rest: workspace cascade: failed to delete task",
					"file", id+".json", "error", err)
			}
		}
	}); err != nil {
		return fmt.Errorf("scan tasks for cascade delete: %w", err)
	}
	return nil
}

// loadWorkspace reads a workspace by ID and writes the appropriate HTTP error if absent.
// Returns (w, true) on success or (_, false) after writing the error response.
func (a *restAPI) loadWorkspace(w http.ResponseWriter, id string) (storedWorkspace, bool) {
	ws, err := readWorkspaceFile(a.homePath, id)
	if err != nil {
		if errors.Is(err, errWorkspaceNotFound) {
			jsonErr(w, http.StatusNotFound, "workspace not found")
			return storedWorkspace{}, false
		}
		slog.Error("rest: load workspace", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return storedWorkspace{}, false
	}
	return ws, true
}

// ensureDefaultWorkspace checks if the default workspace exists; if not, creates it.
// Seeds one workspace named "My Workspace" (FR-1.6, US-6) pre-populated with the
// default team (4 base agents + Planner/Explorer/Researcher specialists) and the
// default delegation edges derived from the seeded per-agent trust graph (M5/M6).
// Idempotent: if a workspace with is_default=true already exists, this is a no-op.
// Thread-safe: serialized by defaultWorkspaceSeedMu to prevent TOCTOU double-seed
// when two gateway boots race (e.g. rapid restart or dual-process test).
// On failure, logs an error but returns nil (non-fatal — gateway continues).
func ensureDefaultWorkspace(home, ownerUsername string, cfg *config.Config) error {
	defaultWorkspaceSeedMu.Lock()
	defer defaultWorkspaceSeedMu.Unlock()

	workspaces, err := listWorkspaceFiles(home)
	if err != nil {
		return fmt.Errorf("ensureDefaultWorkspace: list workspaces: %w", err)
	}
	for _, w := range workspaces {
		if w.IsDefault {
			return nil // already exists
		}
	}
	// No default workspace found — create "My Workspace" with the default team +
	// delegation edges so delegation works out of the box (M5/M6).
	now := time.Now().UTC().Format(time.RFC3339)
	ws := storedWorkspace{
		ID:        ulid.Make().String(),
		Name:      "My Workspace",
		Status:    string(gen.WorkspaceStatusActive),
		Pinned:    false,
		PinOrder:  0,
		IsDefault: true,
		Owner:     ownerUsername,
		CoreTeam:  defaultWorkspaceTeam(cfg),
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Seed delegation edges restricted to the default team so the graph's nodes
	// and edges stay consistent (no edge to an off-team agent like the generic
	// worker). The Planner→Explorer/Researcher specialist edges survive because
	// all three are on the default team.
	ws.Delegation = seedEdgesForTeam(defaultWorkspaceDelegationEdges(cfg), ws.CoreTeam)
	if err := writeWorkspaceFile(home, ws); err != nil {
		return fmt.Errorf("ensureDefaultWorkspace: write: %w", err)
	}
	slog.Info("rest: default workspace auto-created",
		"id", ws.ID, "owner", ownerUsername,
		"team_size", len(ws.CoreTeam), "edge_count", len(ws.Delegation))
	return nil
}

// HandleWorkspaces dispatches all /api/v1/workspaces* requests.
func (a *restAPI) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/workspaces")

	// /api/v1/workspaces/{id}/milestones[/{milestoneId}] — delegate to HandleMilestones.
	if strings.Contains(rest, "/milestones") {
		a.HandleMilestones(w, r)
		return
	}

	// /api/v1/workspaces/{id}/delegation — the per-workspace delegation graph (M5).
	if strings.HasSuffix(rest, "/delegation") {
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/delegation")
		switch r.Method {
		case http.MethodGet:
			a.handleWorkspaceDelegationGet(w, r, id)
		case http.MethodPut:
			a.handleWorkspaceDelegationPut(w, r, id)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// /api/v1/workspaces/{id}/instructions — per-workspace Project Instructions (AGENT.md).
	if strings.HasSuffix(rest, "/instructions") {
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/instructions")
		a.HandleWorkspaceInstructions(w, r, id)
		return
	}

	// /api/v1/workspaces/{id}
	if len(rest) > 1 {
		id := strings.TrimPrefix(rest, "/")
		// Unknown sub-paths like /workspaces/{id}/anything return 404.
		if strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.handleWorkspaceGet(w, r, id)
		case http.MethodPut:
			a.handleWorkspacePut(w, r, id)
		case http.MethodDelete:
			a.handleWorkspaceDelete(w, r, id)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// /api/v1/workspaces
	switch r.Method {
	case http.MethodGet:
		a.handleWorkspaceList(w, r)
	case http.MethodPost:
		a.handleWorkspacePost(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) handleWorkspaceList(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = string(gen.WorkspaceStatusActive)
	}
	switch statusFilter {
	case string(gen.WorkspaceStatusActive), string(gen.WorkspaceStatusArchived), "all":
		// valid
	default:
		jsonErr(w, http.StatusBadRequest, "invalid status filter")
		return
	}

	workspaces, err := listWorkspaceFiles(a.homePath)
	if err != nil {
		slog.Error("rest: list workspaces", "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	taskCounts, err := computeWorkspaceTaskCounts(a.homePath)
	if err != nil {
		slog.Warn("rest: list workspaces: could not compute task counts", "error", err)
		taskCounts = make(map[string]int)
	}

	var result []gen.Workspace
	for _, ws := range workspaces {
		if statusFilter != "all" && ws.Status != statusFilter {
			continue
		}
		// Owner is attribution only (FR-1.9) — no access gate applied here.
		result = append(result, workspaceToWire(ws, taskCounts[ws.ID]))
	}
	if result == nil {
		result = []gen.Workspace{}
	}

	// Sort: default workspace always first, then pinned items (ascending pin_order),
	// then unpinned newest-first.
	isDefault := func(ws gen.Workspace) bool { return ws.IsDefault != nil && *ws.IsDefault }
	sort.Slice(result, func(i, j int) bool {
		if isDefault(result[i]) != isDefault(result[j]) {
			return isDefault(result[i])
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

func (a *restAPI) handleWorkspacePost(w http.ResponseWriter, r *http.Request) {
	var req gen.WorkspaceCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "WorkspaceCreateRequest", &req, validateEnabled) {
		return
	}

	// Trim before the length check so a whitespace-only name ("   ") is rejected
	// as empty rather than silently accepted (UAT fix). Persist the trimmed value.
	req.Name = strings.TrimSpace(req.Name)
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
	// SEC-5: reject non-http/https repository URLs.
	if req.Repository != nil {
		if err := validateRepositoryURL(*req.Repository); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.CoreTeam != nil && len(*req.CoreTeam) > 20 {
		jsonErr(w, http.StatusBadRequest, "core_team may have at most 20 entries")
		return
	}

	// Stamp the creating user's username as owner (attribution only, not a gate).
	c := a.callerIdentity(r)

	now := time.Now().UTC().Format(time.RFC3339)
	ws := storedWorkspace{
		ID:        ulid.Make().String(),
		Name:      req.Name,
		Status:    string(gen.WorkspaceStatusActive),
		Pinned:    false,
		PinOrder:  0,
		Owner:     c.Username,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.Description != nil {
		ws.Description = *req.Description
	}
	if req.Repository != nil {
		ws.Repository = *req.Repository
	}
	cfg := a.agentLoop.GetConfig()
	if req.CoreTeam != nil {
		ws.CoreTeam = deduplicateStrings(*req.CoreTeam)
	} else {
		// No explicit team: seed the default roster (4 base + specialists) so a
		// fresh workspace works out of the box (M6), mirroring My Workspace.
		ws.CoreTeam = defaultWorkspaceTeam(cfg)
	}
	// Seed default delegation edges from each team agent's seeded role (M5),
	// restricted to edges whose endpoints are both on this workspace's team so a
	// custom core_team never gains edges to agents it did not include.
	ws.Delegation = seedEdgesForTeam(defaultWorkspaceDelegationEdges(cfg), ws.CoreTeam)

	if err := writeWorkspaceFile(a.homePath, ws); err != nil {
		slog.Error("rest: create workspace", "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	wire := workspaceToWire(ws, 0)
	if a.auditor != nil {
		_ = a.auditor.Log(
			&audit.Entry{
				Event:    "workspace.create",
				Decision: audit.DecisionAllow,
				Details:  map[string]any{"id": ws.ID, "name": ws.Name},
			},
		)
	}
	jsonCreated(w, wire)
}

func (a *restAPI) handleWorkspaceGet(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		return
	}
	// FR-1.9: owner is attribution only — no access gate.
	jsonOK(w, workspaceToWire(ws, countTasksForWorkspace(a.homePath, id)))
}

func (a *restAPI) handleWorkspacePut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	var req gen.WorkspaceUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "WorkspaceUpdateRequest", &req, validateEnabled) {
		return
	}

	// Validate fields before touching disk.
	if req.Name != nil {
		// Trim before the empty check so a whitespace-only name is rejected
		// rather than silently accepted (UAT fix). Persist the trimmed value.
		trimmedName := strings.TrimSpace(*req.Name)
		if trimmedName == "" {
			jsonErr(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		if len(trimmedName) > 200 {
			jsonErr(w, http.StatusBadRequest, "name exceeds 200 characters")
			return
		}
		req.Name = &trimmedName
	}
	if req.Description != nil && len(*req.Description) > 2000 {
		jsonErr(w, http.StatusBadRequest, "description exceeds 2000 characters")
		return
	}
	if req.Repository != nil && len(*req.Repository) > 500 {
		jsonErr(w, http.StatusBadRequest, "repository exceeds 500 characters")
		return
	}
	// SEC-5: reject non-http/https repository URLs.
	if req.Repository != nil {
		if err := validateRepositoryURL(*req.Repository); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.CoreTeam != nil && len(*req.CoreTeam) > 20 {
		jsonErr(w, http.StatusBadRequest, "core_team may have at most 20 entries")
		return
	}
	if req.Status != nil && !req.Status.Valid() {
		jsonErr(w, http.StatusBadRequest, `status must be "active" or "archived"`)
		return
	}

	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		return
	}

	// FR-1.9: no access gate — owner is attribution only.

	// Apply partial update (merge semantics) — track whether anything changed.
	changed := false
	if req.Name != nil && *req.Name != ws.Name {
		ws.Name = *req.Name
		changed = true
	}
	if req.Description != nil && *req.Description != ws.Description {
		ws.Description = *req.Description
		changed = true
	}
	if req.Repository != nil && *req.Repository != ws.Repository {
		ws.Repository = *req.Repository
		changed = true
	}
	if req.CoreTeam != nil {
		deduped := deduplicateStrings(*req.CoreTeam)
		if !slices.Equal(deduped, ws.CoreTeam) {
			ws.CoreTeam = deduped
			changed = true
		}
	}
	if req.Status != nil && string(*req.Status) != ws.Status {
		ws.Status = string(*req.Status)
		changed = true
	}
	if req.Pinned != nil && *req.Pinned != ws.Pinned {
		ws.Pinned = *req.Pinned
		changed = true
	}
	if req.PinOrder != nil && *req.PinOrder != ws.PinOrder {
		ws.PinOrder = *req.PinOrder
		changed = true
	}

	// No-op: nothing changed — return current state without writing.
	if !changed {
		jsonOK(w, workspaceToWire(ws, countTasksForWorkspace(a.homePath, id)))
		return
	}

	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := writeWorkspaceFile(a.homePath, ws); err != nil {
		slog.Error("rest: update workspace: write", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(
			&audit.Entry{
				Event:    "workspace.update",
				Decision: audit.DecisionAllow,
				Details:  map[string]any{"id": id},
			},
		)
	}
	jsonOK(w, workspaceToWire(ws, countTasksForWorkspace(a.homePath, id)))
}

func (a *restAPI) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	// Verify the workspace exists before cascading.
	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		return
	}

	// FR-1.9: no access gate — owner is attribution only.

	// Default workspace cannot be deleted (FR-1.6 delete-protection retained).
	if ws.IsDefault {
		jsonErr(w, http.StatusConflict, "cannot delete the default workspace")
		return
	}

	// Cascade: (1) milestones for workspace → (2) clear milestone_id on those tasks →
	// (3) delete tasks → (4) session links → (5) workspace file.
	deleteMilestonesForWorkspace(a.homePath, id)

	if err := deleteTasksForWorkspace(a.homePath, id); err != nil {
		slog.Error("rest: delete workspace: cascade tasks", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to scan tasks for cascade delete")
		return
	}

	path := filepath.Join(a.homePath, "workspaces", id+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("rest: delete workspace: remove file", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(
			&audit.Entry{
				Event:    "workspace.delete",
				Decision: audit.DecisionAllow,
				Details:  map[string]any{"id": id},
			},
		)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteMilestonesForWorkspace removes all milestone files for the given workspace and
// clears milestone_id on tasks that referenced them (cascade delete).
// Best-effort: individual file errors are logged and skipped.
func deleteMilestonesForWorkspace(home, workspaceID string) {
	all, err := listMilestoneFiles(home)
	if err != nil {
		slog.Warn("rest: workspace cascade: could not list milestones for workspace",
			"workspace_id", workspaceID, "error", err)
		return
	}
	for _, m := range all {
		if m.WorkspaceID != workspaceID {
			continue
		}
		// Clear milestone_id on tasks referencing this milestone before deleting.
		clearMilestoneOnTasks(home, m.ID)
		path := filepath.Join(home, "milestones", m.ID+".json")
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("rest: workspace cascade: failed to delete milestone",
				"milestone_id", m.ID, "workspace_id", workspaceID, "error", err)
		}
	}
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
