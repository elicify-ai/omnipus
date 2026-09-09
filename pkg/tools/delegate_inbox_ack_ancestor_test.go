// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for the nested-delegation message leak (HIGH,
// 2026-08).
//
// executeInbox and executePeek were re-keyed to rec.ParentDurableKey (the
// key a child's messages are actually Appended under — see
// message_parent.go::ownerKeyFor) but executeInboxAck was left keyed to
// callerOwnerKey(ctx). Since verifyCallerOwnsSession deliberately permits an
// ANCESTOR (FR-039), whose key is by definition NOT the target's
// ParentDurableKey, an A -> B -> C chain let A drain C's message and then
// ack it against A's OWN inbox file: every id came back Unknown, nothing was
// acknowledged, and the message was redelivered on every subsequent drain
// while permanently consuming C's InboxUnackedMax budget.
package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// seedChainRecord persists one lifecycle record for a delegated session with
// the given direct-parent durable key.
func seedChainRecord(t *testing.T, lc *session.LifecycleStore, sessionID, parentDurableKey string) {
	t.Helper()
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID:        sessionID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeHuman,
		ParentDurableKey: parentDurableKey,
		WorkspaceID:      "ws-1",
		AgentID:          "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record %s failed: %v", sessionID, err)
	}
}

// TestDelegateInboxAck_AncestorAck_KeyedByTargetParent_NoRedelivery is the
// RED/GREEN test for the leak. Chain: A (root chat) -> B -> C. C's message
// is Appended under C's own ParentDurableKey ("sess-B"). The ANCESTOR A
// drains it (permitted by FR-039) and then acks it.
//
// Pre-fix this ack ran AckDetailed("sess-A", ...) and every id came back
// Unknown, so the message survived and the very next drain redelivered it.
// Post-fix the ack is keyed by rec.ParentDurableKey ("sess-B"), matching the
// read path, so the id is genuinely acknowledged and never redelivered.
func TestDelegateInboxAck_AncestorAck_KeyedByTargetParent_NoRedelivery(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)

	// A is the root chat session: it has no lifecycle record of its own
	// (a plain chat session is never a delegated child), exactly as in
	// production.
	const rootA = "sess-A"
	const midB = "sess-B"
	const leafC = "sess-C"
	seedChainRecord(t, lc, midB, rootA)
	seedChainRecord(t, lc, leafC, midB)

	// C appends its message under its OWN direct parent's key — this is
	// what message_parent.go does in production (ownerKeyFor(rec)).
	msg := progressMsgForDelegateTest(t, leafC, "pm-leaf-1")
	if _, err := inbox.Append(midB, msg); err != nil {
		t.Fatalf("seed inbox message failed: %v", err)
	}

	// The ANCESTOR (A, two hops up) is the caller throughout.
	ctx := WithTranscriptSessionID(context.Background(), rootA)

	// 1. Drain succeeds for the ancestor (this already worked pre-fix — it
	//    is the half of the pair that made the mismatch invisible).
	drainResult := tool.Execute(ctx, map[string]any{
		"action":     "inbox",
		"session_id": leafC,
	})
	if drainResult.IsError {
		t.Fatalf("ancestor inbox drain failed: %s", drainResult.ForLLM)
	}
	if !strings.Contains(drainResult.ForLLM, "pm-leaf-1") {
		t.Fatalf("expected the ancestor's drain to deliver pm-leaf-1, got: %s", drainResult.ForLLM)
	}

	// 2. The ack must actually acknowledge it — not report it Unknown.
	ackResult := tool.Execute(ctx, map[string]any{
		"action":      "inbox_ack",
		"session_id":  leafC,
		"message_ids": []any{"pm-leaf-1"},
	})
	if ackResult.IsError {
		t.Fatalf("ancestor inbox_ack failed: %s", ackResult.ForLLM)
	}
	if !strings.Contains(ackResult.ForLLM, "Acknowledged 1 message(s).") {
		t.Errorf("expected the ancestor's ack to acknowledge the real message "+
			"('Acknowledged 1 message(s).'), got: %s", ackResult.ForLLM)
	}
	if strings.Contains(ackResult.ForLLM, "not recognized") {
		t.Errorf("the message must NOT come back Unknown — the ack is keyed to the wrong "+
			"inbox when it does. Got: %s", ackResult.ForLLM)
	}

	// 3. A second drain must NOT redeliver it. This is the user-visible
	//    symptom: a permanently redelivered message that never frees its
	//    slot in the child's InboxUnackedMax budget.
	redrain := tool.Execute(ctx, map[string]any{
		"action":     "inbox",
		"session_id": leafC,
	})
	if redrain.IsError {
		t.Fatalf("second inbox drain failed: %s", redrain.ForLLM)
	}
	if strings.Contains(redrain.ForLLM, "pm-leaf-1") {
		t.Errorf("pm-leaf-1 was REDELIVERED after being acked — the ack did not land in the "+
			"same inbox the drain reads. Got: %s", redrain.ForLLM)
	}

	// 4. Ground truth at the store: the message is gone from the child's
	//    real owner key, which is the only thing that relieves the
	//    per-child unacked ceiling enforced in MessageInboxStore.Append.
	remaining, _, _, derr := inbox.Drain(midB, leafC, "", 10)
	if derr != nil {
		t.Fatalf("store-level Drain failed: %v", derr)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 unacked messages under the child's own owner key %q after the "+
			"ancestor's ack, got %d — these permanently consume InboxUnackedMax", midB, len(remaining))
	}
}

// TestDelegateInboxAck_DirectParentStillWorks pins the common case the fix
// must not regress: the DIRECT parent's own key IS rec.ParentDurableKey, so
// its ack behaves exactly as before.
func TestDelegateInboxAck_DirectParentStillWorks(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	const parent = "parent-direct"
	const child = "child-direct"
	seedChainRecord(t, lc, child, parent)

	if _, err := inbox.Append(parent, progressMsgForDelegateTest(t, child, "pm-direct-1")); err != nil {
		t.Fatalf("seed inbox message failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), parent)
	res := tool.Execute(ctx, map[string]any{
		"action":      "inbox_ack",
		"session_id":  child,
		"message_ids": []any{"pm-direct-1"},
	})
	if res.IsError {
		t.Fatalf("direct-parent inbox_ack failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Acknowledged 1 message(s).") {
		t.Errorf("expected 'Acknowledged 1 message(s).', got: %s", res.ForLLM)
	}
}

// TestDelegateInboxAck_UnrelatedCallerDenied proves the ownership gate the
// fix necessarily introduces: keying the ack by rec.ParentDurableKey without
// verifying the caller owns rec would let ANY caller ack messages in an
// inbox it has no relationship to. A stranger must be denied, and the
// message must survive untouched.
func TestDelegateInboxAck_UnrelatedCallerDenied(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	const victimParent = "victim-parent"
	const victimChild = "victim-child"
	seedChainRecord(t, lc, victimChild, victimParent)

	if _, err := inbox.Append(victimParent, progressMsgForDelegateTest(t, victimChild, "pm-victim-1")); err != nil {
		t.Fatalf("seed inbox message failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "unrelated-stranger")
	res := tool.Execute(ctx, map[string]any{
		"action":      "inbox_ack",
		"session_id":  victimChild,
		"message_ids": []any{"pm-victim-1"},
	})
	if !res.IsError {
		t.Fatalf("an unrelated caller must NOT be able to ack another owner's inbox, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not owned by the calling session") {
		t.Errorf("expected an ownership denial, got: %s", res.ForLLM)
	}

	remaining, _, _, derr := inbox.Drain(victimParent, victimChild, "", 10)
	if derr != nil {
		t.Fatalf("store-level Drain failed: %v", derr)
	}
	if len(remaining) != 1 {
		t.Errorf("the victim's message must survive a denied ack, got %d remaining", len(remaining))
	}
}
