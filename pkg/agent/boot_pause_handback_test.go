// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Issue #1011 D1, review round 3: once a pause handback can be delivered,
// boot recovery must not re-deliver an unconsumed one as a FINAL answer.
// Doing so stamped it `<child>:<gen>:final`, marked the merely-paused child
// completed in the side panel, and made the interrupted notice that follows
// (same deterministic id) a silent duplicate — the interruption was lost.
//
// A pause handback does not park the child (only a waiting question does,
// message_parent.go::parkNeedsInput), so a child that paused and then lost
// its gateway is still `running` at boot: the running branch of
// recoverSteered, which re-nudges unconsumed entries and then reports the
// interruption. This drives that branch into the REAL SteerUpwardDeliverer,
// where the final-id stamping and dedupe live.

package agent

import (
	"context"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestBoot_PauseHandbackRedeliveredAsBlockerNotFinal pins the founder
// contract of 2026-10-04 (ADR-20260928 Correction C5/D8) for a run THIS boot
// just stopped: a child mid-flight (running) at boot with an unconsumed
// pause handback in its parent's inbox is stopped by the boot itself
// (C5 pass one: the ledgered restart stop), and
//
//   - the record lands LifecycleStopped — a non-terminal stop carrying the
//     restart stop note (cause restart, actor system), NEVER
//     failed(interrupted): the restart stop + its stop notice already told
//     the parent what happened;
//   - the parent receives the D6 stop NOTICE (fatal=false,
//     stopped_child:/cause: restart) — not a second, contradictory
//     "interrupted: gateway restarted while session was running" fatal under
//     the deterministic final id;
//   - the pause handback survives under its own id, never re-stamped with
//     the final id (issue #1011 D1, this test's namesake) and never
//     swallowed.
//
// The old oracle (failed(interrupted) + the interrupted fatal) asserted the
// pre-C5 verdict on a run the restart itself accounts for; it was wrong, not
// the code that stopped sending it.
func TestBoot_PauseHandbackRedeliveredAsBlockerNotFinal(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID) // child state: running
	seedUnifiedSession(t, al, parentID)

	// The child's pause handback reached the parent's inbox before the
	// restart and was never consumed.
	if _, err := inbox.Append(parentID, pauseHandbackMessage(t, childID, "pause-1")); err != nil {
		t.Fatalf("Append pause handback: %v", err)
	}

	var notices []string
	recovery := &SteerBootRecovery{
		Lifecycle:      lifecycle,
		Sessions:       al.GetSessionStore(),
		Inbox:          inbox,
		Classifier:     NewSteerRecordClassifier(lifecycle, al.GetSessionStore()),
		Deliverer:      deliverer,
		OperatorNotice: func(message string) { notices = append(notices, message) },
	}
	recovery.recoverSteered(context.Background(), childID, func(_, message string) { notices = append(notices, message) })

	// --- The record: stopped, not failed; the restart stop note ---
	rec, lerr := lifecycle.Load(childID)
	if lerr != nil {
		t.Fatalf("Load child: %v", lerr)
	}
	if rec.State != session.LifecycleStopped {
		t.Fatalf("child after boot = state %q, want %q — a run this boot just stopped lands an ordinary, non-terminal stop, never failed(interrupted)", rec.State, session.LifecycleStopped)
	}
	if rec.FailedReason != "" {
		t.Errorf("child failed_reason = %q, want empty — the restart interrupted a live run, it did not fail it", rec.FailedReason)
	}
	if rec.StopNote == nil {
		t.Fatal("child carries no stop note — the boot stop must land the restart stop note")
	}
	if rec.StopNote.Cause != session.StopCauseRestart || rec.StopNote.By != session.StopActorSystem {
		t.Errorf("child stop note = (cause %q, by %q), want (%q, %q) — the ledgered restart stop, actor system",
			rec.StopNote.Cause, rec.StopNote.By, session.StopCauseRestart, session.StopActorSystem)
	}

	// --- The parent's inbox: stop notice, not an interrupted fatal ---
	msgs, _, _, err := inbox.Drain(parentID, childID, "", 20)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	finalID := childID + ":1:final"
	noticeIDPrefix := "stopped-notice:" + parentID + ":" + childID + ":1:"
	var pauseEntries, interruptedFatal, stopNotices int
	for _, m := range msgs {
		kind, _ := m.Discriminator()
		switch kind {
		case "handback":
			hb, herr := m.AsSessionMessageHandback()
			if herr != nil {
				t.Fatalf("AsSessionMessageHandback: %v", herr)
			}
			if hb.MessageId == finalID {
				t.Fatalf("pause handback was re-stamped with the final id %q", finalID)
			}
			if hb.Mode == generated.SessionMessageHandbackModePause && hb.MessageId == "pause-1" {
				pauseEntries++
			}
		case "error":
			e, eerr := m.AsSessionMessageError()
			if eerr != nil {
				t.Fatalf("AsSessionMessageError: %v", eerr)
			}
			if e.MessageId == finalID || strings.HasPrefix(e.Text, "interrupted:") {
				// The retired second verdict: a fatal "interrupted: gateway
				// restarted…" on a run the boot already stopped and noticed.
				interruptedFatal++
				continue
			}
			if strings.HasPrefix(e.MessageId, noticeIDPrefix) {
				stopNotices++
				if e.Fatal {
					t.Errorf("stopped-child notice %q is fatal=true, want false — it is a notice, not a terminal verdict", e.MessageId)
				}
				if !strings.HasPrefix(e.Text, session.LifecycleNoticePrefixStoppedChild) {
					t.Errorf("stopped-child notice text = %q, want the %q prefix", e.Text, session.LifecycleNoticePrefixStoppedChild)
				}
				if !strings.Contains(e.Text, "cause: "+string(session.StopCauseRestart)) {
					t.Errorf("stopped-child notice text = %q, want it to name the restart cause", e.Text)
				}
				if !strings.Contains(e.Text, "actor: "+session.StopActorSystem) {
					t.Errorf("stopped-child notice text = %q, want it to name the system actor", e.Text)
				}
			}
		}
	}
	if pauseEntries != 1 {
		t.Errorf("inbox: pause handbacks=%d (want 1, under its own id %q) of %d entries", pauseEntries, "pause-1", len(msgs))
	}
	if interruptedFatal != 0 {
		t.Errorf("inbox: %d interrupted-fatal entries — the parent must get the stop notice only, never a second \"interrupted: gateway restarted\" verdict on a run the boot itself stopped", interruptedFatal)
	}
	if stopNotices != 1 {
		t.Errorf("inbox: stopped-child notices=%d (want 1, id prefix %q) of %d entries — the restart stop's direct-parent notice", stopNotices, noticeIDPrefix, len(msgs))
	}

	// --- Side panel: a stopped helper is never read as completed ---
	entries, rerr := al.GetSessionStore().ReadTranscript(parentID)
	if rerr != nil {
		t.Fatalf("ReadTranscript(parent): %v", rerr)
	}
	var states []string
	for i := range entries {
		if entries[i].SystemSubtype == session.SystemSubtypeSubagentState && entries[i].SubagentState != nil {
			states = append(states, entries[i].SubagentState.State)
		}
	}
	for _, st := range states {
		if st == string(session.LifecycleCompleted) {
			t.Fatalf("side panel marked the stopped child completed; states = %v", states)
		}
	}
}
