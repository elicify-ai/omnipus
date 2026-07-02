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
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/task"
	"github.com/dapicom-ai/omnipus/pkg/workspace"
)

// errWorkspaceNotFound is returned by readWorkspaceFile when the workspace file
// does not exist on disk. Callers use errors.Is(err, errWorkspaceNotFound).
var errWorkspaceNotFound = errors.New("workspace not found")

// defaultWorkspaceSeedMu serializes concurrent calls to ensureDefaultWorkspace
// (e.g. from two racing gateway boots) so exactly one default workspace is created.
var defaultWorkspaceSeedMu sync.Mutex

// storedWorkspace is an alias for the canonical on-disk workspace type.
// The shared type lives in pkg/workspace so that the tool write path
// (pkg/sysagent/tools) uses the same struct and can never silently drop
// fields — including the delegation graph — written by the gateway.
// not-wire-format: mapped to gen.Workspace before sending over the wire.
type storedWorkspace = workspace.Workspace

// storedDelegationEdge is an alias for the canonical delegation-edge type.
// The shared type lives in pkg/workspace so gateway and tool writes stay
// byte-for-byte compatible on the delegation field.
// not-wire-format: mapped to gen.WorkspaceDelegationEdge before sending over the wire.
type storedDelegationEdge = workspace.DelegationEdge

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
	// FR-001/ADR-027: include member_configs when present so the SPA can render
	// heartbeat settings without a separate fetch. Uses the generated named wire
	// types (gen.WorkspaceMemberConfig / gen.WorkspaceMemberHeartbeat) — see the
	// member_configs rewrite in scripts/gen-go-fixup.go (Constraint #8).
	if len(w.MemberConfigs) > 0 {
		wireMC := make(map[string]gen.WorkspaceMemberConfig, len(w.MemberConfigs))
		for agentID, mc := range w.MemberConfigs {
			entry := gen.WorkspaceMemberConfig{}
			if hb := mc.Heartbeat; hb != nil {
				enabled := hb.Enabled
				hbWire := &gen.WorkspaceMemberHeartbeat{Enabled: &enabled}
				if hb.Body != "" {
					hbWire.Body = &hb.Body
				}
				if hb.IntervalMinutes > 0 {
					iv := hb.IntervalMinutes
					hbWire.IntervalMinutes = &iv
				}
				if hb.SessionID != "" {
					hbWire.SessionId = &hb.SessionID
				}
				entry.Heartbeat = hbWire
			}
			wireMC[agentID] = entry
		}
		wire.MemberConfigs = &wireMC
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
	seedEdges := seedEdgesForTeam(defaultWorkspaceDelegationEdges(cfg), ws.CoreTeam)
	// Defense-in-depth: validate each seeded edge so no unvalidated edge is ever
	// persisted. The source is the trusted compiled-in roster so failures are
	// unexpected; on failure log WARN and drop the offending edge rather than
	// hard-failing boot over a seed-config issue.
	team := workspaceTeamSet(ws)
	ceiling := delegationDepthCeiling(cfg)
	validEdges := seedEdges[:0:0]
	for _, edge := range seedEdges {
		if verr := edge.Validate(team, ceiling); verr != nil {
			slog.Warn("rest: ensureDefaultWorkspace: dropping invalid seed delegation edge",
				"from", edge.FromAgent, "to", edge.ToAgent, "error", verr)
			continue
		}
		validEdges = append(validEdges, edge)
	}
	ws.Delegation = validEdges
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
	seedEdges := seedEdgesForTeam(defaultWorkspaceDelegationEdges(cfg), ws.CoreTeam)
	// Defense-in-depth: validate each seeded edge so no unvalidated edge is ever
	// persisted. The source is the trusted compiled-in roster so failures are
	// unexpected; on failure log WARN and drop the offending edge (do not hard-fail
	// the create request over a seed-config issue).
	createTeam := workspaceTeamSet(ws)
	createCeiling := delegationDepthCeiling(cfg)
	validSeedEdges := seedEdges[:0:0]
	for _, edge := range seedEdges {
		if verr := edge.Validate(createTeam, createCeiling); verr != nil {
			slog.Warn("rest: handleWorkspaceCreate: dropping invalid seed delegation edge",
				"from", edge.FromAgent, "to", edge.ToAgent, "error", verr)
			continue
		}
		validSeedEdges = append(validSeedEdges, edge)
	}
	ws.Delegation = validSeedEdges

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

// workspaceMemberConfigsFromWire translates the generated member_configs map
// (gen.WorkspaceMemberConfig, pointer fields) into the internal
// workspace.MemberConfig map (value fields). It returns mcPresent=false when the
// wire field is absent (nil) so callers preserve merge semantics (absent →
// unchanged). The server-managed session_id (FR-010, readOnly in the contract)
// is intentionally NOT read from client input.
func workspaceMemberConfigsFromWire(wire *map[string]gen.WorkspaceMemberConfig) (map[string]workspace.MemberConfig, bool) {
	if wire == nil {
		return nil, false
	}
	out := make(map[string]workspace.MemberConfig, len(*wire))
	for agentID, wmc := range *wire {
		var mc workspace.MemberConfig
		if hb := wmc.Heartbeat; hb != nil {
			mh := &workspace.MemberHeartbeat{}
			if hb.Enabled != nil {
				mh.Enabled = *hb.Enabled
			}
			if hb.IntervalMinutes != nil {
				mh.IntervalMinutes = *hb.IntervalMinutes
			}
			if hb.Body != nil {
				mh.Body = *hb.Body
			}
			mc.Heartbeat = mh
		}
		out[agentID] = mc
	}
	return out, true
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

	// member_configs uses merge semantics: when present (non-nil) it replaces the
	// config for each listed agent and GCs stale entries; when absent it is left
	// unchanged. session_id is server-managed (FR-010) and ignored on input.
	incomingMC, mcPresent := workspaceMemberConfigsFromWire(req.MemberConfigs)

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

	// HIGH-2: sessionsCreated tracks heartbeat sessions minted this request so
	// they can be rolled back on any error path before the workspace is persisted.
	// Declared at function scope so the writeWorkspaceFile error branch can also
	// roll back (not just the eager-session loop).
	type sessionCreated struct {
		agentID   string
		sessionID string
	}
	var sessionsCreated []sessionCreated
	rollbackCreatedSessions := func() {
		for _, sc := range sessionsCreated {
			if st := a.agentLoop.GetAgentStore(sc.agentID); st != nil {
				if delErr := st.DeleteSession(sc.sessionID); delErr != nil {
					slog.Warn("rest: workspace PUT: rollback session delete failed",
						"agent_id", sc.agentID, "session_id", sc.sessionID, "error", delErr)
				}
			}
		}
	}

	// Determine effective CoreTeam for member_configs validation: the request
	// value when present (not yet applied), else the current stored value.
	effectiveCoreTeam := ws.CoreTeam
	if req.CoreTeam != nil {
		effectiveCoreTeam = deduplicateStrings(*req.CoreTeam)
	}

	// FR-010/022: validate and eagerly-session incoming member_configs before
	// touching the workspace on disk.
	if mcPresent && len(incomingMC) > 0 {
		cfg := a.agentLoop.GetConfig()
		if vErr := workspace.ValidateMemberConfigs(effectiveCoreTeam, incomingMC, configOnlyIsWorker(cfg)); vErr != nil {
			jsonErr(w, http.StatusUnprocessableEntity, vErr.Error())
			return
		}

		// FR-010: for each newly-enabled heartbeat with no SessionID, create an
		// eager standing session so the cron job can continue it across runs.
		// Disable path: if an incoming entry transitions enabled→disabled and the
		// stored entry has a session_id, release that standing session now so it
		// does not remain as an orphan (FIX-3).
		for agentID, mc := range incomingMC {
			hb := mc.Heartbeat
			// Disable path: release the stored standing session when heartbeat
			// transitions to disabled/absent and the stored entry had a session_id.
			if hb == nil || !hb.Enabled {
				if stored, exists := ws.MemberConfigs[agentID]; exists &&
					stored.Heartbeat != nil && stored.Heartbeat.SessionID != "" {
					if st := a.agentLoop.GetAgentStore(agentID); st != nil {
						if delErr := st.DeleteSession(stored.Heartbeat.SessionID); delErr != nil {
							slog.Warn("rest: workspace PUT: disable-path session release failed",
								"workspace_id", id, "agent_id", agentID,
								"session_id", stored.Heartbeat.SessionID, "error", delErr)
						} else {
							slog.Info("rest: workspace PUT: released heartbeat session on disable",
								"workspace_id", id, "agent_id", agentID,
								"session_id", stored.Heartbeat.SessionID)
						}
					}
					// Clear the stored session_id from the incoming config so the
					// persisted entry carries no stale session reference.
					if hb == nil {
						mc.Heartbeat = &workspace.MemberHeartbeat{}
					} else {
						mc.Heartbeat = &workspace.MemberHeartbeat{
							Enabled:         false,
							IntervalMinutes: hb.IntervalMinutes,
							Body:            hb.Body,
						}
					}
					incomingMC[agentID] = mc
				}
				continue
			}
			// Enable path: hb != nil && hb.Enabled.
			if hb.SessionID != "" {
				continue
			}
			// Check whether the existing config already has a session_id for
			// this (workspace, agent) pair — if so, reuse it (idempotent enable).
			if existing, exists := ws.MemberConfigs[agentID]; exists &&
				existing.Heartbeat != nil && existing.Heartbeat.SessionID != "" {
				hb.SessionID = existing.Heartbeat.SessionID
				mc.Heartbeat = hb
				incomingMC[agentID] = mc
				continue
			}
			sessStore := a.agentLoop.GetAgentStore(agentID)
			if sessStore == nil {
				// HIGH-3: nil store is an internal inconsistency (agent passed
				// CoreTeam validation, so it must be registered). Persisting
				// enabled=true with an empty session_id is invalid state — roll
				// back any sessions created so far and return 500.
				slog.Error("rest: workspace PUT: agent store unavailable for heartbeat session",
					"workspace_id", id, "agent_id", agentID)
				rollbackCreatedSessions()
				jsonErr(w, http.StatusInternalServerError, "agent store unavailable for heartbeat session")
				return
			}
			meta, sessErr := sessStore.NewHeartbeatSession(id, agentID)
			if sessErr != nil {
				slog.Error("rest: workspace PUT: failed to create heartbeat session",
					"workspace_id", id, "agent_id", agentID, "error", sessErr)
				// HIGH-2: roll back sessions created earlier in this loop.
				rollbackCreatedSessions()
				jsonErr(w, http.StatusInternalServerError, "failed to create heartbeat session")
				return
			}
			sessionsCreated = append(sessionsCreated, sessionCreated{agentID: agentID, sessionID: meta.ID})
			hb.SessionID = meta.ID
			mc.Heartbeat = hb
			incomingMC[agentID] = mc
		}
	}

	// FR-1.9: no access gate — owner is attribution only.

	// Default workspace cannot be archived (mirrors the delete-protection guard below).
	if ws.IsDefault && req.Status != nil && *req.Status == gen.WorkspaceUpdateRequestStatusArchived {
		jsonErr(w, http.StatusConflict, "cannot archive the default workspace")
		return
	}

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

	// FR-022: merge incoming member_configs (when present) and GC stale entries
	// (agents removed from CoreTeam) so the stored map stays consistent.
	coreTeamChanged := req.CoreTeam != nil
	if mcPresent {
		if ws.MemberConfigs == nil {
			ws.MemberConfigs = make(map[string]workspace.MemberConfig)
		}
		for agentID, mc := range incomingMC {
			ws.MemberConfigs[agentID] = mc
		}
		// GC: drop entries for agents no longer on the effective team.
		pruned, removed := workspace.GCMemberConfigs(ws.CoreTeam, ws.MemberConfigs)
		if len(removed) > 0 {
			slog.Info("rest: workspace PUT: GC member_configs", "workspace_id", id, "removed", removed)
			// FIX-4a: release standing sessions for GC-pruned members (members
			// whose agent is no longer in the CoreTeam).
			for _, removedID := range removed {
				if oldMC, had := ws.MemberConfigs[removedID]; had &&
					oldMC.Heartbeat != nil && oldMC.Heartbeat.SessionID != "" {
					if st := a.agentLoop.GetAgentStore(removedID); st != nil {
						if delErr := st.DeleteSession(oldMC.Heartbeat.SessionID); delErr != nil {
							slog.Warn("rest: workspace PUT: GC session release failed",
								"workspace_id", id, "agent_id", removedID,
								"session_id", oldMC.Heartbeat.SessionID, "error", delErr)
						} else {
							slog.Info("rest: workspace PUT: GC released heartbeat session",
								"workspace_id", id, "agent_id", removedID,
								"session_id", oldMC.Heartbeat.SessionID)
						}
					}
				}
			}
		}
		ws.MemberConfigs = pruned
		changed = true
	} else if coreTeamChanged {
		// FIX-4a: core_team changed without member_configs — GC stale member_config
		// entries whose agent is no longer on the new team, and release their sessions.
		if ws.MemberConfigs != nil {
			pruned, removed := workspace.GCMemberConfigs(ws.CoreTeam, ws.MemberConfigs)
			if len(removed) > 0 {
				slog.Info("rest: workspace PUT: core_team shrink GC member_configs",
					"workspace_id", id, "removed", removed)
				for _, removedID := range removed {
					if oldMC, had := ws.MemberConfigs[removedID]; had &&
						oldMC.Heartbeat != nil && oldMC.Heartbeat.SessionID != "" {
						if st := a.agentLoop.GetAgentStore(removedID); st != nil {
							if delErr := st.DeleteSession(oldMC.Heartbeat.SessionID); delErr != nil {
								slog.Warn("rest: workspace PUT: core_team shrink session release failed",
									"workspace_id", id, "agent_id", removedID,
									"session_id", oldMC.Heartbeat.SessionID, "error", delErr)
							} else {
								slog.Info("rest: workspace PUT: core_team shrink released heartbeat session",
									"workspace_id", id, "agent_id", removedID,
									"session_id", oldMC.Heartbeat.SessionID)
							}
						}
					}
				}
				ws.MemberConfigs = pruned
			}
		}
	}

	// No-op: nothing changed — return current state without writing.
	if !changed {
		jsonOK(w, workspaceToWire(ws, countTasksForWorkspace(a.homePath, id)))
		return
	}

	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := writeWorkspaceFile(a.homePath, ws); err != nil {
		slog.Error("rest: update workspace: write", "id", id, "error", err)
		// HIGH-2: roll back any heartbeat sessions created this request since the
		// workspace file was not persisted (they would be permanently orphaned).
		rollbackCreatedSessions()
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// FR-007: after persisting, reconcile cron schedules to reflect the new
	// member_configs. Best-effort: a failure is logged but does not prevent
	// the 200 response (the data is already safely on disk).
	if mcPresent || coreTeamChanged {
		a.reconcileHeartbeatSchedules()
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

	// Cascade: (1) heartbeat cron jobs → (2) heartbeat sessions → (3) milestones →
	// (4) tasks → (5) workspace file → (6) workspace directory.
	// FR-023/US-9: release all heartbeat cron jobs owned by this workspace before
	// removing the workspace file. Best-effort (logged on failure).
	if cs := a.cronService.Load(); cs != nil {
		releaseHeartbeatJobsForWorkspace(cs, id)
	}

	// HIGH-1 (FR-023): release standing heartbeat sessions for each member that
	// had a heartbeat enabled. The sessions live in per-agent session stores (NOT
	// under the workspace directory), so RemoveAll of the workspace dir does not
	// remove them. This must run before the workspace file is deleted (we need
	// member_configs to find which sessions to release). Best-effort per-session.
	releaseHeartbeatSessionsForWorkspace(a.agentLoop, ws)

	deleteMilestonesForWorkspace(a.homePath, id)

	if err := deleteTasksForWorkspace(a.homePath, id); err != nil {
		slog.Error("rest: delete workspace: cascade tasks", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to scan tasks for cascade delete")
		return
	}

	// ADR-029 FR-025/MAJ-005: disable and unbind all channel instances bound to
	// this workspace BEFORE removing the workspace file. If this config write fails
	// the delete aborts with 500, leaving the workspace + bindings fully consistent
	// (no orphan). Ordering guarantee: config unbind → workspace file delete.
	if err := unbindChannelInstancesForWorkspace(a, id); err != nil {
		slog.Error("rest: delete workspace: cascade channel unbind", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to unbind channel instances for workspace")
		return
	}

	path := filepath.Join(a.homePath, "workspaces", id+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("rest: delete workspace: remove file", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Best-effort: remove the per-workspace directory (AGENT.md / memory room).
	// The JSON removal above is the authoritative delete; a stale directory is not fatal.
	wsDir := workspace.WorkspaceDir(a.homePath, id)
	if err := os.RemoveAll(wsDir); err != nil {
		slog.Warn("rest: delete workspace: cascade dir", "id", id, "dir", wsDir, "error", err)
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

// releaseHeartbeatSessionsForWorkspace deletes the standing heartbeat session for
// every member of ws that has a heartbeat with a non-empty session_id. These
// sessions live in per-agent session stores (not under the workspace directory),
// so the workspace-directory RemoveAll does NOT remove them. Best-effort per
// session: a failure is logged and skipped so one bad session does not block the
// rest of the cascade.
//
// Called from handleWorkspaceDelete before the workspace file is removed (we need
// member_configs to find which sessions to release).
func releaseHeartbeatSessionsForWorkspace(al agentLoopAccessor, ws storedWorkspace) {
	for agentID, mc := range ws.MemberConfigs {
		if mc.Heartbeat == nil || mc.Heartbeat.SessionID == "" {
			continue
		}
		sessStore := al.GetAgentStore(agentID)
		if sessStore == nil {
			slog.Warn("heartbeat cascade: agent store not found; heartbeat session orphaned",
				"workspace_id", ws.ID, "agent_id", agentID,
				"session_id", mc.Heartbeat.SessionID)
			continue
		}
		if err := sessStore.DeleteSession(mc.Heartbeat.SessionID); err != nil {
			slog.Warn("heartbeat cascade: failed to delete heartbeat session",
				"workspace_id", ws.ID, "agent_id", agentID,
				"session_id", mc.Heartbeat.SessionID, "error", err)
		}
	}
}

// agentLoopAccessor is the minimal interface required by
// releaseHeartbeatSessionsForWorkspace.  *agent.AgentLoop satisfies it.
type agentLoopAccessor interface {
	GetAgentStore(agentID string) *session.UnifiedStore
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

// unbindChannelInstancesForWorkspace disables and unbinds every channel instance
// bound to workspaceID. Called from handleWorkspaceDelete BEFORE the workspace
// file is removed (ADR-029 FR-025/MAJ-005) so that a config-write failure aborts
// the delete cleanly with no orphan. "Unbind" clears BOTH WorkspaceID AND Identity
// (leaving Identity would make the next inbound drift on a now-missing workspace)
// and sets Enabled=false. If no instances are bound this is a no-op returning nil.
func unbindChannelInstancesForWorkspace(a *restAPI, workspaceID string) error {
	cfg := a.agentLoop.GetConfig()
	// Identify bound instances.
	var boundKeys []string
	for key, inst := range cfg.Channels {
		if inst.WorkspaceID == workspaceID {
			boundKeys = append(boundKeys, key)
		}
	}
	if len(boundKeys) == 0 {
		return nil // nothing to do
	}
	sort.Strings(boundKeys) // deterministic order for logging

	return a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			// No channels section in raw JSON — nothing to unbind.
			return nil
		}
		for _, key := range boundKeys {
			ch, _ := channels[key].(map[string]any)
			if ch == nil {
				ch = map[string]any{}
			}
			// Clear workspace binding and identity; disable the instance.
			delete(ch, "workspace_id")
			delete(ch, "identity")
			ch["enabled"] = false
			channels[key] = ch
		}
		m["channels"] = channels

		slog.Info("rest: workspace delete: disabled and unbound channel instances",
			"workspace_id", workspaceID, "instance_ids", boundKeys)
		return nil
	})
}
