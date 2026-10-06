package agent

// U1 RED — corrected sub-agent control-plane ADR, two dispatched families
// against real stores:
//
//	case 7 — the completion frontier (D6 Q2=B): hasRunningOrQueuedDescendant
//	         must count a needs_input descendant as BLOCKING, and a stopped
//	         descendant must NOT block and must CUT the traversal of its own
//	         subtree (D8.6: a restart-stopped descendant does not block a
//	         claim by itself — the D6 notice, not a blocking frontier row,
//	         is what tells the parent).
//	case 8 — #1053 (D6/D7): a genuinely failed parent lands promptly and
//	         cascade-stops its reachable active descendants through the D7
//	         machinery — each stopped with goals kept and its own D6 notice
//	         to ITS direct parent — and the fatal hand-back to the
//	         grandparent names the descendants and their stop results (or
//	         incomplete stops).
//
// Oracles are the ADR rows only (D6 Q2=B, D6b, D7, D8.6, #1053, F0929-6/7).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// u1PersistSteered writes a steered lifecycle record directly (frontier
// fixtures need records, not launched sessions): running under parentID by
// default, or needs_input / stopped(+note) per the caller.
func u1PersistSteered(t *testing.T, al *AgentLoop, id, parentID string, state session.LifecycleState) {
	t.Helper()
	rec := &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws-u1-frontier", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		// Launch always writes the direct parent's reporting destination.
		// A bare edge without it cannot publish the D6 notice (D8.10).
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: parentID, RootSessionID: parentID,
			ReportingTarget: session.ReportingTarget{
				SessionID: parentID, Channel: "webchat", ChatID: parentID,
			},
		},
	}
	switch state {
	case session.LifecycleNeedsInput:
		rec.State = state
		rec.NeedsInput = &session.NeedsInput{
			CorrelationID:   "u1-frontier-q",
			Reconstructable: true,
			TTLDeadline:     time.Now().Add(time.Hour),
		}
	case session.LifecycleQueued:
		// queued is a valid resting record state (the dispatch-admission
		// shape the frontier's Queued branch exists for); validateLifecycle-
		// RecordForPersist imposes no extra field on it.
		rec.State = state
	}
	if state == session.LifecycleStopped {
		rec.State = state
		rec.StopNote = &session.StopNote{
			At: time.Now().UTC(), By: "human:dan",
			Seq: uint64(rec.Generation), Cause: session.StopCauseStop,
		}
	}
	persistLifecycle(t, al.GetSessionLifecycleStore(), rec)
}

// TestU1CompletionFrontierNeedsInputBlocksAndStoppedCuts pins D6 Q2=B on
// hasRunningOrQueuedDescendant — the exact frontier the parent's
// done-claim consults:
//
//   - a needs_input descendant BLOCKS (D6b: a child waiting for an answer
//     holds back its parent's done);
//   - a stopped descendant does NOT block and CUTS traversal: a running
//     descendant BEYOND the stopped node is invisible to the frontier
//     (D8.6: the stopped child's direct-parent notice — not a blocked
//     claim — is what reaches the parent);
//   - the cut is state-driven: when the same record is queued again (the
//     state an explicit same-generation RESUME lands, D2), the frontier
//     blocks again.
func TestU1CompletionFrontierNeedsInputBlocksAndStoppedCuts(t *testing.T) {
	t.Run("needs_input descendant blocks", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		root := newTestSteeringSession(t, al, "ws-u1-frontier-ni")
		u1PersistSteered(t, al, "sess-u1-ni-child", root, session.LifecycleNeedsInput)
		got, err := al.hasRunningOrQueuedDescendant(root)
		if err != nil {
			t.Fatalf("hasRunningOrQueuedDescendant: %v", err)
		}
		if !got {
			t.Errorf("frontier with a needs_input descendant = false, want true — " +
				"D6b: a child waiting for an answer holds back its parent's done")
		}
	})
	t.Run("stopped descendant cuts subtree traversal", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		root := newTestSteeringSession(t, al, "ws-u1-frontier-cut")
		u1PersistSteered(t, al, "sess-u1-cut-mid", root, session.LifecycleStopped)
		u1PersistSteered(t, al, "sess-u1-cut-leaf", "sess-u1-cut-mid", session.LifecycleQueued)
		got, err := al.hasRunningOrQueuedDescendant(root)
		if err != nil {
			t.Fatalf("hasRunningOrQueuedDescendant: %v", err)
		}
		if got {
			t.Errorf("frontier behind a stopped node = true, want false — D6 Q2=B/D8.6: " +
				"a stopped descendant does not block and its subtree (queued leaf here) " +
				"must be cut from the traversal; the parent decides from the D6 notice")
		}
	})
	t.Run("resume back to queued re-blocks", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		root := newTestSteeringSession(t, al, "ws-u1-frontier-resume")
		u1PersistSteered(t, al, "sess-u1-resume-mid", root, session.LifecycleStopped)
		u1PersistSteered(t, al, "sess-u1-resume-leaf", "sess-u1-resume-mid", session.LifecycleQueued)
		// Record-level stand-in for RESUME's landed state (D2: same-
		// generation queued); the frontier must be state-driven, not
		// id-driven.
		if err := al.GetSessionLifecycleStore().Mutate("sess-u1-resume-mid", func(r *session.LifecycleRecord) error {
			r.State = session.LifecycleQueued
			return nil
		}); err != nil {
			t.Fatalf("Mutate(mid -> queued): %v", err)
		}
		got, err := al.hasRunningOrQueuedDescendant(root)
		if err != nil {
			t.Fatalf("hasRunningOrQueuedDescendant: %v", err)
		}
		if !got {
			t.Errorf("frontier after the stopped node returned to queued = false, want true — " +
				"the cut must follow the record's state, not its identity")
		}
	})
}

// TestIssue1053_GenuineFailedParentCascadeStopsDescendants pins #1053: a
// genuine failure (not a stop) lands the parent failed promptly (#947's
// no-hang, preserved), cascade-stops its reachable ACTIVE descendants via
// the D7 machinery — each kept at stopped with its goal open and its own D6
// notice to its own direct parent — and the fatal hand-back to the
// grandparent NAMES the descendants and their stop results (or incomplete
// stops), so the chain's head can decide with the full picture.
func TestIssue1053_GenuineFailedParentCascadeStopsDescendants(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	root := newTestSteeringSession(t, al, "ws-u1-1053")
	mid, _ := r1AdmitChild(t, al, root, "u1-1053-mid", "held real admission before synthetic genuine failure")
	leaf := u1LaunchChild(t, al, mid.SessionID, "u1-1053-leaf")
	leafGoal := activateTestGoalRecord(t, leaf.SessionID, "1053 leaf goal stays open")
	wakes := observeU1ParentNoticeWakes(t, al, mid.SessionID)

	// Genuine failure of the middle node: an ordinary completion with an
	// error — no stop, no fence, no human.
	if err := al.completeSteeredTurn(context.Background(), mid,
		turnResult{finalContent: ""}, errors.New("boom")); err != nil {
		t.Fatalf("completeSteeredTurn(genuine failure): %v", err)
	}

	// (1) The failed parent landed PROMPTLY (the call above returned; #947's
	// no-hang is preserved — D6 #1053).
	midRec, err := al.GetSessionLifecycleStore().Load(mid.SessionID)
	if err != nil {
		t.Fatalf("Load(mid): %v", err)
	}
	if midRec.State != session.LifecycleFailed || !midRec.Terminal() {
		t.Fatalf("mid state after genuine failure = %q (terminal=%v), want failed — #1053: a genuine failure lands promptly", midRec.State, midRec.Terminal())
	}

	// (2) The reachable ACTIVE descendant is cascade-stopped via D7 —
	// stopped, non-terminal, with a cascade stop note.
	leafRec, err := al.GetSessionLifecycleStore().Load(leaf.SessionID)
	if err != nil {
		t.Fatalf("Load(leaf): %v", err)
	}
	if leafRec.State != session.LifecycleStopped || leafRec.Terminal() {
		t.Fatalf("leaf state after its parent's genuine failure = %q (terminal=%v), want stopped non-terminal — "+
			"#1053/D7: a genuine failure cascade-stops reachable active descendants", leafRec.State, leafRec.Terminal())
	}
	if leafRec.StopNote == nil || leafRec.StopNote.Cause != session.StopCauseCascade {
		t.Errorf("leaf stop note after the failure cascade = %+v, want cause %q (D2/D7)", leafRec.StopNote, session.StopCauseCascade)
	}

	// (3) F0929-6: the cascade never ends the leaf's session-owned goal.
	leafGoalRec, err := resolveGoalRecordStore().Get(leafGoal)
	if err != nil {
		t.Fatalf("Get(leaf goal): %v", err)
	}
	if leafGoalRec.State != generated.GoalStateActive {
		t.Errorf("leaf goal after the failure cascade = %q, want active — D6 Goal row: no stop of any kind ends a goal", leafGoalRec.State)
	}

	// (4) The leaf's DIRECT parent (mid) holds exactly one D6 stopped-child
	// notice for the leaf, woken once — the grandparent gets none.
	noticeID, _ := assertU1StoppedChildNotice(t, al, mid.SessionID, leaf, string(session.StopCauseCascade), "")
	if got := wakes(noticeID); got != 1 {
		t.Errorf("mid wakes for the leaf's stopped-child notice = %d, want exactly 1 (D6)", got)
	}
	assertU1NoStoppedNoticeFor(t, al, root, leaf.SessionID)

	// (5) The fatal hand-back to the grandparent NAMES the descendants and
	// their stop results (or incomplete stops) — the chain's head decides
	// with the full picture, not a bare "failed: boom".
	entries, err := al.GetMessageInboxStore().Entries(root)
	if err != nil {
		t.Fatalf("Entries(root): %v", err)
	}
	var sawFatalForMid bool
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		raw, merr := entry.Message.MarshalJSON()
		if merr != nil {
			t.Fatalf("MarshalJSON(root message): %v", merr)
		}
		fields := u1DecodeMessageFields(t, raw)
		if fields["session_id"] != mid.SessionID || fields["fatal"] != true {
			continue
		}
		sawFatalForMid = true
		// IDs are exact wire strings. u1NoticeBody lowercases and replaces
		// underscores for prose matching, so it cannot test a raw session ID.
		body, textOK := fields["text"].(string)
		if !textOK || !strings.Contains(body, leaf.SessionID) {
			t.Errorf("mid's fatal hand-back to the grandparent does not name the cascade-stopped descendant %s: %s — "+
				"#1053: the fatal hand-back names descendants and their stop results", leaf.SessionID, raw)
		}
	}
	if !sawFatalForMid {
		t.Errorf("no fatal hand-back for mid reached the grandparent's inbox (%d entries) — #1053 keeps the #947 upward report", len(entries))
	}
}
