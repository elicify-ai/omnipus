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
//	owner, is_default, setup_pending, delegation, member_configs, mounts,
//	created_at, updated_at
//
// repository was deleted with no back-compat (FR-9.1, ADR-061 D7, matching
// the ADR-035/037 precedent) — do not reintroduce it. Git linkage is now a
// convenience on top of mounting: clone to an operator-chosen location, then
// mount it.
//
// Delegation is the typed form shared with delegation.go's DelegationEdge.
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

	// Delegation is the per-workspace delegation graph (M5): the directed edges
	// that authorize who-delegates-to-whom on this workspace. This is the
	// editable source of truth surfaced in the workspace Team tab. nil/empty
	// means no delegation configured. This graph is the SOLE delegation
	// enforcement mechanism (ADR-037) — there is no separate per-agent
	// delegation_policy; that field was removed entirely, and this graph is
	// both what the UI/update_workspace edit AND what the runtime enforces.
	//
	// DelegationEdge is defined in delegation.go and has the same JSON tags as
	// the former storedDelegationEdge in pkg/gateway; they are structurally
	// identical and now unified here.
	Delegation []DelegationEdge `json:"delegation,omitempty"`

	// MemberConfigs holds per-(workspace, agent) configuration keyed by agent
	// ID. Currently carries heartbeat settings (FR-001). Only agents present
	// in CoreTeam are valid keys; the gateway handler GCs stale entries when
	// CoreTeam shrinks (FR-022). omitempty so fresh workspaces serialize
	// without the field.
	MemberConfigs map[string]MemberConfig `json:"member_configs,omitempty"`

	// Mounts are this workspace's named write-grants on real local folders
	// (FR-5, ADR-061 D4). Each renders as a symlink work/<name> -> HostPath
	// (FR-5.4) and, on the app/kernel layers, an AllowedRoots write grant
	// (FR-6.1 — see AllowedMountRoots in mount.go). Mutated exclusively via
	// CreateMount/DeleteMount (mount.go), never via a direct field write,
	// so the symlink and the record can never drift apart. Status is NOT
	// stored here — it is always computed live from the filesystem
	// (MountStatus, FR-8.2/FR-8.5), never persisted, never stale.
	Mounts []Mount `json:"mounts,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
