package agent

// Deterministic A1 coverage for founder ruling Q2 B.
//
// Oracle sources:
//   - coordination/logs/fix890-opus/arch-q2-design.md, "Invariants" and
//     "Tests required";
//   - coordination/logs/fix890-opus/q2b-r1-red.report.md, "A1 seam required".
//
// Expected outcomes come from those sources, not observed implementation
// output. The two test hooks provide ordering only; every asserted state is
// produced by the real claim, launch, goal-store, and lifecycle-store paths.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

type q2bAtomicLaunchOutcome struct {
	result steer.LaunchResult
	err    error
}

func q2bAwaitAtomicitySignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func q2bAwaitAtomicityLaunch(t *testing.T, done <-chan q2bAtomicLaunchOutcome) q2bAtomicLaunchOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent descendant launch")
		return q2bAtomicLaunchOutcome{}
	}
}

func q2bAwaitAtomicityClaim(t *testing.T, done <-chan *turnResult) *turnResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent completion claim")
		return nil
	}
}

func q2bAtomicityLaunch(h *q2bHarness, callID string) q2bAtomicLaunchOutcome {
	result, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: h.child.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "publish across the completion-claim boundary",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	return q2bAtomicLaunchOutcome{result: result, err: err}
}

func TestGoalQ2B_ClaimLaunchAtomicity(t *testing.T) {
	t.Run("launch past fence publishes before claim scans descendants", func(t *testing.T) {
		h := newQ2BHarness(t, "q2b-a1-launch-first-parent")
		before := q2bChildCount(t, h.lifecycle, h.child.SessionID)

		launchAtFence := make(chan struct{})
		releaseLaunch := make(chan struct{})
		claimAtLock := make(chan struct{})
		launchDone := make(chan q2bAtomicLaunchOutcome, 1)
		claimDone := make(chan *turnResult, 1)
		var launchOnce sync.Once
		var claimOnce sync.Once

		oldLaunchHook := launchAfterCompletionFenceTestHook
		oldClaimHook := goalClaimBeforePublicationLockTestHook
		launchAfterCompletionFenceTestHook = func(parentSessionID string) {
			if parentSessionID != h.child.SessionID {
				return
			}
			launchOnce.Do(func() {
				close(launchAtFence)
				<-releaseLaunch
			})
		}
		goalClaimBeforePublicationLockTestHook = func(sessionID string) {
			if sessionID != h.child.SessionID {
				return
			}
			claimOnce.Do(func() {
				close(claimAtLock)
				// The launch already owns the publication lock. Releasing it
				// from the claim's last pre-lock instruction fixes the order
				// without a scheduler delay: publication must finish before the
				// real claim path can acquire that same lock and scan children.
				close(releaseLaunch)
			})
		}
		t.Cleanup(func() {
			launchAfterCompletionFenceTestHook = oldLaunchHook
			goalClaimBeforePublicationLockTestHook = oldClaimHook
		})

		go func() {
			launchDone <- q2bAtomicityLaunch(h, "q2b-a1-launch-first-child")
		}()
		q2bAwaitAtomicitySignal(t, launchAtFence, "launch to pass the completion fence")

		const evidence = "the launch already admitted under the publication lock must be observed"
		go func() {
			claimDone <- h.claimMet(evidence)
		}()
		q2bAwaitAtomicitySignal(t, claimAtLock, "claim to reach the publication lock")

		launchOutcome := q2bAwaitAtomicityLaunch(t, launchDone)
		claimResult := q2bAwaitAtomicityClaim(t, claimDone)
		if launchOutcome.err != nil {
			t.Fatalf("launch that passed the fence = %v, want successful publication", launchOutcome.err)
		}
		if launchOutcome.result.SessionID == "" {
			t.Fatal("successful launch returned an empty child session id")
		}
		if after := q2bChildCount(t, h.lifecycle, h.child.SessionID); after != before+1 {
			t.Fatalf("published child records = %d, want %d", after, before+1)
		}
		if claimResult.goalDeferredAdjudication != nil {
			t.Fatal("claim scheduled adjudication after a launch had passed the fence; want held claim")
		}
		if !h.al.goalCompletionWaiting(h.child.GoalRef) {
			t.Fatal("claim phase after observing the published child is not waiting_descendants")
		}
		g := h.goalRecord()
		if g.LatestClaim == nil || g.LatestClaim.Evidence != evidence {
			t.Fatalf("latest claim = %+v, want durable evidence %q", g.LatestClaim, evidence)
		}
		if calls := h.judge.callCount(); calls != 0 {
			t.Fatalf("Judge calls after launch-first serialization = %d, want 0", calls)
		}
	})

	t.Run("claim fence refuses launch before another child is created", func(t *testing.T) {
		h := newQ2BHarness(t, "q2b-a1-claim-first-parent")
		q2bLaunchDescendant(t, h, "q2b-a1-existing-child", "queued")
		before := q2bChildCount(t, h.lifecycle, h.child.SessionID)

		claimAtLock := make(chan struct{})
		releaseClaim := make(chan struct{})
		claimDone := make(chan *turnResult, 1)
		launchReady := make(chan struct{})
		fenceInstalled := make(chan struct{})
		launchDone := make(chan q2bAtomicLaunchOutcome, 1)
		launchPassedFence := make(chan struct{}, 1)
		var claimOnce sync.Once

		oldLaunchHook := launchAfterCompletionFenceTestHook
		oldClaimHook := goalClaimBeforePublicationLockTestHook
		goalClaimBeforePublicationLockTestHook = func(sessionID string) {
			if sessionID != h.child.SessionID {
				return
			}
			claimOnce.Do(func() {
				close(claimAtLock)
				<-releaseClaim
			})
		}
		launchAfterCompletionFenceTestHook = func(parentSessionID string) {
			if parentSessionID == h.child.SessionID {
				launchPassedFence <- struct{}{}
			}
		}
		t.Cleanup(func() {
			launchAfterCompletionFenceTestHook = oldLaunchHook
			goalClaimBeforePublicationLockTestHook = oldClaimHook
		})

		const evidence = "the existing queued child keeps the installed fence active"
		go func() {
			result := h.claimMet(evidence)
			claimDone <- result
			close(fenceInstalled)
		}()
		q2bAwaitAtomicitySignal(t, claimAtLock, "claim to reach its pre-lock seam")

		go func() {
			close(launchReady)
			<-fenceInstalled
			launchDone <- q2bAtomicityLaunch(h, "q2b-a1-refused-child")
		}()
		q2bAwaitAtomicitySignal(t, launchReady, "concurrent launch goroutine to be ready")
		close(releaseClaim)

		claimResult := q2bAwaitAtomicityClaim(t, claimDone)
		launchOutcome := q2bAwaitAtomicityLaunch(t, launchDone)
		if claimResult.goalDeferredAdjudication != nil {
			t.Fatal("claim with an existing queued descendant scheduled adjudication; want active fence")
		}
		if launchOutcome.err == nil {
			t.Fatal("launch after claim installed the completion fence succeeded; want refusal")
		}
		if !strings.Contains(launchOutcome.err.Error(), "completion claim is pending") {
			t.Fatalf("launch refusal = %q, want completion-claim-pending reason", launchOutcome.err)
		}
		if after := q2bChildCount(t, h.lifecycle, h.child.SessionID); after != before {
			t.Fatalf("child records after fenced launch = %d, want unchanged %d", after, before)
		}
		select {
		case <-launchPassedFence:
			t.Fatal("refused launch reached the post-fence hook; refusal must precede every child record")
		default:
		}
		if !h.al.goalCompletionWaiting(h.child.GoalRef) {
			t.Fatal("claim-first completion phase is not waiting_descendants")
		}
		if calls := h.judge.callCount(); calls != 0 {
			t.Fatalf("Judge calls after claim-first serialization = %d, want 0", calls)
		}
	})
}
