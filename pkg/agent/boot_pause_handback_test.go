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
	if len(notices) != 0 {
		t.Fatalf("unexpected operator notices: %v", notices)
	}

	// Inbox: the pause handback under its own id, and the interrupted notice
	// under the deterministic final id — both present, neither swallowed.
	msgs, _, _, err := inbox.Drain(parentID, childID, "", 20)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	finalID := childID + ":1:final"
	var pauseEntries, interruptedFinal int
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
			if e.MessageId == finalID && strings.HasPrefix(e.Text, "interrupted:") {
				interruptedFinal++
			}
		}
	}
	if pauseEntries != 1 || interruptedFinal != 1 {
		t.Fatalf("inbox: pause handbacks=%d interrupted-final=%d (want 1 and 1) of %d entries", pauseEntries, interruptedFinal, len(msgs))
	}

	// Side panel: never "completed"; exactly one end frame — the
	// interruption's, not a second one from a pause misread as final.
	entries, rerr := al.GetSessionStore().ReadTranscript(parentID)
	if rerr != nil {
		t.Fatalf("ReadTranscript(parent): %v", rerr)
	}
	var ends int
	var states []string
	for i := range entries {
		switch {
		case entries[i].SystemSubtype == session.SystemSubtypeSubagentEnd:
			ends++
		case entries[i].SystemSubtype == session.SystemSubtypeSubagentState && entries[i].SubagentState != nil:
			states = append(states, entries[i].SubagentState.State)
		}
	}
	for _, st := range states {
		if st == string(session.LifecycleCompleted) {
			t.Fatalf("side panel marked the paused child completed; states = %v", states)
		}
	}
	if ends != 1 {
		t.Fatalf("subagent_end frames = %d, want exactly 1 (the interruption's); states = %v", ends, states)
	}

	rec, lerr := lifecycle.Load(childID)
	if lerr != nil {
		t.Fatalf("Load child: %v", lerr)
	}
	if rec.State != session.LifecycleFailed || rec.FailedReason != failedReasonInterrupted {
		t.Fatalf("child after boot = state %q reason %q, want failed/%s", rec.State, rec.FailedReason, failedReasonInterrupted)
	}
}
