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
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// workspace is the canonical on-disk workspace type shared with pkg/gateway.
// Using the shared type (instead of a local struct) ensures that both write
// paths — the REST gateway and the update_workspace tool — serialize exactly
// the same fields in exactly the same JSON tags, so neither can silently drop
// fields (in particular the delegation graph) written by the other.
type workspace = workspacepkg.Workspace

func workspacesDir(home string) string { return filepath.Join(home, "workspaces") }

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

// workspaceFromFile reads a workspace JSON into the workspace struct.
// Greenfield: no legacy agent_ids→core_team migration (FR-1.10).
func workspaceFromFile(data []byte) (workspace, error) {
	var w workspace
	if err := json.Unmarshal(data, &w); err != nil {
		return w, err
	}
	// Legacy files without status field default to active.
	if w.Status == "" {
		w.Status = "active"
	}
	return w, nil
}

// computeWorkspaceTaskCount counts GTD tasks on disk with matching workspace_id.
// Only GTD tasks (status ∈ {inbox,next,active,waiting,done,failed}) are counted;
// workflow tasks (queued/assigned/running/completed) are excluded.
func computeWorkspaceTaskCount(home, workspaceID string) int {
	tasks, err := listEntities[unifiedTask](tasksDir(home))
	if err != nil {
		return 0
	}
	n := 0
	for i := range tasks {
		if !isValidTaskStatus(string(tasks[i].Status)) {
			continue
		}
		if tasks[i].WorkspaceID == workspaceID {
			n++
		}
	}
	return n
}

// computeWorkspaceTaskCounts returns a map[workspaceID]count from a single listEntities call.
// Only GTD tasks are counted; workflow tasks are excluded.
func computeWorkspaceTaskCounts(home string) map[string]int {
	counts := make(map[string]int)
	tasks, err := listEntities[unifiedTask](tasksDir(home))
	if err != nil {
		return counts
	}
	for i := range tasks {
		if !isValidTaskStatus(string(tasks[i].Status)) {
			continue
		}
		if tasks[i].WorkspaceID != "" {
			counts[tasks[i].WorkspaceID]++
		}
	}
	return counts
}

// readWorkspaceFromDisk reads the raw bytes for a workspace entity and calls workspaceFromFile
// to handle legacy migration. Returns an error if the file is missing or unreadable.
func readWorkspaceFromDisk(home, id string) (workspace, error) {
	if err := validateID(id); err != nil {
		return workspace{}, fmt.Errorf("read workspace: %w", err)
	}
	path := entityPath(workspacesDir(home), id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return workspace{}, fmt.Errorf("NOT_FOUND: %s", id)
		}
		return workspace{}, fmt.Errorf("read workspace %s: %w", id, err)
	}
	return workspaceFromFile(data)
}

// ---- system.workspace.create ----

type WorkspaceCreateTool struct{ deps *Deps }

func NewWorkspaceCreateTool(d *Deps) *WorkspaceCreateTool { return &WorkspaceCreateTool{deps: d} }
func (t *WorkspaceCreateTool) Name() string               { return "create_workspace" }
func (t *WorkspaceCreateTool) Scope() tools.ToolScope     { return tools.ScopeCore }
func (t *WorkspaceCreateTool) Description() string {
	return "Create a new workspace to group related tasks. Call this when the user mentions starting a new workspace, initiative, or area of work. Returns the created workspace's id — pass this id to create_task_in_workspace when creating tasks for the workspace.\nParameters: name (required, workspace title), description (optional, free text), core_team (optional, list of agent IDs associated with this workspace), repository (optional, git URL)."
}

func (t *WorkspaceCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "Workspace title (required)"},
			"description": map[string]any{"type": "string", "description": "Optional free-text description"},
			"core_team": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional list of agent IDs associated with this workspace",
			},
			"repository": map[string]any{"type": "string", "description": "Optional git repository URL"},
		},
		"required": []string{"name"},
	}
}

func (t *WorkspaceCreateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	if len(name) > 200 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name exceeds 200 characters", ""))
	}

	// Owner is stamped from the session context (attribution only — not an access gate).
	owner := tools.ToolSessionOwner(ctx)
	w := workspace{
		ID:        ulid.Make().String(),
		Name:      name,
		Status:    "active",
		Pinned:    false,
		PinOrder:  0,
		Owner:     owner,
		CreatedAt: nowISO(),
		UpdatedAt: nowISO(),
	}
	if v, ok := args["description"].(string); ok {
		if len(v) > 2000 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "description exceeds 2000 characters", ""))
		}
		w.Description = v
	}
	if raw, ok := args["core_team"].([]any); ok {
		team, err := sanitizeCoreTeam(raw)
		if err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Provide at most 20 agent IDs"))
		}
		w.CoreTeam = team
	}
	if v, ok := args["repository"].(string); ok {
		if len(v) > 500 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "repository exceeds 500 characters", ""))
		}
		w.Repository = v
	}
	if err := writeEntity(workspacesDir(t.deps.Home), w.ID, w); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": w.ID, "name": w.Name, "description": w.Description,
		"status": w.Status, "pinned": w.Pinned, "pin_order": w.PinOrder,
		"core_team": w.CoreTeam, "repository": w.Repository,
		"task_count": 0, "created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
	}))
}

// ---- system.workspace.update ----

type WorkspaceUpdateTool struct{ deps *Deps }

func NewWorkspaceUpdateTool(d *Deps) *WorkspaceUpdateTool { return &WorkspaceUpdateTool{deps: d} }
func (t *WorkspaceUpdateTool) Name() string               { return "update_workspace" }
func (t *WorkspaceUpdateTool) Scope() tools.ToolScope     { return tools.ScopeCore }
func (t *WorkspaceUpdateTool) Description() string {
	return "Update an existing workspace's name, description, status, pin state, core team, or repository. Call this when the user wants to rename, archive, pin, or reconfigure a workspace. Use list_workspaces first to find the workspace id.\nParameters: id (required, from list_workspaces), name, description, status (active/archived), pinned (bool), pin_order (int), core_team (list of agent IDs), repository (git URL). Only provided fields are updated."
}

func (t *WorkspaceUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Workspace ID from list_workspaces"},
			"name":        map[string]any{"type": "string", "description": "New workspace title"},
			"description": map[string]any{"type": "string", "description": "New description"},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"active", "archived"},
				"description": "active or archived",
			},
			"pinned":    map[string]any{"type": "boolean", "description": "Pin to top of sidebar"},
			"pin_order": map[string]any{"type": "integer", "description": "Sort position among pinned workspaces"},
			"core_team": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "List of agent IDs associated with this workspace",
			},
			"repository": map[string]any{"type": "string", "description": "Git repository URL"},
		},
		"required": []string{"id"},
	}
}

func (t *WorkspaceUpdateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}

	// Serialize the full load-modify-write cycle against this workspace ID
	// (workspace.LockID) so a racing gateway PUT/DELETE/delegation-PUT, the WS
	// setup-kickoff consumer, or another concurrent tool call cannot
	// interleave with this update. workspace.LockID's own doc comment names
	// this tool's create/update/delete paths as required callers; this was a
	// gap left over from the earlier fix wave that wired the lock into every
	// gateway workspace writer but not this tool.
	unlock := workspacepkg.LockID(id)
	defer unlock()

	w, err := readWorkspaceFromDisk(t.deps.Home, id)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_NOT_FOUND", fmt.Sprintf("No workspace %q", id),
			"Use list_workspaces to see available workspaces"))
	}

	// Snapshot the pre-update core team so a core_team change below can be
	// diffed for newly added members — see the delegation-edge auto-seed
	// block after the field-update section.
	oldTeam := append([]string(nil), w.CoreTeam...)
	coreTeamChanged := false

	if v, ok := args["name"].(string); ok && v != "" {
		if len(v) > 200 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "name exceeds 200 characters", ""))
		}
		w.Name = v
	}
	if v, ok := args["description"].(string); ok {
		if len(v) > 2000 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "description exceeds 2000 characters", ""))
		}
		w.Description = v
	}
	if v, ok := args["status"].(string); ok {
		if v != "active" && v != "archived" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				fmt.Sprintf("invalid status %q: must be active or archived", v), ""))
		}
		w.Status = v
	}
	if v, ok := args["pinned"].(bool); ok {
		w.Pinned = v
	}
	if v, ok := args["pin_order"].(float64); ok {
		w.PinOrder = int(v)
	}
	if raw, ok := args["core_team"].([]any); ok {
		team, err := sanitizeCoreTeam(raw)
		if err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Provide at most 20 agent IDs"))
		}
		w.CoreTeam = team
		coreTeamChanged = true
	}
	if v, ok := args["repository"].(string); ok {
		if len(v) > 500 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "repository exceeds 500 characters", ""))
		}
		w.Repository = v
	}

	// Auto-seed default delegation edges for newly added core_team members
	// (fix wave, ADR-037 gap). Per ADR-037 the per-workspace Delegation[] edge
	// graph is the SOLE runtime delegation authority and is fail-closed (no
	// edge ⇒ deny). Default edges used to be seeded ONLY at workspace
	// creation (pkg/gateway's defaultWorkspaceDelegationEdges); this tool had
	// no way to grow the graph at all, so a workspace whose team Ava (or an
	// operator) builds up entirely through update_workspace — the normal
	// workspace-setup-interview flow — ended up with ZERO delegation edges:
	// every worker-type specialist she added became permanently unreachable
	// by delegation. Only NEWLY ADDED members are considered — edges wholly
	// among pre-existing members are never re-added, so a deliberate prior
	// removal (via the Team tab's delegation PUT) is never silently
	// resurrected by a later, unrelated core_team edit.
	var delegationSeedNote string
	if coreTeamChanged {
		added := teamDiffAdded(oldTeam, w.CoreTeam)
		if len(added) > 0 {
			ceiling := workspaceDelegationDepthCeiling(t.deps)
			seeded := seedDelegationEdgesForNewMembers(w.CoreTeam, added, w.Delegation, ceiling)
			if len(seeded) > 0 {
				w.Delegation = append(w.Delegation, seeded...)
				delegationSeedNote = seededEdgesSummary(seeded, added)
			}
		}
	}

	// Defensive delegation-edge validation (security boundary — fail closed).
	//
	// update_workspace still does NOT expose a general `delegation` arg —
	// the ONLY way this tool can add an edge is the auto-seed block above,
	// and every candidate edge it appends has already been individually
	// validated (and dropped + logged on failure, never escalated to an
	// update-aborting error) before being appended to w.Delegation.
	// readWorkspaceFromDisk otherwise loads the existing graph and
	// writeEntity re-serializes it verbatim. But the moment ANY write path
	// persists the Delegation slice, it becomes a write surface — a corrupted
	// on-disk edge being rewritten here, or a future `delegation` arg, could
	// otherwise launder an invalid edge that is then TRUSTED at runtime (the
	// per-workspace graph is the sole delegation authority). So re-validate
	// EVERY edge (pre-existing and auto-seeded alike) against the shared
	// workspace.DelegationEdge.Validate authority before committing, using
	// the same team set (core_team ∪ existing-edge endpoints) and depth
	// ceiling the gateway PUT handler enforces. Any invalid edge aborts the
	// whole write — we never persist an unvalidated edge. This whole-graph
	// pass is a second, broader safety net on top of the auto-seed's own
	// per-candidate validation (which uses a narrower, newTeam-only team
	// set) — it is expected to be a no-op immediately after a clean
	// auto-seed, and exists to catch a corrupted on-disk edge from another
	// writer.
	if len(w.Delegation) > 0 {
		team := workspaceDelegationTeamSet(w)
		ceiling := workspaceDelegationDepthCeiling(t.deps)
		for i := range w.Delegation {
			if err := w.Delegation[i].Validate(team, ceiling); err != nil {
				return tools.ErrorResult(errorJSON("INVALID_DELEGATION_EDGE", err.Error(),
					"Fix the workspace delegation graph via the Team tab or the delegation API"))
			}
		}
	}

	w.UpdatedAt = nowISO()
	if err := writeEntity(workspacesDir(t.deps.Home), id, w); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	tc := computeWorkspaceTaskCount(t.deps.Home, id)
	resp := map[string]any{
		"id": w.ID, "name": w.Name, "description": w.Description,
		"status": w.Status, "pinned": w.Pinned, "pin_order": w.PinOrder,
		"core_team": w.CoreTeam, "repository": w.Repository,
		"task_count": tc, "created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
	}
	if delegationSeedNote != "" {
		resp["delegation_seeded"] = delegationSeedNote
	}
	return tools.NewToolResult(successJSON(resp))
}

// teamDiffAdded returns the members present in newTeam but absent from
// oldTeam — the set of newly added core_team members a core_team update
// should consider for delegation-edge auto-seeding. Both slices are assumed
// already deduplicated (sanitizeCoreTeam dedupes newTeam; a freshly-loaded
// workspace's stored core_team is likewise already deduplicated by every
// writer). Order follows newTeam.
func teamDiffAdded(oldTeam, newTeam []string) []string {
	oldSet := make(map[string]struct{}, len(oldTeam))
	for _, id := range oldTeam {
		oldSet[id] = struct{}{}
	}
	var added []string
	for _, id := range newTeam {
		if _, ok := oldSet[id]; !ok {
			added = append(added, id)
		}
	}
	return added
}

// edgeModeCategory collapses a coreagent seed's 3-value delegate-tool call
// vocabulary (config.DelegationMode: task/background/await) down to the
// workspace trust-edge's 2-value vocabulary (workspace.DelegationMode:
// direct/task).
//
// This is a DELIBERATE, INDEPENDENT copy of agent.EdgeModeCategory
// (pkg/agent/loop.go), not a shared call: pkg/agent/loop.go already imports
// pkg/sysagent/tools (as systools), so the reverse import here would create
// an import cycle. pkg/gateway's defaultWorkspaceDelegationEdges has the
// exact same collapse logic and the same "deliberate independent
// replication" precedent (its own doc comment explains why it doesn't share
// a helper with pkg/agent either). Keep this in lock-step with
// agent.EdgeModeCategory if that mapping ever changes.
func edgeModeCategory(mode config.DelegationMode) workspacepkg.DelegationMode {
	switch mode {
	case config.DelegationModeTask:
		return workspacepkg.ModeTask
	case config.DelegationModeAwait, config.DelegationModeBackground:
		return workspacepkg.ModeDirect
	default:
		slog.Warn(
			"sysagent: edgeModeCategory: unrecognized config.DelegationMode value, defaulting to ModeDirect category",
			"mode",
			string(mode),
		)
		return workspacepkg.ModeDirect
	}
}

// seedDelegationEdgesForNewMembers derives the default delegation edges an
// update_workspace core_team change should auto-add for NEWLY ADDED members.
// Mirrors pkg/gateway's defaultWorkspaceDelegationEdges: for every agent in
// newTeam with a compiled-in coreagent.SeedDelegationEdges seed, each seeded
// target becomes a candidate edge, with modes collapsed/deduped via the
// local edgeModeCategory and depth copied verbatim.
//
// A candidate edge is included iff ALL of:
//   - both endpoints are members of newTeam (an edge never reaches outside
//     the team being saved);
//   - at least one endpoint is in `added` (edges wholly among PRE-EXISTING
//     members are never re-added — a prior deliberate removal of that edge
//     via the Team tab's delegation PUT stays removed, even though the
//     agents involved are still on the team);
//   - no edge with the same (from_agent, to_agent) already exists in
//     `existing`, and it has not already been staged earlier in this same
//     call (so a shared target, e.g. two different agents both seeding
//     →worker, cannot itself produce a duplicate — though in practice
//     (from_agent, to_agent) pairs are unique per newTeam member anyway);
//   - the candidate edge passes workspace.DelegationEdge.Validate against
//     newTeam and ceiling. An invalid candidate is dropped and logged, never
//     escalated to a failure of the whole update.
func seedDelegationEdgesForNewMembers(
	newTeam []string,
	added []string,
	existing []workspacepkg.DelegationEdge,
	ceiling int,
) []workspacepkg.DelegationEdge {
	if len(added) == 0 || len(newTeam) == 0 {
		return nil
	}
	teamSet := make(map[string]bool, len(newTeam))
	for _, id := range newTeam {
		teamSet[id] = true
	}
	addedSet := make(map[string]bool, len(added))
	for _, id := range added {
		addedSet[id] = true
	}
	present := make(map[string]bool, len(existing))
	for _, e := range existing {
		present[e.FromAgent+"\x00"+e.ToAgent] = true
	}

	var out []workspacepkg.DelegationEdge
	for _, from := range newTeam {
		dp := coreagent.SeedDelegationEdges(coreagent.CoreAgentID(from))
		if dp == nil || len(dp.To) == 0 {
			continue
		}
		modes := make([]workspacepkg.DelegationMode, 0, len(dp.Modes))
		seenMode := make(map[workspacepkg.DelegationMode]bool, len(dp.Modes))
		for _, m := range dp.Modes {
			wm := edgeModeCategory(m)
			if seenMode[wm] {
				continue
			}
			seenMode[wm] = true
			modes = append(modes, wm)
		}
		var depth *int
		if dp.Depth != nil {
			d := *dp.Depth
			depth = &d
		}
		for _, ref := range dp.To {
			if ref.Kind != config.AgentRefKindLocal || ref.ID == "*" || ref.ID == from {
				continue
			}
			to := ref.ID
			if !teamSet[to] {
				continue
			}
			if !addedSet[from] && !addedSet[to] {
				continue
			}
			key := from + "\x00" + to
			if present[key] {
				continue
			}
			present[key] = true
			edge := workspacepkg.DelegationEdge{
				FromAgent: from,
				ToAgent:   to,
				Modes:     append([]workspacepkg.DelegationMode(nil), modes...),
				Depth:     depth,
			}
			if err := edge.Validate(teamSet, ceiling); err != nil {
				slog.Warn("sysagent: update_workspace: dropping invalid auto-seeded delegation edge",
					"from_agent", from, "to_agent", to, "error", err)
				continue
			}
			out = append(out, edge)
		}
	}
	return out
}

// seededEdgesSummary formats the tool's success-result note about
// auto-seeded delegation edges, e.g. "seeded 2 default delegation edge(s)
// for: jim, worker" — listing the newly added members actually touched (as
// either endpoint) by a seeded edge, sorted for stable output. An added
// member with no seedable edges (no compiled-in seed of its own, and never
// referenced as a seeded target) is not mentioned. Returns "" when seeded is
// empty.
func seededEdgesSummary(seeded []workspacepkg.DelegationEdge, added []string) string {
	if len(seeded) == 0 {
		return ""
	}
	addedSet := make(map[string]bool, len(added))
	for _, id := range added {
		addedSet[id] = true
	}
	touched := make(map[string]bool)
	for _, e := range seeded {
		if addedSet[e.FromAgent] {
			touched[e.FromAgent] = true
		}
		if addedSet[e.ToAgent] {
			touched[e.ToAgent] = true
		}
	}
	names := make([]string, 0, len(touched))
	for id := range touched {
		names = append(names, id)
	}
	sort.Strings(names)
	return fmt.Sprintf("seeded %d default delegation edge(s) for: %s", len(seeded), strings.Join(names, ", "))
}

// workspaceDelegationDepthCeilingFallback mirrors the gateway's
// delegationDepthCeilingFallback (and the agent loop's getSubTurnConfig default):
// the effective max delegation chain depth when agents.defaults.subturn.max_depth
// is unset. Kept in lock-step so the tool's defensive edge validation applies the
// exact same depth ceiling the gateway PUT handler does.
const workspaceDelegationDepthCeilingFallback = 3

// workspaceDelegationDepthCeiling returns the effective delegation depth ceiling
// from the live config, mirroring gateway.delegationDepthCeiling: the configured
// agents.defaults.subturn.max_depth when set (> 0), else the fallback of 3. A nil
// GetCfg (tests without a wired config) yields the fallback.
func workspaceDelegationDepthCeiling(d *Deps) int {
	if d == nil || d.GetCfg == nil {
		return workspaceDelegationDepthCeilingFallback
	}
	cfg := d.GetCfg()
	if cfg != nil && cfg.Agents.Defaults.SubTurn.MaxDepth > 0 {
		return cfg.Agents.Defaults.SubTurn.MaxDepth
	}
	return workspaceDelegationDepthCeilingFallback
}

// workspaceDelegationTeamSet computes the workspace team membership set used to
// validate a delegation edge's endpoints, delegating to the canonical
// workspace.TeamSet — the SINGLE derivation shared with the gateway PUT handler
// (gateway.workspaceTeamSet is the same thin adapter). Routing both call sites
// through one helper guarantees the tool's defensive validation neither over- nor
// under-restricts relative to the API write path; previously the two hand-rolled
// copies diverged on whitespace trimming (this one did NOT trim), which TeamSet
// now fixes uniformly.
func workspaceDelegationTeamSet(w workspace) map[string]bool {
	return workspacepkg.TeamSet(w.CoreTeam, w.Delegation)
}

// ---- system.workspace.delete ----

type WorkspaceDeleteTool struct{ deps *Deps }

func NewWorkspaceDeleteTool(d *Deps) *WorkspaceDeleteTool { return &WorkspaceDeleteTool{deps: d} }
func (t *WorkspaceDeleteTool) Name() string               { return "delete_workspace" }
func (t *WorkspaceDeleteTool) Scope() tools.ToolScope     { return tools.ScopeCore }
func (t *WorkspaceDeleteTool) Description() string {
	return "Delete a workspace and all its tasks. This is irreversible — all GTD tasks belonging to the workspace are permanently deleted. Call list_workspaces first to find the workspace id. Requires confirm:true.\nParameters: id (required), confirm (bool, must be true to prevent accidental deletion)."
}

func (t *WorkspaceDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Workspace ID from list_workspaces"},
			"confirm": map[string]any{
				"type":        "boolean",
				"description": "Must be true to confirm irreversible deletion",
			},
		},
		"required": []string{"id", "confirm"},
	}
}

func (t *WorkspaceDeleteTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
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
			"confirm must be true to delete a workspace", ""))
	}

	// Serialize the whole read-check-cascade-remove cycle against this
	// workspace ID (workspace.LockID), mirroring the gateway's
	// handleWorkspaceDelete: a racing kickoff consume, PUT, or
	// delegation-PUT must not be able to read/write the workspace file while
	// this irreversible delete is in flight.
	unlock := workspacepkg.LockID(id)
	defer unlock()

	// Guard: verify the workspace exists before any irreversible mutations.
	if _, err := readWorkspaceFromDisk(t.deps.Home, id); err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_NOT_FOUND", fmt.Sprintf("No workspace %q", id),
			"Use list_workspaces to see available workspaces"))
	}

	// Step 1: cascade-delete tasks
	tasks, err := listEntities[unifiedTask](tasksDir(t.deps.Home))
	if err != nil {
		slog.Error("sysagent: workspace cascade delete: failed to list tasks", "workspace_id", id, "error", err)
		return tools.ErrorResult(errorJSON("SAVE_FAILED", "could not list tasks for cascade delete: "+err.Error(), ""))
	}
	tasksDeleted := 0
	for _, tk := range tasks {
		if !isValidTaskStatus(string(tk.Status)) {
			continue
		}
		if tk.WorkspaceID == id {
			if err := deleteEntity(tasksDir(t.deps.Home), tk.ID); err != nil {
				slog.Warn("sysagent: workspace cascade delete: failed to delete task",
					"workspace_id", id, "task_id", tk.ID, "error", err)
			} else {
				tasksDeleted++
			}
		}
	}

	// Step 2: delete the workspace file.
	if err := deleteEntity(workspacesDir(t.deps.Home), id); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}

	// Step 3: best-effort cascade of per-workspace directory (AGENT.md / memory room).
	// The JSON removal above is the authoritative delete; a stale directory is not fatal.
	wsDir := workspacepkg.WorkspaceDir(t.deps.Home, id)
	if err := os.RemoveAll(wsDir); err != nil {
		slog.Warn("sysagent: workspace cascade delete: failed to remove workspace dir",
			"workspace_id", id, "dir", wsDir, "error", err)
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"id": id, "deleted": true, "tasks_deleted": tasksDeleted,
	}))
}

// ---- system.workspace.list ----

type WorkspaceListTool struct{ deps *Deps }

func NewWorkspaceListTool(d *Deps) *WorkspaceListTool { return &WorkspaceListTool{deps: d} }
func (t *WorkspaceListTool) Name() string             { return "list_workspaces" }
func (t *WorkspaceListTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *WorkspaceListTool) Description() string {
	return "List all active workspaces with their task counts. Call this to find workspace ids before calling create_task_in_workspace, list_tasks_in_workspace, or update_workspace. Returns newest workspaces first.\nParameters: status (optional: active/archived/all, defaults to active)."
}

func (t *WorkspaceListTool) Parameters() map[string]any {
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

func (t *WorkspaceListTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	statusFilter, _ := args["status"].(string)
	if statusFilter == "" {
		statusFilter = "active"
	}

	// Single-pass task count for all workspaces.
	counts := computeWorkspaceTaskCounts(t.deps.Home)

	// Read all workspaces from disk, applying the legacy migration via workspaceFromFile.
	dir := workspacesDir(t.deps.Home)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return tools.ErrorResult(errorJSON("LIST_FAILED", err.Error(), ""))
	}

	var workspaces []workspace
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < 6 || e.Name()[len(e.Name())-5:] != ".json" {
			continue
		}
		wid := e.Name()[:len(e.Name())-5]
		data, err := os.ReadFile(entityPath(dir, wid))
		if err != nil {
			slog.Warn("sysagent: skipping unreadable workspace file", "file", e.Name(), "error", err)
			continue
		}
		w, err := workspaceFromFile(data)
		if err != nil {
			slog.Warn("sysagent: skipping corrupt workspace file", "file", e.Name(), "error", err)
			continue
		}
		// Apply status filter.
		if statusFilter != "all" && w.Status != statusFilter {
			continue
		}
		workspaces = append(workspaces, w)
	}

	// Sort: default workspace first, then pinned by pin_order ascending,
	// then unpinned by created_at descending (newest first).
	sort.Slice(workspaces, func(i, j int) bool {
		wi, wj := workspaces[i], workspaces[j]
		if wi.IsDefault != wj.IsDefault {
			return wi.IsDefault // default before non-default
		}
		if wi.Pinned != wj.Pinned {
			return wi.Pinned // pinned before unpinned
		}
		if wi.Pinned && wj.Pinned {
			if wi.PinOrder != wj.PinOrder {
				return wi.PinOrder < wj.PinOrder
			}
			return wi.CreatedAt < wj.CreatedAt
		}
		return wi.CreatedAt > wj.CreatedAt // unpinned: newest first
	})

	// Build response list attaching computed task counts.
	type workspaceResponse struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Status      string   `json:"status"`
		Pinned      bool     `json:"pinned"`
		PinOrder    int      `json:"pin_order"`
		IsDefault   bool     `json:"is_default"`
		CoreTeam    []string `json:"core_team,omitempty"`
		Repository  string   `json:"repository,omitempty"`
		TaskCount   int      `json:"task_count"`
		CreatedAt   string   `json:"created_at"`
		UpdatedAt   string   `json:"updated_at"`
	}
	resp := make([]workspaceResponse, 0, len(workspaces))
	for _, w := range workspaces {
		resp = append(resp, workspaceResponse{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			Status:      w.Status,
			Pinned:      w.Pinned,
			PinOrder:    w.PinOrder,
			IsDefault:   w.IsDefault,
			CoreTeam:    w.CoreTeam,
			Repository:  w.Repository,
			TaskCount:   counts[w.ID],
			CreatedAt:   w.CreatedAt,
			UpdatedAt:   w.UpdatedAt,
		})
	}
	return tools.NewToolResult(successJSON(map[string]any{"workspaces": resp}))
}

// ---- system.workspace.get ----

type WorkspaceGetTool struct{ deps *Deps }

func NewWorkspaceGetTool(d *Deps) *WorkspaceGetTool { return &WorkspaceGetTool{deps: d} }
func (t *WorkspaceGetTool) Name() string            { return "get_workspace" }
func (t *WorkspaceGetTool) Scope() tools.ToolScope  { return tools.ScopeCore }
func (t *WorkspaceGetTool) Description() string {
	return "Get a single workspace by ID including its live task count. Use this to refresh workspace data after creating tasks.\nParameters: id (required, from list_workspaces)."
}

func (t *WorkspaceGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Workspace ID from list_workspaces"},
		},
		"required": []string{"id"},
	}
}

func (t *WorkspaceGetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	w, err := readWorkspaceFromDisk(t.deps.Home, id)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_NOT_FOUND", fmt.Sprintf("No workspace %q", id),
			"Use list_workspaces to see available workspaces"))
	}
	tc := computeWorkspaceTaskCount(t.deps.Home, id)
	return tools.NewToolResult(successJSON(map[string]any{
		"id": w.ID, "name": w.Name, "description": w.Description,
		"status": w.Status, "pinned": w.Pinned, "pin_order": w.PinOrder,
		"is_default": w.IsDefault, "core_team": w.CoreTeam, "repository": w.Repository,
		"task_count": tc, "created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
	}))
}
