package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// B1, D4 ledger-first: hold the REAL queue's push lock, not a fake sink.
// The registered delegate tool must have persisted acceptance while its push
// is still blocked. Steer uses the actual authorized tool; respond is #1198.
func TestQADelegateSteerReceipt_AcceptanceDurableBeforePush(t *testing.T) {
	for _, action := range []string{"steer"} { // Respond verb is explicitly deferred to #1198.
		t.Run(action, func(t *testing.T) {
			f := newQAReceiptFixture(t)
			tool := delegateToolFor(t, f.al)
			text := "Exact accepted " + action + " instruction."
			ctx := tools.WithTranscriptSessionID(context.Background(), f.parentID)
			done := make(chan *tools.ToolResult, 1)
			f.al.steering.mu.Lock()
			locked := true
			defer func() {
				if locked {
					f.al.steering.mu.Unlock()
					<-done
				}
			}()
			go func() {
				done <- tool.Execute(ctx, map[string]any{
					"action": action, "session_id": f.child.SessionID,
					"text": text, "correlation_id": "qa-" + action + "-correlation",
				})
			}()
			deadline := time.Now().Add(10 * time.Second) // Deadlock bound, not a timing oracle.
			var lines []qaReceiptLine
			for len(lines) == 0 && time.Now().Before(deadline) {
				lines = qaReceiptReadLines(t, f.al, f.child.SessionID)
				if len(lines) == 0 {
					time.Sleep(time.Millisecond)
				}
			}
			if len(lines) != 1 {
				f.al.steering.mu.Unlock()
				locked = false
				result := <-done
				if result == nil || result.IsError {
					t.Fatalf("SETUP delegate %s was refused, not accepted: %+v", action, result)
				}
				t.Fatalf("D4 accepted %s control had no durable intent before blocked push: physical lines=%d, want exactly 1 queued steer; accepted result=%q", action, len(lines), result.ForLLM)
			}
			line := lines[0]
			if line.Verb != action || line.Text != text || line.State != "queued" || line.ControlID == "" || line.AcceptedAt.IsZero() || line.Actor == "" || line.Seq != 1 || line.Generation != f.child.Generation || len(f.al.steering.queues[f.child.SessionID]) != 0 {
				t.Fatalf("D4S-02 first %s acceptance/blocked queue = %+v/depth %d, want its OWN verb/exact text/queued/nonempty identity/time/seq 1/current generation/empty queue", action, line, len(f.al.steering.queues[f.child.SessionID]))
			}
			f.al.steering.mu.Unlock()
			locked = false
			result := <-done
			if result == nil || result.IsError {
				t.Fatalf("real authorized delegate %s refused accepted control: %+v", action, result)
			}
			f.al.steering.mu.Lock()
			items := append([]steeringQueueItem(nil), f.al.steering.queues[f.child.SessionID]...)
			f.al.steering.mu.Unlock()
			if len(items) != 1 || !reflect.DeepEqual(items[0].message, providers.Message{Role: "user", Content: text}) {
				t.Fatalf("accepted control queue item = %+v, want exactly its original message", items)
			}
			if id := qaReceiptItemControlID(items[0]); id == "" || id != line.ControlID {
				t.Fatalf("queue steerControlID = %q, want nonempty durable ledger identity %q", id, line.ControlID)
			}
			f.al.abandonSteeredQueuedSteering(nil, f.child.SessionID, errors.New("fixture completed acceptance observation"), 1)
			qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{line.ControlID: "superseded"})
		})
	}
}

// B2, D4 delivered is a durable transcript fact, not handed-to-turn.
func TestQADelegateSteerReceipt_DeliveredRequiresReopenedTranscript(t *testing.T) {
	f := newQAReceiptFixture(t)
	const text = "Persist this exact delegate instruction before claiming delivered."
	line := qaReceiptEnqueue(t, f, text, "qa-durable-steer")
	_, _, messages, ids, err := f.al.dequeueSteeringItemsForScopeWithFallbackResult(f.child.SessionID)
	if err != nil || !reflect.DeepEqual(messages, []providers.Message{{Role: "user", Content: text}}) || !reflect.DeepEqual(ids, []string{"qa-durable-steer"}) {
		t.Fatalf("real consume = %+v/%q/%v, want exact instruction/correlation/nil", messages, ids, err)
	}
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{line.ControlID: "delivered"})
	entries := qaReceiptReopenTranscript(t, f.store, f.child.SessionID)
	matched := 0
	for _, entry := range entries {
		if entry.Content == text {
			matched++
			if entry.Role != "user" || entry.AgentID != testDefaultAgentID || !strings.Contains(entry.ID, line.ControlID) || entry.Timestamp.IsZero() {
				t.Errorf("D4 durable instruction lost exact input/control identity: entry=%+v control=%q", entry, line.ControlID)
			}
		}
	}
	if matched != 1 {
		t.Fatalf("D4 receipt says delivered but reopened transcript has %d exact instruction entries, want exactly 1 (control %s)", matched, line.ControlID)
	}
	if depth := f.al.pendingSteeringCountForScope(f.child.SessionID); depth != 0 {
		t.Errorf("delivered steer still owns queue slot: depth=%d, want 0", depth)
	}
}

// B3/D5: a newer Stop resolves every accepted pending steer, never an orphan.
func TestQADelegateSteerReceipt_NewerStopSupersedesPending(t *testing.T) {
	f := newQAReceiptFixture(t)
	first := qaReceiptEnqueue(t, f, "First pending instruction.", "qa-stop-first")
	second := qaReceiptEnqueue(t, f, "Second pending instruction.", "qa-stop-second")
	res, err := f.al.StopSession(context.Background(), StopRequest{
		SessionID: f.child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-stop-owner"}, Channel: "webchat",
	})
	if err != nil || res.RootErr != nil || len(res.Report.Unreachable) != 0 {
		t.Fatalf("normal one Stop failed: %+v err=%v", res, err)
	}
	cur, err := f.al.GetSessionLifecycleStore().Load(f.child.SessionID)
	if err != nil || cur.State != session.LifecycleStopped || cur.Stop != nil {
		t.Fatalf("one Stop did not land: current=%+v err=%v", cur, err)
	}
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{first.ControlID: "superseded", second.ControlID: "superseded"})
	if depth := f.al.pendingSteeringCountForScope(f.child.SessionID); depth != 0 {
		t.Errorf("Stop left pending delegate inputs on a consumerless queue: %d", depth)
	}
	lines := qaReceiptReadLines(t, f.al, f.child.SessionID)
	for _, id := range []string{first.ControlID, second.ControlID} {
		line := qaReceiptLatest(lines)[id]
		if line.Reason != "stop" || line.SupersededBySeq == nil || cur.StopNote == nil || *line.SupersededBySeq != int64(cur.StopNote.Seq) {
			t.Errorf("D4S-01 supersession lost exact reason/superseding sequence: receipt=%+v stop note=%+v, want reason stop and newer Stop seq", line, cur.StopNote)
		}
		transitions := 0
		for _, history := range lines {
			if history.ControlID == id && history.State == "superseded" {
				transitions++
			}
		}
		if transitions != 1 {
			t.Errorf("D4S-01 superseded appends for %q = %d, want exactly 1", id, transitions)
		}
	}
}

func TestQADelegateSteerReceipt_RefusedPushSupersedesAcceptance(t *testing.T) {
	f := newQAReceiptFixture(t)
	for i := 0; i < MaxQueueSize; i++ { // Existing queue boundary; fill exactly, then push max+1.
		if err := f.al.steering.pushScope(f.child.SessionID, providers.Message{Role: "user", Content: "human filler"}); err != nil {
			t.Fatalf("SETUP queue boundary slot %d: %v", i, err)
		}
	}
	const text = "Refused push still needs a final receipt."
	_, err := f.al.EnqueueSteeringMessage(f.child.SessionID, testDefaultAgentID, providers.Message{Role: "user", Content: text}, "qa-full")
	if err == nil || err.Error() != "steering queue is full" {
		t.Fatalf("full push error = %v, want exact steering queue is full", err)
	}
	line := qaReceiptAcceptedSteer(t, f.al, f.child.SessionID, text)
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{line.ControlID: "superseded"})
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))[line.ControlID]
	if latest.Reason != "refused: steering queue is full" || f.al.pendingSteeringCountForScope(f.child.SessionID) != MaxQueueSize {
		t.Errorf("refusal lost cause or changed existing queue: latest=%+v depth=%d", latest, f.al.pendingSteeringCountForScope(f.child.SessionID))
	}
}

func TestQADelegateSteerReceipt_AbandonedDrainSupersedesAcceptance(t *testing.T) {
	f := newQAReceiptFixture(t)
	line := qaReceiptEnqueue(t, f, "An abandoned instruction cannot remain queued.", "qa-abandoned")
	f.al.abandonSteeredQueuedSteering(nil, f.child.SessionID, errors.New("fixture pre-turn failure"), 3)
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{line.ControlID: "superseded"})
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))[line.ControlID]
	if latest.Reason == "" || f.al.pendingSteeringCountForScope(f.child.SessionID) != 0 {
		t.Errorf("abandonment did not resolve text/queue: latest=%+v depth=%d", latest, f.al.pendingSteeringCountForScope(f.child.SessionID))
	}
}

// Human-chat ledgering and the separate respond verb are explicitly deferred
// by the founder to #1198. Their uncommitted draft is saved with the receipts;
// neither a skip nor a passing exclusion test substitutes for that deferral.
