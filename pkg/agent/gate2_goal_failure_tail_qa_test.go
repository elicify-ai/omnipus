package agent

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// F7 / architect A3. Oracle: amended control-plane ADR D-F, D2 and T27:
// "The goal tail claims only the execution that produced it, never a
// replacement." An obsolete failed producer must cause ZERO subtree effects,
// not merely lose its final commit. The current-producer case prevents a fix
// that disables genuine failed-parent cascades altogether.
// Real goal_claim, admission, immutable handles, Judge dispatch, StopSession,
// lifecycle/goal/transcript/inbox and disposal stay real. Only providers are
// held or made unavailable. No production hook or shortened retry is installed.
func TestQAGate2GoalFailureTail_ObsoleteProducerCannotStopReplacementHelpers(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "current_producer_stops_its_own_helper_control"
		if replacement {
			name = "obsolete_producer_leaves_replacement_helper_running"
		}
		t.Run(name, func(t *testing.T) {
			f := newG5GoalTailFixture(t, "qa2-f7-"+name)
			work := f.goalWork(t)
			// The original owner's disposal already joined in the fixture. Add
			// the third provider boundary before ANY replacement is admitted.
			if replacement {
				f.worker.answers = append(f.worker.answers, "the replacement helper's answer")
				f.worker.release = append(f.worker.release, make(chan struct{}))
			}
			judgeEntered, releaseJudge, tailDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			openJudge := func() { releaseOnce.Do(func() { close(releaseJudge) }) }
			judge := &fakeJudgeProvider{chatFn: func(call int) (*providers.LLMResponse, error) {
				if call == 1 {
					close(judgeEntered)
					<-releaseJudge
				}
				// A real provider-edge refusal, not a fabricated completion error.
				// It is unavailable without transient in-round retry. The real
				// three-attempt goal re-drive and its 5/15 s waits are preserved.
				return nil, providers.ErrProviderNeedsSignIn
			}}
			f.judge.Provider = judge
			go func() { f.al.dispatchDeferredGoalAdjudication(work); close(tailDone) }()
			t.Cleanup(func() {
				openJudge()
				select {
				case <-tailDone:
				case <-time.After(40 * time.Second):
					t.Error("Judge failure tail did not finish after its provider was released")
				}
			})
			select {
			case <-judgeEntered:
			case <-time.After(10 * time.Second):
				t.Fatal("SETUP: original producing claim never reached the real Judge provider")
			}

			parent := f.original
			if replacement {
				dispatched, err := NewSteerLauncher(f.al).Dispatch(context.Background(), parent.SessionID, parent.Generation)
				if err != nil || dispatched.State != steer.DispatchRunning {
					t.Fatalf("SETUP real replacement Dispatch = %+v/%v, want running", dispatched, err)
				}
				r1AwaitProvider(t, f.worker, 1)
				parent = qa2ReopenedLifecycleRecord(t, f.al, parent.SessionID)
				if parent.Generation != f.original.Generation || parent.ExecutionID == nil || reflect.DeepEqual(parent.ExecutionID, f.original.ExecutionID) {
					t.Fatalf("SETUP replacement = %+v, want same generation and a genuinely fresh run", parent)
				}
				qa2JoinHeldExecutionOnCleanup(t, f.al, parent, func() { f.worker.open(1) })
			}
			launched, err := NewSteerLauncher(f.al).Launch(context.Background(), steer.LaunchRequest{
				SteeringSessionID: parent.SessionID, TargetAgentID: "native-agent", Task: "do the current parent's helper work",
				Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "qa2-f7-helper-" + name},
			})
			if err != nil {
				t.Fatalf("SETUP real helper Launch: %v", err)
			}
			dispatched, err := NewSteerLauncher(f.al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
			if err != nil || dispatched.State != steer.DispatchRunning {
				t.Fatalf("SETUP real helper Dispatch = %+v/%v, want running", dispatched, err)
			}
			childIndex := 1
			if replacement {
				childIndex = 2
			}
			r1AwaitProvider(t, f.worker, childIndex)
			child := qa2ReopenedLifecycleRecord(t, f.al, launched.SessionID)
			childHandle := f.al.getActiveTurnState(child.SessionID)
			qa2JoinHeldExecutionOnCleanup(t, f.al, child, func() { f.worker.open(childIndex) })
			if child.State != session.LifecycleRunning || child.Stop != nil || child.StopNote != nil || child.SteeringSessionID() != parent.SessionID {
				t.Fatalf("SETUP current parent's helper = %+v, want live and unfenced under the real parent edge", child)
			}

			openJudge()
			select {
			case <-tailDone:
			case <-time.After(40 * time.Second):
				t.Fatal("Judge failure tail did not finish its real bounded re-drive")
			}
			if calls := judge.callCount(); calls != 3 {
				t.Fatalf("SETUP unavailable Judge provider calls = %d, want all three real goal-tail attempts", calls)
			}
			gotParent := qa2ReopenedLifecycleRecord(t, f.al, parent.SessionID)
			gotChild := qa2ReopenedLifecycleRecord(t, f.al, child.SessionID)
			if !replacement {
				if gotParent.State != session.LifecycleFailed || gotParent.FinalDelivery == nil || gotParent.FinalDelivery.CommitID != f.original.ExecutionID.RunID || !strings.Contains(gotParent.FailedReason, "judge unavailable") {
					t.Errorf("current producer failure = %+v, want its own failed commit naming the unavailable Judge", gotParent)
				}
				qa2AwaitDisposed(t, childHandle.opts.executionDisposition.done, "current failed producer's stopped helper")
				gotChild = qa2ReopenedLifecycleRecord(t, f.al, child.SessionID)
				if gotChild.State != session.LifecycleStopped || gotChild.Stop != nil || gotChild.StopNote == nil || gotChild.StopNote.Cause != session.StopCauseCascade || gotChild.StopEffect == nil || gotChild.StopEffect.Target.RunID != child.ExecutionID.RunID || gotChild.StopEffect.Target.BootSeq != child.ExecutionID.BootSeq {
					t.Errorf("current producer's helper = %+v, want landed stopped/cascade targeting its actual producing run (D6 positive control)", gotChild)
				}
				return
			}
			if gotParent.State != session.LifecycleRunning || gotParent.FinalDelivery != nil || !reflect.DeepEqual(gotParent.ExecutionID, parent.ExecutionID) {
				t.Errorf("F7: obsolete tail committed against B: %+v, want unchanged running replacement without old final", gotParent)
			}
			if gotChild.State != session.LifecycleRunning || gotChild.Stop != nil || gotChild.StopNote != nil || gotChild.StopEffect != nil || !reflect.DeepEqual(gotChild.ExecutionID, child.ExecutionID) {
				t.Errorf("F7: obsolete failed goal tail stopped B's helper: state=%s stop=%+v note=%+v effect=%+v; want running under its original execution with NO Stop", gotChild.State, gotChild.Stop, gotChild.StopNote, gotChild.StopEffect)
			}
			if len(qaReceiptReadLines(t, f.al, child.SessionID)) != 0 {
				t.Error("F7: obsolete producer accepted a helper Stop control; stale final refusal must occur BEFORE every subtree effect")
			}
			messages, _, _, err := f.inbox.Drain(f.parentID, parent.SessionID, "", 10)
			if err != nil || len(messages) != 0 {
				t.Errorf("F7: old failed tail published to its parent: messages=%d error=%v, want no old final", len(messages), err)
			}
		})
	}
}

func qa2ReopenedLifecycleRecord(t *testing.T, al *AgentLoop, id string) *session.LifecycleRecord {
	t.Helper()
	fresh := session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir())
	rec, err := fresh.Load(id)
	if err != nil {
		t.Fatalf("reopen actual owning lifecycle for %s: %v", id, err)
	}
	return rec
}

func qa2JoinHeldExecutionOnCleanup(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord, release func()) {
	t.Helper()
	ts := al.getActiveTurnState(rec.SessionID)
	if ts == nil || ts.opts.executionDisposition == nil || al.tsExecutionClaim(ts, rec.SessionID) != al.executionClaimFor(rec) {
		t.Fatalf("SETUP admitted execution %s has no matching producing handle/disposal barrier", rec.SessionID)
	}
	done := ts.opts.executionDisposition.done
	t.Cleanup(func() {
		release()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("admitted execution %s did not retire after provider release", rec.SessionID)
		}
	})
}
