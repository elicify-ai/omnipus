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
//	         every stop path; since the steering-commands amendment's
//	         Correction C2 removed the 24-hour question expiry with no
//	         replacement, the former question-expiry leg now pins the
//	         superseding rule instead — a question parked long past its
//	         original deadline neither expires nor stops or fails its
//	         helper, and the goal stays active across its boot.
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
// duplicate the notice nor change its identity, and the working parent is
// woken exactly once across all of it.
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

	// Live retry: the stop call replayed on the already-stopped child must
	// neither re-stamp a fresh stop_seq nor append a second notice.
	stop("retry")
	assertU1NoticeStable(t, al, parent, rec, string(session.StopCauseStop), "dan", noticeID, note, wakes)

	// Boot replay: fresh store instances prove on-disk deduplication, not an
	// in-memory one (D8.4: repeated sweeps neither create new transitions
	// for an already-stopped child nor replay wakes after a consumed id).
	home := al.GetConfig().Agents.Defaults.Home
	replayU1StoppedNotices(t, al, filepath.Join(home, "session_lifecycle"), filepath.Join(home, "session_messages"))
	assertU1NoticeStable(t, al, parent, rec, string(session.StopCauseStop), "dan", noticeID, note, wakes)
}

// TestU1SecondStopOfSameGenerationGetsFreshStopSeqAndNotice covers the D6
// dedup key's stop_seq component: stopping the SAME generation a second
// time (after an explicit same-generation RESUME cleared the note) mints a
// fresh stop_seq and therefore a FRESH notice — dedup must not swallow it.
// The same-generation RESUME seam does not exist yet (the delegate action
// enum has no resume action, and SteerCanceller.Revive mints a NEW
// generation instead of resuming the same one), so the second half of the
// behaviour cannot be driven at all.
func TestU1SecondStopOfSameGenerationGetsFreshStopSeqAndNotice(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parent := newTestSteeringSession(t, al, "ws-u1-regen")
	rec := u1LaunchChild(t, al, parent, "u1-regen")
	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
		t.Fatalf("CancelSubtree(first): %v", err)
	}
	assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseStop), "dan")
	t.Fatal("BLOCKED: same-generation RESUME is not implemented — required by ADR D2 CRIT-001 " +
		"(same-generation resume and restart fence) and F0929-5, so a second stop of the same " +
		"generation can mint a fresh stop_seq and a fresh notice per D6's dedup key")
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
	// D2: the stop itself is durably registered as the current-generation
	// fence even while the notice write fails. (The completion path lands
	// the STATE only after delivery succeeds — deliver-first, boot repairs
	// the gap — so the state may still read running under a failing writer;
	// the fence is what must not be lost.)
	if got.Stop == nil || got.Stop.Generation != got.Generation {
		t.Errorf("stop fence under a failing notice writer = %+v, want a live current-generation fence — the stop is durably registered even when its notice cannot be written (D2)", got.Stop)
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

	// Repair + retry: the pending notice delivers exactly once, identity
	// stable, one wake.
	if err := os.Chmod(inboxDir, 0o750); err != nil {
		t.Fatalf("Chmod(inbox dir, repaired): %v", err)
	}
	replayU1StoppedNotices(t, al, filepath.Join(home, "session_lifecycle"), filepath.Join(home, "session_messages"))
	noticeID, note := assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseStop), "dan")
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
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-goal-failed")
		rec := u1LaunchChild(t, al, parent, "u1-goal-failed")
		wireSteerCompletionDeps(t, al)
		goalID := activateTestGoalRecord(t, rec.SessionID, "U1 goal open across failed")
		if err := al.completeSteeredTurn(context.Background(), rec,
			turnResult{finalContent: ""}, errors.New("boom")); err != nil {
			t.Fatalf("completeSteeredTurn(failed): %v", err)
		}
		assertGoalActive(t, goalID, "failed completion")
	})
	t.Run("aged question", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		parent := newTestSteeringSession(t, al, "ws-u1-goal-aged-question")
		rec := u1LaunchChild(t, al, parent, "u1-goal-aged-question")
		goalID := activateTestGoalRecord(t, rec.SessionID, "U1 goal open across an aged question")
		// A question parked 24h past its original deadline — the exact shape
		// the retired 24h expiry (ADR D1.8, removed by the steering-commands
		// amendment's Correction C2 with no replacement) used to fail the
		// helper on. u1BootRecovery wires the completion deps this park's
		// message_parent tool delivers through.
		w6qeParkQuestion(t, al, rec.SessionID, "u1-goal-aged-question-corr",
			session.QuestionAuthorityOwnerRequired, time.Now().Add(-48*time.Hour))
		var operatorNotices []string
		recovery := u1BootRecovery(t, al, &operatorNotices)
		if err := recovery.Run(context.Background()); err != nil {
			t.Fatalf("SteerBootRecovery.Run: %v", err)
		}
		// The aged question neither expires nor stops or fails its helper:
		// after boot the helper is still parked in needs_input — never
		// stopped, never failed — and its goal stays active (F0929-6).
		cur := w6qeMustLoad(t, al.GetSessionLifecycleStore(), rec.SessionID)
		if cur.State != session.LifecycleNeedsInput || cur.NeedsInput == nil {
			t.Fatalf("helper with an aged question after boot = (%s, needs_input %v), want needs_input — "+
				"C2: a helper question does not expire and does not park (fail) its helper", cur.State, cur.NeedsInput)
		}
		if cur.Generation != rec.Generation {
			t.Fatalf("helper generation after boot = %d, want %d — an aged question must not dispatch a run or mint a generation",
				cur.Generation, rec.Generation)
		}
		assertGoalActive(t, goalID, "aged question boot sweep")
	})
}
