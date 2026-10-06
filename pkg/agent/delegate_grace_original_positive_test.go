package agent

// D2/D7 under the founder's one-stop decision (2026-10-05): real registered
// stop_all, unchanged canonical wiring/default timer. This independent positive
// proves the ORIGINAL stop FORCES its own still-working A at 3 s (polite stop
// immediately, never the retired 5 s grace), rather than merely sparing B or
// accepting a fresh stop. Only external paid-provider I/O is held. No test
// calls a hard-cancel method.

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type originalGraceProvider struct {
	entered       chan struct{}
	softCancelled chan struct{}
	release       chan struct{}
	once          sync.Once
	calls         atomic.Int32
}

func (p *originalGraceProvider) open()                   { p.once.Do(func() { close(p.release) }) }
func (p *originalGraceProvider) GetDefaultModel() string { return "original-public-grace-positive" }
func (p *originalGraceProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if n := p.calls.Add(1); n != 1 {
		return nil, fmt.Errorf("original grace provider: unexpected additional request %d", n)
	}
	close(p.entered)
	// The real cooperative hook cancels providerCancel immediately. Witness
	// that exact boundary, then model noncooperative I/O until its gate opens.
	<-ctx.Done()
	close(p.softCancelled)
	<-p.release
	return &providers.LLMResponse{Content: "late generated answer must not become a terminal final"}, nil
}

func TestDelegateStopAll_OriginalStopForcesOriginalSelectedRunAtThreeSeconds(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &originalGraceProvider{entered: make(chan struct{}), softCancelled: make(chan struct{}), release: make(chan struct{})}
	instance, found := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !found {
		t.Fatal("SETUP: genuine registered delegate agent missing")
	}
	instance.Provider = provider
	t.Cleanup(provider.open)
	parent := newTestSteeringSession(t, al, "ws-original-grace-positive")
	old := launchGoalBearingChild(t, al, parent, "call-original-grace-positive", goalChildLaunchOptions{live: true})
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: real original A did not reach its external provider")
	}
	handle := al.getActiveTurnState(old.SessionID)
	if handle == nil || !handle.IsAlive() || al.tsExecutionClaim(handle, old.SessionID) != al.executionClaimFor(old) {
		t.Fatal("SETUP: actual original immutable handle/owner missing")
	}
	stoppedAt := time.Now()
	result := delegateToolFor(t, al).Execute(tools.WithTranscriptSessionID(context.Background(), parent),
		map[string]any{"action": "stop_all", "session_id": old.SessionID})
	if result == nil || result.IsError {
		t.Fatalf("actual registered public Stop-all: %+v", result)
	}
	select {
	case <-provider.softCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: canonical soft hook did not reach the actual provider-cancel boundary")
	}
	accepted := rootReopenedRecord(t, al, old.SessionID)
	if accepted.State != session.LifecycleRunning || accepted.Stop == nil || accepted.StopEffect == nil || accepted.StopNote == nil || accepted.StopEffect.ControlID == "" || accepted.StopEffect.Target.Generation != old.Generation || accepted.StopEffect.Target.RunID != old.ExecutionID.RunID || accepted.StopEffect.Target.BootSeq != old.ExecutionID.BootSeq || !handle.IsAlive() || handle.hardAbortRequested() {
		t.Fatalf("positive premise: A did not remain WORKING under its original accepted soft selection: record=%+v effect=%+v", accepted, accepted.StopEffect)
	}
	beforeEffects, err := al.GetSessionLifecycleStore().AcceptedStopEffects(old.SessionID)
	if err != nil || len(beforeEffects) != 1 || !reflect.DeepEqual(beforeEffects[0], *accepted.StopEffect) {
		t.Fatalf("SETUP: genuine original acceptance inventory=%+v err=%v, want exactly its one full pair", beforeEffects, err)
	}
	// The default timer is untouched; only its real callback can force the
	// abort here. I/O remains held, so a normal provider completion cannot win.
	// The polite stage was immediate; the forced stage is at 3 s, never 5 s.
	requireOneStopTimeline(t, "original stop of A", handle, stoppedAt)
	provider.open()
	joinGoalFixtureRuns(t, al)
	final := rootReopenedRecord(t, al, old.SessionID)
	if final.State != session.LifecycleStopped || final.Terminal() || final.Generation != old.Generation || !reflect.DeepEqual(final.ExecutionID, old.ExecutionID) || final.Stop != nil || final.StopNote == nil || final.StopNote.Cause != session.StopCauseStop || !reflect.DeepEqual(final.StopEffect, accepted.StopEffect) || final.FinalDelivery != nil || !handle.hardAbortRequested() {
		t.Errorf("original grace failed its own selected nonfatal landing or accepted a different Stop: final=%+v effect=%+v want=%+v", final, final.StopEffect, accepted.StopEffect)
	}
	afterEffects, err := al.GetSessionLifecycleStore().AcceptedStopEffects(old.SessionID)
	if err != nil || !reflect.DeepEqual(afterEffects, beforeEffects) {
		t.Errorf("ORIGINAL timer accepted a new control instead of carrying its own selection: before=%+v after=%+v err=%v", beforeEffects, afterEffects, err)
	}
	transitions, err := al.GetSessionLifecycleStore().ListStoppedTransitions(old.SessionID)
	if err != nil || len(transitions) != 1 || transitions[0].ControlID != accepted.StopEffect.ControlID || transitions[0].Generation != old.Generation || transitions[0].Cause != session.StopCauseStop {
		t.Errorf("original stop landed history=%+v err=%v, want only its original control/owner", transitions, err)
	}
	if goal := mustGoalRecord(t, old.GoalRef); goal.State != generated.GoalStateActive {
		t.Errorf("original grace stop ended active goal: %+v", goal)
	}
	if provider.calls.Load() != 1 || al.getActiveTurnState(old.SessionID) != nil || al.steerAdmission().activeCount() != 0 || al.steerAdmission().queueLen() != 0 {
		t.Errorf("original grace did not join its one actual execution/disposal: calls=%d active/queued=%d/%d", provider.calls.Load(), al.steerAdmission().activeCount(), al.steerAdmission().queueLen())
	}
}
