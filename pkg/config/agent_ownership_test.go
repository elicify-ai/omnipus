//go:build !cgo

// agent_ownership_test.go — AuthorizeAgentAccess unit tests.
//
// Covers the per-user data-privacy fix: under the single-user model every
// authenticated user resolves to UserRoleAdmin (see UserRole.UnmarshalJSON in
// config.go), but that collapse is about removing admin-only GATING on
// settings screens — NOT about collapsing per-account data isolation. A
// second account created via the Users management screen (POST
// /api/v1/users) must not see another account's private custom agents just
// because it also carries Role==admin.
//
// AuthorizeAgentAccess is the single root-cause chokepoint: rest_workspace.go
// (agent workspace file access) and schedules.go's authorizeScheduleOwner
// both call it directly with no admin-bypass of their own, so proving it
// here proves those callers correct by construction.
//
// Traces to: architect finding on feat/agent-system-fixes-2 (7-reviewer
// gate) — "AuthorizeAgentAccess's user.Role == UserRoleAdmin OR clause is
// unconditionally true under the single-user model, defeating per-user
// agent/workspace/schedule privacy." Operator decision: restore per-user
// privacy (2026-07).
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthorizeAgentAccess_OwnerOnly_NoAdminBypass proves the core fix: a
// second user who also resolves to UserRoleAdmin (single-user model) is
// still denied access to a custom agent they do not own. Before the fix, the
// `user.Role == UserRoleAdmin` OR-clause made this unconditionally true and
// admin2 could read admin1's private agent.
func TestAuthorizeAgentAccess_OwnerOnly_NoAdminBypass(t *testing.T) {
	agent := &AgentConfig{
		ID:            "alices-agent",
		Type:          AgentTypeCustom,
		OwnerUsername: "alice",
	}

	owner := &UserConfig{Username: "alice", Role: UserRoleAdmin}
	require.NoError(t, AuthorizeAgentAccess(owner, agent),
		"the owner must always be able to access their own agent")

	secondAdmin := &UserConfig{Username: "bob", Role: UserRoleAdmin}
	err := AuthorizeAgentAccess(secondAdmin, agent)
	require.Error(t, err,
		"a second account resolving to admin must NOT bypass ownership and read alice's private agent")
	assert.Contains(t, err.Error(), "another user")
}

// TestAuthorizeAgentAccess_SystemAndCoreAgents_AlwaysAccessible proves the
// system/core carve-out is unaffected by the ownership fix: any
// authenticated user (owner or not, admin or not) may access system/core
// agents, which never carry an OwnerUsername.
func TestAuthorizeAgentAccess_SystemAndCoreAgents_AlwaysAccessible(t *testing.T) {
	for _, typ := range []AgentType{AgentTypeSystem, AgentTypeCore} {
		agent := &AgentConfig{ID: "mia", Type: typ}
		user := &UserConfig{Username: "anyone", Role: UserRoleAdmin}
		assert.NoError(t, AuthorizeAgentAccess(user, agent),
			"type=%s must be accessible to any authenticated user", typ)
	}
}

// TestAuthorizeAgentAccess_OrphanAgent_ReturnsSentinel proves the orphan
// sentinel (empty OwnerUsername on a custom agent) is preserved by the fix —
// it is returned regardless of the caller's role, including admin.
func TestAuthorizeAgentAccess_OrphanAgent_ReturnsSentinel(t *testing.T) {
	agent := &AgentConfig{ID: "orphaned", Type: AgentTypeCustom, OwnerUsername: ""}
	user := &UserConfig{Username: "anyone", Role: UserRoleAdmin}
	err := AuthorizeAgentAccess(user, agent)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentOrphan)
}

// TestAuthorizeAgentAccess_WorkerAgent_OwnerOnly proves the fix applies to
// worker-tier agents too (AgentTypeWorker is not a system/core type, so it
// goes through the same ownership branch as AgentTypeCustom).
func TestAuthorizeAgentAccess_WorkerAgent_OwnerOnly(t *testing.T) {
	agent := &AgentConfig{ID: "max-worker", Type: AgentTypeWorker, OwnerUsername: "bob"}

	owner := &UserConfig{Username: "bob", Role: UserRoleAdmin}
	assert.NoError(t, AuthorizeAgentAccess(owner, agent))

	other := &UserConfig{Username: "alice", Role: UserRoleAdmin}
	assert.Error(t, AuthorizeAgentAccess(other, agent))
}
