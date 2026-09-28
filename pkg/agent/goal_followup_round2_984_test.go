package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestGoalDelegation984_MetWithRunningDescendantWaitsForFinalHandback updates
// the pre-Q2 F1 oracle: a met claim cannot wake a verdict while descendant
// work remains. The descendant's terminal transition schedules one fresh
// evaluation; only that fresh met claim may produce the final handback wake.
func TestGoalDelegation984_MetWithRunningDescendantWaitsForFinalHandback(t *testing.T) {
	h := newQ2BHarness(t, "q2b-followup-running-descendant")
	descendant := q2bLaunchDescendant(t, h, "q2b-followup-grandchild", session.LifecycleRunning)
	q2bKeepTurnAlive(t, h.al, descendant.SessionID)

	initial := h.claimMet("descendant still owns required work")
	if initial.goalDeferredAdjudication != nil {
		t.Fatal("met claim scheduled adjudication while descendant was running")
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Fatalf("Judge calls before descendant completion = %d, want 0", calls)
	}
	if messages := h.parentMessages(); len(messages) != 0 {
		t.Fatalf("parent messages before descendant completion = %d, want 0", len(messages))
	}
	if wakes := h.parentWakeEvents(); len(wakes) != 0 {
		t.Fatalf("parent wakes before descendant completion = %d, want 0", len(wakes))
	}

	if err := h.al.completeSteeredTurn(context.Background(), descendant,
		turnResult{finalContent: "descendant evidence"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn(descendant): %v", err)
	}
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 1 {
		t.Fatalf("post-terminal re-evaluations = %d, want exactly 1", got)
	}

	fresh := h.claimMet("fresh claim after reviewing descendant evidence")
	if fresh.goalDeferredAdjudication == nil {
		t.Fatal("fresh post-handback claim did not schedule adjudication")
	}
	h.al.dispatchDeferredGoalAdjudication(fresh.goalDeferredAdjudication)
	if calls := h.judge.callCount(); calls != 1 {
		t.Fatalf("Judge calls after fresh claim = %d, want exactly 1", calls)
	}
	if wakes := h.parentWakeEvents(); len(wakes) != 1 || wakes[0].SourceKind != "message_parent:handback" {
		t.Fatalf("parent wakes after fresh met claim = %+v, want exactly one final handback", wakes)
	}
	wantFinalID := fmt.Sprintf("%s:%d:final", h.child.SessionID, h.child.Generation)
	wakes := h.parentWakeEvents()
	if got := fmt.Sprint(wakes[0].Metadata["steer_message_id"]); got != wantFinalID {
		t.Fatalf("final parent wake id = %q, want deterministic id %q", got, wantFinalID)
	}
	messages := h.parentMessages()
	if len(messages) != 1 {
		t.Fatalf("parent messages after fresh met claim = %d, want exactly 1", len(messages))
	}
	if got := messageIDOf(messages[0]); got != wantFinalID {
		t.Fatalf("final parent message id = %q, want deterministic id %q", got, wantFinalID)
	}
	class, err := session.ClassifySessionMessage(messages[0])
	if err != nil {
		t.Fatalf("ClassifySessionMessage: %v", err)
	}
	if class.Kind != "handback" {
		t.Fatalf("only parent message kind = %q, want handback", class.Kind)
	}
}

// TestGoal984_CompletionTailSingleShotDuringFinishedTurnRace pins architect
// re-review F4. Both competing paths pass their preconditions before either
// may deliver: the normal just-finished turn disposition and the goal-ender's
// deferred tail. One deterministic final entry means exactly one parent wake.
func TestGoal984_CompletionTailSingleShotDuringFinishedTurnRace(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "call-f4-single-shot")

	ts, err := al.reconstructSteeredTurn(child, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	ts.isFinished.Store(true)
	al.activeTurnStates.Store(child.SessionID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(child.SessionID) })

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	completeBeforeDeliveryTestHook = func(sessionID string) {
		if sessionID != child.SessionID {
			return
		}
		arrived <- struct{}{}
		<-release
	}
	t.Cleanup(func() { completeBeforeDeliveryTestHook = nil })

	var wakeMu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		wakeMu.Lock()
		defer wakeMu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})

	directDone := make(chan error, 1)
	go func() {
		directDone <- al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "child result"}, nil)
	}()
	deferredDone := make(chan struct{})
	go func() {
		al.completeSteeredTurnIfDeferredAtGate(child.SessionID)
		close(deferredDone)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatalf("completion path %d did not reach the deterministic pre-delivery seam", i+1)
		}
	}
	close(release)
	if err := <-directDone; err != nil {
		t.Fatalf("normal completion path: %v", err)
	}
	select {
	case <-deferredDone:
	case <-time.After(10 * time.Second):
		t.Fatal("deferred completion path did not return")
	}

	wakeMu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	wakeMu.Unlock()
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != wantID {
		t.Fatalf("parent wakes = %v, want exactly one deterministic final wake %q", gotWakeIDs, wantID)
	}
}
