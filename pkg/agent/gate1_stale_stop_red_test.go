package agent

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// A forward-only normal Stop dependency: the concrete canceller still accepts
// and stamps the real Stop. Only callback scheduling is delayed; its original
// StopSession closure, context, result and error are forwarded unchanged.
// No production test hook or substitute receipt/queue implementation is used.
type gate1StopCallbackBarrier struct {
	real *SteerCanceller
	gate *t27GatedCancel
}

func (b *gate1StopCallbackBarrier) StopTurnsWithCause(ctx context.Context, id string, by steer.Principal, tree bool, cause session.StopCause, effect GenerationCancelFunc) (steer.CancelReport, error) {
	b.gate.realTurn = effect
	return b.real.StopTurnsWithCause(ctx, id, by, tree, cause, b.gate.cancelTurn)
}

func (b *gate1StopCallbackBarrier) ContinueAcceptedStop(ctx context.Context, intent session.UnfinishedStopIntent, effect GenerationCancelFunc) (steer.CancelReport, error) {
	return b.real.ContinueAcceptedStop(ctx, intent, effect)
}

// F1/A1: ADR-20260928 D2/D4/D5/T27, amended 2026-10-06.
// Older undelivered A is superseded by S; later fresh B is NOT. Use genuine
// queued admissions behind a real provider-held sibling, so neither A nor B
// can be consumed by a model while the specified Stop interleaving is forced.
func TestGate1Stop_OldCallbackPreservesNewerSteer(t *testing.T) {
	f := newQAReceiptFixture(t)
	f.al.GetConfig().Performance.MaxParallelAgents = 1
	_, blocker := r1AdmitChild(t, f.al, f.parentID, "gate1-stop-slot-blocker", "blocker finished")
	dispatched, err := NewSteerLauncher(f.al).Dispatch(context.Background(), f.child.SessionID, f.child.Generation)
	if err != nil || dispatched.State != steer.DispatchQueued {
		t.Fatalf("SETUP: original real admission=%+v err=%v, want queued behind actual sibling", dispatched, err)
	}
	f.child = rootReopenedRecord(t, f.al, f.child.SessionID)
	if f.child.ExecutionID == nil || f.child.ExecutionID.RunID == "" {
		t.Fatal("SETUP: original queued admission has no durable execution identity")
	}
	ctx := tools.WithTranscriptSessionID(context.Background(), f.parentID)
	tool := delegateToolFor(t, f.al)
	accept := func(text, correlation string) qaReceiptLine {
		t.Helper()
		result := tool.Execute(ctx, map[string]any{"action": "steer", "session_id": f.child.SessionID, "text": text, "correlation_id": correlation})
		if result == nil || result.IsError {
			t.Fatalf("SETUP: actual delegate steer refused: %+v", result)
		}
		return qaReceiptAcceptedSteer(t, f.al, f.child.SessionID, text)
	}
	first := accept("A was accepted before Stop S.", "gate1-before-stop")
	gate := newT27GatedCancel(nil)
	barrier := &gate1StopCallbackBarrier{real: f.al.steerCanceller(), gate: gate}
	stopDone := make(chan error, 1)
	go func() {
		result, stopErr := f.al.StopSession(context.Background(), StopRequest{
			SessionID: f.child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "gate1-owner"},
			Channel: "webchat", Canceller: barrier,
		})
		if stopErr == nil {
			stopErr = result.RootErr
		}
		stopDone <- stopErr
	}()
	t.Cleanup(func() { gate.openGate(); <-stopDone })
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: real StopSession callback never reached its scheduling barrier")
	}
	selected := rootReopenedRecord(t, f.al, f.child.SessionID)
	if selected.Stop == nil || selected.StopNote == nil || selected.StopEffect == nil || selected.StopEffect.Target.RunID != f.child.ExecutionID.RunID {
		t.Fatalf("SETUP: gated S has no real durable fence/sequence/selected admission: %+v", selected)
	}
	stopSeq := int64(selected.StopNote.Seq)
	if first.Seq >= stopSeq {
		t.Fatalf("SETUP: A seq=%d must precede actual S seq=%d", first.Seq, stopSeq)
	}
	// The real never-ran owner settles the exact original accepted selection.
	// No test stamps a stopped/running lifecycle or fabricates an identity.
	if _, landErr := f.al.SteerGenerationCancel(gate.acceptanceContext(t), f.child.SessionID, f.child.Generation); landErr != nil {
		t.Fatalf("SETUP: selected queued Stop settlement: %v", landErr)
	}
	t27StopLanded(t, f.al.GetSessionLifecycleStore(), f.child.SessionID, f.child.Generation)
	generation, resumeErr := f.al.steerCanceller().Revive(context.Background(), f.child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "gate1-owner"})
	if resumeErr != nil || generation != f.child.Generation {
		t.Fatalf("SETUP: actual same-generation resume=%d err=%v", generation, resumeErr)
	}
	replacement, dispatchErr := NewSteerLauncher(f.al).Dispatch(context.Background(), f.child.SessionID, generation)
	if dispatchErr != nil || replacement.State != steer.DispatchQueued {
		t.Fatalf("SETUP: real replacement admission=%+v err=%v, want queued", replacement, dispatchErr)
	}
	fresh := rootReopenedRecord(t, f.al, f.child.SessionID)
	if fresh.ExecutionID == nil || fresh.ExecutionID.RunID == f.child.ExecutionID.RunID || fresh.ExecutionID.BootSeq != f.child.ExecutionID.BootSeq {
		t.Fatalf("SETUP: replacement is not distinct same-generation/same-boot admission: %+v", fresh.ExecutionID)
	}
	second := accept("B was accepted after S landed and the session resumed.", "gate1-after-resume")
	if second.Seq <= stopSeq {
		t.Fatalf("SETUP: B seq=%d must follow actual S seq=%d", second.Seq, stopSeq)
	}
	beforeCallback := rootReopenedRecord(t, f.al, f.child.SessionID)
	gate.openGate()
	select {
	case stopErr := <-stopDone:
		// Keep the joined result available for cleanup, even after an assertion.
		stopDone <- stopErr
		if stopErr != nil {
			t.Errorf("old StopSession returned an unexpected error: %v", stopErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: old StopSession callback did not finish after release")
	}
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))
	old := latest[first.ControlID]
	if old.State != "superseded" || old.SupersededBySeq == nil || *old.SupersededBySeq != stopSeq || old.Reason != "stop" {
		t.Errorf("F1: A receipt=%+v, want superseded by exactly old S seq=%d", old, stopSeq)
	}
	newer := latest[second.ControlID]
	if newer.State != "queued" || newer.SupersededBySeq != nil {
		t.Errorf("F1: newer B receipt state=%q superseded_by_seq=%v, want queued/no superseder (A=%d < S=%d < B=%d)", newer.State, newer.SupersededBySeq, first.Seq, stopSeq, second.Seq)
	}
	f.al.steering.mu.Lock()
	items := append([]steeringQueueItem(nil), f.al.steering.queues[f.child.SessionID]...)
	f.al.steering.mu.Unlock()
	if len(items) != 1 || items[0].steerControlID != second.ControlID || items[0].correlationID != "gate1-after-resume" || !reflect.DeepEqual(items[0].message, providers.Message{Role: "user", Content: second.Text}) {
		t.Errorf("F1: queue after old S callback=%+v, want depth 1 with original newer B identity/text/correlation", items)
	}
	if after := rootReopenedRecord(t, f.al, f.child.SessionID); !reflect.DeepEqual(after, beforeCallback) {
		t.Errorf("F1: old Stop callback rewrote replacement admission: before=%+v after=%+v", beforeCallback, after)
	}
	// Cleanup is a genuine NEW Stop, not a queue deletion or assertion bypass.
	if _, cleanupErr := f.al.StopSession(context.Background(), StopRequest{SessionID: f.child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "gate1-cleanup"}}); cleanupErr != nil {
		t.Errorf("new Stop cleanup: %v", cleanupErr)
	}
	blocker.open(0)
	joinGoalFixtureRuns(t, f.al)
}
