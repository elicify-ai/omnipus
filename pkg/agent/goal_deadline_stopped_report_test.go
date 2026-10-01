// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Investigation-only reproduction for the finding dispatched to
// coordination/logs/fix890-opus/goal-deadline-repro: does
// steer_completion.go::finishSteeredGoalTurn silently drop a goal-bearing
// child that ends via deadline or cancellation and resolves to the
// non-terminal `stopped` lifecycle state?
//
// completionDisposition (steer_completion.go) maps both
// errors.Is(runErr, context.DeadlineExceeded) and
// errors.Is(runErr, context.Canceled) to session.LifecycleStopped — which is
// NOT one of the two states session.IsTerminalLifecycleState recognizes
// (only Completed and Failed are terminal; pkg/session/lifecycle.go's
// terminalLifecycleStates map). finishSteeredGoalTurn's dead-turn guard
// reads:
//
//	if _, nextState, _ := completionDisposition(...); session.IsTerminalLifecycleState(nextState) {
//	    completeSteeredTurn(...)   // writes state + delivers upward
//	    return
//	}
//	if runErr != nil {
//	    return                    // <-- deadline/cancel land here, silently
//	}
//
// For a failed turn (nextState=LifecycleFailed) the first branch fires and
// the child is reported upward correctly. For a timed-out or cancelled
// goal-bearing turn (nextState=LifecycleStopped) the first branch never
// fires, and runErr is always non-nil for these two outcomes, so the second
// guard returns immediately: completeSteeredTurn is never called, the
// child's lifecycle record is never written to `stopped`, and the parent's
// inbox never receives any report for this generation.
//
// This mirrors the now-uncompilable pkg/agent/steer_goal_failure_test.go
// (blocked package-wide by two unrelated files' retired lifecycle
// constants, per this dispatch's brief) — that file's own doc comment
// already names this exact defect as a historical regression
// ("finishSteeredGoalTurn returned on the first line for any run error").
// That file asserts against session.LifecycleTimedOut/LifecycleCancelled,
// which pkg/session/lifecycle.go's six-state model retired in favor of one
// shared non-terminal session.LifecycleStopped — this file uses only the
// six current states so it compiles and runs independently of that
// blocker.
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestGoalDeadlineRepro_StoppedGoalChildReportsUpward(t *testing.T) {
	tests := []struct {
		name   string
		runErr error
		result turnResult
	}{
		{
			name:   "goal child times out (context.DeadlineExceeded)",
			runErr: context.DeadlineExceeded,
		},
		{
			name:   "goal child is cancelled (context.Canceled)",
			runErr: context.Canceled,
			result: turnResult{status: TurnEndStatusAborted},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			lifecycleDir, inboxDir := t.TempDir(), t.TempDir()
			lifecycle := session.NewLifecycleStore(lifecycleDir)
			inbox := session.NewMessageInboxStore(inboxDir)
			al.SetSessionMessagingStores(inbox, lifecycle)
			wireSteerCompletionDeps(t, al)

			parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
			if err != nil {
				t.Fatalf("NewSession(parent): %v", err)
			}
			rec := launchGoalBearingChild947(t, al, parentMeta.ID, "call-goal-deadline-repro")

			ts, err := al.reconstructSteeredTurn(rec, nil)
			if err != nil {
				t.Fatalf("reconstructSteeredTurn: %v", err)
			}

			// Sanity: completionDisposition itself agrees this outcome is
			// non-terminal `stopped`, not one of the two terminal states —
			// otherwise this test would not be exercising the gap at all.
			_, nextState, _ := completionDisposition(tc.result, tc.runErr, "")
			if nextState != session.LifecycleStopped {
				t.Fatalf("completionDisposition(%v) = state %q, want %q — this fixture no longer exercises the non-terminal path",
					tc.runErr, nextState, session.LifecycleStopped)
			}
			if session.IsTerminalLifecycleState(nextState) {
				t.Fatalf("session.IsTerminalLifecycleState(%q) = true, want false (LifecycleStopped is explicitly non-terminal per pkg/session/lifecycle.go)", nextState)
			}

			result := tc.result
			al.finishSteeredGoalTurn(ts, rec, &result, tc.runErr)

			// The child's lifecycle record must reflect what actually
			// happened instead of staying `running` for ever.
			got, loadErr := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if loadErr != nil {
				t.Fatalf("Load(child after the dead goal turn): %v", loadErr)
			}
			if got.State != session.LifecycleStopped {
				t.Errorf("child lifecycle state after %v = %q, want %q — finishSteeredGoalTurn's terminal-only guard left a dead goal-bearing child stuck at %q for ever",
					tc.runErr, got.State, session.LifecycleStopped, got.State)
			}

			// The parent must learn the child died/stopped — some message
			// for this child's generation, not silence.
			msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(parentMeta.ID, rec.SessionID, "", 10)
			if drainErr != nil {
				t.Fatalf("Drain(parent): %v", drainErr)
			}
			if len(msgs) == 0 {
				t.Errorf("parent inbox messages for child %q = 0, want at least 1 — finishSteeredGoalTurn's early return on a non-nil runErr for a non-terminal disposition never calls completeSteeredTurn, so the parent never receives the report", rec.SessionID)
			}

			// Consequence: a parent only waiting on this child never learns
			// it can stop waiting either.
			blocked, blockErr := al.hasRunningOrQueuedDescendant(parentMeta.ID)
			if blockErr != nil {
				t.Fatalf("hasRunningOrQueuedDescendant: %v", blockErr)
			}
			t.Logf("hasRunningOrQueuedDescendant(parent) after the dead goal child = %v (child lifecycle state %q); the live-turn check alone can mask the orphaned-record symptom even when it returns false — the record-state and inbox assertions above are this test's real oracle", blocked, got.State)
		})
	}
}
