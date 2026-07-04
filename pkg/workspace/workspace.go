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
//	id, name, description, status, pinned, pin_order, core_team, repository,
//	owner, is_default, delegation, created_at, updated_at
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

	// Repository is an optional https:// URL for the associated git repo (SEC-5).
	Repository string `json:"repository,omitempty"`

	// Owner is the username of the user who created this workspace.
	// Attribution only — not an access gate (FR-1.9).
	Owner string `json:"owner,omitempty"`

	// IsDefault is true only for the auto-created default workspace (FR-1.6).
	IsDefault bool `json:"is_default,omitempty"`

	// Delegation is the per-workspace delegation graph (M5): the directed edges
	// that authorize who-delegates-to-whom on this workspace. This is the
	// editable source of truth surfaced in the workspace Team tab. nil/empty
	// means no delegation configured. The per-agent delegation_policy remains
	// the enforcement cap; this graph is what the UI and update_workspace edit.
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

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
