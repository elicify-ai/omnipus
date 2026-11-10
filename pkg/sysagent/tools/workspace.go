// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

func workspaceRevisionError(id string, err error) *tools.ToolResult {
	if errors.Is(err, workspacepkg.ErrInvalidRevision) || errors.Is(err, workspacepkg.ErrInvalidWorkspaceID) {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Use the revision returned by get_workspace"))
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "NOT_FOUND") {
		return tools.ErrorResult(errorJSON("WORKSPACE_NOT_FOUND", fmt.Sprintf("No workspace %q", id), "Use list_workspaces to see available workspaces"))
	}
	if errors.Is(err, workspacepkg.ErrRevisionConflict) {
		return tools.ErrorResult(errorJSON("REVISION_CONFLICT", err.Error(), "Call get_workspace and retry with its current revision"))
	}
	if errors.Is(err, workspacepkg.ErrDelegationUnreadable) {
		return tools.ErrorResult(errorJSON("DELEGATION_STORE_UNREADABLE", err.Error(),
			"Inspect $OMNIPUS_HOME/entities/delegation/"+id+".json and restore a readable record; this workspace still exists"))
	}
	return tools.ErrorResult(errorJSON("READ_FAILED", err.Error(), "Inspect workspace storage; a revision retry cannot repair unreadable storage"))
}

func workspaceReadError(id string, err error) *tools.ToolResult {
	if errors.Is(err, workspacepkg.ErrInvalidWorkspaceID) {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Use a workspace id from list_workspaces"))
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "NOT_FOUND") {
		return tools.ErrorResult(errorJSON("WORKSPACE_NOT_FOUND", fmt.Sprintf("No workspace %q", id), "Use list_workspaces to see available workspaces"))
	}
	if errors.Is(err, workspacepkg.ErrDelegationUnreadable) {
		return tools.ErrorResult(errorJSON("DELEGATION_STORE_UNREADABLE", err.Error(),
			"Inspect $OMNIPUS_HOME/entities/delegation/"+id+".json and restore a readable record; this workspace still exists"))
	}
	return tools.ErrorResult(errorJSON("READ_FAILED", err.Error(), "Inspect the workspace record on disk; this is not a missing workspace"))
}

// workspace is the canonical on-disk workspace type shared with pkg/gateway.
// Using the shared type (instead of a local struct) ensures that both write
// paths — the REST gateway and the update_workspace tool — serialize exactly
// the same fields in exactly the same JSON tags, so neither can silently drop
// fields (in particular the delegation graph) written by the other.
type workspace = workspacepkg.Workspace

func workspacesDir(home string) string { return filepath.Join(home, "workspaces") }

var (
	saveWorkspaceDelegation = workspacepkg.SaveDelegation
	deleteWorkspaceEntity   = deleteEntity
)

func workspaceChangedFields(before, after workspace) []string {
	fields := make([]string, 0, 7)
	if before.Name != after.Name {
		fields = append(fields, "name")
	}
	if before.Description != after.Description {
		fields = append(fields, "description")
	}
	if before.Status != after.Status {
		fields = append(fields, "status")
	}
	if before.Pinned != after.Pinned {
		fields = append(fields, "pinned")
	}
	if before.PinOrder != after.PinOrder {
		fields = append(fields, "pin_order")
	}
	if !slices.Equal(before.CoreTeam, after.CoreTeam) {
		fields = append(fields, "core_team")
	}
	if before.SetupPending != after.SetupPending {
		fields = append(fields, "setup_pending")
	}
	return fields
}

func workspaceActualState(state workspacepkg.State) map[string]any {
	edges := append([]workspacepkg.DelegationEdge{}, state.Delegation...)
	return map[string]any{
		"exists":      true,
		"id":          state.Workspace.ID,
		"name":        state.Workspace.Name,
		"description": state.Workspace.Description,
		"status":      state.Workspace.Status,
		"pinned":      state.Workspace.Pinned,
		"pin_order":   state.Workspace.PinOrder,
		"core_team":   state.Workspace.CoreTeam,
		"delegation":  edges,
	}
}

func workspaceMutationError(id, stage, persistence, revision string, changed []string, actual map[string]any) *tools.ToolResult {
	payload := map[string]any{
		"success":            false,
		"id":                 id,
		"persistence_status": persistence,
		"activation_status":  "not_attempted",
		"changed_fields":     changed,
		"error_stage":        stage,
		"message":            "Workspace state could not be saved completely. Read the current workspace before retrying.",
		"actual_state":       actual,
		"error": map[string]any{
			"code":       "SAVE_FAILED",
			"message":    "workspace state could not be saved completely",
			"suggestion": "Read the current workspace and delegation graph before retrying",
		},
	}
	if revision != "" {
		payload["revision"] = revision
	}
	if persistence == "none" {
		return tools.ErrorResult(successJSON(payload))
	}
	return changeErrorWithOutcome(payload, partlySaved(
		fmt.Sprintf("the workspace was partly saved: the %s step failed", stage), "workspace state re-read from disk",
		[]string{"step " + stage}))
}

// workspaceView is the one canonical workspace object every workspace tool
// result uses (#1057 F6): every field present, lists never null.
func workspaceView(w workspace, edges []workspacepkg.DelegationEdge, revision string, taskCount int) map[string]any {
	team := w.CoreTeam
	if team == nil {
		team = []string{}
	}
	if edges == nil {
		edges = []workspacepkg.DelegationEdge{}
	}
	return map[string]any{
		"id": w.ID, "name": w.Name, "description": w.Description,
		"status": w.Status, "pinned": w.Pinned, "pin_order": w.PinOrder,
		"is_default": w.IsDefault, "core_team": team, "delegation": edges,
		"revision": revision, "task_count": taskCount,
		"created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
	}
}

// teamMemberRefusal returns the refusal for the first id in ids that is not an
// agent under exact identity (#1057 F2; exact-id ruling 2026-10-14): neither
// the live roster nor the agent entity store's exact read knows it. A file at
// the expected record name is not enough — the store checks the record's own
// id and exact file name (SF-07). An agent record that cannot be read is
// refused as READ_FAILED, distinct from an unknown id. nil when every id is an
// agent.
func teamMemberRefusal(d *Deps, ids []string) *tools.ToolResult {
	live := configAgentPresenceSet(d)
	for _, id := range ids {
		if live[id] {
			continue
		}
		if d == nil || strings.TrimSpace(d.Home) == "" || validateID(id) != nil {
			return unknownTeamMemberResult(id)
		}
		_, err := agentstore.New(d.Home).Get(id)
		switch {
		case err == nil:
			continue
		case errors.Is(err, entity.ErrNotFound):
			return unknownTeamMemberResult(id)
		default:
			return tools.ErrorResult(errorJSON("READ_FAILED",
				fmt.Sprintf("core_team names %q, whose agent record could not be read: %v", id, err),
				"Repair the agent record, or leave it out of core_team"))
		}
	}
	return nil
}

// unknownTeamMemberResult is the refusal for a core_team entry that names no agent.
func unknownTeamMemberResult(id string) *tools.ToolResult {
	return tools.ErrorResult(errorJSON("INVALID_INPUT",
		fmt.Sprintf("core_team names %q, which is not an agent", id),
		"Use agent ids from list_agents"))
}

// sanitizeCoreTeam deduplicates (case-sensitive) the raw entries. Empty strings
// and non-string values are silently dropped.
func sanitizeCoreTeam(raw []any) []string {
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
	return out
}

// firstExcludedTeamMember returns the first team entry whose roster identity
// is barred from workspace-team membership (coreagent.
// ExcludedFromWorkspaceTeams — the hidden System Agents), or "" when the
// team is clean. ADR-090 FR-006: hidden agents cannot be added as ordinary
// teammates to bypass role boundaries. These tool write paths bypass the REST validator
// (gateway's validateCoreTeamMembers), so without this check an agent-driven
// update_workspace could land exactly the membership the API refuses —
// sanitizeCoreTeam above only dedupes.
func firstExcludedTeamMember(team []string) string {
	if i := slices.IndexFunc(team, func(id string) bool {
		return coreagent.ExcludedFromWorkspaceTeams(coreagent.CoreAgentID(id))
	}); i >= 0 {
		return team[i]
	}
	return ""
}

// excludedTeamMemberMessage says WHY the excluded id cannot join a team, in
// the same vocabulary the REST validator (gateway's validateCoreTeamMembers)
// already uses, so an operator moving between the Team tab and an
// agent-driven tool call sees one consistent rejection instead of two.
func excludedTeamMemberMessage(id string) string {
	return fmt.Sprintf("core_team member %q is a hidden System Agent and cannot be added to a workspace team roster", id)
}

// omnipusMembershipError is the FR-003 refusal in this package's error shape.
func omnipusMembershipError() *tools.ToolResult {
	return tools.ErrorResult(errorJSON("INVALID_INPUT", coreagent.OmnipusMembershipRefusalText,
		"Leave Omnipus on the default workspace's team only"))
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
		if errors.Is(err, os.ErrNotExist) {
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
	return "Create a new workspace to group related tasks. Call this when the user mentions starting a new workspace, initiative, or area of work. Returns the created workspace's id — pass this id as workspace_id to create_task when delivering tasks to the workspace. Members named in core_team also get the default delegation trust edges between them, so they can delegate to each other immediately. name is capped at 200 characters and description at 2000; exceeding either is rejected.\nParameters: name (required, workspace title), description (optional, free text), core_team (optional, list of agent IDs associated with this workspace)."
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
			"delegation": map[string]any{
				"type": "array", "description": "Complete replacement delegation graph; [] clears it",
				"items": map[string]any{"type": "object", "properties": map[string]any{
					"from_agent": map[string]any{"type": "string"}, "to_agent": map[string]any{"type": "string"},
					"modes": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"direct", "task"}}},
					"depth": map[string]any{"type": "integer", "minimum": 0},
				}, "required": []string{"from_agent", "to_agent"}},
			},
		},
		"required": []string{"name"},
	}
}

func (t *WorkspaceCreateTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
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
		w.CoreTeam = sanitizeCoreTeam(raw)
	}
	// ADR-090 FR-006 — reject an excluded roster identity (Admin, hidden
	// System Agents) BEFORE any write. At create time the whole core_team is
	// newly introduced, so every entry is checked; this mirrors the REST
	// POST's whole-list validateCoreTeamMembers and closes the tool-side
	// bypass of it (sanitizeCoreTeam only dedupes).
	if excluded := firstExcludedTeamMember(w.CoreTeam); excluded != "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", excludedTeamMemberMessage(excluded),
			"Remove the excluded role from core_team — ADR-090 bars it from workspace teams"))
	}
	// omnipus-system-agent FR-003: a created workspace is never the default
	// one, so Omnipus cannot be on its team.
	if coreagent.OmnipusMembershipRefusal(false, nil, w.CoreTeam) {
		return omnipusMembershipError()
	}
	if refusal := teamMemberRefusal(t.deps, w.CoreTeam); refusal != nil {
		return refusal
	}
	explicitDelegation, delegationPresent, delegationErr := parseWorkspaceDelegationArg(args, w.CoreTeam, workspaceDelegationDepthCeiling(t.deps))
	if delegationErr != nil {
		return tools.ErrorResult(errorJSON("INVALID_DELEGATION_EDGE", delegationErr.Error(), "Send a complete valid graph using current team members"))
	}

	// Serialize this create against the freshly-minted workspace ID
	// (workspace.LockID), mirroring update_workspace/delete_workspace.
	// workspace.LockID's own doc comment names the create path as a required
	// caller — this tool previously never took the lock at all, which meant a
	// racing gateway PUT/DELETE/delegation-PUT against the same ID (however
	// unlikely with a fresh ULID) was not excluded, and — more importantly —
	// the delegation-store write below (SaveDelegation) requires the lock to
	// be held per its own caller contract.
	unlock := workspacepkg.LockID(w.ID)
	defer unlock()

	// Compute (but do not yet persist) the default delegation edges for the
	// initial core_team, closing the same ADR-037 gap
	// update_workspace's seedDelegationEdgesForNewMembers fixes for the update
	// path (see that function's doc comment for the full rationale). Without
	// this, a workspace created here with a core_team gets ZERO delegation
	// edges — under ADR-037's fail-closed rule (no edge ⇒ deny) no member
	// could delegate to any other. The REST create path (pkg/gateway's
	// defaultWorkspaceDelegationEdges) already seeds at creation; this tool
	// did not. Every member of a fresh core_team is "newly added" relative to
	// the empty prior team, so `added` is the whole team.
	var seeded []workspacepkg.DelegationEdge
	if len(w.CoreTeam) > 0 {
		ceiling := workspaceDelegationDepthCeiling(t.deps)
		configPresent := configAgentPresenceSet(t.deps)
		seeded = seedDelegationEdgesForNewMembers(w.CoreTeam, w.CoreTeam, nil, ceiling, configPresent, selfEdgeExcludedAgentIDs(t.deps))
	}
	if delegationPresent {
		// Explicit graph replaces the default seed, but every member of the
		// fresh team still gets its ordinary self-edge (FR-014/015/016).
		seeded, _ = withNewMemberSelfEdges(explicitDelegation, w.CoreTeam,
			workspaceDelegationDepthCeiling(t.deps), configAgentPresenceSet(t.deps), selfEdgeExcludedAgentIDs(t.deps))
	}

	if err := writeEntity(workspacesDir(t.deps.Home), w.ID, w); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	// Persist seeded edges AFTER the record, so a failed record write can
	// never leave the store authorizing agents the record does not list on
	// the team — same ordering rationale as update_workspace.
	var delegationSeedNote string
	if len(seeded) > 0 {
		if err := saveWorkspaceDelegation(t.deps.Home, w.ID, seeded); err != nil {
			slog.Error("sysagent: create_workspace: failed to seed delegation edges",
				"workspace_id", w.ID, "error", err)
			if rollbackErr := deleteWorkspaceEntity(workspacesDir(t.deps.Home), w.ID); rollbackErr != nil {
				slog.Error("sysagent: create_workspace: failed to roll back workspace record", "workspace_id", w.ID, "error", rollbackErr)
				actual, readErr := workspacepkg.ReadStateLocked(t.deps.Home, w.ID)
				if readErr != nil {
					slog.Error("sysagent: create_workspace: failed to read partial state", "workspace_id", w.ID, "error", readErr)
					return workspaceMutationError(w.ID, "rollback_workspace", "partial", "", []string{"workspace"}, map[string]any{"exists": true, "state_read_failed": true})
				}
				return workspaceMutationError(w.ID, "rollback_workspace", "partial", actual.Revision, []string{"workspace"}, workspaceActualState(actual))
			}
			return workspaceMutationError(w.ID, "save_delegation", "none", "", []string{}, map[string]any{"exists": false, "delegation": []workspacepkg.DelegationEdge{}})
		} else {
			delegationSeedNote = seededEdgesSummary(seeded, w.CoreTeam)
		}
	}
	return workspaceWrittenResult(t.deps, w.ID, workspaceWrite{
		changed: []string{"workspace"}, seedNote: delegationSeedNote, ceiling: workspaceDelegationDepthCeiling(t.deps),
		provision: len(w.CoreTeam) > 0, team: w.CoreTeam, toolName: "create_workspace",
	})
}

// workspaceWrite describes one completed workspace write for its read-back.
type workspaceWrite struct {
	changed     []string
	seedNote    string
	setupNote   string
	beforeEdges []workspacepkg.DelegationEdge // the graph before this call (nil on create)
	ceiling     int
	// provision asks for the main chats of team (the saved core team) to be
	// prepared after the write (memberMainsWarning); toolName names the caller.
	provision bool
	team      []string
	toolName  string
}

// workspaceWrittenResult re-reads a just-written workspace and renders the
// canonical object with its new revision (#1057 F4) and the FR-032 verdict:
// the saved graph must be valid (every edge passes the shared validation, no
// cycle) and every edge this call added must join two current core-team
// members. A read-back failure or a graph problem is "saved but not working".
// The caller holds workspacepkg.LockID(id).
func workspaceWrittenResult(deps *Deps, id string, wr workspaceWrite) *tools.ToolResult {
	home := deps.Home
	const scope = "workspace record and delegation graph re-read from disk; graph validity and new edges' membership checked"
	notTested := []string{"a delegation between the members"}
	// After a team change every member gets its main chat (FR-002). This runs
	// before, and independently of, the read-back: the team is saved either
	// way, and a provisioning problem never undoes it — it is reported.
	var problems []string
	mainWarning := ""
	if wr.provision && len(wr.team) > 0 {
		mainWarning = memberMainsWarning(deps, wr.toolName, id, wr.team)
	}
	state, err := readWorkspaceStateBack(home, id)
	if err != nil {
		resp := map[string]any{"id": id, "changed_fields": wr.changed}
		problems = append(problems, "the saved workspace could not be read back: "+err.Error())
		if mainWarning != "" {
			resp["main_chat_warning"] = mainWarning
			problems = append(problems, mainWarning)
		}
		return changeSuccess(resp, savedNotWorking(strings.Join(problems, " "), scope, notTested...))
	}
	resp := workspaceView(state.Workspace, state.Delegation, state.Revision, computeWorkspaceTaskCount(home, id))
	resp["changed_fields"] = wr.changed
	if wr.seedNote != "" {
		resp["delegation_seeded"] = wr.seedNote
	}
	if wr.setupNote != "" {
		resp["setup_completed"] = wr.setupNote
	}
	if mainWarning != "" {
		resp["main_chat_warning"] = mainWarning
		problems = append(problems, mainWarning)
	}
	if graph := workspaceGraphProblems(state, wr.beforeEdges, wr.ceiling); len(graph) > 0 {
		problems = append(problems, "the saved delegation graph is not valid: "+strings.Join(graph, "; "))
	}
	if len(problems) > 0 {
		return changeSuccess(resp, savedNotWorking(strings.Join(problems, " "), scope, notTested...))
	}
	return changeSuccess(resp, working(scope, notTested...))
}

// readWorkspaceStateBack is the post-write read-back. A variable only so a
// test can fail it (FailWorkspaceReadBackForTest); production never reassigns it.
var readWorkspaceStateBack = workspacepkg.ReadStateLocked

// workspaceGraphProblems checks a saved state's delegation graph (FR-032 /
// OSA-4): every edge passes the shared validation against the runtime team
// set, the graph has no cycle, and every edge not in before joins two current
// core-team members. One line per problem.
func workspaceGraphProblems(state workspacepkg.State, before []workspacepkg.DelegationEdge, ceiling int) []string {
	runtimeTeam := state.Team()
	members := workspacepkg.TeamSet(state.Workspace.CoreTeam, nil)
	existed := make(map[string]bool, len(before))
	for _, e := range before {
		existed[e.FromAgent+"\x00"+e.ToAgent] = true
	}
	var problems []string
	for _, e := range state.Delegation {
		if err := e.Validate(runtimeTeam, ceiling); err != nil {
			problems = append(problems, fmt.Sprintf("edge %s → %s: %v", e.FromAgent, e.ToAgent, err))
			continue
		}
		if existed[e.FromAgent+"\x00"+e.ToAgent] {
			continue
		}
		if !members[strings.TrimSpace(e.FromAgent)] || !members[strings.TrimSpace(e.ToAgent)] {
			problems = append(problems, fmt.Sprintf("new edge %s → %s joins an agent that is not on the core team",
				e.FromAgent, e.ToAgent))
		}
	}
	if delegationHasCycle(state.Delegation) {
		problems = append(problems, "the delegation graph contains a cycle")
	}
	return problems
}

// provisionMemberMains gives every eligible member of workspaceID's team its
// main chat after a workspace tool changed the team (omnipus-system-agent
// FR-002; the same provisioning a REST workspace save and create_agent run,
// final-gate H2). The sidebar lists a member only when its member_configs
// entry carries main_session_id, which is advertised only for a main that
// already exists, so without this a member added through these tools stayed
// invisible and its chat "unavailable" (L1 hands-on finding, 2026-10-14).
//
// The membership change itself has already been saved, so a provisioning
// failure does not undo it: it is logged at ERROR and returned as a warning
// for the tool result, so the agent tells the user instead of the member
// silently missing from the sidebar. The main is re-attempted when the chat
// is opened. Returns "" on success or when provisioning is not wired.
func provisionMemberMains(deps *Deps, toolName, workspaceID string) string {
	if deps == nil || deps.EnsureWorkspaceMains == nil {
		return ""
	}
	if err := deps.EnsureWorkspaceMains(workspaceID); err != nil {
		slog.Error("sysagent: "+toolName+": a member's main chat was not created; it is re-attempted when the chat is opened",
			"workspace_id", workspaceID, "error", err)
		return "The team was saved, but a member's main chat could not be prepared: " + err.Error() +
			". It is retried when that chat is opened."
	}
	return ""
}

// memberMainsWarning prepares the main chats of team's members
// (provisionMemberMains) and returns "" when every member that should have one
// can get it, or the warning to report. The main provisioner only sees agents
// in the live roster, so a native chat-target agent that is saved but not
// active (its activation failed or is pending) is skipped without an error;
// that skip is named here instead of being reported as success (W-R2-01).
func memberMainsWarning(deps *Deps, toolName, workspaceID string, team []string) string {
	var parts []string
	if w := provisionMemberMains(deps, toolName, workspaceID); w != "" {
		parts = append(parts, w)
	}
	inactive, err := inactiveNativeMembers(deps, team)
	if len(inactive) > 0 {
		parts = append(parts, fmt.Sprintf("Agent(s) %s are saved but not active, so their main chat could not be "+
			"prepared; it is prepared when the agent is active and its chat is opened.", strings.Join(inactive, ", ")))
	}
	if err != nil {
		slog.Error("sysagent: "+toolName+": member records could not be verified for main chats",
			"workspace_id", workspaceID, "error", err)
		parts = append(parts, fmt.Sprintf("The team was saved, but main-chat readiness in workspace %q could not be verified: %v. "+
			"Repair the agent record or remove the member from the team, then retry.", workspaceID, err))
	}
	return strings.Join(parts, " ")
}

// inactiveNativeMembers lists the members that are saved native chat-target
// agents but missing from the live roster. Read failures are returned alongside
// any verified inactive members, never treated as intentional ineligibility.
// Nothing is listed when the live roster cannot be read (nothing to compare against).
func inactiveNativeMembers(deps *Deps, team []string) ([]string, error) {
	if !liveConfigAvailable(deps) || strings.TrimSpace(deps.Home) == "" || deps.EnsureWorkspaceMains == nil {
		return nil, nil
	}
	live := configAgentPresenceSet(deps)
	store := agentstore.New(deps.Home)
	var out []string
	var verificationErr error
	for _, id := range team {
		if live[id] {
			continue
		}
		a, err := store.Get(id)
		if err != nil {
			verificationErr = errors.Join(verificationErr, fmt.Errorf("agent %q could not be read: %w", id, err))
			continue
		}
		if a.IsChatTarget() {
			out = append(out, id)
		}
	}
	return out, verificationErr
}

// ---- system.workspace.update ----

type WorkspaceUpdateTool struct{ deps *Deps }

func NewWorkspaceUpdateTool(d *Deps) *WorkspaceUpdateTool { return &WorkspaceUpdateTool{deps: d} }
func (t *WorkspaceUpdateTool) Name() string               { return "update_workspace" }
func (t *WorkspaceUpdateTool) Scope() tools.ToolScope     { return tools.ScopeCore }
func (t *WorkspaceUpdateTool) Description() string {
	return "Update an existing workspace using the revision returned by get_workspace. Call this to change its name, description, status, pin state, complete core team, or complete delegation graph. Omitted fields are preserved. core_team replaces the entire team. Omitted delegation preserves valid existing edges, removes edges for removed members, and may seed approved edges for newly added built-ins; an explicit delegation array replaces the graph for continuing members, and [] clears their lines; each member newly added by the same update still receives its self-line unless the operator's seed defaults exclude it. Read the workspace first, include all intended membership and graph effects in the proposal, then submit its id and revision."
}

func (t *WorkspaceUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Workspace ID from list_workspaces"},
			"revision":    map[string]any{"type": "string", "description": "Revision returned by get_workspace"},
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
			"delegation": map[string]any{
				"type": "array", "description": "Complete replacement delegation graph; [] clears it",
				"items": map[string]any{"type": "object", "properties": map[string]any{
					"from_agent": map[string]any{"type": "string"}, "to_agent": map[string]any{"type": "string"},
					"modes": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"direct", "task"}}},
					"depth": map[string]any{"type": "integer", "minimum": 0},
				}, "required": []string{"from_agent", "to_agent"}},
			},
		},
		"required": []string{"id", "revision"},
	}
}

// workspaceUpdate carries update_workspace's state across its stages.
type workspaceUpdate struct {
	t                 *WorkspaceUpdateTool
	args              map[string]any
	id                string
	state             workspacepkg.State
	w                 workspace
	oldTeam           []string
	coreTeamChanged   bool
	pendingDelegation []workspacepkg.DelegationEdge
	seedNote          string
	setupNote         string
}

func (t *WorkspaceUpdateTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	id, _ := args["id"].(string)
	revision, _ := args["revision"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}

	// Serialize the full load-modify-write cycle against this workspace ID
	// (workspace.LockID) so a racing gateway PUT/DELETE/delegation-PUT, the WS
	// setup-kickoff consumer, or another concurrent tool call cannot
	// interleave with this update (workspace.LockID names this tool's
	// create/update/delete paths as required callers).
	unlock := workspacepkg.LockID(id)
	defer unlock()

	state, err := workspacepkg.CheckRevisionLocked(t.deps.Home, id, revision)
	if err != nil {
		return workspaceRevisionError(id, err)
	}
	u := &workspaceUpdate{t: t, args: args, id: id, state: state, w: state.Workspace,
		oldTeam: append([]string(nil), state.Workspace.CoreTeam...)}
	if r := u.applyFieldsAndTeam(); r != nil {
		return r
	}
	if r := u.planDelegation(); r != nil {
		return r
	}
	return u.commit()
}

// applyFieldsAndTeam applies the record fields and checks a core_team change
// against every membership rule, before any write.
func (u *workspaceUpdate) applyFieldsAndTeam() *tools.ToolResult {
	var fieldError *tools.ToolResult
	u.coreTeamChanged, fieldError = updateWorkspaceFields(&u.w, u.args)
	if fieldError != nil {
		return fieldError
	}
	if !u.coreTeamChanged {
		return nil
	}
	added := teamDiffAdded(u.oldTeam, u.w.CoreTeam)
	// ADR-090 FR-006 — reject INTRODUCING an excluded roster identity (Admin,
	// hidden System Agents) before any write. The DELTA rule the REST PUT
	// applies: only newly added members are validated, so a record that still
	// carries a forged pre-existing entry can be healed by omitting it.
	if excluded := firstExcludedTeamMember(added); excluded != "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", excludedTeamMemberMessage(excluded),
			"Remove the excluded role from core_team — ADR-090 bars it from workspace teams"))
	}
	// #1057 F2: a newly added member must name a real agent.
	if refusal := teamMemberRefusal(u.t.deps, added); refusal != nil {
		return refusal
	}
	// omnipus-system-agent FR-003: Omnipus belongs to the default workspace
	// only — refused before any write, ahead of the generic built-in rule.
	if coreagent.OmnipusMembershipRefusal(u.w.IsDefault, u.oldTeam, u.w.CoreTeam) {
		return omnipusMembershipError()
	}
	// D8 (founder ruling 2026-10-11): built-ins cannot leave the default
	// workspace by ANY writer (ensureBuiltinRosterPresent re-adds them at the
	// next boot). An unreadable roster is not an empty one: fail closed.
	if u.w.IsDefault && len(teamDiffAdded(u.w.CoreTeam, u.oldTeam)) > 0 && !liveConfigAvailable(u.t.deps) {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"the live configuration is unavailable, so a team removal from the default workspace cannot be checked",
			"Retry once the gateway configuration is loaded"))
	}
	if err := workspacepkg.RefuseBuiltinRemoval(u.w.IsDefault, u.oldTeam, u.w.CoreTeam, configAgentIDs(u.t.deps)); err != nil {
		return tools.ErrorResult(errorJSON(strings.ToUpper(workspacepkg.BuiltinRemovalCode),
			workspacepkg.BuiltinRemovalMessage,
			"Built-in roster members rejoin the default workspace at every boot; remove them from other workspaces instead"))
	}
	// Removal cleanup (gate round 1 S6): dropped members are garbage-collected
	// out of member_configs, exactly as the REST PUT's GC does.
	if pruned, removed := workspacepkg.GCMemberConfigs(u.w.CoreTeam, u.w.MemberConfigs); len(removed) > 0 {
		u.w.MemberConfigs = pruned
	}
	return nil
}

// planDelegation computes the delegation graph this call writes (nil when it
// writes none), clears SetupPending when a team is installed, and validates
// every edge it would persist — all before any write.
func (u *workspaceUpdate) planDelegation() *tools.ToolResult {
	d := u.t.deps
	ceiling := workspaceDelegationDepthCeiling(d)
	explicit, present, err := parseWorkspaceDelegationArg(u.args, u.w.CoreTeam, ceiling)
	if err != nil {
		return tools.ErrorResult(errorJSON("INVALID_DELEGATION_EDGE", err.Error(), "Send a complete valid graph using current team members"))
	}
	added := teamDiffAdded(u.oldTeam, u.w.CoreTeam)
	switch {
	case present:
		// An explicit complete graph replaces the stored one verbatim, but
		// every member THIS call introduces still gets its ordinary self-edge
		// (session-core FR-014/015/016, founder ruling 2026-10-10).
		u.pendingDelegation = explicit
		if u.coreTeamChanged {
			var selfSeeded []workspacepkg.DelegationEdge
			u.pendingDelegation, selfSeeded = withNewMemberSelfEdges(explicit, added, ceiling,
				configAgentPresenceSet(d), selfEdgeExcludedAgentIDs(d))
			u.seedNote = seededEdgesSummary(selfSeeded, added)
		}
	case u.coreTeamChanged && len(added) > 0:
		// Auto-seed default edges for NEWLY added members only (ADR-037: the
		// per-workspace graph is the sole, fail-closed delegation authority),
		// read from and written to the delegation store, never the record.
		existing, storeOK := workspacepkg.LoadDelegation(d.Home, u.id)
		if !storeOK {
			// FAIL CLOSED: seeding over an unreadable edge list would drop the
			// operator's real graph.
			return tools.ErrorResult(errorJSON("DELEGATION_STORE_UNREADABLE",
				"workspace delegation record is unreadable — refusing to seed delegation edges over it",
				"Inspect (or remove) $OMNIPUS_HOME/entities/delegation/"+u.id+".json, then re-save the graph from the Team tab"))
		}
		seeded := seedDelegationEdgesForNewMembers(u.w.CoreTeam, added, existing, ceiling, configAgentPresenceSet(d), selfEdgeExcludedAgentIDs(d))
		if len(seeded) > 0 {
			u.pendingDelegation = append(append([]workspacepkg.DelegationEdge(nil), existing...), seeded...)
			u.seedNote = seededEdgesSummary(seeded, added)
		}
	}
	// Installing a non-empty team through this tool IS the setup completing,
	// so it clears SetupPending itself (fix wave finding #2); a plain rename or
	// an un-staffing never touches the flag.
	if u.coreTeamChanged && len(u.w.CoreTeam) > 0 && u.w.SetupPending {
		u.w.SetupPending = false
		u.setupNote = "workspace setup marked complete"
	}
	// Defensive whole-graph validation before the store is rewritten (security
	// boundary — fail closed): a corrupted on-disk edge from another writer is
	// never laundered by this write.
	if u.pendingDelegation != nil {
		team := workspaceDelegationTeamSet(u.w, u.pendingDelegation)
		for i := range u.pendingDelegation {
			if err := u.pendingDelegation[i].Validate(team, ceiling); err != nil {
				return tools.ErrorResult(errorJSON("INVALID_DELEGATION_EDGE", err.Error(),
					"Fix the workspace delegation graph via the Team tab or the delegation API"))
			}
		}
	}
	return nil
}

// commit writes the record (then the delegation store) and renders the
// read-back. A semantically empty update writes nothing and keeps the
// revision (#1057 F5).
func (u *workspaceUpdate) commit() *tools.ToolResult {
	home := u.t.deps.Home
	changed := workspaceChangedFields(u.state.Workspace, u.w)
	if u.pendingDelegation != nil && sameDelegationGraph(u.pendingDelegation, u.state.Delegation) {
		// Re-sending the saved graph is not a change (#1057 F5, architect M01).
		u.pendingDelegation = nil
	}
	if u.pendingDelegation != nil {
		changed = append(changed, "delegation")
	}
	_, teamSupplied := u.args["core_team"]
	if len(changed) == 0 {
		const noOpScope = "no field differs from the saved workspace; nothing was written"
		resp := workspaceView(u.state.Workspace, u.state.Delegation, u.state.Revision, computeWorkspaceTaskCount(home, u.id))
		resp["changed"] = false
		resp["changed_fields"] = []string{}
		// Re-sending the team repairs a member main chat a previous change
		// could not prepare (code-reviewer R2-02); nothing else is written.
		if teamSupplied && len(u.w.CoreTeam) > 0 {
			if warning := memberMainsWarning(u.t.deps, "update_workspace", u.id, u.w.CoreTeam); warning != "" {
				resp["main_chat_warning"] = warning
				return changeSuccess(resp, savedNotWorking(warning, noOpScope))
			}
		}
		return changeSuccess(resp, working(noOpScope))
	}
	u.w.UpdatedAt = nowISO()
	if err := writeEntity(workspacesDir(home), u.id, u.w); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	// Persist edges AFTER the record, so a failed record write can never leave
	// the store authorizing agents the record does not list on the team.
	if u.pendingDelegation != nil {
		if err := saveWorkspaceDelegation(home, u.id, u.pendingDelegation); err != nil {
			slog.Error("sysagent: update_workspace: delegation save failed after workspace write", "workspace_id", u.id, "error", err)
			actual, readErr := workspacepkg.ReadStateLocked(home, u.id)
			if readErr != nil {
				slog.Error("sysagent: update_workspace: failed to read partial state", "workspace_id", u.id, "error", readErr)
				return workspaceMutationError(u.id, "save_delegation", "partial", "", workspaceChangedFields(u.state.Workspace, u.w), map[string]any{"exists": true, "state_read_failed": true})
			}
			return workspaceMutationError(u.id, "save_delegation", "partial", actual.Revision, workspaceChangedFields(u.state.Workspace, actual.Workspace), workspaceActualState(actual))
		}
	}
	return workspaceWrittenResult(u.t.deps, u.id, workspaceWrite{
		changed: changed, seedNote: u.seedNote, setupNote: u.setupNote,
		beforeEdges: u.state.Delegation, ceiling: workspaceDelegationDepthCeiling(u.t.deps),
		provision: u.coreTeamChanged || teamSupplied, team: u.w.CoreTeam, toolName: "update_workspace",
	})
}

// sameDelegationGraph reports whether two delegation graphs hold the same
// edges, in any order.
func sameDelegationGraph(a, b []workspacepkg.DelegationEdge) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(edges []workspacepkg.DelegationEdge) []string {
		out := make([]string, 0, len(edges))
		for _, e := range edges {
			modes := make([]string, 0, len(e.Modes))
			for _, m := range e.Modes {
				modes = append(modes, string(m))
			}
			sort.Strings(modes)
			depth := "-"
			if e.Depth != nil {
				depth = fmt.Sprint(*e.Depth)
			}
			out = append(out, e.FromAgent+"\x00"+e.ToAgent+"\x00"+strings.Join(modes, ",")+"\x00"+depth)
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(key(a), key(b))
}

// updateWorkspaceFields applies optional record fields in input order.
func updateWorkspaceFields(w *workspacepkg.Workspace, args map[string]any) (bool, *tools.ToolResult) {
	coreTeamChanged := false

	if raw, present := args["name"]; present {
		v, ok := raw.(string)
		// #1057 F1: an explicit empty or whitespace name is refused like on
		// create; omitting name leaves it unchanged.
		if !ok || strings.TrimSpace(v) == "" {
			return false, tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required",
				"Omit name to keep the current one, or give a non-empty name"))
		}
		if len(v) > 200 {
			return false, tools.ErrorResult(errorJSON("INVALID_INPUT", "name exceeds 200 characters", ""))
		}
		w.Name = v
	}
	if v, ok := args["description"].(string); ok {
		if len(v) > 2000 {
			return false, tools.ErrorResult(errorJSON("INVALID_INPUT", "description exceeds 2000 characters", ""))
		}
		w.Description = v
	}
	if v, ok := args["status"].(string); ok {
		if v != "active" && v != "archived" {
			return false, tools.ErrorResult(errorJSON("INVALID_INPUT",
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
		w.CoreTeam = sanitizeCoreTeam(raw)
		coreTeamChanged = true
	}
	return coreTeamChanged, nil
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

func parseWorkspaceDelegationArg(args map[string]any, teamIDs []string, ceiling int) ([]workspacepkg.DelegationEdge, bool, error) {
	raw, present := args["delegation"]
	if !present {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, errors.New("delegation must be an array; null is not allowed")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, true, errors.New("delegation must be an array")
	}
	team := workspacepkg.TeamSet(teamIDs, nil)
	edges := make([]workspacepkg.DelegationEdge, 0, len(items))
	seen := make(map[string]int, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, true, errors.New("delegation entries must be objects")
		}
		from, _ := row["from_agent"].(string)
		to, _ := row["to_agent"].(string)
		edge := workspacepkg.DelegationEdge{FromAgent: strings.TrimSpace(from), ToAgent: strings.TrimSpace(to)}
		if rawModes, exists := row["modes"]; exists {
			values, ok := rawModes.([]any)
			if !ok {
				return nil, true, errors.New("delegation modes must be an array")
			}
			for _, value := range values {
				mode, ok := value.(string)
				if !ok {
					return nil, true, errors.New("delegation modes must be strings")
				}
				edge.Modes = append(edge.Modes, workspacepkg.DelegationMode(mode))
			}
		}
		if rawDepth, exists := row["depth"]; exists {
			depth, err := jsonInt(rawDepth, "delegation depth")
			if err != nil {
				return nil, true, errors.New("delegation depth must be an integer")
			}
			edge.Depth = &depth
		}
		if err := edge.Validate(team, ceiling); err != nil {
			return nil, true, err
		}
		key := edge.FromAgent + "\x00" + edge.ToAgent
		if index, duplicate := seen[key]; duplicate {
			edges[index] = edge
		} else {
			seen[key] = len(edges)
			edges = append(edges, edge)
		}
	}
	if delegationHasCycle(edges) {
		return nil, true, errors.New("delegation graph contains a cycle")
	}
	return edges, true, nil
}

// edgeModeCategory collapses a coreagent seed's 3-value delegate-tool call
// vocabulary (config.DelegationMode: task/background) down to the
// workspace trust-edge's 2-value vocabulary (workspace.DelegationMode:
// direct/task).
//
// This is a DELIBERATE, INDEPENDENT copy of agent.EdgeModeCategory
// (pkg/agent/loop.go), not a shared call: pkg/agent/loop.go already imports
// pkg/sysagent/tools (as systools), so the reverse import here would create
// an import cycle. pkg/gateway does NOT have a comparable independent copy —
// pkg/gateway already imports pkg/agent extensively, so its
// defaultWorkspaceDelegationEdges CALLS agent.EdgeModeCategory directly
// (pkg/gateway/rest_workspace_delegation.go) instead of replicating the
// collapse. The only other place this exact loop is replayed independently
// is the gateway's own parity test,
// TestDefaultWorkspaceDelegationEdges_MatchesCoreagentSeed
// (pkg/gateway/rest_workspace_delegation_test.go), which deliberately
// re-derives the expected edge set without going through
// defaultWorkspaceDelegationEdges so it can catch a regression in that
// function's transformation logic. Keep this in lock-step with
// agent.EdgeModeCategory if that mapping ever changes.
func edgeModeCategory(mode config.DelegationMode) workspacepkg.DelegationMode {
	switch mode {
	case config.DelegationModeTask:
		return workspacepkg.ModeTask
	case config.DelegationModeBackground:
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

// ACCEPTED DESIGN (operator decision, 2026-08-23): update_workspace being
// agent-drivable is intentional and is NOT the #636 vulnerability.
//
// #636 was that the delegation edge list lived in a file a sandboxed child
// could append to directly, bypassing every gate. That is fixed — the list
// now lives in entities/, which fspolicy denies unconditionally on every
// turn, and the workspace record can no longer carry it at all.
//
// This path is a different thing and is deliberately allowed: an agent with
// the update_workspace tool can grow the graph, but ONLY by replaying the
// compiled-in coreagent.SeedDelegationEdges matrix among agents already on
// the team and already present in the live config. It cannot invent an
// arbitrary edge, name an off-team agent, or widen a depth cap beyond the
// compiled seed. The control is the tool policy governing update_workspace,
// which is an explicit per-agent grant with no default fallback
// (CLAUDE.md Hard Constraint #6).
//
// Do not "harden" this into an operator-only path without an ADR — it would
// break workspace self-assembly, which is the feature it exists for.
//
// seedDelegationEdgesForNewMembers derives the default delegation edges an
// update_workspace core_team change should auto-add for NEWLY ADDED members.
// Mirrors pkg/gateway's defaultWorkspaceDelegationEdges: for every agent in
// newTeam with a compiled-in coreagent.SeedDelegationEdges seed, each seeded
// target becomes a candidate edge, with modes collapsed/deduped via the
// local edgeModeCategory. Non-self depth is copied from the seed policy;
// self-edges (for any agent — the seed gives every agent except the hidden
// System Agents a self-edge) are pinned by coreagent.SeededEdgeDepth to
// min(3, ceiling) (ADR-090 FR-006).
//
// A candidate edge is included iff ALL of:
//   - both endpoints are members of newTeam (an edge never reaches outside
//     the team being saved);
//   - both endpoints are present in the live agent config (configPresent,
//     built from Deps.GetCfg().Agents.List — see configAgentPresenceSet).
//     newTeam is caller-supplied (a workspace's stored core_team, or the
//     tool's raw core_team argument) and is never itself validated against
//     the roster, while coreagent.SeedDelegationEdges keys off a compiled
//     constant ID string ("jim", "mia", ...) rather than looking
//     the agent up in cfg — so without this check a core_team entry that
//     names a deleted-but-still-seed-keyed agent (or a stale/typo'd ID that
//     happens to collide with a seed key) would still produce a persisted
//     edge naming a nonexistent agent. Mirrors the gateway's own
//     presence-map pattern (defaultWorkspaceDelegationEdges iterates
//     cfg.Agents.List directly for its `from` side);
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
//
// Two deliberate semantics worth calling out explicitly:
//
//  1. Remove-then-re-add RE-SEEDS. If a member is removed from core_team in
//     one update and re-added in a later one, it is — from this function's
//     point of view — indistinguishable from a brand-new addition: its
//     default edges are seeded again (bounded to the compiled matrix, and
//     only for edges whose OTHER endpoint is currently on the team). This is
//     intentional, not a gap the "never resurrect a removed edge" rule
//     above is supposed to close — that rule only protects edges among
//     members that stayed on the team the whole time. Re-adding a member is
//     itself an affirmative action visible in the Team tab, so re-seeding
//     its defaults is the expected, reviewable outcome.
//
//  2. The REST PUT path (pkg/gateway's handleWorkspaceDelegationPut) does
//     NOT call this or any equivalent auto-seed logic. That endpoint is the
//     Team tab UI's save path: the UI reads the CURRENT graph, lets a human
//     edit it, and PUTs back the COMPLETE edge list it computed — auto-seeding
//     there would mean the tool silently reintroducing edges the human just
//     deleted in the same save, or duplicating ones they kept. Auto-seeding
//     is scoped to update_workspace (this tool, primarily driven by Ava's
//     workspace-setup interview) and workspace creation
//     (defaultWorkspaceDelegationEdges) — the two paths that only ever GROW
//     a team programmatically and have no "human just edited the graph
//     directly" step to clobber.
func seedDelegationEdgesForNewMembers(
	newTeam []string,
	added []string,
	existing []workspacepkg.DelegationEdge,
	ceiling int,
	configPresent map[string]bool,
	excluded map[string]bool,
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

	// Ordinary self-rows for every NEWLY introduced member (session-core
	// C-DELEGATE FR-014/015): the same shared workspace-graph computation the
	// install seed and create_agent's join use, so an agent added later gets the
	// identical ordinary {id→id} row. Only members in `added` are introduced —
	// a continuing member whose self-row an operator removed is never
	// resurrected (deletion is authoritative), and SelfEdgeSeedRows also skips
	// any id already self-edged in `existing`.
	//
	// `excluded` is the OPERATOR seed data
	// (config.workspace_seed_defaults.self_edge.exclude_agent_ids, resolved by
	// the caller via selfEdgeExcludedAgentIDs and passed in as a set so this
	// package need not import config). The exclusion is DATA, never a Go
	// identity predicate: an operator adding an ordinary id such as `mia` takes
	// effect here with no code change, and an explicit empty list excludes
	// nobody. It is NOT limited to the hidden system agents — the shipped
	// default merely happens to name them.
	var introduced []string
	for _, id := range newTeam {
		if configPresent[id] && addedSet[id] {
			introduced = append(introduced, id)
		}
	}
	out = append(out, workspacepkg.SelfEdgeSeedRows(introduced, existing, excluded, ceiling)...)
	for _, e := range out {
		present[e.FromAgent+"\x00"+e.ToAgent] = true
	}

	for _, from := range newTeam {
		if !configPresent[from] {
			continue
		}
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
		for _, ref := range dp.To {
			// Self refs are supplied generically above; skip so a policy self ref
			// does not duplicate the ordinary self-row.
			if ref.Kind != config.AgentRefKindLocal || ref.ID == "*" || ref.ID == from {
				continue
			}
			to := ref.ID
			if !teamSet[to] {
				continue
			}
			if !configPresent[to] {
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
				Depth:     coreagent.SeededEdgeDepth(from, to, dp.Depth, ceiling),
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

// withNewMemberSelfEdges returns graph with the ordinary self-edge of every id in
// added appended (and that appended slice on its own), for a call that carries
// an EXPLICIT complete delegation graph. An explicit graph suppresses default
// seeding, but session-core FR-014/015/016 (founder ruling 2026-10-10) makes
// the self-edge unconditional for a member a call introduces, so this runs the
// one shared workspace.SelfEdgeSeedRows computation over the introduced
// members that are present in the live config (configPresent) and not on the
// operator exclusion data (excluded). Members already carrying a self-row in
// graph are skipped; continuing members are not in added, so a self-line the
// graph omits for them is never restored. A nil/empty added returns graph as
// sent.
func withNewMemberSelfEdges(
	graph []workspacepkg.DelegationEdge,
	added []string,
	ceiling int,
	configPresent map[string]bool,
	excluded map[string]bool,
) (merged, seeded []workspacepkg.DelegationEdge) {
	var introduced []string
	for _, id := range added {
		if configPresent[id] {
			introduced = append(introduced, id)
		}
	}
	seeded = workspacepkg.SelfEdgeSeedRows(introduced, graph, excluded, ceiling)
	if len(seeded) == 0 {
		return graph, nil
	}
	return append(append([]workspacepkg.DelegationEdge(nil), graph...), seeded...), seeded
}

// configAgentPresenceSet returns the set of agent IDs present in the live
// config's Agents.List, for use as seedDelegationEdgesForNewMembers's
// configPresent filter. Mirrors the gateway's presence-map pattern: pkg/gateway's
// defaultWorkspaceDelegationEdges iterates cfg.Agents.List directly for its
// `from` side, so a from-agent absent from the live roster can never seed a
// stale edge there. seedDelegationEdgesForNewMembers instead iterates the
// caller-supplied newTeam (a workspace's core_team, which is never itself
// validated against the roster), so without this presence check a stale or
// typo'd core_team entry that happens to collide with a compiled
// coreagent.SeedDelegationEdges key ("jim", "mia", ...) could still
// seed an edge naming an agent that no longer exists in config.
//
// A nil Deps/GetCfg/Config (test scaffolding without a wired config) yields
// an empty set — every candidate is excluded rather than trusting an unknown
// roster, matching this codebase's "no default-policy fallback" convention
// (CLAUDE.md Hard Constraint #6): presence is explicit, seeded data, never a
// permissive code branch.
// delegationHasCycle reports whether the graph has a cycle between distinct
// agents (a self-row is not a cycle).
func delegationHasCycle(edges []workspacepkg.DelegationEdge) bool {
	adj := make(map[string][]string)
	for _, edge := range edges {
		if edge.FromAgent != edge.ToAgent {
			adj[edge.FromAgent] = append(adj[edge.FromAgent], edge.ToAgent)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var cycle func(string) bool
	cycle = func(node string) bool {
		if visiting[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visiting[node] = true
		for _, next := range adj[node] {
			if cycle(next) {
				return true
			}
		}
		visiting[node] = false
		visited[node] = true
		return false
	}
	for node := range adj {
		if cycle(node) {
			return true
		}
	}
	return false
}

func configAgentPresenceSet(d *Deps) map[string]bool {
	present := make(map[string]bool)
	if d == nil || d.GetCfg == nil {
		return present
	}
	cfg := d.GetCfg()
	if cfg == nil {
		return present
	}
	for i := range cfg.Agents.List {
		present[cfg.Agents.List[i].ID] = true
	}
	return present
}

// liveConfigAvailable reports whether the live config can be read at all.
func liveConfigAvailable(d *Deps) bool {
	return d != nil && d.GetCfg != nil && d.GetCfg() != nil
}

// configAgentIDs returns the live config's agent ids — the same data
// configAgentPresenceSet reads, as a slice — for
// workspacepkg.DefaultWorkspaceTeamIDs, the shared D8 roster derivation both
// workspace writers feed RefuseBuiltinRemoval from.
func configAgentIDs(d *Deps) []string {
	present := configAgentPresenceSet(d)
	ids := make([]string, 0, len(present))
	for id := range present {
		ids = append(ids, id)
	}
	return ids
}

// selfEdgeExcludedAgentIDs resolves the operator's self-edge seed exclusion
// set (config.workspace_seed_defaults.self_edge.exclude_agent_ids) for the
// sysagent seed writers. It is the ONE resolver the workspace create/update
// producers and create_agent's membership join read, so every writer honours
// the same operator data — never a Go identity predicate.
//
// A missing/nil live config (deps not wired, or a nil *Config) is treated as
// "unset", which resolves to the SHIPPED default list (the hidden system agents
// judge/plansupervisor) rather than an empty set: an empty set would mean
// "exclude nobody" and seed the very agents the shipped default bars. An
// explicit operator `[]` is a present-but-empty list and DOES exclude nobody —
// that distinction lives in config.SelfEdgeExcludeAgentIDSet, which this defers
// to.
func selfEdgeExcludedAgentIDs(d *Deps) map[string]bool {
	if d != nil && d.GetCfg != nil {
		return d.GetCfg().SelfEdgeExcludeAgentIDSet()
	}
	// nil *Config resolves to the shipped default (SelfEdgeExcludeAgentIDSet is
	// nil-receiver-safe).
	return (*config.Config)(nil).SelfEdgeExcludeAgentIDSet()
}

// workspaceDelegationDepthCeilingFallback mirrors the gateway fallback used
// when performance.max_delegation_depth is unset. Kept in lock-step so the
// tool and gateway validate edges against the same ceiling.
const workspaceDelegationDepthCeilingFallback = 10

// workspaceDelegationDepthCeiling returns the effective delegation depth
// ceiling from the live config, mirroring gateway.delegationDepthCeiling:
// performance.max_delegation_depth when set (> 0), otherwise 10. A nil GetCfg
// (tests without a wired config) yields the fallback.
func workspaceDelegationDepthCeiling(d *Deps) int {
	if d == nil || d.GetCfg == nil {
		return workspaceDelegationDepthCeilingFallback
	}
	cfg := d.GetCfg()
	if cfg != nil {
		depth, valid := config.ConfiguredMaxDelegationDepth(cfg.Performance, "sysagent workspace delegation edge validation")
		if !valid {
			// Same rule as gateway.delegationDepthCeiling: fail closed.
			return 0
		}
		if depth > 0 {
			return depth
		}
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
//
// edges is the workspace's delegation edge list as loaded from the DELEGATION
// STORE (workspacepkg.LoadDelegation), never a field on the workspace record —
// the record is writable by the sandboxed child the delegation decision
// constrains, so deriving team membership from it would let that child widen
// its own team. See pkg/workspace/delegationstore.go.
func workspaceDelegationTeamSet(w workspace, edges []workspacepkg.DelegationEdge) map[string]bool {
	return workspacepkg.TeamSet(w.CoreTeam, edges)
}

// ---- system.workspace.delete ----

type WorkspaceDeleteTool struct{ deps *Deps }

func NewWorkspaceDeleteTool(d *Deps) *WorkspaceDeleteTool { return &WorkspaceDeleteTool{deps: d} }
func (t *WorkspaceDeleteTool) Name() string               { return "delete_workspace" }
func (t *WorkspaceDeleteTool) Scope() tools.ToolScope     { return tools.ScopeCore }
func (t *WorkspaceDeleteTool) Description() string {
	return "Delete a workspace. This is IRREVERSIBLE and destroys more than the workspace record: its GTD tasks, mount grants, delegation graph, and workspace directory are permanently removed; workflow-status tasks that still reference it become orphans. Read it with get_workspace first. Parameters: id, revision from that read, and confirm:true are all required."
}

func (t *WorkspaceDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string", "description": "Workspace ID from list_workspaces"},
			"revision": map[string]any{"type": "string", "description": "Revision returned by get_workspace"},
			"confirm": map[string]any{
				"type":        "boolean",
				"description": "Must be true to confirm irreversible deletion",
			},
		},
		"required": []string{"id", "revision", "confirm"},
	}
}

func (t *WorkspaceDeleteTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	id, _ := args["id"].(string)
	revision, _ := args["revision"].(string)
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

	// Serialize the read-check-cascade-remove cycle against this workspace ID
	// (workspace.LockID), mirroring the gateway's handleWorkspaceDelete: a
	// racing kickoff consume, PUT, or delegation-PUT must not be able to
	// read/write the workspace file while this irreversible delete is in
	// flight. Released explicitly (no defer) the moment the authoritative
	// workspace JSON is gone (Step 2) — Step 3's directory sweep is
	// best-effort cleanup that never touches workspaces/{id}.json, so
	// holding the lock across a potentially large recursive os.RemoveAll
	// buys no correctness benefit and would needlessly block a
	// shard-colliding kickoff's WS readLoop. Mirrors the same lock-scoping
	// restructure in the gateway's handleWorkspaceDelete.
	unlock := workspacepkg.LockID(id)

	// Guard: verify the workspace exists before any irreversible mutations.
	state, err := workspacepkg.CheckRevisionLocked(t.deps.Home, id, revision)
	if err != nil {
		unlock()
		return workspaceRevisionError(id, err)
	}
	if refusal := workspacepkg.RefuseDefaultDeletion(state.Workspace.IsDefault); refusal != nil {
		unlock()
		return tools.ErrorResult(errorJSON("CONFLICT", refusal.Error(), ""))
	}
	changedChannels, cleanupErr := unbindWorkspaceChannels(t.deps, id)
	if cleanupErr != nil {
		unlock()
		return workspaceDeleteChannelFailure(id, changedChannels,
			"could not disable and unbind workspace channels; workspace was not deleted: "+cleanupErr.Error())
	}

	// Step 1: cascade-delete tasks
	tasks, err := listEntities[unifiedTask](tasksDir(t.deps.Home))
	if err != nil {
		unlock()
		slog.Error("sysagent: workspace cascade delete: failed to list tasks", "workspace_id", id, "error", err)
		return workspaceDeleteChannelFailure(id, changedChannels, "could not list tasks for cascade delete: "+err.Error())
	}
	tasksDeleted := 0
	var cleanupFailures []string
	for _, tk := range tasks {
		if !isValidTaskStatus(string(tk.Status)) {
			continue
		}
		if tk.WorkspaceID == id {
			if err := deleteEntity(tasksDir(t.deps.Home), tk.ID); err != nil {
				slog.Warn("sysagent: workspace cascade delete: failed to delete task",
					"workspace_id", id, "task_id", tk.ID, "error", err)
				cleanupFailures = append(cleanupFailures, "task "+tk.ID)
			} else {
				tasksDeleted++
			}
		}
	}

	// Step 2: delete the workspace file. This is the authoritative delete —
	// still under the lock.
	if err := deleteEntity(workspacesDir(t.deps.Home), id); err != nil {
		unlock()
		if tasksDeleted > 0 {
			return changeErrorWithOutcome(map[string]any{
				"success": false,
				"error":   map[string]any{"code": "SAVE_FAILED", "message": err.Error()},
				"id":      id, "tasks_deleted": tasksDeleted,
				"channels_disabled_and_unbound": append([]string{}, changedChannels...),
			}, partlySaved(fmt.Sprintf("%d task(s) were deleted but the workspace record was not: %v", tasksDeleted, err),
				"", []string{"workspace record deletion"}))
		}
		return workspaceDeleteChannelFailure(id, changedChannels, err.Error())
	}

	// The workspace file is gone — release the lock now. Step 3 below is
	// best-effort cascade cleanup that does not touch workspaces/{id}.json.
	unlock()

	// Step 3: best-effort cascade of the mount store. Mounts live in
	// entities/mounts/<id>.json (out of a sandboxed child's reach — see
	// pkg/workspace/mountstore.go), NOT under the workspace directory, so the
	// RemoveAll below does not reach them. Removing the record removes only the
	// record of the grants; the operator's real folders are never touched
	// (FR-8.6). Runs after unlock — DeleteMountStore takes LockID itself, and
	// this pool is not reentrant.
	if err := workspacepkg.DeleteMountStore(t.deps.Home, id); err != nil {
		slog.Warn("sysagent: workspace cascade delete: failed to remove mount store",
			"workspace_id", id, "error", err)
		cleanupFailures = append(cleanupFailures, "mount store")
	}

	// Step 3b: best-effort cascade of the delegation store. Delegation edges
	// live in entities/delegation/<id>.json (out of a sandboxed child's reach —
	// see pkg/workspace/delegationstore.go), NOT under the workspace directory,
	// so the RemoveAll below does not reach them. Runs after unlock for the
	// same reason as the mount store: DeleteDelegationStore takes LockID itself
	// and this pool is not reentrant.
	if err := workspacepkg.DeleteDelegationStore(t.deps.Home, id); err != nil {
		slog.Warn("sysagent: workspace cascade delete: failed to remove delegation store",
			"workspace_id", id, "error", err)
		cleanupFailures = append(cleanupFailures, "delegation store")
	}

	// Step 4: best-effort cascade of per-workspace directory (AGENT.md / memory room).
	// The JSON removal above is the authoritative delete; a stale directory is not fatal.
	wsDir := workspacepkg.WorkspaceDir(t.deps.Home, id)
	if err := os.RemoveAll(wsDir); err != nil {
		slog.Warn("sysagent: workspace cascade delete: failed to remove workspace dir",
			"workspace_id", id, "dir", wsDir, "error", err)
		cleanupFailures = append(cleanupFailures, "workspace folder")
	}

	changedFields := []string{"workspace"}
	scope := "workspace record deleted; its tasks, mount store, delegation store and folder removed"
	if len(changedChannels) > 0 {
		changedFields = append(changedFields, "channels")
		scope += "; bound channel configuration disabled and unbound"
	}
	resp := map[string]any{
		"id": id, "deleted": true, "tasks_deleted": tasksDeleted,
		"changed_fields":                changedFields,
		"channels_disabled_and_unbound": append([]string{}, changedChannels...),
	}
	if len(cleanupFailures) > 0 {
		return changeSuccess(resp, partlySaved("the workspace is deleted but some of its data could not be removed",
			scope, cleanupFailures))
	}
	return changeSuccess(resp, working(scope))
}

// ---- system.workspace.list ----

type WorkspaceListTool struct{ deps *Deps }

func NewWorkspaceListTool(d *Deps) *WorkspaceListTool { return &WorkspaceListTool{deps: d} }
func (t *WorkspaceListTool) Name() string             { return "list_workspaces" }
func (t *WorkspaceListTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *WorkspaceListTool) Description() string {
	return "List workspaces with their live task counts. Call this to find workspace ids to use with create_task, list_tasks (their optional workspace_id), get_workspace, or update_workspace. Ordering is: the default workspace first, then pinned workspaces by pin_order, then the rest newest-first — so the first result is not necessarily the most recently created one. Returns id, name, description, status, pinned, pin_order, is_default, core_team and task_count. Any workspace file that cannot be read is skipped and counted in `skipped`.\nParameters: status (optional: active/archived/all, defaults to active)."
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
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return tools.ErrorResult(errorJSON("LIST_FAILED", err.Error(), ""))
	}

	var workspaces []workspace
	skipped := 0
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < 6 || e.Name()[len(e.Name())-5:] != ".json" {
			continue
		}
		wid := e.Name()[:len(e.Name())-5]
		data, err := os.ReadFile(entityPath(dir, wid))
		if err != nil {
			slog.Warn("sysagent: skipping unreadable workspace file", "file", e.Name(), "error", err)
			skipped++
			continue
		}
		w, err := workspaceFromFile(data)
		if err != nil {
			slog.Warn("sysagent: skipping corrupt workspace file", "file", e.Name(), "error", err)
			skipped++
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
		Description string   `json:"description"`
		Status      string   `json:"status"`
		Pinned      bool     `json:"pinned"`
		PinOrder    int      `json:"pin_order"`
		IsDefault   bool     `json:"is_default"`
		CoreTeam    []string `json:"core_team"`
		// Delegation is the workspace's graph (#1057 F6: the same field every
		// workspace view carries, [] when empty). DelegationUnreadable marks
		// a graph that could not be read, so [] is never mistaken for "none".
		Delegation           []workspacepkg.DelegationEdge `json:"delegation"`
		DelegationUnreadable bool                          `json:"delegation_unreadable,omitempty"`
		TaskCount            int                           `json:"task_count"`
		CreatedAt            string                        `json:"created_at"`
		UpdatedAt            string                        `json:"updated_at"`
	}
	resp := make([]workspaceResponse, 0, len(workspaces))
	for _, w := range workspaces {
		if w.CoreTeam == nil {
			w.CoreTeam = []string{} // #1057 F6: lists are never null
		}
		edges, edgesOK := workspacepkg.LoadDelegation(t.deps.Home, w.ID)
		if edges == nil {
			edges = []workspacepkg.DelegationEdge{}
		}
		resp = append(resp, workspaceResponse{
			Delegation:           edges,
			DelegationUnreadable: !edgesOK,
			ID:                   w.ID,
			Name:                 w.Name,
			Description:          w.Description,
			Status:               w.Status,
			Pinned:               w.Pinned,
			PinOrder:             w.PinOrder,
			IsDefault:            w.IsDefault,
			CoreTeam:             w.CoreTeam,
			TaskCount:            counts[w.ID],
			CreatedAt:            w.CreatedAt,
			UpdatedAt:            w.UpdatedAt,
		})
	}
	out := map[string]any{"workspaces": resp}
	if skipped > 0 {
		out["skipped"] = skipped
	}
	return tools.NewToolResult(successJSON(out))
}

// ---- system.workspace.get ----

type WorkspaceGetTool struct{ deps *Deps }

func NewWorkspaceGetTool(d *Deps) *WorkspaceGetTool { return &WorkspaceGetTool{deps: d} }
func (t *WorkspaceGetTool) Name() string            { return "get_workspace" }
func (t *WorkspaceGetTool) Scope() tools.ToolScope  { return tools.ScopeCore }
func (t *WorkspaceGetTool) Description() string {
	// The opening line MUST stay under maxManifestLineLen (140 runes,
	// pkg/tools/manifest.go) — get_workspace is a Tier-2 (previewed) tool,
	// so this first line (up to the first '\n') is what actually renders in
	// the compressed manifest block on EVERY turn. manifest.go splits on the
	// first newline and shows only the opening sentence there; the fuller
	// detail below is still available via ToolSearch/the tool's own
	// Parameters(), just not in the always-visible preview line. See
	// TestVisibility_PreviewedDescriptionsFitWithoutTruncation (internal,
	// pkg/tools) and TestVisibility_ScopeCoreGetWorkspaceDescriptionFitsWithoutTruncation
	// (external, pkg/tools/manifest_scopecore_test.go) which both guard this.
	return "Get a workspace by ID, including its authoritative team, delegation graph, revision, and live task count.\n" +
		"Use this before every workspace mutation and for readback. Returns id, name, description, status, pinned, pin_order, is_default, core_team, delegation, revision, task_count, created_at, and updated_at.\nParameters: id (required, from list_workspaces)."
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
	state, err := workspacepkg.ReadState(t.deps.Home, id)
	if err != nil {
		return workspaceReadError(id, err)
	}
	return tools.NewToolResult(successJSON(workspaceView(state.Workspace, state.Delegation, state.Revision,
		computeWorkspaceTaskCount(t.deps.Home, id))))
}
