package agent

import (
	"context"
	"fmt"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Coverage-restoration brief, Finding 2: the registered goal_claim(met) stays
// refused while a direct child's real terminal write is pending, even without
// a registered live turn. D2 CRIT-001 supersedes the old delivery-first oracle:
// "A producer must not call the upward deliverer before this commit."
// Assert no pre-commit publication; retain the exact handback oracle after it.
func TestGoalClaim_MetRefusedDuringDirectChildCompletionWrite(t *testing.T) {
	h := newQ2BHarness(t, "claim-completion-write-owner")
	child := stampG5ExitedExecution(t, h.al, q2bLaunchDescendant(t, h, "claim-completion-write-child", session.LifecycleRunning))
	if ts := h.al.getActiveTurnState(child.SessionID); ts != nil {
		t.Fatal("fixture: direct child has a registered turn; this case requires completion-write liveness alone")
	}
	if h.al.steeredCompletionWriteActive(child.SessionID) {
		t.Fatal("fixture: completion write was already active before the real completion started")
	}
	if got := h.goalRecord().State; got != generated.GoalStateActive {
		t.Fatalf("fixture: parent goal state = %q, want active", got)
	}

	const answer = "the direct child's delivered report"
	boundaryCalls := 0
	oldHook := completeStateWriteTestHook
	completeStateWriteTestHook = func(sessionID string) {
		if sessionID != child.SessionID {
			return
		}
		boundaryCalls++
		current, err := h.lifecycle.Load(child.SessionID)
		if err != nil {
			t.Fatalf("Load(child before terminal write): %v", err)
		}
		if current.State != session.LifecycleRunning {
			t.Fatalf("child at pre-commit boundary = %q, want running", current.State)
		}
		if current.FinalDelivery != nil {
			t.Fatalf("child has an outbox before its atomic terminal commit: %+v", current.FinalDelivery)
		}
		if ts := h.al.getActiveTurnState(child.SessionID); ts != nil {
			t.Fatal("child acquired a registered turn during completion; the isolated completion-write case was not exercised")
		}
		if !h.al.steeredCompletionWriteActive(child.SessionID) {
			t.Fatal("real completion did not mark the pending terminal write as active")
		}
		messages, _, _, err := h.inbox.Drain(h.child.SessionID, child.SessionID, "", 2)
		if err != nil {
			t.Fatalf("Drain(parent before child's terminal write): %v", err)
		}
		if len(messages) != 0 {
			t.Fatalf("published child messages before terminal/outbox commit = %d, want zero (D2 CRIT-001)", len(messages))
		}

		want := "1 sub-agents are still running: " + child.SessionID + q2bRejectSuffix
		q2bAssertRejectedClaim(t, h, "the child is still committing its completion", want)
		if got := h.goalRecord().State; got != generated.GoalStateActive {
			t.Fatalf("rejected parent claim changed goal state to %q, want active", got)
		}
	}
	t.Cleanup(func() { completeStateWriteTestHook = oldHook })

	if err := h.al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: answer}, nil); err != nil {
		t.Fatalf("complete direct child: %v", err)
	}
	if boundaryCalls != 1 {
		t.Fatalf("pre-commit boundary calls = %d, want exactly 1", boundaryCalls)
	}
	completed, err := h.lifecycle.Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(completed child): %v", err)
	}
	if completed.State != session.LifecycleCompleted {
		t.Fatalf("child after completion = %q, want completed", completed.State)
	}
	if completed.FinalDelivery == nil || completed.FinalDelivery.CommitID != child.ExecutionID.RunID {
		t.Fatalf("committed child outbox = %+v, want producing run %q", completed.FinalDelivery, child.ExecutionID.RunID)
	}
	messages, _, _, err := h.inbox.Drain(h.child.SessionID, child.SessionID, "", 2)
	if err != nil {
		t.Fatalf("Drain(parent after child's terminal write): %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("delivered child messages after terminal write = %d, want exactly one final handback", len(messages))
	}
	handback, err := messages[0].AsSessionMessageHandback()
	if err != nil {
		t.Fatalf("decode delivered child handback: %v", err)
	}
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if handback.MessageId != wantID || handback.Mode != generated.SessionMessageHandbackModeFinal || handback.ResultSoFar != answer {
		t.Fatalf("delivered handback = %+v, want id=%q mode=final result=%q", handback, wantID, answer)
	}
	if h.al.steeredCompletionWriteActive(child.SessionID) {
		t.Fatal("completion-write liveness remained active after the terminal write")
	}
	// Positive control: the same registered parent tool becomes eligible only
	// after the held boundary has passed, without changing the parent's goal.
	q2bAssertOriginalMetResult(t, h, "the delivered child is now terminal")
}
