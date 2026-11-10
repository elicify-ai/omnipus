// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_goal_terminal_mapper_test.go pins tools.GoalStateForTerminalTask —
// the shared mapper every task-status writer's disposition routes through
// (tools.TerminateTaskGoalRecord) — because the mapper is the POLICY POINT
// for MAJ-003/D8.10 (ADR-20260928-sub-agent-control-plane): it is what makes
// a user Stop goal-non-terminal for every writer at once, and what keeps the
// guard registry's single classified exemption (PlanEngine.cancelMemberLocked)
// safe. A mapper that starts mapping stopped_by_user again would clear goals
// behind every Stop without any writer changing.
//
// Oracles are the frozen ADR (D6 Goal row, MAJ-003; D8.10; T20), never the
// mapper's current output:
//   - a task's genuine adjudications end the paired record: done -> met,
//     failure/attempt-exhaustion -> exhausted;
//   - failed(stopped_by_user) — a user Stop — is NOT an adjudication: the
//     mapper must REFUSE it (no state, ok=false), so no writer can clear the
//     goal through the shared transition;
//   - a non-terminal (mid-flight) status is likewise refused.
package tools

import (
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestGoalStateForTerminalTask_ClassifiesAdjudicationsAndRefusesUserStop(t *testing.T) {
	cases := []struct {
		name         string
		status       task.Status
		cancelReason task.CancelReason
		wantState    generated.GoalState
		wantOK       bool
	}{
		{
			name:      "done_maps_to_met",
			status:    task.StatusDone,
			wantState: generated.GoalStateMet, wantOK: true,
		},
		{
			name:      "plain_failure_maps_to_exhausted",
			status:    task.StatusFailed,
			wantState: generated.GoalStateExhausted, wantOK: true,
		},
		{
			// THE MAJ-003 refusal: a user Stop is not an adjudication.
			name:         "user_stop_is_refused",
			status:       task.StatusFailed,
			cancelReason: task.CancelReasonStoppedByUser,
			wantState:    "", wantOK: false,
		},
		{
			name:      "midflight_in_progress_is_refused",
			status:    task.StatusInProgress,
			wantState: "", wantOK: false,
		},
		{
			name:      "not_started_inbox_is_refused",
			status:    task.StatusInbox,
			wantState: "", wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotState, gotOK := GoalStateForTerminalTask(tc.status, tc.cancelReason)
			if gotOK != tc.wantOK || gotState != tc.wantState {
				t.Fatalf("GoalStateForTerminalTask(%q, %q) = (%q, %v); want (%q, %v) — "+
					"ADR-20260928-sub-agent-control-plane D8.10/T20",
					tc.status, tc.cancelReason, gotState, gotOK, tc.wantState, tc.wantOK)
			}
		})
	}
}
