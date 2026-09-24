// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// approval_missing_turnid_test.go covers D-03 (ADR-092, 2026-09-24 UAT
// tester t5)'s defense-in-depth guard in
// approvalRegistryV2.requestApproval: an approval request with an empty
// turn id must be refused immediately (fail CLOSED) rather than admitted as
// a pending entry no client can ever render a card for.
//
// Mirrors approvals_adr057_test.go's TestApprovalRegistry_EmptyActingSessionIDIsCounted
// in shape, but the outcome is deliberately the OPPOSITE: an empty acting
// session id still admits the entry as pending (merely uncancellable — see
// that test's own doc comment), while an empty turn id refuses the request
// outright, because a client CAN still render and answer a card with a
// valid turn id and an unreachable session id, but NO client can ever
// render a card with no turn id at all (the wire contract's minLength:1 on
// turn_id causes the SPA to silently drop the frame before it reaches the
// approval queue).
package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// TestApprovalRegistry_EmptyTurnIDFailsClosed is the guard's core proof: a
// request with turnID=="" must NOT be admitted as pending. If the guard in
// requestApproval were removed, this test fails on every assertion below —
// accepted would be true, entry.state would be ApprovalStatePending (not a
// terminal state), and the synchronous resultCh read would block (this test
// uses a non-blocking select specifically so that regression hangs the test
// with a clear "did not resolve synchronously" failure rather than an
// indefinite stall).
func TestApprovalRegistry_EmptyTurnIDFailsClosed(t *testing.T) {
	r := newTestApprovalRegistry(t)

	if got := r.missingTurnID.Load(); got != 0 {
		t.Fatalf("fixture: counter must start at 0, got %d", got)
	}

	entry, accepted := r.requestApproval(
		"call_missing_turn", "bash", map[string]any{"command": "touch /tmp/probe"},
		"ava", childActingSid, "",
	)

	if accepted {
		t.Fatal("D-03: a request with an empty turn id must be REFUSED (accepted=false), " +
			"mirroring the saturated-cap path — admitting it as pending would leave the " +
			"caller blocked until timeout with no card any client could ever render")
	}
	if entry == nil {
		t.Fatal("requestApproval must still return a synthetic entry when refusing, " +
			"exactly like the saturated-cap path does")
	}
	if entry.state != ApprovalStateDeniedMissingTurnID {
		t.Errorf("entry.state = %q, want %q", entry.state, ApprovalStateDeniedMissingTurnID)
	}
	if !entry.state.isTerminal() {
		t.Error("the synthetic entry's state must be terminal — the caller must never see it as pending")
	}
	if got := r.missingTurnID.Load(); got != 1 {
		t.Errorf("D-03: an empty turn id must be COUNTED; count = %d, want 1", got)
	}

	// The caller (policyApproverAdapter.RequestApproval) reads this
	// channel unconditionally after requestApproval returns — it must
	// already hold a delivered, non-approved outcome, or that caller would
	// block on a request nobody is coming to answer.
	select {
	case outcome := <-entry.resultCh:
		if outcome.Approved {
			t.Error("an empty-turn-id request must never be approved")
		}
		if outcome.Reason != denialReasonMissingTurnID {
			t.Errorf("outcome.Reason = %q, want %q", outcome.Reason, denialReasonMissingTurnID)
		}
	default:
		t.Fatal("D-03: the outcome must be delivered synchronously by requestApproval itself " +
			"(pre-filled resultCh) — a caller blocking here is exactly the hang this guard exists to prevent")
	}

	// The synthetic entry must never have been registered in the map at
	// all (mirrors the saturated-cap path) — an entry no client can ever
	// see must also not be independently resolvable/lookupable later, and
	// must not have consumed a pending slot.
	if got := r.get(entry.ApprovalID); got != nil {
		t.Error("a refused empty-turn-id request must not be registered in the entries map")
	}
	if pending := r.pendingApprovals(); len(pending) != 0 {
		t.Errorf("a refused empty-turn-id request must not appear as pending; got %d pending", len(pending))
	}
}

// TestApprovalRegistry_EmptyTurnIDDoesNotBroadcast proves the second half
// of "defense in depth": policyApproverAdapter.RequestApproval only calls
// wsHandler.broadcastToolApprovalRequired when accepted==true (see its own
// source — the saturated-cap comment there: "accepted==false → saturated;
// no WS broadcast, outcome pre-delivered"). Since requestApproval's
// empty-turn-id guard also returns accepted==false, the SAME skip applies:
// no tool_approval_required frame is ever sent for an entry no client could
// render anyway. Driven through the real adapter and a real WSHandler with
// an attached fake connection, not asserted about the registry alone.
func TestApprovalRegistry_EmptyTurnIDDoesNotBroadcast(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0
	handler.approvalRegV2 = reg
	tabs := apprResAttachConns(t, handler, 1)

	adapter := newPolicyApproverAdapter(reg, handler)
	approved, reason, recordGrant := adapter.RequestApproval(context.Background(), agent.PolicyApprovalReq{
		ToolCallID: "call_missing_turn",
		ToolName:   "bash",
		Args:       map[string]any{"command": "touch /tmp/probe"},
		AgentID:    "ava",
		SessionID:  childActingSid,
		TurnID:     "",
	})

	if approved {
		t.Fatal("an empty-turn-id request must never be approved")
	}
	if reason != denialReasonMissingTurnID {
		t.Errorf("reason = %q, want %q", reason, denialReasonMissingTurnID)
	}
	if recordGrant {
		t.Error("an empty-turn-id refusal must never record a grant")
	}

	select {
	case raw := <-tabs[0].sendCh:
		t.Fatalf("no frame must ever be sent for an empty-turn-id request, got: %s", raw)
	default:
		// Expected: nothing was broadcast.
	}
}
