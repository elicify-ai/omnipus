// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// steerCloseCountingProvider answers every model call with one fixed reply
// and counts the calls. Mutex-guarded: the worker goroutine calls Chat while
// the test reads the count.
type steerCloseCountingProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *steerCloseCountingProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "steer-close reply"}, nil
}

func (p *steerCloseCountingProvider) GetDefaultModel() string { return "steer-close" }

func (p *steerCloseCountingProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestSessionWorker_MessageAfterFinalDrainIsNotStranded pins the worker's
// one-message-one-turn guarantee across the tail of a turn: a user message
// that reaches the worker AFTER processTurn's last steering-drain check (the
// turn's reply is already on its way out, but processTurn has not returned)
// must still run. Before the fix, inTurn was still true in that window, so
// enqueue pushed the message into the steering queue — whose only consumer,
// the drain loop, had already finished — and the message was never run
// (CI: TestSinceCursor_CursorAtExactBoundary timed out waiting for turn 2).
//
// The window is held open without a test seam: the outbound bus is filled to
// capacity, so the turn's final reply publish (which runs after the drain)
// blocks until the test drains the bus.
func TestSessionWorker_MessageAfterFinalDrainIsNotStranded(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	sessionID := newTestSteeringSession(t, al, "")
	provider := &steerCloseCountingProvider{}
	adr093UseProvider(t, al, provider)

	// Fill the outbound bus so the first turn's final publish blocks.
	filler := 0
	for al.bus.TryPublishOutbound(bus.OutboundMessage{Channel: "filler", ChatID: "filler", Content: "x"}) {
		filler++
	}
	if filler == 0 {
		t.Fatal("setup: could not fill the outbound bus")
	}

	scope := "agent:" + testDefaultAgentID + ":session:" + sessionID
	w := newSessionWorker(scope, al, func() {})
	go w.runLoop()
	t.Cleanup(func() {
		w.cancel()
		<-w.done
	})

	if !w.enqueue(adr093HumanMessage("first question", sessionID)) {
		t.Fatal("first message was not accepted")
	}
	deadline := time.Now().Add(10 * time.Second)
	for provider.callCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the first turn never reached the model")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The model call has returned; give processTurn time to pass its
	// steering drain and block on the full outbound bus.
	time.Sleep(500 * time.Millisecond)

	if !w.enqueue(adr093HumanMessage("second question", sessionID)) {
		t.Fatal("second message was not accepted")
	}

	// Unblock the publish and keep the bus drained.
	stopDrain := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-al.bus.OutboundChan():
			case <-stopDrain:
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(stopDrain)
		<-drained
	})

	deadline = time.Now().Add(10 * time.Second)
	for provider.callCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the second message never ran (model calls = %d, want 2): a message that arrives after the turn's final steering drain was stranded in the steering queue instead of starting the next turn", provider.callCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := al.pendingSteeringCountForScope(scope); n != 0 {
		t.Fatalf("steering queue for %s still holds %d message(s) with no turn to drain them", scope, n)
	}
}
