// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 D1.8 relay pack — SUPERSEDED by the steering-commands amendment
// (Correction C2): the question expiry AND its relay closer are REMOVED from
// boot with no replacement. The rule this pack now pins: boot NEVER closes
// any question record — not an asker's own aged park, not a relay linked to
// it by RelayOf or Origin, not an unrelated record — whatever any record's
// age. Every open record boot finds stays open and answerable with its own
// fields preserved.
//
// (The pack was RED for the original D1.8 relay closure: "question expiry
// closes open relays as superseded". That outcome is retired, not deleted;
// the same scenario — a real park plus relay records seeded through the REAL
// session.QuestionStore, then the production boot consumer twice — now pins
// the superseding no-closure rule at the same strength, with the retired
// superseded-closure outcome asserted negatively through the status oracle.)
//
// Nothing here is a fake store: every record — the aged asker's park AND the
// relays — lives in the REAL session.QuestionStore over PendingQuestionDir
// (the durable JSONL sidecar pkg/session/question_record.go defines), seeded
// through that real store's own Append with the record shape it validates.
//
// Test plan (elicify-test-writing step 1)
//
//   behaviour under test: after a process restart, SteerBootRecovery.Run —
//     driven exactly as w6_question_expiry_boot_test.go drives it (park
//     through the production message_parent tool, reopen the stores, run
//     the production boot consumer twice) — leaves every OPEN record in the
//     same QuestionStore directory open and answerable: records whose
//     Origin or RelayOf name the asker's aged question, a record whose
//     links name a different question, and a record with no links at all.
//     The asker itself keeps its needs_input record unchanged, with no
//     fatal notice to its direct parent and its goal still active.
//
//   specification source: the amendment's Correction C2 (the expiry, its
//     relay closer and its notice are removed; no replacement expiry) —
//     never the code under test.
//
//   unit boundary: everything real — LifecycleStore, UnifiedStore,
//     MessageInboxStore, session.QuestionStore (park AND relay seeding),
//     MessageParentTool's production park, SteerBootRecovery.Run with the
//     real upward deliverer. No scheduler exists in this tree and none is
//     invented here.
//
//   case table (one row per seeded record):
//     the asker's own park (48h past its original deadline) -> stays
//       needs_input / open / answerable at the ORIGINAL deadline; zero
//       fatals; goal active.
//     OPEN relay with RelayOf naming the aged question (aged past its own
//       deadline) -> stays open, fields preserved.
//     OPEN record with Origin naming the aged question (aged past its own
//       deadline) -> stays open, fields preserved.
//     OPEN record whose Origin names a DIFFERENT correlation (fresh) ->
//       stays open.
//     OPEN record with no links (fresh) -> stays open.
//     all of the above re-asserted after the SECOND boot (no flip).
//
//   deferred to CHECK (skill step 4 items 2-3): green-after-implementation
//     and the mutation probes — re-adding any closure keyed to the asker's
//     question (killed by the two linked-record legs), closing every open
//     record (killed by the bystander leg), closing only aged records
//     (killed by the mixed-age seeding), failing the asker (killed by the
//     asker oracle).
//
//   known gaps: the D1.4 provenance writer stays with its own pack; no
//     periodic scheduler exists to drive (none is invented here).

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestW6QuestionExpiryRelay_BootNeverClosesQuestionRecords (was
// TestW6QuestionExpiryRelay_ExpiredQuestionClosesRelaysSuperseded) drives
// the superseding rule: boot closes NO question record — the asker's aged
// park, relays linked to it, and unlinked bystanders all stay open and
// answerable with their own fields preserved, across two boots.
func TestW6QuestionExpiryRelay_BootNeverClosesQuestionRecords(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-relay")
	parent := u1LaunchChild(t, al, root, "w6-qexp-relay-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-relay-asker")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 relay no-closure: goal must stay active")
	relayOfRec := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-relay-of")
	originRec := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-relay-origin")
	otherRec := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-relay-other")
	bystanderRec := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-relay-bystander")

	correlationID := "w6-qexp-relay-expired"
	parkClock := time.Now().Add(-48 * time.Hour)
	deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
		session.QuestionAuthorityOwnerRequired, parkClock)

	lc, inbox := w6qeReopen(t, al)

	// Seed the relay records through the REAL QuestionStore — the same
	// durable sidecar boot would have read. Two linked records are seeded
	// AGED (their own deadlines long past) and two unlinked/other records
	// FRESH, so the no-closure rule is pinned for every age: no record's
	// status may depend on any deadline.
	store := w6qeQuestionStore(lc)
	agedDeadline := time.Now().Add(-48 * time.Hour)
	freshDeadline := time.Now().Add(48 * time.Hour)
	seedRelay := func(rec *session.LifecycleRecord, corr string, recordDeadline time.Time, origin, relayOf *session.QuestionRelayLink) {
		t.Helper()
		q := session.PendingQuestion{
			CorrelationID:    corr,
			AskerSessionID:   rec.SessionID,
			AskerGeneration:  rec.Generation,
			Authority:        session.QuestionAuthoritySelfOK,
			OriginalDeadline: recordDeadline,
			Origin:           origin,
			RelayOf:          relayOf,
			Status:           session.QuestionStatusOpen,
		}
		if err := store.Append(q); err != nil {
			t.Fatalf("QuestionStore.Append(relay %s): %v", rec.SessionID, err)
		}
		seeded, err := store.Load(rec.SessionID)
		if err != nil {
			t.Fatalf("QuestionStore.Load(relay %s) after seed: %v", rec.SessionID, err)
		}
		if seeded.Status != session.QuestionStatusOpen || seeded.AskerSessionID != rec.SessionID {
			t.Fatalf("seed of relay %s did not land an open record: status=%s asker=%s",
				rec.SessionID, seeded.Status, seeded.AskerSessionID)
		}
	}
	seedRelay(relayOfRec, "w6-qexp-relay-relayof", agedDeadline, nil,
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: correlationID})
	seedRelay(originRec, "w6-qexp-relay-origin", agedDeadline,
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: correlationID}, nil)
	seedRelay(otherRec, "w6-qexp-relay-unrelated", freshDeadline,
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: "w6-qexp-relay-never-expired"}, nil)
	seedRelay(bystanderRec, "w6-qexp-relay-bystander", freshDeadline, nil, nil)

	// The production boot consumer, twice — the second run must change
	// nothing, exactly as the retired expiry's idempotence guarantee did.
	var notices []string
	recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #1: %v", err)
	}

	assertRecordStillOpen := func(sessionID, corr string, recordDeadline time.Time, link *session.QuestionRelayLink, when string) {
		t.Helper()
		q := w6qeMustQuestion(t, lc, sessionID)
		if q.Status != session.QuestionStatusOpen || !q.Answerable() {
			t.Fatalf("record %s (%s) = (status %s, answerable %t) after %s, want open and answerable — "+
				"C2: boot closes no question record, whatever its age or links", sessionID, corr, q.Status, q.Answerable(), when)
		}
		if !q.OriginalDeadline.Equal(recordDeadline) {
			t.Fatalf("record %s (%s) deadline %s after %s, want its OWN %s unchanged — boot must not rewrite any record's fields",
				sessionID, corr, q.OriginalDeadline.Format(time.RFC3339Nano), when,
				recordDeadline.Format(time.RFC3339Nano))
		}
		if link != nil {
			got := q.RelayOf
			if got == nil || got.SessionID != link.SessionID || got.CorrelationID != link.CorrelationID {
				t.Fatalf("record %s (%s) RelayOf after %s = %v, want %v preserved — boot must not rewrite any record's fields",
					sessionID, corr, when, got, link)
			}
		}
	}

	assertRecordStillOpen(relayOfRec.SessionID, "w6-qexp-relay-relayof", agedDeadline,
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: correlationID}, "boot #1")
	assertRecordStillOpen(originRec.SessionID, "w6-qexp-relay-origin", agedDeadline, nil, "boot #1")
	assertRecordStillOpen(otherRec.SessionID, "w6-qexp-relay-unrelated", freshDeadline, nil, "boot #1")
	assertRecordStillOpen(bystanderRec.SessionID, "w6-qexp-relay-bystander", freshDeadline, nil, "boot #1")

	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #2: %v", err)
	}

	// The no-closure rule survives the second boot unchanged (no flip).
	assertRecordStillOpen(relayOfRec.SessionID, "w6-qexp-relay-relayof", agedDeadline, nil, "boot #2")
	assertRecordStillOpen(originRec.SessionID, "w6-qexp-relay-origin", agedDeadline, nil, "boot #2")
	assertRecordStillOpen(otherRec.SessionID, "w6-qexp-relay-unrelated", freshDeadline, nil, "boot #2")
	assertRecordStillOpen(bystanderRec.SessionID, "w6-qexp-relay-bystander", freshDeadline, nil, "boot #2")

	// ORACLE: the asker's own aged park survives too — still needs_input
	// with the same park at the same generation, its sidecar open and
	// answerable at the ORIGINAL deadline, zero fatal notices of any text
	// (and specifically none with the retired owner-unreachable outcome),
	// and its goal still active.
	after := w6qeMustLoad(t, lc, child.SessionID)
	if after.State != session.LifecycleNeedsInput || after.NeedsInput == nil {
		t.Fatalf("aged asker after boot = (%s, needs_input %v), want needs_input — C2: a helper question does not expire and does not fail its helper",
			after.State, after.NeedsInput)
	}
	if after.Generation != generation {
		t.Fatalf("asker generation after boot = %d, want %d — boot must not dispatch a run for an aged question",
			after.Generation, generation)
	}
	if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 0 {
		t.Fatalf("boot produced %d fatal error notice(s) for the aged asker, want 0 — the expiry notice is retired with the expiry (C2)", fatals)
	}
	if ownerFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); ownerFatals != 0 {
		t.Fatalf("boot produced %d %q fatal(s), want 0 — that outcome is retired (C2)", ownerFatals, w6qeOwnerUnreachableText)
	}
	afterQ := w6qeMustQuestion(t, lc, child.SessionID)
	if afterQ.Status != session.QuestionStatusOpen || !afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("asker sidecar after boot = (status %s, answerable %t, deadline %s), want open, answerable, %s unchanged — boot closes no question record",
			afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano),
			deadline.Format(time.RFC3339Nano))
	}
	w6qeAssertGoalActive(t, goalID, "relay no-closure at boot")
}
