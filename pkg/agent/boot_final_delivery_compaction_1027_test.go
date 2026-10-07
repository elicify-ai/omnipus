// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_final_delivery_compaction_1027_test.go: issue #1027 — a committed
// final that was already delivered, woken and consumed (acked) must never be
// delivered a second time just because inbox compaction later purged the
// acked message and its ack entry from the parent's inbox. ADR-091: each
// upward event starts at most one parent turn.
//
// Oracle: the parent inbox is the observable. After the second boot delivery
// pass the replay id `<child>:<gen>:final` must not be present again (it was
// consumed; compaction removed it; nothing may re-append it), and the
// durable facts must show the final delivered so it stops being pending.

package agent

import (
	"fmt"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestBootFinalDelivery_AckedFinalPurgedByCompaction_IsNotRedelivered(t *testing.T) {
	h := newBootRecoveryHarness(t)
	// Tiny retention so a handful of acked fillers purge the final.
	h.inbox.AckedRetentionMax = 2
	h.inbox.CompactionAckedTrigger = 3
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-1027"
	finalID, _, _ := commitAllGenDoneFinal(t, h, child, parent, commitID, "the one and only hand-back")
	al, deliverer := newRealDelivererLoopFixture(t, h)

	// Live publication: the final is appended and the parent is woken. The
	// durable facts record exactly those two receipts.
	if _, err := runRealDelivererPass(t, h, al, deliverer); err != nil {
		t.Fatalf("first delivery pass: %v", err)
	}
	if _, ok := realDelivererInboxMessages(t, h.inbox, parent)[finalID]; !ok {
		t.Fatalf("control: first pass did not append %s", finalID)
	}
	state, revision, _, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState: %v", err)
	}
	woke := state.Merge(session.FinalDeliveryProgress{InboxAppended: true, WakeRecorded: true})
	if err = h.lifecycle.UpdateFinalDelivery(child, 1, commitID, revision, session.FinalDeliveryCommand{Advance: &woke}); err != nil {
		t.Fatalf("record wake fact: %v", err)
	}

	// The parent's turn consumes the final.
	if _, err = h.inbox.AckDetailed(parent, []string{finalID}); err != nil {
		t.Fatalf("ack final: %v", err)
	}
	// Newer acked traffic accumulates until compaction purges the old final.
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("filler-%d", i)
		var m generated.SessionMessage
		gen := 1
		if err = m.FromSessionMessageHandback(generated.SessionMessageHandback{
			MessageId: id, SessionId: child, ParentSessionId: &parent,
			CreatedAt: time.Now().UTC(), Depth: 1, Direction: "child_to_parent",
			Generation: &gen, Kind: "handback", Mode: generated.SessionMessageHandbackModeFinal,
			ResultSoFar: "filler", SenderIdentity: "agent-1",
			Artifacts: []string{}, OpenQuestions: []string{}, UntrustedOrigin: true,
		}); err != nil {
			t.Fatalf("build filler: %v", err)
		}
		if _, err = h.inbox.Append(parent, m); err != nil {
			t.Fatalf("append filler %d: %v", i, err)
		}
		if _, err = h.inbox.AckDetailed(parent, []string{id}); err != nil {
			t.Fatalf("ack filler %d: %v", i, err)
		}
	}
	if _, ok := realDelivererInboxMessages(t, h.inbox, parent)[finalID]; ok {
		t.Fatalf("fixture control: compaction did not purge the acked final %s from the parent inbox", finalID)
	}

	// Boot recovery runs again.
	notices, err := runRealDelivererPass(t, h, al, deliverer)
	if err != nil {
		t.Fatalf("second delivery pass: %v", err)
	}
	if _, ok := realDelivererInboxMessages(t, h.inbox, parent)[finalID]; ok {
		t.Fatalf("the consumed final %s was re-appended to the parent inbox after compaction purged it — the parent would be woken a second time for one hand-back (notices %q)", finalID, notices)
	}
	after, _, _, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState after: %v", err)
	}
	if !after.Delivered() {
		t.Fatalf("durable facts after the second pass = %+v, want the final recorded delivered so it stops being pending", after)
	}
}
