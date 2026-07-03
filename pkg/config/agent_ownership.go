// agent_ownership.go implements agent ownership helpers.

package config

import "errors"

// ErrAgentOrphan is returned by AuthorizeAgentAccess when a custom agent has an
// empty OwnerUsername. Callers should translate this to HTTP 503 with a message
// explaining that the agent has no owner and must be reassigned by an admin.
// FR-093a
var ErrAgentOrphan = errors.New("agent has no owner; reassign via PATCH /api/v1/agents/<id>")

// IsSystemAgent reports whether agent a is a system-level agent.
// System agents (type "system" or "core") are accessible to any authenticated
// user regardless of ownership. They must never have an OwnerUsername set.
func IsSystemAgent(a *AgentConfig) bool {
	return a.Type == AgentTypeSystem || a.Type == AgentTypeCore
}

// RequiresOwner reports whether agent a must have a non-empty OwnerUsername.
// Custom agents (and unclassified agents that default to custom) require an
// owner. System and core agents do not.
func RequiresOwner(a *AgentConfig) bool {
	return !IsSystemAgent(a)
}

// AuthorizeAgentAccess returns nil if user is permitted to access agent.
//
// Access rules:
// - System/core agents: any authenticated user may access.
// - Custom agents with an owner: owner ONLY may access.
// - Custom agents with an empty OwnerUsername (orphan): returns ErrAgentOrphan.
// Callers (Lanes B5/B6) translate this to HTTP 503.
//
// Ownership is intentionally NOT bypassed by UserRoleAdmin. Under the
// single-user model every authenticated user resolves to UserRoleAdmin (see
// UserRole.UnmarshalJSON in config.go) — that collapse is about removing
// admin-only GATING on settings screens for the single (default) account, not
// about collapsing per-account data isolation. A second account can still be
// created via the Users management screen (POST /api/v1/users) today, and
// such an account must NOT see another user's private custom agents,
// workspaces, or schedules just because it also carries Role==admin — an
// admin-bypass here would silently defeat per-user privacy for anyone who
// creates a second account. (Operator decision, 2026-07: restore per-user
// privacy — see agent_ownership_migration.go / the single-user-model ADR
// history for the "single-user" framing this narrows.)
func AuthorizeAgentAccess(user *UserConfig, agent *AgentConfig) error {
	if IsSystemAgent(agent) {
		// System/core agents are accessible to any authenticated user.
		return nil
	}
	// Custom agent.
	if agent.OwnerUsername == "" {
		// Orphan: no owner assigned yet. Return sentinel so callers can return 503.
		return ErrAgentOrphan
	}
	// Ownership only — see the no-admin-bypass rationale above.
	if user.Username == agent.OwnerUsername {
		return nil
	}
	return errors.New("access denied: agent belongs to another user")
}
