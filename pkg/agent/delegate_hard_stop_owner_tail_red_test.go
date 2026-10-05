package agent

// Frozen D2/D4/T27, preserved by October 4 C6: a hard-abort REQUEST does
// not settle the original execution tail. Only external provider I/O is held.
import (
	"context"
	"reflect"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestDelegateStopAll_HardRequestRetainsFenceUntilOriginalOwnerTail(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &originalGraceProvider{entered: make(chan struct{}), softCancelled: make(chan struct{}), release: make(chan struct{})}
	instance, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: actual registered agent missing")
	}
	instance.Provider = provider
	t.Cleanup(provider.open)
	parent := newTestSteeringSession(t, al, "ws-hard-stop-original-tail")
	old := launchGoalBearingChild(t, al, parent, "call-hard-stop-owner-tail", goalChildLaunchOptions{live: true})
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: real original execution did not enter external provider")
	}
	handle := al.getActiveTurnState(old.SessionID)
	claim := al.executionClaimFor(old)
	if handle == nil || !handle.IsAlive() || al.tsExecutionClaim(handle, old.SessionID) != claim {
		t.Fatal("SETUP: original immutable owner handle missing")
	}
	goalBefore := mustGoalRecord(t, old.GoalRef)
	result := delegateToolFor(t, al).Execute(tools.WithTranscriptSessionID(context.Background(), parent),
		map[string]any{"action": "stop_all", "session_id": old.SessionID, "hard": true})
	if result == nil || result.IsError {
		t.Fatalf("actual registered hard Stop-all refused: %+v", result)
	}
	select {
	case <-provider.softCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("hard Stop did not reach original provider cancellation boundary")
	}
	if !handle.hardAbortRequested() {
		t.Error("actual hard request did not target original immutable execution")
	}
	// Provider has witnessed cancellation, but cannot return before release.
	// Therefore the real owning dispatch/completion tail cannot have settled.
	accepted := rootReopenedRecord(t, al, old.SessionID)
	if accepted.State != session.LifecycleRunning || accepted.Stop == nil || accepted.Stop.Generation != old.Generation || accepted.StopNote == nil || accepted.StopEffect == nil || !reflect.DeepEqual(accepted.ExecutionID, old.ExecutionID) {
		t.Errorf("D2/D4: hard-abort request prematurely landed/cleared original owning fence while provider I/O is held: state=%s fence=%+v note=%+v effect=%+v", accepted.State, accepted.Stop, accepted.StopNote, accepted.StopEffect)
	}
	if accepted.StopEffect == nil {
		t.Fatal("hard Stop lost its authentic selected control/owner pair")
	}
	if accepted.StopEffect.Target.RunID != old.ExecutionID.RunID || accepted.StopEffect.Target.BootSeq != old.ExecutionID.BootSeq || accepted.StopEffect.Target.Generation != old.Generation {
		t.Errorf("hard request lost original execution targeting: effect=%+v original=%+v", accepted.StopEffect, old.ExecutionID)
	}
	transitions, err := al.GetSessionLifecycleStore().ListStoppedTransitions(old.SessionID)
	if err != nil || len(transitions) != 0 {
		t.Errorf("D4: stopped landing/receipt preceded owner-tail settlement: transitions=%+v err=%v", transitions, err)
	}
	provider.open()
	joinGoalFixtureRuns(t, al)
	stopped := rootReopenedRecord(t, al, old.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.Terminal() || stopped.Stop != nil || stopped.StopNote == nil || stopped.FinalDelivery != nil || !reflect.DeepEqual(stopped.ExecutionID, old.ExecutionID) || !reflect.DeepEqual(stopped.StopEffect, accepted.StopEffect) {
		t.Errorf("D2: real owner tail did not land only its selected nonfatal Stop: stopped=%+v", stopped)
	}
	transitions, err = al.GetSessionLifecycleStore().ListStoppedTransitions(old.SessionID)
	if err != nil || len(transitions) != 1 || transitions[0].ControlID != accepted.StopEffect.ControlID {
		t.Errorf("D4: selected stop must land once after tail: transitions=%+v err=%v", transitions, err)
	}
	if after := mustGoalRecord(t, old.GoalRef); !reflect.DeepEqual(after, goalBefore) || after.State != generated.GoalStateActive {
		t.Errorf("hard Stop altered active goal: before=%+v after=%+v", goalBefore, after)
	}
	f := &bootNoticeControlFixture{al: al, parent: parent, resumed: old}
	assertBootNoticeNoFatalFinal(t, f)

	// Real explicit same-generation Resume and fresh provider B. Delayed A
	// completion uses its authentic old claim, never a fabricated run id.
	freshGate := newGoalRunGate("replacement B remains active", nil)
	installGoalRunProvider(t, al, freshGate)
	generation, err := al.steerCanceller().Revive(context.Background(), old.SessionID, steer.Principal{Kind: steer.PrincipalKindAgent, ID: parent})
	if err != nil || generation != old.Generation {
		t.Fatalf("actual same-generation Resume: generation=%d err=%v", generation, err)
	}
	dispatchChild(t, al, old.SessionID, generation, true)
	awaitGoalProvider(t, freshGate)
	fresh := rootReopenedRecord(t, al, old.SessionID)
	freshHandle := al.getActiveTurnState(old.SessionID)
	if fresh.ExecutionID == nil || fresh.ExecutionID.RunID == old.ExecutionID.RunID || fresh.Stop != nil || fresh.StopNote != nil || freshHandle == nil || !freshHandle.IsAlive() {
		t.Fatal("SETUP: real Resume did not admit a fresh unfenced B")
	}
	if err := al.completeSteeredTurnForExecution(context.Background(), old, turnResult{finalContent: "obsolete original A answer"}, context.Canceled, claim); err != nil {
		t.Fatalf("delayed genuine old completion boundary: %v", err)
	}
	if current := rootReopenedRecord(t, al, old.SessionID); !reflect.DeepEqual(current, fresh) || freshHandle.hardAbortRequested() || !freshHandle.IsAlive() {
		t.Errorf("T27: obsolete original A completion wrote/cancelled resumed B: before=%+v after=%+v", fresh, current)
	}
	assertBootNoticeNoFatalFinal(t, f)
	freshGate.open()
	joinGoalFixtureRuns(t, al)
}
