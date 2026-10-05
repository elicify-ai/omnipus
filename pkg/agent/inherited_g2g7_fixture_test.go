package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// g2StoppedOrdinaryRoot admits an actual ordinary turn, then lets its owner
// land Stop. It must not change adr093StoppedRoot: fence tests use that helper
// deliberately to construct an IN-FLIGHT stop, which is not resumable yet.
// newSteerAL has already minted and wired one genuine BootEpochStore.
func g2StoppedOrdinaryRoot(t *testing.T, al *AgentLoop, id string) *session.LifecycleRecord {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: ordinary-root agent is not registered")
	}
	original := inst.Provider
	provider, release := installParkedProvider(t, al)
	defer release()
	defer func() { inst.Provider = original }()
	done := make(chan error, 1)
	go func() {
		_, _, err := al.processMessage(context.Background(), adr093HumanMessage("Work that the owner will stop.", id))
		done <- err
	}()
	adr093WaitForEntered(t, provider, 30*time.Second)
	before := adr093Load(t, al, id)
	if before.ExecutionID == nil || before.ExecutionID.BootSeq != al.bootEpochFor() || before.ExecutionID.RunID == "" {
		t.Fatalf("SETUP: ordinary turn lacks a genuine admitted execution: %+v", before.ExecutionID)
	}
	result, err := al.StopSession(context.Background(), StopRequest{
		SessionID: id, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "user-adr093"},
	})
	if err != nil || result.RootErr != nil || len(result.Report.Unreachable) != 0 {
		t.Fatalf("SETUP: real ordinary-root Stop = %+v, %v", result, err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: selected ordinary execution did not finish after Stop")
	}
	stopped := awaitSteeringRepairStopped(t, al, id, before.Generation)
	if stopped.ExecutionID == nil || *stopped.ExecutionID != *before.ExecutionID {
		t.Fatalf("SETUP: stopped landing lost the selected execution: before=%+v after=%+v", before.ExecutionID, stopped.ExecutionID)
	}
	return stopped
}

// g2HeldMessagingChild replaces W6's hand-written running state with real
// Launch/Dispatch. Only the model boundary is held; admission and Stop are real.
func g2HeldMessagingChild(t *testing.T, al *AgentLoop, provider *parkedProvider, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID,
		Task: "do delegated work", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("SETUP: Launch(%s): %v", callID, err)
	}
	if err := al.GetSessionLifecycleStore().Mutate(res.SessionID, func(rec *session.LifecycleRecord) error {
		rec.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
		return nil
	}); err != nil {
		t.Fatalf("SETUP: reporting target: %v", err)
	}
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning || dispatched.Generation != res.Generation {
		t.Fatalf("SETUP: Dispatch(%s) = %+v, %v", callID, dispatched, err)
	}
	adr093WaitForEntered(t, provider, 30*time.Second)
	rec := adr093Load(t, al, res.SessionID)
	claim := al.tsExecutionClaim(al.getActiveTurnState(res.SessionID), res.SessionID)
	if rec.ExecutionID == nil || !claim.matches(rec) || !al.steerAdmission().hasExecutionReservation(claim) {
		t.Fatalf("SETUP: messaging child lacks its real admitted execution: record=%+v claim=%+v", rec.ExecutionID, claim)
	}
	return rec
}
