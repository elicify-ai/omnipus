// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 D1.8 RED — the relay half of the question-park expiry: when the boot
// consumer expires a question, every OPEN relay OF that question must be
// closed as superseded too (ADR-20260928 D1.8 "question expiry closes open
// relays as superseded").
//
// This is exactly the gap w6_question_expiry_boot_test.go's header reported
// BLOCKED: "the c8 W6 partial has no relay writer, so no relay chain can be
// built without hand-seeding fake records". Nothing here is a fake store:
// every record — the expired asker's park AND the relays — lives in the REAL
// session.QuestionStore over PendingQuestionDir (the durable JSONL sidecar
// pkg/session/question_record.go defines; no production relay writer exists in
// this tree yet, so the relay records are seeded through that real store's
// own Append with the record shape it validates, which is the production
// write path any relay feature must use).
//
// Test plan (elicify-test-writing step 1)
//
//   behaviour under test: after a process restart, SteerBootRecovery.Run —
//     driven exactly as w6_question_expiry_boot_test.go drives it (park
//     through the production message_parent tool, reopen the stores, run the
//     production boot consumer twice) — expires the asker's overdue question
//     AND closes, as superseded, every other OPEN record in the same
//     QuestionStore directory whose Origin or RelayOf names the expired
//     question (asker session id + correlation id). A record whose links name
//     a DIFFERENT question, and a record with no links at all, stay open.
//     The asker's existing expiry behaviour is unchanged: failed
//     (owner_unreachable), exactly one fatal "owner could not be reached" to
//     the DIRECT parent, the superseded sidecar at the UNCHANGED original
//     deadline, the goal still active, no generation minted, and a second
//     boot does not double-fail (T8).
//
//   specification source: ADR-20260928 section "D1 ... 8. The 24-hour
//     question-park limit" — "closes open relays superseded" — plus D1.5
//     (an expired question cannot be reserved) and D5 (one fatal error to
//     the direct parent).
//
//   unit boundary: everything real — LifecycleStore, UnifiedStore,
//     MessageInboxStore, session.QuestionStore (park AND relay seeding),
//     MessageParentTool's production park, SteerBootRecovery.Run with the
//     real upward deliverer. No periodic scheduler exists in this tree and
//     none is invented here — the periodic leg of D1.8/T8 stays BLOCKED per
//     w6_question_expiry_boot_test.go's header.
//
//   case table:
//     expired owner_required asker + OPEN relay with RelayOf -> the relay's
//       tail record reads superseded with its own fields preserved.
//     expired owner_required asker + OPEN relay with Origin -> same closure.
//     OPEN record whose Origin names a question that did NOT expire ->
//       stays open (the closure is keyed to the expired question's pair).
//     OPEN record with no links -> stays open (boot must not close records
//       it cannot tie to the expired question).
//     the asker itself -> the unchanged D1.8 expiry of
//       TestW6QuestionExpiryBoot_ExpiredOwnerRequired_FailsAskerOwnerUnreachableOnce.
//
//   deferred to CHECK: mutation probes (drop the relay scan entirely —
//     killed by the two closure legs; match on correlation id only — killed
//     by the unrelated-origin leg; close every open record — killed by the
//     bystander leg; close relays but break the asker's own expiry — killed
//     by the unchanged-behaviour assertions).

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestW6QuestionExpiryRelay_ExpiredQuestionClosesRelaysSuperseded drives
// D1.8's relay closure: an expired question's boot expiry supersedes the
// OPEN relay records that name it, leaves every unrelated OPEN record alone,
// and leaves the asker's own expiry exactly as the boot test pins it.
func TestW6QuestionExpiryRelay_ExpiredQuestionClosesRelaysSuperseded(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-relay")
	parent := u1LaunchChild(t, al, root, "w6-qexp-relay-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-relay-asker")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 relay expiry: goal must stay active")
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
	// durable sidecar boot reads. Each relay is itself NOT overdue (its
	// deadline is in the future), so the ONLY thing that can close it is the
	// asker's expiry cascading down — that is the D1.8 behaviour under test,
	// not the relay's own deadline.
	store := w6qeQuestionStore(lc)
	relayDeadline := time.Now().Add(48 * time.Hour)
	seedRelay := func(rec *session.LifecycleRecord, corr string, origin, relayOf *session.QuestionRelayLink) {
		t.Helper()
		q := session.PendingQuestion{
			CorrelationID:    corr,
			AskerSessionID:   rec.SessionID,
			AskerGeneration:  rec.Generation,
			Authority:        session.QuestionAuthoritySelfOK,
			OriginalDeadline: relayDeadline,
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
	seedRelay(relayOfRec, "w6-qexp-relay-relayof", nil,
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: correlationID})
	seedRelay(originRec, "w6-qexp-relay-origin",
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: correlationID}, nil)
	seedRelay(otherRec, "w6-qexp-relay-unrelated",
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: "w6-qexp-relay-never-expired"}, nil)
	seedRelay(bystanderRec, "w6-qexp-relay-bystander", nil, nil)

	// The production boot consumer, twice — T8 forbids a double-failure.
	var notices []string
	recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #1: %v", err)
	}

	assertRelayClosed := func(sessionID, corr string, link *session.QuestionRelayLink, when string) {
		t.Helper()
		q := w6qeMustQuestion(t, lc, sessionID)
		if q.Status != session.QuestionStatusSuperseded {
			t.Fatalf("relay %s (%s) status %s after %s, want superseded — D1.8: an expired question closes its open relays as superseded",
				sessionID, corr, q.Status, when)
		}
		if q.Answerable() {
			t.Fatalf("relay %s (%s) still answerable after %s — a superseded relay cannot be reserved (D1.5)", sessionID, corr, when)
		}
		if !q.OriginalDeadline.Equal(relayDeadline) {
			t.Fatalf("relay %s (%s) deadline %s after %s, want its OWN %s unchanged — closing a relay must not rewrite its fields",
				sessionID, corr, q.OriginalDeadline.Format(time.RFC3339Nano), when,
				relayDeadline.Format(time.RFC3339Nano))
		}
		if link != nil {
			got := q.RelayOf
			if got == nil || got.SessionID != link.SessionID || got.CorrelationID != link.CorrelationID {
				t.Fatalf("relay %s (%s) RelayOf after %s = %v, want %v preserved — closing a relay must not rewrite its fields",
					sessionID, corr, when, got, link)
			}
		}
	}
	assertRelayOpen := func(sessionID, corr string, when string) {
		t.Helper()
		q := w6qeMustQuestion(t, lc, sessionID)
		if q.Status != session.QuestionStatusOpen || !q.Answerable() {
			t.Fatalf("relay %s (%s) = (status %s, answerable %t) after %s, want open and answerable — "+
				"a record whose links do not name the EXPIRED question is not that expiry's to close",
				sessionID, corr, q.Status, q.Answerable(), when)
		}
	}

	assertRelayClosed(relayOfRec.SessionID, "w6-qexp-relay-relayof",
		&session.QuestionRelayLink{SessionID: child.SessionID, CorrelationID: correlationID}, "boot #1")
	assertRelayClosed(originRec.SessionID, "w6-qexp-relay-origin", nil, "boot #1")
	assertRelayOpen(otherRec.SessionID, "w6-qexp-relay-unrelated", "boot #1")
	assertRelayOpen(bystanderRec.SessionID, "w6-qexp-relay-bystander", "boot #1")

	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #2: %v", err)
	}

	// The closures survive the second boot unchanged (no flip, no re-close).
	assertRelayClosed(relayOfRec.SessionID, "w6-qexp-relay-relayof", nil, "boot #2")
	assertRelayClosed(originRec.SessionID, "w6-qexp-relay-origin", nil, "boot #2")
	assertRelayOpen(otherRec.SessionID, "w6-qexp-relay-unrelated", "boot #2")
	assertRelayOpen(bystanderRec.SessionID, "w6-qexp-relay-bystander", "boot #2")

	// The asker's existing expiry behaviour is unchanged — the same pins
	// TestW6QuestionExpiryBoot_ExpiredOwnerRequired_FailsAskerOwnerUnreachableOnce
	// holds, now with relays in the same directory.
	after := w6qeMustLoad(t, lc, child.SessionID)
	if after.State != session.LifecycleFailed || after.FailedReason != w6qeReasonOwnerUnreachable {
		t.Fatalf("expired asker after boot = (%s, %q), want (failed, %q) — relay closure must not change D1.8's own expiry",
			after.State, after.FailedReason, w6qeReasonOwnerUnreachable)
	}
	if after.Generation != generation {
		t.Fatalf("asker generation after boot = %d, want %d — expiry must not dispatch a run or mint a generation",
			after.Generation, generation)
	}
	if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); fatals != 1 {
		t.Fatalf("fatal %q notices to the DIRECT parent after two boots = %d, want exactly 1 — "+
			"relay closure must not add or duplicate the asker's parent notice",
			w6qeOwnerUnreachableText, fatals)
	}
	afterQ := w6qeMustQuestion(t, lc, child.SessionID)
	if afterQ.Status != session.QuestionStatusSuperseded || afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("asker sidecar after boot = (status %s, answerable %t, deadline %s), want superseded, unanswerable, %s unchanged",
			afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano),
			deadline.Format(time.RFC3339Nano))
	}
	w6qeAssertGoalActive(t, goalID, "relay expiry at boot")
}
