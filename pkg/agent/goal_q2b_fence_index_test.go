package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// q2bFenceIndexBreakGoalStore makes every goal-directory ListActive fail. The
// direct failing read is the control: a successful launch under this condition
// cannot have obtained its completion-fence decision from that directory.
func q2bFenceIndexBreakGoalStore(t *testing.T) {
	t.Helper()
	goalsDir := resolveGoalRecordStore().Dir()
	if err := os.RemoveAll(goalsDir); err != nil {
		t.Fatalf("remove goal records for unavailable-store fixture: %v", err)
	}
	if err := os.WriteFile(goalsDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("replace goal directory with a file: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(goalsDir); err != nil {
			t.Errorf("remove unavailable-store fixture: %v", err)
		}
		if err := os.MkdirAll(goalsDir, 0o700); err != nil {
			t.Errorf("restore goal directory for cleanup: %v", err)
		}
	})
	if _, err := resolveGoalRecordStore().ListActive(); err == nil {
		t.Fatal("fixture did not fail the goal-store read; cannot prove the fence skipped it")
	}
}

func q2bFenceIndexAssertLaunch(t *testing.T, h *q2bHarness, callID string, wantFence bool) {
	t.Helper()
	before := q2bChildCount(t, h.lifecycle, h.child.SessionID)
	_, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: h.child.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "completion fence index must not consult the goal store",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if wantFence {
		if err == nil || !strings.Contains(err.Error(), "completion claim is pending for steering session") {
			t.Fatalf("launch with a pending completion phase = %v, want the completion-claim refusal (not a goal-store read error)", err)
		}
		if after := q2bChildCount(t, h.lifecycle, h.child.SessionID); after != before {
			t.Fatalf("child count after refused launch = %d, want %d", after, before)
		}
		return
	}
	if err != nil {
		t.Fatalf("launch without a completion phase = %v, want success despite unreadable goal store", err)
	}
	if after := q2bChildCount(t, h.lifecycle, h.child.SessionID); after != before+1 {
		t.Fatalf("child count after allowed launch = %d, want %d", after, before+1)
	}
}

func TestGoalQ2B_CompletionFenceLaunchSkipsUnreadableGoalStore(t *testing.T) {
	h := newQ2BHarness(t, "q2b-index-unreadable")
	if !h.al.goalInstallWaitingCompletion(h.child.GoalRef) {
		t.Fatal("install waiting completion phase before making goal store unreadable")
	}
	q2bFenceIndexBreakGoalStore(t)

	q2bFenceIndexAssertLaunch(t, h, "index-waiting", true)
	if !h.al.goalPromoteCompletionToAdjudicating(h.child.GoalRef) {
		t.Fatal("promote waiting completion phase while goal store is unreadable")
	}
	q2bFenceIndexAssertLaunch(t, h, "index-adjudicating", true)
	h.al.goalClearCompletionPhase(h.child.GoalRef)
	q2bFenceIndexAssertLaunch(t, h, "index-cleared", false)
}

func TestGoalQ2B_SessionFenceIndexRestoresAndClearsOnRestate(t *testing.T) {
	h := newQ2BHarness(t, "q2b-index-restart")
	if err := recordGoalClaim(h.child.GoalRef, generated.GoalLatestClaimStatusMet, "durable pending completion"); err != nil {
		t.Fatalf("persist met claim for restore evidence: %v", err)
	}
	rec := h.goalRecord()
	if rec.LatestClaim == nil || rec.LatestClaim.Status != generated.GoalLatestClaimStatusMet || rec.ActiveSessionID != h.child.SessionID {
		t.Fatalf("invalid durable restore evidence: claim %+v, owner %q", rec.LatestClaim, rec.ActiveSessionID)
	}
	if !h.al.goalInstallWaitingCompletion(h.child.GoalRef) {
		t.Fatal("install phase before process-local restart simulation")
	}
	resetGoalTriggerStateForTest()
	t.Cleanup(resetGoalTriggerStateForTest)
	q2bFenceIndexBreakGoalStore(t)

	q2bFenceIndexAssertLaunch(t, h, "index-restart-cleared", false)
	h.al.goalRestoreWaitingCompletion(rec)
	q2bFenceIndexAssertLaunch(t, h, "index-restored-waiting", true)
	h.al.goalClearCompletionPhaseFromRestate(rec.GoalID)
	q2bFenceIndexAssertLaunch(t, h, "index-restate-cleared", false)
}
