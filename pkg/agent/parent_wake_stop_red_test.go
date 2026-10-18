// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Tests 10, 11 and 13.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md
//   - PW-D3, PW-D13, §9.6
//   - FR-008, FR-009, FR-010
//   - BDD "User Stop of the helper writes a row and starts nothing"
//   - BDD "Restart wakes an idle parent and shows a row"
//   - BDD "A historical timeout wakes and does not resume"
//
// Cause is the landed transition, not the stopped_child: text prefix.
// An intentional Stop writes one stopped row and wakes nobody, including a
// second delivery of the same notice. A restart or historical timeout of an
// idle (completed) parent wakes once and writes one row. A timeout does not
// resume the helper.
//
// Step 4 items 2–3 are deferred to CHECK.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestStopNotice_IntentionalStopNoWake(t *testing.T) {
	for _, actor := range []string{"human:owner", "agent:parent"} {
		t.Run(actor, func(t *testing.T) {
			al, lifecycle, _, _ := newPWDeliverTestLoop(t)
			const parentID, childID = "parent-pw-userstop", "child-pw-userstop"
			pwSeedDeliverySessions(t, al, parentID, childID)
			pwLandFencelessStop(t, lifecycle, childID, parentID, session.StopCauseStop, actor)

			wakes := w1hObserveWakes(t, al)
			rec, err := lifecycle.Load(childID)
			if err != nil {
				t.Fatalf("Load child: %v", err)
			}
			if _, err = al.deliverLandedStopNotices(context.Background(), rec); err != nil {
				t.Fatalf("deliverLandedStopNotices: %v", err)
			}
			if _, err = al.deliverLandedStopNotices(context.Background(), rec); err != nil {
				t.Fatalf("boot-replay deliverLandedStopNotices: %v", err)
			}
			trs, err := lifecycle.ListStoppedTransitions(childID)
			if err != nil || len(trs) != 1 {
				t.Fatalf("landed history = %d (err %v), want 1", len(trs), err)
			}
			id := stoppedChildNoticeID(parentID, childID, trs[0].Generation, trs[0].StopSeq)
			if got := wakes.count(id); got != 0 {
				t.Fatalf("intentional Stop by %s woke the parent %d time(s), want 0 on first delivery and on replay (PW-D13 / FR-010)", actor, got)
			}
			rows := pwRowsContaining(t, al, parentID, "helper_event")
			if len(rows) != 1 {
				t.Fatalf("parent helper_event rows = %d, want 1 stopped row (FR-008 / §9.3)", len(rows))
			}
			if !strings.Contains(strings.ToLower(rows[0].Content), "not resume") && !strings.Contains(strings.ToLower(rows[0].Content), "will not resume") {
				t.Fatalf("stopped row = %q, want a sentence that the helper will not resume on its own (§9.3)", rows[0].Content)
			}
			after, err := lifecycle.Load(childID)
			if err != nil {
				t.Fatalf("Load after: %v", err)
			}
			if after.State == session.LifecycleRunning || after.State == session.LifecycleQueued {
				t.Fatalf("helper state = %q after an intentional Stop, want it not resumed (PW-D13)", after.State)
			}
		})
	}
}

func TestStopNotice_RestartWakesIdleParent(t *testing.T) {
	al, lifecycle, _, _ := newPWDeliverTestLoop(t)
	const parentID, childID = "parent-pw-restart", "child-pw-restart"
	pwSeedDeliverySessions(t, al, parentID, childID)
	if err := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleCompleted
		rec.Stop = nil
		return nil
	}); err != nil {
		t.Fatalf("mark parent idle: %v", err)
	}
	pwLandFencelessStop(t, lifecycle, childID, parentID, session.StopCauseRestart, session.StopActorRestart)

	wakes := w1hObserveWakes(t, al)
	rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load child: %v", err)
	}
	if _, err = al.deliverLandedStopNotices(context.Background(), rec); err != nil {
		t.Fatalf("deliverLandedStopNotices: %v", err)
	}
	trs, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil || len(trs) != 1 {
		t.Fatalf("landed history = %d (err %v), want 1", len(trs), err)
	}
	id := stoppedChildNoticeID(parentID, childID, trs[0].Generation, trs[0].StopSeq)
	if got := wakes.count(id); got != 1 {
		t.Fatalf("restart notice woke the idle parent %d time(s), want 1 (PW-D3 / FR-009 / Test 11)", got)
	}
	rows := pwRowsContaining(t, al, parentID, "helper_event")
	if len(rows) != 1 {
		t.Fatalf("parent restart rows = %d, want 1 (FR-008)", len(rows))
	}
	if !strings.Contains(rows[0].Content, "Interrupted by a restart.") {
		t.Fatalf("restart row = %q, want the immutable sentence \"Interrupted by a restart.\" (§9.7 / FR-015)", rows[0].Content)
	}
}

func TestStopNotice_HistoricalTimeoutWakesNoResume(t *testing.T) {
	al, lifecycle, _, _ := newPWDeliverTestLoop(t)
	const parentID, childID = "parent-pw-timeout", "child-pw-timeout"
	pwSeedDeliverySessions(t, al, parentID, childID)
	if err := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleCompleted
		return nil
	}); err != nil {
		t.Fatalf("mark parent idle: %v", err)
	}
	pwLandFencelessStop(t, lifecycle, childID, parentID, session.StopCauseTimeout, "budget")

	wakes := w1hObserveWakes(t, al)
	rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load child: %v", err)
	}
	if _, err = al.deliverLandedStopNotices(context.Background(), rec); err != nil {
		t.Fatalf("deliverLandedStopNotices: %v", err)
	}
	trs, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil || len(trs) != 1 {
		t.Fatalf("landed history = %d (err %v), want 1", len(trs), err)
	}
	id := stoppedChildNoticeID(parentID, childID, trs[0].Generation, trs[0].StopSeq)
	if got := wakes.count(id); got != 1 {
		t.Fatalf("historical timeout woke the idle parent %d time(s), want 1 (FR-009 / Test 13)", got)
	}
	after, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load after: %v", err)
	}
	if after.State == session.LifecycleRunning || after.State == session.LifecycleQueued {
		t.Fatalf("timed-out helper state = %q, want it not resumed (FR-009)", after.State)
	}
	rows := pwRowsContaining(t, al, parentID, "helper_event")
	if len(rows) != 1 {
		t.Fatalf("parent timeout rows = %d, want 1 (FR-008)", len(rows))
	}
}

func pwLandFencelessStop(t *testing.T, lifecycle *session.LifecycleStore, childID, parentID string, cause session.StopCause, actor string) {
	t.Helper()
	err := lifecycle.Mutate(childID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleStopped
		note := &session.StopNote{
			At: time.Now().UTC(), By: actor, Cause: cause, Seq: 1,
		}
		if cause == session.StopCauseRestart {
			note.BootSeq = 1
		}
		rec.StopNote = note
		_, landErr := lifecycle.RecordFencelessLandedStopLocked(childID, session.LandedStop{
			ParentSessionID: parentID,
			Generation:      rec.Generation,
			Cause:           cause,
			Actor:           actor,
			At:              time.Now().UTC(),
		})
		return landErr
	})
	if err != nil {
		t.Fatalf("land fence-less %s stop: %v", cause, err)
	}
}

func pwRowsContaining(t *testing.T, al *AgentLoop, sessionID, subtype string) []session.TranscriptEntry {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.SystemSubtype == subtype {
			out = append(out, e)
		}
	}
	return out
}
