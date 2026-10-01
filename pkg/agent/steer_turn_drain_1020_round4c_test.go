package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// A steer inside the durable commit window belongs to the finishing hand-off,
// not to the now-empty ordinary queue. It must be accepted even after the
// final queue-empty recheck, and its original message/receipt must survive.
func TestSteeredTurnDrain1020Round4c_CommitWindowAcceptsLateSteer(t *testing.T) {
	queue := newSteeringQueue(SteeringAll)
	const scope = "late-steer-child"
	late := steeringQueueItem{
		message:       providers.Message{Role: "user", Content: "late instruction"},
		correlationID: "late-steer-receipt",
	}
	var got []steeringQueueItem
	started, terminal, err := queue.runTerminalTransitionWithFinishing(scope,
		func() error { return nil },
		func() (bool, error) {
			finishing, enqueueErr := queue.pushItemScopeChecked(scope, late, nil)
			if enqueueErr != nil {
				t.Errorf("commit-window enqueue refused late steer: %v", enqueueErr)
			}
			if !finishing {
				t.Error("commit-window enqueue was not classified as post-finish")
			}
			return true, nil
		},
		func(items []steeringQueueItem) { got = items },
	)
	if err != nil || !started || !terminal {
		t.Fatalf("terminal transition = (started=%v, terminal=%v, err=%v), want (true, true, nil)", started, terminal, err)
	}
	if len(got) != 1 {
		t.Fatalf("finishing hand-off contains %d items, want exactly the accepted late steer", len(got))
	}
	if !reflect.DeepEqual(got[0].message, late.message) || got[0].correlationID != late.correlationID {
		t.Errorf("finishing hand-off = (%+v, %q), want (%+v, %q)", got[0].message, got[0].correlationID, late.message, late.correlationID)
	}
}

// TestSteeredTurnDrain1020Round4_OuterExhaustionReportsEachQueuedSteerToParent
// pins the *outer* retry budget. Two successful same-generation continuations
// leave room for a third completion attempt; during its failed delivery an
// independent terminal transition prevents the last two accepted steers from
// being drained. Both must be abandoned individually, not left in memory.
func TestSteeredTurnDrain1020Round4_OuterExhaustionReportsEachQueuedSteerToParent(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider

	var childID string
	var deliveries int
	const tailPrefix = "round4c-tail-"
	deliverer := &round3FailingDeliverer{onDeliver: func() error {
		deliveries++
		count := 1
		if deliveries == continueDrainMaxRetries {
			count = 2
		}
		for i := 1; i <= count; i++ {
			id := fmt.Sprintf("round4c-continue-%d", deliveries)
			if deliveries == continueDrainMaxRetries {
				id = fmt.Sprintf("%s%d", tailPrefix, i)
			}
			if _, err := al.EnqueueSteeringMessage(childID, testDefaultAgentID,
				providers.Message{Role: "user", Content: id}, id); err != nil {
				return fmt.Errorf("enqueue steer %s: %w", id, err)
			}
		}
		if deliveries == continueDrainMaxRetries {
			// Model a separate terminal writer (for example, a Stop cascade)
			// landing after both steers were accepted in the finishing window.
			// drainSteeredTurn then cannot run them, so only the OUTER budget's
			// exhaustion path can dispose of them.
			if err := al.GetSessionLifecycleStore().Mutate(childID, func(rec *session.LifecycleRecord) error {
				// U1 collapsed cancellation into the single non-terminal
				// LifecycleStopped, and the D2/CRIT-001 invariant requires any
				// record landing it to carry a non-nil StopNote in the SAME
				// mutation (persistLocked rejects otherwise). StopCauseCascade
				// (not StopCauseStop) models this as the child being reached as
				// a DESCENDANT of an independent ancestor's cascade — this
				// write is deliberately not the cascade's own direct target,
				// matching "a separate terminal writer ... landing after" in
				// the comment above.
				rec.State = session.LifecycleStopped
				rec.StopNote = &session.StopNote{
					At:    time.Now().UTC(),
					By:    session.StopActorSystem,
					Seq:   uint64(rec.Generation),
					Cause: session.StopCauseCascade,
				}
				return nil
			}); err != nil {
				return fmt.Errorf("concurrent terminal transition: %w", err)
			}
		}
		return nil
	}}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), nil, deliverer)
	child := launchQueuedSteeredTurnDrainChild1020(t, al, testDefaultAgentID, "round4c outer exhaustion")
	childID = child.SessionID
	snapshot := setLifecycleState1020(t, al, childID, session.LifecycleRunning)
	ts, err := al.reconstructSteeredTurn(snapshot, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}

	al.disposeSteeredTurnResult(ts, snapshot, snapshot.Generation, turnResult{finalContent: "seed answer"}, nil)
	if deliveries != continueDrainMaxRetries {
		t.Fatalf("delivery attempts = %d, want exactly outer budget %d", deliveries, continueDrainMaxRetries)
	}
	if requests := provider.Requests(); len(requests) != continueDrainMaxRetries-1 {
		t.Fatalf("continuation requests = %d, want %d before the independent terminal write", len(requests), continueDrainMaxRetries-1)
	}
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.State != session.LifecycleStopped || rec.Generation != snapshot.Generation {
		t.Fatalf("independent transition = %s generation %d, want stopped generation %d", rec.State, rec.Generation, snapshot.Generation)
	}
	if pending := al.pendingSteeringCountForScope(childID); pending != 0 {
		t.Errorf("outer exhaustion left %d queued steers, want 0: both must be reported, not stranded", pending)
	}

	childEntries, err := al.GetSessionStore().ReadTranscript(childID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	childErrors := 0
	for _, entry := range childEntries {
		if entry.Status == "error" && strings.Contains(entry.Content, "queued follow-up message could not be processed") {
			childErrors++
		}
	}
	if childErrors != 2 {
		t.Errorf("child abandonment error entries = %d, want exactly one for each of the two tail steers", childErrors)
	}

	parentID := snapshot.SteeringSessionID()
	parentEntries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent): %v", err)
	}
	counts := map[string]int{tailPrefix + "1": 0, tailPrefix + "2": 0}
	parentErrors := 0
	for _, entry := range parentEntries {
		frame := entry.SubagentMessage
		if frame == nil || frame.Kind != "error" || frame.ChildSessionId == nil || *frame.ChildSessionId != childID {
			continue
		}
		parentErrors++
		if frame.SessionId != parentID || frame.Text == nil || !strings.Contains(*frame.Text, "could not be processed") {
			t.Errorf("parent error frame has wrong recipient or explanation: %+v", frame)
			continue
		}
		for id := range counts {
			if strings.Contains(*frame.Text, id) {
				counts[id]++
			}
		}
	}
	if parentErrors != 2 || counts[tailPrefix+"1"] != 1 || counts[tailPrefix+"2"] != 1 {
		t.Errorf("parent error frames = %d; steer identities = %v, want two distinct failures, one per tail steer", parentErrors, counts)
	}
}
