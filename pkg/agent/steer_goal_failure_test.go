// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-5 — the outcome table applies to EVERY steered
// session, including one launched with acceptance criteria
// (delegate(goal=…)). A goal decides what "done" means; it does not decide
// what "died" means.
//
// Regression for the defect these tests' absence hid:
// steer_completion.go::finishSteeredGoalTurn returned on the first line for
// any run error, so a goal-bearing child that failed, ran out of time or was
// stopped delivered NOTHING upward and its record stayed `running` for ever
// — the parent waited on a child that was already dead, and
// hasRunningOrQueuedDescendant kept the parent from ever completing either.

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// launchGoalBearingChild launches a running child under parentID carrying
// acceptance criteria, and returns its record.
func launchGoalBearingChild(t *testing.T, al *AgentLoop, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "prove the goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch(goal child): %v", err)
	}
	lifecycle := al.GetSessionLifecycleStore()
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(goal child): %v", err)
	}
	if rec.GoalRef == "" {
		t.Fatalf("the launched child carries no goal reference; this fixture proves nothing")
	}
	rec.State = session.LifecycleRunning
	if err := lifecycle.Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}
	return rec
}

// TestGoalDelegation_DeadChildReportsUpwardAndLandsTerminal walks I-5's three
// failing outcomes for a goal-bearing child: a provider failure, the lifetime
// deadline, and a Stop. Each must reach the parent's inbox as a fatal error
// and leave the child's record in the matching terminal state, so the parent
// is no longer blocked by a descendant that is not running.
func TestGoalDelegation_DeadChildReportsUpwardAndLandsTerminal(t *testing.T) {
	tests := []struct {
		name      string
		runErr    error
		result    turnResult
		wantState session.LifecycleState
		wantText  string
	}{
		{
			name:      "a failing goal child reports the failure upward",
			runErr:    errors.New("downstream provider failure"),
			wantState: session.LifecycleFailed,
			wantText:  "failed:",
		},
		{
			name:      "a goal child that runs out of time reports the timeout upward",
			runErr:    context.DeadlineExceeded,
			wantState: session.LifecycleTimedOut,
			wantText:  "timed_out:",
		},
		{
			name:      "a stopped goal child reports the interruption upward",
			runErr:    context.Canceled,
			result:    turnResult{status: TurnEndStatusAborted},
			wantState: session.LifecycleCancelled,
			wantText:  "interrupted:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			wireSteerCompletionDeps(t, al)
			parentID := newTestSteeringSession(t, al, "ws-1")
			rec := launchGoalBearingChild(t, al, parentID, "call-goal-dead")

			ts, err := al.reconstructSteeredTurn(rec, nil)
			if err != nil {
				t.Fatalf("reconstructSteeredTurn: %v", err)
			}
			result := tc.result
			al.finishSteeredGoalTurn(ts, rec, &result, tc.runErr)

			msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(parentID, rec.SessionID, "", 10)
			if drainErr != nil {
				t.Fatalf("Drain(parent): %v", drainErr)
			}
			if len(msgs) != 1 {
				t.Fatalf("parent messages = %d, want exactly 1 — a goal child that died told its parent nothing", len(msgs))
			}
			kind, kindErr := msgs[0].Discriminator()
			if kindErr != nil || kind != "error" {
				t.Fatalf("message kind = %q (%v), want error", kind, kindErr)
			}
			e, asErr := msgs[0].AsSessionMessageError()
			if asErr != nil {
				t.Fatalf("AsSessionMessageError: %v", asErr)
			}
			if !e.Fatal {
				t.Errorf("error fatal = false, want true (I-5: every failing outcome is fatal)")
			}
			if !strings.Contains(e.Text, tc.wantText) {
				t.Errorf("error text = %q, want it to contain %q", e.Text, tc.wantText)
			}

			got, loadErr := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if loadErr != nil {
				t.Fatalf("Load(child after the failed turn): %v", loadErr)
			}
			if got.State != tc.wantState {
				t.Fatalf("child state = %q, want %q — a dead child left `running` blocks its parent for ever", got.State, tc.wantState)
			}
			blocked, blockErr := al.hasRunningOrQueuedDescendant(parentID)
			if blockErr != nil {
				t.Fatalf("hasRunningOrQueuedDescendant: %v", blockErr)
			}
			if blocked {
				t.Errorf("the parent still has a running or queued descendant after its only child died")
			}
		})
	}
}

// TestGoalDelegation_DeadChildUnblocksTheWaitingParent is the consequence the
// user actually feels: a parent that is only waiting on a goal-bearing
// worker completes once that worker dies, instead of hanging until the
// gateway restarts.
//
// Updated for ADR-091 fix lane 1, Finding A (the release blocker): this test
// used to pin the DELETED completeWaitingAncestors shortcut — it seeded a
// stale pre-existing transcript entry on waitingParent ("parent result") and
// asserted that dying child's completion synchronously, inline, copied that
// stale text up to the root, with no wake ever consumed. That shortcut is
// exactly the bug landing order I-5 calls out ("the parent's handback is
// written by the last such child's completion wake RE-ENTERING the
// parent") — completeWaitingAncestors read the parent's OWN last answer
// instead of ever re-entering it. The user-facing guarantee this test
// protects (a parent with no other work left is not stuck waiting on a dead
// worker forever) still holds — through the real mechanism: Deliver's wake
// reaches waitingParent, and processSteeredSystemWake (Finding B) re-enters
// it for a genuine turn that reaches its own real completion.
func TestGoalDelegation_DeadChildUnblocksTheWaitingParent(t *testing.T) {
	al, cleanup := newSteerALWithProvider(t, &depthEchoProvider{})
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	waitingParent := launchRunningChild(t, al, rootID, "call-waiting-parent")
	child := launchGoalBearingChild(t, al, waitingParent.SessionID, "call-goal-child")

	ts, err := al.reconstructSteeredTurn(child, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	result := turnResult{}
	al.finishSteeredGoalTurn(ts, child, &result, errors.New("downstream provider failure"))

	// waitingParent must NOT be silently completed by any shortcut — it
	// stays running until the wake below genuinely re-enters it (proves
	// completeWaitingAncestors is really gone, not just unreachable here by
	// coincidence).
	before, loadErr := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if loadErr != nil {
		t.Fatalf("Load(waitingParent) before its wake: %v", loadErr)
	}
	if before.State != session.LifecycleRunning {
		t.Fatalf("waitingParent state before its own wake ran = %q, want running (nothing may complete it early)", before.State)
	}

	var wake bus.InboundMessage
	select {
	case wake = <-al.bus.InboundChan():
	case <-time.After(5 * time.Second):
		t.Fatal("the dead child's report never woke waitingParent — Finding A's ReportingTarget fix did not take effect")
	}
	if _, err := al.processSystemMessage(context.Background(), wake); err != nil {
		t.Fatalf("processSystemMessage(wake waitingParent): %v", err)
	}

	parent, loadErr := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if loadErr != nil {
		t.Fatalf("Load(waiting parent): %v", loadErr)
	}
	if parent.State != session.LifecycleCompleted {
		t.Fatalf("waiting parent state = %q, want completed — its only worker is dead and it was genuinely re-entered", parent.State)
	}
	msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(rootID, waitingParent.SessionID, "", 10)
	if drainErr != nil {
		t.Fatalf("Drain(root): %v", drainErr)
	}
	if len(msgs) != 1 {
		t.Fatalf("root messages = %d, want 1 handback from the parent", len(msgs))
	}
	handback, hErr := msgs[0].AsSessionMessageHandback()
	if hErr != nil {
		t.Fatalf("AsSessionMessageHandback: %v", hErr)
	}
	// depthEchoProvider makes waitingParent's real final answer exactly the
	// wake content it was re-entered with, which reports the dead child's
	// own real failure — never a value this test invented.
	if !strings.Contains(handback.ResultSoFar, "failed:") {
		t.Errorf("result_so_far = %q, want it to reflect the real re-entry's report about the dead worker", handback.ResultSoFar)
	}
}
