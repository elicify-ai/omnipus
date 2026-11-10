package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestGoalQ2B_RejectRunningChildren_NeedsInputWithoutLiveTurn(t *testing.T) {
	h := newQ2BHarness(t, "q2b-needs-input-owner")
	child := q2bLaunchDescendant(t, h, "q2b-needs-input-direct", session.LifecycleQueued)
	if err := h.lifecycle.Mutate(child.SessionID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleNeedsInput
		rec.NeedsInput = &session.NeedsInput{CorrelationID: "q2b-needs-input-question"}
		return nil
	}); err != nil {
		t.Fatalf("park direct child awaiting input: %v", err)
	}

	children, err := h.lifecycle.List(session.LifecycleFilter{SteeringSessionID: h.child.SessionID})
	if err != nil || len(children) != 1 || children[0].SessionID != child.SessionID || children[0].State != session.LifecycleNeedsInput {
		t.Fatalf("fixture direct children = %+v, err=%v, want one needs_input child %q", children, err, child.SessionID)
	}
	if turn := h.al.getActiveTurnState(child.SessionID); turn != nil {
		t.Fatalf("fixture: parked child has an active turn: %+v", turn)
	}
	if h.al.steeredCompletionWriteActive(child.SessionID) {
		t.Fatal("fixture: parked child has an active completion write")
	}

	want := "1 sub-agents are still running: " + child.SessionID + ". Wait for them to finish or cancel them, then claim again"
	q2bAssertRejectedClaim(t, h, "worker is waiting for an answer", want)
}
