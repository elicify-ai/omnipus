// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_final_delivery_compaction_1027_test.go: issue #1027 — a committed
// final that was already delivered, woken and consumed (acked) must never be
// delivered a second time just because inbox compaction later purged the
// acked message and its ack entry from the parent's inbox. ADR-091: each
// upward event starts at most one parent turn.
//
// The "consumed and compacted away" shortcut needs POSITIVE evidence: the
// final was appended AND the parent was woken (durable facts), and the
// parent's inbox still shows compaction's footprint (as many acked messages
// as the retention cap keeps). A missing inbox file, a never-woken append, or
// a thinned-out inbox carry none of that and must fall through to the normal
// redelivery (dedup by message id), never be written off silently.
//
// Oracle: the parent inbox is the observable. After the second boot delivery
// pass the replay id `<child>:<gen>:final` must not be present again (it was
// consumed; compaction removed it; nothing may re-append it), and the
// durable facts must show the final delivered so it stops being pending.

package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// compactedFinalFixture commits a final, publishes it once through the real
// deliverer, optionally records the wake fact, has the parent consume it, and
// then pushes nFillers newer acked messages through a tiny-retention inbox.
type compactedFinalFixture struct {
	h        *bootRecoveryHarness
	al       *AgentLoop
	deliv    *SteerUpwardDeliverer
	parent   string
	child    string
	commitID string
	finalID  string
}

func newCompactedFinalFixture(t *testing.T, recordWake, publishFirst bool, nFillers int) *compactedFinalFixture {
	t.Helper()
	h := newBootRecoveryHarness(t)
	h.inbox.AckedRetentionMax = 2
	h.inbox.CompactionAckedTrigger = 3
	f := &compactedFinalFixture{h: h, commitID: "commit-g1-1027"}
	f.parent = h.rootSession(t)
	f.child = h.newSession(t, session.SessionTypeDelegate, f.parent)
	f.finalID, _, _ = commitAllGenDoneFinal(t, h, f.child, f.parent, f.commitID, "the one and only hand-back")
	f.al, f.deliv = newRealDelivererLoopFixture(t, h)

	if publishFirst {
		if _, err := runRealDelivererPass(t, h, f.al, f.deliv); err != nil {
			t.Fatalf("first delivery pass: %v", err)
		}
		if _, ok := realDelivererInboxMessages(t, h.inbox, f.parent)[f.finalID]; !ok {
			t.Fatalf("control: first pass did not append %s", f.finalID)
		}
	}
	facts := session.FinalDeliveryProgress{InboxAppended: true, WakeRecorded: recordWake}
	state, revision, _, err := h.lifecycle.FinalDeliveryState(f.child, 1, f.commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState: %v", err)
	}
	merged := state.Merge(facts)
	if err = h.lifecycle.UpdateFinalDelivery(f.child, 1, f.commitID, revision, session.FinalDeliveryCommand{Advance: &merged}); err != nil {
		t.Fatalf("record delivery facts: %v", err)
	}
	if publishFirst {
		if _, err = h.inbox.AckDetailed(f.parent, []string{f.finalID}); err != nil {
			t.Fatalf("ack final: %v", err)
		}
	}
	for i := 0; i < nFillers; i++ {
		id := fmt.Sprintf("filler-%d", i)
		var m generated.SessionMessage
		gen := 1
		if err = m.FromSessionMessageHandback(generated.SessionMessageHandback{
			MessageId: id, SessionId: f.child, ParentSessionId: &f.parent,
			CreatedAt: time.Now().UTC(), Depth: 1, Direction: "child_to_parent",
			Generation: &gen, Kind: "handback", Mode: generated.SessionMessageHandbackModeFinal,
			ResultSoFar: "filler", SenderIdentity: "agent-1",
			Artifacts: []string{}, OpenQuestions: []string{}, UntrustedOrigin: true,
		}); err != nil {
			t.Fatalf("build filler: %v", err)
		}
		if _, err = h.inbox.Append(f.parent, m); err != nil {
			t.Fatalf("append filler %d: %v", i, err)
		}
		if _, err = h.inbox.AckDetailed(f.parent, []string{id}); err != nil {
			t.Fatalf("ack filler %d: %v", i, err)
		}
	}
	return f
}

func (f *compactedFinalFixture) finalInInbox(t *testing.T) bool {
	t.Helper()
	_, ok := realDelivererInboxMessages(t, f.h.inbox, f.parent)[f.finalID]
	return ok
}

// Acked + woken + compacted away: consumed, never re-sent, recorded delivered,
// and the shortcut announces itself through the operator notice.
func TestBootFinalDelivery_AckedFinalPurgedByCompaction_IsNotRedelivered(t *testing.T) {
	f := newCompactedFinalFixture(t, true, true, 12)
	if f.finalInInbox(t) {
		t.Fatalf("fixture control: compaction did not purge the acked final %s", f.finalID)
	}
	notices, err := runRealDelivererPass(t, f.h, f.al, f.deliv)
	if err != nil {
		t.Fatalf("second delivery pass: %v", err)
	}
	if f.finalInInbox(t) {
		t.Fatalf("the consumed final %s was re-appended after compaction purged it — a second parent wake for one hand-back (notices %q)", f.finalID, notices)
	}
	after, _, _, err := f.h.lifecycle.FinalDeliveryState(f.child, 1, f.commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState after: %v", err)
	}
	if !after.Delivered() {
		t.Fatalf("durable facts after the second pass = %+v, want delivered", after)
	}
	joined := strings.Join(notices, "\n")
	for _, want := range []string{f.child, f.parent, "consumed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("operator notice %q does not name %q — the shortcut must be visible", joined, want)
		}
	}
}

// No inbox file at all (lost/never created): nothing proves consumption, so the
// final is delivered again (the replay id dedups it if it does turn up).
func TestBootFinalDelivery_MissingInboxFile_IsRedelivered(t *testing.T) {
	f := newCompactedFinalFixture(t, true, false, 0)
	if _, err := runRealDelivererPass(t, f.h, f.al, f.deliv); err != nil {
		t.Fatalf("delivery pass: %v", err)
	}
	if !f.finalInInbox(t) {
		t.Fatalf("a final whose inbox file is missing was written off as consumed instead of redelivered")
	}
}

// Appended but the parent was never recorded as woken, then the message is
// gone: that is not evidence of consumption.
func TestBootFinalDelivery_NeverWokenAppendThenVanished_IsRedelivered(t *testing.T) {
	f := newCompactedFinalFixture(t, false, true, 12)
	if f.finalInInbox(t) {
		t.Fatalf("fixture control: compaction did not purge the final %s", f.finalID)
	}
	if _, err := runRealDelivererPass(t, f.h, f.al, f.deliv); err != nil {
		t.Fatalf("delivery pass: %v", err)
	}
	if !f.finalInInbox(t) {
		t.Fatalf("a never-woken final that vanished from the inbox was written off as consumed instead of redelivered")
	}
}

// Woken and appended, but the inbox holds fewer acked messages than
// compaction always leaves behind: the final cannot have been compacted away
// (torn line, malformed purge), so it is redelivered.
func TestBootFinalDelivery_VanishedWithoutCompactionFootprint_IsRedelivered(t *testing.T) {
	f := newCompactedFinalFixture(t, true, false, 1)
	if _, err := runRealDelivererPass(t, f.h, f.al, f.deliv); err != nil {
		t.Fatalf("delivery pass: %v", err)
	}
	if !f.finalInInbox(t) {
		t.Fatalf("a final with no compaction footprint in the inbox was written off as consumed instead of redelivered")
	}
}
