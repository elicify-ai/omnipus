package agent

// D2/D7/T27 under the founder's one-stop decision (2026-10-05): the ORIGINAL
// public Stop-all's FORCED stop (3 s after the polite stop; the tool-side 5 s
// grace backstop is retired) must not accept a new Stop on, nor touch, a fresh
// same-generation execution admitted after the original landed. The canonical
// registered tool and the real timer stay untouched; only the external paid
// provider edge is gated and the real wall clock is waited out -- no goroutine
// stack inspection, no cascade-lock barrier, no fabricated identity.

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Only the old paid-provider edge ignores cancellation, until its gate opens.
// This holds a genuine working A while the real grace timer reaches its cut.
type graceSelectionProvider struct {
	mu    sync.Mutex
	gates []*goalRunGate
	next  int
}

func (p *graceSelectionProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := p.next
	p.next++
	p.mu.Unlock()
	if index >= len(p.gates) {
		return nil, fmt.Errorf("unexpected real provider call%d", index)
	}
	gate := p.gates[index]
	gate.enteredOnce.Do(func() { close(gate.entered) })
	if index == 0 {
		<-gate.release
		return &providers.LLMResponse{Content: gate.answer}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate.release:
		return &providers.LLMResponse{Content: gate.answer}, nil
	}
}
func (*graceSelectionProvider) GetDefaultModel() string { return "actual-public-grace-selection" }
func TestDelegateStopAll_ForcedStopSparesSameGenerationResume(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement_queued_%v", queued), func(t *testing.T) { publicGraceSelectionCase(t, queued) })
	}
}
func publicGraceSelectionCase(t *testing.T, queued bool) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1
	oldGate := newGoalRunGate("old uncooperative paid request exits", nil)
	freshGate := newGoalRunGate("fresh request must not inherit old backstop", nil)
	gates := []*goalRunGate{oldGate, freshGate}
	var blocker *goalRunGate
	if queued {
		blocker = newGoalRunGate("independent slot blocker", nil)
		gates = []*goalRunGate{oldGate, blocker, freshGate}
	}
	instance, found := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !found {
		t.Fatal("SETUP: actual delegate agent missing")
	}
	instance.Provider = &graceSelectionProvider{gates: gates}
	t.Cleanup(func() {
		for _, gate := range gates {
			gate.open()
		}
	})
	parentID := newTestSteeringSession(t, al, "ws-public-grace-selection")
	old := launchGoalBearingChild(t, al, parentID, "call-real-public-grace", goalChildLaunchOptions{live: true})
	awaitGoalProvider(t, oldGate)
	oldHandle := al.getActiveTurnState(old.SessionID)
	if oldHandle == nil || !al.tsExecutionClaim(oldHandle, old.SessionID).matches(old) {
		t.Fatal("SETUP: genuine old immutable handle missing")
	}
	if queued {
		blockerID, blockerGen := launchParkedChild(t, al, parentID, "call-public-grace-blocker", "independent slot blocker")
		dispatchChild(t, al, blockerID, blockerGen, false)
	}
	tool := delegateToolFor(t, al)
	ctx := tools.WithTranscriptSessionID(context.Background(), parentID)
	stoppedAt := time.Now()
	result := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": old.SessionID})
	if result == nil || result.IsError {
		t.Fatalf("actual public Stop-all failed: %+v", result)
	}
	canceller := al.steerCanceller()
	accepted := rootReopenedRecord(t, al, old.SessionID)
	if accepted.Stop == nil || accepted.StopEffect == nil || accepted.StopEffect.Target.RunID != old.ExecutionID.RunID {
		t.Fatal("instrument: real original stop acceptance no longer selects A")
	}
	// A's uncooperative provider returns once its gate opens; its owning tail
	// then lands the selected stop. The forced stop (3 s) has NOT fired yet.
	oldGate.open()
	if queued {
		// The blocker now owns the released slot; wait for A's real disposal tail,
		// not the entire dispatch group (which correctly still includes blocker).
		select {
		case <-oldHandle.Finished():
		case <-time.After(5 * time.Second):
			t.Fatal("selected old turn did not exit")
		}
		waitForGate(t, "original selected admission's disposal/slot release", func() bool { return !al.steerAdmission().hasExecutionReservation(al.executionClaimFor(old)) })
		awaitGoalProvider(t, blocker)
	} else {
		joinGoalFixtureRuns(t, al)
	}
	stopped := rootReopenedRecord(t, al, old.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.Stop != nil || stopped.StopNote == nil || stopped.Generation != old.Generation {
		t.Fatalf("selected A did not actually land before Resume: %+v", stopped)
	}
	generation, resumeErr := canceller.Revive(context.Background(), old.SessionID, steer.Principal{Kind: steer.PrincipalKindAgent, ID: parentID})
	if resumeErr != nil || generation != old.Generation {
		t.Fatalf("real same-generation Resume=%d err=%v", generation, resumeErr)
	}
	dispatchChild(t, al, old.SessionID, generation, !queued)
	if !queued {
		awaitGoalProvider(t, freshGate)
	}
	fresh := rootReopenedRecord(t, al, old.SessionID)
	freshHandle := al.getActiveTurnState(old.SessionID)
	if fresh.ExecutionID == nil || fresh.ExecutionID.RunID == old.ExecutionID.RunID || fresh.ExecutionID.BootSeq != old.ExecutionID.BootSeq || fresh.Generation != old.Generation || fresh.Stop != nil || fresh.StopNote != nil {
		t.Fatalf("fresh B admission is not actual same-G/boot distinct identity: %+v", fresh)
	}
	if queued && al.steerAdmission().queueLen() != 1 {
		t.Fatal("instrument: actual B queued entry not present")
	}
	if !queued && (freshHandle == nil || !al.tsExecutionClaim(freshHandle, old.SessionID).matches(fresh)) {
		t.Fatal("instrument: actual fresh B handle missing")
	}
	// Outlive the original stop's whole timeline: the forced stop fires 3 s
	// after the polite one, and the retired 5 s grace would have fired by 6 s.
	// Both must find A already landed and leave B alone.
	if wait := 6500*time.Millisecond - time.Since(stoppedAt); wait > 0 {
		time.Sleep(wait)
	}
	after := rootReopenedRecord(t, al, old.SessionID)
	if after.State != fresh.State || after.Generation != fresh.Generation || !reflect.DeepEqual(after.ExecutionID, fresh.ExecutionID) || !reflect.DeepEqual(after.Stop, fresh.Stop) || !reflect.DeepEqual(after.StopNote, fresh.StopNote) || !reflect.DeepEqual(after.StopEffect, fresh.StopEffect) {
		t.Errorf("the ORIGINAL stop's forced stage accepted/landed a NEW stop on B: before=%+v after=%+v", fresh, after)
	}
	if queued {
		if al.steerAdmission().queueLen() != 1 {
			t.Errorf("the original stop's forced stage removed actual queued B: queue=%d,want1", al.steerAdmission().queueLen())
		}
	} else if freshHandle.hardAbortRequested() || !freshHandle.IsAlive() {
		t.Error("the original stop's forced stage aborted fresh SAME-generation provider/handle")
	}
	if goal := mustGoalRecord(t, old.GoalRef); goal.State != generated.GoalStateActive {
		t.Errorf("the original stop's forced stage ended goal: %+v", goal)
	}
	// Cleanup stop. The separate positive test below begins with a provably
	// live B and proves a genuinely newer stop still stops it.
	cleanupStop := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": old.SessionID})
	if cleanupStop == nil || cleanupStop.IsError {
		t.Fatalf("public Stop cleanup failed: %+v", cleanupStop)
	}
	if queued {
		blocker.open()
	}
	joinGoalFixtureRuns(t, al)
	final := rootReopenedRecord(t, al, old.SessionID)
	if final.State != session.LifecycleStopped || final.Stop != nil || final.StopNote == nil || final.ExecutionID == nil || *final.ExecutionID != *fresh.ExecutionID {
		t.Errorf("the newer public Stop failed to stop actual B: %+v", final)
	}
}

// Unlike the cleanup in a failing stale-timer case, this control begins with
// a proven WORKING replacement and proves a genuinely newer PUBLIC Stop's
// effect (the one stop method; there is no separate hard variant any more).
func TestDelegateStopAll_NewPublicStopStopsActualSameGenerationReplacement(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	oldGate, freshGate := newGoalRunGate("old hard-stopped request", nil), newGoalRunGate("fresh hard-stop positive request", nil)
	installGoalRunProvider(t, al, oldGate, freshGate)
	parentID := newTestSteeringSession(t, al, "ws-public-hard-positive")
	old := launchGoalBearingChild(t, al, parentID, "call-public-hard-positive", goalChildLaunchOptions{live: true})
	awaitGoalProvider(t, oldGate)
	tool := delegateToolFor(t, al)
	ctx := tools.WithTranscriptSessionID(context.Background(), parentID)
	initial := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": old.SessionID})
	if initial == nil || initial.IsError {
		t.Fatalf("initial actual PUBLIC Stop: %+v", initial)
	}
	joinGoalFixtureRuns(t, al)
	stopped := rootReopenedRecord(t, al, old.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.Stop != nil || stopped.StopEffect == nil || stopped.StopEffect.Target.RunID != old.ExecutionID.RunID {
		t.Fatalf("initial actual selected Stop did not land A: %+v", stopped)
	}
	generation, resumeErr := al.steerCanceller().Revive(context.Background(), old.SessionID, steer.Principal{Kind: steer.PrincipalKindAgent, ID: parentID})
	if resumeErr != nil || generation != old.Generation {
		t.Fatalf("real same-generation Resume=%d err=%v", generation, resumeErr)
	}
	dispatchChild(t, al, old.SessionID, generation, true)
	awaitGoalProvider(t, freshGate)
	fresh := rootReopenedRecord(t, al, old.SessionID)
	handle := al.getActiveTurnState(old.SessionID)
	if fresh.State != session.LifecycleRunning || fresh.ExecutionID == nil || fresh.ExecutionID.RunID == old.ExecutionID.RunID || fresh.ExecutionID.BootSeq != old.ExecutionID.BootSeq || fresh.Stop != nil || fresh.StopNote != nil || handle == nil || !handle.IsAlive() || handle.hardAbortRequested() {
		t.Fatalf("positive premise: B is not an actual fresh WORKING run: %+v", fresh)
	}
	newer := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": old.SessionID})
	if newer == nil || newer.IsError {
		t.Fatalf("genuine new PUBLIC Stop: %+v", newer)
	}
	joinGoalFixtureRuns(t, al)
	final := rootReopenedRecord(t, al, old.SessionID)
	if final.State != session.LifecycleStopped || final.Stop != nil || final.StopEffect == nil || final.StopEffect.ControlID == stopped.StopEffect.ControlID || final.StopEffect.Target.RunID != fresh.ExecutionID.RunID || final.StopEffect.Target.BootSeq != fresh.ExecutionID.BootSeq {
		t.Fatalf("new PUBLIC Stop did not select/land actual B: %+v", final)
	}
	if polite, _ := handle.gracefulInterruptRequested(); !polite {
		t.Fatalf("new PUBLIC Stop never asked actual B politely: %+v", final)
	}
	if goal := mustGoalRecord(t, old.GoalRef); goal.State != generated.GoalStateActive {
		t.Errorf("new public Stop ended active goal: %+v", goal)
	}
}
