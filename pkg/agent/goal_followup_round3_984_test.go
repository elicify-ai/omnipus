package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestGoal984_StoredNotWokenHandbackFallsBackToOneVerdictWake(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "call-f1-stored-not-woken")
	inbox := al.GetMessageInboxStore()
	const goalID = "goal-f1-stored-not-woken"
	verdictID := appendTestMetVerdict(t, inbox, parentID, child.SessionID, goalID, 1)

	wireTerminalReportDeliverer(al, &recordingUpwardDeliverer{delivery: steer.Delivery{
		MessageID: fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation),
		Outcome:   steer.DeliveryStoredNotWoken,
	}})
	woke, err := al.deliverSteeredCompletion(context.Background(), child, steer.OutcomeFinalAnswer,
		session.LifecycleCompleted, "finished", "")
	if err != nil {
		t.Fatalf("deliverSteeredCompletion: %v", err)
	}
	if woke {
		t.Fatal("stored-not-woken hand-back reported a wake-producing delivery")
	}
	assertUnackedMessageIDs(t, inbox, parentID, child.SessionID, verdictID)

	wireSteerCompletionDeps(t, al)
	var mu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})
	if err := al.wakeMetVerdictEntry(child.SessionID, goalID, 1); err != nil {
		t.Fatalf("wakeMetVerdictEntry: %v", err)
	}
	mu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	mu.Unlock()
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != verdictID {
		t.Fatalf("parent wakes = %v, want exactly verdict %q", gotWakeIDs, verdictID)
	}
	assertUnackedMessageIDs(t, inbox, parentID, child.SessionID)
}

func TestGoal984_WakeMetVerdictLeavesEntryUnackedWhenParentStopped(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "call-f1-parent-stopped")
	inbox := al.GetMessageInboxStore()
	const goalID = "goal-f1-parent-stopped"
	verdictID := appendTestMetVerdict(t, inbox, parentID, child.SessionID, goalID, 1)

	lifecycle := al.GetSessionLifecycleStore()
	if err := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.Stop = &session.Stop{
			At: time.Now().UTC(), Generation: rec.Generation,
			By: session.Principal{Kind: session.PrincipalKindHuman, ID: "operator"},
		}
		return nil
	}); err != nil {
		t.Fatalf("stop parent: %v", err)
	}
	if err := al.wakeMetVerdictEntry(child.SessionID, goalID, 1); err == nil {
		t.Fatal("stored-not-woken fallback reported success for a stopped parent")
	}
	assertUnackedMessageIDs(t, inbox, parentID, child.SessionID, verdictID)
}

func TestGoal984_PausedPreflightCallerDoesNotRedeliverAfterCompletion(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "call-f4-paused-preflight")

	firstArrived := make(chan struct{})
	secondArrived := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	var hookMu sync.Mutex
	hookCalls := 0
	completeBeforeDeliveryTestHook = func(sessionID string) {
		if sessionID != child.SessionID {
			return
		}
		hookMu.Lock()
		hookCalls++
		call := hookCalls
		hookMu.Unlock()
		switch call {
		case 1:
			close(firstArrived)
			<-releaseFirst
		case 2:
			close(secondArrived)
			<-releaseSecond
		}
	}
	t.Cleanup(func() { completeBeforeDeliveryTestHook = nil })

	var wakeMu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		wakeMu.Lock()
		defer wakeMu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "first"}, nil)
	}()
	<-firstArrived
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "stale second"}, nil)
	}()
	<-secondArrived
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first completion: %v", err)
	}
	close(releaseSecond)
	if err := <-secondDone; err != nil {
		t.Fatalf("paused completion: %v", err)
	}

	wakeMu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	wakeMu.Unlock()
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != wantID {
		t.Fatalf("parent wakes = %v, want one final wake %q", gotWakeIDs, wantID)
	}
}

func TestGoal984_StaleGenerationCompletionCannotOccupyRevivedFinal(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	childGen1 := launchRunningChild(t, al, parentID, "call-f4-stale-generation")

	staleArrived := make(chan struct{})
	releaseStale := make(chan struct{})
	completeBeforeDeliveryTestHook = func(sessionID string) {
		if sessionID == childGen1.SessionID {
			close(staleArrived)
			<-releaseStale
		}
	}
	t.Cleanup(func() { completeBeforeDeliveryTestHook = nil })

	staleDone := make(chan error, 1)
	go func() {
		staleDone <- al.completeSteeredTurn(context.Background(), childGen1, turnResult{finalContent: "stale generation one"}, nil)
	}()
	<-staleArrived
	lifecycle := al.GetSessionLifecycleStore()
	if err := lifecycle.Mutate(childGen1.SessionID, func(rec *session.LifecycleRecord) error {
		rec.Stop = &session.Stop{
			At: time.Now().UTC(), Generation: rec.Generation,
			By: session.Principal{Kind: session.PrincipalKindHuman, ID: "operator"},
		}
		return nil
	}); err != nil {
		t.Fatalf("stop generation one: %v", err)
	}
	gen2, err := NewSteerCanceller(lifecycle).Revive(context.Background(), childGen1.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	close(releaseStale)
	if staleErr := <-staleDone; staleErr != nil {
		t.Fatalf("stale completion: %v", staleErr)
	}

	entries, err := al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent): %v", err)
	}
	wantGen2ID := fmt.Sprintf("%s:%d:final", childGen1.SessionID, gen2)
	for _, entry := range entries {
		if entry.Message != nil && messageIDOf(*entry.Message) == wantGen2ID {
			t.Fatalf("stale generation-one caller occupied revived final id %q", wantGen2ID)
		}
	}

	completeBeforeDeliveryTestHook = nil
	childGen2, err := lifecycle.Load(childGen1.SessionID)
	if err != nil {
		t.Fatalf("Load generation two: %v", err)
	}
	if completeErr := al.completeSteeredTurn(context.Background(), childGen2, turnResult{finalContent: "legitimate generation two"}, nil); completeErr != nil {
		t.Fatalf("generation-two completion: %v", completeErr)
	}
	entries, err = al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent after generation two): %v", err)
	}
	var gen2Handbacks int
	for _, entry := range entries {
		if entry.Message == nil || messageIDOf(*entry.Message) != wantGen2ID {
			continue
		}
		handback, decodeErr := entry.Message.AsSessionMessageHandback()
		if decodeErr != nil {
			t.Fatalf("AsSessionMessageHandback: %v", decodeErr)
		}
		gen2Handbacks++
		if handback.ResultSoFar != "legitimate generation two" {
			t.Fatalf("generation-two hand-back result = %q, want legitimate result", handback.ResultSoFar)
		}
	}
	if gen2Handbacks != 1 {
		t.Fatalf("generation-two hand-backs = %d, want 1", gen2Handbacks)
	}
}

func TestGoal984_CompletionFlightsRemovedOnEveryExit(t *testing.T) {
	tests := []struct {
		name     string
		complete func(*AgentLoop, *session.LifecycleRecord) (bool, error)
	}{
		{name: "success", complete: func(*AgentLoop, *session.LifecycleRecord) (bool, error) { return true, nil }},
		{name: "stop", complete: func(al *AgentLoop, rec *session.LifecycleRecord) (bool, error) {
			err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(cur *session.LifecycleRecord) error {
				cur.Stop = &session.Stop{At: time.Now().UTC(), Generation: cur.Generation,
					By: session.Principal{Kind: session.PrincipalKindHuman, ID: "operator"}}
				return nil
			})
			return true, err
		}},
		{name: "revive", complete: func(al *AgentLoop, rec *session.LifecycleRecord) (bool, error) {
			if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(cur *session.LifecycleRecord) error {
				cur.Stop = &session.Stop{At: time.Now().UTC(), Generation: cur.Generation,
					By: session.Principal{Kind: session.PrincipalKindHuman, ID: "operator"}}
				return nil
			}); err != nil {
				return true, err
			}
			_, err := NewSteerCanceller(al.GetSessionLifecycleStore()).Revive(context.Background(), rec.SessionID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
			return true, err
		}},
		{name: "terminal write failure", complete: func(*AgentLoop, *session.LifecycleRecord) (bool, error) {
			return true, errors.New("injected terminal write failure")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			parentID := newTestSteeringSession(t, al, "ws-1")
			rec := launchRunningChild(t, al, parentID, "call-f4-cleanup-"+tt.name)
			_, _ = al.runSteeredCompletionOnce(rec, func() (bool, error) { return tt.complete(al, rec) })
			key := steeredCompletionFlightKey{loop: al, sessionID: rec.SessionID, generation: rec.Generation}
			if _, retained := steeredCompletionFlights.Load(key); retained {
				steeredCompletionFlights.Delete(key)
				t.Fatalf("completion flight retained after %s", tt.name)
			}
		})
	}
}

func appendTestMetVerdict(t *testing.T, inbox *session.MessageInboxStore, parentID, childID, goalID string, round int) string {
	t.Helper()
	id := goalVerdictUpwardMessageID(goalID, round)
	var message generated.SessionMessage
	if err := message.FromSessionMessageGoalStatus(generated.SessionMessageGoalStatus{
		Kind: generated.SessionMessageGoalStatusKindGoalStatus, MessageId: id, SessionId: childID,
		SenderIdentity: "judge", CreatedAt: time.Now().UTC(), Depth: 1, GoalId: goalID,
		Condition: generated.SessionMessageGoalStatusConditionMet,
		Direction: generated.SessionMessageGoalStatusDirectionSessionToParent,
	}); err != nil {
		t.Fatalf("encode goal verdict: %v", err)
	}
	if _, err := inbox.Append(parentID, message); err != nil {
		t.Fatalf("append goal verdict: %v", err)
	}
	return id
}

func assertUnackedMessageIDs(t *testing.T, inbox *session.MessageInboxStore, ownerKey, childID string, want ...string) {
	t.Helper()
	messages, _, _, err := inbox.Drain(ownerKey, childID, "", 32)
	if err != nil {
		t.Fatalf("Drain(%q): %v", ownerKey, err)
	}
	got := make([]string, 0, len(messages))
	for _, message := range messages {
		got = append(got, messageIDOf(message))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unacknowledged message ids = %v, want %v", got, want)
	}
}
