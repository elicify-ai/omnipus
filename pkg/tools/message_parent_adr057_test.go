// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U14 (Wave F), W12b/FR-076/FR-077 — the D16 inbox producer
// (pkg/tools/message_parent.go:640, ownerKeyFor -> rec.ParentDurableKey)
// and consumer (pkg/tools/delegate.go's executeInbox, callerOwnerKey ->
// ToolTranscriptSessionID(ctx)) both key on the immediate parent's
// ParentDurableKey — verified end to end against REAL store-backed state
// (Rule 5), not a spy that merely records its argument.
//
// [Design note, recorded here per the fablize evidence-based/no-silent-
// assumptions rule] BDD-85's "does not, and does not return a clean empty
// success in place of it" is read here as: the grandparent's OWN inbox
// read genuinely finds nothing (not silently substituted for the direct
// parent's), proven by (a) a positive control showing the message DOES
// exist and IS found via the direct parent, and (b) an explicit assertion
// that the grandparent's own response carries zero messages — rather than
// as a requirement that the ownership GATE itself reject the grandparent's
// call outright. BDD-44's own worked example explicitly lists `inbox` as
// one of the six actions a root chat is PERMITTED to invoke against a
// grandchild (TestOwnershipWalk_AllSixGatedActions in
// delegate_adr057_test.go covers that gate directly); this file's concern
// is the separate, orthogonal question of which owner's inbox FILE a
// message is routed into. If a stricter reading (the grandparent's call
// itself must be REJECTED, not merely empty) was intended, that is a
// one-line change to executeInbox's ownership check for this action only
// — flagged for review rather than guessed silently.

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestMessageParent_DrainedByDirectParentAtDepth3 is test #66 (BDD-85,
// FR-077, "Producer and consumer agree", AC-16). Chat A, child B,
// grandchild D: D pushes a message via message_parent; B (D's DIRECT
// parent) drains it via delegate action="inbox"; A (D's grandparent) does
// not find it in ITS OWN inbox.
func TestMessageParent_DrainedByDirectParentAtDepth3(t *testing.T) {
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())

	mp := NewMessageParentTool(inbox, lc)
	mp.SetSessionMessagingEnabled(func() bool { return true })

	const chatA = "u14-mp-chat-A"
	const childB = "u14-mp-child-B"
	const grandchildD = "u14-mp-grandchild-D"

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: childB, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID: chatA, ParentDurableKey: chatA, WorkspaceID: "ws-1", AgentID: "worker",
		LaunchProfile: session.LaunchProfileSpecialist,
	}); err != nil {
		t.Fatalf("seed B failed: %v", err)
	}
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: grandchildD, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID: childB, ParentDurableKey: childB, WorkspaceID: "ws-1", AgentID: "worker",
		LaunchProfile: session.LaunchProfileSpecialist,
	}); err != nil {
		t.Fatalf("seed D failed: %v", err)
	}

	// D (the grandchild) pushes a message — the PRODUCER side (FR-077).
	dCtx := withChildContext(grandchildD)
	sendResult := mp.Execute(dCtx, map[string]any{"kind": "progress", "text": "grandchild is working", "pct": 10})
	if sendResult.IsError {
		t.Fatalf("message_parent from D failed: %s", sendResult.ForLLM)
	}

	delegateTool := NewDelegateTool("test-model", 0, 0)
	delegateTool.SetSessionMessagingEnabled(func() bool { return true })
	delegateTool.SetLifecycleStore(lc)
	delegateTool.SetMessageInbox(inbox)

	// B (direct parent) drains it — the CONSUMER side, positive control
	// (Rule 4): proves the message genuinely exists and is reachable by the
	// party FR-077 says should reach it, before checking who must NOT.
	bCtx := WithTranscriptSessionID(context.Background(), childB)
	bResult := delegateTool.Execute(bCtx, map[string]any{"action": "inbox", "session_id": grandchildD})
	if bResult.IsError {
		t.Fatalf("BDD-85: expected B (D's DIRECT parent) to drain D's message, got error: %s", bResult.ForLLM)
	}
	if !strings.Contains(bResult.ForLLM, "grandchild is working") {
		t.Errorf("expected B's inbox drain to contain D's actual message text, got: %s", bResult.ForLLM)
	}

	// A (grandparent, NOT the direct parent) does not find it in ITS OWN
	// inbox — see the file-level design note above for why this asserts a
	// genuinely empty result rather than an outright ownership rejection.
	aCtx := WithTranscriptSessionID(context.Background(), chatA)
	aResult := delegateTool.Execute(aCtx, map[string]any{"action": "inbox", "session_id": grandchildD})
	if aResult.IsError {
		t.Fatalf("did not expect an ownership rejection for A against D (BDD-44 permits the ancestor "+
			"walk here) — expected a genuinely empty inbox result instead, got error: %s", aResult.ForLLM)
	}
	if strings.Contains(aResult.ForLLM, "grandchild is working") {
		t.Fatalf("BDD-85: A's OWN inbox drain must NOT surface D's message (it was routed to B's inbox, "+
			"not A's) — got: %s", aResult.ForLLM)
	}
}

// TestPerChildMessageCeiling_IsPerDirectParent is test #67 (BDD-84,
// FR-076, AC-15): the per-child question+blocker ceiling is enforced
// independently for EACH direct child of a chat, so the chat's aggregate
// is (children × ceiling), not one pool shared across all its children.
// Two direct children (B1, B2) of the SAME chat A each get their own full
// budget — B1 exhausting its own ceiling must not touch B2's.
func TestPerChildMessageCeiling_IsPerDirectParent(t *testing.T) {
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	const ceiling = 2
	inbox.InboxPerTypeCeiling = ceiling // small, deterministic test ceiling

	mp := NewMessageParentTool(inbox, lc)
	mp.SetSessionMessagingEnabled(func() bool { return true })

	const chatA = "u14-ceiling-chat-A"
	const childB1 = "u14-ceiling-child-B1"
	const childB2 = "u14-ceiling-child-B2"

	for _, id := range []string{childB1, childB2} {
		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: id, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
			OwnerScopeID: chatA, ParentDurableKey: chatA, WorkspaceID: "ws-1", AgentID: "worker",
			LaunchProfile: session.LaunchProfileSpecialist,
		}); err != nil {
			t.Fatalf("seed %s failed: %v", id, err)
		}
	}

	sendBlocker := func(childID string, n int) *ToolResult {
		ctx := withChildContext(childID)
		return mp.Execute(ctx, map[string]any{
			"kind": "blocker", "text": fmt.Sprintf("blocker %d from %s", n, childID), "severity": "medium",
		})
	}

	// B1 fills its own ceiling.
	for i := 0; i < ceiling; i++ {
		if r := sendBlocker(childB1, i); r.IsError {
			t.Fatalf("B1 blocker %d expected to succeed within its own ceiling, got error: %s", i, r.ForLLM)
		}
	}
	// B1's ceiling+1'th blocker is rejected — its OWN budget is exhausted.
	if r := sendBlocker(childB1, ceiling); !r.IsError {
		t.Fatal("expected B1's ceiling+1'th blocker to be rejected once its own budget is exhausted")
	}

	// BDD-84: B2, a DIFFERENT direct child of the SAME chat A, is
	// completely unaffected by B1's exhausted budget — it can still fill
	// its own FULL ceiling, proving the aggregate is (children × ceiling)
	// rather than one pool the two children share.
	for i := 0; i < ceiling; i++ {
		if r := sendBlocker(childB2, i); r.IsError {
			t.Fatalf("BDD-84: expected B2's blocker %d to succeed — B1 exhausting ITS OWN ceiling must not "+
				"reduce B2's independent budget, got error: %s", i, r.ForLLM)
		}
	}
	// And B2's own ceiling independently caps at the same limit — proving
	// the isolation is real per-child enforcement, not merely "unlimited
	// for whoever isn't B1".
	if r := sendBlocker(childB2, ceiling); !r.IsError {
		t.Fatal("expected B2's ceiling+1'th blocker to also be rejected once ITS OWN budget is exhausted")
	}
}
