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
// user actually feels: a parent that has produced its own answer and is only
// waiting on a goal-bearing worker completes once that worker dies, instead
// of hanging until the gateway restarts.
func TestGoalDelegation_DeadChildUnblocksTheWaitingParent(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	waitingParent := launchRunningChild(t, al, rootID, "call-waiting-parent")
	if err := al.GetSessionStore().AppendTranscriptStrict(waitingParent.SessionID, session.TranscriptEntry{
		ID: "parent-answer", Role: "assistant", Content: "parent result", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict(parent answer): %v", err)
	}
	child := launchGoalBearingChild(t, al, waitingParent.SessionID, "call-goal-child")

	ts, err := al.reconstructSteeredTurn(child, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	result := turnResult{}
	al.finishSteeredGoalTurn(ts, child, &result, errors.New("downstream provider failure"))

	parent, loadErr := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if loadErr != nil {
		t.Fatalf("Load(waiting parent): %v", loadErr)
	}
	if parent.State != session.LifecycleCompleted {
		t.Fatalf("waiting parent state = %q, want completed — its only worker is dead and it has an answer", parent.State)
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
	if handback.ResultSoFar != "parent result" {
		t.Errorf("result_so_far = %q, want %q", handback.ResultSoFar, "parent result")
	}
}
