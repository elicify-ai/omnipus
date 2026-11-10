package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// G1 completion fixtures use real admission, not a forged running record.
// Frozen control-plane ADR D2: "Persist it under the lifecycle lock before
// admission, and copy it unchanged into the admission entry and live execution
// handle." The provider is the only held boundary; stores and admission are real.
func g1AdmitCompletionChild(t *testing.T, al *AgentLoop, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	if al.bootEpochFor() == 0 {
		mintGenuineBootEpochForLoop(t, al)
	}
	rec, _ := r1AdmitChild(t, al, parentID, callID, "finished")
	return rec
}

func g1LaunchQueuedChild(t *testing.T, al *AgentLoop, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	launched, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID,
		Task: "do delegated work", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("SETUP real Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
	if err != nil || rec.State != session.LifecycleQueued {
		t.Fatalf("SETUP queued child = %+v, error=%v", rec, err)
	}
	return rec
}

// Descendants must already exist: the parent's initial real turn then returns
// without a terminal commit because the ordinary completion frontier blocks it.
// This creates a genuine idle-running parent that a child's wake can re-enter.
func g1AdmitWaitingParent(t *testing.T, al *AgentLoop, parent *session.LifecycleRecord) *session.LifecycleRecord {
	t.Helper()
	if al.bootEpochFor() == 0 {
		mintGenuineBootEpochForLoop(t, al)
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP registered parent agent missing")
	}
	inst.Provider = &depthEchoProvider{}
	result, err := NewSteerLauncher(al).Dispatch(context.Background(), parent.SessionID, parent.Generation)
	if err != nil || result.State != steer.DispatchRunning {
		t.Fatalf("SETUP parent real Dispatch=%+v error=%v", result, err)
	}
	admitted, admitErr := al.GetSessionLifecycleStore().Load(parent.SessionID)
	if admitErr != nil || admitted.ExecutionID == nil {
		t.Fatalf("SETUP parent's durable admission is missing: %+v error=%v", admitted, admitErr)
	}
	claim := al.executionClaimFor(admitted)
	// Compute Finished alone is not the outer completion barrier. Wait until
	// this exact producer has retired its disposal tail and admission slot.
	waitFor(t, 5*time.Second, func() bool {
		return al.getActiveTurnState(parent.SessionID) == nil && al.executionDispositionFor(claim) == nil
	})
	rec, err := al.GetSessionLifecycleStore().Load(parent.SessionID)
	if err != nil || rec.State != session.LifecycleRunning || rec.ExecutionID == nil ||
		rec.ExecutionID.RunID == "" || rec.ExecutionID.BootSeq != al.bootEpochFor() {
		t.Fatalf("SETUP idle-running parent without genuine admission = %+v, error=%v", rec, err)
	}
	return rec
}

func g1StopThroughOwner(t *testing.T, al *AgentLoop, child *session.LifecycleRecord) *session.LifecycleRecord {
	t.Helper()
	owner := al.executionDispositionFor(al.executionClaimFor(child))
	if owner == nil {
		t.Fatal("SETUP real child's outer execution owner is missing before Stop")
	}
	result, err := al.StopSession(context.Background(), StopRequest{
		SessionID: child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"},
	})
	if err != nil || result.RootErr != nil || len(result.Report.Unreachable) != 0 {
		t.Fatalf("SETUP owner Stop=%+v error=%v", result, err)
	}
	// The owner closes done after stop landing AND notice/receipt publication,
	// not just after compute exits. Observe that actual settlement barrier.
	select {
	case <-owner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP owning Stop disposition did not settle")
	}
	owner.mu.Lock()
	settlementErr := owner.result
	owner.mu.Unlock()
	if settlementErr != nil {
		t.Fatalf("SETUP owning Stop settlement failed: %v", settlementErr)
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil || rec.State != session.LifecycleStopped || rec.Generation != child.Generation || rec.Stop != nil ||
		rec.StopNote == nil || rec.StopNote.Cause != session.StopCauseStop {
		t.Fatalf("SETUP owner-landed stopped record=%+v error=%v", rec, err)
	}
	return rec
}

// Retry the durable publisher, not a new completion (a terminal outcome is
// immutable). Frozen D2: "Publish only a committed outbox" and "An existing id
// with the same committed payload/identity means retry downstream effects if
// unacknowledged." The payload is read from the real store, never rebuilt.
func g1RetryCommittedFinal(t *testing.T, al *AgentLoop, child *session.LifecycleRecord) (bool, error) {
	t.Helper()
	commit, _, _, retired, err := al.GetSessionLifecycleStore().CommittedFinalDelivery(
		child.SessionID, child.Generation, child.ExecutionID.RunID)
	_ = retired
	if err != nil {
		return false, fmt.Errorf("read real committed final: %w", err)
	}
	var message generated.SessionMessage
	if err := message.UnmarshalJSON(commit.Payload); err != nil {
		return false, fmt.Errorf("decode exact committed final: %w", err)
	}
	return al.publishCommittedFinal(context.Background(), child, steeredCommitResult{
		kind: steeredCommitTerminal, commit: &commit, message: message, messageID: commit.MessageID,
	})
}

// Resume prepares the same generation; this real Dispatch stamps and pins the
// new admission rather than hand-constructing the replacement's run identity.
func g1AdmitResumedChild(t *testing.T, al *AgentLoop, child *session.LifecycleRecord) *session.LifecycleRecord {
	t.Helper()
	provider := &r1CompletionProvider{
		answers: []string{"legitimate generation two"}, entered: make(chan int, 1),
		release: []chan struct{}{make(chan struct{})},
	}
	inst, ok := al.GetRegistry().GetAgent(child.AgentID)
	if !ok {
		t.Fatal("SETUP resumed child's registered agent missing")
	}
	inst.Provider = provider
	t.Cleanup(provider.openAll)
	result, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if err != nil || result.State != steer.DispatchRunning {
		t.Fatalf("SETUP resumed real Dispatch=%+v error=%v", result, err)
	}
	r1AwaitProvider(t, provider, 0)
	current, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	ts := al.getActiveTurnState(child.SessionID)
	if err != nil || current.ExecutionID == nil || ts == nil ||
		al.tsExecutionClaim(ts, current.SessionID) != al.executionClaimFor(current) {
		t.Fatalf("SETUP replacement lacks genuine matching durable/live identity: record=%+v error=%v", current, err)
	}
	return current
}
