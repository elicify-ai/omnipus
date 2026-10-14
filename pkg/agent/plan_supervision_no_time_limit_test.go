package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestSupervisionDeadline_NeverFailsThePlanWhileTheSupervisionTurnIsInFlight
// pins issue #1306 for plans: the FR-021 observation deadline must not treat a
// supervision turn that is still working as having "produced nothing". At the
// attempt ceiling the deadline used to terminate the plan
// failed(supervision_unavailable) even while the last supervision turn was
// still running; now it waits for that turn to settle, and only then applies
// (the post-settle grace).
func TestSupervisionDeadline_NeverFailsThePlanWhileTheSupervisionTurnIsInFlight(t *testing.T) {
	h := newTestPlanEngine(t)
	ctx := context.Background()
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	outside := mustCreateTask(t, h.tasks, &task.Task{Title: "outside blocker", WorkspaceID: "ws", Status: task.StatusInbox})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "stuck member", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusBlocked, BlockedBy: []string{outside.ID},
	})

	h.pe.processPlan(ctx, "p1")
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get p1: %v", err)
	}
	if got.Supervision == nil || got.Supervision.Attempts != 1 {
		t.Fatalf("SETUP: the stall wake must arm the deadline as attempt 1, got %+v", got.Supervision)
	}
	// The last allowed attempt is the one in flight.
	maxAttempts := h.pe.supervisionMaxAttempts(got)
	if _, uerr := h.plans.Update("p1", plan.Patch{SupervisionAttempts: &maxAttempts}); uerr != nil {
		t.Fatalf("SETUP: put the plan at its attempt ceiling: %v", uerr)
	}
	unit := supervisionUnitForPlan("p1")
	if regErr := h.pe.registry().Register(unit, "live-supervision-turn"); regErr != nil {
		t.Fatalf("SETUP: hold the supervision claim: %v", regErr)
	}

	h.clock.Set(h.clock.Now().Add(10 * defaultSupervisionTurnTimeout))
	h.pe.processPlan(ctx, "p1")
	if got, _ = h.plans.Get("p1"); got.State == plan.StateFailed {
		t.Fatalf("plan failed (%q) while its supervision turn was still working — the deadline cut off a running review (issue #1306)",
			got.FailedReason)
	}

	// The turn settles without producing anything: now the deadline applies.
	h.pe.registry().Unregister(unit)
	h.pe.processPlan(ctx, "p1")
	if got, _ = h.plans.Get("p1"); got.State != plan.StateFailed || got.FailedReason != plan.FailedReasonSupervisionUnavailable {
		t.Fatalf("plan state = %q (%q) after the turn settled past the deadline at the ceiling, want failed(%s)",
			got.State, got.FailedReason, plan.FailedReasonSupervisionUnavailable)
	}
}
