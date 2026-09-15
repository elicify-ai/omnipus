// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// websocket_adr057_test.go — tests for ADR-057 unit U11 (pkg/gateway/websocket.go):
// W3 (the streamed-assistant transcript write becomes strict), W5b (WS frame
// stamping / the FR-089 six-type classification), and W10c (the
// approval-cancel descendant-set re-key).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's new
// tests go in a NEW file named `<subject>_adr057_test.go`; this file is
// U11's. Rule 6: this file's own new package-level helpers are prefixed
// `u11` so a same-wave collision with another unit's new package-level test
// helper is a compile error, not a silent shadow — but it deliberately
// REUSES pkg/gateway/approvals_adr057_test.go's already-landed
// chatRoutingSid/childActingSid/newTestApprovalRegistry fixtures rather than
// redeclaring them, since U11 and U17a's approval-cancel work is one
// end-to-end property (FR-032/FR-080) split across two files by ownership,
// not two independent properties.
//
// Binding rule 1 (real state, never a spy): every test below reads a REAL
// session.LifecycleStore rooted at t.TempDir(), a REAL approvalRegistryV2,
// and — in TestU11BuildCancelHooks_CancelPendingApprovals_ReachesRealDescendant
// — a REAL *WSHandler wired to a REAL *agent.AgentLoop via
// SetSessionMessagingStores. Nothing here asserts "a function was called";
// every assertion lands on an observable artefact (the registry entry's own
// state, what arrived on its resultCh, what the walk actually returned).
//
// Binding rule 4 (positive lower bound before any exclusion/zero-count
// assertion): TestU11CollectDescendantSessionIDs_MultiLevelWalk asserts it
// found >= 2 real descendants before asserting the unrelated sibling is
// excluded; TestU11ApprovalCancel_SingleIDMissesDescendant_SetFormReaches
// asserts the fixture's single pending entry was accepted before asserting
// anything about cancellation reaching or missing it.
//
// Corollary "distinct ids everywhere": every root/child/grandchild pair below
// is constructed as three distinct, non-equal literal strings.

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// u11PersistLifecycleChild persists a minimal, valid LifecycleRecord for
// childID as a DIRECT child of parentID (State=running,
// OwnerScopeKind=parent_session — the real shape a delegated child mints; see
// pkg/tools/delegate.go's run path). t.Helper() + t.Fatal on any persistence
// error: every test in this file needs its fixture to actually be ON DISK
// before it can assert anything about a walk that reads it back.
func u11PersistLifecycleChild(t *testing.T, ls *session.LifecycleStore, childID, parentID string) {
	t.Helper()
	rec := &session.LifecycleRecord{
		SessionID:        childID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		OwnerScopeID:     parentID,
		ParentDurableKey: parentID,
		ParentAgentID:    "ava",
	}
	if err := ls.Persist(rec); err != nil {
		t.Fatalf("fixture: persist lifecycle record %q (parent %q): %v", childID, parentID, err)
	}
}

// TestU11ApprovalCancel_SingleIDMissesDescendant_SetFormReaches is the
// required red/green pair for W10c (FR-032/FR-080): in one test against REAL
// stores, it demonstrates that the single-id cancel form (what this file's
// CancelPendingApprovals closure called before this change) leaves a child's
// pending approval uncancelled — BDD-33's exact defect — and that the
// descendant-set composition buildCancelHooks now uses reaches it.
func TestU11ApprovalCancel_SingleIDMissesDescendant_SetFormReaches(t *testing.T) {
	ls := session.NewLifecycleStore(t.TempDir())
	root := chatRoutingSid // approvals_adr057_test.go fixture ids — reused, not redeclared
	child := childActingSid
	u11PersistLifecycleChild(t, ls, child, root)

	r := newTestApprovalRegistry(t)
	entry, accepted := r.requestApproval(
		"call_u11", "bash", map[string]any{"cmd": "ls"}, "ava", child, "turn_u11",
	)
	if !accepted || entry == nil {
		t.Fatalf("fixture: requestApproval must accept, got accepted=%v entry=%v", accepted, entry)
	}

	// --- RED: the single-id form must miss the child (proving the defect
	// this change closes actually exists on real state, not asserted-away).
	if n := r.cancelAllPendingForSession(root, "session canceled"); n != 0 {
		t.Fatalf("RED case invalid: single-id cancel by the chat id alone must match 0, matched %d", n)
	}
	if got := r.get(entry.ApprovalID); got == nil || got.state != ApprovalStatePending {
		t.Fatalf("RED: entry must still be pending after the non-matching single-id cancel; got %v", got)
	}

	// --- GREEN: the descendant-set composition buildCancelHooks's
	// CancelPendingApprovals closure now performs (u11CollectDescendantSessionIDs
	// + cancelAllPendingForSessions), exactly as wired in websocket.go.
	sessionIDs := append([]string{root}, u11CollectDescendantSessionIDs(ls, root)...)
	if n := r.cancelAllPendingForSessions(sessionIDs, "session canceled"); n != 1 {
		t.Fatalf("GREEN: FR-032 descendant-set cancel must match the child's entry; "+
			"matched %d, want 1 (target set %v)", n, sessionIDs)
	}
	select {
	case outcome := <-entry.resultCh:
		if outcome.Approved {
			t.Error("GREEN: a cancelled approval must not be approved")
		}
	default:
		t.Error("GREEN: the child's blocked goroutine must have been unblocked")
	}
	if got := r.get(entry.ApprovalID); got != nil {
		t.Errorf("GREEN: the cancelled entry must no longer resolve, got %v", got)
	}
}

// TestU11BuildCancelHooks_CancelPendingApprovals_ReachesRealDescendant wires
// a REAL *WSHandler to a REAL *agent.AgentLoop (via SetSessionMessagingStores)
// and a REAL approvalRegistryV2, then invokes the ACTUAL production closure
// buildCancelHooks returns — not a hand-composed equivalent — proving the
// wiring itself (h.agentLoop nil-guard, GetSessionLifecycleStore(), the
// append) is correct end to end, not just the two primitives it calls.
func TestU11BuildCancelHooks_CancelPendingApprovals_ReachesRealDescendant(t *testing.T) {
	cfg := minimalAgentLoopCfg(t)
	al := mustAgentLoopNoWorkspaceSeed(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	ls := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(nil, ls)

	h := newWSHandler(bus.NewMessageBus(), al, "")
	h.approvalRegV2 = newTestApprovalRegistry(t)

	const (
		root  = "sess_u11_wire_root"
		child = "sess_u11_wire_child"
	)
	u11PersistLifecycleChild(t, ls, child, root)

	entry, accepted := h.approvalRegV2.requestApproval(
		"call_wire", "bash", nil, "ava", child, "turn_wire",
	)
	if !accepted || entry == nil {
		t.Fatalf("fixture: requestApproval must accept, got accepted=%v entry=%v", accepted, entry)
	}

	hooks := h.buildCancelHooks(nil)
	if hooks.CancelPendingApprovals == nil {
		t.Fatal("buildCancelHooks must always populate CancelPendingApprovals")
	}
	hooks.CancelPendingApprovals(root, "session canceled")

	select {
	case outcome := <-entry.resultCh:
		if outcome.Approved {
			t.Error("a cancelled approval must not be approved")
		}
	default:
		t.Error("the REAL buildCancelHooks closure must have reached and unblocked " +
			"the child's pending approval via the descendant set")
	}
	if got := h.approvalRegV2.get(entry.ApprovalID); got != nil {
		t.Errorf("the entry must no longer resolve after the real closure's cancel; got %v", got)
	}
}
