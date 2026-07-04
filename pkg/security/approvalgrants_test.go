// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security

import "testing"

// TestApprovalGrantStore_ScopedByAgentAndSession verifies the core scoping
// invariant: a grant recorded for (session, agent, tool) applies ONLY to
// that exact triple — a different agent in the same session, or the same
// agent in a different session, must still be asked.
func TestApprovalGrantStore_ScopedByAgentAndSession(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "agent-a", "exec")

	if !s.IsAllowed("session-1", "agent-a", "exec") {
		t.Error("IsAllowed(S, A, T) after Record(S, A, T) must be true")
	}
	if s.IsAllowed("session-1", "agent-b", "exec") {
		t.Error("a DIFFERENT agent in the same session must NOT inherit the grant")
	}
	if s.IsAllowed("session-2", "agent-a", "exec") {
		t.Error("the same agent in a DIFFERENT session must NOT reuse the grant")
	}
	if s.IsAllowed("session-1", "agent-a", "other_tool") {
		t.Error("a grant for one tool must not apply to a different tool")
	}
}

// TestApprovalGrantStore_SurvivesReconnect is the direct regression test for
// the bug: two "hook instances" (simulating two WebSocket connections across
// a reconnect) sharing the same AgentLoop-owned store must see the same
// grant — recorded via "hook1", visible via "hook2".
func TestApprovalGrantStore_SurvivesReconnect(t *testing.T) {
	// Both "hooks" hold a reference to the SAME store, exactly as both would
	// via al.ApprovalGrants() on the shared AgentLoop, unlike the old
	// per-connection alwaysAllowed map that a fresh hook could never see.
	shared := NewApprovalGrantStore()
	hook1 := shared
	hook2 := shared

	hook1.Record("session-1", "agent-a", "exec")

	if !hook2.IsAllowed("session-1", "agent-a", "exec") {
		t.Error("a grant recorded via one connection's hook must be visible via a reconnected hook sharing the same store")
	}
}

// TestApprovalGrantStore_Inherit verifies delegation inheritance: a child
// agent inherits the parent's grant set at spawn time (copy-at-spawn,
// union not replace), scoped to the tool(s) actually granted.
func TestApprovalGrantStore_Inherit(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "parent", "exec")

	s.Inherit("session-1", "parent", "child")

	if !s.IsAllowed("session-1", "child", "exec") {
		t.Error("child must inherit the parent's granted tool")
	}
	if s.IsAllowed("session-1", "child", "other_tool") {
		t.Error("child must NOT inherit a grant the parent never had")
	}
}

// TestApprovalGrantStore_InheritNoParentGrant verifies a parent with no
// grants produces no grants on the child (no accidental universal-allow).
func TestApprovalGrantStore_InheritNoParentGrant(t *testing.T) {
	s := NewApprovalGrantStore()

	s.Inherit("session-1", "parent", "child")

	if s.IsAllowed("session-1", "child", "exec") {
		t.Error("child must inherit nothing when the parent has no grants")
	}
}

// TestApprovalGrantStore_InheritPreservesChildOwnGrants verifies Inherit is
// a union, not a replace: a grant the child already holds independently
// survives inheriting the parent's (different) grants.
func TestApprovalGrantStore_InheritPreservesChildOwnGrants(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "child", "read_file")
	s.Record("session-1", "parent", "exec")

	s.Inherit("session-1", "parent", "child")

	if !s.IsAllowed("session-1", "child", "read_file") {
		t.Error("child's own pre-existing grant must survive Inherit")
	}
	if !s.IsAllowed("session-1", "child", "exec") {
		t.Error("child must also gain the parent's grant")
	}
}

// TestApprovalGrantStore_FailSafeEmptyKeys verifies the consent-boundary
// fail-safe: an empty session_id or agent_id must NEVER be treated as a
// valid scoping key, in either direction (Record or IsAllowed).
func TestApprovalGrantStore_FailSafeEmptyKeys(t *testing.T) {
	s := NewApprovalGrantStore()

	if s.IsAllowed("", "agent-a", "exec") {
		t.Error("empty session_id must never be allowed")
	}
	if s.IsAllowed("session-1", "", "exec") {
		t.Error("empty agent_id must never be allowed")
	}
	if s.IsAllowed("session-1", "agent-a", "") {
		t.Error("empty tool must never be allowed")
	}

	// Recording under an empty key must not create a cross-caller collision:
	// two unrelated callers that both pass an empty session_id must NOT
	// match each other.
	s.Record("", "agent-a", "exec")
	if s.IsAllowed("", "agent-a", "exec") {
		t.Error("a grant recorded under an empty session_id must never be retrievable")
	}

	s.Record("session-1", "", "exec")
	if s.IsAllowed("session-1", "", "exec") {
		t.Error("a grant recorded under an empty agent_id must never be retrievable")
	}
}

// TestApprovalGrantStore_NilStoreNeverPanicsNeverAutoApproves verifies that
// every method is safe to call on a nil *ApprovalGrantStore — a nil store
// must fail closed (IsAllowed => false) rather than panic or auto-approve.
func TestApprovalGrantStore_NilStoreNeverPanicsNeverAutoApproves(t *testing.T) {
	var s *ApprovalGrantStore // nil

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil *ApprovalGrantStore must never panic, got: %v", r)
		}
	}()

	if s.IsAllowed("session-1", "agent-a", "exec") {
		t.Error("nil store must never report a grant as allowed")
	}
	s.Record("session-1", "agent-a", "exec")  // must be a no-op, not a panic
	s.Inherit("session-1", "parent", "child") // must be a no-op, not a panic
	s.ClearSession("session-1")               // must be a no-op, not a panic
	if s.IsAllowed("session-1", "agent-a", "exec") {
		t.Error("nil store must still report false after no-op writes")
	}
}

// TestApprovalGrantStore_ClearSession verifies ClearSession removes every
// grant for the given session (across all agents) while leaving other
// sessions' grants intact — no unbounded growth, no cross-session leak.
func TestApprovalGrantStore_ClearSession(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "agent-a", "exec")
	s.Record("session-1", "agent-b", "read_file")
	s.Record("session-2", "agent-a", "exec")

	s.ClearSession("session-1")

	if s.IsAllowed("session-1", "agent-a", "exec") {
		t.Error("ClearSession must remove agent-a's grant in session-1")
	}
	if s.IsAllowed("session-1", "agent-b", "read_file") {
		t.Error("ClearSession must remove agent-b's grant in session-1")
	}
	if !s.IsAllowed("session-2", "agent-a", "exec") {
		t.Error("ClearSession must NOT touch grants belonging to a different session")
	}
}
