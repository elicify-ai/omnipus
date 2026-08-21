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
	s.Record("session-1", "agent-a", "exec", nil)

	if !s.IsAllowed("session-1", "agent-a", "exec", nil) {
		t.Error("IsAllowed(S, A, T) after Record(S, A, T) must be true")
	}
	if s.IsAllowed("session-1", "agent-b", "exec", nil) {
		t.Error("a DIFFERENT agent in the same session must NOT inherit the grant")
	}
	if s.IsAllowed("session-2", "agent-a", "exec", nil) {
		t.Error("the same agent in a DIFFERENT session must NOT reuse the grant")
	}
	if s.IsAllowed("session-1", "agent-a", "other_tool", nil) {
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

	hook1.Record("session-1", "agent-a", "exec", nil)

	if !hook2.IsAllowed("session-1", "agent-a", "exec", nil) {
		t.Error(
			"a grant recorded via one connection's hook must be visible via a reconnected hook sharing the same store",
		)
	}
}

// TestApprovalGrantStore_Inherit verifies delegation inheritance: a child
// agent inherits the parent's grant set at spawn time (copy-at-spawn,
// union not replace), scoped to the tool(s) actually granted.
//
// ADR-057 FR-031 retired the single-key Inherit in favor of the two-key
// InheritFrom (pkg/agent/subturn.go, unit U7); same-session (src==dst) is
// still a valid same-session-delegation fixture shape, so this test is
// adapted to call InheritFrom directly with sessionID repeated as both
// source and destination — byte-identical to what the retired shim itself
// did. See approvalgrants_adr057_test.go for the new two-key coverage
// (TestApprovalGrants_InheritFromTwoKeys et al.), which is the primary
// regression coverage for the two-key contract going forward.
func TestApprovalGrantStore_Inherit(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "parent", "exec", nil)

	s.InheritFrom("session-1", "parent", "session-1", "child")

	if !s.IsAllowed("session-1", "child", "exec", nil) {
		t.Error("child must inherit the parent's granted tool")
	}
	if s.IsAllowed("session-1", "child", "other_tool", nil) {
		t.Error("child must NOT inherit a grant the parent never had")
	}
}

// TestApprovalGrantStore_InheritNoParentGrant verifies a parent with no
// grants produces no grants on the child (no accidental universal-allow).
func TestApprovalGrantStore_InheritNoParentGrant(t *testing.T) {
	s := NewApprovalGrantStore()

	s.InheritFrom("session-1", "parent", "session-1", "child")

	if s.IsAllowed("session-1", "child", "exec", nil) {
		t.Error("child must inherit nothing when the parent has no grants")
	}
}

// TestApprovalGrantStore_InheritPreservesChildOwnGrants verifies InheritFrom
// is a union, not a replace: a grant the child already holds independently
// survives inheriting the parent's (different) grants.
func TestApprovalGrantStore_InheritPreservesChildOwnGrants(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "child", "read_file", nil)
	s.Record("session-1", "parent", "exec", nil)

	s.InheritFrom("session-1", "parent", "session-1", "child")

	if !s.IsAllowed("session-1", "child", "read_file", nil) {
		t.Error("child's own pre-existing grant must survive InheritFrom")
	}
	if !s.IsAllowed("session-1", "child", "exec", nil) {
		t.Error("child must also gain the parent's grant")
	}
}

// TestApprovalGrantStore_FailSafeEmptyKeys verifies the consent-boundary
// fail-safe: an empty session_id or agent_id must NEVER be treated as a
// valid scoping key, in either direction (Record or IsAllowed).
func TestApprovalGrantStore_FailSafeEmptyKeys(t *testing.T) {
	s := NewApprovalGrantStore()

	if s.IsAllowed("", "agent-a", "exec", nil) {
		t.Error("empty session_id must never be allowed")
	}
	if s.IsAllowed("session-1", "", "exec", nil) {
		t.Error("empty agent_id must never be allowed")
	}
	if s.IsAllowed("session-1", "agent-a", "", nil) {
		t.Error("empty tool must never be allowed")
	}

	// Recording under an empty key must not create a cross-caller collision:
	// two unrelated callers that both pass an empty session_id must NOT
	// match each other.
	s.Record("", "agent-a", "exec", nil)
	if s.IsAllowed("", "agent-a", "exec", nil) {
		t.Error("a grant recorded under an empty session_id must never be retrievable")
	}

	s.Record("session-1", "", "exec", nil)
	if s.IsAllowed("session-1", "", "exec", nil) {
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

	if s.IsAllowed("session-1", "agent-a", "exec", nil) {
		t.Error("nil store must never report a grant as allowed")
	}
	s.Record("session-1", "agent-a", "exec", nil)              // must be a no-op, not a panic
	s.InheritFrom("session-1", "parent", "session-1", "child") // must be a no-op, not a panic
	s.ClearSession("session-1")                                // must be a no-op, not a panic
	if s.IsAllowed("session-1", "agent-a", "exec", nil) {
		t.Error("nil store must still report false after no-op writes")
	}
}

// TestApprovalGrantStore_ClearSession verifies ClearSession removes every
// grant for the given session (across all agents) while leaving other
// sessions' grants intact — no unbounded growth, no cross-session leak.
func TestApprovalGrantStore_ClearSession(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "agent-a", "exec", nil)
	s.Record("session-1", "agent-b", "read_file", nil)
	s.Record("session-2", "agent-a", "exec", nil)

	s.ClearSession("session-1")

	if s.IsAllowed("session-1", "agent-a", "exec", nil) {
		t.Error("ClearSession must remove agent-a's grant in session-1")
	}
	if s.IsAllowed("session-1", "agent-b", "read_file", nil) {
		t.Error("ClearSession must remove agent-b's grant in session-1")
	}
	if !s.IsAllowed("session-2", "agent-a", "exec", nil) {
		t.Error("ClearSession must NOT touch grants belonging to a different session")
	}
}

// TestApprovalGrantStore_ArgsAreExactMatch is the locked UAT decision:
// Always Allow remembers the WHOLE arguments object. A grant for
// bash {command:ls} must not approve bash {command:rm -rf /}.
func TestApprovalGrantStore_ArgsAreExactMatch(t *testing.T) {
	s := NewApprovalGrantStore()
	ls := map[string]any{"command": "ls"}
	rm := map[string]any{"command": "rm -rf /"}

	if !s.Record("session-1", "agent-a", "bash", ls) {
		t.Fatal("Record of ls must succeed")
	}
	if !s.IsAllowed("session-1", "agent-a", "bash", ls) {
		t.Error("same tool, same args must be allowed")
	}
	if s.IsAllowed("session-1", "agent-a", "bash", rm) {
		t.Error("same tool, different args must NOT be allowed")
	}

	if !s.Record("session-1", "agent-a", "bash", rm) {
		t.Fatal("a second Record of the same tool with different args must union, not replace")
	}
	if !s.IsAllowed("session-1", "agent-a", "bash", ls) {
		t.Error("union: the first fingerprint must still match after a second Record")
	}
	if !s.IsAllowed("session-1", "agent-a", "bash", rm) {
		t.Error("union: the second fingerprint must also match")
	}
}

// TestApprovalGrantStore_KeyOrderDoesNotMatter proves encoding/json's
// sorted-key marshal is the fingerprint: swapped object keys are the same grant.
func TestApprovalGrantStore_KeyOrderDoesNotMatter(t *testing.T) {
	s := NewApprovalGrantStore()
	if !s.Record("session-1", "agent-a", "bash", map[string]any{"a": 1, "b": 2}) {
		t.Fatal("Record must succeed")
	}
	if !s.IsAllowed("session-1", "agent-a", "bash", map[string]any{"b": 2, "a": 1}) {
		t.Error("same object with keys swapped must fingerprint identically")
	}
}

// TestApprovalGrantStore_NilAndEmptyArgsAreTheSameGrant: a no-arg tool
// recorded with nil args must match a later empty-map call, and vice versa.
func TestApprovalGrantStore_NilAndEmptyArgsAreTheSameGrant(t *testing.T) {
	s := NewApprovalGrantStore()
	if !s.Record("session-1", "agent-a", "ping", nil) {
		t.Fatal("Record(nil) must succeed — empty args is a real no-arg grant")
	}
	if !s.IsAllowed("session-1", "agent-a", "ping", map[string]any{}) {
		t.Error("nil Record must match an empty-map IsAllowed")
	}

	s2 := NewApprovalGrantStore()
	if !s2.Record("session-1", "agent-a", "ping", map[string]any{}) {
		t.Fatal("Record(empty map) must succeed")
	}
	if !s2.IsAllowed("session-1", "agent-a", "ping", nil) {
		t.Error("empty-map Record must match a nil IsAllowed")
	}
}

// TestApprovalGrantStore_RequestMountPathADoesNotGrantPathB is the
// product-level consequence: Always Allow on Add folder means "this folder,
// this session", not "any folder".
func TestApprovalGrantStore_RequestMountPathADoesNotGrantPathB(t *testing.T) {
	s := NewApprovalGrantStore()
	pathA := map[string]any{"host_path": "/Users/dana/Documents/projects/api"}
	pathB := map[string]any{"host_path": "/Users/dana/Secrets"}

	if !s.Record("session-1", "agent-a", "request_mount", pathA) {
		t.Fatal("Record of path A must succeed")
	}
	if !s.IsAllowed("session-1", "agent-a", "request_mount", pathA) {
		t.Error("path A must be allowed after Always Allow on path A")
	}
	if s.IsAllowed("session-1", "agent-a", "request_mount", pathB) {
		t.Error("path B must NOT be auto-approved by a grant for path A")
	}
}

// TestApprovalGrantStore_InheritFromCopiesFingerprints: a child inherits
// the parent's exact argument fingerprints, not a blanket tool-name grant.
func TestApprovalGrantStore_InheritFromCopiesFingerprints(t *testing.T) {
	s := NewApprovalGrantStore()
	ls := map[string]any{"command": "ls"}
	rm := map[string]any{"command": "rm -rf /"}
	if !s.Record("session-1", "parent", "bash", ls) {
		t.Fatal("Record must succeed")
	}

	s.InheritFrom("session-1", "parent", "session-1", "child")

	if !s.IsAllowed("session-1", "child", "bash", ls) {
		t.Error("child must inherit the parent's fingerprint")
	}
	if s.IsAllowed("session-1", "child", "bash", rm) {
		t.Error("child must NOT inherit a blanket grant for other args of the same tool")
	}
}

// TestApprovalGrantStore_MarshalFailureFailsClosed: args that cannot be
// encoded are never stored and never match.
func TestApprovalGrantStore_MarshalFailureFailsClosed(t *testing.T) {
	s := NewApprovalGrantStore()
	bad := map[string]any{"ch": make(chan int)}

	if s.Record("session-1", "agent-a", "bash", bad) {
		t.Error("Record must return false when args cannot be marshaled")
	}
	if s.IsAllowed("session-1", "agent-a", "bash", bad) {
		t.Error("IsAllowed must return false when args cannot be marshaled")
	}
	if s.IsAllowed("session-1", "agent-a", "bash", nil) {
		t.Error("a failed Record must not have stored any grant")
	}
}
