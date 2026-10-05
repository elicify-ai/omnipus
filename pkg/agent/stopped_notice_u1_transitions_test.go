package agent

// U1 RED — corrected sub-agent control-plane ADR, D6 stopped-child notice.
// This file covers four of the dispatched RED families against REAL stores:
//
//	case 1 — one notice per DIRECT parent on every transition into stopped
//	         (direct stop, timeout, cascade from an ancestor, restart boot);
//	case 2 — the notice's inbox identity is deterministic and deduplicated
//	         across a live retry and a boot replay;
//	case 3 — parent-state routing: a working parent is woken exactly once;
//	         a stopped / waiting-for-answer / done / failed parent retains
//	         the notice with NO wake;
//	case 4 — a delivery failure stays pending and is VISIBLY reported, and
//	         a boot retry after repair delivers exactly once;
//	case 6 — the D6 Goal row: the session-owned goal stays active across
//	         every retained stop/completion path. The former aged-question
//	         subcase is removed: ADR-20261004 locked decision 6 and C2 delete
//	         the owner_required park itself, needs_input, and its 24-hour
//	         expiry outright, with no replacement expiry.
//
// Oracles are the ADR rows only (D6/D8/D2, F0929-6/7, MIN-001/MIN-005);
// helpers live in stopped_child_notice_u1_test.go. The plan-stop transition
// leg lives in plan_member_stop_u1_test.go — the coordinator adjudicated it
// in scope on 2026-10-02 (an earlier revision of this comment called the
// plan member's direct-parent edge an open question; the architect's §3.3
// assessment corrected that premise: D6 names plan stop, D8.10 assigns
// each direct parent the notice, and the edge already exists in SteeredBy
// via StartTaskNow).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// u1LaunchChild is launchRunningChild plus a routable reporting target: an
// upward notice and its parent wake need a destination, exactly as the
// completion race tests set one for Deliver's wake attempt.
func u1LaunchChild(t *testing.T, al *AgentLoop, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	rec := launchRunningChild(t, al, parentID, callID)
	if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
		r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
		return nil
	}); err != nil {
		t.Fatalf("Mutate(reporting target): %v", err)
	}
	return rec
}

// assertU1NoStoppedNoticeFor asserts observerID's inbox holds NO stopped-
// child notice about childID. D6 routes one notice to the child's DIRECT
// parent only — an indirect ancestor never receives a copy (D6/D8.4: "the
// root does not act in C's place on D").
func assertU1NoStoppedNoticeFor(t *testing.T, al *AgentLoop, observerID, childID string) {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(observerID)
	if err != nil {
		t.Fatalf("Entries(%s): %v", observerID, err)
	}
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		raw, merr := entry.Message.MarshalJSON()
		if merr != nil {
			t.Fatalf("MarshalJSON(observer message): %v", merr)
		}
		fields := u1DecodeMessageFields(t, raw)
		if fields["session_id"] != childID {
			continue
		}
		if fields["fatal"] == true || (fields["kind"] == "handback" && fields["mode"] == "final") {
			t.Errorf("indirect ancestor %s received a legacy terminal completion for stopped child %s: %s",
				observerID, childID, raw)
			continue
		}
		if body := u1NoticeBody(fields); body != "" &&
			u1ContainsAll(body, "resume", "redirect") {
			t.Errorf("indirect ancestor %s received a stopped-child notice for %s — "+
				"D6 routes it to the child's DIRECT parent only: %s", observerID, childID, raw)
		}
	}
}

// u1DecodeMessageFields decodes an inbox message's JSON envelope into a
// field map, mirroring assertU1StoppedChildNotice's reading of entries.
func u1DecodeMessageFields(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode inbox message %s: %v", raw, err)
	}
	return fields
}

// u1ContainsAll reports whether every needle is present in s (s is already
// lower-cased by u1NoticeBody).
func u1ContainsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}

// u1StopChild drives the branch's real live-turn stop shape: the stop stamps
// the fence and cancels the turn, and a RUNNING child lands `stopped` when
// its turn unwinds through completeSteeredTurn with context.Canceled (a
// queued child is landed by the stop itself). Fatals if the completion write
// itself fails.
func u1StopChild(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord) {
	t.Helper()
	wireSteerCompletionDeps(t, al)
	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	current, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child after stop): %v", err)
	}
	if current.State == session.LifecycleStopped {
		return
	}
	if err := al.completeSteeredTurn(context.Background(), current,
		turnResult{finalContent: ""}, context.Canceled); err != nil {
		t.Fatalf("completeSteeredTurn(cancelled turn): %v", err)
	}
}

// u1BootRecovery builds a SteerBootRecovery over al's CURRENT stores with a
// captured operator-notice sink, so a test can require boot recovery either
// stays silent (clean replay) or reports a delivery failure visibly (D8.4:
// "If storing a notice fails, keep a durable retry item and report the
// failure ... do not silently claim delivery").
func u1BootRecovery(t *testing.T, al *AgentLoop, operatorNotices *[]string) *SteerBootRecovery {
	t.Helper()
	wireSteerCompletionDeps(t, al)
	lifecycle := al.GetSessionLifecycleStore()
	inbox := al.GetMessageInboxStore()
	// The actual store instance minted for this simulated writing boot is
	// retained, not reconstructed from an execution's admitting boot number.
	bootEpochRegistryMu.Lock()
	writingBoot := bootEpochRegistry[al]
	bootEpochRegistryMu.Unlock()
	return &SteerBootRecovery{
		Lifecycle: lifecycle, Sessions: al.GetSessionStore(), Inbox: inbox,
		BootEpoch:  writingBoot,
		Classifier: NewSteerRecordClassifier(lifecycle, al.GetSessionStore()),
		Deliverer:  al.getUpwardDeliverer(),
		// No EndSessionGoal: MAJ-003 — boot recovery never ends a
		// session-owned goal. The field itself is retired and removed by the
		// backend's Commit B.
		OperatorNotice: func(message string) {
			*operatorNotices = append(*operatorNotices, message)
		},
	}
}

// u1SetParentRecordState drives a steered parent record into a given
// non-working state so leaf-stop routing can be observed against it. The
// store's own invariants are honoured: needs_input carries its NeedsInput,
// failed its reason, stopped its lasting stop note (D2: persistLocked
// rejects a stopped record without one).
func u1SetParentRecordState(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord, state session.LifecycleState) {
	t.Helper()
	if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
		r.State = state
		switch state {
		case session.LifecycleNeedsInput:
			r.NeedsInput = &session.NeedsInput{
				CorrelationID:   "u1-route-q",
				Reconstructable: true,
				TTLDeadline:     time.Now().Add(time.Hour),
			}
		case session.LifecycleFailed:
			r.FailedReason = "failed: boom"
		case session.LifecycleStopped:
			r.StopNote = &session.StopNote{
				At: time.Now().UTC(), By: "human:dan",
				Seq: uint64(r.Generation), Cause: session.StopCauseStop,
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Mutate(parent record -> %s): %v", state, err)
	}
}

// TestU1StoppedNoticeEveryTransitionIntoStopped pins D6's one-notice-per-
// transition rule across the transitions this unit drives directly: a direct
// stop, a lifetime timeout, a cascade swept down from an ancestor, and a
// restart boot sweep. Every leg asserts the SAME invariants through the
// shared helper: the child is stopped and non-terminal at its unchanged
// generation, its DIRECT parent holds exactly one notice carrying the stop
// cause/actor and the decide-offers, and (for the cascade leg) an indirect
// ancestor holds none.
func TestU1StoppedNoticeEveryTransitionIntoStopped(t *testing.T) {
	t.Run("direct stop", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-direct")
		wakes := observeU1ParentNoticeWakes(t, al, parent)
		rec := u1LaunchChild(t, al, parent, "u1-direct")
		u1StopChild(t, al, rec)
		noticeID, _ := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseStop), "dan")
		if got := wakes(noticeID); got != 1 {
			t.Errorf("working-parent wakes for the stopped-child notice = %d, want exactly 1 (D6)", got)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-timeout")
		wakes := observeU1ParentNoticeWakes(t, al, parent)
		rec := u1LaunchChild(t, al, parent, "u1-timeout")
		wireSteerCompletionDeps(t, al)
		// Q19: a session's own lifetime-budget expiry stops the working
		// turn — completionDisposition maps DeadlineExceeded to
		// stopped/OutcomeTimedOut, and no cascade stamps a fence first, so
		// the landing synthesizes the note with cause timeout (D2).
		if err := al.completeSteeredTurn(context.Background(), rec,
			turnResult{finalContent: ""}, context.DeadlineExceeded); err != nil {
			t.Fatalf("completeSteeredTurn(timeout): %v", err)
		}
		noticeID, _ := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseTimeout), "")
		if got := wakes(noticeID); got != 1 {
			t.Errorf("working-parent wakes for the timeout stopped-child notice = %d, want exactly 1 (D6)", got)
		}
	})

	t.Run("cascade from ancestor", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		root := newTestSteeringSession(t, al, "ws-u1-cascade")
		child := u1LaunchChild(t, al, root, "u1-cascade-child")
		grand := u1LaunchChild(t, al, child.SessionID, "u1-cascade-grand")
		canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
		if _, err := canceller.CancelSubtree(context.Background(), child.SessionID,
			steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
			t.Fatalf("CancelSubtree: %v", err)
		}
		// Each stamped child lands stopped as its turn unwinds.
		u1StopChild(t, al, child)
		u1StopChild(t, al, grand)
		// The cascade's direct target and its swept descendant EACH notify
		// their own direct parent (D8.4: in root -> C -> D, the root
		// receives C's stop; C receives D's stop even when C itself is
		// stopped).
		assertU1StoppedChildNotice(t, al, root, child, string(session.StopCauseStop), "dan")
		assertU1StoppedChildNotice(t, al, child.SessionID, grand, string(session.StopCauseCascade), "dan")
		// ... and the indirect ancestor receives nothing about the
		// grandchild — the root does not act in C's place on D (D6/D8.4).
		assertU1NoStoppedNoticeFor(t, al, root, grand.SessionID)
	})

	t.Run("restart boot sweep", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-restart")
		wakes := observeU1ParentNoticeWakes(t, al, parent)
		rec := u1LaunchChild(t, al, parent, "u1-restart")
		var operatorNotices []string
		recovery := u1BootRecovery(t, al, &operatorNotices)
		// D8: a restart is an automatic stop with cause restart; pass two
		// persists the direct-parent notice before any wake.
		if err := recovery.Run(context.Background()); err != nil {
			t.Fatalf("SteerBootRecovery.Run: %v", err)
		}
		if len(operatorNotices) != 0 {
			t.Errorf("boot recovery reported problems on a clean single-child restart: %v", operatorNotices)
		}
		noticeID, _ := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseRestart), "")
		if got := wakes(noticeID); got != 1 {
			t.Errorf("working-parent wakes for the restart stopped-child notice = %d, want exactly 1 (D6/D8.4)", got)
		}
	})
}

// TestU1StoppedNoticeIdentityDeduplicated pins D6's deterministic inbox
// identity (parent_id, child_id, child_generation, stop_seq): a live retry
// of the stop call and a boot replay over fresh store instances neither
// duplicate the notice nor change its identity. ADR-20261004 locked decision
// 1 + C3 require an untaken id to re-ring at boot; dedup applies to the durable
// entry, not to the total wake count across distinct delivery passes.
func TestU1StoppedNoticeIdentityDeduplicated(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parent := newTestSteeringSession(t, al, "ws-u1-identity")
	wakes := observeU1ParentNoticeWakes(t, al, parent)
	rec := u1LaunchChild(t, al, parent, "u1-identity")
	wireSteerCompletionDeps(t, al)
	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	stop := func(when string) {
		t.Helper()
		if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
			steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
			t.Fatalf("CancelSubtree(%s): %v", when, err)
		}
		current, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
		if err != nil {
			t.Fatalf("Load(child after %s): %v", when, err)
		}
		if current.State == session.LifecycleStopped {
			return
		}
		if err := al.completeSteeredTurn(context.Background(), current,
			turnResult{finalContent: ""}, context.Canceled); err != nil {
			t.Fatalf("completeSteeredTurn(%s): %v", when, err)
		}
	}
	stop("first")
	noticeID, note := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseStop), "dan")
	if got := wakes(noticeID); got != 1 {
		t.Errorf("wakes after first delivery = %d, want exactly 1 (D6)", got)
	}

	t.Logf("notice wake phase landing: total=%d, want 1", wakes(noticeID))
	// Live retry: repeating Stop on an already-stopped child changes nothing
	// (D2/MIN-007); it is not a fresh stop transition or a boot delivery pass.
	beforeRetry := wakes(noticeID)
	stop("retry")
	if afterRetry := wakes(noticeID); afterRetry != beforeRetry {
		t.Fatalf("QUESTION: live repeat Stop changed wakes %d -> %d without a new transition; do not pin this as an approved re-ring", beforeRetry, afterRetry)
	}
	assertU1NoticeStable(t, al, parent, rec, string(session.StopCauseStop), "dan", noticeID, note, wakes)
	t.Logf("notice wake phase live repeat Stop: delta=%d, want 0", wakes(noticeID)-beforeRetry)

	// Boot replay: fresh stores prove on-disk entry/identity deduplication.
	// This id remains UNTAKEN, so decision 1/C3 require one new ring on this
	// boot pass. A second ring must not create another durable work item.
	if rerNoteAcked(t, al, parent, noticeID) {
		t.Fatal("setup: notice was taken before boot; the untaken re-ring case is not exercised")
	}
	beforeBoot := wakes(noticeID)
	home := al.GetConfig().Agents.Defaults.Home
	replayU1StoppedNotices(t, al, filepath.Join(home, "session_lifecycle"), filepath.Join(home, "session_messages"))
	idAfterBoot, noteAfterBoot := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseStop), "dan")
	if idAfterBoot != noticeID || noteAfterBoot != note {
		t.Errorf("boot replay changed notice identity or stop event: id %q -> %q; note %s -> %s (D6/C3)", noticeID, idAfterBoot, note, noteAfterBoot)
	}
	if delta := wakes(noticeID) - beforeBoot; delta != 1 {
		t.Errorf("untaken notice's boot-pass wake delta = %d, want exactly 1 (ADR-20261004 decision 1/C3)", delta)
	}
	t.Logf("notice wake phase boot replay: delta=%d, total=%d, want delta 1 and total 2", wakes(noticeID)-beforeBoot, wakes(noticeID))
}

// TestU1SecondStopOfSameGenerationGetsFreshStopSeqAndNotice covers D2's
// fresh execution on same-generation RESUME and D6's stop_seq dedup key.
// Like the W1 two-stop test, it drives real owners through Stop/Revive/
// Dispatch. It additionally pins the new run_id and cleared stop metadata;
// the former unconditional BLOCKED stub is obsolete, not a behaviour oracle.
func TestU1SecondStopOfSameGenerationGetsFreshStopSeqAndNotice(t *testing.T) {
	al, provider, release := w1hSetup(t)
	defer release()
	parent := newTestSteeringSession(t, al, "ws-u1-regen")
	rec := w1hLaunchLiveChild(t, al, provider.entered, parent, "u1-regen")
	lifecycle := al.GetSessionLifecycleStore()
	canceller := NewSteerCanceller(lifecycle)
	owner := w1hOwner("dan")
	stop := func(phase string) {
		t.Helper()
		report, err := canceller.StopTurns(context.Background(), rec.SessionID, owner, false, al.SteerGenerationCancel)
		if err != nil || len(report.Unreachable) != 0 {
			t.Fatalf("StopTurns(%s): report=%+v err=%v", phase, report, err)
		}
		// Wait for the owning execution's entire completion/disposal tail,
		// not just its landed lifecycle value, before trying another admission.
		joinGoalFixtureRuns(t, al)
	}
	stop("first")
	notice1, _ := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseStop), "dan")
	first, err := lifecycle.ListStoppedTransitions(rec.SessionID)
	if err != nil || len(first) != 1 {
		t.Fatalf("first stop history = %s, err=%v; want exactly one transition (D6)", w1hFormatTransitions(first), err)
	}
	if notice1 != w1hNoticeID(parent, rec.SessionID, rec.Generation, first[0].StopSeq) {
		t.Fatalf("first notice identity %q does not name its own stop_seq %d (D6)", notice1, first[0].StopSeq)
	}
	if generation, err := canceller.Revive(context.Background(), rec.SessionID, owner); err != nil || generation != rec.Generation {
		t.Fatalf("same-generation Revive returned generation=%d err=%v, want %d (D2)", generation, err, rec.Generation)
	}
	resumed, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(after Revive): %v", err)
	}
	if resumed.State != session.LifecycleQueued || resumed.Stop != nil || resumed.StopNote != nil || resumed.StopEffect != nil {
		t.Fatalf("Revive did not atomically clear stop metadata and queue replacement: state=%q fence=%+v note=%+v effect=%+v (D2)",
			resumed.State, resumed.Stop, resumed.StopNote, resumed.StopEffect)
	}
	result, err := NewSteerLauncher(al).Dispatch(context.Background(), rec.SessionID, rec.Generation)
	if err != nil || result.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(after Revive) = %+v, err=%v; want real running replacement", result, err)
	}
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("resumed owner never reached its provider — no second live turn to stop")
	}
	// D2's execution-identity clause fixes the admission boundary: persist
	// the fresh tuple before queue/live admission and copy it into the owner.
	// Exercise the whole Revive -> Dispatch path, not the standalone Revive
	// half that has not admitted a replacement yet.
	resumed, err = lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(admitted replacement): %v", err)
	}
	if resumed.ExecutionID == nil || resumed.ExecutionID.RunID == "" || resumed.ExecutionID.RunID == rec.ExecutionID.RunID ||
		resumed.ExecutionID.BootSeq != al.bootEpochFor() {
		t.Fatalf("replacement identity = %+v, want a fresh run_id in the current boot, not original %+v (D2)", resumed.ExecutionID, rec.ExecutionID)
	}
	if handle := al.getActiveTurnState(rec.SessionID); handle == nil || al.tsExecutionClaim(handle, rec.SessionID) != al.executionClaimFor(resumed) {
		t.Fatal("admitted replacement owner does not carry its persisted fresh identity (D2)")
	}
	stop("second")
	landed, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(after second stop): %v", err)
	}
	if landed.State != session.LifecycleStopped || landed.Generation != rec.Generation || landed.Stop != nil || landed.StopNote == nil {
		t.Fatalf("second stop = state %q generation %d fence=%+v note=%+v, want stopped at unchanged generation %d with note and no fence (D2)",
			landed.State, landed.Generation, landed.Stop, landed.StopNote, rec.Generation)
	}
	both, err := lifecycle.ListStoppedTransitions(rec.SessionID)
	if err != nil || len(both) != 2 {
		t.Fatalf("two-stop history = %s, err=%v; want two original transitions (D6)", w1hFormatTransitions(both), err)
	}
	if both[0] != first[0] || both[1].StopSeq <= first[0].StopSeq || both[1].Generation != rec.Generation {
		t.Errorf("two-stop history = %s; want original first stop, same generation and strictly advancing second stop_seq (D2/D6)", w1hFormatTransitions(both))
	}
	notice2 := w1hNoticeID(parent, rec.SessionID, rec.Generation, both[1].StopSeq)
	if notice2 == notice1 {
		t.Errorf("second stop reused notice identity %q, want a fresh identity (D6)", notice2)
	}
	if ids := w1hStoppedNoticeIDsIn(t, al, parent); len(ids) != 2 {
		t.Errorf("direct-parent notice ids = %v, want exactly two distinct stops' entries (D6)", ids)
	}
	for _, transition := range both {
		id := w1hNoticeID(parent, rec.SessionID, rec.Generation, transition.StopSeq)
		messages := w1hNoticesWithID(t, al, parent, id)
		if len(messages) != 1 {
			t.Errorf("notice entries for %s = %d, want exactly 1 (D6)", id, len(messages))
		}
		for _, message := range messages {
			w1hAssertNoticeMatchesTransition(t, message, parent, transition)
		}
	}
}

// TestU1ParentStateRoutingOfStoppedNotice pins D6's routing table: a working
// parent is woken exactly once for its notice; a stopped, waiting-for-answer,
// done or failed parent RETAINS the notice in its inbox with NO wake, reading
// it when explicitly resumed or answered.
func TestU1ParentStateRoutingOfStoppedNotice(t *testing.T) {
	cases := []struct {
		name     string
		state    session.LifecycleState
		wantWake bool
	}{
		{"working parent woken exactly once", session.LifecycleRunning, true},
		{"stopped parent retains without wake", session.LifecycleStopped, false},
		{"waiting-for-answer parent retains without wake", session.LifecycleNeedsInput, false},
		{"done parent retains without wake", session.LifecycleCompleted, false},
		{"failed parent retains without wake", session.LifecycleFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			root := newTestSteeringSession(t, al, "ws-u1-routing")
			mid := u1LaunchChild(t, al, root, "u1-route-mid")
			leaf := u1LaunchChild(t, al, mid.SessionID, "u1-route-leaf")
			if tc.state != session.LifecycleRunning {
				u1SetParentRecordState(t, al, mid, tc.state)
			}
			wakes := observeU1ParentNoticeWakes(t, al, mid.SessionID)
			u1StopChild(t, al, leaf)
			noticeID, _ := assertU1StoppedChildNotice(t, al, mid.SessionID, leaf, string(session.StopCauseStop), "dan")
			if got := wakes(noticeID); tc.wantWake && got != 1 {
				t.Errorf("working-parent wakes = %d, want exactly 1 (D6)", got)
			} else if !tc.wantWake && got != 0 {
				t.Errorf("wakes to a %s parent = %d, want 0 — the parent retains the notice without a wake and reads it when explicitly resumed/answered (D6)", tc.state, got)
			}
		})
	}
}

// TestU1NoticeDeliveryFailureStaysPendingVisibleAndRetries pins D6/D8.4's
// failure rule with the failure injected at the filesystem edge (the parent's
// inbox directory made read-only — the store's own writer, not a mock): the
// stop still lands, the notice stays UNdelivered rather than silently
// applied, the boot retry while still broken REPORTS the failure instead of
// claiming delivery, and a retry after repair delivers exactly once with one
// wake.
func TestU1NoticeDeliveryFailureStaysPendingVisibleAndRetries(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parent := newTestSteeringSession(t, al, "ws-u1-delivery")
	wakes := observeU1ParentNoticeWakes(t, al, parent)
	rec := u1LaunchChild(t, al, parent, "u1-delivery")
	wireSteerCompletionDeps(t, al)
	home := al.GetConfig().Agents.Defaults.Home
	// The inbox store persists one <ownerKey>.jsonl per parent directly in
	// this directory (pkg/session/message_inbox.go::MessageInboxStore.path),
	// and appends go through atomic temp-file renames INSIDE it — so making
	// the directory read-only fails every append at the filesystem edge
	// while reads keep working and pending state stays observable.
	inboxDir := filepath.Join(home, "session_messages")
	// The store creates this directory lazily on first append; create it now
	// so the read-only chmod below has something to bite on.
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox dir): %v", err)
	}
	if err := os.Chmod(inboxDir, 0o500); err != nil {
		t.Fatalf("Chmod(inbox dir, read-only): %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(inboxDir, 0o750) })

	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	// Land the live-turn stop like the branch does. The completion's own
	// upward delivery MAY fail under the injected read-only inbox — that is
	// the injection working, not a fixture bug; what must stay pending is
	// the D6 notice, so the completion error is recorded, not fatal.
	current, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child after stop): %v", err)
	}
	if current.State != session.LifecycleStopped {
		if cerr := al.completeSteeredTurn(context.Background(), current,
			turnResult{finalContent: ""}, context.Canceled); cerr != nil {
			t.Logf("completion delivery failed under the injected read-only inbox (expected): %v", cerr)
		}
	}
	got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	// Frozen ADR Vocabulary/D2: a landed stopped record has no current Stop
	// marker. Notice publication follows the landing; a writer failure leaves
	// the history pending, never the execution running behind a live fence.
	if got.State != session.LifecycleStopped || got.Terminal() || got.Generation != rec.Generation {
		t.Errorf("lifecycle under a failing notice writer = %q, terminal=%v, generation=%d; want non-terminal stopped at generation %d (D2/D6)",
			got.State, got.Terminal(), got.Generation, rec.Generation)
	}
	if got.Stop != nil {
		t.Errorf("landed stop retains fence under a failing notice writer = %+v, want nil (D2)", got.Stop)
	}
	if got.StopNote == nil {
		t.Fatal("landed stop lost its separate lasting note under a failing notice writer (D2)")
	}
	retained := *got.StopNote
	if retained.Cause != session.StopCauseStop || retained.By != "human:dan" || retained.At.IsZero() || retained.Seq == 0 {
		t.Errorf("retained stop note = %+v, want cause stop, actor human:dan, original time and positive stop_seq (D2/D6)", retained)
	}
	history, err := al.GetSessionLifecycleStore().ListStoppedTransitions(rec.SessionID)
	if err != nil || len(history) != 1 {
		t.Fatalf("pending landed history = %s, err=%v; want exactly one durable transition (D6/C3)", w1hFormatTransitions(history), err)
	}
	pending := history[0]
	if pending.SessionID != rec.SessionID || pending.ParentSessionID != parent || pending.Generation != rec.Generation ||
		pending.StopSeq != retained.Seq || pending.Cause != retained.Cause || pending.Actor != retained.By || !pending.At.Equal(retained.At) {
		t.Errorf("pending landed transition = %+v, want the retained stop's exact identity/cause/actor/time (D6/C3)", pending)
	}
	pendingID := w1hNoticeID(parent, rec.SessionID, rec.Generation, pending.StopSeq)
	if count := wakes(pendingID); count != 0 {
		t.Errorf("wakes before a failed notice append is repaired = %d, want 0 — delivery remains pending (D6/D8.4)", count)
	}
	entries, err := al.GetMessageInboxStore().Entries(parent)
	if err != nil {
		t.Fatalf("Entries(parent) under failing writer: %v", err)
	}
	if delivered := u1CountStoppedNotices(t, entries, rec.SessionID); delivered != 0 {
		t.Errorf("parent inbox holds %d stopped-child notices although its directory was unwritable — "+
			"a failed delivery must stay PENDING, never be claimed (D6/D8.4)", delivered)
	}

	// Boot retry while still broken: the failure is REPORTED, not silent.
	var brokenNotices []string
	recovery := u1BootRecovery(t, al, &brokenNotices)
	if err := recovery.Run(context.Background()); err != nil {
		// Recorded, not fatal: post-GREEN the boot retry must SUCCEED while
		// reporting (D8.4 keep a durable retry item); an error today is part
		// of the red picture, and the remaining assertions still run.
		t.Errorf("boot retry while broken returned an error instead of recording the retry and reporting: %v", err)
	}
	if len(brokenNotices) == 0 {
		t.Errorf("boot recovery reported NOTHING while the parent inbox was unwritable — " +
			"D8.4: keep a durable retry item and report the failure; do not silently claim delivery")
	}
	afterBroken, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(after failed boot retry): %v", err)
	}
	if afterBroken.State != session.LifecycleStopped || afterBroken.Stop != nil || afterBroken.StopNote == nil || *afterBroken.StopNote != retained {
		t.Errorf("failed boot retry changed the landed stop: state=%q fence=%+v note=%+v; want stopped, nil fence and retained note %+v (D2/D8)",
			afterBroken.State, afterBroken.Stop, afterBroken.StopNote, retained)
	}
	stillPending, err := al.GetSessionLifecycleStore().ListStoppedTransitions(rec.SessionID)
	if err != nil || len(stillPending) != 1 || stillPending[0] != pending {
		t.Errorf("history after failed boot retry = %s, err=%v; want the same pending transition %+v (D6/C3)", w1hFormatTransitions(stillPending), err, pending)
	}
	if count := wakes(pendingID); count != 0 {
		t.Errorf("failed boot retry rang the undelivered notice %d time(s), want 0 (D6/D8.4)", count)
	}

	// Repair + retry: the pending notice delivers exactly once, identity
	// stable, one wake.
	if err := os.Chmod(inboxDir, 0o750); err != nil {
		t.Fatalf("Chmod(inbox dir, repaired): %v", err)
	}
	replayU1StoppedNotices(t, al, filepath.Join(home, "session_lifecycle"), filepath.Join(home, "session_messages"))
	noticeID, note := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseStop), "dan")
	if noticeID != pendingID {
		t.Errorf("repaired notice id = %q, want pending transition id %q — retry must publish the original stop (D6/C3)", noticeID, pendingID)
	}
	for _, message := range w1hNoticesWithID(t, al, parent, pendingID) {
		w1hAssertNoticeMatchesTransition(t, message, parent, pending)
	}
	if got := wakes(noticeID); got != 1 {
		t.Errorf("wakes across failure + repair + retry = %d, want exactly 1 (D6)", got)
	}
	assertU1NoticeStable(t, al, parent, rec, string(session.StopCauseStop), "dan", noticeID, note, wakes)
}

// u1CountStoppedNotices counts notice-shaped messages about childID in an
// already-read inbox entry slice (the failing-writer leg may not use the
// asserting helper, which fatals on absence).
func u1CountStoppedNotices(t *testing.T, entries []session.InboxEntry, childID string) int {
	t.Helper()
	count := 0
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		raw, err := entry.Message.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON(inbox message): %v", err)
		}
		fields := u1DecodeMessageFields(t, raw)
		if fields["session_id"] != childID {
			continue
		}
		if body := u1NoticeBody(fields); body != "" && u1ContainsAll(body, "resume", "redirect") {
			count++
		}
	}
	return count
}

// TestU1GoalStaysActiveAcrossEveryStopPath pins the D6 Goal row (MAJ-003):
// every session-owned active goal stays active across stop, timeout,
// restart, done and failed. Only /goal clear / clear_goal ends a goal; no
// lifecycle transition may end one (F0929-6). Natural met/exhaustion
// adjudication is independent and deliberately NOT driven here.
func TestU1GoalStaysActiveAcrossEveryStopPath(t *testing.T) {
	assertGoalActive := func(t *testing.T, goalID, path string) {
		t.Helper()
		g, err := resolveGoalRecordStore().Get(goalID)
		if err != nil {
			t.Fatalf("Get(goal after %s): %v", path, err)
		}
		if g.State != generated.GoalStateActive {
			t.Errorf("goal state after %s = %q, want active — D6 Goal row: only clear_goal ends a session-owned goal", path, g.State)
		}
	}
	t.Run("direct stop", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-goal-stop")
		rec := u1LaunchChild(t, al, parent, "u1-goal-stop")
		goalID := activateTestGoalRecord(t, rec.SessionID, "U1 goal open across stop")
		u1StopChild(t, al, rec)
		assertGoalActive(t, goalID, "direct stop")
	})
	t.Run("timeout", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-goal-timeout")
		rec := u1LaunchChild(t, al, parent, "u1-goal-timeout")
		wireSteerCompletionDeps(t, al)
		goalID := activateTestGoalRecord(t, rec.SessionID, "U1 goal open across timeout")
		if err := al.completeSteeredTurn(context.Background(), rec,
			turnResult{finalContent: ""}, context.DeadlineExceeded); err != nil {
			t.Fatalf("completeSteeredTurn(timeout): %v", err)
		}
		assertGoalActive(t, goalID, "lifetime timeout")
	})
	t.Run("restart boot sweep", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-goal-restart")
		rec := u1LaunchChild(t, al, parent, "u1-goal-restart")
		goalID := activateTestGoalRecord(t, rec.SessionID, "U1 goal open across restart")
		var operatorNotices []string
		recovery := u1BootRecovery(t, al, &operatorNotices)
		if err := recovery.Run(context.Background()); err != nil {
			t.Fatalf("SteerBootRecovery.Run: %v", err)
		}
		assertGoalActive(t, goalID, "restart boot sweep")
	})
	t.Run("done", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-goal-done")
		rec := u1LaunchChild(t, al, parent, "u1-goal-done")
		wireSteerCompletionDeps(t, al)
		goalID := activateTestGoalRecord(t, rec.SessionID, "U1 goal open across done")
		if err := al.completeSteeredTurn(context.Background(), rec,
			turnResult{finalContent: "finished the delegated work"}, nil); err != nil {
			t.Fatalf("completeSteeredTurn(done): %v", err)
		}
		assertGoalActive(t, goalID, "done completion")
	})
	t.Run("failed", func(t *testing.T) {
		t.Setenv("OMNIPUS_HOME", t.TempDir())
		al, cleanup := newSteerAL(t)
		defer cleanup()
		wireSteerCompletionDeps(t, al)
		// D2: the producing execution's identity is persisted before admission.
		// Fail the real admitted owner at the provider edge, not a record that
		// was only labelled running and never dispatched.
		provider := newGoalRunGate("", errors.New("boom"))
		installGoalRunProvider(t, al, provider)
		parent := newTestSteeringSession(t, al, "ws-u1-goal-failed")
		rec := launchGoalBearingChild(t, al, parent, "u1-goal-failed", goalChildLaunchOptions{live: true})
		awaitGoalProvider(t, provider)
		assertGoalActive(t, rec.GoalRef, "before provider failure")
		provider.open()
		joinGoalFixtureRuns(t, al)
		failed, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
		if err != nil {
			t.Fatalf("Load(after provider failure): %v", err)
		}
		if failed.State != session.LifecycleFailed || failed.Generation != rec.Generation {
			t.Fatalf("provider failure outcome = %q at generation %d, want failed at admitted generation %d (D2)",
				failed.State, failed.Generation, rec.Generation)
		}
		if failed.FinalDelivery == nil || failed.FinalDelivery.CommitID != rec.ExecutionID.RunID ||
			failed.FinalDelivery.Outcome != string(steer.OutcomeFailed) {
			t.Fatalf("failure outbox = %+v, want the real admitted owner's failed commit (D2)", failed.FinalDelivery)
		}
		assertGoalActive(t, rec.GoalRef, "failed completion")
	})
	// No aged-question subcase: ADR-20261004 locked decision 6 + C2 delete
	// owner_required parking and its needs_input lifecycle outright. Keeping
	// a fixture parked beyond the retired deadline would test a removed state,
	// not the surviving D6 goal rule. No replacement expiry is designed.
}
