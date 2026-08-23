// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

// Workspace is the canonical on-disk representation of a workspace record
// (~/.omnipus/workspaces/<id>.json). It is the single source of truth for the
// serialized format and is shared by pkg/gateway (REST CRUD) and
// pkg/sysagent/tools (update_workspace / create_workspace / etc.) so that
// both write paths stay byte-for-byte compatible and neither can silently drop
// fields written by the other.
//
// JSON tag rules (must stay stable — the files are the long-term store):
//
//	id, name, description, status, pinned, pin_order, core_team,
//	owner, is_default, setup_pending, member_configs,
//	created_at, updated_at
//
// `mounts` was REMOVED from this record and must not return — it is a write
// grant and this file is writable by a sandboxed child. See the note where the
// field used to be, and mountstore.go.
//
// `delegation` was REMOVED for the same reason — it is an AUTHORIZATION and
// this file is writable by a sandboxed child. See the note where that field
// used to be, and delegationstore.go.
//
// repository was deleted with no back-compat (FR-9.1, ADR-063 D7, matching
// the ADR-035/037 precedent) — do not reintroduce it. Git linkage is now a
// convenience on top of mounting: clone to an operator-chosen location, then
// mount it.
//
// Adding a new field here requires a matching JSON tag and must be
// backward-compatible (omitempty on optional fields so old files still parse).
type Workspace struct { // not-wire-format: internal disk-cache struct, mapped to gen.Workspace before sending over the wire
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"` // "active" | "archived"
	Pinned      bool   `json:"pinned"`
	PinOrder    int    `json:"pin_order"` // 0 = unpinned

	// CoreTeam is the list of agent IDs associated with this workspace.
	CoreTeam []string `json:"core_team,omitempty"`

	// Owner is the username of the user who created this workspace.
	// Attribution only — not an access gate (FR-1.9).
	Owner string `json:"owner,omitempty"`

	// IsDefault is true only for the auto-created default workspace (FR-1.6).
	IsDefault bool `json:"is_default,omitempty"`

	// SetupPending is true while this workspace's initial team-setup interview
	// has not yet run. Set server-side at creation when the default (Ava-only)
	// roster was auto-seeded (handleWorkspacePost's implicit-core_team branch);
	// cleared server-side when the setup kickoff turn is accepted (the
	// metadata.workspace_setup_kickoff MessageFrame flag, see contracts/asyncapi.yaml).
	// Always false for the auto-created default workspace (ensureDefaultWorkspace)
	// and for any workspace created with an explicit core_team.
	SetupPending bool `json:"setup_pending,omitempty"`

	// NOTE — delegation edges are deliberately NOT a field here, and must never
	// become one again. A delegation edge is an AUTHORIZATION, and per ADR-037
	// the per-workspace edge list is the SOLE runtime authority for
	// who-may-delegate-to-whom (the global AgentConfig.DelegationPolicy was
	// deleted precisely so this one gate could not be bypassed). That makes the
	// edge list a security decision input, and this record is writable by the
	// principal it constrains: the kernel policy grants $OMNIPUS_HOME RWX and
	// fspolicy.DeniedPathsFor re-admits the whole `workspaces` root for any
	// re-rooted workspace turn, so `bash` can append to its own workspace
	// record. Storing the edge list here let a child append
	// {"from_agent":"me","to_agent":"anyone"} and grant ITSELF delegation to any
	// agent on the workspace. Re-validating on load does not close it — a
	// hostile edge is SHAPE-LEGAL and indistinguishable from an operator-created
	// one; the list has to be unreachable. It lives in
	// $OMNIPUS_HOME/entities/delegation/<id>.json, inside the
	// kernel-and-app-layer denied `entities` root. See delegationstore.go's
	// leading comment.
	//
	// Delegation remains on the WIRE unchanged (gen.WorkspaceDelegation, the
	// GET/PUT /workspaces/{id}/delegation pair) — pkg/gateway sources it from
	// workspace.LoadDelegation and persists it with workspace.SaveDelegation.
	// Only the storage location changed; contracts/ is untouched.
	//
	// An old record still carrying a `delegation` array (or one a child plants
	// there) is IGNORED by encoding/json on load and dropped on the next save.
	// That is the intended migration: importing those edges into the protected
	// store would launder exactly the attacker-controlled data this move exists
	// to distrust. WHAT AN EXISTING INSTALL LOSES: a workspace upgraded across
	// this change starts with an EMPTY delegation graph — delegation is
	// deny-by-default, so every delegation on that workspace is refused until an
	// operator re-saves the graph from the Team tab (PUT
	// /api/v1/workspaces/{id}/delegation). That is the fail-closed direction and
	// it is deliberate; a fresh install is unaffected (create-time seeding writes
	// straight to the store).

	// MemberConfigs holds per-(workspace, agent) configuration keyed by agent
	// ID. Currently carries heartbeat settings (FR-001). Only agents present
	// in CoreTeam are valid keys; the gateway handler GCs stale entries when
	// CoreTeam shrinks (FR-022). omitempty so fresh workspaces serialize
	// without the field.
	MemberConfigs map[string]MemberConfig `json:"member_configs,omitempty"`

	// NOTE — mounts are deliberately NOT a field here, and must never become
	// one again. A mount is a WRITE GRANT, and this record is writable by a
	// sandboxed child: the kernel policy grants $OMNIPUS_HOME RWX, and
	// fspolicy.DeniedPathsFor re-admits the whole `workspaces` root for any
	// re-rooted workspace turn, so `bash` can append to its own workspace
	// record. Storing the grant list here let a child mount "/" for itself and
	// then write anywhere — the exact defeat of ADR-062's default-deny-writes
	// property. Re-validating on load does not close it, because host_path "/"
	// is a legitimate operator-reachable value (FR-7.6 warn-and-allow); the
	// list has to be unreachable. It lives in
	// $OMNIPUS_HOME/entities/mounts/<id>.json, inside the kernel-and-app-layer
	// denied `entities` root. See mountstore.go's leading comment.
	//
	// Mounts remain on the WIRE (gen.Workspace.Mounts) — pkg/gateway's
	// workspaceToWire sources them from workspace.LoadMounts. Only the storage
	// location changed.
	//
	// An old record still carrying a `mounts` array (or one a child plants
	// there) is IGNORED by encoding/json on load and dropped on the next save.
	// That is the intended migration: importing those entries into the
	// protected store would launder exactly the attacker-controlled data this
	// move exists to distrust.

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
