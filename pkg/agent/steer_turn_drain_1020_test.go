// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for issue #1020: a steered child must drain steering
// queued after runTurn's final internal dequeue. BoundaryFinalReply is the
// deterministic boundary: runDispatchedSteeredTurn invokes it synchronously
// after runTurn returns and before disposing the child's result.
package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const (
	steerTurnDrainWakeText1020      = "ISSUE-1020-LATE-UPWARD-WAKE"
	steerTurnDrainMessageText1020   = "ISSUE-1020-LATE-DELEGATE-STEER"
	steerTurnDrainWakeMessageID1020 = "issue-1020-wake-message"
)

type steerTurnDrainProvider1020 struct {
	mu       sync.Mutex
	requests [][]providers.Message
}

func (p *steerTurnDrainProvider1020) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	call := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()

	if call > 1 {
		return nil, fmt.Errorf("issue 1020 provider received unexpected call %d", call+1)
	}
	return &providers.LLMResponse{
		Content:      fmt.Sprintf("issue 1020 response %d", call+1),
		FinishReason: "stop",
	}, nil
}

func (p *steerTurnDrainProvider1020) GetDefaultModel() string { return "issue-1020-drain" }

func (p *steerTurnDrainProvider1020) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		requests[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return requests
}

type steerTurnDrainObserver1020 struct {
	once  sync.Once
	hook  func(sessionID string) error
	mu    sync.Mutex
	err   error
	calls int
	runs  int
}

func (o *steerTurnDrainObserver1020) Observe(boundary steer.Boundary, sessionID string, _ steer.Audience) {
	if boundary != steer.BoundaryFinalReply {
		return
	}
	o.mu.Lock()
	o.calls++
	o.mu.Unlock()
	o.once.Do(func() {
		if o.hook == nil {
			return
		}
		err := o.hook(sessionID)
		o.mu.Lock()
		o.err = err
		o.runs++
		o.mu.Unlock()
	})
}

func (o *steerTurnDrainObserver1020) result() (calls, runs int, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls, o.runs, o.err
}

type steerTurnDrainDeliverer1020 struct {
	once      sync.Once
	delivered chan struct{}
}

func newSteerTurnDrainDeliverer1020() *steerTurnDrainDeliverer1020 {
	return &steerTurnDrainDeliverer1020{delivered: make(chan struct{})}
}

func (d *steerTurnDrainDeliverer1020) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	d.once.Do(func() { close(d.delivered) })
	return steer.Delivery{MessageID: event.ChildSessionID + ":1:final", Outcome: steer.DeliveryWoke}, nil
}

func launchSteeredTurnDrainChild1020(
	t *testing.T,
	al *AgentLoop,
	provider *steerTurnDrainProvider1020,
	deliverer *steerTurnDrainDeliverer1020,
	hook func(sessionID string) error,
) string {
	t.Helper()

	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider
	observer := &steerTurnDrainObserver1020{hook: hook}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), observer, deliverer)

	parentID := newTestSteeringSession(t, al, "ws-1")
	launched, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "exercise the issue 1020 post-turn drain",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "issue-1020"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if dispatched.State != steer.DispatchRunning {
		t.Fatalf("Dispatch state = %q, want %q", dispatched.State, steer.DispatchRunning)
	}

	select {
	case <-deliverer.delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the steered child to reach upward delivery")
	}
	boundaryCalls, hookRuns, hookErr := observer.result()
	if hookErr != nil {
		t.Fatalf("BoundaryFinalReply steering injection: %v", hookErr)
	}
	if boundaryCalls == 0 {
		t.Fatal("BoundaryFinalReply was not observed")
	}
	if hook != nil && hookRuns != 1 {
		t.Fatalf("BoundaryFinalReply steering injection count = %d, want exactly 1", hookRuns)
	}
	return launched.SessionID
}

func TestSteeredTurnDrain1020_LateWakeContinuesChildWithoutRestart(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, func(sessionID string) error {
		return al.EnqueueSteeringWake(
			sessionID,
			testDefaultAgentID,
			sessionID,
			steerTurnDrainWakeMessageID1020,
			providers.Message{Role: "user", Content: steerTurnDrainWakeText1020},
		)
	})

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Errorf("child provider request count = %d, want 2: initial turn plus late-wake continuation", len(requests))
	} else if got := countMessagesContaining(requests[1], steerTurnDrainWakeText1020); got != 1 {
		t.Errorf("late wake occurrences in continuation request = %d, want exactly 1", got)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering count after child delivery = %d, want 0", got)
	}

	entries, err := al.GetSessionStore().ReadTranscript(childID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	consumed := 0
	for _, entry := range entries {
		if entry.Content == "consumed "+steerTurnDrainWakeMessageID1020 {
			consumed++
		}
	}
	if consumed != 1 {
		t.Errorf("wake consumed-marker count = %d, want exactly 1", consumed)
	}
}

func TestSteeredTurnDrain1020_LateDelegateSteerContinuesChildWithoutRestart(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, func(sessionID string) error {
		_, err := al.EnqueueSteeringMessage(
			sessionID,
			testDefaultAgentID,
			providers.Message{Role: "user", Content: steerTurnDrainMessageText1020},
			"issue-1020-late-delegate-steer",
		)
		return err
	})

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Errorf("child provider request count = %d, want 2: initial turn plus late-steer continuation", len(requests))
	} else if got := countMessagesContaining(requests[1], steerTurnDrainMessageText1020); got != 1 {
		t.Errorf("late delegate-steer occurrences in continuation request = %d, want exactly 1", got)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering count after child delivery = %d, want 0", got)
	}
}

func TestSteeredTurnDrain1020_EmptyQueueAddsNoContinuation(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, nil)

	if got := len(provider.Requests()); got != 1 {
		t.Errorf("child provider request count = %d, want exactly 1 when the post-turn queue is empty", got)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering count after empty-queue child delivery = %d, want 0", got)
	}
}
