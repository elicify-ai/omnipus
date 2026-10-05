// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W1 direct-parent stopped-child notice — FENCE-LESS re-ring until taken
// (founder decision, 2026-10-04): "a stop notice keeps ringing until the
// parent takes it, including a stop that has no saved control history, and
// including after a restart. A second ring must not make the parent do the
// work twice. There is no periodic timer."
//
// Updated the same day to ADR-20260928 Correction C3 (every landed stop
// ledgered): the fence-less landing now appends its transition to the
// control ledger in the same Mutate that writes the stop note
// (steer_completion_commit.go::landSteeredStopLocked ->
// session.LifecycleStore.RecordFencelessLandedStopLocked), so the notice's
// identity and its rediscovery come from the LEDGER's landed history alone.
// The note-derived fallback this file once used for identity
// (stoppedTransitionFromLandedNote, the synthesized note's generation
// stand-in seq) is retired: a RESUME clearing the active note must never be
// able to lose an untaken notice, and two fence-less stops in one generation
// must never share one notice id. The store allocates the fence-less line's
// stop_seq as the next monotonic control sequence (never supplied by the
// caller, never the generation stand-in).
//
// Scenario: a real live child's stop lands with NO accepted control and NO
// fence — the synthesized-note landing (steer_completion_commit.go::
// landSteeredStopLocked, reached here through the production completion
// boundary with a cancelled disposition, the same landing a lifetime-budget
// expiry or a legacy cancel produces). Its ledger line fabricates no
// accepted Stop control: ControlID is empty and the landing execution's
// run_id/boot_seq ride the landed projection instead.
//
// Oracles (one row each; spec sources are the founder decision above plus
// Correction C3 and the ledger's own allocation contract — never the code
// under test):
//   - the fence-less landing ledgered EXACTLY ONE transition in the landing
//     itself: ControlID empty (no fabricated accepted control), the store-
//     allocated stop_seq (the first landed line of a fresh child is seq 1 —
//     the same one per-child monotonic progression the fenced writer
//     applies), the child's generation, the direct parent, cause stop
//   - the notice is composed from that ledger transition: the existing
//     stopped-notice id (parent, child, generation, REAL stop_seq) and text,
//     exactly one durable line, and it rings once
//   - an unacked note re-rings on the next delivery pass (boot) — exactly
//     once per pass, including after a restart
//   - repeated rings keep ONE durable line, never re-stop the child, never
//     append history
//   - after the parent's production ack, further passes ring ZERO times
//
// Reuses the shared W1 harness and the fenced pack's restart/ack helpers
// (stopped_notice_rering_test.go) unchanged: same production boot entry, same
// durable stores, same ring observation seam.
//
// Deferred to CHECK (elicify-test-writing step 4 items 2-3, this dispatch's
// rules): green-after-implementation and the mutation probes — re-deriving
// the notice from the retained note again (killed by the ledger-exactly-one
// oracle), reusing the generation as the fence-less stop_seq (killed by the
// two-stops identity pack in stopped_notice_fenceless_ledger_test.go),
// re-appending on retry (killed by the one-durable-line oracle), ringing
// after the ack (killed by the taken-note leg).
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
	// control-ledger history — the landing below synthesizes the note and
	// ledgeres this child's first landed stop.
	pre, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("setup: Load(%s): %v", childID, err)
	}
	if pre.Stop != nil || pre.StopNote != nil {
		t.Fatalf("setup: child carries Stop=%v StopNote=%v, want neither — the landing must be genuinely fence-less", pre.Stop, pre.StopNote)
	}
	if trs, err := lifecycle.ListStoppedTransitions(childID); err != nil || len(trs) != 0 {
		t.Fatalf("setup: landed history = %s (err %v), want none — the fence-less landing below must be this child's first landed stop", w1hFormatTransitions(trs), err)
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

	// ORACLE (C3 — the notice's identity lives in the CONTROL LEDGER): the
	// landing ledgered exactly one transition in the same lock hold that
	// wrote the note. It fabricates no accepted Stop control (empty
	// ControlID) and carries the store-allocated stop_seq — the first landed
	// line of a fresh child takes the ledger's first monotonic sequence (1),
	// the same one per-child progression the fenced writer applies (D4);
	// never the note's generation stand-in. A landing without this line
	// would lose its notice the moment a RESUME cleared the note.
	trs, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after the fence-less landing): %v", err)
	}
	if len(trs) != 1 {
		t.Fatalf("landed history after the fence-less landing = %s, want exactly 1 transition — the landing ledgered it in the same lock hold (C3); an unledgered fence-less stop's notice could not survive a resume clearing the note", w1hFormatTransitions(trs))
	}
	tr := trs[0]
	if tr.ControlID != "" {
		t.Fatalf("fence-less transition carries control_id %q, want empty — a fence-less stop fabricates no accepted Stop control (C3)", tr.ControlID)
	}
	if tr.StopSeq != 1 || tr.Generation != generation || tr.ParentSessionID != parentID || tr.Cause != session.StopCauseStop {
		t.Fatalf("fence-less transition = %s, want {seq:1 gen:%d parent:%q cause:stop} — the store allocates the first monotonic stop_seq for a fresh child's first landed stop (D4), the landing names the direct parent, and the cancelled disposition is a stop", w1hFormatTransitions(trs), generation, parentID)
	}

	// ORACLE (D6 id and content, existing format only): the notice carries
	// the existing stopped-child id composed from the LEDGER transition's
	// real stop_seq, and the existing stopped-child text for that
	// transition's tuple. No second message format is invented.
	noticeID := w1hNoticeID(parentID, childID, generation, tr.StopSeq)
	notices := w1hNoticesWithID(t, al, parentID, noticeID)
	if len(notices) != 1 {
		t.Fatalf("parent inbox holds %d line(s) of %s after the landing, want exactly 1 — the fence-less landing publishes the one D6 notice discovered from the ledger", len(notices), noticeID)
	}
	w1hAssertNoticeMatchesTransition(t, notices[0], parentID, tr)
	if c := wakes.count(noticeID); c != 1 {
		t.Fatalf("the landing rang the parent %d time(s) for %s, want exactly 1", c, noticeID)
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
	// parent again for the unacked fence-less note — exactly once. The
	// replay discovers the transition from the LEDGER history: the record's
	// note is never re-read.
	rerStartupPass(t, restart, childID)
	if got := rings.count(noticeID); got != 1 {
		t.Errorf("after the boot pass the restart rang the parent %d time(s) for the unacked fence-less notice %s, want exactly 1 — a stop with no accepted control still keeps ringing after a restart", got, noticeID)
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
	// still exactly the ONE ledgered transition — unchanged tuple, never a
	// second line — and the child still stopped at its generation.
	if got := len(w1hNoticesWithID(t, restart, parentID, noticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after two rings, want exactly 1 — repeated rings never duplicate the note", got, noticeID)
	}
	if trs2, err := restart.GetSessionLifecycleStore().ListStoppedTransitions(childID); err != nil || len(trs2) != 1 || w1hFormatTransitions(trs2) != w1hFormatTransitions(trs) {
		t.Errorf("landed history after the rings = %s (err %v), want still exactly [%s] — a ring is a doorbell, it never appends history", w1hFormatTransitions(trs2), err, w1hFormatTransitions(trs))
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
