package agent

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func (f *uatD1Fixture) assertMetHandback(t *testing.T, child *session.LifecycleRecord, wantWorkerCalls int) {
	t.Helper()
	// Await the actual persisted settlement, including the verdict ack that
	// finishes the met tail, not merely the Judge provider's early response.
	// Then join real admission and active requests, without a production hook.
	f.awaitMetSettlement(t, child)
	f.awaitParentAndDrain(t)
	got := uatD1ReadLifecycle(t, f.al, child.SessionID)
	if got.State != session.LifecycleCompleted || !got.Terminal() || got.Generation != child.Generation || got.FinalDelivery == nil {
		t.Fatalf("D1 user expectation: helper completed with one committed handback; actual state=%q terminal=%v generation=%d outbox=%+v, want completed in generation %d", got.State, got.Terminal(), got.Generation, got.FinalDelivery, child.Generation)
	}
	g := uatD1ReadGoal(t, child.GoalRef)
	if g.State != generated.GoalStateMet || g.Round != 1 || g.LatestClaim == nil || g.LatestClaim.Status != generated.GoalLatestClaimStatusMet || g.LatestClaim.Evidence != uatD1Answer || g.LatestVerdict == nil || !g.LatestVerdict.Met {
		t.Fatalf("D1 retained goal must contain the real met claim and verdict after exactly one Judge round: %+v", g)
	}
	if len(g.Criteria) != 1 || g.Criteria[0].Text != uatD1Criterion || g.Criteria[0].Status != task.CritMet || len(g.DoD) != 1 || g.DoD[0].Text != uatD1DoD || g.DoD[0].Status != task.CritMet {
		t.Fatalf("D1 retained projected criterion/DoD must be the submitted items, both met: %+v/%+v", g.Criteria, g.DoD)
	}
	msgs := f.inboxMessages(t)
	var kinds []string
	for _, message := range msgs {
		class, err := session.ClassifySessionMessage(message)
		if err != nil {
			t.Fatalf("D1 persisted inbox envelope is invalid: %v", err)
		}
		kinds = append(kinds, class.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"goal_status", "handback"}) {
		t.Fatalf("D1 parent persisted outcomes=%v, want exactly [goal_status handback], no missing or duplicated final", kinds)
	}
	verdict, err := msgs[0].AsSessionMessageGoalStatus()
	if err != nil || verdict.Condition != generated.SessionMessageGoalStatusConditionMet || verdict.Evidence == nil || len(*verdict.Evidence) != 2 || verdict.SessionId != child.SessionID || string(verdict.Direction) != "session_to_parent" || (verdict.ParentSessionId != nil && *verdict.ParentSessionId != f.parentID) {
		t.Fatalf("D1 parent met verdict=%+v error=%v, want met with evidence for both submitted items from the actual child", verdict, err)
	}
	handback, err := msgs[1].AsSessionMessageHandback()
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if err != nil || handback.MessageId != wantID || handback.Mode != generated.SessionMessageHandbackModeFinal || handback.ResultSoFar != uatD1Answer || handback.SessionId != child.SessionID || string(handback.Direction) != "child_to_parent" || (handback.ParentSessionId != nil && *handback.ParentSessionId != f.parentID) {
		t.Fatalf("D1 parent handback=%+v error=%v, want exact final %q id=%q from child to its direct parent", handback, err, uatD1Answer, wantID)
	}
	rawFinal, err := msgs[1].MarshalJSON()
	if err != nil || !bytes.Equal(rawFinal, got.FinalDelivery.Payload) || got.ExecutionID == nil || got.FinalDelivery.CommitID != got.ExecutionID.RunID {
		t.Fatalf("D1 parent received bytes must equal the final committed by real execution: outbox=%+v error=%v", got.FinalDelivery, err)
	}
	if len(f.worker.Requests()) != wantWorkerCalls || f.judge.callCount() != 1 || !reflect.DeepEqual(f.judge.askedOnCall(1), []string{uatD1Criterion, uatD1DoD}) {
		t.Fatalf("D1 provider trace: worker calls=%d Judge calls=%d asked=%v, want %d/1 and exact submitted criterion+DoD", len(f.worker.Requests()), f.judge.callCount(), f.judge.askedOnCall(1), wantWorkerCalls)
	}
	f.assertParentReceivedOnce(t, uatD1Answer)
	f.assertNoRunningChild(t)
}

func (f *uatD1Fixture) awaitMetSettlement(t *testing.T, child *session.LifecycleRecord) {
	t.Helper()
	deadline := time.NewTimer(uatD1Watchdog)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond) // wait for discrete durable events, not elapsed-time correctness
	defer tick.Stop()
	for {
		entries, err := session.NewMessageInboxStore(filepath.Join(f.home, "session_messages")).Entries(f.parentID)
		if err != nil {
			t.Fatalf("read actual met settlement: %v", err)
		}
		verdictID := ""
		acked := make(map[string]bool)
		for _, entry := range entries {
			if entry.Kind == session.InboxEntryAck {
				for _, id := range entry.AckedIDs {
					acked[id] = true
				}
			} else if entry.Message != nil {
				class, err := session.ClassifySessionMessage(*entry.Message)
				if err != nil {
					t.Fatalf("classify met settlement: %v", err)
				}
				if class.Kind == "goal_status" {
					status, err := entry.Message.AsSessionMessageGoalStatus()
					if err != nil {
						t.Fatalf("decode actual met settlement: %v", err)
					}
					verdictID = status.MessageId
				}
			}
		}
		// Reading Entries acquires the store's own lock after the ack append.
		// The goal met tail has no further persistent action after that ack
		// (finishMetGoal -> return; redriveGoalAdjudication -> return).
		if verdictID != "" && acked[verdictID] {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			got := uatD1ReadLifecycle(t, f.al, child.SessionID)
			g := uatD1ReadGoal(t, child.GoalRef)
			t.Fatalf("D1 met claim never settled into a completed helper and acknowledged parent verdict: child_state=%q goal_state=%q worker_calls=%d Judge_calls=%d parent_messages=%d reason=%q", got.State, g.State, len(f.worker.Requests()), f.judge.callCount(), len(f.inboxMessages(t)), g.LatestReason)
		}
	}
}
