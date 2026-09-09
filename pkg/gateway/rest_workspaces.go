// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// errWorkspaceNotFound is returned by readWorkspaceFile when the workspace file
// does not exist on disk. Callers use errors.Is(err, errWorkspaceNotFound).
var errWorkspaceNotFound = errors.New("workspace not found")

// removeAllFn indirects os.RemoveAll so tests can inject a deterministic
// failure. A chmod-based test (make the parent dir r-x so its contents cannot
// be unlinked) only produces EACCES for an unprivileged uid — CI runs as root,
// which bypasses DAC permission checks entirely, so RemoveAll there succeeds and
// the handler correctly returns 204. That made the delete-failure regression
// test pass locally (uid 1000) and fail in CI. Mirrors the same seam in
// pkg/session/unified.go.
var removeAllFn = os.RemoveAll

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

// repositoryRetiredMsg is returned (400) whenever a caller's raw request body
// carries a "repository" field on POST/PUT /api/v1/workspaces (FR-9.2,
// ADR-063 D7). Workspace.repository is deleted from the wire, storage and the
// sysagent tool with no back-compat (FR-9.1) — a caller still sending it must
// get a loud 400, not a silent drop. Git linkage is now a convenience on top
// of mounting (FR-9.3): clone the URL to an operator-chosen location, then
// mount that folder into the workspace.
const repositoryRetiredMsg = "repository is retired — clone the git URL to a location of your choosing, then mount that folder into the workspace"

// maxWorkspaceBodyBytes caps the buffered body these guards read.
//
// The previous code truncated at this size and then REPLACED r.Body with the
// truncated copy, so a body over the limit silently became malformed JSON and
// the caller reported a parse error for a request that was merely large. The
// limit is kept — an unbounded read here is a trivial memory amplifier — but a
// body that hits it is now reported as too large, by its own message, rather
// than corrupted into a different error.
const maxWorkspaceBodyBytes = 1 << 20 // 1 MiB

// rejectRetiredRepositoryField reads the full request body, 400s when it
// contains a "repository" field (FR-9.2), and — on success — restores r.Body
// so the caller's normal decode is unaffected. Returns false (and has already
// written the error response) when the body could not be read or carried the
// retired field; true means the caller may proceed with decoding.
// Mirrors the sandbox_profile/delegation_policy raw-body-sniff precedent in
// pkg/gateway/rest.go's updateAgent (ADR-035 §7 / ADR-037).
func rejectRetiredRepositoryField(w http.ResponseWriter, r *http.Request) bool {
	return rejectTopLevelField(w, r, "repository", repositoryRetiredMsg)
}

// mountsNotWritableHereMsg is returned (400) whenever a caller's raw request
// body carries a "mounts" field on POST/PUT /api/v1/workspaces.
// WorkspaceMount.yaml/Workspace.yaml's own schema comment is explicit that
// mounts are "created and removed via the dedicated mounts lifecycle, not
// via this record's own create/update requests" — gen.WorkspaceCreateRequest
// and gen.WorkspaceUpdateRequest carry no "mounts" field at all, so without
// this raw-body sniff a client sending one would have it silently dropped by
// Go's default JSON decode rather than getting a loud 400 (same rationale as
// rejectRetiredRepositoryField above, mirroring the sandbox_profile /
// delegation_policy / repository precedents).
const mountsNotWritableHereMsg = "mounts cannot be created, changed, or removed via POST/PUT /api/v1/workspaces — use the dedicated mounts lifecycle"

// rejectMountsWriteField is rejectRetiredRepositoryField's sibling for the
// "mounts" field. Kept as a separate function (rather than folding into
// rejectRetiredRepositoryField) so each retired/reserved field gets its own
// named message and its own call site is self-documenting about which field
// it guards; both are cheap raw-body substring sniffs on the same buffered
// body.
func rejectMountsWriteField(w http.ResponseWriter, r *http.Request) bool {
	return rejectTopLevelField(w, r, "mounts", mountsNotWritableHereMsg)
}

// rejectTopLevelField 400s when the request body carries `field` as a TOP-LEVEL
// KEY, and restores r.Body either way so the caller's normal decode is
// unaffected.
//
// It decodes into map[string]json.RawMessage rather than sniffing the raw bytes
// for `"field"`. A substring match cannot tell a key from a VALUE, so
// POST /workspaces {"name":"repository"} — a perfectly reasonable workspace name
// — was rejected with "repository is retired; clone the git URL…", which is
// nonsense for the request actually made. The same held for a description or a
// core_team entry equal to either word.
//
// A body that is not a JSON object at all is passed through untouched: rejecting
// it here would pre-empt the caller's own decode, which produces a better error
// for that case than this guard can.
func rejectTopLevelField(w http.ResponseWriter, r *http.Request, field, msg string) bool {
	// +1 so a body EXACTLY at the cap is distinguishable from one over it.
	rawBody, readErr := io.ReadAll(io.LimitReader(r.Body, maxWorkspaceBodyBytes+1))
	if readErr != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	if len(rawBody) > maxWorkspaceBodyBytes {
		jsonErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))

	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &top); err != nil {
		// Not an object (or malformed) — let the caller's decode report it.
		return true
	}
	if _, present := top[field]; present {
		jsonErr(w, http.StatusBadRequest, msg)
		return false
	}
	return true
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
// deserialises to a valid task (status in the 6-state vocabulary).
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
//
// Delegates to workspace.SaveRecord. This function used to be a byte-for-byte
// duplicate of it — same path, marshalling, flock and atomic write — which is
// the shape that lets two writers of one file drift apart. Kept as a named
// function because the call sites read better with it and it documents where
// the gateway's workspace writes go.
func writeWorkspaceFile(home string, w storedWorkspace) error {
	return workspace.SaveRecord(home, w)
}

// wireMount is a type ALIAS (not a new named type — the "=" form) for the
// exact anonymous struct shape oapi-codegen generated for gen.Workspace's
// Mounts field. Even though contracts/components/schemas/Workspace.yaml
// references WorkspaceMount.yaml via $ref, oapi-codegen inlined it as an
// anonymous struct rather than emitting a named gen.WorkspaceMount type (no
// such type exists in pkg/api/generated) — so this alias, copied field-for-
// field and tag-for-tag from openapi_types.gen.go, is the only way to
// construct a value assignable to gen.Workspace.Mounts (*[]struct{...})
// without hand-rolling a parallel, possibly-drifting wire struct. This is
// NOT a new hand-written wire type under Constraint #8 — it is an alias of
// the generated one; a `go vet`/lint mismatch here (wrong field/tag) would
// fail to compile against gen.Workspace.Mounts, not silently drift.
type wireMount = struct {
	HostPath string                     `json:"host_path"`
	Name     string                     `json:"name"`
	Status   *gen.WorkspaceMountsStatus `json:"status,omitempty"`
}

// mountsToWire loads workspace id's mounts from the MOUNT STORE and converts
// them to the wire shape, computing each entry's live status
// (workspace.MountStatus, FR-8.2/FR-8.5) at response-build time — mirrors
// task_count's own "computed at read time, never stored" convention
// (gen.Workspace's own doc comment). Returns nil when there are no mounts so
// the field is omitted entirely (json:"mounts,omitempty"), matching
// WorkspaceMount.yaml's "Absent when no mount exists" contract.
//
// The source is workspace.LoadMounts, NOT a field on the workspace record —
// mounts are write grants and were moved out of that (child-writable) record;
// see pkg/workspace/mountstore.go. Mounts stay on the wire unchanged: only
// where the server reads them from changed, so contracts/ is untouched.
func mountsToWire(home, id string) *[]wireMount {
	mounts, ok := workspace.LoadMounts(home, id)
	if !ok || len(mounts) == 0 {
		return nil
	}
	out := make([]wireMount, len(mounts))
	for i, m := range mounts {
		status := gen.WorkspaceMountsStatus(workspace.MountStatus(m))
		out[i] = wireMount{HostPath: m.HostPath, Name: m.Name, Status: &status}
	}
	return &out
}

// workspaceToWire converts a storedWorkspace to the generated gen.Workspace wire type.
// taskCount is passed in (computed by the caller); home is $OMNIPUS_HOME, needed
// because mounts no longer live on the record and are loaded from their own
// store (see mountsToWire).
func workspaceToWire(home string, w storedWorkspace, taskCount int) gen.Workspace {
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
	if w.SetupPending {
		sp := true
		wire.SetupPending = &sp
	}
	if w.Description != "" {
		wire.Description = &w.Description
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
	// FR-5/FR-8.2: mounts, with each entry's status computed live (never
	// stored — see mountsToWire's doc comment).
	wire.Mounts = mountsToWire(home, w.ID)
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
// full install roster (every agent coreagent delivers — 4 base + Worker +
// Planner/Explorer/Researcher) and the default delegation edges derived from
// the seeded per-agent trust graph (M5/M6), including Jim/Mia/Ava/Ray→Worker.
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
			// FR-008/US-3 AS-4: an upgraded install whose default workspace
			// already exists still needs the built-in roster back-filled
			// (e.g. a coreagent added after the operator's install) — but
			// must NEVER retroactively auto-add a custom/user-created agent.
			// Best-effort: a back-fill failure is logged, not fatal — the
			// gateway continues booting on the pre-existing default workspace.
			if berr := ensureBuiltinRosterPresent(home, w, cfg); berr != nil {
				slog.Warn("rest: ensureDefaultWorkspace: failed to back-fill built-in roster",
					"workspace_id", w.ID, "error", berr)
			}
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
	// and edges stay consistent (no edge to an agent not on the team). With the
	// full install roster (including Worker) this keeps Jim→Worker and the other
	// coreagent seed edges; seedEdgesForTeam still drops edges if a lite install
	// omitted an endpoint agent from config.
	seedEdges := seedEdgesForTeam(defaultWorkspaceDelegationEdges(cfg), ws.CoreTeam)
	// Defense-in-depth: validate each seeded edge so no unvalidated edge is ever
	// persisted. The source is the trusted compiled-in roster so failures are
	// unexpected; on failure log WARN and drop the offending edge rather than
	// hard-failing boot over a seed-config issue.
	// The workspace is brand new, so its delegation store is empty and the team
	// set is core_team alone.
	team := workspace.TeamSet(ws.CoreTeam, nil)
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
	if err := writeWorkspaceFile(home, ws); err != nil {
		return fmt.Errorf("ensureDefaultWorkspace: write: %w", err)
	}
	// Seed edges go to the delegation store, never onto the record — see
	// pkg/workspace/delegationstore.go. This is a hard failure: a boot that
	// silently produced a default workspace with no delegation graph would look
	// healthy and then deny every delegation at runtime.
	if err := saveWorkspaceDelegation(home, ws.ID, validEdges); err != nil {
		return fmt.Errorf("ensureDefaultWorkspace: seed delegation: %w", err)
	}
	slog.Info("rest: default workspace auto-created",
		"id", ws.ID, "owner", ownerUsername,
		"team_size", len(ws.CoreTeam), "edge_count", len(validEdges))
	return nil
}

// saveWorkspaceDelegation persists a workspace's delegation edge set to the
// delegation store, taking the per-workspace lock SaveDelegation requires its
// caller to hold. Use it from paths that do NOT already hold workspace.LockID
// (the create paths); handleWorkspaceDelegationPut holds the lock across its
// whole load-modify-write and calls workspace.SaveDelegation directly, because
// the lock pool is not reentrant.
func saveWorkspaceDelegation(home, id string, edges []storedDelegationEdge) error {
	unlock := workspace.LockID(id)
	defer unlock()
	return workspace.SaveDelegation(home, id, edges)
}

// ensureBuiltinRosterPresent unions any built-in-roster member
// (defaultWorkspaceTeam(cfg) — coreagent.All() ∩ configured agents) missing
// from the existing default workspace w's CoreTeam, and persists the change
// ONLY if the set actually grew (ADR-046 P1, FR-008 / US-3 AS-4). This keeps
// an upgraded install's pre-existing default workspace current with the
// installed built-in roster (e.g. a coreagent added post-upgrade) every
// boot, idempotently and safely:
//   - NEVER removes an existing member (including a custom agent an operator
//     added to the team by hand).
//   - NEVER adds a non-built-in ID — only defaultWorkspaceTeam(cfg)'s own
//     coreagent.All() ∩ configured-agents set is eligible.
//   - Does NOT touch w.Delegation. Expanding or seeding a workspace team must
//     never create or imply a Delegation[] trust edge (FR-038) — trust stays
//     workspace-scoped and explicit (ADR-037).
func ensureBuiltinRosterPresent(home string, w storedWorkspace, cfg *config.Config) error {
	builtin := defaultWorkspaceTeam(cfg)
	if len(builtin) == 0 {
		return nil
	}
	existing := make(map[string]bool, len(w.CoreTeam))
	for _, id := range w.CoreTeam {
		existing[id] = true
	}
	grew := false
	for _, id := range builtin {
		if !existing[id] {
			w.CoreTeam = append(w.CoreTeam, id)
			existing[id] = true
			grew = true
		}
	}
	if !grew {
		return nil
	}
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(home, w); err != nil {
		return fmt.Errorf("ensureBuiltinRosterPresent: write: %w", err)
	}
	slog.Info("rest: ensureDefaultWorkspace: back-filled built-in roster into existing default workspace",
		"workspace_id", w.ID, "team_size", len(w.CoreTeam))
	return nil
}

// logWorkspacelessAgents is a boot-time diagnostic (ADR-046 P1, FR-007/008):
// it enumerates every configured agent (cfg.Agents.List) that is a member of
// NO workspace's CoreTeam and emits ONE WARN naming all of them, if any
// exist. Called once at boot, after ensureDefaultWorkspace has run, so an
// operator upgrading an install with pre-existing custom agents sees the
// full list up front — rather than discovering it only as per-turn
// ErrAgentNotWorkspaceMember refusals, one agent at a time, as those agents
// happen to be invoked.
//
// This is diagnostic ONLY: it never mutates any workspace or agent — FR-008
// forbids auto-adding a pre-existing/custom agent to any team, and that rule
// applies here too. The operator remedy is manual: add the agent to a
// workspace's Team tab.
func logWorkspacelessAgents(home string, cfg *config.Config) {
	if cfg == nil || len(cfg.Agents.List) == 0 {
		return
	}
	var workspaceless []string
	for i := range cfg.Agents.List {
		id := cfg.Agents.List[i].ID
		if id == "" {
			continue
		}
		if _, found := workspace.FindForAgent(home, id); !found {
			workspaceless = append(workspaceless, id)
		}
	}
	if len(workspaceless) == 0 {
		return
	}
	sort.Strings(workspaceless)
	slog.Warn(
		"gateway: configured agents are members of no workspace — they cannot execute a turn until added to a workspace's Team tab (ADR-046 P1, FR-007/008)",
		"agent_ids",
		strings.Join(workspaceless, ","),
		"count",
		len(workspaceless),
	)
}

// HandleWorkspaces dispatches all /api/v1/workspaces* requests.
func (a *restAPI) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/workspaces")

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

	// /api/v1/workspaces/{id}/plans — Plan entities scoped to this workspace
	// (ADR-049 D1, Wave 2-C1 deferred REST paths; mirrors the removed
	// /workspaces/{id}/milestones shape). GET/POST only; individual plan
	// GET/PUT/DELETE and the /approve /stop actions live at
	// /api/v1/plans/{id}... (HandlePlans, rest_plans.go).
	if strings.HasSuffix(rest, "/plans") {
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/plans")
		switch r.Method {
		case http.MethodGet:
			a.handleWorkspacePlansList(w, id)
		case http.MethodPost:
			a.handleWorkspacePlanCreate(w, r, id)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// /api/v1/workspaces/{id}/media* — workspace media library (ADR-051-rev4:
	// list, get/delete by id, POST attachments). Suffix matching is wrong for
	// this family (a hypothetical id containing "media" as a substring would
	// misroute here, and /media/{mediaID} has a trailing segment), so this
	// checks the actual second path segment instead. Restored for #609: this
	// dispatch existed on the release parent (41e16237) and was dropped in the
	// #597 merge resolution, orphaning HandleWorkspaceMedia entirely — the
	// regression test in rest_workspaces_media_dispatch_test.go goes through
	// HandleWorkspaces, not the handler directly, so the wiring itself is
	// what's guarded.
	if segs := strings.Split(strings.TrimPrefix(rest, "/"), "/"); len(segs) >= 2 && segs[1] == "media" {
		a.HandleWorkspaceMedia(w, r)
		return
	}

	// /api/v1/workspaces/{id}/mounts[/{name}] — the mount lifecycle (FR-7.1/
	// FR-7.3, ADR-063 D4). Same second-path-segment dispatch as /media above
	// (not suffix matching — /mounts/{name} has a trailing segment).
	if segs := strings.Split(strings.TrimPrefix(rest, "/"), "/"); len(segs) >= 2 && segs[1] == "mounts" {
		a.HandleWorkspaceMounts(w, r)
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
		result = append(result, workspaceToWire(a.homePath, ws, taskCounts[ws.ID]))
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
	// FR-9.2: repository is retired from the wire entirely (no back-compat).
	// gen.WorkspaceCreateRequest no longer has the field at all, so without
	// this raw-body sniff a client still sending it would have it silently
	// dropped by Go's default JSON decode rather than getting a loud 400.
	if !rejectRetiredRepositoryField(w, r) {
		return
	}
	// FR-5: mounts have their own dedicated lifecycle (workspace.CreateMount/
	// DeleteMount) — see mountsNotWritableHereMsg's doc comment.
	if !rejectMountsWriteField(w, r) {
		return
	}

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
	// review r1 major M4/Gap #6: reject an unregistered or System Agent id
	// before any workspace/session side effects.
	if req.CoreTeam != nil {
		if vErr := validateCoreTeamMembers(a.agentLoop.GetConfig(), *req.CoreTeam); vErr != nil {
			jsonErr(w, http.StatusBadRequest, vErr.Error())
			return
		}
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
	cfg := a.agentLoop.GetConfig()
	if req.CoreTeam != nil && len(*req.CoreTeam) > 0 {
		ws.CoreTeam = deduplicateStrings(*req.CoreTeam)
	} else {
		// No explicit team (nil, OR an explicit empty array — an empty array
		// means "unspecified", not "deliberately no team"; otherwise a caller
		// sending "core_team": [] would get a permanently teamless workspace
		// with setup_pending left false and no agent ever available to run the
		// interview and add members): new user-created workspaces start with
		// only Ava on the team — she interviews the user and builds out the
		// rest of the team herself (the setup_pending flow). This deliberately
		// does NOT mirror ensureDefaultWorkspace's full-roster seed, which
		// remains reserved for the auto-created boot default workspace
		// ("My Workspace").
		ws.CoreTeam = newWorkspaceSetupTeam(cfg)
		// Only mark setup_pending when the seed actually produced a
		// non-empty team. newWorkspaceSetupTeam returns nil when Ava is absent
		// from the live config (e.g. a lite/custom install) — without this
		// guard, such an install's workspace would be permanently stuck with
		// setup_pending=true and an empty core_team: nothing (no kickoff-eligible
		// agent) is ever available to run the interview and clear the flag.
		ws.SetupPending = len(ws.CoreTeam) > 0
	}
	// Seed default delegation edges from each team agent's seeded role (M5),
	// restricted to edges whose endpoints are both on this workspace's team so a
	// custom core_team never gains edges to agents it did not include.
	seedEdges := seedEdgesForTeam(defaultWorkspaceDelegationEdges(cfg), ws.CoreTeam)
	// Defense-in-depth: validate each seeded edge so no unvalidated edge is ever
	// persisted. The source is the trusted compiled-in roster so failures are
	// unexpected; on failure log WARN and drop the offending edge (do not hard-fail
	// the create request over a seed-config issue).
	// The workspace is brand new, so its delegation store is empty and the team
	// set is core_team alone.
	createTeam := workspace.TeamSet(ws.CoreTeam, nil)
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

	if err := writeWorkspaceFile(a.homePath, ws); err != nil {
		slog.Error("rest: create workspace", "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// Seed edges go to the delegation store, never onto the record — see
	// pkg/workspace/delegationstore.go. The workspace record is already
	// committed at this point, so a store failure is logged rather than turned
	// into a 500 the client would read as "nothing was created". The fallout is
	// fail-closed (a workspace with no delegation graph denies every
	// delegation) and repairable from the Team tab.
	if err := saveWorkspaceDelegation(a.homePath, ws.ID, validSeedEdges); err != nil {
		slog.Error("rest: create workspace: seed delegation store", "error", err, "id", ws.ID)
	}
	wire := workspaceToWire(a.homePath, ws, 0)
	if a.auditor != nil {
		if err := a.auditor.Log(
			&audit.Entry{
				Event:    "workspace.create",
				Decision: audit.DecisionAllow,
				Details:  map[string]any{"id": ws.ID, "name": ws.Name},
			},
		); err != nil {
			slog.Warn("audit write failed", "event", "workspace.create", "id", ws.ID, "error", err)
		}
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
	jsonOK(w, workspaceToWire(a.homePath, ws, countTasksForWorkspace(a.homePath, id)))
}

// workspaceMemberConfigsFromWire translates the generated member_configs map
// (gen.WorkspaceMemberConfig, pointer fields) into the internal
// workspace.MemberConfig map (value fields). It returns mcPresent=false when the
// wire field is absent (nil) so callers preserve merge semantics (absent →
// unchanged). The server-managed session_id (FR-010, readOnly in the contract)
// is intentionally NOT read from client input.
func workspaceMemberConfigsFromWire(
	wire *map[string]gen.WorkspaceMemberConfig,
) (map[string]workspace.MemberConfig, bool) {
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

	// FR-9.2: repository is retired from the wire entirely (no back-compat).
	// gen.WorkspaceUpdateRequest no longer has the field at all, so without
	// this raw-body sniff a client still sending it would have it silently
	// dropped by Go's default JSON decode rather than getting a loud 400.
	if !rejectRetiredRepositoryField(w, r) {
		return
	}
	// FR-5: mounts have their own dedicated lifecycle (workspace.CreateMount/
	// DeleteMount) — see mountsNotWritableHereMsg's doc comment.
	if !rejectMountsWriteField(w, r) {
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
	if req.Status != nil && !req.Status.Valid() {
		jsonErr(w, http.StatusBadRequest, `status must be "active" or "archived"`)
		return
	}

	// Serialize the full load-modify-write cycle against this workspace
	// ID so a concurrent kickoff consume (pkg/gateway/websocket.go), a racing
	// delete, or a racing delegation PUT cannot interleave with this update —
	// e.g. resurrecting a just-cleared setup_pending flag with this request's
	// stale in-memory copy, or this write clobbering a concurrent rename.
	// Held for the whole handler (including the heartbeat-session-creation
	// loop below) via defer: correctness first, this is not a hot path.
	unlock := workspace.LockID(id)
	defer unlock()

	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		return
	}

	// review r1 major M4/Gap #6 (ADR-054 D6 rule 1: "validate the delta, not
	// the world"): reject an unregistered or System Agent id — but only
	// among members THIS WRITE INTRODUCES, not the whole incoming array.
	//
	// Why: core_team is a full-replacement field (a typical caller reads the
	// current team, adds/removes one id, and PUTs the whole array back). The
	// pre-fix version of this check ran validateCoreTeamMembers against the
	// ENTIRE incoming *req.CoreTeam — so a workspace that already carried
	// even one dangling member (e.g. an agent deleted after being added,
	// which rule 2 says must be surfaced, never fatal) would have every
	// future PUT rejected outright, because that same stale id keeps
	// reappearing in every round-tripped array. This is the exact "permanently
	// wedged workspace, no repair path" failure ADR-054 §1 documents. Diffing
	// against the workspace's own pre-write ws.CoreTeam and validating only
	// the introduced ids un-wedges it: a pre-existing dangling member is
	// carried through untouched (still dangling, still reachable via
	// RepairDanglingCoreTeamMembers below), while a genuinely new bad id is
	// still rejected exactly as before.
	if req.CoreTeam != nil {
		newTeam := deduplicateStrings(*req.CoreTeam)
		existingMembers := make(map[string]struct{}, len(ws.CoreTeam))
		for _, memberID := range ws.CoreTeam {
			existingMembers[memberID] = struct{}{}
		}
		var introduced []string
		for _, memberID := range newTeam {
			if _, alreadyMember := existingMembers[memberID]; !alreadyMember {
				introduced = append(introduced, memberID)
			}
		}
		if vErr := validateCoreTeamMembers(a.agentLoop.GetConfig(), introduced); vErr != nil {
			jsonErr(w, http.StatusBadRequest, vErr.Error())
			return
		}
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
			// Shared-store-first, per-agent-fallback delete — see
			// deleteHeartbeatSessionAnyStore's doc for why (matches where the
			// eager-creation call below now writes).
			if delErr := deleteHeartbeatSessionAnyStore(a.agentLoop, sc.agentID, sc.sessionID); delErr != nil {
				slog.Warn("rest: workspace PUT: rollback session delete failed",
					"agent_id", sc.agentID, "session_id", sc.sessionID, "error", delErr)
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
		if vErr := workspace.ValidateMemberConfigs(
			effectiveCoreTeam,
			incomingMC,
			configOnlyIsWorker(cfg),
		); vErr != nil {
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
					// ADR-049 D4/FR-065: this codebase has no global per-agent
					// enable/disable REST toggle — the per-workspace
					// member-heartbeat flag (ADR-027) is the only one, so it
					// is the wiring point for PausePlansOwnedBy/
					// ResumePlansOwnedBy. A stored session_id here is the same
					// "was previously enabled" signal the session-release
					// logic immediately below already relies on. Best-effort:
					// a pause failure is logged, never blocks the PUT.
					if pe := agent.GetPlanEngine(a.agentLoop); pe != nil {
						if perr := pe.PausePlansOwnedBy(agentID); perr != nil {
							slog.Warn("rest: workspace PUT: pause plans on heartbeat disable failed",
								"workspace_id", id, "agent_id", agentID, "error", perr)
						}
					}
					// Shared-store-first, per-agent-fallback delete — mirrors
					// eager creation above and rollbackCreatedSessions below.
					// A direct GetAgentStore(agentID).DeleteSession() here
					// misses sessions minted in the shared store (the
					// eager-creation default per the FIX comment above),
					// leaving the standing session orphaned on disable.
					if delErr := deleteHeartbeatSessionAnyStore(a.agentLoop, agentID, stored.Heartbeat.SessionID); delErr != nil {
						slog.Warn("rest: workspace PUT: disable-path session release failed",
							"workspace_id", id, "agent_id", agentID,
							"session_id", stored.Heartbeat.SessionID, "error", delErr)
					} else {
						slog.Info("rest: workspace PUT: released heartbeat session on disable",
							"workspace_id", id, "agent_id", agentID,
							"session_id", stored.Heartbeat.SessionID)
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
			// ADR-049 D4/FR-065: resume any plan owned by this agent that was
			// paused for owner_disabled (idempotent no-op when nothing was
			// paused for that reason — see ResumePlansOwnedBy's doc comment).
			// Fires unconditionally on every enabled=true entry in this PUT
			// (not gated on the idempotent-enable early-continue below), so a
			// re-submitted "already enabled" PUT still self-heals a plan that
			// somehow stayed paused. Best-effort: a failure is logged, never
			// blocks the PUT.
			if pe := agent.GetPlanEngine(a.agentLoop); pe != nil {
				if rerr := pe.ResumePlansOwnedBy(agentID); rerr != nil {
					slog.Warn("rest: workspace PUT: resume plans on heartbeat enable failed",
						"workspace_id", id, "agent_id", agentID, "error", rerr)
				}
			}
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
			// FIX (pre-existing defect, confirmed against loop.go's own
			// GetSessionStore/GetAgentStore doc comment): eager creation MUST use
			// the shared session store, not the legacy per-agent store. The
			// heartbeat cron's continue-mode session lookup (pickSession in
			// schedules.go) resolves the stored session_id exclusively via
			// al.GetSessionStore().GetOrCreateScheduledSession — a session minted
			// in the per-agent store is invisible to that lookup, so the first
			// heartbeat fire silently created a second, empty session under the
			// SAME id in the shared store while this eagerly-created one (holding
			// the WorkspaceID stamp) sat orphaned. Mirrors the shared-store-
			// primary/per-agent-fallback idiom createSessionHTTP (rest.go) already
			// uses for the joined session model.
			sessStore := a.agentLoop.GetSessionStore()
			if sessStore == nil {
				sessStore = a.agentLoop.GetAgentStore(agentID)
			}
			if sessStore == nil {
				// HIGH-3: nil store is an internal inconsistency (agent passed
				// CoreTeam validation, so it must be registered). Persisting
				// enabled=true with an empty session_id is invalid state — roll
				// back any sessions created so far and return 500.
				slog.Error("rest: workspace PUT: session store unavailable for heartbeat session",
					"workspace_id", id, "agent_id", agentID)
				rollbackCreatedSessions()
				jsonErr(w, http.StatusInternalServerError, "session store unavailable for heartbeat session")
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
					if delErr := deleteHeartbeatSessionAnyStore(
						a.agentLoop, removedID, oldMC.Heartbeat.SessionID); delErr != nil {
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
						if delErr := deleteHeartbeatSessionAnyStore(
							a.agentLoop, removedID, oldMC.Heartbeat.SessionID); delErr != nil {
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
				ws.MemberConfigs = pruned
			}
		}
	}

	// No-op: nothing changed — return current state without writing.
	if !changed {
		jsonOK(w, workspaceToWire(a.homePath, ws, countTasksForWorkspace(a.homePath, id)))
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
		if err := a.auditor.Log(
			&audit.Entry{
				Event:    "workspace.update",
				Decision: audit.DecisionAllow,
				Details:  map[string]any{"id": id},
			},
		); err != nil {
			slog.Warn("audit write failed", "event", "workspace.update", "id", id, "error", err)
		}
	}
	jsonOK(w, workspaceToWire(a.homePath, ws, countTasksForWorkspace(a.homePath, id)))
}

func (a *restAPI) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	// The per-ID lock guards ONLY the
	// authoritative delete — load/validate, the two HARD cascade steps that
	// must complete (or abort the whole delete) before the workspace file is
	// removed, and the file removal itself — so a racing kickoff consume
	// cannot read the workspace after this delete has removed it
	// (readWorkspaceFile then fails and consumeWorkspaceSetupKickoff correctly
	// rejects it), and so a racing PUT/delegation-PUT cannot write back a
	// stale copy after this delete removes the file. It is released
	// IMMEDIATELY after the workspace file is gone via explicit unlock() calls
	// at every exit from this section (no defer) — the remaining BEST-EFFORT
	// cascade (cron jobs, heartbeat sessions, mailboxes, the
	// workspace directory RemoveAll) never touches workspaces/{id}.json, so it
	// does not need to serialize against it; running it after unlock avoids
	// holding the lock across a potentially multi-second directory RemoveAll
	// plus config/credential rewrites, which could otherwise block a
	// shard-colliding kickoff's WS readLoop for no correctness benefit.
	unlock := workspace.LockID(id)

	// Verify the workspace exists before cascading.
	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		unlock()
		return
	}

	// FR-1.9: no access gate — owner is attribution only.

	// Default workspace cannot be deleted (FR-1.6 delete-protection retained).
	if ws.IsDefault {
		unlock()
		jsonErr(w, http.StatusConflict, "cannot delete the default workspace")
		return
	}

	// HARD cascade step (gates the delete — must stay under the lock, before
	// the workspace file is removed): a task-scan failure aborts the whole
	// delete with 500.
	if err := deleteTasksForWorkspace(a.homePath, id); err != nil {
		unlock()
		slog.Error("rest: delete workspace: cascade tasks", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to scan tasks for cascade delete")
		return
	}

	// HARD cascade step (gates the delete — must stay under the lock, before
	// the workspace file is removed): ADR-029 FR-025/MAJ-005 — disable and
	// unbind all channel instances bound to this workspace BEFORE removing the
	// workspace file. If this config write fails the delete aborts with 500,
	// leaving the workspace + bindings fully consistent (no orphan). Ordering
	// guarantee: config unbind → workspace file delete.
	if err := unbindChannelInstancesForWorkspace(a, id); err != nil {
		unlock()
		slog.Error("rest: delete workspace: cascade channel unbind", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to unbind channel instances for workspace")
		return
	}

	// The authoritative delete: remove the workspace JSON file. Still under
	// the lock — this is the write the lock exists to serialize.
	path := filepath.Join(a.homePath, "workspaces", id+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		unlock()
		slog.Error("rest: delete workspace: remove file", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// The workspace file is gone — release the lock now. Everything below is
	// best-effort cascade cleanup that does not touch workspaces/{id}.json.
	unlock()

	// Best-effort cascade (order preserved from before this restructure):
	// (1) heartbeat cron jobs → (2) heartbeat sessions →
	// (3) mailboxes → (4) workspace directory. Milestones (formerly step 3)
	// were removed (ADR-049 D1) — tasks now carry workspace-scoped tags
	// instead, which need no per-workspace cascade cleanup.
	// FR-023/US-9: release all heartbeat cron jobs owned by this workspace.
	// Best-effort (logged on failure).
	if cs := a.cronService.Load(); cs != nil {
		releaseHeartbeatJobsForWorkspace(cs, id)
	}

	// HIGH-1 (FR-023): release standing heartbeat sessions for each member that
	// had a heartbeat enabled. The sessions live in per-agent session stores (NOT
	// under the workspace directory), so RemoveAll of the workspace dir does not
	// remove them. ws (loaded above, before unlock) still carries the
	// member_configs needed to find which sessions to release. Best-effort
	// per-session.
	releaseHeartbeatSessionsForWorkspace(a.agentLoop, ws)

	// M11: remove every mailbox (config.mailboxes entry + stored credential)
	// bound to this workspace. Best-effort (logged on failure, never aborts
	// the delete) — see removeMailboxesForWorkspace's doc comment.
	removeMailboxesForWorkspace(a, id)

	// FR-026 + FR-043a + SC-017: the deleted workspace's BROWSER.
	//
	// This is the one and only place a browser profile directory is removed.
	// A workspace's profile holds its live logins — session cookies for
	// whatever its agents signed into — so a departed client's data must
	// actually depart, and equally must NOT depart on any of the four events
	// that merely pause a workspace (idle close, eviction, roster change,
	// reload). Those keep the profile precisely so the workspace is still
	// logged in when it comes back.
	//
	// ORDER IS THE WHOLE THING (SC-017): Close(key) must RETURN before
	// DeleteProfile(key) runs. Chrome writes its cookie jar and Local Storage
	// on the way down; deleting the directory out from under a live process
	// races those writes, and DeleteProfile refuses outright while the key is
	// still live rather than trusting the caller to have got it right.
	// One Close + one DeleteProfile is not enough, and the single-shot version
	// reported success in the case where it was not — see
	// deleteWorkspaceBrowserProfile.
	if pool := browserPoolFor(a); pool != nil {
		if key, kerr := browser.ParseBrowsingKeyString("ws:" + id); kerr == nil {
			if derr := deleteWorkspaceBrowserProfile(pool, key); derr != nil {
				slog.Warn("rest: delete workspace: cascade browser profile", "id", id, "error", derr)
			}
		} else {
			// Never silent: an unusable key means the profile directory this
			// workspace's logins live in is not addressable, so nothing below
			// removes it and the data outlives the workspace.
			slog.Warn("rest: delete workspace: cascade browser profile: unusable browsing key — "+
				"the workspace's browser profile is NOT removed",
				"id", id, "error", kerr)
		}
	} else {
		// The pool is nil when this gateway booted with browser tools
		// disabled. A profile directory written by an earlier boot that had
		// them enabled is still on disk, with the workspace's live logins in
		// it, and this delete does not reach it. Say so rather than letting
		// the absence of a pool read as the absence of a profile.
		slog.Warn("rest: delete workspace: no browser pool on this gateway — a browser profile left on "+
			"disk by an earlier boot is NOT removed by this delete",
			"id", id)
	}

	// Remove the workspace's mount record. Mounts live in
	// entities/mounts/<id>.json (out of a sandboxed child's reach — see
	// pkg/workspace/mountstore.go), NOT under the workspace directory, so the
	// RemoveAll below does not reach them. This removes only the record of the
	// grants; the operator's real folders are never touched (FR-8.6).
	// Best-effort, and it runs AFTER unlock() above because DeleteMountStore
	// takes LockID itself and that pool is not reentrant.
	if err := workspace.DeleteMountStore(a.homePath, id); err != nil {
		slog.Warn("rest: delete workspace: cascade mount store", "id", id, "error", err)
	}

	// Remove the workspace's delegation record, for the same reason and with
	// the same lifecycle as the mount store above: it lives in
	// entities/delegation/<id>.json (see pkg/workspace/delegationstore.go), NOT
	// under the workspace directory, so the RemoveAll below never reaches it.
	// Leaving it behind would strand an authorization record for a workspace
	// that no longer exists — and would silently re-authorize the graph if the
	// id were ever reused. Best-effort, and after unlock() because
	// DeleteDelegationStore takes LockID itself and that pool is not reentrant.
	if err := workspace.DeleteDelegationStore(a.homePath, id); err != nil {
		slog.Warn("rest: delete workspace: cascade delegation store", "id", id, "error", err)
	}

	// Best-effort: remove the per-workspace directory. This now holds more than
	// AGENT.md and the shared memory room: its work/ subdirectory is also the
	// SHARED project-work directory every CoreTeam member (native or
	// subagent_3p) actually runs in (pkg/workspace.FindForAgent-driven
	// filesystem re-rooting, agent loop's runTurn / external_dispatch.go's
	// runExternalCLISubTurn) — so this delete also destroys any working files
	// those agents wrote there. That is the correct, expected semantic (a
	// deleted workspace deletes its own shared working tree) — this comment
	// update only makes the blast radius explicit; the JSON removal above
	// remains the authoritative delete, and a stale directory left behind on
	// a RemoveAll failure is not fatal. Runs unlocked (see restructure note
	// above) — it is best-effort and never touches workspaces/{id}.json.
	//
	// FR-009: cascade-delete the workspace's media library BEFORE the
	// directory wipe so the library's CascadeDelete API can emit the
	// media.cascade_delete audit event with the full deleted-entry summary
	// (media_ids, filenames, bytes_freed). The actor is the authenticated
	// principal that triggered DELETE — empty string when no principal is
	// resolved (e.g. unauthenticated dev-mode bypass) — sourced from the
	// same r.Context() lookup the sibling media-delete handler
	// (rest_workspace_media.go's handleWorkspaceMediaDelete) already uses,
	// rather than a hardcoded "" that made every bulk cascade-delete
	// unattributable regardless of who was actually authenticated. The hook
	// opens a fresh library instance because the original lib (if any) was
	// held by the request scope, not the delete handler's scope.
	actor := a.callerIdentity(r).Username
	mediaCascadeFailed := false
	if hookErr := workspace.WorkspaceDeleteHook(a.homePath, id, actor, a.auditor); hookErr != nil {
		if errors.Is(hookErr, workspace.ErrCascadeStraggler) {
			// Re-review FIX 2: library.CascadeDelete's two-phase commit only
			// returns a non-nil error together with a fully-populated
			// deleted/bytesFreed summary when the manifest was already
			// committed and the sole remaining failure is a final on-disk
			// unlink of an already-quarantined file. WorkspaceDeleteHook
			// (pkg/workspace/media_delete.go) detects exactly that and wraps
			// it in workspace.ErrCascadeStraggler. Every quarantine path
			// lives under wsDir/media/, and the unconditional
			// os.RemoveAll(wsDir) below always cleans it up moments later
			// regardless of this branch — so it must NOT be reported to the
			// client as a failed delete (that used to 500 a delete that
			// fully succeeded). Logged at Warn for operator visibility only;
			// the media.cascade_delete audit event WorkspaceDeleteHook
			// already emitted still records Decision=error for this moment
			// in time (see logCascadeAuditEvent's doc).
			logger.WarnCF("rest", "delete workspace: media cascade-delete straggler (self-healed by directory wipe)",
				map[string]any{"id": id, "actor": actor, "error": hookErr.Error()})
		} else {
			mediaCascadeFailed = true
			// Re-review FIX 1: was a bare slog.Error, invisible on a
			// backgrounded gateway (slog.SetDefault is never called anywhere
			// in this repo). Route through pkg/logger instead.
			logger.ErrorCF("rest", "delete workspace: media cascade-delete",
				map[string]any{"id": id, "actor": actor, "error": hookErr.Error()})
		}
	}

	// The directory wipe below runs UNCONDITIONALLY even when the cascade
	// above failed. This is safe, not merely convenient: CascadeDelete's own
	// atomicity (library.CascadeDelete's doc) means a mid-cascade failure
	// rolls back the in-memory manifest AND best-effort-restores any
	// already-quarantined files back to their original names; even in the
	// worst case where that best-effort restore itself also fails and
	// leaves stray quarantine files behind, those files are still inside
	// wsDir/media/, which this RemoveAll deletes wholesale regardless of
	// the library's internal bookkeeping. There is no code path where a
	// cascade failure leaves workspace media bytes OUTSIDE wsDir, so
	// proceeding here cannot orphan a file onto disk — skipping RemoveAll
	// on cascade failure would only leave MORE behind, not less. The real
	// gap a failed cascade used to leave was an audit one (a cascade that
	// failed before deleting anything produced zero audit trail), which
	// WorkspaceDeleteHook now closes by always emitting a
	// media.cascade_delete event (DecisionError on failure) regardless of
	// how many entries were actually removed.
	wsDir := workspace.WorkspaceDir(a.homePath, id)
	// FIX 3: RemoveAll's own failure (e.g. EBUSY/permission on
	// workspaces/<id>/work/, which a live agent turn may still be writing
	// to) used to only reach a slog.Warn — the response gate below checked
	// solely mediaCascadeFailed, so a real leftover-directory failure was
	// still reported to the caller as a 204 "fully deleted" while wsDir was
	// still (partially) on disk. dirRemoveFailed threads that outcome into
	// the same gate, alongside — not merged into — mediaCascadeFailed, so
	// the existing straggler-vs-hard-cascade-failure distinction above is
	// unaffected: this only tracks whether the directory wipe itself, run
	// unconditionally regardless of that distinction, actually succeeded.
	dirRemoveFailed := false
	if err := removeAllFn(wsDir); err != nil {
		dirRemoveFailed = true
		slog.Warn("rest: delete workspace: cascade dir", "id", id, "dir", wsDir, "error", err)
	}

	if a.auditor != nil {
		if err := a.auditor.Log(
			&audit.Entry{
				Event:    "workspace.delete",
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"id":                   id,
					"actor":                actor,
					"media_cascade_failed": mediaCascadeFailed,
					"dir_remove_failed":    dirRemoveFailed,
				},
			},
		); err != nil {
			slog.Warn("audit write failed", "event", "workspace.delete", "id", id, "error", err)
		}
	}

	// The workspace record itself IS gone at this point (the authoritative
	// delete happened earlier, under the lock, and already released it) —
	// a 404/409 here would misrepresent that. But a failed media cascade OR
	// a failed directory wipe is a genuine partial failure the caller must
	// be able to see, not a blank 204 implying total success (FIX-4: a
	// silent 204 here is exactly what let every media-cascade failure go
	// unnoticed; FIX 3 closes the same gap for a RemoveAll failure, which
	// used to bypass this gate entirely). 500 is consistent with the two
	// HARD cascade steps earlier in this same handler (task scan, channel
	// unbind), which already return this status for cascade failures on
	// this endpoint. The caller can confirm the workspace itself is gone
	// via a follow-up GET (404) and inspect whatever survives on disk /
	// in the media library via GET /workspaces/{id}/media.
	if mediaCascadeFailed || dirRemoveFailed {
		msg := "workspace deleted, but media library cleanup failed; see server logs"
		switch {
		case mediaCascadeFailed && dirRemoveFailed:
			msg = "workspace deleted, but media library cleanup and on-disk directory removal both failed; see server logs"
		case dirRemoveFailed:
			msg = "workspace record deleted, but the on-disk workspace directory could not be fully removed; see server logs"
		}
		jsonErr(w, http.StatusInternalServerError, msg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// releaseHeartbeatSessionsForWorkspace deletes the standing heartbeat session for
// every member of ws that has a heartbeat with a non-empty session_id. These
// sessions live in a session store (the shared store for every heartbeat
// created after the eager-creation fix below, or the legacy per-agent store
// for one created before it — see deleteHeartbeatSessionAnyStore), never under
// the workspace directory, so the workspace-directory RemoveAll does NOT
// remove them. Best-effort per session: a failure is logged and skipped so
// one bad session does not block the rest of the cascade.
//
// Called from handleWorkspaceDelete before the workspace file is removed (we need
// member_configs to find which sessions to release).
func releaseHeartbeatSessionsForWorkspace(al agentLoopAccessor, ws storedWorkspace) {
	for agentID, mc := range ws.MemberConfigs {
		if mc.Heartbeat == nil || mc.Heartbeat.SessionID == "" {
			continue
		}
		if err := deleteHeartbeatSessionAnyStore(al, agentID, mc.Heartbeat.SessionID); err != nil {
			slog.Warn("heartbeat cascade: failed to delete heartbeat session",
				"workspace_id", ws.ID, "agent_id", agentID,
				"session_id", mc.Heartbeat.SessionID, "error", err)
		}
	}
}

// agentLoopAccessor is the minimal interface required by
// releaseHeartbeatSessionsForWorkspace and deleteHeartbeatSessionAnyStore.
// *agent.AgentLoop satisfies it.
type agentLoopAccessor interface {
	GetAgentStore(agentID string) *session.UnifiedStore
	GetSessionStore() *session.UnifiedStore
}

// deleteHeartbeatSessionAnyStore deletes a standing heartbeat session,
// checking BOTH the shared session store and the agent's legacy per-agent
// store UNCONDITIONALLY (no short-circuit) — every heartbeat session is
// created in the shared store as of the eager-creation fix (see the FIX
// comment on the enable-path NewHeartbeatSession call above in
// HandleWorkspaces), but an install that had already provisioned a session
// under the OLD code (legacy per-agent store) before that fix shipped can end
// up with the SAME session ID present in BOTH stores: the first heartbeat
// fire's pickSession calls GetOrCreateScheduledSession(id, owner) against the
// SHARED store, which does not find the legacy copy (different store
// instance) and mints a second, independent session directory under the
// identical ID instead. From that point the shared copy is the live one, but
// the legacy copy still sits on disk and must also be released — a
// short-circuit "return as soon as one store succeeds" leaves it there
// forever the moment the shared delete succeeds first, which defeats the
// whole point of this fallback (it exists precisely to migrate that
// dual-copy population, not just the simpler single-copy one).
//
// Each store is checked for the session's existence before attempting a
// delete on it, so "this store never had the session" is distinguished from
// "this store had it and failed to remove it": DeleteSession's not-found
// error carries no sentinel to test against (a plain fmt.Errorf), so
// existence is determined here directly via os.Stat against the store's own
// BaseDir() rather than parsed out of an error string. This also fixes the
// masking defect in the previous version: there, if the shared store's
// delete failed for a REAL reason (I/O error, permission error) while the
// legacy store happened to also hold a copy and deleted cleanly, the
// function returned nil (success) and the real shared-store failure was
// silently swallowed. Now a genuine failure in either store is always
// surfaced, regardless of what the other store did.
//
// Returns nil once every store that HAD the session successfully released
// it (a store that never had it is not an error). Returns a joined error if
// any store's stat or delete genuinely failed. Returns an error if neither
// store had the session at all (nothing to migrate, but also nothing
// released — worth the caller's existing log-and-continue Warn).
func deleteHeartbeatSessionAnyStore(al agentLoopAccessor, agentID, sessionID string) error {
	// Reject IDs that could escape a store's base directory before ever
	// stat-ing them. Mirrors session.validateSessionID's rules; that
	// function is unexported and applied inside DeleteSession itself, but
	// existence here is checked directly against each store's BaseDir()
	// below (ahead of any call into DeleteSession), so the same rejection
	// is duplicated narrowly at this boundary rather than relied upon from
	// across the package.
	if sessionID == "" || strings.Contains(sessionID, "/") || strings.Contains(sessionID, "\\") ||
		strings.Contains(sessionID, "..") {
		return fmt.Errorf("invalid heartbeat session id %q for agent %q", sessionID, agentID)
	}

	tryDelete := func(store *session.UnifiedStore) (deleted bool, err error) {
		if store == nil {
			return false, nil
		}
		dir := filepath.Join(store.BaseDir(), sessionID)
		if _, statErr := os.Stat(dir); statErr != nil {
			if os.IsNotExist(statErr) {
				return false, nil // absent from this store: not a failure
			}
			return false, fmt.Errorf("stat session %q: %w", sessionID, statErr)
		}
		if delErr := store.DeleteSession(sessionID); delErr != nil {
			return false, fmt.Errorf("delete session %q: %w", sessionID, delErr)
		}
		return true, nil
	}

	// Both stores are always attempted — the dual-copy case (the entire
	// reason this fallback exists) requires both deletes to run every time,
	// not just until the first one succeeds.
	sharedDeleted, sharedErr := tryDelete(al.GetSessionStore())
	legacyDeleted, legacyErr := tryDelete(al.GetAgentStore(agentID))

	if sharedErr != nil || legacyErr != nil {
		return errors.Join(sharedErr, legacyErr)
	}
	if !sharedDeleted && !legacyDeleted {
		return fmt.Errorf("no session store had session %q for agent %q", sessionID, agentID)
	}
	return nil
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

// validateCoreTeamMembers rejects a core_team containing an id that is not a
// registered agent, or that IS registered but is a System Agent
// (AgentConfig.IsSystem, Type=="system", ADR-049 D3) — review r1 major
// M4/Gap #6. System Agents (e.g. the Judge) are seeded, locked, no-tools
// internal-LLM agents that are NEVER chat targets and are documented as
// "excluded from ... team rosters" by AgentConfig.IsSystem's own doc comment
// (pkg/config/config.go), but neither handleWorkspacePost nor
// handleWorkspacePut enforced that at the write path before this fix — a
// caller could silently add a System Agent (or a typo'd/nonexistent id) to a
// workspace's core_team with zero validation. Returns nil for an empty
// coreTeam (nothing to validate).
//
// This rejection stays exactly as it was even after ADR-052's Judge/verifier
// fix made System Agents IMPLICIT members of EVERY workspace (operator
// decision, 2026-07-21: "make the judge a member of every workspace, keep it
// simple" — pkg/workspace's isImplicitMember, consulted by
// FindForAgent/FindForAgentPreferring). Implicit membership everywhere makes
// an EXPLICIT core_team entry for a System Agent strictly redundant, never
// necessary — so this validation continues to reject one on write, rather
// than being relaxed or repurposed.
func validateCoreTeamMembers(cfg *config.Config, coreTeam []string) error {
	if len(coreTeam) == 0 {
		return nil
	}
	if cfg == nil {
		return fmt.Errorf("core_team member %q is not a registered agent", coreTeam[0])
	}
	byID := make(map[string]*config.AgentConfig, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		byID[cfg.Agents.List[i].ID] = &cfg.Agents.List[i]
	}
	for _, id := range coreTeam {
		ac, ok := byID[id]
		if !ok {
			return fmt.Errorf("core_team member %q is not a registered agent", id)
		}
		if ac.IsSystem() {
			return fmt.Errorf(
				"core_team member %q is a System Agent and cannot be added to a workspace team roster", id)
		}
	}
	return nil
}

// RepairDanglingCoreTeamMembers is the first-class "drop dangling members"
// repair operation ADR-054 D6 rule 3 requires: an explicit way to clean up a
// core_team whose members no longer all resolve to a registered agent,
// without hand-editing the workspace's JSON file. It is the counterpart to
// validateCoreTeamMembers' reject-on-write behavior — where that function
// stops a write from INTRODUCING a dangling reference, this function
// removes ones that already got in (e.g. a workspace hand-edited outside the
// API, or created before this write path existed).
//
// Only true dangling references — an id with no matching entry in
// cfg.Agents.List at all — are dropped. A member id that resolves but is a
// System Agent (which validateCoreTeamMembers separately rejects on write) is
// NOT touched here: it is not "dangling" (the agent exists), so silently
// removing it would be a different, unrelated correction this operation does
// not claim to make. repaired preserves the original member order; dropped
// lists exactly which ids were removed (also in original order) so the
// caller can report/log/audit what changed. A nil/empty coreTeam, or a nil
// cfg (nothing to resolve against), returns the input unchanged with a nil
// dropped slice.
//
// Wiring a REST endpoint around this function (contract-first per
// Constraint #8: a new wire shape needs an openapi.yaml schema + regenerated
// types before any handler can use it) is left to the wave that owns the
// REST conversion — this function is the tested, ready-to-call primitive
// that endpoint would call.
func RepairDanglingCoreTeamMembers(cfg *config.Config, coreTeam []string) (repaired []string, dropped []string) {
	if len(coreTeam) == 0 {
		return coreTeam, nil
	}
	if cfg == nil {
		return nil, append([]string(nil), coreTeam...)
	}
	registered := make(map[string]struct{}, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		registered[cfg.Agents.List[i].ID] = struct{}{}
	}
	repaired = make([]string, 0, len(coreTeam))
	for _, id := range coreTeam {
		if _, ok := registered[id]; ok {
			repaired = append(repaired, id)
		} else {
			dropped = append(dropped, id)
		}
	}
	return repaired, dropped
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

// removeMailboxesForWorkspace removes every (agent, workspace) mailbox pair
// bound to workspaceID from config.mailboxes and deletes each removed pair's
// stored credential (M11 review-gate finding: the workspace-delete cascade
// handled cron jobs, sessions, milestones, tasks, and channel bindings but
// left mailboxes orphaned — bound to a workspace ID that no longer resolves).
//
// Structurally mirrors unbindChannelInstancesForWorkspace (identify bound
// keys from the live config, then rewrite the raw JSON map under
// safeUpdateConfigJSON), but — unlike that step, whose caller aborts the
// delete with 500 on a config-write failure — this step is BEST-EFFORT, like
// the milestone/heartbeat cascade steps earlier in handleWorkspaceDelete: a
// mailbox is a downstream feature of the workspace, not a binding that must
// stay consistent for the delete itself to be safe, so any failure here
// (config write, malformed on-disk entry, credential removal) is logged and
// the workspace delete proceeds regardless.
func removeMailboxesForWorkspace(a *restAPI, workspaceID string) {
	cfg := a.agentLoop.GetConfig()
	// Identify bound (agent, workspace) pairs from the live config.
	var boundAgentIDs []string
	for agentID, byWorkspace := range cfg.Mailboxes {
		if _, ok := byWorkspace[workspaceID]; ok {
			boundAgentIDs = append(boundAgentIDs, agentID)
		}
	}
	if len(boundAgentIDs) == 0 {
		return // nothing to do
	}
	sort.Strings(boundAgentIDs) // deterministic order for logging

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		mailboxes, _ := m["mailboxes"].(map[string]any)
		if mailboxes == nil {
			// No mailboxes section in raw JSON — nothing to remove.
			return nil
		}
		for _, agentID := range boundAgentIDs {
			normalized, err := mailboxAgentEntryToNested(mailboxes[agentID])
			if err != nil {
				// Malformed on-disk entry: skip this agent rather than aborting
				// the whole cascade (best-effort). setAgentMailbox/deleteAgentMailbox
				// will surface the malformed shape as a 500 on their own next write.
				slog.Warn("rest: workspace delete: cascade mailboxes: malformed entry, skipping",
					"agent_id", agentID, "workspace_id", workspaceID, "error", err)
				continue
			}
			delete(normalized, workspaceID)
			if len(normalized) == 0 {
				delete(mailboxes, agentID)
			} else {
				mailboxes[agentID] = normalized
			}
		}
		m["mailboxes"] = mailboxes

		slog.Info("rest: workspace delete: removed bound mailboxes",
			"workspace_id", workspaceID, "agent_ids", boundAgentIDs)
		return nil
	}); err != nil {
		slog.Warn("rest: workspace delete: cascade mailboxes: config write failed",
			"workspace_id", workspaceID, "agent_ids", boundAgentIDs, "error", err)
		return
	}

	// Config is durable now (or the write no-op'd because the section was
	// absent) — delete each removed pair's stored credential. A missing entry
	// is not an error (removeStoredCredential already tolerates that); log
	// only on a real failure.
	for _, agentID := range boundAgentIDs {
		if err := a.removeStoredCredential(mailboxCredKey(agentID, workspaceID)); err != nil {
			slog.Warn("rest: workspace delete: cascade mailboxes: credential removal failed",
				"agent_id", agentID, "workspace_id", workspaceID, "error", err)
		}
	}
}

// --- FR-043a / SC-017: the deleted workspace's browser profile ---------------

// browserProfileDeleteAttempts and browserProfileDeleteSettle bound the
// confirm-and-retry in deleteWorkspaceBrowserProfile.
//
// Four attempts and a short settle, not a long wait: the window being covered
// is a Chrome writing its last few files on the way down, which is milliseconds
// to low hundreds of milliseconds. The normal case costs neither — it confirms
// on the first pass and never sleeps at all. browserProfileDeleteSettle is a
// var rather than a const only so a test can shrink it.
const browserProfileDeleteAttempts = 4

var browserProfileDeleteSettle = 150 * time.Millisecond

// browserProfileExistsFn indirects the CONFIRM step's os.Stat.
//
// It exists for one reason: the condition this function was written for — a
// profile directory that comes BACK after a successful delete, because a Chrome
// that another goroutine was already shutting down was still writing its cookie
// jar into it — cannot be staged from a test without launching a real browser
// and winning a race against it. The seam lets a test stage it exactly, the way
// removeAllFn above stages a delete failure. The happy path deliberately does
// NOT use the seam in its own test, so the production Stat is exercised too.
var browserProfileExistsFn = func(dir string) bool {
	_, err := os.Stat(dir)
	return err == nil
}

// deleteWorkspaceBrowserProfile performs the FR-043a profile delete and then
// CONFIRMS it, retrying while the directory keeps coming back.
//
// The single-shot version — Close(key) then DeleteProfile(key), warn on error —
// had two ways to leave a departed client's live logins sitting on disk, and
// neither of them produced an error to warn about:
//
//   - MID-SHUTDOWN. Close(key) returns immediately when the pool's instances
//     map has no entry for the key. It has no entry when something else is
//     ALREADY tearing that browser down — the idle reaper's CloseIdle removes
//     the instance from the map first and only then runs the seconds-long
//     coordinator Shutdown. So Close returns while Chrome is still alive,
//     DeleteProfile sees no live instance and RemoveAll's the directory, and
//     the dying Chrome then recreates it and writes the cookie jar and Local
//     Storage it flushes on exit. DeleteProfile returned nil. The workspace is
//     gone and its logins are not.
//
//   - RE-ACQUIRED. A turn that started just before the delete can Acquire the
//     same key after Close returned, putting an instance back in the map;
//     DeleteProfile then refuses by design ("call Close first") and nothing
//     ever tried again.
//
// The fix is to stop trusting the delete's own return value as evidence and ask
// the filesystem. This is the whole point: DeleteProfile answering nil is a
// statement about one RemoveAll call, not about whether the directory is gone,
// and those two things differ in exactly the case that matters.
//
// The residual is named rather than hidden: a Chrome that recreates the
// directory AFTER the final confirming Stat still wins, and this returns nil.
// Closing that properly needs the pool to expose a per-key "shutdown finished"
// latch that Close can wait on — a pool.go change, out of this unit's scope —
// and is described in the R5 report. What this function guarantees is that the
// common orderings are handled and that a directory still standing at the end
// is REPORTED instead of silently accepted.
func deleteWorkspaceBrowserProfile(pool *browser.BrowserPool, key browser.BrowsingKey) error {
	dir, dirErr := pool.ProfileDirFor(key)

	var lastErr error
	for attempt := 1; attempt <= browserProfileDeleteAttempts; attempt++ {
		// Close first, every time. On the retry passes this is what handles
		// the re-acquired case: the instance that appeared after the previous
		// pass is torn down before the next DeleteProfile is asked.
		pool.Close(key)
		lastErr = pool.DeleteProfile(key)

		if dirErr != nil {
			// The key has no resolvable profile directory, so there is nothing
			// to confirm against and retrying cannot change that.
			return lastErr
		}
		if !browserProfileExistsFn(dir) {
			return nil
		}
		if attempt < browserProfileDeleteAttempts {
			time.Sleep(browserProfileDeleteSettle)
		}
	}

	if lastErr != nil {
		return fmt.Errorf("browser profile %s survived %d delete attempts: %w",
			dir, browserProfileDeleteAttempts, lastErr)
	}
	return fmt.Errorf(
		"browser profile directory %s still exists after %d delete attempts that each reported success — "+
			"a browser shutting down is most likely recreating it, and the deleted workspace's logins are "+
			"still on disk",
		dir, browserProfileDeleteAttempts)
}

// browserPoolFor reaches the per-workspace browser pool through the agent loop,
// tolerating both a nil loop and a pool that has not been built yet (a gateway
// booted with browser tools disabled never builds one). Extracted so the
// delete handler reads as one intention rather than three nil checks.
func browserPoolFor(a *restAPI) *browser.BrowserPool {
	if a == nil || a.agentLoop == nil {
		return nil
	}
	return a.agentLoop.BrowserPool()
}
