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
// - Custom agents with an owner: owner OR admin may access.
// - Custom agents with an empty OwnerUsername (orphan): returns ErrAgentOrphan.
// Callers (Lanes B5/B6) translate this to HTTP 503.
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
	// Owner or admin may access.
	if user.Role == UserRoleAdmin || user.Username == agent.OwnerUsername {
		return nil
	}
	return errors.New("access denied: agent belongs to another user")
}
