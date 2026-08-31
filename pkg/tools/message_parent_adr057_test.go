// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U14 (Wave F), W12b/FR-076/FR-077 — the D16 inbox producer
// (pkg/tools/message_parent.go:640, ownerKeyFor -> rec.ParentDurableKey).
//
// [UPDATED, 14-reviewer sign-off MEDIUM-2, post release/v0.1.1] This test
// originally also asserted the CONSUMER side (delegate.go's executeInbox)
// keyed reads by callerOwnerKey (ToolTranscriptSessionID(ctx)) — i.e. the
// grandparent A's own inbox read on grandchild D's session came back empty,
// per BDD-85 ("A grandchild's message_parent is drained only by its direct
// parent"). That was a real bug, found and fixed independently of this
// file: verifyCallerOwnsSession's ownership GATE was later extended (FR-039)
// to permit an authorized ANCESTOR (not just the direct parent) up to
// SetOwnershipWalkMaxDepth hops — TestOwnershipWalk_AllSixGatedActions
// (delegate_adr057_test.go) explicitly lists `inbox`/`peek` among the six
// actions a root chat is PERMITTED to invoke against a grandchild — but
// executeInbox/executePeek's own DATA READ was never updated to match: they
// kept resolving the store partition from the CALLER's own key instead of
// the target's rec.ParentDurableKey (the key the message was actually
// Appended under), so an authorized ancestor's call succeeded (passed the
// gate) yet silently returned empty — a success-shaped false negative, not
// a genuine absence. executeRespond in the same file already used the
// correct key (rec.ParentDurableKey) throughout, which is what exposed the
// inconsistency. The fix keys executeInbox/executePeek's store reads by
// rec.ParentDurableKey unconditionally, matching executeRespond and closing
// the gap between "who may call this" (the FR-039 gate) and "what they
// actually see" (the data key) — see pkg/tools/delegate_signoff14_test.go
// for the dedicated regression coverage of both actions.
//
// This SUPERSEDES BDD-85/FR-077's original "only the direct parent" data-
// visibility contract (docs/internal/specs/adr-057-session-unification-spec.md)
// for any caller the FR-039 walk itself authorizes — BDD-85 predates that
// walk's extension past a single hop and was never revisited when it
// landed. Flagged here rather than silently reconciled: an architect should
// confirm the spec text itself is updated (or BDD-85 is deliberately
// narrowed to the *unauthorized* case) rather than leaving the traced
// requirement and the code permanently disagreeing.

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestMessageParent_DrainedByDirectParentAtDepth3 is test #66 (BDD-85,
// FR-077, "Producer and consumer agree", AC-16), UPDATED per the 14-reviewer
// sign-off MEDIUM-2 fix (see file header). Chat A, child B, grandchild D: D
// pushes a message via message_parent; B (D's DIRECT parent) drains it via
// delegate action="inbox" — and so does A (D's grandparent, an ANCESTOR the
// FR-039 ownership walk authorizes), since both are reading the SAME
// correctly-keyed partition (rec.ParentDurableKey = B) regardless of which
// authorized caller asks.
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
	}); err != nil {
		t.Fatalf("seed B failed: %v", err)
	}
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: grandchildD, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID: childB, ParentDurableKey: childB, WorkspaceID: "ws-1", AgentID: "worker",
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

	// A (grandparent, NOT the direct parent, but an ANCESTOR the FR-039
	// ownership walk authorizes — TestOwnershipWalk_AllSixGatedActions lists
	// `inbox` among the six permitted actions) now ALSO finds it: MEDIUM-2's
	// fix keys the read by D's own rec.ParentDurableKey (= B) unconditionally,
	// not by which authorized caller happens to ask. See this file's header
	// comment for why this supersedes BDD-85's original "only the direct
	// parent" data-visibility text.
	aCtx := WithTranscriptSessionID(context.Background(), chatA)
	aResult := delegateTool.Execute(aCtx, map[string]any{"action": "inbox", "session_id": grandchildD})
	if aResult.IsError {
		t.Fatalf("did not expect an ownership rejection for A against D (BDD-44 permits the ancestor "+
			"walk here) — expected A to successfully read D's inbox, got error: %s", aResult.ForLLM)
	}
	if !strings.Contains(aResult.ForLLM, "grandchild is working") {
		t.Fatalf("MEDIUM-2: expected authorized ancestor A's inbox read to surface D's message (keyed by "+
			"D's own rec.ParentDurableKey, same as B's read), got: %s", aResult.ForLLM)
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
