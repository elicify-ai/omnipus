package agent

// Founder Q2=B replacement: only a met claim with this session's own live
// direct children is rejected, synchronously by the registered goal_claim tool.
// The exact refusal (including "1 sub-agents") comes from the founder's rule;
// the success payload comes from origin/release/v0.1.1:pkg/tools/goal_claim.go.
// Reuse the Q2 harness to create real sessions, goal records, lifecycle edges,
// and a production-wired tool; its prose-marker helper is NOT the call boundary.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const q2bRejectSuffix = ". Wait for them to finish or cancel them, then claim again"

// q2bClaimThroughRegisteredTool uses the same session-context call path as the
// existing b6ClaimMetAndPersist test, without persisting a rejected call.
func q2bClaimThroughRegisteredTool(t *testing.T, h *q2bHarness, evidence string) *tools.ToolResult {
	t.Helper()
	claimTool, ok := h.childTurn.agent.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("BLOCKED: production goal_claim is not registered — required by founder Q2=B replacement")
	}
	result := claimTool.Execute(tools.WithTranscriptSessionID(context.Background(), h.child.SessionID),
		map[string]any{"status": tools.GoalClaimStatusMet, "evidence": evidence})
	if result == nil {
		t.Fatal("goal_claim returned nil; expected a tool result in the same call")
	}
	return result
}

func q2bAssertRejectedClaim(t *testing.T, h *q2bHarness, evidence, want string) {
	t.Helper()
	result := q2bClaimThroughRegisteredTool(t, h, evidence)
	if result.ForLLM != want {
		t.Fatalf("goal_claim rejection text = %q, want exactly %q", result.ForLLM, want)
	}
	if !result.IsError {
		t.Fatalf("goal_claim returned IsError=false for rejected met claim: %+v", result)
	}
	if got := h.goalRecord().LatestClaim; got != nil {
		t.Fatalf("rejected claim persisted a held latest claim = %+v, want nil", got)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Fatalf("Judge calls from rejected tool call = %d, want 0", calls)
	}
}

func q2bAssertOriginalMetResult(t *testing.T, h *q2bHarness, evidence string) {
	t.Helper()
	result := q2bClaimThroughRegisteredTool(t, h, evidence)
	// The pre-Q2=B tool marshals a payload with evidence, goal_id and status;
	// its met call does not invoke the Judge inside Execute.
	want := fmt.Sprintf(`{"evidence":"%s","goal_id":"%s","status":"met"}`, evidence, h.child.GoalRef)
	if result.ForLLM != want {
		t.Fatalf("goal_claim success text = %q, want original payload %q", result.ForLLM, want)
	}
	if result.IsError {
		t.Fatalf("goal_claim refused an eligible met claim: %+v", result)
	}
	if got := h.goalRecord().LatestClaim; got != nil {
		t.Fatalf("original tool call persisted claim before transcript processing = %+v, want nil", got)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Fatalf("Judge calls inside original goal_claim Execute = %d, want 0", calls)
	}
}

func q2bMakeDirectChildTerminal(t *testing.T, h *q2bHarness, child *session.LifecycleRecord, state session.LifecycleState) {
	t.Helper()
	if err := h.lifecycle.Mutate(child.SessionID, func(rec *session.LifecycleRecord) error {
		rec.State = state
		if state == session.LifecycleFailed {
			rec.FailedReason = "fixture worker failed"
		}
		return nil
	}); err != nil {
		t.Fatalf("make direct child %q terminal (%s): %v", child.SessionID, state, err)
	}
}

func TestGoalQ2B_RejectRunningChildren_OneQueuedDirectChild(t *testing.T) {
	h := newQ2BHarness(t, "q2b-reject-queued-owner")
	child := q2bLaunchDescendant(t, h, "q2b-reject-queued-direct", session.LifecycleQueued)
	want := "1 sub-agents are still running: " + child.SessionID + q2bRejectSuffix
	q2bAssertRejectedClaim(t, h, "queued worker has not finished", want)
}

func TestGoalQ2B_RejectRunningChildren_OneAliveRunningDirectChild(t *testing.T) {
	h := newQ2BHarness(t, "q2b-reject-running-owner")
	child := q2bLaunchDescendant(t, h, "q2b-reject-running-direct", session.LifecycleRunning)
	q2bKeepTurnAlive(t, h.al, child.SessionID)
	if turn := h.al.getActiveTurnState(child.SessionID); turn == nil || !turn.IsAlive() {
		t.Fatal("fixture: running direct child has no alive turn")
	}
	want := "1 sub-agents are still running: " + child.SessionID + q2bRejectSuffix
	q2bAssertRejectedClaim(t, h, "running worker has not finished", want)
}

func TestGoalQ2B_RejectRunningChildren_TwoDirectChildrenInListOrder(t *testing.T) {
	h := newQ2BHarness(t, "q2b-reject-two-owner")
	queued := q2bLaunchDescendant(t, h, "q2b-reject-two-queued", session.LifecycleQueued)
	running := q2bLaunchDescendant(t, h, "q2b-reject-two-running", session.LifecycleRunning)
	q2bKeepTurnAlive(t, h.al, running.SessionID)

	children, err := h.lifecycle.List(session.LifecycleFilter{SteeringSessionID: h.child.SessionID})
	if err != nil {
		t.Fatalf("List(direct children): %v", err)
	}
	wantIDs := []string{queued.SessionID, running.SessionID}
	sort.Strings(wantIDs) // LifecycleIndex.children defines the List order.
	if len(children) != 2 || children[0].SessionID != wantIDs[0] || children[1].SessionID != wantIDs[1] {
		t.Fatalf("fixture direct-child List = %+v, want ids %v", children, wantIDs)
	}
	want := "2 sub-agents are still running: " + strings.Join(wantIDs, ",") + q2bRejectSuffix
	q2bAssertRejectedClaim(t, h, "both workers still own tasks", want)
}

func TestGoalQ2B_RejectRunningChildren_AllDirectChildrenTerminalProceed(t *testing.T) {
	h := newQ2BHarness(t, "q2b-terminal-direct-owner")
	for _, tc := range []struct {
		callID string
		state  session.LifecycleState
	}{
		{"q2b-completed-direct", session.LifecycleCompleted},
		{"q2b-failed-direct", session.LifecycleFailed},
		// No third row: U1 collapsed paused/cancelled/timed-out into the single
		// non-terminal LifecycleStopped — session.IsTerminalLifecycleState /
		// terminalLifecycleStates list only LifecycleCompleted and LifecycleFailed
		// as terminal now, so there is no longer a 3rd terminal state to use here.
	} {
		child := q2bLaunchDescendant(t, h, tc.callID, session.LifecycleQueued)
		q2bMakeDirectChildTerminal(t, h, child, tc.state)
	}
	q2bAssertOriginalMetResult(t, h, "terminal workers already handed back")
}

func TestGoalQ2B_RejectRunningChildren_NoDirectChildrenProceed(t *testing.T) {
	h := newQ2BHarness(t, "q2b-no-direct-owner")
	children, err := h.lifecycle.List(session.LifecycleFilter{SteeringSessionID: h.child.SessionID})
	if err != nil || len(children) != 0 {
		t.Fatalf("fixture direct children = %+v, err=%v, want none", children, err)
	}
	q2bAssertOriginalMetResult(t, h, "no delegated work remains")
}

func TestGoalQ2B_RejectRunningChildren_RunningGrandchildWithTerminalDirectChildProceeds(t *testing.T) {
	h := newQ2BHarness(t, "q2b-grandchild-owner")
	direct := q2bLaunchDescendant(t, h, "q2b-grandchild-direct", session.LifecycleRunning)
	grandchildResult, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: direct.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "finish the grandchild's independent work",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2b-running-grandchild"},
	})
	if err != nil {
		t.Fatalf("Launch(grandchild): %v", err)
	}
	grandchild, err := h.lifecycle.Load(grandchildResult.SessionID)
	if err != nil {
		t.Fatalf("Load(grandchild): %v", err)
	}
	grandchild.State = session.LifecycleRunning
	if persistErr := h.lifecycle.Persist(grandchild); persistErr != nil {
		t.Fatalf("Persist(grandchild running): %v", persistErr)
	}
	q2bKeepTurnAlive(t, h.al, grandchild.SessionID)
	q2bMakeDirectChildTerminal(t, h, direct, session.LifecycleCompleted)

	own, err := h.lifecycle.List(session.LifecycleFilter{SteeringSessionID: h.child.SessionID})
	if err != nil || len(own) != 1 || own[0].SessionID != direct.SessionID || !own[0].Terminal() {
		t.Fatalf("fixture owner direct children = %+v, err=%v, want only terminal direct child %q", own, err, direct.SessionID)
	}
	nested, err := h.lifecycle.List(session.LifecycleFilter{SteeringSessionID: direct.SessionID})
	if err != nil || len(nested) != 1 || nested[0].SessionID != grandchild.SessionID || nested[0].State != session.LifecycleRunning {
		t.Fatalf("fixture grandchild = %+v, err=%v, want only running grandchild %q", nested, err, grandchild.SessionID)
	}
	q2bAssertOriginalMetResult(t, h, "all of my direct workers are terminal")
}
