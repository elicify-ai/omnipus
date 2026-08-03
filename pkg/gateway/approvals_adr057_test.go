package gateway

// approvals_adr057_test.go — tests for ADR-057 W10 / unit U17a
// (pkg/gateway/approvals.go).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's new
// tests go in a NEW file named `<subject>_adr057_test.go`; this file is U17a's.
// It covers TDD plan rows #86 (BDD-90) and #87 (BDD-91), plus the FR-032
// descendant-set signature the cancel path (U11/U15) consumes.
//
// Binding rule 1 (real state, never a spy): every assertion below reads the
// registry's own entries and the outcomes actually delivered on the blocked
// goroutine's channel. Nothing asserts "a function was called".
//
// Corollary "distinct ids everywhere": the chat/routing id and the child's
// acting id are always constructed as distinct, non-equal values, and the
// pre-assertions below fail loudly if they ever coincide — a fixture in which
// they are equal cannot tell a correctly re-keyed registry from the old one.

import (
	"testing"
	"time"
)

// approvalTestIDs are the two ids every test in this file distinguishes: the
// chat's routing session id (what the SPA sees on the session-scoped
// tool_approval_required frame) and the delegated child's own acting session
// id (what the registry entry must carry, FR-080).
const (
	chatRoutingSid = "sess_chat_routing_1a2b"
	childActingSid = "sess_child_acting_3c4d"
)

// newTestApprovalRegistry builds a registry whose terminal entries are deleted
// synchronously, so deletion is assertable without waiting on a timer — the
// same convention the pre-existing approvals tests in this package use.
func newTestApprovalRegistry(t *testing.T) *approvalRegistryV2 {
	t.Helper()
	if chatRoutingSid == childActingSid {
		t.Fatal("fixture defect: the chat routing id and the child acting id must " +
			"be distinct or these tests cannot discriminate")
	}
	r := newApprovalRegistryV2(0, 30*time.Second)
	r.terminalRetention = 0
	return r
}

// TestApprovalRegistry_EntryCarriesActingSessionID is TDD plan row #86,
// tracing to BDD-90 (US-6 acceptance scenario 5).
//
// BDD-90:
//
//	Given  a child turn raising a real approval request, with the child's id
//	       distinct from the chat's
//	When   the registry entry is inspected
//	Then   its SessionID is the CHILD's own session id
//	But    it is not the chat's routing id
//
// The consequence is asserted too, because the field value alone is only half
// the property: cancellation matches on that field by exact equality, so an
// entry carrying the child's id must be UNREACHABLE by the chat id alone and
// reachable by the descendant set (FR-032). That pair is what a chat-level
// Stop actually depends on.
func TestApprovalRegistry_EntryCarriesActingSessionID(t *testing.T) {
	r := newTestApprovalRegistry(t)

	entry, accepted := r.requestApproval(
		"call_1", "bash", map[string]any{"cmd": "ls"},
		"ava", childActingSid, "turn_1",
	)
	if !accepted || entry == nil {
		t.Fatalf("fixture: requestApproval must accept, got accepted=%v entry=%v", accepted, entry)
	}

	if entry.SessionID != childActingSid {
		t.Errorf("BDD-90: entry.SessionID = %q, want the child's acting id %q",
			entry.SessionID, childActingSid)
	}
	if entry.SessionID == chatRoutingSid {
		t.Errorf("BDD-90: entry.SessionID must NOT be the chat's routing id %q", chatRoutingSid)
	}
	if got := r.missingActingSessionID.Load(); got != 0 {
		t.Errorf("a well-formed request must not be counted as missing an acting "+
			"session id; count = %d", got)
	}

	// --- The chat id alone matches nothing (this is why FR-032 exists).
	if n := r.cancelAllPendingForSession(chatRoutingSid, "session canceled"); n != 0 {
		t.Errorf("cancelling by the chat's routing id alone must match 0 entries, "+
			"matched %d — a chat-level Stop that passes only the chat id leaves the "+
			"child blocked until its approval timeout", n)
	}
	if got := r.get(entry.ApprovalID); got == nil || got.state != ApprovalStatePending {
		t.Fatalf("the entry must still be pending after a non-matching cancel; got %v", got)
	}

	// --- The descendant set does match (FR-032).
	if n := r.cancelAllPendingForSessions(
		[]string{chatRoutingSid, childActingSid}, "session canceled",
	); n != 1 {
		t.Errorf("FR-032: cancelling over the descendant set must match the child's "+
			"entry; matched %d, want 1", n)
	}
	// Observable artefacts: the outcome was delivered, the timer stopped, and
	// the entry no longer resolves (terminalRetention == 0 deletes inline).
	select {
	case outcome := <-entry.resultCh:
		if outcome.Approved {
			t.Error("a cancelled approval must not be approved")
		}
		if outcome.Reason != "session canceled" {
			t.Errorf("outcome.Reason = %q, want %q", outcome.Reason, "session canceled")
		}
	default:
		t.Error("the blocked goroutine must have been unblocked with an outcome")
	}
	if entry.timer != nil {
		t.Error("the entry's timeout timer must be stopped and cleared")
	}
	if got := r.get(entry.ApprovalID); got != nil {
		t.Errorf("the terminal entry must no longer resolve, got %v", got)
	}
}

// TestApprovalRegistry_EmptyActingSessionIDIsCounted covers the FR-080
// diagnostic. An entry with an empty acting session id can never be matched by
// cancellation (no caller ever asks to cancel ""), so it survives every Stop
// and only resolves on the 300 s timeout. That is a hung turn, and it must be
// observable rather than silent.
func TestApprovalRegistry_EmptyActingSessionIDIsCounted(t *testing.T) {
	r := newTestApprovalRegistry(t)

	if got := r.missingActingSessionID.Load(); got != 0 {
		t.Fatalf("fixture: counter must start at 0, got %d", got)
	}
	entry, accepted := r.requestApproval("call_x", "bash", nil, "ava", "", "turn_x")
	if !accepted || entry == nil {
		t.Fatalf("fixture: requestApproval must still accept, got accepted=%v", accepted)
	}
	if got := r.missingActingSessionID.Load(); got != 1 {
		t.Errorf("FR-080: an empty acting session id must be COUNTED; count = %d, want 1", got)
	}

	// The reason it matters, asserted rather than asserted-about: an empty id
	// is not cancellable, and the plural form must never treat "" as a target.
	if n := r.cancelAllPendingForSessions([]string{""}, "session canceled"); n != 0 {
		t.Errorf("an empty target id must match nothing, matched %d — otherwise one "+
			"malformed caller auto-denies every entry that also lost its id", n)
	}
	if got := r.get(entry.ApprovalID); got == nil || got.state != ApprovalStatePending {
		t.Errorf("the entry must still be pending; got %v", got)
	}
}

// TestApprovalRoundTrip_ChildApprovedResolvesByApprovalID is TDD plan row #87,
// tracing to BDD-91 (US-6 acceptance scenario 6).
//
// BDD-91:
//
//	Given  a child's pending entry, and a tool_approval_required frame whose
//	       session_id is the CHAT's routing key
//	When   the client responds APPROVE
//	Then   the response resolves to the child's entry BY APPROVAL ID
//	And    the child's tool call proceeds without a prompt or a timeout
//	But    resolution does NOT depend on the frame's session_id matching the
//	       registry's, so the routing-key change cannot break it
//
// v1 covered only the cancel path; the interactive approve round trip — the
// one a user actually drives — was never asserted. The independence clause is
// asserted concretely: a second entry under the CHAT's id is created alongside
// the child's, and approving the child's approval id must resolve exactly one
// of them, the child's.
func TestApprovalRoundTrip_ChildApprovedResolvesByApprovalID(t *testing.T) {
	r := newTestApprovalRegistry(t)

	childEntry, ok := r.requestApproval(
		"call_child", "bash", map[string]any{"cmd": "go test"},
		"ava", childActingSid, "turn_child",
	)
	if !ok || childEntry == nil {
		t.Fatalf("fixture: the child's request must be accepted")
	}
	// A sibling entry keyed by the CHAT's routing id — the value the client
	// actually saw on the frame. If resolution ever consulted session id, this
	// is the entry it would wrongly reach.
	chatEntry, ok := r.requestApproval(
		"call_chat", "bash", nil, "jim", chatRoutingSid, "turn_chat",
	)
	if !ok || chatEntry == nil {
		t.Fatalf("fixture: the chat's request must be accepted")
	}
	if childEntry.ApprovalID == chatEntry.ApprovalID {
		t.Fatal("fixture defect: the two approval ids must differ")
	}

	// The child's turn goroutine, blocked exactly as the real one is.
	got := make(chan ApprovalOutcome, 1)
	go func() { got <- <-childEntry.resultCh }()

	// The client responds APPROVE, carrying only the approval id.
	resolveOK, gone := r.resolve(childEntry.ApprovalID, ApprovalActionApprove)
	if !resolveOK || gone {
		t.Fatalf("BDD-91: approve must resolve; resolveOK=%v gone=%v", resolveOK, gone)
	}

	select {
	case outcome := <-got:
		if !outcome.Approved {
			t.Errorf("BDD-91: the child's tool call must proceed; outcome = %+v", outcome)
		}
		if outcome.Reason != "approved" {
			t.Errorf("outcome.Reason = %q, want %q", outcome.Reason, "approved")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BDD-91: the child's blocked goroutine was never unblocked — this is " +
			"the 300 s hang the requirement exists to prevent")
	}

	// Independence from session id: the sibling under the chat's routing id is
	// untouched, so resolution cannot have matched on that field.
	if chatEntry.state != ApprovalStatePending {
		t.Errorf("resolving by approval id must not touch an entry that merely shares "+
			"the frame's routing session id; sibling state = %q", chatEntry.state)
	}
	select {
	case o := <-chatEntry.resultCh:
		t.Errorf("the sibling entry must not have been resolved; got %+v", o)
	default:
	}

	// The approval id remains the ONLY key, in both directions. This fixture
	// sets terminalRetention to 0, so the resolved entry is deleted inline and
	// a repeat action is not-found rather than 410 Gone (the retention-window
	// 410 contract is FR-018's and is covered by this package's existing
	// TestApprovalRegistry_AllTransitions).
	if got := r.get(childEntry.ApprovalID); got != nil {
		t.Errorf("the resolved entry must no longer resolve by id, got %v", got)
	}
	if resolveOK, gone := r.resolve(childEntry.ApprovalID, ApprovalActionApprove); resolveOK || gone {
		t.Errorf("a repeat action on a deleted entry must be not-found; got "+
			"resolveOK=%v gone=%v", resolveOK, gone)
	}
	// An approval id that was never issued resolves nothing and must not fall
	// back to any session-id-shaped match against the surviving sibling.
	if resolveOK, gone := r.resolve("approval_never_issued", ApprovalActionApprove); resolveOK || gone {
		t.Errorf("an unknown approval id must resolve nothing; got resolveOK=%v gone=%v",
			resolveOK, gone)
	}
	if chatEntry.state != ApprovalStatePending {
		t.Errorf("the sibling must still be pending after an unknown-id resolve; "+
			"state = %q", chatEntry.state)
	}
}

// TestApprovalRegistry_CancelAllPendingForSessions_SetSemantics pins the
// signature U11 and U15 consume for FR-032: the plural form takes a set, is
// order-independent, ignores empty and unknown ids, and de-duplicates.
func TestApprovalRegistry_CancelAllPendingForSessions_SetSemantics(t *testing.T) {
	r := newTestApprovalRegistry(t)

	grandchildSid := "sess_grandchild_5e6f"
	unrelatedSid := "sess_unrelated_7a8b"

	mk := func(sid, callID string) *approvalEntry {
		t.Helper()
		e, ok := r.requestApproval(callID, "bash", nil, "ava", sid, "turn_"+callID)
		if !ok || e == nil {
			t.Fatalf("fixture: requestApproval(%s) must be accepted", sid)
		}
		return e
	}
	child := mk(childActingSid, "c1")
	grandchild := mk(grandchildSid, "c2")
	unrelated := mk(unrelatedSid, "c3")

	// Descendant set with duplicates, an empty id, and an unknown id mixed in.
	n := r.cancelAllPendingForSessions([]string{
		chatRoutingSid, childActingSid, grandchildSid, childActingSid, "", "sess_never_existed",
	}, "session canceled")
	if n != 2 {
		t.Errorf("FR-032: want 2 cancelled (child + grandchild), got %d — duplicates "+
			"must not double-count and unknown/empty ids must not match", n)
	}

	for name, e := range map[string]*approvalEntry{"child": child, "grandchild": grandchild} {
		if e.state != ApprovalStateDeniedCancel {
			t.Errorf("%s entry state = %q, want %q", name, e.state, ApprovalStateDeniedCancel)
		}
		select {
		case o := <-e.resultCh:
			if o.Approved {
				t.Errorf("%s must not be approved", name)
			}
		default:
			t.Errorf("%s goroutine was not unblocked", name)
		}
	}
	if unrelated.state != ApprovalStatePending {
		t.Errorf("an entry outside the descendant set must be untouched; state = %q",
			unrelated.state)
	}

	// An entirely empty set is a no-op, not a match-everything.
	if n := r.cancelAllPendingForSessions(nil, "session canceled"); n != 0 {
		t.Errorf("a nil target set must match nothing, matched %d", n)
	}
	if unrelated.state != ApprovalStatePending {
		t.Errorf("a nil target set must leave every entry pending; state = %q",
			unrelated.state)
	}
}
