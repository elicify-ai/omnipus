// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W1 direct-parent stopped-child notice — FENCE-LESS re-ring until taken
// (founder decision, 2026-10-04, this dispatch's brief): "a stop notice keeps
// ringing until the parent takes it, including a stop that has no saved
// control history, and including after a restart. A second ring must not make
// the parent do the work twice. There is no periodic timer."
//
// Scenario: a real live child's stop lands with NO accepted control and NO
// fence — the synthesized-note landing (steer_completion_commit.go::
// landSteeredStopLocked, reached here through the production completion
// boundary with a cancelled disposition, the same landing a lifetime-budget
// expiry or a legacy cancel produces). Its StopSeq is the synthesized note's
// own generation stand-in; no control-ledger landed history exists for it
// (RecordLandedStopLocked refuses to fabricate one).
//
// Known-red reason at the pre-fix pin (verified by reading, before any run):
// the landing pass delivered the notice ONCE from the in-memory commit
// result (steer_completion.go's landed == nil && landedNote != nil branch),
// while the replay path — stopped_notice.go::deliverLandedStopNotices —
// discovers notices ONLY from ListStoppedTransitions. A later delivery pass
// and a restart therefore had nothing to discover: the boot passes below
// rang ZERO times, and an untaken fence-less notice sat silent forever. The
// fix makes the retained note — already durable on the landed record —
// discoverable by that same replay path; the tests then pass every oracle.
//
// Oracles (one row each, all from the founder decision):
//   - the fence-less landing publishes exactly one notice with the EXISTING
//     stopped-child id (generation stand-in seq) and text, and rings once
//   - no ledger history is fabricated for it (one landing, zero controls)
//   - an unacked note re-rings on the next delivery pass (boot) — exactly
//     once per pass
//   - repeated rings keep ONE durable line, never re-stop the child, never
//     write history
//   - after the parent's production ack, further passes ring ZERO times
//
// Reuses the shared W1 harness and the fenced pack's restart/ack helpers
// (stopped_notice_rering_test.go) unchanged: same production boot entry, same
// durable stores, same ring observation seam.
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestStoppedNotice_FenceLessStop_ReringsUntilTaken(t *testing.T) {
	al, _, _ := w1hSetup(t)
	provider, releaseTurns := w1hInstallSignalProvider(t, al)
	t.Cleanup(releaseTurns)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-fenceless-rering")
	wakes := w1hObserveWakes(t, al)
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-fenceless-rering")
	childID, generation := rec.SessionID, rec.Generation

	// Preconditions for THE fence-less landing: no fence, no note, no
	// control-ledger history — the landing below synthesizes the note.
	pre, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("setup: Load(%s): %v", childID, err)
	}
	if pre.Stop != nil || pre.StopNote != nil {
		t.Fatalf("setup: child carries Stop=%v StopNote=%v, want neither — the landing must be genuinely fence-less", pre.Stop, pre.StopNote)
	}
	if trs, err := lifecycle.ListStoppedTransitions(childID); err != nil || len(trs) != 0 {
		t.Fatalf("setup: landed history = %s (err %v), want none — a fence-less stop has no accepted control", w1hFormatTransitions(trs), err)
	}

	// The production completion boundary lands the stop with a cancelled
	// disposition and no fence — the synthesized-note (fence-less) landing.
	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{}, context.Canceled); err != nil {
		t.Fatalf("completeSteeredTurn (fence-less stop landing): %v", err)
	}

	// ORACLE (the landing is fence-less and durable): stopped at the same
	// generation, the note retained, the fence still absent.
	cur, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after landing): %v", err)
	}
	if cur.State != session.LifecycleStopped || cur.Generation != generation {
		t.Fatalf("after the landing the child is state=%q generation=%d, want stopped at generation %d", cur.State, cur.Generation, generation)
	}
	if cur.StopNote == nil || cur.Stop != nil {
		t.Fatalf("after the landing StopNote=%v Stop=%v, want a retained note and no fence — the fence-less landing's identity", cur.StopNote, cur.Stop)
	}

	// ORACLE (D6 id and content, existing format only): the notice carries
	// the existing stopped-child id composed from the retained note's
	// generation stand-in seq, and the existing stopped-child text for that
	// note's tuple. No second message format is invented.
	tr := stoppedTransitionFromLandedNote(cur, cur.StopNote)
	noticeID := w1hNoticeID(parentID, childID, generation, uint64(generation))
	if tr.StopSeq != uint64(generation) || tr.ParentSessionID != parentID {
		t.Fatalf("note-derived transition = %s, want {seq:%d parent:%q} — the synthesized note's generation stand-in", w1hFormatTransitions([]session.StoppedTransition{tr}), generation, parentID)
	}
	notices := w1hNoticesWithID(t, al, parentID, noticeID)
	if len(notices) != 1 {
		t.Fatalf("parent inbox holds %d line(s) of %s after the landing, want exactly 1 — the fence-less landing publishes the one D6 notice", len(notices), noticeID)
	}
	w1hAssertNoticeMatchesTransition(t, notices[0], parentID, tr)
	if c := wakes.count(noticeID); c != 1 {
		t.Fatalf("the landing rang the parent %d time(s) for %s, want exactly 1", c, noticeID)
	}
	// ORACLE (D8.5/D4): the fence-less landing fabricates no ledger history.
	if trs, err := lifecycle.ListStoppedTransitions(childID); err != nil || len(trs) != 0 {
		t.Fatalf("landed history after the fence-less landing = %s (err %v), want none — no accepted control, no fabricated history", w1hFormatTransitions(trs), err)
	}
	if rerNoteAcked(t, al, parentID, noticeID) {
		t.Fatalf("the notice %s is already acked at the landing — the re-ring scenario requires an untaken note", noticeID)
	}

	// The parent never took the note. The process "dies"; the restart is a
	// fresh loop over the same durable home — its doorbell born silent.
	restart := rerRestartLoop(t, al.GetConfig().Agents.Defaults.Home)
	rings := w1hObserveWakes(t, restart)
	if c := rings.count(noticeID); c != 0 {
		t.Fatalf("setup: the fresh process's doorbell already rang %d time(s) for %s — it must start silent", c, noticeID)
	}

	// ORACLE (founder decision — after a restart): the boot pass rings the
	// parent again for the unacked fence-less note — exactly once.
	rerStartupPass(t, restart, childID)
	if got := rings.count(noticeID); got != 1 {
		t.Errorf("after the boot pass the restart rang the parent %d time(s) for the unacked fence-less notice %s, want exactly 1 — a stop with no saved control history still keeps ringing after a restart", got, noticeID)
	}

	// ORACLE (the ring rides the stored note): still exactly one line, the
	// existing id — never a re-append, never a second id.
	if got := len(w1hNoticesWithID(t, restart, parentID, noticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after the re-ring, want exactly 1 — the re-ring re-rings the stored note", got, noticeID)
	}
	if ids := w1hStoppedNoticeIDsIn(t, restart, parentID); len(ids) != 1 || ids[0] != noticeID {
		t.Errorf("parent inbox stopped-notice ids = %v, want exactly [%s] — no second stop-notice format or id", ids, noticeID)
	}

	// ORACLE (founder decision — every delivery pass): the next pass rings
	// again, exactly once, while the note stays untaken. (There is no
	// periodic timer and this test adds none — this is the next delivery
	// pass, the rule any such cycle must satisfy.)
	rerStartupPass(t, restart, childID)
	if got := rings.count(noticeID); got != 2 {
		t.Errorf("after the second delivery pass the parent was rung %d time(s) total for unacked %s, want exactly 2 — every delivery pass rings until the parent takes the note", got, noticeID)
	}

	// ORACLE (a second ring never applies the stop twice): one durable line,
	// still zero ledger history, the child still stopped at its generation.
	if got := len(w1hNoticesWithID(t, restart, parentID, noticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after two rings, want exactly 1 — repeated rings never duplicate the note", got, noticeID)
	}
	if trs, err := restart.GetSessionLifecycleStore().ListStoppedTransitions(childID); err != nil || len(trs) != 0 {
		t.Errorf("landed history after the rings = %s (err %v), want still none — a ring is a doorbell, it never fabricates history", w1hFormatTransitions(trs), err)
	}
	rungCur, err := restart.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(after two rings): %v", err)
	}
	if rungCur.State != session.LifecycleStopped || rungCur.Generation != generation {
		t.Errorf("after two rings the child record is state=%q generation=%d, want stopped at generation %d — a second ring must not apply the stop a second time", rungCur.State, rungCur.Generation, generation)
	}

	// The parent TAKES the note — the production ack the parent's wake
	// consumer makes (loop_inbound.go's inbox.Ack of the message id).
	if err := restart.GetMessageInboxStore().Ack(parentID, []string{noticeID}); err != nil {
		t.Fatalf("parent ack of %s: %v", noticeID, err)
	}
	if !rerNoteAcked(t, restart, parentID, noticeID) {
		t.Fatalf("the ack of %s did not persist — the taken-note stop condition is unobservable", noticeID)
	}

	// ORACLE (founder decision — "until the parent takes it"): a further
	// delivery pass after the ack rings ZERO times.
	rerStartupPass(t, restart, childID)
	if got := rings.count(noticeID); got != 2 {
		t.Errorf("after the note was taken a further pass left the ring count at %d, want still 2 — a taken note must never ring the parent into doing the work twice", got)
	}
}
