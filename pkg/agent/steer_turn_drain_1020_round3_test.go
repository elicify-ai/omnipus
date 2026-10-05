// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Issue #1020 finishing handoff, reconciled against R1-RULING.md and frozen
// ADR-20260928 D2/D4/D5. The real terminal/outbox commit, not a provider answer
// or the finishing flag, separates same-generation continuation from a next
// round. Publication failure preserves a committed final. Upward wakes retain
// their real durable identity and may not undo Stop. Historical test symbols
// remain for failure-map continuity; per-test comments identify superseded
// generation/final assumptions. No conservation/ordering/dedup oracle is relaxed.
package agent

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
)

// round3FailingDeliverer is S2's fault injector: onDeliver runs (as a side
// effect, synchronously, inside the real terminal-transition's prepare()
// window) BEFORE Deliver reports a hard failure — modeling "terminal
// delivery FAILS" (spec item 6) with a late steer landing during the same
// attempt.
type round3FailingDeliverer struct {
	onDeliver func() error
}

func (d *round3FailingDeliverer) Deliver(_ context.Context, _ steer.UpwardEvent) (steer.Delivery, error) {
	if d.onDeliver != nil {
		if err := d.onDeliver(); err != nil {
			return steer.Delivery{}, err
		}
	}
	return steer.Delivery{}, fmt.Errorf("round3 s2: simulated terminal delivery failure")
}

// TestSteeredTurnDrain1020Round3_LateSteerDuringFinishAcceptedThenRevivesNextGenerationFinal
// covers S1: a steer arrives during terminal delivery, after the empty
// check. It must be ACCEPTED (item 2), the generation-g final must still
// carry the pre-steer answer unchanged (item 3), and the held post-finish
// steer must revive the child to generation g+1 (item 4).
// Historical symbol retained for failure-map continuity. R1 supersedes this
// fixture's G+1/two-finals oracle: acceptance inside prepare is still pre-commit.
func TestSteeredTurnDrain1020Round3_LateSteerDuringFinishAcceptedThenRevivesNextGenerationFinal(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child, provider := r1AdmitChild(t, al, rootID, "round3-s1-revive", "pre-steer answer", "Mock response")
	var enqueueErr error
	r1InjectBeforeCommit(t, al, child, func() {
		_, enqueueErr = al.EnqueueSteeringMessage(child.SessionID, testDefaultAgentID,
			providers.Message{Role: "user", Content: "ROUND3-S1-LATE-STEER"}, "round3-s1-late-steer")
	})
	provider.open(0)
	r1AwaitProvider(t, provider, 1)
	if enqueueErr != nil {
		t.Fatalf("pre-commit finishing steer was refused: %v", enqueueErr)
	}
	r1AssertNoFinal(t, al, child)
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider calls=%d, want initial + one continuation", len(requests))
	}
	assertSteerRevivalInput1020(t, requests[1:], []string{"ROUND3-S1-LATE-STEER"})
	provider.open(1)
	joinGoalFixtureRuns(t, al)
	r1RequireSameGenerationFinal(t, al, child, "Mock response")
}

// R1 supersedes this historical name's same-generation oracle: publication
// failure AFTER a commit leaves G owed; accepted input must run independently
// in G+1. A failure here is conservation loss, not an internal sentinel error.
func TestSteeredTurnDrain1020Round3_DeliveryFailureConsumesLateSteerAsSameGenerationContinuation(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-round3-failed-publication")
	child, provider := r1AdmitChild(t, al, parentID, "round3-s2-real-owner", "first answer", "late steer answer")
	var enqueueErr error
	var status EnqueueStatus
	var firstCommitted *session.LifecycleRecord
	var once sync.Once
	deliverer := &round3FailingDeliverer{onDeliver: func() error {
		once.Do(func() {
			firstCommitted = rootReopenedRecord(t, al, child.SessionID)
			_, status, enqueueErr = al.EnqueueSteeringMessageWithStatus(child.SessionID, testDefaultAgentID,
				providers.Message{Role: "user", Content: "ROUND3-S2-LATE-STEER"}, "round3-s2-late-steer")
		})
		return nil
	}}
	al.SetSteerAudienceDeps(al.getSteerAudienceResolver(), nil, deliverer)
	provider.openAll()
	joinGoalFixtureRuns(t, al)
	if firstCommitted == nil || firstCommitted.State != session.LifecycleCompleted || firstCommitted.FinalDelivery == nil {
		t.Fatalf("failed-delivery fixture did not observe actual G commit: %+v", firstCommitted)
	}
	if enqueueErr != nil || status != EnqueueStatusPostFinish {
		t.Fatalf("failed-publication late steer status=%v error=%v, want accepted", status, enqueueErr)
	}
	old, progress, _, _, err := al.GetSessionLifecycleStore().CommittedFinalDelivery(child.SessionID, child.Generation, child.ExecutionID.RunID)
	if err != nil || !reflect.DeepEqual(old, *firstCommitted.FinalDelivery) || progress.InboxAppended {
		t.Fatalf("failed publication changed/lost committed G or falsely marked delivered: old=%+v progress=%+v error=%v", old, progress, err)
	}
	rec := rootReopenedRecord(t, al, child.SessionID)
	if rec.Generation != child.Generation+1 || rec.State != session.LifecycleCompleted {
		t.Fatalf("accepted post-commit steer stranded after failed publication: G=%d state=%q pending=%d provider calls=%d; want one independently completed G=%d, old G final still owed", rec.Generation, rec.State, al.pendingSteeringCountForScope(child.SessionID), len(provider.Requests()), child.Generation+1)
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider calls=%d, want seed and exactly one accepted-input round", len(requests))
	}
	assertSteerRevivalInput1020(t, requests[1:], []string{"ROUND3-S2-LATE-STEER"})
	if pending := al.pendingSteeringCountForScope(child.SessionID); pending != 0 {
		t.Errorf("stranded accepted input=%d, want zero", pending)
	}
}

// R1: a pre-commit upward wake is ordinary input, not permission to manufacture
// G+1. Preserve the real durable entry, its exact text, consumer and dedup.
func TestSteeredTurnDrain1020Round3_DescendantWakeDuringOwnTerminalDeliveryNeverStranded(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child, provider := r1AdmitChild(t, al, rootID, "round3-s3-wake", "pre-wake answer", "wake processed answer")
	inbox := al.GetMessageInboxStore()
	const descendantID = "round3-s3-grandchild"
	const text = "ROUND3-S3-DESCENDANT-WAKE"
	var message generated.SessionMessage
	if err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: descendantID + ":1:final", SessionId: descendantID, CreatedAt: time.Now().UTC(),
		Depth: 2, SenderIdentity: testDefaultAgentID, Mode: generated.SessionMessageHandbackModeFinal,
		ResultSoFar: text, Artifacts: []string{}, OpenQuestions: []string{},
	}); err != nil {
		t.Fatalf("FromSessionMessageHandback(descendant): %v", err)
	}
	appended, err := inbox.Append(child.SessionID, message)
	if err != nil {
		t.Fatalf("Append(real descendant inbox entry): %v", err)
	}
	var enqueueErr error
	var unackedAtBoundary []generated.SessionMessage
	var boundaryReadErr error
	r1InjectBeforeCommit(t, al, child, func() {
		enqueueErr = al.EnqueueSteeringWake(child.SessionID, testDefaultAgentID, child.SessionID,
			appended.MessageID, providers.Message{Role: "user", Content: text})
		unackedAtBoundary, _, _, boundaryReadErr = inbox.Drain(child.SessionID, descendantID, "", 10)
	})
	provider.open(0)
	r1AwaitProvider(t, provider, 1)
	if enqueueErr != nil {
		t.Fatalf("durable pre-commit wake refused: %v", enqueueErr)
	}
	if boundaryReadErr != nil || len(unackedAtBoundary) != 1 || messageIDOf(unackedAtBoundary[0]) != appended.MessageID {
		t.Fatalf("real unacknowledged entry at pre-commit boundary=%+v error=%v, want %q", unackedAtBoundary, boundaryReadErr, appended.MessageID)
	}
	handback, err := unackedAtBoundary[0].AsSessionMessageHandback()
	if err != nil || handback.ResultSoFar != text {
		t.Fatalf("durable descendant text=%q error=%v, want exact %q", handback.ResultSoFar, err, text)
	}
	r1AssertNoFinal(t, al, child)
	assertSteerRevivalInput1020(t, provider.Requests()[1:], []string{text})
	provider.open(1)
	joinGoalFixtureRuns(t, al)
	r1RequireSameGenerationFinal(t, al, child, "wake processed answer")
	acked, err := deliverEntryIsAcked(inbox, child.SessionID, descendantID, appended.MessageID)
	if err != nil || !acked {
		t.Fatalf("real wake consumer did not acknowledge %q: acked=%v error=%v", appended.MessageID, acked, err)
	}
	remaining, _, more, err := inbox.Drain(child.SessionID, "", "", 10)
	if err != nil || more || len(remaining) != 0 {
		t.Fatalf("unconsumed descendant entries=%d more=%v error=%v, want zero", len(remaining), more, err)
	}
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	consumed := 0
	for _, e := range entries {
		if e.ID == "consumed-"+appended.MessageID && e.Content == "consumed "+appended.MessageID {
			consumed++
		}
	}
	if consumed != 1 {
		t.Errorf("durable wake consumed markers=%d, want exactly one", consumed)
	}
	if len(provider.Requests()) != 2 {
		t.Errorf("wake duplicated provider work: calls=%d, want initial plus one wake", len(provider.Requests()))
	}
}

// R1 Stop-race row: accepted upward input on a stopped recipient remains
// unacknowledged until a legitimate consumer; it must neither undo Stop nor
// fabricate consumption. Then drive that explicit consumer and prove once-only.
func TestSteeredTurnDrain1020Round3_StopRaceDuringWakeDeliveryNeverStrandsAcceptedWake(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child, provider := r1AdmitChild(t, al, rootID, "round3-s4-stop-race", "before Stop", "after legitimate resume", "after legitimate resume")
	inbox := al.GetMessageInboxStore()
	const descendantID = "round3-s4-grandchild"
	const wakeText = "ROUND3-S4-DURABLE-WAKE"
	var message generated.SessionMessage
	if err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: descendantID + ":1:final", SessionId: descendantID, CreatedAt: time.Now().UTC(),
		Depth: 2, SenderIdentity: testDefaultAgentID, Mode: generated.SessionMessageHandbackModeFinal,
		ResultSoFar: wakeText, Artifacts: []string{}, OpenQuestions: []string{},
	}); err != nil {
		t.Fatalf("FromSessionMessageHandback(descendant): %v", err)
	}
	appended, err := inbox.Append(child.SessionID, message)
	if err != nil {
		t.Fatalf("Append(child inbox): %v", err)
	}
	var stopErr, enqueueErr error
	var stopResult StopResult
	r1InjectBeforeCommit(t, al, child, func() {
		stopResult, stopErr = al.StopSession(context.Background(), StopRequest{
			SessionID: child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"},
			HooksFor: func(string) CancelHooks { return CancelHooks{} },
		})
		enqueueErr = al.EnqueueSteeringWake(child.SessionID, testDefaultAgentID, child.SessionID,
			appended.MessageID, providers.Message{Role: "user", Content: wakeText})
	})
	provider.openAll()
	joinGoalFixtureRuns(t, al)
	if stopErr != nil || stopResult.RootErr != nil || len(stopResult.Report.Unreachable) != 0 || !stopResult.Fired {
		t.Fatalf("real owner Stop did not succeed: result=%+v error=%v", stopResult, stopErr)
	}
	if enqueueErr != nil {
		t.Fatalf("durable finishing-window wake was not accepted: %v", enqueueErr)
	}
	stopped := rootReopenedRecord(t, al, child.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.Terminal() || stopped.Stop != nil || stopped.Generation != child.Generation || stopped.FinalDelivery != nil {
		t.Fatalf("accepted wake undid Stop or published candidate final: %+v", stopped)
	}
	unacked, _, more, err := inbox.Drain(child.SessionID, descendantID, "", 10)
	if err != nil || more || len(unacked) != 1 || messageIDOf(unacked[0]) != appended.MessageID {
		t.Fatalf("stopped recipient lost its one durable UNACKED descendant entry: entries=%+v more=%v error=%v", unacked, more, err)
	}
	acked, err := deliverEntryIsAcked(inbox, child.SessionID, descendantID, appended.MessageID)
	if err != nil || acked {
		t.Fatalf("stopped recipient falsely acknowledged entry: acked=%v error=%v", acked, err)
	}
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(stopped child): %v", err)
	}
	for _, e := range entries {
		if e.ID == "consumed-"+appended.MessageID || e.Content == "consumed "+appended.MessageID {
			t.Error("stopped recipient forged a consumed marker without running")
		}
	}
	if len(provider.Requests()) != 1 {
		t.Fatalf("wake automatically ran the stopped recipient: provider calls=%d, want original only", len(provider.Requests()))
	}
	resumed, err := al.ReviveStoppedSession(context.Background(), child.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"}, "Resume explicitly and consume the saved descendant entry")
	if err != nil || !resumed {
		t.Fatalf("legitimate explicit consumer: resumed=%v error=%v", resumed, err)
	}
	joinGoalFixtureRuns(t, al)
	after := rootReopenedRecord(t, al, child.SessionID)
	if after.Generation != child.Generation || after.State != session.LifecycleCompleted || after.ExecutionID == nil || after.ExecutionID.RunID == child.ExecutionID.RunID {
		t.Fatalf("legitimate consumer did not finish at same G with a fresh execution: %+v", after)
	}
	assertSteerRevivalInput1020(t, provider.Requests()[1:], []string{wakeText})
	acked, err = deliverEntryIsAcked(inbox, child.SessionID, descendantID, appended.MessageID)
	if err != nil || !acked {
		t.Fatalf("legitimate consumer did not ACK: acked=%v error=%v", acked, err)
	}
	entries, err = al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(after resume): %v", err)
	}
	consumed := 0
	for _, e := range entries {
		if e.ID == "consumed-"+appended.MessageID && e.Content == "consumed "+appended.MessageID {
			consumed++
		}
	}
	if consumed != 1 || al.pendingSteeringCountForScope(child.SessionID) != 0 {
		t.Fatalf("accepted wake duplicated/stranded: consumed=%d queued=%d, want exactly one/zero", consumed, al.pendingSteeringCountForScope(child.SessionID))
	}
}

// TestSteeredTurnDrain1020Round3_NoLateSteerControlExactlyOneFinalNoRevival
// is S5's positive control: with no late steer at all, completion must
// still behave exactly as before this round's spec — exactly one final,
// no revival, the generation left unchanged.
func TestSteeredTurnDrain1020Round3_NoLateSteerControlExactlyOneFinalNoRevival(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round3-s5-control")
	startGeneration := child.Generation

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "only answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}

	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if !rec.Terminal() {
		t.Fatalf("child state = %q, want terminal with no late steer to hold it open", rec.State)
	}
	if rec.Generation != startGeneration {
		t.Errorf("child generation = %d, want unchanged %d: no late steer means no revival", rec.Generation, startGeneration)
	}

	msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(rootID, child.SessionID, "", 10)
	if drainErr != nil {
		t.Fatalf("Drain(root): %v", drainErr)
	}
	if len(msgs) != 1 {
		t.Fatalf("delivered message count = %d, want exactly 1 final and no revival", len(msgs))
	}
	handback, herr := msgs[0].AsSessionMessageHandback()
	if herr != nil || handback.ResultSoFar != "only answer" {
		t.Errorf("final content = %q (decode err %v), want %q", handback.ResultSoFar, herr, "only answer")
	}
}
